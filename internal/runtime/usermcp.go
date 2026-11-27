package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// UserServer is one of the user's own MCP servers, as its CLI reports it:
// what Settings lists to choose from, and what a call on a runtime opted in
// to it is given (Spec.UserMCP). Its secrets are the CLI's: Claude Code and
// Copilot start the server from their own configuration, and only Codex's,
// which a call that skips config.toml must be handed, is read, and kept
// unexported so it is never shown or stored.
type UserServer struct {
	Name string `json:"name"`
	// Source is where the CLI found the server: for Claude Code "config"
	// (its user, local, or project scope), "claude.ai" (a connector of the
	// signed-in account), or "plugin"; for Codex "config"; for Copilot what
	// its listing says ("user", "workspace", "plugin").
	Source string `json:"source,omitempty"`
	// Transport is "stdio" or "http". URL is an HTTP server's endpoint, and
	// Command a stdio server's program, without its arguments.
	Transport string `json:"transport,omitempty"`
	URL       string `json:"url,omitempty"`
	Command   string `json:"command,omitempty"`
	// Status is what the CLI says of the server now (UserServerReady and the
	// rest), and Detail why, when it says.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	// SignIn reports that the CLI can sign in to the server itself
	// (SignInUserServer): an HTTP server on Claude Code or Codex.
	SignIn bool `json:"signIn,omitempty"`

	// args are a Claude Code stdio server's arguments, as its listing splits
	// them, which a plugin's server is allowed by.
	args []string
	// codex is the server's Codex configuration, secrets included.
	codex *codexServer
}

// What a CLI says of one of the user's servers.
const (
	UserServerReady       = "ready"
	UserServerNeedsSignIn = "needs-sign-in"
	UserServerFailed      = "failed"
	// UserServerUnknown is a server the CLI says nothing of until a call
	// starts it.
	UserServerUnknown  = "unknown"
	UserServerMissing  = "missing"
	UserServerDisabled = "disabled"
)

// Usable reports whether a call can be given the server: one that needs
// signing in, failed, is gone, or is switched off cannot.
func (server UserServer) Usable() bool {
	return server.Status == UserServerReady || server.Status == UserServerUnknown
}

// UserServers is a runtime that can list the user's own MCP servers and sign
// in to them: every built-in CLI.
type UserServers interface {
	// ListUserServers lists every MCP server the CLI's own setup would give
	// a worker started in dir, with what the CLI says of each.
	ListUserServers(ctx context.Context, dir string) ([]UserServer, error)
	// ResolveUserServers reports on the named servers only, in that order,
	// as a call started in dir is about to be given them. One the CLI no
	// longer lists is UserServerMissing, and one the runtime cannot be given
	// by that name is UserServerFailed.
	ResolveUserServers(ctx context.Context, dir string, names []string) ([]UserServer, error)
	// SignInUserServer runs the CLI's own sign-in for a server, which opens
	// the browser, and returns when it ends.
	SignInUserServer(ctx context.Context, dir, name string) error
}

// UserServerError reports that one of the user's own servers a call was
// given did not start, so the call stopped before the model ran. Codex is
// told each server is required; Claude Code and Copilot go on without it and
// say so in the run's events instead.
type UserServerError struct {
	Server string
	Reason string
	// SignIn says the server needs the user to sign in to it.
	SignIn bool
}

func (failure *UserServerError) Error() string {
	return fmt.Sprintf("your MCP server %q did not start: %s", failure.Server, failure.Reason)
}

// MaxUserServerNameBytes bounds the name of a user's server Inkuna passes on.
const MaxUserServerNameBytes = 200

// ValidUserServerName reports whether a name can name one of the user's
// servers in userMcp: printable, trimmed, and short. Each CLI narrows it
// further when it lists its servers (userServerNameProblem).
func ValidUserServerName(name string) bool {
	if name == "" || name != strings.TrimSpace(name) || len(name) > MaxUserServerNameBytes {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validateUserMCP(runtime domain.Runtime, names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !ValidUserServerName(name) {
			return fmt.Errorf("runtime %q: userMcp names %q, which is not the name of an MCP server: use a server's name as the CLI lists it", runtime, name)
		}
		if seen[name] {
			return fmt.Errorf("runtime %q: userMcp names %q twice", runtime, name)
		}
		seen[name] = true
	}
	return nil
}

var (
	// claudeToolUnsafe is what Claude Code replaces in a server's name to
	// make its tools' prefix: "claude.ai Gmail" becomes mcp__claude_ai_Gmail.
	claudeToolUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]`)
	// codexServerName is what Codex accepts as a server's name.
	codexServerName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// copilotServerName is a name Copilot's tool allowlist reads as one
	// server: no comma, space, or parenthesis.
	copilotServerName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// userServerNameProblem says why a runtime cannot be given one of the
// user's servers by its name, or "". Copilot reads a server's name alone as
// all its tools, so a name it already gives a tool or a permission kind
// would hand the step that instead.
func userServerNameProblem(kind domain.Runtime, name string) string {
	switch kind {
	case domain.RuntimeClaude:
		// Claude Code splits mcp__<server>__<tool> at the first double
		// underscore, so the rule would allow one tool of another server.
		if strings.Contains(claudeToolPrefix(name), "__") {
			return "Claude Code would read the double underscore in its tools' names as the start of a tool's name"
		}
	case domain.RuntimeCodex:
		if !codexServerName.MatchString(name) {
			return "Codex takes only letters, digits, underscores, and dashes in a server's name"
		}
	case domain.RuntimeCopilot:
		if !copilotServerName.MatchString(name) {
			return "Copilot's tool allowlist cannot name a server with that name"
		}
		if reservedMCPServerNames[strings.ToLower(name)] {
			return "Copilot reads that name as one of its own tools, not the server"
		}
	}
	return ""
}

// userServerTimeout bounds a CLI's listing, which Claude Code's spends
// connecting to every server.
const userServerTimeout = 90 * time.Second

// signInTimeout bounds a sign-in, which waits for the user in the browser.
const signInTimeout = 10 * time.Minute

func (runtime *commandRuntime) ListUserServers(ctx context.Context, dir string) ([]UserServer, error) {
	switch runtime.spec.effectivePreset() {
	case domain.RuntimeClaude:
		output, err := runtime.userServerCommand(ctx, dir, "mcp", "list")
		if err != nil {
			return nil, err
		}
		return runtime.checked(parseClaudeList(output)), nil
	case domain.RuntimeCodex:
		servers, err := runtime.codexListing(ctx, dir)
		if err != nil {
			return nil, err
		}
		return runtime.checked(servers), nil
	case domain.RuntimeCopilot:
		servers, err := runtime.copilotListing(ctx, dir)
		if err != nil {
			return nil, err
		}
		return runtime.checked(servers), nil
	default:
		return nil, fmt.Errorf("runtime %q is an argument template, which cannot be given MCP servers", runtime.spec.Name)
	}
}

func (runtime *commandRuntime) ResolveUserServers(ctx context.Context, dir string, names []string) ([]UserServer, error) {
	if len(names) == 0 {
		return nil, nil
	}
	var listed []UserServer
	var err error
	switch runtime.spec.effectivePreset() {
	case domain.RuntimeClaude:
		listed, err = runtime.claudeResolve(ctx, dir, names)
	case domain.RuntimeCodex:
		listed, err = runtime.codexListing(ctx, dir)
		if err == nil {
			err = runtime.codexDetails(ctx, dir, listed, names)
		}
	case domain.RuntimeCopilot:
		listed, err = runtime.copilotListing(ctx, dir)
	default:
		return nil, fmt.Errorf("runtime %q is an argument template, which cannot be given MCP servers", runtime.spec.Name)
	}
	if err != nil {
		return nil, err
	}
	byName := make(map[string]UserServer, len(listed))
	for _, server := range runtime.checked(listed) {
		byName[server.Name] = server
	}
	resolved := make([]UserServer, 0, len(names))
	for _, name := range names {
		server, ok := byName[name]
		if !ok {
			server = UserServer{Name: name, Status: UserServerMissing, Detail: string(runtime.spec.Name) + " no longer lists it"}
		}
		resolved = append(resolved, server)
	}
	return resolved, nil
}

// checked marks the servers this runtime cannot be given by their name.
func (runtime *commandRuntime) checked(servers []UserServer) []UserServer {
	for index := range servers {
		if problem := userServerNameProblem(runtime.spec.effectivePreset(), servers[index].Name); problem != "" {
			servers[index].Status, servers[index].Detail, servers[index].SignIn = UserServerFailed, problem, false
		}
	}
	return servers
}

func (runtime *commandRuntime) SignInUserServer(ctx context.Context, dir, name string) error {
	if !ValidUserServerName(name) {
		return fmt.Errorf("%q is not the name of an MCP server", name)
	}
	switch runtime.spec.effectivePreset() {
	case domain.RuntimeClaude, domain.RuntimeCodex:
	case domain.RuntimeCopilot:
		return errors.New("there is no sign-in command for a Copilot MCP server: run copilot in a terminal and sign in to it with /mcp")
	default:
		return fmt.Errorf("runtime %q is an argument template, which cannot be given MCP servers", runtime.spec.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, signInTimeout)
	defer cancel()
	// The name is one argument after the subcommand, so a name that starts
	// with a dash is still a name.
	command := exec.CommandContext(ctx, runtime.binary, "mcp", "login", "--", name)
	command.Dir = dir
	command.Env = runtime.environment(RunRequest{})
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("sign in to MCP server %q: %w: %s", name, err, redact(truncate(strings.TrimSpace(output.String()), 512)))
	}
	return nil
}

// userServerCommand runs one of the CLI's MCP subcommands with the worker's
// environment and returns its standard output.
func (runtime *commandRuntime) userServerCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, userServerTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, runtime.binary, args...)
	command.Dir = dir
	command.Env = runtime.environment(RunRequest{})
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w: %s", runtime.spec.Name, strings.Join(args, " "), err, redact(truncate(strings.TrimSpace(stderr.String()), 512)))
	}
	return output, nil
}

// claudeResolve reports on the named Claude Code servers. A connector's
// URL, which is what allows it (claudeAllowedServers), is only in the full
// listing, which connects to every server; otherwise each server is asked
// on its own.
func (runtime *commandRuntime) claudeResolve(ctx context.Context, dir string, names []string) ([]UserServer, error) {
	for _, name := range names {
		if strings.HasPrefix(name, "claude.ai ") {
			output, err := runtime.userServerCommand(ctx, dir, "mcp", "list")
			if err != nil {
				return nil, err
			}
			return parseClaudeList(output), nil
		}
	}
	servers := make([]UserServer, 0, len(names))
	for _, name := range names {
		output, err := runtime.userServerCommand(ctx, dir, "mcp", "get", "--", name)
		if err != nil {
			// Claude Code exits non-zero for a name it does not know.
			if strings.Contains(err.Error(), "No MCP server found") {
				continue
			}
			return nil, err
		}
		if server, ok := parseClaudeGet(name, output); ok {
			servers = append(servers, server)
		}
	}
	return servers, nil
}

// parseClaudeList reads `claude mcp list`, which prints one server a line:
// "<name>: <URL or command line>[ (HTTP)] - <status>". A name can hold
// spaces and colons ("claude.ai Gmail", "plugin:postman:postman") but not
// ": ", and a status never holds " - ".
func parseClaudeList(output []byte) []UserServer {
	var servers []UserServer
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		name, rest, ok := strings.Cut(line, ": ")
		if !ok || name == "" {
			continue
		}
		cut := strings.LastIndex(rest, " - ")
		if cut < 0 {
			continue
		}
		target, status := strings.TrimSpace(rest[:cut]), strings.TrimSpace(rest[cut+3:])
		server := UserServer{Name: name, Source: claudeSource(name, "")}
		for _, suffix := range []string{" (HTTP)", " (SSE)"} {
			if trimmed, found := strings.CutSuffix(target, suffix); found {
				target, server.Transport = trimmed, "http"
			}
		}
		if server.Transport == "" && (strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "http://")) {
			server.Transport = "http"
		}
		if server.Transport == "http" {
			server.URL = target
		} else {
			server.Transport = "stdio"
			fields := strings.Fields(target)
			if len(fields) > 0 {
				server.Command, server.args = fields[0], fields[1:]
			}
		}
		server.Status, server.Detail = claudeStatus(status)
		server.SignIn = server.Transport == "http" && server.Status != UserServerFailed
		servers = append(servers, server)
	}
	return servers
}

// parseClaudeGet reads `claude mcp get <name>`: the name, then indented
// "Key: value" lines.
func parseClaudeGet(name string, output []byte) (UserServer, bool) {
	server := UserServer{Name: name, Transport: "stdio"}
	found := false
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok || !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "    ") {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Scope":
			server.Source, found = claudeSource(name, value), true
		case "Status":
			server.Status, server.Detail = claudeStatus(value)
		case "Type":
			if value != "stdio" {
				server.Transport = "http"
			}
		case "URL":
			server.URL = value
		case "Command":
			server.Command = value
		case "Args":
			server.args = strings.Fields(value)
		}
	}
	if server.Status == "" {
		server.Status = UserServerUnknown
	}
	server.SignIn = server.Transport == "http" && server.Status != UserServerFailed
	return server, found
}

func claudeSource(name, scope string) string {
	switch {
	case strings.HasPrefix(name, "claude.ai ") || strings.HasPrefix(scope, "claude.ai"):
		return "claude.ai"
	case strings.HasPrefix(name, "plugin:"):
		return "plugin"
	default:
		return "config"
	}
}

// claudeStatus reads a status Claude Code prints after its mark.
func claudeStatus(text string) (string, string) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "needs authentication"):
		return UserServerNeedsSignIn, "needs sign-in"
	case strings.Contains(lower, "connected"):
		return UserServerReady, ""
	case strings.Contains(lower, "pending approval"):
		return UserServerFailed, "waits for approval in this project"
	case strings.Contains(lower, "failed"):
		return UserServerFailed, strings.TrimSpace(strings.TrimLeft(text, "✗✘×! "))
	default:
		return UserServerUnknown, strings.TrimSpace(strings.TrimLeft(text, "✔✓✗✘×!⏸ "))
	}
}

// codexServer is one server's entry in Codex's listing and details.
type codexServer struct {
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	AuthStatus string `json:"auth_status"`
	Transport  struct {
		Type              string            `json:"type"`
		URL               string            `json:"url"`
		BearerTokenEnvVar string            `json:"bearer_token_env_var"`
		HTTPHeaders       map[string]string `json:"http_headers"`
		EnvHTTPHeaders    map[string]string `json:"env_http_headers"`
		Command           string            `json:"command"`
		Args              []string          `json:"args"`
		Env               map[string]string `json:"env"`
		EnvVars           []json.RawMessage `json:"env_vars"`
		Cwd               string            `json:"cwd"`
	} `json:"transport"`
	EnabledTools      []string `json:"enabled_tools"`
	DisabledTools     []string `json:"disabled_tools"`
	StartupTimeoutSec *float64 `json:"startup_timeout_sec"`
	ToolTimeoutSec    *float64 `json:"tool_timeout_sec"`
}

// codexListing reads `codex mcp list --json`. Codex connects to nothing to
// list, so only its sign-in state is known before a call.
func (runtime *commandRuntime) codexListing(ctx context.Context, dir string) ([]UserServer, error) {
	output, err := runtime.userServerCommand(ctx, dir, "mcp", "list", "--json")
	if err != nil {
		return nil, err
	}
	var listing []codexServer
	if err := json.Unmarshal(output, &listing); err != nil {
		return nil, fmt.Errorf("read Codex's MCP server list: %w", err)
	}
	servers := make([]UserServer, 0, len(listing))
	for index := range listing {
		entry := &listing[index]
		server := UserServer{Name: entry.Name, Source: "config", URL: entry.Transport.URL, Command: entry.Transport.Command, codex: entry}
		server.Transport = "stdio"
		if entry.Transport.Type != "stdio" {
			server.Transport = "http"
		}
		switch {
		case !entry.Enabled:
			server.Status, server.Detail = UserServerDisabled, "switched off in config.toml"
		case entry.AuthStatus == "not_logged_in":
			server.Status, server.Detail = UserServerNeedsSignIn, "needs sign-in"
		case entry.AuthStatus == "o_auth":
			server.Status, server.Detail = UserServerReady, "signed in"
		case entry.AuthStatus == "bearer_token":
			server.Status = UserServerReady
		default:
			server.Status, server.Detail = UserServerUnknown, "starts with each call"
		}
		server.SignIn = server.Transport == "http" && entry.Transport.BearerTokenEnvVar == ""
		servers = append(servers, server)
	}
	return servers, nil
}

// codexDetails adds each named server's tool filters, which only `codex mcp
// get` reports, to its listed configuration.
func (runtime *commandRuntime) codexDetails(ctx context.Context, dir string, listed []UserServer, names []string) error {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	for index := range listed {
		server := &listed[index]
		if !wanted[server.Name] || server.codex == nil || userServerNameProblem(domain.RuntimeCodex, server.Name) != "" {
			continue
		}
		output, err := runtime.userServerCommand(ctx, dir, "mcp", "get", "--json", "--", server.Name)
		if err != nil {
			return err
		}
		var details codexServer
		if err := json.Unmarshal(output, &details); err != nil {
			return fmt.Errorf("read Codex's MCP server %q: %w", server.Name, err)
		}
		details.AuthStatus, details.Name = server.codex.AuthStatus, server.Name
		server.codex = &details
	}
	return nil
}

// copilotListing reads `copilot mcp list --json`. Copilot says nothing of a
// server until a call starts it, and has no sign-in command.
func (runtime *commandRuntime) copilotListing(ctx context.Context, dir string) ([]UserServer, error) {
	output, err := runtime.userServerCommand(ctx, dir, "mcp", "list", "--json")
	if err != nil {
		return nil, err
	}
	var listing struct {
		MCPServers map[string]struct {
			Type    string `json:"type"`
			URL     string `json:"url"`
			Command string `json:"command"`
			Source  string `json:"source"`
			Enabled *bool  `json:"enabled"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(output, &listing); err != nil {
		return nil, fmt.Errorf("read Copilot's MCP server list: %w", err)
	}
	servers := make([]UserServer, 0, len(listing.MCPServers))
	for name, entry := range listing.MCPServers {
		server := UserServer{Name: name, Source: entry.Source, URL: entry.URL, Command: entry.Command, Transport: "stdio", Status: UserServerUnknown, Detail: "starts with each call"}
		if entry.Type == "http" || entry.Type == "sse" || entry.URL != "" {
			server.Transport = "http"
		}
		if entry.Enabled != nil && !*entry.Enabled {
			server.Status, server.Detail = UserServerDisabled, "switched off in Copilot's configuration"
		}
		servers = append(servers, server)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return servers, nil
}

// claudeAllowedServers is the allowedMcpServers setting for a Claude Code
// call given the user's own servers, which then load from the user's
// configuration rather than one strict file. Claude Code starts only the
// servers it lists: the call's declared servers and the user's own by name,
// a connector or a plugin's HTTP server by its URL, and a plugin's stdio
// server by its command, the only ways it matches those.
func claudeAllowedServers(request RunRequest) []any {
	allowed := make([]any, 0, len(request.MCPServers)+len(request.UserServers))
	for _, server := range request.MCPServers {
		allowed = append(allowed, map[string]any{"serverName": server.Name})
	}
	for _, server := range request.UserServers {
		switch {
		case server.Source == "config":
			allowed = append(allowed, map[string]any{"serverName": server.Name})
		case server.URL != "":
			allowed = append(allowed, map[string]any{"serverUrl": server.URL})
		case server.Command != "":
			allowed = append(allowed, map[string]any{"serverCommand": append([]string{server.Command}, server.args...)})
		}
	}
	return allowed
}

// claudeUserRules are the --allowedTools rules for the user's own servers,
// each allowed whole by its tools' prefix.
func claudeUserRules(servers []UserServer) []string {
	rules := make([]string, 0, len(servers))
	for _, server := range servers {
		rules = append(rules, "mcp__"+claudeToolPrefix(server.Name))
	}
	return rules
}

// claudeToolPrefix is the name Claude Code puts in a server's tools' names.
func claudeToolPrefix(name string) string {
	return claudeToolUnsafe.ReplaceAllString(name, "_")
}

// codexUserServers turns the user's Codex servers into servers a call that
// skips config.toml is given: each whole unless the user's configuration
// filters its tools, and its secrets by value, which Codex hands the server
// from its own environment and keeps from the shell, as for a declared
// server. A variable the configuration only names comes from Inkuna's own
// environment, and is left out when Inkuna has none. A literal variable
// every CLI is started with, such as PATH, is no secret, and goes in the
// server's own env, since Codex's environment already has one.
func codexUserServers(servers []UserServer) []MCPServer {
	converted := make([]MCPServer, 0, len(servers))
	for _, server := range servers {
		entry := server.codex
		if entry == nil {
			continue
		}
		mcp := MCPServer{Name: server.Name, Tools: append([]string(nil), entry.EnabledTools...), user: true, codexExtra: map[string]any{}}
		transport := entry.Transport
		if transport.Type == "stdio" {
			mcp.Command = append([]string{transport.Command}, transport.Args...)
			mcp.Env = map[string]string{}
			plain := map[string]string{}
			for name, value := range transport.Env {
				if ReservedVariable(name) {
					plain[name] = value
					continue
				}
				mcp.Env[name] = value
			}
			for _, raw := range transport.EnvVars {
				if name := codexVariableName(raw); name != "" && mcp.Env[name] == "" {
					if value, ok := os.LookupEnv(name); ok && !ReservedVariable(name) {
						mcp.Env[name] = value
					}
				}
			}
			if len(plain) > 0 {
				mcp.codexExtra["env"] = plain
			}
			if transport.Cwd != "" {
				mcp.codexExtra["cwd"] = transport.Cwd
			}
		} else {
			mcp.URL = transport.URL
			if name := transport.BearerTokenEnvVar; name != "" {
				mcp.Bearer = os.Getenv(name)
			}
			mcp.headers = map[string]string{}
			for header, value := range transport.HTTPHeaders {
				mcp.headers[header] = value
			}
			for header, name := range transport.EnvHTTPHeaders {
				if value, ok := os.LookupEnv(name); ok {
					mcp.headers[header] = value
				}
			}
		}
		if len(entry.DisabledTools) > 0 {
			mcp.codexExtra["disabled_tools"] = entry.DisabledTools
		}
		if entry.StartupTimeoutSec != nil {
			mcp.codexExtra["startup_timeout_sec"] = *entry.StartupTimeoutSec
		}
		if entry.ToolTimeoutSec != nil {
			mcp.codexExtra["tool_timeout_sec"] = *entry.ToolTimeoutSec
		}
		converted = append(converted, mcp)
	}
	return converted
}

// codexVariableName reads one env_vars entry: a name, or an object naming it.
func codexVariableName(raw json.RawMessage) string {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return name
	}
	var entry struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &entry) == nil {
		return entry.Name
	}
	return ""
}

// codexHeaderVariable names the variable that holds one of a URL server's
// headers in Codex's environment.
func codexHeaderVariable(server string, index int) string {
	return fmt.Sprintf("INKUNA_MCP_%s_HEADER_%d", strings.ToUpper(strings.ReplaceAll(server, "-", "_")), index)
}

// codexConfigKey quotes a server's name in a dotted configuration key when
// it is not a bare TOML key.
func codexConfigKey(name string) string {
	if codexServerName.MatchString(name) {
		return name
	}
	quoted, _ := json.Marshal(name)
	return string(quoted)
}

// codexDisabledArgs switches off every server in the user's config.toml a
// call is not given, for a Codex call that loads the file (userConfig):
// userMcp is the only way a worker starts one. The listing is asked, as for
// Copilot, and one that fails stops the call rather than let a server start
// unseen.
func (runtime *commandRuntime) codexDisabledArgs(ctx context.Context, dir string, given map[string]bool) ([]string, error) {
	listed, err := runtime.codexListing(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("list Codex's MCP servers so the worker starts only those it is given: %w", err)
	}
	var args []string
	for _, server := range listed {
		if !given[server.Name] && server.Status != UserServerDisabled {
			args = append(args, "--config", "mcp_servers."+codexConfigKey(server.Name)+".enabled=false")
		}
	}
	return args, nil
}

// codexOwnServerArgs are the overrides for the user's servers a Codex call
// that loads config.toml is given: each is required, so one that cannot
// start stops Codex before the model runs, and its calls need no approval,
// which exec could not ask for.
func codexOwnServerArgs(servers []UserServer) []string {
	args := make([]string, 0, 4*len(servers))
	for _, server := range servers {
		key := "mcp_servers." + codexConfigKey(server.Name)
		args = append(args, "--config", key+`.default_tools_approval_mode="approve"`, "--config", key+".required=true")
	}
	return args
}

// codexRequiredFailure finds a server Codex says did not start in a line it
// printed: "required MCP servers failed to initialize: <name>: <reason>".
func codexRequiredFailure(line string, servers []UserServer) (*UserServerError, bool) {
	_, rest, ok := strings.Cut(line, "required MCP servers failed to initialize: ")
	if !ok {
		return nil, false
	}
	for _, server := range servers {
		reason, found := strings.CutPrefix(rest, server.Name+": ")
		if !found {
			if _, after, inside := strings.Cut(rest, "; "+server.Name+": "); inside {
				reason, found = after, true
			}
		}
		if !found {
			continue
		}
		if end := strings.Index(reason, "; "); end >= 0 {
			reason = reason[:end]
		}
		reason = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(reason), " (code -32603)"))
		signIn := server.SignIn && strings.Contains(strings.ToLower(reason), "oauth")
		return &UserServerError{Server: server.Name, Reason: redact(truncate(reason, 300)), SignIn: signIn}, true
	}
	return nil, false
}

// userServerProblems reads what Claude Code's init event and Copilot's
// server events say of the user's servers a call was given, and returns a
// note for each that did not start. The call goes on without it: the user's
// own setup is not something a step can fix.
func userServerProblems(kind domain.Runtime, payload map[string]any, given map[string]bool) map[string]string {
	if len(given) == 0 {
		return nil
	}
	rawType, _ := payload["type"].(string)
	var entries []any
	switch {
	case kind == domain.RuntimeClaude && rawType == "system" && payload["subtype"] == "init":
		entries, _ = payload["mcp_servers"].([]any)
	case kind == domain.RuntimeCopilot && rawType == "session.mcp_servers_loaded":
		data, _ := payload["data"].(map[string]any)
		entries, _ = data["servers"].([]any)
	}
	problems := map[string]string{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		name, _ := entry["name"].(string)
		status, _ := entry["status"].(string)
		if !given[name] || (status != "failed" && status != "needs-auth") {
			continue
		}
		reason := "it needs you to sign in"
		if status == "failed" {
			reason = "it failed to start"
			if detail, _ := entry["error"].(string); strings.TrimSpace(detail) != "" {
				reason += ": " + redact(truncate(strings.TrimSpace(detail), 300))
			}
		}
		problems[name] = fmt.Sprintf("Your MCP server %q did not start, so the step goes on without it: %s", name, reason)
	}
	return problems
}

// userServerList is the call's user server names, sorted.
func userServerList(request RunRequest) []string {
	names := make([]string, 0, len(request.UserServers))
	for _, server := range request.UserServers {
		names = append(names, server.Name)
	}
	sort.Strings(names)
	return names
}

// userServerNames is the set of the call's user server names.
func userServerNames(request RunRequest) map[string]bool {
	names := make(map[string]bool, len(request.UserServers))
	for _, server := range request.UserServers {
		names[server.Name] = true
	}
	return names
}
