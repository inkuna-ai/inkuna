package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

// fakeCLI writes a shell script that records its arguments in calls and
// then runs body.
func fakeCLI(t *testing.T, body string) (binary, calls string) {
	t.Helper()
	directory := t.TempDir()
	binary, calls = filepath.Join(directory, "fake-cli"), filepath.Join(directory, "calls")
	binary = fakecli.Write(t, binary, "#!/bin/sh\necho \"$*\" >> '"+calls+"'\n"+body)
	return binary, calls
}

func TestEachCLIReportsOnTheUsersServersItIsAskedAbout(t *testing.T) {
	claudeBody := `case "$*" in
"mcp get -- linear-server") printf 'linear-server:\n  Scope: User config\n  Status: ! Needs authentication\n  Type: http\n  URL: https://mcp.linear.app/mcp\n';;
"mcp get -- gone") echo 'No MCP server found with name: gone' >&2; exit 1;;
"mcp list") printf 'claude.ai Gmail: https://gmailmcp.googleapis.com/mcp/v1 - ✔ Connected\n';;
esac
`
	codexBody := `case "$*" in
"mcp list --json") echo '[{"name":"linear","enabled":true,"transport":{"type":"streamable_http","url":"https://mcp.linear.app/mcp"},"auth_status":"not_logged_in"},{"name":"notes","enabled":true,"transport":{"type":"stdio","command":"notes"},"auth_status":"unsupported"}]';;
"mcp get --json -- linear") echo '{"name":"linear","enabled":true,"transport":{"type":"streamable_http","url":"https://mcp.linear.app/mcp"}}';;
"mcp get --json -- notes") echo '{"name":"notes","enabled":true,"transport":{"type":"stdio","command":"notes","args":["--stdio"],"env":{"NOTES_TOKEN":"notes-secret-value"}},"enabled_tools":["read"]}';;
esac
`
	copilotBody := `echo '{"mcpServers":{"wiki":{"type":"http","url":"https://wiki.example/mcp","source":"user"},"write":{"type":"local","command":"w","source":"user"}}}'
`
	dir := t.TempDir()

	claude, claudeCalls := fakeCLI(t, claudeBody)
	servers, err := NewClaude(claude).(UserServers).ResolveUserServers(context.Background(), dir, []string{"linear-server", "gone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0].Status != UserServerNeedsSignIn || !servers[0].SignIn || servers[1].Status != UserServerMissing {
		t.Fatalf("Claude Code servers = %#v", servers)
	}
	if calls := readRecord(t, claudeCalls); strings.Contains(calls, "mcp list") {
		t.Fatalf("asking about servers by name must not connect to all of them: %q", calls)
	}
	// A connector's URL, which allows it, is only in the full listing.
	servers, err = NewClaude(claude).(UserServers).ResolveUserServers(context.Background(), dir, []string{"claude.ai Gmail"})
	if err != nil || len(servers) != 1 || servers[0].URL != "https://gmailmcp.googleapis.com/mcp/v1" || servers[0].Status != UserServerReady {
		t.Fatalf("connector = %#v, %v", servers, err)
	}

	codex, _ := fakeCLI(t, codexBody)
	servers, err = NewCodex(codex).(UserServers).ResolveUserServers(context.Background(), dir, []string{"notes", "linear"})
	if err != nil {
		t.Fatal(err)
	}
	if servers[0].Name != "notes" || servers[0].codex == nil || servers[0].codex.Transport.Env["NOTES_TOKEN"] != "notes-secret-value" || len(servers[0].codex.EnabledTools) != 1 {
		t.Fatalf("Codex's notes server must carry its configuration: %#v", servers[0])
	}
	if servers[1].Status != UserServerNeedsSignIn || !servers[1].SignIn {
		t.Fatalf("Codex's linear server = %#v", servers[1])
	}

	copilot, _ := fakeCLI(t, copilotBody)
	listed, err := NewCopilot(copilot).(UserServers).ListUserServers(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Name != "wiki" || listed[0].Status != UserServerUnknown || listed[0].SignIn || listed[1].Status != UserServerFailed {
		t.Fatalf("Copilot servers = %#v", listed)
	}
}

func TestSigningInRunsTheCLIsOwnSignIn(t *testing.T) {
	dir := t.TempDir()
	claude, calls := fakeCLI(t, "")
	if err := NewClaude(claude).(UserServers).SignInUserServer(context.Background(), dir, "-x"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(readRecord(t, calls)); got != "mcp login -- -x" {
		t.Fatalf("sign-in ran %q", got)
	}
	failing, _ := fakeCLI(t, "echo 'authorization was denied' >&2\nexit 1\n")
	if err := NewCodex(failing).(UserServers).SignInUserServer(context.Background(), dir, "linear"); err == nil || !strings.Contains(err.Error(), "authorization was denied") {
		t.Fatalf("a failed sign-in must say why: %v", err)
	}
	if err := NewCopilot("copilot").(UserServers).SignInUserServer(context.Background(), dir, "wiki"); err == nil || !strings.Contains(err.Error(), "/mcp") {
		t.Fatalf("Copilot has no sign-in command: %v", err)
	}
}

func TestACodexCallWhoseUserServerDidNotStartSaysWhich(t *testing.T) {
	codex, _ := fakeCLI(t, "echo 'Error: required MCP servers failed to initialize: linear: OAuth authorization required' >&2\nexit 1\n")
	var entry codexServer
	entry.Transport.Type, entry.Transport.URL = "streamable_http", "https://mcp.linear.app/mcp"
	server := codexUser("linear", entry)
	server.SignIn = true
	request := RunRequest{RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("look"), Permission: domain.PermissionReadOnly,
		Budget: domain.Budget{WallTimeSeconds: 10, MaxOutputBytes: 4096}, UserServers: []UserServer{server}}
	_, err := adapter(t, Spec{Name: domain.RuntimeCodex, Binary: codex, UserMCP: []string{"linear"}}).Start(context.Background(), request, func(domain.RunEvent) error { return nil })
	var failure *UserServerError
	if !errors.As(err, &failure) || failure.Server != "linear" || !failure.SignIn {
		t.Fatalf("the call must say the user's server needs signing in: %v", err)
	}
}

func TestACallGoesOnWithoutAUserServerTheStreamSaysDidNotStart(t *testing.T) {
	claude, _ := fakeCLI(t, `echo '{"type":"system","subtype":"init","mcp_servers":[{"name":"claude.ai Gmail","status":"needs-auth"}]}'
echo '{"type":"result","subtype":"success","structured_output":{"status":"completed"}}'
`)
	request := userServersRequest(t)
	request.MCPServers = nil
	request.UserServers = request.UserServers[1:2]
	request.Budget = domain.Budget{WallTimeSeconds: 10, MaxOutputBytes: 4096}
	var warnings []string
	_, err := adapter(t, Spec{Name: domain.RuntimeClaude, Binary: claude, UserMCP: []string{"claude.ai Gmail"}}).Start(context.Background(), request, func(entry domain.RunEvent) error {
		if entry.Kind == domain.EventWarning {
			warnings = append(warnings, entry.Summary)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("a user's server that did not start must not stop the call: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"claude.ai Gmail" did not start`) {
		t.Fatalf("warnings = %q", warnings)
	}
}

func TestACodexCallLoadingTheUsersConfigurationSwitchesOffTheServersItIsNotGiven(t *testing.T) {
	codex, calls := fakeCLI(t, `[ "$1" = mcp ] && { echo '[{"name":"linear","enabled":true,"transport":{"type":"streamable_http","url":"u"},"auth_status":"o_auth"},{"name":"notes","enabled":true,"transport":{"type":"stdio","command":"n"},"auth_status":"unsupported"},{"name":"off","enabled":false,"transport":{"type":"stdio","command":"o"},"auth_status":"unsupported"}]'; exit 0; }
echo '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"status\":\"completed\"}"}}'
`)
	request := RunRequest{RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("look"), Permission: domain.PermissionReadOnly,
		Budget: domain.Budget{WallTimeSeconds: 10, MaxOutputBytes: 4096}, UserServers: []UserServer{{Name: "linear", Source: "config", Status: UserServerReady}}}
	if _, err := adapter(t, Spec{Name: domain.RuntimeCodex, Binary: codex, UserConfig: true, UserMCP: []string{"linear"}}).Start(context.Background(), request, func(domain.RunEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(readRecord(t, calls)), "\n")
	worker := lines[len(lines)-1]
	if !strings.Contains(worker, "--config mcp_servers.notes.enabled=false -") || strings.Contains(worker, "mcp_servers.linear.enabled=false") || strings.Contains(worker, "mcp_servers.off.enabled") || !strings.Contains(worker, "mcp_servers.linear.required=true") {
		t.Fatalf("Codex worker = %q", worker)
	}
}
