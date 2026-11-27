package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

// readRecord returns a file a fake CLI wrote, or fails the test.
func readRecord(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEachCLIStartsWithTheGrantedServersAndTheirSecretsOnlyWhereTheServerReadsThem(t *testing.T) {
	directory := t.TempDir()
	record := func(name string) string { return filepath.Join(directory, name) }
	// Each fake keeps its arguments, its environment, and the configuration
	// file it was pointed at, with whether only the user can read it.
	keep := "echo \"$*\" > '" + record("args") + "'\nenv > '" + record("env") + "'\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in --mcp-config|--additional-mcp-config) f=${2#@}; cp \"$f\" '" + record("config") + "'; " +
		"[ -n \"$(find \"$f\" -perm 600)\" ] && echo private > '" + record("mode") + "'; echo \"$f\" > '" + record("path") + "';; esac; shift; done\n"
	for _, fake := range []struct {
		runtime func(string) AgentRuntime
		script  string
		config  bool
	}{
		{NewClaude, keep + "echo '{\"type\":\"result\",\"subtype\":\"success\",\"structured_output\":{\"status\":\"completed\"}}'\n", true},
		{NewCopilot, "[ \"$1\" = mcp ] && { echo '{\"mcpServers\":{\"docs-search\":{\"source\":\"user\"},\"notes\":{\"source\":\"user\"}}}'; exit 0; }\n" + keep + "echo '{\"type\":\"result\"}'\n", true},
		{NewCodex, keep + "echo '{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"{\\\"status\\\":\\\"completed\\\"}\"}}'\n", false},
	} {
		for _, name := range []string{"args", "env", "config", "mode", "path"} {
			_ = os.Remove(record(name))
		}
		binary := filepath.Join(directory, "fake-cli")
		binary = fakecli.Write(t, binary, "#!/bin/sh\n"+fake.script)
		runtime := fake.runtime(binary)
		name := runtime.Capabilities().Runtime
		request := grantedRequest()
		request.WorkingDir, request.MCPDir = t.TempDir(), filepath.Join(directory, "runs", "run", "mcp")
		request.Budget = domain.Budget{WallTimeSeconds: 10, MaxOutputBytes: 1 << 20}
		if _, err := runtime.Start(context.Background(), request, func(domain.RunEvent) error { return nil }); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		args, environment := readRecord(t, record("args")), readRecord(t, record("env"))
		if strings.Contains(args, "docs-secret") || strings.Contains(args, "lin-secret") {
			t.Fatalf("%s: a secret reached the command line: %s", name, args)
		}
		if !fake.config {
			// Codex has its servers' secrets in its own environment, and keeps
			// them from its shell.
			if !strings.Contains(environment, "DOCS_TOKEN=docs-secret") || !strings.Contains(args, `shell_environment_policy.exclude=["DOCS_TOKEN","INKUNA_MCP_LINEAR_BEARER"]`) {
				t.Fatalf("codex: env %s\nargs %s", environment, args)
			}
			continue
		}
		if strings.Contains(environment, "docs-secret") || strings.Contains(environment, "lin-secret") {
			t.Fatalf("%s: a secret reached the CLI's environment, which its shell inherits", name)
		}
		config := readRecord(t, record("config"))
		if !strings.Contains(config, `"DOCS_TOKEN":"docs-secret"`) || !strings.Contains(config, `"Authorization":"Bearer lin-secret"`) {
			t.Fatalf("%s: the configuration must hand each server its secret: %s", name, config)
		}
		path := strings.TrimSpace(readRecord(t, record("path")))
		// Windows files have no 0600 for the fake to find; there the file's
		// place in the run's directory keeps it.
		private := goruntime.GOOS == "windows" || readRecord(t, record("mode")) == "private\n"
		if !private || filepath.Dir(path) != request.MCPDir {
			t.Fatalf("%s: the configuration must be the user's alone, in the run's directory: %s", name, path)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s: the configuration must be removed when the call ends: %v", name, err)
		}
		if name == domain.RuntimeCopilot && (strings.Contains(args, "--disable-mcp-server=docs-search") || !strings.Contains(args, "--disable-mcp-server=notes")) {
			t.Fatalf("copilot: the user's other servers stay off and the granted one on: %s", args)
		}
	}
}

func TestACallWhoseGrantedServerDidNotStartStopsAtOnce(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-claude")
	script := "#!/bin/sh\necho '{\"type\":\"system\",\"subtype\":\"init\",\"mcp_servers\":[{\"name\":\"docs-search\",\"status\":\"failed\"},{\"name\":\"linear\",\"status\":\"connected\"}]}'\nexec sleep 30\n"
	binary = fakecli.Write(t, binary, script)
	request := grantedRequest()
	request.WorkingDir, request.MCPDir = t.TempDir(), filepath.Join(t.TempDir(), "mcp")
	request.Budget = domain.Budget{WallTimeSeconds: 25, MaxOutputBytes: 1 << 20}
	started := time.Now()
	_, err := NewClaude(binary).Start(context.Background(), request, func(domain.RunEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), `MCP server "docs-search" did not start`) {
		t.Fatalf("the call must stop and say which server failed: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("the call must stop when the server fails, not run on: %s", elapsed)
	}
	if entries, _ := os.ReadDir(request.MCPDir); len(entries) != 0 {
		t.Fatalf("the configuration must be removed: %v", entries)
	}
}

func TestAServersSecretIsHiddenInTheEventsInkunaKeeps(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-claude")
	// The server echoed its token in a tool result.
	script := "#!/bin/sh\necho '{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"content\":\"the key is docs-secret-value\"}]}}'\n" +
		"echo '{\"type\":\"result\",\"subtype\":\"success\",\"structured_output\":{\"status\":\"completed\"}}'\n"
	binary = fakecli.Write(t, binary, script)
	request := grantedRequest()
	request.MCPServers[0].Env["DOCS_TOKEN"] = "docs-secret-value"
	request.WorkingDir, request.MCPDir = t.TempDir(), filepath.Join(t.TempDir(), "mcp")
	request.Budget = domain.Budget{WallTimeSeconds: 10, MaxOutputBytes: 1 << 20}
	var kept strings.Builder
	if _, err := NewClaude(binary).Start(context.Background(), request, func(event domain.RunEvent) error {
		kept.WriteString(event.Summary)
		fmt.Fprint(&kept, event.Payload)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kept.String(), "docs-secret-value") || !strings.Contains(kept.String(), "the key is [REDACTED]") {
		t.Fatalf("the server's secret must be hidden in kept events: %s", kept.String())
	}
}

func TestAWorkersSecretIsHiddenInTheEventsAndOutputInkunaKeeps(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-claude")
	// The worker printed a variable it was given, and repeated it in its
	// handoff.
	script := "#!/bin/sh\necho '{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"content\":\"token '\"$GITHUB_TOKEN\"'\"}]}}'\n" +
		"echo '{\"type\":\"result\",\"subtype\":\"success\",\"structured_output\":{\"status\":\"completed\",\"summary\":\"pushed with '\"$GITHUB_TOKEN\"'\"}}'\n"
	binary = fakecli.Write(t, binary, script)
	request := RunRequest{
		RunID: "run-step", Prompt: []byte("look"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
		WorkingDir: t.TempDir(), Budget: domain.Budget{WallTimeSeconds: 10, MaxOutputBytes: 1 << 20},
		Environment: []string{"GITHUB_TOKEN=gh-worker-secret", "SHORT=abc"},
	}
	var kept strings.Builder
	result, err := NewClaude(binary).Start(context.Background(), request, func(event domain.RunEvent) error {
		kept.WriteString(event.Summary)
		fmt.Fprint(&kept, event.Payload)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kept.String(), "gh-worker-secret") || !strings.Contains(kept.String(), "token [REDACTED]") {
		t.Fatalf("the worker's secret must be hidden in kept events: %s", kept.String())
	}
	if strings.Contains(string(result.Output), "gh-worker-secret") || !strings.Contains(string(result.Output), "pushed with [REDACTED]") {
		t.Fatalf("the worker's secret must be hidden in the output: %s", result.Output)
	}
}

func TestAServersSecretIsHiddenInTheOutputAndTheFailureInkunaKeeps(t *testing.T) {
	run := func(t *testing.T, secret, result string) (RunResult, string, error) {
		t.Helper()
		// The line is printed from a file, so no shell's echo reads its
		// backslashes as escapes.
		directory := t.TempDir()
		binary, stream := filepath.Join(directory, "fake-claude"), filepath.Join(directory, "stream.jsonl")
		if err := os.WriteFile(stream, []byte(result+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		binary = fakecli.Write(t, binary, "#!/bin/sh\ncat '"+stream+"'\n")
		request := grantedRequest()
		request.MCPServers[0].Env["DOCS_TOKEN"] = secret
		request.WorkingDir, request.MCPDir = t.TempDir(), filepath.Join(t.TempDir(), "mcp")
		request.Budget = domain.Budget{WallTimeSeconds: 10, MaxOutputBytes: 1 << 20}
		var kept strings.Builder
		output, err := NewClaude(binary).Start(context.Background(), request, func(event domain.RunEvent) error {
			kept.WriteString(event.Summary)
			return nil
		})
		return output, kept.String(), err
	}
	// The worker repeated the token in its handoff.
	result, _, err := run(t, "docs-secret-value", `{"type":"result","subtype":"success","structured_output":{"status":"completed","summary":"read with docs-secret-value"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Output), "docs-secret-value") || !strings.Contains(string(result.Output), "read with [REDACTED]") {
		t.Fatalf("the server's secret must be hidden in the output: %s", result.Output)
	}
	// A token with a quote and an ampersand is escaped in the JSON, as
	// \" and &, so only its decoded form matches.
	result, _, err = run(t, `docs"secret&value`, `{"type":"result","subtype":"success","structured_output":{"status":"completed","summary":"read with docs\"secret&value"}}`)
	if err != nil {
		t.Fatal(err)
	}
	var handoff struct{ Summary string }
	if err := json.Unmarshal(result.Output, &handoff); err != nil || handoff.Summary != "read with [REDACTED]" {
		t.Fatalf("an escaped secret must be hidden in the output: %s (%v)", result.Output, err)
	}
	// The CLI's failure quoted it.
	_, kept, err := run(t, "docs-secret-value", `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"the server refused docs-secret-value"}`)
	if err == nil || strings.Contains(err.Error(), "docs-secret-value") || !strings.Contains(err.Error(), "refused [REDACTED]") {
		t.Fatalf("the server's secret must be hidden in the failure: %v", err)
	}
	if strings.Contains(kept, "docs-secret-value") || !strings.Contains(kept, "refused [REDACTED]") {
		t.Fatalf("the server's secret must be hidden in the failure event: %s", kept)
	}
}

func TestAClaudeCallGivenSecretsDoesNotStartWithoutTheSandbox(t *testing.T) {
	missing := claudeSandboxMissing
	claudeSandboxMissing = func() string { return "Claude Code's sandbox needs bwrap, which this machine does not have" }
	t.Cleanup(func() { claudeSandboxMissing = missing })
	directory := t.TempDir()
	binary, started := filepath.Join(directory, "fake-claude"), filepath.Join(directory, "started")
	binary = fakecli.Write(t, binary, "#!/bin/sh\ntouch '"+started+"'\n")
	request := grantedRequest()
	request.WorkingDir, request.MCPDir = t.TempDir(), filepath.Join(t.TempDir(), "mcp")
	request.Budget = domain.Budget{WallTimeSeconds: 10, MaxOutputBytes: 1 << 20}
	_, err := NewClaude(binary).Start(context.Background(), request, func(domain.RunEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "needs bwrap") || !strings.Contains(err.Error(), "grant the step no server with secrets") {
		t.Fatalf("a call that cannot keep its shell from the secrets must say why it did not start: %v", err)
	}
	if _, statErr := os.Stat(started); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("the CLI must not start")
	}
}

func TestACallWhoseGrantedServerDidNotStartFailsEvenWhenTheCLIExitsCleanly(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-claude")
	// The CLI reports the failed server and finishes, exiting zero, before
	// it can be stopped.
	script := "#!/bin/sh\necho '{\"type\":\"system\",\"subtype\":\"init\",\"mcp_servers\":[{\"name\":\"docs-search\",\"status\":\"failed\"}]}'\n" +
		"echo '{\"type\":\"result\",\"subtype\":\"success\",\"structured_output\":{\"status\":\"completed\"}}'\nexit 0\n"
	binary = fakecli.Write(t, binary, script)
	request := grantedRequest()
	request.WorkingDir, request.MCPDir = t.TempDir(), filepath.Join(t.TempDir(), "mcp")
	request.Budget = domain.Budget{WallTimeSeconds: 10, MaxOutputBytes: 1 << 20}
	for attempt := 0; attempt < 20; attempt++ {
		result, err := NewClaude(binary).Start(context.Background(), request, func(domain.RunEvent) error { return nil })
		if err == nil || !strings.Contains(err.Error(), `MCP server "docs-search" did not start`) || result.Output != nil {
			t.Fatalf("attempt %d: a step without its granted server must fail: %v, output %s", attempt, err, result.Output)
		}
	}
}
