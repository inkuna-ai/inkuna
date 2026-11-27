// Package agent loads specialized agent definitions. An agent is a Markdown
// file with YAML frontmatter, the same shape Claude Code uses for
// .claude/agents, so users can author one file and have every runtime
// (Codex, Claude, Copilot) honour it. The orchestrator chooses among these
// definitions; Go enforces their permissions.
package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/frontmatter"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// Source records where a definition was loaded from. Later sources override
// earlier ones by name: builtin < global < repository. Remote agents are not a
// layer: they never override a local agent (see WithRemote).
type Source string

const (
	SourceBuiltIn    Source = "builtin"
	SourceGlobal     Source = "global"
	SourceRepository Source = "repository"
	// SourceRemote marks an external A2A agent connected as a read-only
	// advisor. It has no instructions, runtime, or worker secrets, and it is
	// always read-only.
	SourceRemote Source = "remote"
)

// Auto is the sentinel agent name meaning "let the orchestrator decide".
const Auto = "auto"

// Orchestrator is the reserved name of the planning agent. It is never a
// worker and cannot be assigned by a rule.
const Orchestrator = "orchestrator"

// Human is the reserved A2A card representing the user. Human-input steps
// send tasks to it and the inbox answers them.
const Human = "human"

var safeName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)

// Definition is one specialized agent.
type Definition struct {
	Name            string            `yaml:"name" json:"name"`
	Description     string            `yaml:"description" json:"description"`
	Runtime         domain.Runtime    `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Model           string            `yaml:"model,omitempty" json:"model,omitempty"`
	ReasoningEffort string            `yaml:"reasoningEffort,omitempty" json:"reasoningEffort,omitempty"`
	Permission      domain.Permission `yaml:"permission,omitempty" json:"permission"`
	Skills          []string          `yaml:"skills,omitempty" json:"skills,omitempty"`
	TaskKinds       []domain.TaskKind `yaml:"taskKinds,omitempty" json:"taskKinds,omitempty"`
	Tags            []string          `yaml:"tags,omitempty" json:"tags,omitempty"`
	MaxTurns        int               `yaml:"maxTurns,omitempty" json:"maxTurns,omitempty"`
	Secrets         []string          `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Instructions    string            `yaml:"-" json:"instructions"`
	Source          Source            `yaml:"-" json:"source"`
	Path            string            `yaml:"-" json:"path,omitempty"`
	// Auto is only meaningful for a remote agent: whether the orchestrator may
	// pick it. Without it a remote agent is used only where a rule names it.
	Auto bool `yaml:"-" json:"auto,omitempty"`
	// MCP names the MCP servers inkuna.yaml declares that this agent's steps
	// are given. One the machine does not declare is left out of the plan,
	// with a note, so a global agent works on every machine.
	MCP []string `yaml:"mcp,omitempty" json:"mcp,omitempty"`
	// Tracker names the kinds of tracker change this agent's steps may ask
	// for in their handoff. A step gets only those the repository's
	// tracker.yaml, as this machine's inkuna.yaml narrows it, allows.
	Tracker []domain.TrackerKind `yaml:"tracker,omitempty" json:"tracker,omitempty"`
	// Extra is the file's frontmatter keys Inkuna does not read, as YAML in
	// the order the file has them. The same file often serves another tool,
	// such as a Claude Code agent's tools or color, so those keys are ignored
	// rather than refused, and written back when the agent is saved.
	Extra string `yaml:"-" json:"extra,omitempty"`
}

// Remote reports whether the agent lives outside this machine.
func (definition Definition) Remote() bool { return definition.Source == SourceRemote }

// ValidName reports whether name can be an agent's name: well formed and not
// reserved for the harness.
func ValidName(name string) bool {
	return safeName.MatchString(name) && name != Auto && name != Human && name != Orchestrator
}

// Validate enforces the invariants the orchestrator and executor rely on.
func (definition Definition) Validate() error {
	if !safeName.MatchString(definition.Name) {
		return fmt.Errorf("agent name %q must be lower-case letters, digits, and dashes", definition.Name)
	}
	if definition.Name == Auto || definition.Name == Human {
		return fmt.Errorf("agent name %q is reserved", definition.Name)
	}
	if strings.TrimSpace(definition.Description) == "" {
		return fmt.Errorf("agent %q needs a description", definition.Name)
	}
	if strings.TrimSpace(definition.Instructions) == "" {
		return fmt.Errorf("agent %q needs instructions in the Markdown body", definition.Name)
	}
	if definition.Runtime != "" && (!validRuntime(definition.Runtime) || definition.Runtime == domain.RuntimeRemote) {
		return fmt.Errorf("agent %q has invalid runtime %q", definition.Name, definition.Runtime)
	}
	if definition.Permission != domain.PermissionReadOnly && definition.Permission != domain.PermissionWorktree {
		return fmt.Errorf("agent %q permission must be read-only or worktree-write", definition.Name)
	}
	if definition.MaxTurns < 0 {
		return fmt.Errorf("agent %q maxTurns must not be negative", definition.Name)
	}
	for _, kind := range definition.TaskKinds {
		if !validTaskKind(kind) {
			return fmt.Errorf("agent %q lists unknown task kind %q", definition.Name, kind)
		}
	}
	for _, server := range definition.MCP {
		if !agentruntime.ValidMCPServerName(server) {
			return fmt.Errorf("agent %q names MCP server %q, which is not a valid server name", definition.Name, server)
		}
	}
	for _, kind := range definition.Tracker {
		if !domain.ValidTrackerKind(kind) {
			return fmt.Errorf("agent %q asks for tracker kind %q; the kinds are status, comment, edit, and create", definition.Name, kind)
		}
	}
	if _, err := extraYAML(definition.Extra); err != nil {
		return fmt.Errorf("agent %q: %w", definition.Name, err)
	}
	return nil
}

// Supports reports whether the agent declares the task kind. An agent with no
// task kinds is general-purpose.
func (definition Definition) Supports(kind domain.TaskKind) bool {
	if len(definition.TaskKinds) == 0 {
		return true
	}
	for _, supported := range definition.TaskKinds {
		if supported == kind {
			return true
		}
	}
	return false
}

// Card renders the definition as an A2A Agent Card. skillDescriptions maps
// Inkuna skill IDs to their descriptions so the card advertises real skills.
func (definition Definition) Card(skillDescriptions map[string]string) a2a.AgentCard {
	skills := make([]a2a.AgentSkill, 0, len(definition.Skills)+len(definition.TaskKinds))
	for _, id := range definition.Skills {
		description := skillDescriptions[id]
		if description == "" {
			description = "Inkuna skill package " + id
		}
		skills = append(skills, a2a.AgentSkill{ID: id, Name: id, Description: description, Tags: []string{"skill"}})
	}
	for _, kind := range definition.TaskKinds {
		skills = append(skills, a2a.AgentSkill{ID: "task:" + string(kind), Name: string(kind), Description: "Handles " + string(kind) + " tasks", Tags: []string{"task-kind"}})
	}
	metadata := map[string]any{"source": string(definition.Source), "permission": string(definition.Permission)}
	if definition.Runtime != "" {
		metadata["runtime"] = string(definition.Runtime)
	}
	if len(definition.Tags) > 0 {
		metadata["tags"] = definition.Tags
	}
	return a2a.AgentCard{
		Name:               definition.Name,
		Description:        definition.Description,
		URL:                "inkuna://agents/" + definition.Name,
		Version:            "1",
		ProtocolVersion:    a2a.ProtocolVersion,
		Provider:           &a2a.AgentProvider{Organization: "Inkuna"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true, StateTransitionHistory: true},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Skills:             skills,
		Metadata:           metadata,
	}
}

// HumanCard is the card for the user, who participates in the bus like any
// other agent but answers through the desktop inbox.
func HumanCard() a2a.AgentCard {
	return a2a.AgentCard{
		Name: Human, Description: "The Inkuna user. Receives questions in the desktop inbox and answers them.",
		URL: "inkuna://agents/human", Version: "1", ProtocolVersion: a2a.ProtocolVersion,
		Capabilities:       a2a.AgentCapabilities{Streaming: true, StateTransitionHistory: true},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             []a2a.AgentSkill{{ID: "decide", Name: "Decide", Description: "Answers questions that require human judgement or approval"}},
		Metadata:           map[string]any{"source": "builtin", "human": true},
	}
}

// Load parses one agent Markdown file.
func Load(path string, source Source) (Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("read agent %s: %w", path, err)
	}
	definition, err := Parse(string(data))
	if err != nil {
		return Definition{}, fmt.Errorf("%s: %w", path, err)
	}
	expected := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if definition.Name != expected {
		return Definition{}, fmt.Errorf("%s: agent name %q must match file name %q", path, definition.Name, expected)
	}
	definition.Source = source
	definition.Path = path
	return definition, nil
}

// Parse decodes frontmatter plus body.
func Parse(content string) (Definition, error) {
	header, body, err := frontmatter.Split(content)
	if err != nil {
		return Definition{}, fmt.Errorf("agent %w", err)
	}
	definition := Definition{Permission: domain.PermissionReadOnly}
	known, extra, err := splitFrontmatter(header)
	if err != nil {
		return Definition{}, fmt.Errorf("parse agent frontmatter: %w", err)
	}
	if err := known.Decode(&definition); err != nil {
		return Definition{}, fmt.Errorf("parse agent frontmatter: %w", err)
	}
	definition.Extra = extra
	definition.Instructions = strings.TrimSpace(body)
	if err := definition.Validate(); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

// Discover loads every *.md agent in a directory. A missing directory yields
// no agents; a malformed file is an error so problems surface immediately.
func Discover(directory string, source Source) ([]Definition, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	definitions := make([]Definition, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		definition, err := Load(filepath.Join(directory, entry.Name()), source)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

// Registry is the merged view of built-in, global, and repository agents.
type Registry struct{ definitions map[string]Definition }

// NewRegistry merges definitions; later slices override earlier by name.
func NewRegistry(layers ...[]Definition) Registry {
	registry := Registry{definitions: make(map[string]Definition)}
	for _, layer := range layers {
		for _, definition := range layer {
			registry.definitions[definition.Name] = definition
		}
	}
	return registry
}

// Build assembles the registry for a repository: built-ins, then each global
// directory, then the repository's .inkuna/agents.
func Build(repositoryRoot string, globalDirectories []string) (Registry, error) {
	layers := [][]Definition{BuiltIn()}
	for _, directory := range globalDirectories {
		definitions, err := Discover(directory, SourceGlobal)
		if err != nil {
			return Registry{}, err
		}
		layers = append(layers, definitions)
	}
	if repositoryRoot != "" {
		definitions, err := Discover(filepath.Join(repositoryRoot, ".inkuna", "agents"), SourceRepository)
		if err != nil {
			return Registry{}, err
		}
		layers = append(layers, definitions)
	}
	return NewRegistry(layers...), nil
}

// WithRemote adds remote advisors to a copy of the registry. A remote agent
// never replaces a local one: sending a stage's context to a third party must
// not happen because two names happened to match. The names left out are
// returned so the caller can say so.
func (registry Registry) WithRemote(remotes []Definition) (Registry, []string) {
	merged := Registry{definitions: make(map[string]Definition, len(registry.definitions)+len(remotes))}
	for name, definition := range registry.definitions {
		merged.definitions[name] = definition
	}
	skipped := make([]string, 0)
	for _, definition := range remotes {
		if _, taken := merged.definitions[definition.Name]; taken || !ValidName(definition.Name) {
			skipped = append(skipped, definition.Name)
			continue
		}
		definition.Source, definition.Permission = SourceRemote, domain.PermissionReadOnly
		definition.Runtime, definition.Model, definition.ReasoningEffort, definition.Secrets, definition.Skills, definition.MaxTurns = "", "", "", nil, nil, 0
		definition.MCP, definition.Tracker = nil, nil
		merged.definitions[definition.Name] = definition
	}
	sort.Strings(skipped)
	return merged, skipped
}

// Get returns a definition by name.
func (registry Registry) Get(name string) (Definition, bool) {
	definition, ok := registry.definitions[name]
	return definition, ok
}

// Workers lists every assignable agent sorted by name; the orchestrator is
// excluded because it plans rather than executes.
func (registry Registry) Workers() []Definition {
	result := make([]Definition, 0, len(registry.definitions))
	for _, definition := range registry.definitions {
		if definition.Name != Orchestrator {
			result = append(result, definition)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// All lists every definition including the orchestrator.
func (registry Registry) All() []Definition {
	result := make([]Definition, 0, len(registry.definitions))
	for _, definition := range registry.definitions {
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// ForKind returns worker agents that declare support for a task kind, with
// general-purpose agents listed after specialists.
func (registry Registry) ForKind(kind domain.TaskKind) []Definition {
	specialists := make([]Definition, 0)
	generalists := make([]Definition, 0)
	for _, definition := range registry.Workers() {
		switch {
		case definition.Remote() && !definition.Auto:
			// Only a rule naming it may use a remote agent that is not marked
			// for automatic staffing.
		case len(definition.TaskKinds) == 0:
			generalists = append(generalists, definition)
		case definition.Supports(kind):
			specialists = append(specialists, definition)
		}
	}
	return append(specialists, generalists...)
}

// validRuntime checks the form of the name only: runtimes are declared in
// configuration, which the code saving or staffing a definition checks.
func validRuntime(runtime domain.Runtime) bool { return domain.ValidRuntimeName(runtime) }

func validTaskKind(kind domain.TaskKind) bool {
	switch kind {
	case domain.TaskResearch, domain.TaskBugfix, domain.TaskImplementation, domain.TaskReview, domain.TaskDocumentation, domain.TaskSkillAuthoring:
		return true
	default:
		return false
	}
}
