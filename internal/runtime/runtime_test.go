package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestProviderArgumentsEnforceSafety(t *testing.T) {
	codex := NewCodex("fake").(*commandRuntime)
	args := codex.arguments(RunRequest{Permission: domain.PermissionReadOnly}, "/tmp/schema")
	if !containsPair(args, "--sandbox", "read-only") || !containsPair(args, "--output-schema", "/tmp/schema") {
		t.Fatalf("unsafe Codex args: %v", args)
	}
	args = codex.arguments(RunRequest{Permission: domain.PermissionWorktree, Model: "gpt-5.6-sol", ReasoningEffort: "high"}, "/tmp/schema")
	if !containsPair(args, "--model", "gpt-5.6-sol") || !containsPair(args, "--config", `model_reasoning_effort="high"`) {
		t.Fatalf("Codex model selection missing: %v", args)
	}
	claude := NewClaude("fake").(*commandRuntime)
	args = claude.arguments(RunRequest{Schema: []byte(`{"type":"object"}`), Budget: domain.Budget{MaxTurns: 4, MaxCostCents: 125}}, "")
	if !containsPair(args, "--strict-mcp-config", "--mcp-config") || !reflect.DeepEqual(args[5:7], []string{"--mcp-config", `{"mcpServers":{}}`}) {
		t.Fatalf("MCP not disabled: %v", args)
	}
	if !containsPair(args, "--max-turns", "4") || !containsPair(args, "--max-budget-usd", "1.25") {
		t.Fatalf("budgets missing: %v", args)
	}
	copilot := NewCopilot("fake").(*commandRuntime)
	args = copilot.arguments(RunRequest{Prompt: []byte("fix it"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionWorktree, Model: "gpt-5.3-codex", Commands: [][]string{{"go", "test", "./..."}}}, "")
	if !contains(args, "--sandbox") || contains(args, "--allow-all") || !contains(args, "--available-tools=view,glob,grep,apply_patch,create,edit,bash,powershell") {
		t.Fatalf("unsafe Copilot args: %v", args)
	}
	if !contains(args, "--allow-tool=shell(go test ./...)") || !containsPair(args, "--model", "gpt-5.3-codex") {
		t.Fatalf("Copilot command/model permissions missing: %v", args)
	}
	if !contains(args, "--no-custom-instructions") || !contains(args, "--available-tools=view,glob,grep,apply_patch,create,edit,bash,powershell") {
		t.Fatalf("Copilot context was not isolated: %v", args)
	}
}

var permissions = []domain.Permission{domain.PermissionReadOnly, domain.PermissionWorktree}

func adapter(t *testing.T, spec Spec) *commandRuntime {
	t.Helper()
	built, err := New(spec)
	if err != nil {
		t.Fatal(err)
	}
	return built.(*commandRuntime)
}

// claudeSettings decodes the --settings a Claude Code call was given: JSON
// inline, or the file a call given servers gets.
func claudeSettings(t *testing.T, args []string) map[string]any {
	t.Helper()
	index := indexOf(args, "--settings")
	if index < 0 || index+1 >= len(args) {
		t.Fatalf("no --settings: %v", args)
	}
	data := []byte(args[index+1])
	if !strings.HasPrefix(args[index+1], "{") {
		var err error
		if data, err = os.ReadFile(args[index+1]); err != nil {
			t.Fatalf("read --settings file: %v", err)
		}
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("--settings is not JSON: %v", err)
	}
	return settings
}

func TestClaudeWorkersLeaveTheUsersSetupOut(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/home/dev/.claude-work")
	claude := NewClaude("fake").(*commandRuntime)
	for _, permission := range permissions {
		args := claude.arguments(RunRequest{Schema: []byte(`{"type":"object"}`), Permission: permission}, "")
		for _, flag := range []string{"--disable-slash-commands", "--exclude-dynamic-system-prompt-sections", "--no-session-persistence", "--strict-mcp-config"} {
			if !contains(args, flag) {
				t.Fatalf("%s Claude step is missing %s: %v", permission, flag, args)
			}
		}
		want := map[string]any{
			"disableAllHooks": true, "autoMemoryEnabled": false, "includeGitInstructions": false, "outputStyle": "default",
			"claudeMdExcludes": []any{"/home/dev/.claude-work/CLAUDE.md", "/home/dev/.claude-work/rules/**"},
		}
		if settings := claudeSettings(t, args); !reflect.DeepEqual(settings, want) {
			t.Fatalf("%s Claude settings = %v, want %v", permission, settings, want)
		}
		tools := []string{"Read", "Glob", "Grep", "Bash"}
		if permission == domain.PermissionWorktree {
			tools = append(tools, claudeEditTools...)
		}
		if got := args[indexOf(args, "--tools")+1]; got != strings.Join(tools, ",") {
			t.Fatalf("%s Claude tools = %s, want %s", permission, got, strings.Join(tools, ","))
		}
	}

	// The opt-in brings back the user's settings, skills, and memory; Inkuna
	// still chooses the tools and keeps MCP off.
	own := adapter(t, Spec{Name: domain.RuntimeClaude, UserConfig: true})
	for _, permission := range permissions {
		args := own.arguments(RunRequest{Schema: []byte(`{"type":"object"}`), Permission: permission}, "")
		if contains(args, "--disable-slash-commands") || !contains(args, "--strict-mcp-config") {
			t.Fatalf("%s Claude step with the user's setup: %v", permission, args)
		}
		if settings := claudeSettings(t, args); !reflect.DeepEqual(settings, map[string]any{"includeGitInstructions": false}) {
			t.Fatalf("%s Claude settings with the user's setup = %v", permission, settings)
		}
		if tools := strings.Split(args[indexOf(args, "--tools")+1], ","); !contains(tools, "Skill") || contains(tools, "Agent") || contains(tools, "Task") {
			t.Fatalf("%s Claude tools with the user's setup = %v", permission, tools)
		}
	}
}

func TestCodexWorkersLeaveTheUsersSetupOut(t *testing.T) {
	// A v2 model keeps its subagent tools whatever features.multi_agent
	// says, so the thread limit is what stops it spawning.
	always := []string{
		"features.apps=false", "features.multi_agent=false",
		"features.multi_agent_v2.max_concurrent_threads_per_session=1", "features.multi_agent_v2.wait_agent_enabled=false",
		`features.multi_agent_v2.root_agent_usage_hint_text=""`, `features.multi_agent_v2.multi_agent_mode_hint_text=""`,
		"features.image_generation=false", `web_search="disabled"`,
	}
	skipped := []string{"features.hooks=false", "skills.include_instructions=false"}
	codex := NewCodex("fake").(*commandRuntime)
	own := adapter(t, Spec{Name: "codex-own", Preset: domain.RuntimeCodex, UserConfig: true})
	for _, permission := range permissions {
		args := codex.arguments(RunRequest{Permission: permission}, "/tmp/schema")
		if !contains(args, "--ignore-user-config") || !contains(args, "--ephemeral") || args[len(args)-1] != "-" {
			t.Fatalf("%s Codex step loads the user's config: %v", permission, args)
		}
		// --disable refuses a feature an older Codex does not know.
		if contains(args, "--disable") {
			t.Fatalf("%s Codex step switches a feature off with --disable rather than -c: %v", permission, args)
		}
		for _, value := range append(append([]string{}, always...), skipped...) {
			if !containsPair(args, "--config", value) {
				t.Fatalf("%s Codex step is missing --config %s: %v", permission, value, args)
			}
		}

		args = own.arguments(RunRequest{Permission: permission}, "/tmp/schema")
		if contains(args, "--ignore-user-config") || !contains(args, "--ephemeral") {
			t.Fatalf("%s Codex step with the user's config: %v", permission, args)
		}
		for _, value := range always {
			if !containsPair(args, "--config", value) {
				t.Fatalf("%s Codex step with the user's config is missing --config %s: %v", permission, value, args)
			}
		}
		for _, value := range skipped {
			if containsPair(args, "--config", value) {
				t.Fatalf("%s Codex step with the user's config still sets %s: %v", permission, value, args)
			}
		}
	}
}

func TestCopilotWorkersLeaveTheUsersPluginsAndGitHubServerOut(t *testing.T) {
	copilot := NewCopilot("fake").(*commandRuntime)
	own := adapter(t, Spec{Name: domain.RuntimeCopilot, UserConfig: true})
	for _, permission := range permissions {
		for _, runtime := range []*commandRuntime{copilot, own} {
			if args := runtime.arguments(RunRequest{Prompt: []byte("x"), Permission: permission}, ""); !contains(args, "--disable-builtin-mcps") {
				t.Fatalf("%s Copilot step starts the GitHub MCP server: %v", permission, args)
			}
		}
	}
	if environment := copilot.environment(RunRequest{}); !contains(environment, "COPILOT_PLUGIN_DIR_ONLY=true") {
		t.Fatalf("Copilot loads the user's plugins: %v", environment)
	}
	if environment := own.environment(RunRequest{}); contains(environment, "COPILOT_PLUGIN_DIR_ONLY=true") || !contains(environment, "COPILOT_ALLOW_ALL=false") {
		t.Fatalf("Copilot with the user's setup: %v", environment)
	}
}

func TestCopilotWorkersRunInsideItsSandbox(t *testing.T) {
	t.Setenv("COPILOT_CLI_ENABLED_FEATURE_FLAGS", "EXTENSIONS,AUTO_APPROVAL")
	for _, runtime := range []*commandRuntime{NewCopilot("fake").(*commandRuntime), adapter(t, Spec{Name: domain.RuntimeCopilot, UserConfig: true})} {
		// --sandbox alone is ignored unless the sandbox feature is on.
		if args := runtime.arguments(RunRequest{Prompt: []byte("x"), Permission: domain.PermissionWorktree}, ""); !contains(args, "--sandbox") {
			t.Fatalf("Copilot step without --sandbox: %v", args)
		}
		environment := runtime.environment(RunRequest{})
		if !contains(environment, "COPILOT_CLI_ENABLED_FEATURE_FLAGS=SANDBOX") || contains(environment, "COPILOT_CLI_ENABLED_FEATURE_FLAGS=EXTENSIONS,AUTO_APPROVAL") {
			t.Fatalf("Copilot's sandbox feature is not the only one switched on: %v", environment)
		}
	}
}

func TestCallsWithNoToolsGetNone(t *testing.T) {
	request := RunRequest{Prompt: []byte("x"), Schema: []byte(`{}`), Permission: domain.PermissionReadOnly, NoTools: true}
	claude, codex, copilot := NewClaude("fake").(*commandRuntime), NewCodex("fake").(*commandRuntime), NewCopilot("fake").(*commandRuntime)
	for _, runtime := range []*commandRuntime{claude, adapter(t, Spec{Name: domain.RuntimeClaude, UserConfig: true})} {
		if args := runtime.arguments(request, ""); !containsPair(args, "--tools", "") {
			t.Fatalf("Claude call with no tools still has some: %v", args)
		}
	}
	args := codex.arguments(request, "/tmp/schema")
	if !contains(args, "--skip-git-repo-check") || args[len(args)-1] != "-" {
		t.Fatalf("Codex call with no tools cannot run outside a repository: %v", args)
	}
	for _, value := range []string{"features.shell_tool=false", "features.unified_exec=false", "features.view_image=false", "features.sleep_tool=false", "features.goals=false"} {
		if !containsPair(args, "--config", value) {
			t.Fatalf("Codex call with no tools is missing --config %s: %v", value, args)
		}
	}
	// Copilot reads an empty allowlist as every tool.
	if args := copilot.arguments(request, ""); !contains(args, "--available-tools=none") {
		t.Fatalf("Copilot call with no tools still has some: %v", args)
	}

	request.NoTools = false
	if args := claude.arguments(request, ""); !containsPair(args, "--tools", "Read,Glob,Grep,Bash") {
		t.Fatalf("Claude step lost its tools: %v", args)
	}
	if args := codex.arguments(request, "/tmp/schema"); contains(args, "--skip-git-repo-check") || containsPair(args, "--config", "features.shell_tool=false") {
		t.Fatalf("Codex step lost its shell or its repository check: %v", args)
	}
	if args := copilot.arguments(request, ""); !contains(args, "--available-tools=view,glob,grep") {
		t.Fatalf("Copilot step lost its tools: %v", args)
	}
}

func TestResumableCallsKeepTheirSessionAndLaterCallsContinueIt(t *testing.T) {
	claude, codex, copilot := NewClaude("fake").(*commandRuntime), NewCodex("fake").(*commandRuntime), NewCopilot("fake").(*commandRuntime)
	fresh := RunRequest{Prompt: []byte("x"), Schema: []byte(`{}`), Permission: domain.PermissionWorktree}
	if args := claude.arguments(fresh, ""); !contains(args, "--no-session-persistence") || contains(args, "--resume") {
		t.Fatalf("a Claude call nothing resumes must keep no session: %v", args)
	}
	if args := codex.arguments(fresh, "/tmp/schema"); !contains(args, "--ephemeral") || args[1] == "resume" {
		t.Fatalf("a Codex call nothing resumes must keep no session: %v", args)
	}

	kept := fresh
	kept.Resumable = true
	if args := claude.arguments(kept, ""); contains(args, "--no-session-persistence") {
		t.Fatalf("a resumable Claude call must keep its session: %v", args)
	}
	if args := codex.arguments(kept, "/tmp/schema"); contains(args, "--ephemeral") {
		t.Fatalf("a resumable Codex call must keep its session: %v", args)
	}

	// A resumed call continues the session with this request's own tools,
	// sandbox, and schema.
	resumed := kept
	resumed.Session = "session-1"
	args := claude.arguments(resumed, "")
	if !containsPair(args, "--resume", "session-1") || !containsPair(args, "--tools", "Read,Glob,Grep,Bash,Edit,Write,MultiEdit,NotebookEdit") || !containsPair(args, "--json-schema", "{}") {
		t.Fatalf("resumed Claude call = %v", args)
	}
	args = codex.arguments(resumed, "/tmp/schema")
	if strings.Join(args[:4], " ") != "exec resume session-1 --json" || !containsPair(args, "--config", `sandbox_mode="workspace-write"`) || contains(args, "--sandbox") ||
		!containsPair(args, "--output-schema", "/tmp/schema") || contains(args, "--ephemeral") || args[len(args)-1] != "-" {
		t.Fatalf("resumed Codex call = %v", args)
	}
	resumed.Permission = domain.PermissionReadOnly
	if args := codex.arguments(resumed, "/tmp/schema"); !containsPair(args, "--config", `sandbox_mode="read-only"`) {
		t.Fatalf("a resumed read-only Codex call must stay read-only: %v", args)
	}
	if args := copilot.arguments(resumed, ""); !contains(args, "--resume=session-1") || !contains(args, "--available-tools=view,glob,grep") {
		t.Fatalf("resumed Copilot call = %v", args)
	}

	for _, check := range []struct {
		spec Spec
		want bool
	}{
		{Spec{Name: domain.RuntimeClaude}, true},
		{Spec{Name: domain.RuntimeCodex}, true},
		{Spec{Name: domain.RuntimeCopilot}, true},
		{Spec{Name: "claude-work", Preset: domain.RuntimeClaude}, true},
		{customSpec(), false},
	} {
		if got := adapter(t, check.spec).Capabilities().Resume; got != check.want {
			t.Errorf("%s: Resume = %v, want %v", check.spec.Name, got, check.want)
		}
	}
}

func TestExtractSessionReadsEachRuntimesSessionID(t *testing.T) {
	for _, check := range []struct {
		kind    domain.Runtime
		payload string
		want    string
	}{
		{domain.RuntimeClaude, `{"type":"system","subtype":"init","session_id":"9b5c6e52"}`, "9b5c6e52"},
		{domain.RuntimeClaude, `{"type":"result","subtype":"success","session_id":"9b5c6e52"}`, "9b5c6e52"},
		{domain.RuntimeCodex, `{"type":"thread.started","thread_id":"01a15188"}`, "01a15188"},
		{domain.RuntimeCodex, `{"type":"turn.completed","thread_id":"other"}`, ""},
		{domain.RuntimeCopilot, `{"type":"result","sessionId":"fbbf9c2c","exitCode":0}`, "fbbf9c2c"},
		{domain.RuntimeCopilot, `{"type":"assistant.message","sessionId":"other"}`, ""},
	} {
		var payload map[string]any
		if err := json.Unmarshal([]byte(check.payload), &payload); err != nil {
			t.Fatal(err)
		}
		if got, _ := ExtractSession(check.kind, payload); got != check.want {
			t.Errorf("%s %s: session %q, want %q", check.kind, check.payload, got, check.want)
		}
	}
}

func TestExtractActivityCountsOnlyTheModelAtWork(t *testing.T) {
	// The startup and failure events are the ones each CLI streamed for a
	// call whose model request was refused.
	for _, check := range []struct {
		kind    domain.Runtime
		payload string
		want    bool
	}{
		{domain.RuntimeClaude, `{"type":"system","subtype":"init","session_id":"s"}`, false},
		{domain.RuntimeClaude, `{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 400"}]}}`, false},
		{domain.RuntimeClaude, `{"type":"result","subtype":"error_during_execution","is_error":true}`, false},
		{domain.RuntimeClaude, `{"type":"assistant","message":{"model":"claude-haiku","content":[{"type":"tool_use","name":"Read"}]}}`, true},
		{domain.RuntimeCodex, `{"type":"thread.started","thread_id":"t"}`, false},
		{domain.RuntimeCodex, `{"type":"turn.started"}`, false},
		{domain.RuntimeCodex, `{"type":"item.completed","item":{"id":"item_0","type":"error","message":"Codex is ignoring 2 unrecognized configuration settings."}}`, false},
		{domain.RuntimeCodex, `{"type":"error","message":"stub refuses"}`, false},
		{domain.RuntimeCodex, `{"type":"turn.failed","error":{"message":"stub refuses"}}`, false},
		{domain.RuntimeCodex, `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"ls"}}`, true},
		{domain.RuntimeCodex, `{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"done"}}`, true},
		{domain.RuntimeCopilot, `{"type":"session.tools_updated","data":{"model":"claude-haiku-4.5"}}`, false},
		{domain.RuntimeCopilot, `{"type":"user.message","data":{}}`, false},
		{domain.RuntimeCopilot, `{"type":"assistant.turn_start","data":{}}`, false},
		{domain.RuntimeCopilot, `{"type":"model.call_failure","data":{"statusCode":400}}`, false},
		{domain.RuntimeCopilot, `{"type":"assistant.turn_end","data":{}}`, false},
		{domain.RuntimeCopilot, `{"type":"assistant.idle","data":{}}`, false},
		{domain.RuntimeCopilot, `{"type":"result","sessionId":"s","exitCode":1}`, false},
		{domain.RuntimeCopilot, `{"type":"assistant.message","data":{"content":"done"}}`, true},
		{domain.RuntimeCopilot, `{"type":"tool.execution_start","data":{"toolName":"view"}}`, true},
	} {
		var payload map[string]any
		if err := json.Unmarshal([]byte(check.payload), &payload); err != nil {
			t.Fatal(err)
		}
		if got := ExtractActivity(check.kind, payload); got != check.want {
			t.Errorf("%s %s: acted %v, want %v", check.kind, check.payload, got, check.want)
		}
	}
}

func TestExtractCacheLifetimeReadsClaudesCacheWrites(t *testing.T) {
	for _, check := range []struct {
		payload string
		want    time.Duration
		ok      bool
	}{
		{`{"type":"assistant","message":{"usage":{"cache_creation":{"ephemeral_1h_input_tokens":664,"ephemeral_5m_input_tokens":0}}}}`, time.Hour, true},
		{`{"type":"assistant","message":{"usage":{"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":120}}}}`, 5 * time.Minute, true},
		{`{"type":"assistant","message":{"usage":{"cache_creation":{"ephemeral_1h_input_tokens":40,"ephemeral_5m_input_tokens":120}}}}`, 5 * time.Minute, true},
		{`{"type":"result","usage":{"cache_creation":{"ephemeral_1h_input_tokens":900}}}`, time.Hour, true},
		{`{"type":"assistant","message":{"usage":{"cache_read_input_tokens":8530}}}`, 0, false},
		{`{"type":"system","subtype":"init"}`, 0, false},
	} {
		var payload map[string]any
		if err := json.Unmarshal([]byte(check.payload), &payload); err != nil {
			t.Fatal(err)
		}
		if got, ok := ExtractCacheLifetime(domain.RuntimeClaude, payload); got != check.want || ok != check.ok {
			t.Errorf("%s: %v %v, want %v %v", check.payload, got, ok, check.want, check.ok)
		}
	}
	codex := map[string]any{"type": "turn.completed", "usage": map[string]any{"cache_creation": map[string]any{"ephemeral_1h_input_tokens": 9.0}}}
	if _, ok := ExtractCacheLifetime(domain.RuntimeCodex, codex); ok {
		t.Fatal("only Claude Code reports a cache lifetime")
	}
	// Without a report, each runtime falls back to its provider's shortest
	// documented lifetime; Copilot bills per request and has none.
	for runtime, want := range map[*commandRuntime]time.Duration{
		NewClaude("fake").(*commandRuntime): 5 * time.Minute, NewCodex("fake").(*commandRuntime): 5 * time.Minute,
		NewCopilot("fake").(*commandRuntime): 0, adapter(t, customSpec()): 0,
	} {
		if got := runtime.cacheFor(&runState{}); got != want {
			t.Errorf("%s: cache for %v, want %v", runtime.spec.Name, got, want)
		}
	}
	if got := NewClaude("fake").(*commandRuntime).cacheFor(&runState{cacheFor: time.Hour}); got != time.Hour {
		t.Errorf("a reported lifetime must win: %v", got)
	}
}

func TestTheCacheCountsFromTheLastRequestNotTheEndOfTheCall(t *testing.T) {
	start := time.Date(2026, 10, 18, 22, 0, 0, 0, time.UTC)
	type event struct {
		at      time.Duration
		payload string
	}
	for name, check := range map[string]struct {
		kind   domain.Runtime
		events []event
		want   time.Duration
	}{
		// Claude Code reads a file, then writes a long final reply: the
		// cache was last refreshed when that reply's request went out, two
		// minutes before the call ended.
		"Claude Code, a slow final reply": {domain.RuntimeClaude, []event{
			{0, `{"type":"system","subtype":"init"}`},
			{10 * time.Second, `{"type":"assistant","message":{"model":"haiku","content":[{"type":"tool_use","name":"Read"}]}}`},
			{11 * time.Second, `{"type":"user","message":{"content":[{"type":"tool_result","content":"package main"}]}}`},
			{2*time.Minute + 11*time.Second, `{"type":"assistant","message":{"model":"haiku","content":[{"type":"text","text":"Here is the long reply"}]}}`},
			{2*time.Minute + 12*time.Second, `{"type":"assistant","message":{"model":"haiku","content":[{"type":"tool_use","name":"StructuredOutput"}]}}`},
			{2*time.Minute + 13*time.Second, `{"type":"result","subtype":"success"}`},
		}, 11 * time.Second},
		// Codex runs tools for six minutes before it asks: each command's
		// result starts a new request, so the last one counts, not the first.
		"Codex, a long tool loop": {domain.RuntimeCodex, []event{
			{0, `{"type":"thread.started","thread_id":"t"}`},
			{time.Second, `{"type":"turn.started"}`},
			{5 * time.Second, `{"type":"item.started","item":{"id":"1","type":"command_execution","command":"go test ./..."}}`},
			{3 * time.Minute, `{"type":"item.completed","item":{"id":"1","type":"command_execution","exit_code":0}}`},
			{3*time.Minute + 5*time.Second, `{"type":"item.started","item":{"id":"2","type":"command_execution","command":"go vet ./..."}}`},
			{6 * time.Minute, `{"type":"item.completed","item":{"id":"2","type":"command_execution","exit_code":0}}`},
			{6*time.Minute + 20*time.Second, `{"type":"item.completed","item":{"id":"3","type":"agent_message","text":"Which database?"}}`},
			{6*time.Minute + 21*time.Second, `{"type":"turn.completed","usage":{"input_tokens":10}}`},
		}, 6 * time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			state := &runState{requested: start, sent: start}
			for _, event := range check.events {
				var payload map[string]any
				if err := json.Unmarshal([]byte(event.payload), &payload); err != nil {
					t.Fatal(err)
				}
				state.observeRequest(check.kind, payload, start.Add(event.at))
			}
			if want := start.Add(check.want); !state.requested.Equal(want) {
				t.Fatalf("the last request went out at %v, not %v", want, state.requested)
			}
		})
	}
}

func TestCapabilitiesSayWhetherAnyMCPServerStarts(t *testing.T) {
	for _, check := range []struct {
		spec Spec
		want bool
	}{
		{Spec{Name: domain.RuntimeClaude}, true},
		{Spec{Name: domain.RuntimeClaude, UserConfig: true}, true},
		{Spec{Name: domain.RuntimeClaude, UserMCP: []string{"linear-server"}}, false},
		{Spec{Name: domain.RuntimeCodex}, true},
		{Spec{Name: "codex-own", Preset: domain.RuntimeCodex, UserConfig: true}, true},
		{Spec{Name: "codex-own", Preset: domain.RuntimeCodex, UserMCP: []string{"linear"}}, false},
		{Spec{Name: domain.RuntimeCopilot}, true},
		{Spec{Name: domain.RuntimeCopilot, UserConfig: true}, true},
		{Spec{Name: domain.RuntimeCopilot, UserMCP: []string{"notes"}}, false},
		{customSpec(), false},
	} {
		if got := adapter(t, check.spec).Capabilities().MCPDisabled; got != check.want {
			t.Errorf("%s (user config %v): MCPDisabled = %v, want %v", check.spec.Name, check.spec.UserConfig, got, check.want)
		}
	}
}

func TestNormalizeMalformedAndRedact(t *testing.T) {
	if got := redact("Authorization: bearer-secret token=abc123"); got == "Authorization: bearer-secret token=abc123" {
		t.Fatal("secret was not redacted")
	}
	kind, _ := normalize(domain.RuntimeCodex, map[string]any{"type": "tool_call"})
	if kind != domain.EventTool {
		t.Fatalf("got %s", kind)
	}
}

func TestNormalizeClaudeMessageAndToolDetails(t *testing.T) {
	kind, summary := normalize(domain.RuntimeClaude, map[string]any{
		"type": "assistant",
		"message": map[string]any{"content": []any{map[string]any{
			"type": "text", "text": "Inspecting the repository now.",
		}}},
	})
	if kind != domain.EventMessage || summary != "Inspecting the repository now." {
		t.Fatalf("Claude message normalized as kind=%s summary=%q", kind, summary)
	}
	kind, summary = normalize(domain.RuntimeClaude, map[string]any{
		"type": "assistant",
		"message": map[string]any{"content": []any{map[string]any{
			"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "go test ./..."},
		}}},
	})
	if kind != domain.EventTool || summary != "Claude tool: Bash — go test ./..." {
		t.Fatalf("Claude tool normalized as kind=%s summary=%q", kind, summary)
	}
}

func TestNormalizeClaudePermissionDenial(t *testing.T) {
	kind, summary := normalize(domain.RuntimeClaude, map[string]any{
		"type": "system", "subtype": "permission_denied", "message": "command is outside the approved scope",
	})
	if kind != domain.EventWarning || summary != "Claude permission denied: command is outside the approved scope" {
		t.Fatalf("Claude denial normalized as kind=%s summary=%q", kind, summary)
	}
}

func TestNormalizeClaudeRedactsProviderSummaries(t *testing.T) {
	for _, payload := range []map[string]any{
		{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "token=assistant-secret"}}}},
		{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "API_KEY=tool-secret go test ./..."}}}}},
		{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "password=result-secret"}}}},
		{"type": "system", "subtype": "permission_denied", "message": "authorization: denied-secret"},
	} {
		_, summary := normalize(domain.RuntimeClaude, payload)
		if !strings.Contains(summary, "[REDACTED]") || strings.Contains(summary, "secret") {
			t.Fatalf("provider summary was not redacted: %q", summary)
		}
	}
}

func TestTruncatePreservesUTF8(t *testing.T) {
	truncated := truncate("hello 世界", 7)
	if !utf8.ValidString(truncated) || truncated != "hello …" {
		t.Fatalf("truncate returned %q", truncated)
	}
}

func TestDrainRuntimeStreamsWaitsForBothScanners(t *testing.T) {
	events := make(chan domain.RunEvent, 1)
	results := make(chan error, 1)
	var scanners sync.WaitGroup
	scanners.Add(2)
	go func() {
		defer scanners.Done()
		for range 100 {
			events <- domain.RunEvent{}
		}
	}()
	go func() {
		defer scanners.Done()
		results <- nil
	}()
	go func() {
		scanners.Wait()
		close(events)
		close(results)
	}()
	drained := make(chan struct{})
	go func() {
		drainRuntimeStreams(events, results)
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("runtime streams did not drain")
	}
}

func TestCopilotEnvironmentCannotInheritPermissionOrWorkspaceExtensionBypasses(t *testing.T) {
	t.Setenv("COPILOT_ALLOW_ALL", "true")
	t.Setenv("GITHUB_COPILOT_PROMPT_MODE_WORKSPACE_MCP", "true")
	environment := NewCopilot("fake").(*commandRuntime).environment(RunRequest{})
	if !contains(environment, "COPILOT_ALLOW_ALL=false") || !contains(environment, "GITHUB_COPILOT_PROMPT_MODE_WORKSPACE_MCP=false") {
		t.Fatalf("Copilot guardrails missing: %v", environment)
	}
	if contains(environment, "COPILOT_ALLOW_ALL=true") || contains(environment, "GITHUB_COPILOT_PROMPT_MODE_WORKSPACE_MCP=true") {
		t.Fatalf("unsafe Copilot environment inherited: %v", environment)
	}
}

func TestWorkerEnvironmentExcludesAmbientSecrets(t *testing.T) {
	t.Setenv("SNEAKY_TOKEN", "leak")
	t.Setenv("OPENAI_API_KEY", "ambient")
	t.Setenv("LC_ALL", "C.UTF-8")
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/claude-config")
	for _, factory := range []func(string) AgentRuntime{NewClaude, NewCodex, NewCopilot} {
		environment := factory("fake").(*commandRuntime).environment(RunRequest{Environment: []string{"ANTHROPIC_API_KEY=declared"}})
		for _, entry := range environment {
			name, _, _ := strings.Cut(entry, "=")
			if name == "SNEAKY_TOKEN" || name == "OPENAI_API_KEY" {
				t.Fatalf("ambient secret leaked into worker environment: %v", environment)
			}
		}
		if !contains(environment, "ANTHROPIC_API_KEY=declared") || !contains(environment, "LC_ALL=C.UTF-8") || !contains(environment, "CLAUDE_CONFIG_DIR=/tmp/claude-config") {
			t.Fatalf("declared secret or baseline variable missing: %v", environment)
		}
		var hasPath bool
		for _, entry := range environment {
			hasPath = hasPath || strings.HasPrefix(entry, "PATH=")
		}
		if !hasPath {
			t.Fatalf("PATH must be inherited: %v", environment)
		}
	}
}

func containsPair(values []string, left, right string) bool {
	for index := range values {
		if index+1 < len(values) && values[index] == left && values[index+1] == right {
			return true
		}
	}
	return false
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestRedactRemovesMultiWordSecretValues(t *testing.T) {
	cases := map[string][]string{
		`{"authorization":"Bearer sk-ant-abc123","x":1}`: {"Bearer", "sk-ant-abc123"},
		`Authorization: Bearer sk-ant-abc123`:            {"Bearer", "sk-ant-abc123"},
		`password = correct horse battery staple`:        {"correct", "horse", "battery", "staple"},
		`api_key=abc123 token=def456`:                    {"abc123"},
	}
	for input, leaked := range cases {
		got := redact(input)
		for _, word := range leaked {
			if strings.Contains(got, word) {
				t.Fatalf("redact(%q) leaked %q: %q", input, word, got)
			}
		}
	}
	if got := redact(`password = correct horse battery staple`); got != "password = [REDACTED]" {
		t.Fatalf("unexpected redaction %q", got)
	}
	for _, prose := range []string{"no credentials here", "the authentication token was rejected by the server", "secret sauce is not a credential"} {
		if got := redact(prose); got != prose {
			t.Fatalf("prose without a separator must pass through, got %q", got)
		}
	}
}

func TestClaudeArgumentsFollowStepPermission(t *testing.T) {
	claude := NewClaude("fake").(*commandRuntime)
	write := claude.arguments(RunRequest{Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionWorktree, Commands: [][]string{{"go", "test", "./..."}}}, "")
	if !containsPair(write, "--permission-mode", "acceptEdits") {
		t.Fatalf("a writable Claude step cannot edit its worktree: %v", write)
	}
	for _, tool := range []string{"Read", "Edit", "Write", "Bash(go test ./...)"} {
		if !contains(write, tool) {
			t.Fatalf("writable Claude step is missing %s: %v", tool, write)
		}
	}
	if contains(write, "--disallowedTools") || contains(write, "Bash") {
		t.Fatalf("writable Claude step is wider than its allowlist: %v", write)
	}

	read := claude.arguments(RunRequest{Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly}, "")
	if contains(read, "--permission-mode") || !containsPair(read, "--disallowedTools", "Edit") || !contains(read, "Write") {
		t.Fatalf("read-only Claude step can edit files: %v", read)
	}
	for _, arg := range append(append([]string{}, write...), read...) {
		if strings.EqualFold(arg, "bypassPermissions") || arg == "--dangerously-skip-permissions" {
			t.Fatalf("Claude args bypass permissions: %v", arg)
		}
	}
}

func TestGrantsWidenOnlyWhatWasApproved(t *testing.T) {
	grants := []domain.PermissionGrant{
		{Command: "golangci-lint run"}, {Tool: "WebFetch"},
		// None of these may reach a CLI: edits follow the step permission, the
		// shell is allowed one command at a time, and rules are not tool names.
		{Tool: "Write"}, {Tool: "Bash"}, {Tool: "Bash(rm *)"}, {Tool: "--dangerously-skip-permissions"}, {Command: "echo one\necho two"},
	}
	claude := NewClaude("fake").(*commandRuntime)
	args := claude.arguments(RunRequest{Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Grants: grants}, "")
	if !contains(args, "Bash(golangci-lint run)") || !contains(args, "WebFetch") {
		t.Fatalf("approved grants missing: %v", args)
	}
	allowed := args[indexOf(args, "--allowedTools")+1 : indexOf(args, "--disallowedTools")]
	if !reflect.DeepEqual(allowed, []string{"Read", "Glob", "Grep", "Bash(golangci-lint run)", "WebFetch"}) {
		t.Fatalf("grants widened the allowlist beyond what was approved: %v", allowed)
	}
	// An allowed tool the worker does not have would grant nothing.
	if tools := args[indexOf(args, "--tools")+1]; tools != "Read,Glob,Grep,Bash,WebFetch" {
		t.Fatalf("grants widened the toolset beyond what was approved: %s", tools)
	}
	copilot := NewCopilot("fake").(*commandRuntime)
	args = copilot.arguments(RunRequest{Prompt: []byte("x"), Permission: domain.PermissionWorktree, Grants: grants}, "")
	if !contains(args, "--allow-tool=shell(golangci-lint run)") {
		t.Fatalf("Copilot did not receive the approved command: %v", args)
	}
}

// A shell line the CLIs would read as a pattern, or as several commands, is
// never approved, and a stored grant for one allows nothing (#270).
func TestAnApprovedCommandPassesTheRuleCheck(t *testing.T) {
	for _, line := range []string{"rm -rf build/*", "python3 *", "npm:*", "ls ~", "go build ./... && rm -rf ~", "echo $HOME", "cat 'a b'", "sh -c ls"} {
		if grant, ok := GrantFor(domain.PermissionDenial{Tool: "Bash", Command: line}); ok {
			t.Errorf("%q must not be grantable: %#v", line, grant)
		}
	}
	if grant, ok := GrantFor(domain.PermissionDenial{Tool: "Bash", Command: "go  test\t./..."}); !ok || grant.Command != "go test ./..." {
		t.Fatalf("a plain command line is granted as its words joined by single spaces: %#v %v", grant, ok)
	}
	grants := []domain.PermissionGrant{{Command: "rm -rf build/*"}, {Command: "python3 *"}, {Command: "go test ./..."}}
	got := allowedCommands(RunRequest{Commands: [][]string{{"go", "vet", "./..."}}, Grants: grants})
	if !reflect.DeepEqual(got, []string{"go vet ./...", "go test ./..."}) {
		t.Fatalf("a stored grant the rule check refuses must allow nothing: %v", got)
	}
}

func TestExtractClaudeDenials(t *testing.T) {
	payload := map[string]any{"type": "result", "subtype": "success", "permission_denials": []any{
		map[string]any{"tool_name": "Bash", "tool_use_id": "a", "tool_input": map[string]any{"command": " go vet ./... "}},
		map[string]any{"tool_name": "Bash", "tool_use_id": "b", "tool_input": map[string]any{"command": "go vet ./..."}},
		map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "/etc/hosts"}},
		map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "curl -H 'Authorization: Bearer sk-live-123' https://example.com"}},
		map[string]any{"tool_name": "WebFetch", "tool_input": map[string]any{"url": "https://example.com"}},
	}}
	denials := ExtractDenials(domain.RuntimeClaude, payload)
	if len(denials) != 4 {
		t.Fatalf("expected 4 distinct denials, got %#v", denials)
	}
	if grant, ok := GrantFor(denials[0]); !ok || grant != (domain.PermissionGrant{Command: "go vet ./..."}) {
		t.Fatalf("command denial grant = %#v, %v", grant, ok)
	}
	if _, ok := GrantFor(denials[1]); ok {
		t.Fatal("a file edit must never be grantable")
	}
	if denials[2].Command != "" || strings.Contains(denials[2].Detail, "sk-live-123") {
		t.Fatalf("a command carrying a credential was kept: %#v", denials[2])
	}
	if _, ok := GrantFor(denials[2]); ok {
		t.Fatal("a redacted command must not be grantable")
	}
	if grant, ok := GrantFor(denials[3]); !ok || grant != (domain.PermissionGrant{Tool: "WebFetch"}) {
		t.Fatalf("tool denial grant = %#v, %v", grant, ok)
	}
	if got := ExtractDenials(domain.RuntimeCodex, payload); got != nil {
		t.Fatalf("only Claude reports structured denials, got %#v", got)
	}
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

func TestOutcomeNamesHowACallEnded(t *testing.T) {
	for err, want := range map[error]string{
		nil:                      domain.OutcomeCompleted,
		context.DeadlineExceeded: domain.OutcomeTimedOut,
		fmt.Errorf("dispatch: %w", context.Canceled):     domain.OutcomeCancelled,
		errors.New("codex worker failed: exit status 1"): domain.OutcomeFailed,
	} {
		if got := Outcome(err); got != want {
			t.Errorf("Outcome(%v) = %q, want %q", err, got, want)
		}
	}
}
