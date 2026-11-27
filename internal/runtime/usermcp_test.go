package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// claudeListing is `claude mcp list` with every kind of server Claude Code
// reports: config servers over HTTP and stdio, connectors, a plugin's, and
// ones that need signing in, failed, or wait for a project's approval.
const claudeListing = `Checking MCP server health…

claude.ai Gmail: https://gmailmcp.googleapis.com/mcp/v1 - ✔ Connected
claude.ai Claude Docs: https://api.anthropic.com/v1/pages/mcp - ! Needs authentication
plugin:postman:postman: https://mcp.postman.com/minimal (HTTP) - ! Needs authentication
linear-server: https://mcp.linear.app/mcp (HTTP) - ✔ Connected
notes: python3 -I /opt/notes/server.py --stdio - ✔ Connected
broken: /usr/local/bin/broken-mcp - ✗ Failed to connect
repo-tool: npx -y repo-tool - ⏸ Pending approval
`

func TestClaudesListingIsReadServerByServer(t *testing.T) {
	servers := parseClaudeList([]byte(claudeListing))
	want := []UserServer{
		{Name: "claude.ai Gmail", Source: "claude.ai", Transport: "http", URL: "https://gmailmcp.googleapis.com/mcp/v1", Status: UserServerReady, SignIn: true},
		{Name: "claude.ai Claude Docs", Source: "claude.ai", Transport: "http", URL: "https://api.anthropic.com/v1/pages/mcp", Status: UserServerNeedsSignIn, Detail: "needs sign-in", SignIn: true},
		{Name: "plugin:postman:postman", Source: "plugin", Transport: "http", URL: "https://mcp.postman.com/minimal", Status: UserServerNeedsSignIn, Detail: "needs sign-in", SignIn: true},
		{Name: "linear-server", Source: "config", Transport: "http", URL: "https://mcp.linear.app/mcp", Status: UserServerReady, SignIn: true},
		{Name: "notes", Source: "config", Transport: "stdio", Command: "python3", Status: UserServerReady, args: []string{"-I", "/opt/notes/server.py", "--stdio"}},
		{Name: "broken", Source: "config", Transport: "stdio", Command: "/usr/local/bin/broken-mcp", Status: UserServerFailed, Detail: "Failed to connect", args: []string{}},
		{Name: "repo-tool", Source: "config", Transport: "stdio", Command: "npx", Status: UserServerFailed, Detail: "waits for approval in this project", args: []string{"-y", "repo-tool"}},
	}
	if !reflect.DeepEqual(servers, want) {
		t.Fatalf("servers =\n%#v\nwant\n%#v", servers, want)
	}
}

func TestClaudesDetailsOfOneServerAreRead(t *testing.T) {
	output := "linear-server:\n  Scope: User config (available in all your projects)\n  Status: ! Needs authentication\n  Type: http\n  URL: https://mcp.linear.app/mcp\n  Headers:\n    X-Source: plugin\n\nTo remove this server, run: claude mcp remove linear-server -s user\n"
	server, ok := parseClaudeGet("linear-server", []byte(output))
	want := UserServer{Name: "linear-server", Source: "config", Transport: "http", URL: "https://mcp.linear.app/mcp", Status: UserServerNeedsSignIn, Detail: "needs sign-in", SignIn: true}
	if !ok || !reflect.DeepEqual(server, want) {
		t.Fatalf("server = %#v, want %#v", server, want)
	}
	// A server's environment is never read: it is the CLI's secret.
	output = "notes:\n  Scope: Local config (private to you in this project)\n  Status: ✔ Connected\n  Type: stdio\n  Command: python3\n  Args: -I /opt/notes/server.py\n  Environment:\n    NOTES_TOKEN=secret-value\n"
	server, _ = parseClaudeGet("notes", []byte(output))
	if server.Command != "python3" || !slices.Equal(server.args, []string{"-I", "/opt/notes/server.py"}) || server.Status != UserServerReady || server.SignIn {
		t.Fatalf("stdio server = %#v", server)
	}
	if strings.Contains(strings.Join(append(server.args, server.Command, server.Detail), " "), "secret") {
		t.Fatal("a server's secret was read")
	}
}

// userServersRequest is a read-only Claude Code call given a declared server
// and three of the user's own: one from their configuration, a connector,
// and a plugin's.
func userServersRequest(t *testing.T) RunRequest {
	request := grantedRequest()
	request.WorkingDir, request.MCPDir = t.TempDir(), filepath.Join(t.TempDir(), "mcp")
	request.MCPServers = request.MCPServers[:1]
	request.UserServers = []UserServer{
		{Name: "linear-server", Source: "config", Transport: "http", URL: "https://mcp.linear.app/mcp", Status: UserServerReady},
		{Name: "claude.ai Gmail", Source: "claude.ai", Transport: "http", URL: "https://gmailmcp.googleapis.com/mcp/v1", Status: UserServerReady},
		{Name: "plugin:tools:files", Source: "plugin", Transport: "stdio", Command: "node", args: []string{"files.js"}, Status: UserServerReady},
	}
	return request
}

func TestClaudeGivenTheUsersServersLoadsOnlyThoseFromTheirConfiguration(t *testing.T) {
	claude := adapter(t, Spec{Name: domain.RuntimeClaude, UserMCP: []string{"linear-server", "claude.ai Gmail", "plugin:tools:files"}})
	request, cleanup, err := claude.prepareMCP(userServersRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	args := claude.arguments(request, "")
	// Without the user's settings source, Claude Code starts none of the
	// servers in its user configuration; the repository's still stay out.
	if contains(args, "--strict-mcp-config") || !containsPair(args, "--mcp-config", request.mcpFile) || !containsPair(args, "--setting-sources", "user") {
		t.Fatalf("Claude Code must load the user's configuration beside the call's file: %v", args)
	}
	settings := claudeSettings(t, args)
	wantAllowed := []any{
		map[string]any{"serverName": "docs-search"},
		map[string]any{"serverName": "linear-server"},
		map[string]any{"serverUrl": "https://gmailmcp.googleapis.com/mcp/v1"},
		map[string]any{"serverCommand": []any{"node", "files.js"}},
	}
	if got := settings["allowedMcpServers"]; !reflect.DeepEqual(got, wantAllowed) {
		t.Fatalf("allowedMcpServers = %v, want %v", got, wantAllowed)
	}
	allowed := flagValues(args, "--allowedTools")
	for _, rule := range []string{"mcp__linear-server", "mcp__claude_ai_Gmail", "mcp__plugin_tools_files", "mcp__docs-search__search"} {
		if !slices.Contains(allowed, rule) {
			t.Fatalf("%s must be allowed: %v", rule, allowed)
		}
	}
	// The shell is sandboxed away from the user's server configuration,
	// whose secrets Inkuna does not read, and so are the file tools.
	sandbox, _ := settings["sandbox"].(map[string]any)
	filesystem, _ := sandbox["filesystem"].(map[string]any)
	denied, _ := filesystem["denyRead"].([]any)
	config := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
	// Claude Code keeps earlier copies of .claude.json in its backups.
	backups := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "backups")
	if sandbox["enabled"] != true || !slices.Contains(denied, any(config)) || !slices.Contains(denied, any(backups)) || !slices.Contains(denied, any(request.MCPDir)) {
		t.Fatalf("sandbox = %v", sandbox)
	}
	rules := flagValues(args, "--disallowedTools")
	if !slices.Contains(rules, "Read(//"+strings.TrimPrefix(filepath.ToSlash(config), "/")+")") || !slices.Contains(rules, "Read(//"+strings.TrimPrefix(filepath.ToSlash(backups), "/")+"/**)") {
		t.Fatalf("the file tools must be kept out of %s and everything under %s: %v", config, backups, rules)
	}

	// Given only the user's servers, no file of servers is written.
	only := userServersRequest(t)
	only.MCPServers = nil
	request, cleanup, err = claude.prepareMCP(only)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	args = claude.arguments(request, "")
	if request.mcpFile != "" || contains(args, "--mcp-config") || contains(args, "--strict-mcp-config") || request.claudeSettingsFile == "" {
		t.Fatalf("a call given only the user's servers: %v", args)
	}
}

func TestClaudeGivenTheUsersServersStillSkipsTheRepositorysSettings(t *testing.T) {
	claude := adapter(t, Spec{Name: domain.RuntimeClaude, UserMCP: []string{"linear-server"}})
	request := userServersRequest(t)
	if err := os.MkdirAll(filepath.Join(request.WorkingDir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(request.WorkingDir, ".claude", "settings.json"), []byte(`{"enableAllProjectMcpServers": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := claude.prepareMCP(request)
	if err != nil {
		t.Fatalf("a repository setting the worker never loads must not stop the call: %v", err)
	}
	defer cleanup()
	if args := claude.arguments(prepared, ""); !containsPair(args, "--setting-sources", "user") {
		t.Fatalf("only the user's settings load beside the call's: %v", args)
	}
}

func TestClaudeRefusesAUserSettingThatWouldWidenAGrantWhenItLoadsTheirServers(t *testing.T) {
	claude := adapter(t, Spec{Name: domain.RuntimeClaude, UserMCP: []string{"linear-server"}})
	settings := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	for _, check := range []struct{ contents, refused string }{
		{`{"permissions":{"allow":["mcp__docs-search"]}}`, "mcp__docs-search"},
		{`{"allowedMcpServers":[{"serverName":"other"}]}`, "allowedMcpServers"},
	} {
		if err := os.WriteFile(settings, []byte(check.contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := claude.prepareMCP(userServersRequest(t)); err == nil || !strings.Contains(err.Error(), check.refused) {
			t.Errorf("%s must stop the call: %v", check.contents, err)
		}
	}
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeRefusesAUserServerWithAGrantedServersToolNames(t *testing.T) {
	claude := adapter(t, Spec{Name: domain.RuntimeClaude, UserMCP: []string{"Docs-Search"}})
	request := userServersRequest(t)
	request.UserServers = []UserServer{{Name: "Docs-Search", Source: "config", Status: UserServerReady}}
	if _, _, err := claude.prepareMCP(request); err == nil || !strings.Contains(err.Error(), `granted server "docs-search"`) {
		t.Fatalf("a user server that shares a granted server's tool names must stop the call: %v", err)
	}
}

func TestClaudeNeedsItsSandboxForTheUsersServers(t *testing.T) {
	defer func(previous func() string) { claudeSandboxMissing = previous }(claudeSandboxMissing)
	claudeSandboxMissing = func() string { return "Claude Code's sandbox needs bwrap" }
	claude := adapter(t, Spec{Name: domain.RuntimeClaude, UserMCP: []string{"linear-server"}})
	request := userServersRequest(t)
	request.MCPServers = nil
	if _, _, err := claude.prepareMCP(request); err == nil || !strings.Contains(err.Error(), "your own MCP servers") {
		t.Fatalf("a call given the user's servers must not start unsandboxed: %v", err)
	}
}

// codexUser is one of the user's Codex servers as its listing and details
// report it.
func codexUser(name string, entry codexServer) UserServer {
	entry.Name = name
	return UserServer{Name: name, Source: "config", Status: UserServerUnknown, codex: &entry}
}

func TestCodexIsHandedTheUsersServersWithTheirSecretsKeptFromItsShell(t *testing.T) {
	t.Setenv("NOTES_FROM_ENV", "notes-env-secret")
	t.Setenv("WIKI_BEARER", "wiki-bearer-secret")
	var notes, wiki codexServer
	notes.Transport.Type, notes.Transport.Command, notes.Transport.Args = "stdio", "python3", []string{"-I", "notes.py"}
	notes.Transport.Env = map[string]string{"NOTES_TOKEN": "notes-literal-secret", "PATH": "/opt/notes/bin"}
	notes.Transport.EnvVars = []json.RawMessage{json.RawMessage(`"NOTES_FROM_ENV"`), json.RawMessage(`{"name":"UNSET_VARIABLE"}`)}
	notes.Transport.Cwd = "/opt/notes"
	notes.DisabledTools = []string{"delete_note"}
	wiki.Transport.Type, wiki.Transport.URL, wiki.Transport.BearerTokenEnvVar = "streamable_http", "https://wiki.example/mcp", "WIKI_BEARER"
	wiki.Transport.HTTPHeaders = map[string]string{"X-Team": "team-header-secret"}
	wiki.EnabledTools = []string{"search"}
	codex := adapter(t, Spec{Name: domain.RuntimeCodex, UserMCP: []string{"notes", "wiki"}})
	request := RunRequest{RunID: "run", Prompt: []byte("look"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
		UserServers: []UserServer{codexUser("notes", notes), codexUser("wiki", wiki)}}
	request, _, err := codex.prepareMCP(request)
	if err != nil {
		t.Fatal(err)
	}
	args := codex.arguments(request, "/schema.json")
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		`mcp_servers.notes.command="python3"`, `mcp_servers.notes.args=["-I","notes.py"]`, `mcp_servers.notes.env_vars=["NOTES_FROM_ENV","NOTES_TOKEN"]`,
		`mcp_servers.notes.env={"PATH"="/opt/notes/bin"}`, `mcp_servers.notes.cwd="/opt/notes"`, `mcp_servers.notes.disabled_tools=["delete_note"]`,
		"mcp_servers.notes.required=true", `mcp_servers.notes.default_tools_approval_mode="approve"`,
		`mcp_servers.wiki.url="https://wiki.example/mcp"`, `mcp_servers.wiki.bearer_token_env_var="INKUNA_MCP_WIKI_BEARER"`,
		`mcp_servers.wiki.env_http_headers={"X-Team"="INKUNA_MCP_WIKI_HEADER_0"}`, `mcp_servers.wiki.enabled_tools=["search"]`,
		`shell_environment_policy.exclude=["NOTES_FROM_ENV","NOTES_TOKEN","INKUNA_MCP_WIKI_BEARER","INKUNA_MCP_WIKI_HEADER_0"]`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Codex must get %s: %v", want, args)
		}
	}
	if !contains(args, "--ignore-user-config") || strings.Contains(joined, "secret") {
		t.Fatalf("the user's servers must come without config.toml or a secret in the arguments: %v", args)
	}
	environment := strings.Join(codex.environment(request), "\n")
	for _, want := range []string{"NOTES_TOKEN=notes-literal-secret", "NOTES_FROM_ENV=notes-env-secret", "INKUNA_MCP_WIKI_BEARER=wiki-bearer-secret", "INKUNA_MCP_WIKI_HEADER_0=team-header-secret"} {
		if !strings.Contains(environment, want) {
			t.Fatalf("Codex's environment must hold %s", want)
		}
	}
	if strings.Contains(environment, "UNSET_VARIABLE") {
		t.Fatal("a variable Inkuna does not have must be left out")
	}
	if scrub := secretScrubber(request.MCPServers, nil); scrub("header team-header-secret") != "header [REDACTED]" {
		t.Fatal("a server's header must be scrubbed from what Inkuna keeps")
	}
}

func TestCodexLoadingTheUsersConfigurationAllowsTheServersItIsGiven(t *testing.T) {
	codex := adapter(t, Spec{Name: domain.RuntimeCodex, UserConfig: true, UserMCP: []string{"wiki"}})
	request := RunRequest{RunID: "run", Prompt: []byte("look"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
		UserServers: []UserServer{{Name: "wiki", Source: "config", Status: UserServerUnknown}}}
	request, _, err := codex.prepareMCP(request)
	if err != nil {
		t.Fatal(err)
	}
	args := codex.arguments(request, "/schema.json")
	if contains(args, "--ignore-user-config") || !contains(args, `mcp_servers.wiki.default_tools_approval_mode="approve"`) || !contains(args, "mcp_servers.wiki.required=true") || strings.Contains(strings.Join(args, " "), "mcp_servers.wiki.url") {
		t.Fatalf("the server comes from config.toml, approved and required: %v", args)
	}
}

func TestCodexSaysWhichOfTheUsersServersDidNotStart(t *testing.T) {
	servers := []UserServer{{Name: "linear", SignIn: true}, {Name: "notes"}}
	failure, ok := codexRequiredFailure("Error: thread/start failed: Fatal error: Failed to initialize session: required MCP servers failed to initialize: linear: OAuth authorization required (code -32603)", servers)
	if !ok || failure.Server != "linear" || failure.Reason != "OAuth authorization required" || !failure.SignIn {
		t.Fatalf("failure = %#v", failure)
	}
	failure, ok = codexRequiredFailure("required MCP servers failed to initialize: docs: timed out; notes: exited with 1", servers)
	if !ok || failure.Server != "notes" || failure.Reason != "exited with 1" || failure.SignIn {
		t.Fatalf("failure = %#v", failure)
	}
	if _, ok := codexRequiredFailure("required MCP servers failed to initialize: docs: timed out", servers); ok {
		t.Fatal("a declared server's failure is not one of the user's")
	}
}

func TestTheStreamSaysWhichOfTheUsersServersDidNotStart(t *testing.T) {
	given := map[string]bool{"claude.ai Gmail": true, "notes": true}
	init := map[string]any{"type": "system", "subtype": "init", "mcp_servers": []any{
		map[string]any{"name": "claude.ai Gmail", "status": "needs-auth"},
		map[string]any{"name": "notes", "status": "connected"},
		map[string]any{"name": "docs-search", "status": "failed"},
	}}
	problems := userServerProblems(domain.RuntimeClaude, init, given)
	if len(problems) != 1 || !strings.Contains(problems["claude.ai Gmail"], "sign in") {
		t.Fatalf("problems = %v", problems)
	}
	loaded := map[string]any{"type": "session.mcp_servers_loaded", "data": map[string]any{"servers": []any{
		map[string]any{"name": "notes", "status": "failed", "error": "connection closed"},
	}}}
	if problems := userServerProblems(domain.RuntimeCopilot, loaded, given); !strings.Contains(problems["notes"], "connection closed") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestEachCLIRefusesTheUsersServerNamesItCannotTake(t *testing.T) {
	for _, check := range []struct {
		kind domain.Runtime
		name string
		ok   bool
	}{
		{domain.RuntimeClaude, "claude.ai Gmail", true},
		// Claude Code would read mcp__docs-search__read as the read tool of
		// a server named docs-search.
		{domain.RuntimeClaude, "docs-search  read", false},
		{domain.RuntimeClaude, "docs__search", false},
		{domain.RuntimeCodex, "my_server-2", true},
		{domain.RuntimeCodex, "my server", false},
		{domain.RuntimeCopilot, "notes.v2", true},
		{domain.RuntimeCopilot, "write", false},
		{domain.RuntimeCopilot, "a,b", false},
	} {
		if got := userServerNameProblem(check.kind, check.name) == ""; got != check.ok {
			t.Errorf("%s %q: usable = %v, want %v", check.kind, check.name, got, check.ok)
		}
	}
	for _, names := range [][]string{{""}, {" padded"}, {"a\nb"}, {"same", "same"}} {
		if err := (Spec{Name: domain.RuntimeClaude, UserMCP: names}).Validate(); err == nil {
			t.Errorf("userMcp %q must be refused", names)
		}
	}
	if err := (Spec{Name: "gemini", Binary: "gemini", Output: OutputText, Stdin: StdinPrompt, ReadOnlyArgs: []string{"--read-only"}, UserMCP: []string{"notes"}}).Validate(); err == nil {
		t.Fatal("a template cannot be given the user's servers")
	}
}
