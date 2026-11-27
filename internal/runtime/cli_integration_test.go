package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

func TestRuntimeNormalizesFakeJSONLAndMalformedEvents(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-codex")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"tool_call\",\"token\":\"secret-value\"}' 'not-json' '{\"type\":\"result\"}'\n"
	binary = fakecli.Write(t, binary, script)
	runtime := NewCodex(binary)
	events := make([]domain.RunEvent, 0)
	_, err := runtime.Start(context.Background(), RunRequest{RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}}, func(event domain.RunEvent) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	var tool, warning, completed bool
	for _, event := range events {
		tool = tool || event.Kind == domain.EventTool
		warning = warning || event.Kind == domain.EventWarning
		completed = completed || event.Kind == domain.EventComplete
	}
	if !tool || !warning || !completed {
		t.Fatalf("normalized events missing: %#v", events)
	}
}

func TestOnlyCodexIsSentTheStrictFormOfTheSchema(t *testing.T) {
	directory := t.TempDir()
	schema := `{"type":"object","required":["status"],"properties":{"status":{"type":"string"},"note":{"type":"string"}}}`
	for _, fake := range []struct {
		runtime func(string) AgentRuntime
		flag    string
		output  string
		want    string
	}{
		{NewCodex, "--output-schema", `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"status\":\"done\",\"note\":null}"}}`,
			`{"type":"object","required":["status","note"],"properties":{"status":{"type":"string"},"note":{"anyOf":[{"type":"string"},{"type":"null"}]}},"additionalProperties":false}`},
		{NewClaude, "--json-schema", `{"type":"result","subtype":"success","structured_output":{"status":"done"}}`, schema},
	} {
		binary := filepath.Join(directory, "fake"+fake.flag)
		received := filepath.Join(directory, "schema"+fake.flag)
		// Codex is given a file and Claude Code the schema itself; either way
		// the fake keeps what it was sent.
		script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = " + fake.flag + " ]; then if [ -f \"$2\" ]; then cp \"$2\" '" + received + "'; else printf '%s' \"$2\" > '" + received + "'; fi; fi; shift; done\n" +
			"printf '%s\\n' '" + fake.output + "'\n"
		binary = fakecli.Write(t, binary, script)
		result, err := fake.runtime(binary).Start(context.Background(), RunRequest{RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(schema), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}}, func(domain.RunEvent) error { return nil })
		if err != nil || !strings.Contains(string(result.Output), `"status":"done"`) {
			t.Fatalf("%s run: %s, %v", fake.flag, result.Output, err)
		}
		sent, err := os.ReadFile(received)
		if err != nil || string(sent) != fake.want {
			t.Fatalf("%s was sent\n%s\nwant\n%s (%v)", fake.flag, sent, fake.want, err)
		}
	}
}

func TestARunReportsTheSessionALaterRunResumes(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-claude")
	// The fake reports the session it was asked to resume, or a new one.
	script := "#!/bin/sh\nsession=first-session\nwhile [ $# -gt 0 ]; do [ \"$1\" = --resume ] && session=$2; shift; done\n" +
		"printf '%s\\n' \"{\\\"type\\\":\\\"system\\\",\\\"subtype\\\":\\\"init\\\",\\\"session_id\\\":\\\"$session\\\"}\" '{\"type\":\"result\",\"subtype\":\"success\",\"structured_output\":{\"status\":\"completed\"}}'\n"
	binary = fakecli.Write(t, binary, script)
	runtime := NewClaude(binary)
	request := RunRequest{RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Resumable: true, Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}}
	result, err := runtime.Start(context.Background(), request, func(domain.RunEvent) error { return nil })
	if err != nil || result.Session != "first-session" {
		t.Fatalf("first run: session %q, %v", result.Session, err)
	}
	request.RunID, request.Session = "run-turn2", "earlier-session"
	if result, err := runtime.Start(context.Background(), request, func(domain.RunEvent) error { return nil }); err != nil || result.Session != "earlier-session" {
		t.Fatalf("resumed run: session %q, %v", result.Session, err)
	}
}

func TestACallWithoutItsTotalsLeavesNoSessionToResume(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-claude")
	// The stream names its session and a message, then ends without the
	// result that carries the session's totals, as output cut off at the
	// budget would.
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"cut-session\"}' '{\"type\":\"assistant\",\"message\":{\"id\":\"msg_1\",\"content\":[],\"usage\":{\"input_tokens\":6,\"output_tokens\":20}}}'\n"
	binary = fakecli.Write(t, binary, script)
	request := RunRequest{RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Resumable: true, Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}}
	result, err := NewClaude(binary).Start(context.Background(), request, func(domain.RunEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	// Resuming would count the session's earlier usage again, so the next
	// turn starts over; the call still reports what its messages used.
	if result.Session != "" || result.SessionUsage != (domain.Usage{}) || result.Usage != (domain.Usage{InputTokens: 6, OutputTokens: 20, Turns: 1}) {
		t.Fatalf("result = session %q, session usage %+v, usage %+v", result.Session, result.SessionUsage, result.Usage)
	}
}

func TestAFailedRunSaysWhetherTheModelActed(t *testing.T) {
	for name, check := range map[string]struct {
		events string
		acted  bool
	}{
		"failed at startup": {`'{"type":"system","subtype":"init","session_id":"s"}'`, false},
		"failed at work":    {`'{"type":"system","subtype":"init","session_id":"s"}' '{"type":"assistant","message":{"model":"haiku","content":[{"type":"tool_use","name":"Edit"}]}}'`, true},
	} {
		t.Run(name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "fake-claude")
			script := "#!/bin/sh\nprintf '%s\\n' " + check.events + "\necho 'No conversation found' >&2\nexit 1\n"
			binary = fakecli.Write(t, binary, script)
			result, err := NewClaude(binary).Start(context.Background(), RunRequest{RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Session: "s", Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}}, func(domain.RunEvent) error { return nil })
			if err == nil || Outcome(err) != domain.OutcomeFailed || result.Acted != check.acted {
				t.Fatalf("acted %v, err %v; want acted %v and a failure", result.Acted, err, check.acted)
			}
		})
	}
}

func TestARunReportsHowLongItsCacheLasts(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-claude")
	script := "#!/bin/sh\nprintf '%s\\n' " +
		`'{"type":"assistant","message":{"model":"haiku","usage":{"cache_creation":{"ephemeral_1h_input_tokens":664,"ephemeral_5m_input_tokens":0}},"content":[{"type":"text","text":"done"}]}}' ` +
		`'{"type":"result","subtype":"success","session_id":"s","structured_output":{"status":"completed"}}'` + "\n"
	binary = fakecli.Write(t, binary, script)
	started := time.Now()
	result, err := NewClaude(binary).Start(context.Background(), RunRequest{RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}}, func(domain.RunEvent) error { return nil })
	if err != nil || result.CacheUntil.Before(started.Add(time.Hour)) || result.CacheUntil.After(time.Now().Add(time.Hour)) {
		t.Fatalf("cache until %v, %v; want an hour, as Claude Code reported, after the call's request", result.CacheUntil, err)
	}
}

func TestCopilotRuntimeUsesCommonJSONLEventContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-copilot")
	script := "#!/bin/sh\n[ \"$1\" = mcp ] && { echo '{\"mcpServers\":{}}'; exit 0; }\nprintf '%s\\n' '{\"type\":\"tool.execution_start\"}' '{\"type\":\"assistant.message\"}' '{\"type\":\"result\"}'\n"
	binary = fakecli.Write(t, binary, script)
	agent := NewCopilot(binary)
	events := make([]domain.RunEvent, 0)
	_, err := agent.Start(context.Background(), RunRequest{RunID: "copilot-run", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Model: "claude-haiku-4.5", Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}}, func(event domain.RunEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var tool, completed bool
	for _, event := range events {
		tool = tool || event.Kind == domain.EventTool
		completed = completed || event.Kind == domain.EventComplete
	}
	if !tool || !completed {
		t.Fatalf("normalized Copilot events missing: %#v", events)
	}
}

func TestAPromptTooLongForTheCommandLineIsRefusedBeforeStarting(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "started")
	binary := filepath.Join(directory, "fake-copilot")
	// Records any call, and answers the MCP listing as Copilot does.
	script := "#!/bin/sh\ntouch '" + marker + "'\n[ \"$1\" = mcp ] && { echo '{\"mcpServers\":{}}'; exit 0; }\necho '{\"type\":\"result\"}'\n"
	binary = fakecli.Write(t, binary, script)
	request := RunRequest{RunID: "long", WorkingDir: directory, Prompt: bytes.Repeat([]byte("a"), MaxArgumentBytes), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 5}}
	_, err := NewCopilot(binary).Start(context.Background(), request, func(domain.RunEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "budgets.envelope") || !strings.Contains(err.Error(), fmt.Sprint(MaxArgumentBytes)) {
		t.Fatalf("an oversize prompt must be refused with a way out, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the CLI ran, and listed its MCP servers, before the prompt was refused")
	}
	// A prompt that fits starts as usual.
	request.Prompt = []byte("short")
	if _, err := NewCopilot(binary).Start(context.Background(), request, func(domain.RunEvent) error { return nil }); err != nil {
		t.Fatalf("a short prompt must start: %v", err)
	}
	// A server name from the user's configuration is held to the limit too.
	long := filepath.Join(directory, "long-copilot")
	script = "#!/bin/sh\n[ \"$1\" = mcp ] && { printf '{\"mcpServers\":{\"%s\":{}}}' \"$(head -c 140000 /dev/zero | tr '\\0' x)\"; exit 0; }\ntouch '" + marker + "-long'\n"
	long = fakecli.Write(t, long, script)
	if _, err := NewCopilot(long).Start(context.Background(), request, func(domain.RunEvent) error { return nil }); err == nil || !strings.Contains(err.Error(), fmt.Sprint(MaxArgumentBytes)) {
		t.Fatalf("an oversize server name must be refused, got %v", err)
	}
	if _, statErr := os.Stat(marker + "-long"); statErr == nil {
		t.Fatal("the worker started with an argument the system would refuse")
	}
}

func TestCopilotWorkersStartNoneOfTheUsersMCPServers(t *testing.T) {
	directory := t.TempDir()
	calls := filepath.Join(directory, "calls")
	binary := filepath.Join(directory, "fake-copilot")
	// Lists two user servers, one named like a flag, and records every call.
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n" +
		"[ \"$1\" = mcp ] && { echo '{\"mcpServers\":{\"notes\":{\"source\":\"user\"},\"-x\":{\"source\":\"workspace\"}}}'; exit 0; }\n" +
		"echo '{\"type\":\"result\"}'\n"
	binary = fakecli.Write(t, binary, script)
	request := RunRequest{RunID: "copilot-run", WorkingDir: directory, Prompt: []byte("test"), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}}
	sink := func(domain.RunEvent) error { return nil }
	recorded := func() []string {
		t.Helper()
		data, err := os.ReadFile(calls)
		if err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(calls)
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}

	if _, err := adapter(t, Spec{Name: domain.RuntimeCopilot, Binary: binary}).Start(t.Context(), request, sink); err != nil {
		t.Fatal(err)
	}
	got := recorded()
	if len(got) != 2 || got[0] != "mcp list --json" || !strings.HasSuffix(got[1], " --disable-mcp-server=-x --disable-mcp-server=notes") {
		t.Fatalf("Copilot calls = %q, want the listing, then a worker disabling both servers", got)
	}

	// With the user's setup loaded their servers still stay off: userMcp is
	// the only way a worker starts one.
	if _, err := adapter(t, Spec{Name: domain.RuntimeCopilot, Binary: binary, UserConfig: true}).Start(t.Context(), request, sink); err != nil {
		t.Fatal(err)
	}
	if got := recorded(); len(got) != 2 || !strings.HasSuffix(got[1], " --disable-mcp-server=-x --disable-mcp-server=notes") {
		t.Fatalf("Copilot calls with the user's setup = %q, want both servers disabled", got)
	}

	// A server the call is given stays on, with all its tools allowed.
	given := request
	given.UserServers = []UserServer{{Name: "notes", Source: "user", Status: UserServerUnknown}}
	if _, err := adapter(t, Spec{Name: domain.RuntimeCopilot, Binary: binary, UserMCP: []string{"notes"}}).Start(t.Context(), given, sink); err != nil {
		t.Fatal(err)
	}
	got = recorded()
	if len(got) != 2 || !strings.HasSuffix(got[1], " --disable-mcp-server=-x") || !strings.Contains(got[1], "--available-tools=view,glob,grep,notes ") || !strings.Contains(got[1], " --allow-tool=notes ") {
		t.Fatalf("Copilot calls given the user's notes server = %q", got)
	}

	// A listing that fails leaves no way to know what would start.
	broken := filepath.Join(directory, "broken-copilot")
	script = "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n[ \"$1\" = mcp ] && { echo 'Error: mcpServers: Required' >&2; exit 1; }\necho '{\"type\":\"result\"}'\n"
	broken = fakecli.Write(t, broken, script)
	_, err := adapter(t, Spec{Name: domain.RuntimeCopilot, Binary: broken}).Start(t.Context(), request, sink)
	if err == nil || !strings.Contains(err.Error(), "mcpServers: Required") {
		t.Fatalf("a failed listing must stop the worker with Copilot's reason: %v", err)
	}
	if got := recorded(); len(got) != 1 {
		t.Fatalf("the worker started after a failed listing: %q", got)
	}
}

func TestDeclaredTextRuntimeRunsReadOnlyAndReturnsItsFinalJSON(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "fake-cli")
	// Prints its arguments and stdin so the test sees exactly what exec passed.
	script := "#!/bin/sh\nfor arg in \"$@\"; do printf 'arg=%s\\n' \"$arg\"; done\nprintf 'stdin=%s\\n' \"$(cat)\"\nprintf '%s\\n' 'Here is my answer:' '{\"summary\":\"reviewed\"}'\n"
	binary = fakecli.Write(t, binary, script)
	agent, err := New(Spec{
		Name: "fake", Binary: binary, Stdin: StdinPrompt, Output: OutputText,
		Args: []string{"run", "--model", "{model}"}, ReadOnlyArgs: []string{"--read-only"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities, err := agent.Probe(context.Background()); err != nil || !capabilities.Available || capabilities.Runtime != "fake" {
		t.Fatalf("probe = %#v, %v", capabilities, err)
	}
	summaries := make([]string, 0)
	result, err := agent.Start(context.Background(), RunRequest{RunID: "fake-run", WorkingDir: directory, Prompt: []byte("check $(whoami)"), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}}, func(event domain.RunEvent) error {
		summaries = append(summaries, event.Summary)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Output) != `{"summary":"reviewed"}` {
		t.Fatalf("output = %s", result.Output)
	}
	transcript := strings.Join(summaries, "\n")
	for _, want := range []string{"arg=run", "arg=--read-only", "stdin=check $(whoami)"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("missing %q in:\n%s", want, transcript)
		}
	}
	if strings.Contains(transcript, "arg=--model") {
		t.Fatalf("an unset model must drop its flag:\n%s", transcript)
	}
}

func TestFailedAndTimedOutRunsKeepTheirUsage(t *testing.T) {
	directory := t.TempDir()
	claude := filepath.Join(directory, "fake-claude")
	// Claude ran out of turns: an error result that still reports what it used.
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"error_max_turns\",\"is_error\":true,\"result\":\"too many turns\",\"num_turns\":9,\"total_cost_usd\":1.25,\"usage\":{\"input_tokens\":40,\"cache_creation_input_tokens\":3000,\"cache_read_input_tokens\":90000,\"output_tokens\":2100}}'\n"
	claude = fakecli.Write(t, claude, script)
	codex := filepath.Join(directory, "fake-codex")
	// Codex finished a turn, then hung until the wall-time budget ran out.
	script = "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":5000,\"cached_input_tokens\":4000,\"output_tokens\":300}}'\nexec sleep 30\n"
	codex = fakecli.Write(t, codex, script)
	sink := func(domain.RunEvent) error { return nil }
	request := func(runID string, seconds int) RunRequest {
		return RunRequest{RunID: runID, WorkingDir: directory, Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: seconds, MaxOutputBytes: 4096}}
	}

	result, err := NewClaude(claude).Start(context.Background(), request("claude-run", 5), sink)
	if err == nil || Outcome(err) != domain.OutcomeFailed {
		t.Fatalf("an error result must fail the run: %v", err)
	}
	if want := (domain.Usage{InputTokens: 40, CacheWriteTokens: 3000, CacheReadTokens: 90000, OutputTokens: 2100, ReportedCents: 125, Turns: 9}); result.Usage != want || result.Output != nil {
		t.Fatalf("failed run result = %+v, want only usage %+v", result, want)
	}

	// Claude finished a message, then hung before its result.
	hung := filepath.Join(directory, "fake-claude-hung")
	script = "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"assistant\",\"message\":{\"id\":\"msg_1\",\"content\":[],\"usage\":{\"input_tokens\":6,\"cache_creation_input_tokens\":28000,\"cache_read_input_tokens\":0,\"output_tokens\":120}}}'\nexec sleep 30\n"
	hung = fakecli.Write(t, hung, script)
	result, err = NewClaude(hung).Start(context.Background(), request("claude-hung", 1), sink)
	if want := (domain.Usage{InputTokens: 6, CacheWriteTokens: 28000, OutputTokens: 120, Turns: 1}); Outcome(err) != domain.OutcomeTimedOut || result.Usage != want {
		t.Fatalf("a Claude run cut off before its result = %+v, %v; want usage %+v", result.Usage, err, want)
	}

	result, err = NewCodex(codex).Start(context.Background(), request("codex-run", 1), sink)
	if Outcome(err) != domain.OutcomeTimedOut {
		t.Fatalf("a run past its wall time must time out: %v", err)
	}
	if want := (domain.Usage{InputTokens: 1000, CacheReadTokens: 4000, OutputTokens: 300}); result.Usage != want {
		t.Fatalf("timed-out run usage = %+v, want %+v", result.Usage, want)
	}
}

func TestAResumedCallReportsOnlyItsOwnUsage(t *testing.T) {
	directory := t.TempDir()
	codex := filepath.Join(directory, "fake-codex")
	// The second call of a thread: Codex reports the thread's totals.
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"thread-1\"}' '{\"type\":\"item.completed\",\"item\":{\"id\":\"1\",\"type\":\"agent_message\",\"text\":\"{}\"}}' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":3000,\"cached_input_tokens\":900,\"output_tokens\":30}}'\n"
	codex = fakecli.Write(t, codex, script)
	before := domain.Usage{InputTokens: 1000, OutputTokens: 10}
	request := RunRequest{
		RunID: "codex-resume", WorkingDir: directory, Prompt: []byte("continue"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
		Budget: domain.Budget{WallTimeSeconds: 5, MaxOutputBytes: 4096}, Model: "gpt-5.6-luna", Resumable: true, Session: "thread-1", SessionUsage: before,
	}
	result, err := NewCodex(codex).Start(context.Background(), request, func(domain.RunEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if want := (domain.Usage{InputTokens: 1100, CacheReadTokens: 900, OutputTokens: 20}); result.Usage != want {
		t.Fatalf("resumed call usage = %+v, want %+v", result.Usage, want)
	}
	if want := (domain.Usage{InputTokens: 2100, CacheReadTokens: 900, OutputTokens: 30}); result.SessionUsage != want {
		t.Fatalf("session usage = %+v, want the thread's totals %+v", result.SessionUsage, want)
	}
	// The same stream in a new session is all the call's own.
	request.Session, request.RunID = "", "codex-new"
	if result, err = NewCodex(codex).Start(context.Background(), request, func(domain.RunEvent) error { return nil }); err != nil || result.Usage != result.SessionUsage {
		t.Fatalf("a new session's call = %+v, %v", result, err)
	}
}
