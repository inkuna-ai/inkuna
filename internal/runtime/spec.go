package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// Output formats a runtime's stdout can be read as. The three JSON formats
// are the built-in CLIs' event streams; text treats stdout as prose whose last
// JSON object is the structured result.
const (
	OutputClaude  = "claude-stream-json"
	OutputCodex   = "codex-json"
	OutputCopilot = "copilot-json"
	OutputText    = "text"
)

// Stdin behaviours: the prompt is written to the process's standard input, or
// nothing is and the template passes it as an argument.
const (
	StdinPrompt = "prompt"
	StdinNone   = "none"
)

// Spec declares one headless CLI. A preset names a built-in adapter
// (codex, claude, copilot) whose arguments Inkuna builds itself; without one
// the argument templates below are used.
type Spec struct {
	Name   domain.Runtime
	Label  string
	Preset domain.Runtime
	Binary string
	// Args is the template for every run. ReadOnlyArgs or WriteArgs is appended
	// for the step's permission. A runtime without WriteArgs is read-only:
	// nothing in a template can widen what a step was granted.
	Args         []string
	ReadOnlyArgs []string
	WriteArgs    []string
	Stdin        string
	Output       string
	// VersionArgs is the health probe; it defaults to --version.
	VersionArgs []string
	// UserConfig lets a built-in adapter load the user's own CLI setup:
	// Claude Code's hooks, skills, auto-memory, and global instructions,
	// Codex's config.toml (MCP servers included) and skills, and Copilot's
	// installed plugins. Without it a worker starts from the CLI's own
	// defaults plus the repository's instructions.
	UserConfig bool
	// UserMCP names the user's own MCP servers, as the CLI's listing names
	// them, that every step on this runtime is given with all their tools.
	// It is the only way a worker starts one: without it, userConfig or not,
	// the CLI starts none of them (docs/mcp-and-trackers.md).
	UserMCP []string
	// Instance is the ID of the Inkuna that built the runtime, which every
	// process it starts carries in its environment (proc.Mark), so the next
	// Inkuna on the data directory ends what a crash left running. Empty
	// marks nothing.
	Instance string
}

// ValidName reports whether a runtime name is well formed. Whether a runtime
// with that name is declared is a question for the registry.
func ValidName(name domain.Runtime) bool { return domain.ValidRuntimeName(name) }

// IsPreset reports whether name is one of the built-in adapters.
func IsPreset(name domain.Runtime) bool {
	return name == domain.RuntimeCodex || name == domain.RuntimeClaude || name == domain.RuntimeCopilot
}

// placeholderPattern finds {name} tokens in a template argument.
var placeholderPattern = regexp.MustCompile(`\{[a-z_]*\}`)

// placeholders are the only values a template can receive. Each is one whole
// argument value, passed to exec without a shell, so nothing in a prompt or a
// model name can add arguments.
var placeholders = map[string]bool{
	"{prompt}": true, "{model}": true, "{reasoning_effort}": true, "{schema}": true, "{schema_path}": true,
}

// bypassArguments are flags and values that switch a CLI's own permission
// checks off. Least privilege is Inkuna's contract with the user, so a
// template that contains one is refused outright, and so is a step's command
// (CheckCommand). Matching ignores case and a trailing =value.
var bypassArguments = []string{
	"--dangerously-skip-permissions", "--dangerously-bypass-approvals-and-sandbox", "--allow-dangerously-skip-permissions",
	"--yolo", "--full-auto", "--allow-all", "--allow-all-tools", "--allow-all-paths", "--allow-all-urls", "--no-sandbox", "--trust-all-tools",
	"bypasspermissions", "danger-full-access",
}

// Validate rejects a declaration Inkuna could not run within its least
// privilege rules.
func (spec Spec) Validate() error {
	if !ValidName(spec.Name) {
		return fmt.Errorf("runtime name %q must start with a lowercase letter and use only lowercase letters, digits, and dashes (32 at most)", spec.Name)
	}
	if spec.Name == domain.RuntimeRemote {
		return fmt.Errorf("runtime name %q is reserved for remote A2A agents", spec.Name)
	}
	if spec.Preset != "" && !IsPreset(spec.Preset) {
		return fmt.Errorf("runtime %q: preset %q is not codex, claude, or copilot", spec.Name, spec.Preset)
	}
	if IsPreset(spec.Name) && spec.Preset != "" && spec.Preset != spec.Name {
		return fmt.Errorf("runtime %q is built in and cannot use the %s preset", spec.Name, spec.Preset)
	}
	for _, arguments := range [][]string{spec.Args, spec.ReadOnlyArgs, spec.WriteArgs, spec.VersionArgs} {
		if err := validateArguments(spec.Name, arguments); err != nil {
			return err
		}
	}
	for _, argument := range spec.VersionArgs {
		if placeholderPattern.MatchString(argument) {
			return fmt.Errorf("runtime %q: the version probe takes no placeholders", spec.Name)
		}
	}
	if spec.effectivePreset() != "" {
		if len(spec.Args)+len(spec.ReadOnlyArgs)+len(spec.WriteArgs) > 0 || spec.Stdin != "" || spec.Output != "" {
			return fmt.Errorf("runtime %q uses the %s preset, which builds its own arguments; remove args, stdin, and output or drop the preset", spec.Name, spec.effectivePreset())
		}
		return validateUserMCP(spec.Name, spec.UserMCP)
	}
	if spec.UserConfig {
		return fmt.Errorf("runtime %q: userConfig applies only to the codex, claude, and copilot adapters; a template's own arguments decide what its CLI loads", spec.Name)
	}
	if len(spec.UserMCP) > 0 {
		return fmt.Errorf("runtime %q: userMcp applies only to the codex, claude, and copilot adapters; an argument template cannot be given MCP servers", spec.Name)
	}
	if strings.TrimSpace(spec.Binary) == "" {
		return fmt.Errorf("runtime %q needs a binary", spec.Name)
	}
	switch spec.Output {
	case OutputClaude, OutputCodex, OutputCopilot, OutputText:
	default:
		return fmt.Errorf("runtime %q: output must be %s, %s, %s, or %s", spec.Name, OutputClaude, OutputCodex, OutputCopilot, OutputText)
	}
	switch spec.Stdin {
	case StdinPrompt, StdinNone:
	default:
		return fmt.Errorf("runtime %q: stdin must be %s or %s", spec.Name, StdinPrompt, StdinNone)
	}
	// Read-only is only as good as the flags that ask for it. Without any, a
	// read-only step would run the CLI in whatever mode it defaults to, and
	// Inkuna would be calling that read-only on the CLI's word alone.
	if len(spec.ReadOnlyArgs) == 0 {
		return fmt.Errorf("runtime %q needs readOnlyArgs: the flags that keep this CLI from changing files on read-only steps", spec.Name)
	}
	// Each permission gets its own argument set, so each must deliver the
	// prompt on its own: a {prompt} in the read-only arguments does nothing for
	// a step that writes.
	if !spec.deliversPrompt(spec.ReadOnlyArgs) {
		return fmt.Errorf("runtime %q never receives the prompt on read-only steps: set stdin to %s or put {prompt} in args", spec.Name, StdinPrompt)
	}
	if spec.CanWrite() && !spec.deliversPrompt(spec.WriteArgs) {
		return fmt.Errorf("runtime %q never receives the prompt on worktree-write steps: set stdin to %s or put {prompt} in args or writeArgs", spec.Name, StdinPrompt)
	}
	return nil
}

// deliversPrompt reports whether a run with these permission arguments gets
// the prompt, on stdin or as an argument.
func (spec Spec) deliversPrompt(permissionArgs []string) bool {
	if spec.Stdin == StdinPrompt {
		return true
	}
	for _, arguments := range [][]string{spec.Args, permissionArgs} {
		for _, argument := range arguments {
			if strings.Contains(argument, "{prompt}") {
				return true
			}
		}
	}
	return false
}

func validateArguments(name domain.Runtime, arguments []string) error {
	for _, argument := range arguments {
		if strings.TrimSpace(argument) == "" {
			return fmt.Errorf("runtime %q has an empty argument", name)
		}
		for _, token := range placeholderPattern.FindAllString(argument, -1) {
			if !placeholders[token] {
				return fmt.Errorf("runtime %q: %s is not a placeholder; use {prompt}, {model}, {reasoning_effort}, {schema}, or {schema_path}", name, token)
			}
		}
		if bypassArgument(argument) {
			return fmt.Errorf("runtime %q: %q turns the CLI's permission checks off, which Inkuna does not allow", name, argument)
		}
	}
	return nil
}

// effectivePreset is the built-in adapter this declaration runs through, if
// any. The three built-in names are their own preset.
func (spec Spec) effectivePreset() domain.Runtime {
	if spec.Preset != "" {
		return spec.Preset
	}
	if IsPreset(spec.Name) {
		return spec.Name
	}
	return ""
}

// Format is the built-in CLI whose event stream this runtime's stdout is
// read as; it is empty for plain text. What a call's stream can report, such
// as a cost or Copilot's premium requests, follows from it.
func (spec Spec) Format() domain.Runtime {
	if preset := spec.effectivePreset(); preset != "" {
		return preset
	}
	switch spec.Output {
	case OutputClaude:
		return domain.RuntimeClaude
	case OutputCodex:
		return domain.RuntimeCodex
	case OutputCopilot:
		return domain.RuntimeCopilot
	default:
		return ""
	}
}

// CanWrite reports whether the runtime may be given a worktree-write step.
func (spec Spec) CanWrite() bool { return spec.effectivePreset() != "" || len(spec.WriteArgs) > 0 }

// New builds the runtime a declaration describes.
func New(spec Spec) (AgentRuntime, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Binary) == "" {
		spec.Binary = string(spec.effectivePreset())
	}
	if len(spec.VersionArgs) == 0 {
		spec.VersionArgs = []string{"--version"}
	}
	closing, close := context.WithCancel(context.Background())
	return &commandRuntime{spec: spec, kind: spec.Format(), binary: spec.Binary, processes: make(map[string]*exec.Cmd), closing: closing, close: close}, nil
}

// expand fills a template for one run. An argument whose placeholder has no
// value is dropped, together with the flag before it when the argument is
// nothing but the placeholder, so "--model {model}" disappears when routing
// left the model to the CLI.
func expand(template []string, values map[string]string) []string {
	arguments := make([]string, 0, len(template))
	for _, argument := range template {
		missing := false
		expanded := placeholderPattern.ReplaceAllStringFunc(argument, func(token string) string {
			value := values[token]
			missing = missing || value == ""
			return value
		})
		if !missing {
			arguments = append(arguments, expanded)
			continue
		}
		if placeholders[argument] && len(arguments) > 0 && strings.HasPrefix(arguments[len(arguments)-1], "-") {
			arguments = arguments[:len(arguments)-1]
		}
	}
	return arguments
}
