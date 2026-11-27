package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// MCPServer is a declared MCP server as one call is given it, with its
// secrets' values. Only the runtime sees the values, and it puts them where
// the CLI hands them to the server and nowhere the model reaches: a file
// outside every working directory for Claude Code and Copilot, and for Codex
// its own environment, kept out of the model's shell.
type MCPServer struct {
	Name    string
	Command []string
	URL     string
	// Env maps the variables a command's server reads to their values.
	Env map[string]string
	// Bearer is the value a URL's server is sent as its bearer token.
	Bearer string
	// Tools are the server's tools the worker may call; empty means all.
	Tools []string

	// user marks one of the user's own Codex servers, which a call that
	// skips config.toml is given this way (codexUserServers). headers are
	// its HTTP headers' values, secrets like Env's, and codexExtra the rest
	// of its configuration that Codex is passed as it is.
	user       bool
	headers    map[string]string
	codexExtra map[string]any
}

// reservedMCPServerNames are names a CLI already gives a tool or a
// permission kind. Copilot takes a server's name alone to mean all its tools
// in --available-tools and --allow-tool, so a server called "edit" or "write"
// would hand a read-only step the built-in tool or permission instead.
var reservedMCPServerNames = map[string]bool{
	"bash": true, "powershell": true, "view": true, "create": true, "edit": true, "glob": true, "grep": true,
	"task": true, "skill": true, "sql": true, "shell": true, "write": true, "read": true, "url": true, "memory": true,
}

// ValidMCPServerName reports whether a name can name a declared MCP server:
// the form domain.ValidMCPServerName checks, and not a name a CLI reserves.
func ValidMCPServerName(name string) bool {
	return domain.ValidMCPServerName(name) && !reservedMCPServerNames[name]
}

// ReservedVariable reports whether a variable is one every CLI is started
// with, which a server's environment cannot set: Codex gets a server's
// variables in its own environment, where they would replace the CLI's.
func ReservedVariable(name string) bool {
	return baselineEnvironment[name] || hasBaselinePrefix(name)
}

// prepareMCP readies the call's servers: it refuses servers the runtime
// cannot be given, and writes the configuration file a CLI that reads one is
// pointed at. The returned function removes the file.
func (runtime *commandRuntime) prepareMCP(request RunRequest) (RunRequest, func(), error) {
	if len(request.MCPServers) == 0 && len(request.UserServers) == 0 {
		return request, func() {}, nil
	}
	preset := runtime.spec.effectivePreset()
	switch {
	case preset == "":
		return request, func() {}, fmt.Errorf("runtime %q is an argument template, which cannot be given MCP servers", runtime.spec.Name)
	case request.NoTools:
		return request, func() {}, errors.New("a call with no tools cannot be given MCP servers")
	}
	for _, server := range request.UserServers {
		if problem := userServerNameProblem(preset, server.Name); problem != "" || !ValidUserServerName(server.Name) {
			return request, func() {}, fmt.Errorf("your MCP server %q cannot be given to %s: %s", server.Name, runtime.spec.Name, firstNonEmptyText(problem, "its name is not one Inkuna passes on"))
		}
		if preset == domain.RuntimeCodex && !runtime.spec.UserConfig && server.codex == nil {
			return request, func() {}, fmt.Errorf("your MCP server %q was not read from Codex's configuration, so the call cannot be given it", server.Name)
		}
		if preset == domain.RuntimeClaude {
			// Its allow rule would cover a granted server's tools too,
			// beyond the ones inkuna.yaml lists.
			for _, declared := range request.MCPServers {
				if strings.EqualFold(claudeToolPrefix(server.Name), declared.Name) {
					return request, func() {}, fmt.Errorf("your MCP server %q has the same tool names in Claude Code as the granted server %q: rename one of them", server.Name, declared.Name)
				}
			}
		}
	}
	for _, server := range request.MCPServers {
		if !ValidMCPServerName(server.Name) || (len(server.Command) == 0) == (server.URL == "") {
			return request, func() {}, fmt.Errorf("MCP server %q needs a valid name and exactly one of a command and a URL", server.Name)
		}
		for _, tool := range server.Tools {
			if !domain.ValidMCPToolName(tool) {
				return request, func() {}, fmt.Errorf("MCP server %q lists tool %q, which no runtime can allow", server.Name, tool)
			}
		}
	}
	if preset == domain.RuntimeCodex {
		// Codex takes its servers as configuration overrides, and their
		// secrets from its own environment. A call that skips config.toml is
		// handed the user's servers the same way; one that loads it finds
		// them there (codexOwnServerArgs).
		if !runtime.spec.UserConfig {
			request.MCPServers = append(append([]MCPServer(nil), request.MCPServers...), codexUserServers(request.UserServers)...)
		}
		_, err := codexMCPEnvironment(request)
		return request, func() {}, err
	}
	if preset == domain.RuntimeClaude {
		// Without its sandbox, a permitted shell command could read the
		// servers' secrets (claudeSandbox), so the call does not start. The
		// user's own servers' secrets are in their configuration, which
		// Inkuna does not read, so a call given any of them is sandboxed.
		if serversHaveSecrets(request.MCPServers) || len(request.UserServers) > 0 {
			if missing := claudeSandboxMissing(); missing != "" {
				if len(request.UserServers) > 0 {
					return request, func() {}, fmt.Errorf("a Claude Code worker given your own MCP servers runs its shell commands in Claude Code's sandbox, so they cannot read the servers' secrets, but %s: install it, or take the servers off the runtime's userMcp", missing)
				}
				return request, func() {}, fmt.Errorf("a Claude Code worker given an MCP server's secrets runs its shell commands in Claude Code's sandbox, so they cannot read them, but %s: install it, or grant the step no server with secrets", missing)
			}
		}
		if len(request.UserServers) > 0 {
			// The call loads the user's settings file itself, which is where
			// Claude Code finds the user's own servers, so a rule there that
			// would widen a grant cannot be filtered out.
			if err := claudeUserWidens(request.MCPServers); err != nil {
				return request, func() {}, err
			}
			if err := claudeUserAllowsServers(); err != nil {
				return request, func() {}, err
			}
		}
	}
	cleanup := func() {}
	if len(request.MCPServers) > 0 {
		var config any = copilotMCPConfig(request.MCPServers)
		if preset == domain.RuntimeClaude {
			config = claudeMCPConfig(request.MCPServers)
		}
		path, remove, err := writeMCPConfig(request.MCPDir, request.RunID, config)
		if err != nil {
			return request, func() {}, err
		}
		request.mcpFile, cleanup = path, remove
	}
	if preset == domain.RuntimeClaude {
		// The sandbox joins the user's own settings and builds on the user's
		// own sandbox. Given the user's servers, the call loads them from
		// the user's configuration, and allowedMcpServers keeps every other
		// server there from starting.
		inherited := claudeUserSettings(request.MCPServers)
		settings := claudeSettingsFor(runtime.spec.UserConfig, claudeSandbox(request, inherited["sandbox"]), inherited)
		if len(request.UserServers) > 0 {
			settings["allowedMcpServers"] = claudeAllowedServers(request)
		}
		settingsPath, removeSettings, err := writeMCPConfig(request.MCPDir, request.RunID+"-settings", settings)
		if err != nil {
			cleanup()
			return request, func() {}, err
		}
		request.claudeSettingsFile = settingsPath
		removeConfig := cleanup
		cleanup = func() { removeSettings(); removeConfig() }
	}
	return request, cleanup, nil
}

// claudeUserAllowsServers refuses a Claude Code call given the user's own
// servers whose user settings would add servers to the ones the call allows:
// such a call loads that file itself, and its allowedMcpServers or
// enableAllProjectMcpServers would start servers the runtime was not opted
// in to. The repository's settings are never loaded (arguments), so they
// cannot.
func claudeUserAllowsServers() error {
	directory := claudeConfigDir()
	if directory == "" {
		return nil
	}
	path := filepath.Join(directory, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var settings map[string]any
	if json.Unmarshal(data, &settings) != nil {
		return nil
	}
	for _, key := range []string{"allowedMcpServers", "enableAllProjectMcpServers", "enabledMcpjsonServers"} {
		if _, set := settings[key]; set {
			return fmt.Errorf("%s sets %s, which would start MCP servers beside those the runtime's userMcp names: remove it, or take the servers off userMcp", path, key)
		}
	}
	return nil
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// writeMCPConfig writes a call's server configuration, secrets and all, or a
// Claude Code call's settings, into the run's own directory, readable only by
// the user. Never the temporary directory: Copilot's sandbox lets the model
// read that.
func writeMCPConfig(directory, runID string, config any) (string, func(), error) {
	if strings.TrimSpace(directory) == "" || !filepath.IsAbs(directory) {
		return "", func() {}, errors.New("an absolute directory for the call's MCP configuration is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("create the MCP configuration directory: %w", err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", func() {}, fmt.Errorf("encode the MCP configuration: %w", err)
	}
	file, err := os.CreateTemp(directory, safeFileName(runID)+"-*.json")
	if err != nil {
		return "", func() {}, fmt.Errorf("create the MCP configuration: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("write the MCP configuration: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write the MCP configuration: %w", err)
	}
	return path, cleanup, nil
}

// safeFileName keeps a run id's letters, digits, dots, and dashes.
func safeFileName(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, value)
}

// claudeMCPConfig is the --mcp-config file Claude Code reads.
func claudeMCPConfig(servers []MCPServer) map[string]any {
	entries := make(map[string]any, len(servers))
	for _, server := range servers {
		if server.URL != "" {
			entry := map[string]any{"type": "http", "url": server.URL}
			if server.Bearer != "" {
				entry["headers"] = map[string]string{"Authorization": "Bearer " + server.Bearer}
			}
			entries[server.Name] = entry
			continue
		}
		entry := map[string]any{"type": "stdio", "command": server.Command[0], "args": append([]string{}, server.Command[1:]...)}
		if len(server.Env) > 0 {
			entry["env"] = server.Env
		}
		entries[server.Name] = entry
	}
	return map[string]any{"mcpServers": entries}
}

// copilotMCPConfig is the --additional-mcp-config file Copilot reads. Its
// tools list narrows the server as --available-tools does.
func copilotMCPConfig(servers []MCPServer) map[string]any {
	entries := make(map[string]any, len(servers))
	for _, server := range servers {
		tools := append([]string{}, server.Tools...)
		if len(tools) == 0 {
			tools = []string{"*"}
		}
		if server.URL != "" {
			entry := map[string]any{"type": "http", "url": server.URL, "tools": tools}
			if server.Bearer != "" {
				entry["headers"] = map[string]string{"Authorization": "Bearer " + server.Bearer}
			}
			entries[server.Name] = entry
			continue
		}
		entry := map[string]any{"type": "local", "command": server.Command[0], "args": append([]string{}, server.Command[1:]...), "tools": tools}
		if len(server.Env) > 0 {
			entry["env"] = server.Env
		}
		entries[server.Name] = entry
	}
	return map[string]any{"mcpServers": entries}
}

// claudeMCPRules are the --allowedTools rules for the call's servers: a
// server's own name allows all its tools, and mcp__<server>__<tool> one of
// them. Claude Code still shows the model a server's other tools, but a call
// to one is denied, and never offered to the user as a grant.
func claudeMCPRules(servers []MCPServer) []string {
	rules := make([]string, 0, len(servers))
	for _, server := range servers {
		if len(server.Tools) == 0 {
			rules = append(rules, "mcp__"+server.Name)
			continue
		}
		for _, tool := range server.Tools {
			rules = append(rules, "mcp__"+server.Name+"__"+tool)
		}
	}
	return rules
}

// claudeMCPFileRule denies Claude Code's file tools the directory of the
// call's MCP configuration, which holds the servers' secrets: a bare Read
// rule reads any absolute path, and Read rules cover Grep and Glob too. A
// path rule starting with // is absolute.
func claudeMCPFileRule(file string) string {
	return "Read(//" + strings.TrimPrefix(filepath.ToSlash(filepath.Dir(file)), "/") + "/**)"
}

// widensGrant reports whether a Claude Code allow rule would let a worker
// call a granted server's tool that inkuna.yaml does not list: the server's
// own name, a wildcard, or another of its tools. Claude Code adds the allow
// rules of every settings file it loads to the call's own, so such a rule
// would undo the declaration. A server granted whole loses nothing to one.
func widensGrant(rule string, servers []MCPServer) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(rule), "mcp__")
	if !ok {
		return false
	}
	name, tool, _ := strings.Cut(rest, "__")
	for _, server := range servers {
		if len(server.Tools) == 0 || (name != server.Name && !strings.Contains(name, "*")) {
			continue
		}
		if tool == "" || strings.Contains(tool, "*") || !slices.Contains(server.Tools, tool) {
			return true
		}
	}
	return false
}

// claudeUserSettings is the user's Claude Code settings file without the
// allow rules that would widen the call's grants (widensGrant). Everything
// else stays, sign-in and other permissions included; the call is given
// this in --settings and told not to load the file. A missing or unreadable
// file gives nil, as Claude Code would load nothing from it either.
func claudeUserSettings(servers []MCPServer) map[string]any {
	directory := claudeConfigDir()
	if directory == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(directory, "settings.json"))
	if err != nil {
		return nil
	}
	var settings map[string]any
	if json.Unmarshal(data, &settings) != nil {
		return nil
	}
	permissions, _ := settings["permissions"].(map[string]any)
	if allow, ok := permissions["allow"].([]any); ok {
		kept := make([]any, 0, len(allow))
		for _, rule := range allow {
			if text, _ := rule.(string); !widensGrant(text, servers) {
				kept = append(kept, rule)
			}
		}
		permissions["allow"] = kept
	}
	return settings
}

// claudeUserWidens refuses a Claude Code call given the user's own servers
// when the user's settings file has an allow rule that would widen one of
// its declared grants. Such a call loads that file itself, since it is where
// Claude Code finds the user's servers, so the rule cannot be left out as
// claudeUserSettings leaves it out of a call given only declared servers.
func claudeUserWidens(servers []MCPServer) error {
	directory := claudeConfigDir()
	if directory == "" {
		return nil
	}
	path := filepath.Join(directory, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return nil
	}
	for _, rule := range settings.Permissions.Allow {
		if widensGrant(rule, servers) {
			return fmt.Errorf("%s allows %s, which would let a worker given your own MCP servers call tools of a granted server that inkuna.yaml does not list: remove the rule, or list those tools in inkuna.yaml", path, rule)
		}
	}
	return nil
}

// claudeSandbox is the sandbox Claude Code runs a call's shell commands in
// when a granted server has secrets or the call is given the user's own
// servers, or nil. A permitted command can run
// code that reads any file the user can, so no Read rule keeps it from the
// servers' configuration, nor from a running server's environment, which ps
// shows; the operating system's sandbox does. It builds on the user's own
// sandbox settings (inherited), keeping what they deny reading, and adds
// the configuration's directory to it. Writes and the network stay as they
// were: open when the user's commands ran unsandboxed, so a command that
// worked still works, and as the user set them otherwise. Claude Code exits
// rather than run unsandboxed (failIfUnavailable), never retries a command
// outside the sandbox (allowUnsandboxedCommands) or runs one outside it at
// all (excludedCommands, which is dropped), keeps filesystem isolation on,
// and still runs only the commands the step allows (autoAllowBashIfSandboxed).
func claudeSandbox(request RunRequest, inherited any) map[string]any {
	if request.MCPDir == "" || (!serversHaveSecrets(request.MCPServers) && len(request.UserServers) == 0) {
		return nil
	}
	own, _ := inherited.(map[string]any)
	sandbox := make(map[string]any, len(own)+6)
	for key, value := range own {
		sandbox[key] = value
	}
	filesystem, network := map[string]any{}, map[string]any{}
	if settings, ok := own["filesystem"].(map[string]any); ok {
		for key, value := range settings {
			filesystem[key] = value
		}
	}
	if settings, ok := own["network"].(map[string]any); ok {
		for key, value := range settings {
			network[key] = value
		}
	}
	if enabled, _ := own["enabled"].(bool); !enabled {
		filesystem["allowWrite"] = []any{"/"}
		network["allowedDomains"] = []any{"*"}
	}
	denied, _ := filesystem["denyRead"].([]any)
	denied = append(append([]any(nil), denied...), filepath.Clean(request.MCPDir))
	if len(request.UserServers) > 0 {
		// Where Claude Code keeps the user's servers, with their secrets,
		// and its own sign-in.
		files, directories := claudeUserServerFiles()
		for _, path := range append(files, directories...) {
			denied = append(denied, path)
		}
	}
	filesystem["denyRead"] = denied
	filesystem["disabled"] = false
	delete(sandbox, "excludedCommands")
	sandbox["enabled"], sandbox["failIfUnavailable"] = true, true
	sandbox["allowUnsandboxedCommands"], sandbox["autoAllowBashIfSandboxed"] = false, false
	sandbox["filesystem"], sandbox["network"] = filesystem, network
	return sandbox
}

// serversHaveSecrets reports whether any server is given a secret.
func serversHaveSecrets(servers []MCPServer) bool {
	for _, server := range servers {
		if len(server.Env) > 0 || server.Bearer != "" {
			return true
		}
	}
	return false
}

// sandboxMissing says what keeps Claude Code's sandbox from running on goos,
// or "" when nothing does: macOS's sandbox-exec, or bubblewrap and socat on
// Linux. It runs nowhere else.
func sandboxMissing(goos string, lookPath func(string) (string, error)) string {
	var needed []string
	switch goos {
	case "darwin":
		needed = []string{"/usr/bin/sandbox-exec"}
	case "linux":
		needed = []string{"bwrap", "socat"}
	default:
		return "Claude Code's sandbox does not run on " + goos
	}
	var missing []string
	for _, program := range needed {
		if _, err := lookPath(program); err != nil {
			missing = append(missing, program)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "Claude Code's sandbox needs " + strings.Join(missing, " and ") + ", which this machine does not have"
}

// claudeSandboxMissing is sandboxMissing for this machine. Tests replace it.
var claudeSandboxMissing = func() string { return sandboxMissing(goruntime.GOOS, exec.LookPath) }

// codexMCPArgs are the configuration overrides that give Codex the call's
// servers. Each server is required, so one that cannot start stops Codex
// before the model runs; its calls need no approval, which exec could not
// ask for; and enabled_tools hides every tool it does not list. Secrets
// reach a server only by the variable names in env_vars and
// bearer_token_env_var, which codexMCPEnvironment sets.
func codexMCPArgs(servers []MCPServer) []string {
	args := make([]string, 0, 8*len(servers))
	set := func(server, key string, value any) {
		args = append(args, "--config", "mcp_servers."+codexConfigKey(server)+"."+key+"="+tomlValue(value))
	}
	for _, server := range servers {
		if server.URL != "" {
			set(server.Name, "url", server.URL)
			if server.Bearer != "" {
				set(server.Name, "bearer_token_env_var", codexBearerVariable(server.Name))
			}
			if len(server.headers) > 0 {
				variables := make(map[string]string, len(server.headers))
				for index, header := range sortedKeys(server.headers) {
					variables[header] = codexHeaderVariable(server.Name, index)
				}
				set(server.Name, "env_http_headers", variables)
			}
		} else {
			set(server.Name, "command", server.Command[0])
			set(server.Name, "args", append([]string{}, server.Command[1:]...))
			if len(server.Env) > 0 {
				set(server.Name, "env_vars", sortedKeys(server.Env))
			}
		}
		if len(server.Tools) > 0 {
			set(server.Name, "enabled_tools", server.Tools)
		}
		for _, key := range sortedAnyKeys(server.codexExtra) {
			set(server.Name, key, server.codexExtra[key])
		}
		set(server.Name, "default_tools_approval_mode", "approve")
		args = append(args, "--config", "mcp_servers."+codexConfigKey(server.Name)+".required=true")
	}
	return args
}

// codexMCPEnvironment is what Codex's own environment gains for the call's
// servers, and the variables its shell must not see. Codex passes a server
// only the variables its env_vars names, and the model's shell everything
// Codex has, so each is excluded from the shell. A variable the worker is
// already given, as an agent's own secret, stays in the shell: that was the
// user's choice. Variables that would hold two values are refused, as Codex
// has one environment for all of them.
func codexMCPEnvironment(request RunRequest) (codexMCPSetup, error) {
	setup := codexMCPSetup{}
	worker := make(map[string]string, len(request.Environment))
	for _, entry := range request.Environment {
		name, value, _ := strings.Cut(entry, "=")
		worker[name] = value
	}
	values := make(map[string]string)
	owner := make(map[string]string)
	add := func(server, name, value string) error {
		if ReservedVariable(name) {
			return fmt.Errorf("MCP server %q sets %s, which every CLI is started with", server, name)
		}
		if existing, ok := values[name]; ok {
			if existing != value {
				return fmt.Errorf("MCP servers %q and %q both set %s to different secrets, which Codex cannot keep apart", owner[name], server, name)
			}
			return nil
		}
		values[name], owner[name] = value, server
		if current, given := worker[name]; given {
			if current != value {
				return fmt.Errorf("MCP server %q sets %s, which the worker is also given with another value", server, name)
			}
			return nil
		}
		setup.environment = append(setup.environment, name+"="+value)
		setup.excluded = append(setup.excluded, name)
		return nil
	}
	for _, server := range request.MCPServers {
		for _, name := range sortedKeys(server.Env) {
			if err := add(server.Name, name, server.Env[name]); err != nil {
				return codexMCPSetup{}, err
			}
		}
		if server.URL != "" && server.Bearer != "" {
			if err := add(server.Name, codexBearerVariable(server.Name), server.Bearer); err != nil {
				return codexMCPSetup{}, err
			}
		}
		for index, header := range sortedKeys(server.headers) {
			if err := add(server.Name, codexHeaderVariable(server.Name, index), server.headers[header]); err != nil {
				return codexMCPSetup{}, err
			}
		}
	}
	return setup, nil
}

// codexMCPSetup is the environment Codex gains for the call's servers and the
// variables kept out of its shell.
type codexMCPSetup struct {
	environment []string
	excluded    []string
}

// codexShellArgs keeps the servers' secrets out of the model's shell.
func codexShellArgs(excluded []string) []string {
	if len(excluded) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(excluded)
	return []string{"--config", "shell_environment_policy.exclude=" + string(encoded)}
}

// codexBearerVariable names the variable that holds a URL server's bearer
// token in Codex's environment, one no user setting uses.
func codexBearerVariable(server string) string {
	return "INKUNA_MCP_" + strings.ToUpper(strings.ReplaceAll(server, "-", "_")) + "_BEARER"
}

// copilotMCPTools are the --available-tools entries for the call's servers:
// <server>-<tool> for each listed tool, or the server's name for all of
// them. The allowlist is what Copilot shows the model; a tool left off it
// does not exist for the call.
func copilotMCPTools(servers []MCPServer) []string {
	tools := make([]string, 0, len(servers))
	for _, server := range servers {
		if len(server.Tools) == 0 {
			tools = append(tools, server.Name)
			continue
		}
		for _, tool := range server.Tools {
			tools = append(tools, server.Name+"-"+tool)
		}
	}
	return tools
}

// copilotMCPPermissions are the --allow-tool entries for the call's servers.
func copilotMCPPermissions(servers []MCPServer) []string {
	permissions := make([]string, 0, len(servers))
	for _, server := range servers {
		if len(server.Tools) == 0 {
			permissions = append(permissions, "--allow-tool="+server.Name)
			continue
		}
		for _, tool := range server.Tools {
			permissions = append(permissions, "--allow-tool="+server.Name+"("+tool+")")
		}
	}
	return permissions
}

// mcpFailure reports a granted server the CLI says did not start: Claude
// Code's init event, and Copilot's server events. Codex is told each server
// is required, so it stops by itself. A server the user's own setup brought
// is not the step's concern.
func mcpFailure(kind domain.Runtime, payload map[string]any, granted map[string]bool) (string, bool) {
	if len(granted) == 0 {
		return "", false
	}
	rawType, _ := payload["type"].(string)
	var entries []any
	switch {
	case kind == domain.RuntimeClaude && rawType == "system" && payload["subtype"] == "init":
		entries, _ = payload["mcp_servers"].([]any)
	case kind == domain.RuntimeCopilot && rawType == "session.mcp_servers_loaded":
		data, _ := payload["data"].(map[string]any)
		entries, _ = data["servers"].([]any)
	case kind == domain.RuntimeCopilot && rawType == "session.mcp_server_status_changed":
		data, _ := payload["data"].(map[string]any)
		entries = []any{map[string]any{"name": data["serverName"], "status": data["status"], "error": data["error"]}}
	}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		name, _ := entry["name"].(string)
		status, _ := entry["status"].(string)
		if !granted[name] || (status != "failed" && status != "needs-auth") {
			continue
		}
		reason := status
		if detail, _ := entry["error"].(string); strings.TrimSpace(detail) != "" {
			reason = strings.TrimSpace(detail)
		}
		return fmt.Sprintf("MCP server %q did not start: %s", name, reason), true
	}
	return "", false
}

// MinMCPSecretBytes is the shortest server secret Inkuna gives a call: the
// shortest secretScrubber hides, so no shorter one can reach what Inkuna
// keeps.
const MinMCPSecretBytes = 8

// secretScrubber hides the values of the call's secrets: its server
// secrets, and the secrets exported to the worker (RunRequest.Environment).
// A server can echo its token in a tool result, and a worker can print a
// variable it was given, which the CLI reports in its stream; the model sees
// it there, but the run's events and handoff Inkuna keeps must not. Values
// too short to be a credential are left alone, as replacing them would
// garble ordinary text.
func secretScrubber(servers []MCPServer, environment []string) func(string) string {
	var values []string
	for _, server := range servers {
		values = append(values, server.Bearer)
		for _, value := range server.Env {
			values = append(values, value)
		}
		for _, value := range server.headers {
			values = append(values, value)
		}
	}
	for _, pair := range environment {
		if _, value, ok := strings.Cut(pair, "="); ok {
			values = append(values, value)
		}
	}
	var pairs []string
	for _, value := range values {
		if len(value) >= MinMCPSecretBytes {
			pairs = append(pairs, value, "[REDACTED]")
		}
	}
	if len(pairs) == 0 {
		return func(text string) string { return text }
	}
	return strings.NewReplacer(pairs...).Replace
}

// scrubEvent hides the call's server secrets in an event's summary and
// payload. The payload's strings are scrubbed as they are, not as JSON text,
// where an escaped secret would not match (see scrubJSON).
func scrubEvent(entry domain.RunEvent, scrub func(string) string) domain.RunEvent {
	entry.Summary = scrub(entry.Summary)
	if entry.Payload == nil {
		return entry
	}
	if scrubbed, changed := scrubValue(entry.Payload, scrub); changed {
		entry.Payload = scrubbed.(map[string]any)
	}
	return entry
}

// scrubJSON hides the call's server secrets in a JSON document. Its strings
// are scrubbed decoded, because the encoder escapes a quote, a backslash, or
// an ampersand in a secret, and the escaped text would not match. A document
// with no secret in it is returned unchanged.
func scrubJSON(document json.RawMessage, scrub func(string) string) json.RawMessage {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return json.RawMessage(scrub(string(document)))
	}
	scrubbed, changed := scrubValue(value, scrub)
	if !changed {
		return document
	}
	data, err := json.Marshal(scrubbed)
	if err != nil {
		return json.RawMessage(scrub(string(document)))
	}
	return data
}

// scrubValue scrubs every string in a decoded JSON value, keys included, and
// reports whether any changed.
func scrubValue(value any, scrub func(string) string) (any, bool) {
	switch typed := value.(type) {
	case string:
		scrubbed := scrub(typed)
		return scrubbed, scrubbed != typed
	case []any:
		changed := false
		for index, item := range typed {
			var itemChanged bool
			typed[index], itemChanged = scrubValue(item, scrub)
			changed = changed || itemChanged
		}
		return typed, changed
	case map[string]any:
		changed := false
		scrubbed := make(map[string]any, len(typed))
		for key, item := range typed {
			item, itemChanged := scrubValue(item, scrub)
			name := scrub(key)
			scrubbed[name] = item
			changed = changed || itemChanged || name != key
		}
		return scrubbed, changed
	}
	return value, false
}

// grantedServers is the set of the call's server names.
func grantedServers(request RunRequest) map[string]bool {
	names := make(map[string]bool, len(request.MCPServers))
	for _, server := range request.MCPServers {
		if !server.user {
			names[server.Name] = true
		}
	}
	return names
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// tomlValue encodes a configuration override's value as TOML: a string,
// number, or array of strings is the same in JSON, and a table of strings
// becomes an inline table, whose keys are quoted.
func tomlValue(value any) string {
	table, ok := value.(map[string]string)
	if !ok {
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	entries := make([]string, 0, len(table))
	for _, key := range sortedKeys(table) {
		name, _ := json.Marshal(key)
		item, _ := json.Marshal(table[key])
		entries = append(entries, string(name)+"="+string(item))
	}
	return "{" + strings.Join(entries, ",") + "}"
}

// claudeUserServerFiles are where Claude Code keeps the user's MCP servers
// and its sign-in: .claude.json beside or inside its configuration
// directory, the credentials file it uses where no keychain is, and the
// backups directory, which holds earlier copies of .claude.json.
func claudeUserServerFiles() (files, directories []string) {
	directory := claudeConfigDir()
	if directory == "" {
		return nil, nil
	}
	files = []string{filepath.Join(directory, ".claude.json"), filepath.Join(directory, ".credentials.json")}
	if os.Getenv("CLAUDE_CONFIG_DIR") == "" {
		files = append(files, filepath.Join(filepath.Dir(directory), ".claude.json"))
	}
	return files, []string{filepath.Join(directory, "backups")}
}

// claudeUserServerRules are the Read rules that keep Claude Code's file
// tools from claudeUserServerFiles: each file, and everything under each
// directory. A path rule starting with // is absolute.
func claudeUserServerRules() []string {
	files, directories := claudeUserServerFiles()
	rules := make([]string, 0, len(files)+len(directories))
	for _, path := range files {
		rules = append(rules, "Read(//"+strings.TrimPrefix(filepath.ToSlash(path), "/")+")")
	}
	for _, path := range directories {
		rules = append(rules, "Read(//"+strings.TrimPrefix(filepath.ToSlash(path), "/")+"/**)")
	}
	return rules
}
