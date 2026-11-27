package config

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/secrets"
)

// MaxMCPDescriptionBytes caps a server's description, which every step
// granted the server is sent.
const MaxMCPDescriptionBytes = 500

// MCPServerConfig declares an MCP server this machine offers workers. Rules
// and agents grant it by name; Inkuna starts it, or connects to it, only for
// a step granted it (docs/mcp-and-trackers.md).
type MCPServerConfig struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Command is an argv array started directly, never through a shell;
	// URL is a streamable HTTP endpoint. Exactly one is set.
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
	URL     string   `yaml:"url,omitempty" json:"url,omitempty"`
	// Env maps an environment variable the command's server reads to the
	// declared secret that holds its value.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// Bearer names the declared secret a URL's server is sent as its bearer
	// token.
	Bearer string `yaml:"bearer,omitempty" json:"bearer,omitempty"`
	// Tools are the server's tools a worker may call; empty means all.
	Tools []string `yaml:"tools,omitempty" json:"tools,omitempty"`
	// Tracker marks a server that reaches an issue tracker (linear or
	// github). Every change to a tracker waits for a person's approval, so
	// such a server must list the tools it is granted, and only ones that read.
	Tracker string `yaml:"tracker,omitempty" json:"tracker,omitempty"`
}

// Brief is the declaration without its secrets, as a step's grant shows it.
func (entry MCPServerConfig) Brief() domain.MCPServer {
	return domain.MCPServer{
		Name: entry.Name, Description: strings.TrimSpace(entry.Description), Command: append([]string(nil), entry.Command...),
		URL: strings.TrimSpace(entry.URL), Tools: append([]string(nil), entry.Tools...), Tracker: entry.Tracker,
	}
}

// MCPServer returns the declared server of that name.
func (file File) MCPServer(name string) (MCPServerConfig, bool) {
	for _, entry := range file.MCPServers {
		if entry.Name == name {
			return entry, true
		}
	}
	return MCPServerConfig{}, false
}

// MCPServerNames is the set of declared server names, for planning.
func (file File) MCPServerNames() map[string]bool {
	names := make(map[string]bool, len(file.MCPServers))
	for _, entry := range file.MCPServers {
		names[entry.Name] = true
	}
	return names
}

// mcpServerProblems checks the mcpServers block. Each problem names the
// list, as the settings view shows it beside the panel.
func (file File) mcpServerProblems(declaredSecrets map[string]bool) []FieldError {
	problems := make([]FieldError, 0)
	report := func(format string, args ...any) {
		problems = append(problems, FieldError{Field: "mcpServers", Message: fmt.Sprintf(format, args...)})
	}
	names := make(map[string]bool, len(file.MCPServers))
	for _, entry := range file.MCPServers {
		if !agentruntime.ValidMCPServerName(entry.Name) {
			report("MCP server name %q must be lower-case letters, digits, and dashes, start with a letter, and not be a runtime's own tool name", entry.Name)
			continue
		}
		if names[entry.Name] {
			report("MCP server %q is declared twice", entry.Name)
		}
		names[entry.Name] = true
		for _, problem := range entry.problems(declaredSecrets) {
			report("MCP server %q: %s", entry.Name, problem)
		}
	}
	return problems
}

func (entry MCPServerConfig) problems(declaredSecrets map[string]bool) []string {
	problems := make([]string, 0)
	if len(entry.Description) > MaxMCPDescriptionBytes {
		problems = append(problems, fmt.Sprintf("the description is over %d bytes; it goes into every granted step's prompt", MaxMCPDescriptionBytes))
	}
	hasCommand, hasURL := len(entry.Command) > 0, strings.TrimSpace(entry.URL) != ""
	switch {
	case hasCommand == hasURL:
		problems = append(problems, "set exactly one of command and url")
	case hasCommand:
		if strings.TrimSpace(entry.Command[0]) == "" {
			problems = append(problems, "the command needs a program to run")
		}
		for _, argument := range entry.Command {
			if strings.ContainsRune(argument, 0) {
				problems = append(problems, "the command's arguments cannot contain a NUL byte")
				break
			}
		}
		if entry.Bearer != "" {
			problems = append(problems, "bearer is for a url; give a command's server its secrets under env")
		}
	case hasURL:
		if err := mcpURL(entry.URL); err != nil {
			problems = append(problems, err.Error())
		}
		if len(entry.Env) > 0 {
			problems = append(problems, "env is for a command; send a url's server its secret with bearer")
		}
	}
	variables := make([]string, 0, len(entry.Env))
	for variable := range entry.Env {
		variables = append(variables, variable)
	}
	sort.Strings(variables)
	for _, variable := range variables {
		switch {
		case secrets.ValidateName(variable) != nil:
			problems = append(problems, fmt.Sprintf("env variable %q must be an upper-case environment variable name", variable))
		case agentruntime.ReservedVariable(variable):
			problems = append(problems, fmt.Sprintf("env variable %q is one every CLI is started with, so a server cannot be given it", variable))
		}
		if secret := entry.Env[variable]; !declaredSecrets[secret] {
			problems = append(problems, fmt.Sprintf("env variable %s uses secret %q, which must be declared in the secrets block", variable, secret))
		}
	}
	if entry.Bearer != "" && !declaredSecrets[entry.Bearer] {
		problems = append(problems, fmt.Sprintf("bearer uses secret %q, which must be declared in the secrets block", entry.Bearer))
	}
	tools := make(map[string]bool, len(entry.Tools))
	for _, tool := range entry.Tools {
		if !domain.ValidMCPToolName(tool) {
			problems = append(problems, fmt.Sprintf("tool %q must be letters, digits, underscores, dots, and dashes", tool))
			continue
		}
		if tools[tool] {
			problems = append(problems, fmt.Sprintf("tool %q is listed twice", tool))
		}
		tools[tool] = true
	}
	switch entry.Tracker {
	case "":
	case "linear", "github":
		// Every tracker change goes through the review gate, so a tracker's
		// own server is granted only the tools its declaration names.
		if len(entry.Tools) == 0 {
			problems = append(problems, "a tracker's server must list its tools, and only ones that read")
		}
	default:
		problems = append(problems, fmt.Sprintf("tracker %q must be linear or github", entry.Tracker))
	}
	return problems
}

// mcpURL accepts https, and http only to this machine.
func mcpURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("url %q must be an absolute URL without credentials", raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		host := parsed.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
		return fmt.Errorf("url %q must use https, or http only to localhost", raw)
	default:
		return fmt.Errorf("url %q must use https, or http only to localhost", raw)
	}
}
