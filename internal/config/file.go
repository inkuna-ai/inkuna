package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/a2a/remote"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/docs"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/pricing"
	"github.com/inkuna-ai/inkuna/internal/routing"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/internal/yamlnode"
	"gopkg.in/yaml.v3"
)

// FileVersion is the inkuna.yaml schema version this build understands.
const FileVersion = 1

// EnvConfigFile overrides the inkuna.yaml location.
const EnvConfigFile = "INKUNA_CONFIG"

// File is the on-disk configuration. It never contains credential values:
// the secrets block only declares which named secrets Inkuna should resolve
// from the keychain or environment.
type File struct {
	Version       int                 `yaml:"version" json:"version"`
	DataDir       string              `yaml:"dataDir,omitempty" json:"dataDir,omitempty"`
	Orchestrator  OrchestratorConfig  `yaml:"orchestrator" json:"orchestrator"`
	Runtimes      RuntimesConfig      `yaml:"runtimes" json:"runtimes"`
	Concurrency   ConcurrencyConfig   `yaml:"concurrency" json:"concurrency"`
	Timeouts      TimeoutsConfig      `yaml:"timeouts" json:"timeouts"`
	Notifications NotificationsConfig `yaml:"notifications" json:"notifications"`
	Secrets       []SecretRef         `yaml:"secrets" json:"secrets"`
	Repositories  []RepositoryConfig  `yaml:"repositories" json:"repositories"`
	Agents        AgentsConfig        `yaml:"agents" json:"agents"`
	Stages        StagesConfig        `yaml:"stages,omitempty" json:"stages"`
	Linear        LinearConfig        `yaml:"linear" json:"linear"`
	GitHub        GitHubConfig        `yaml:"github" json:"github"`
	Routing       RoutingConfig       `yaml:"routing,omitempty" json:"routing"`
	RemoteAgents  []RemoteAgentConfig `yaml:"remoteAgents,omitempty" json:"remoteAgents,omitempty"`
	// MCPServers declares the MCP servers this machine offers workers; a
	// rule stage or an agent grants them by name.
	MCPServers []MCPServerConfig `yaml:"mcpServers,omitempty" json:"mcpServers,omitempty"`
	// Budgets caps, in bytes, what each part of a worker's context envelope
	// may carry; an unset budget takes its default (domain.DefaultBudgets).
	Budgets domain.Budgets `yaml:"budgets,omitempty" json:"budgets"`
	// Pricing adds or replaces model prices in the built-in pricing table
	// and prices a Copilot premium request.
	Pricing pricing.Overrides `yaml:"pricing,omitempty" json:"pricing"`
	// Checks shapes how evaluation commands' output is kept.
	Checks ChecksConfig `yaml:"checks,omitempty" json:"checks"`
	// Extensions holds each extension's section, keyed by extension name,
	// as written: the extension decodes it strictly itself when the app
	// starts. A section for an extension the build does not have is kept
	// and ignored, so the data directory still opens in a build without it,
	// such as inkunactl's. Aliases in a section are expanded when the file
	// is read, so a section stands alone. The view neither sees nor edits
	// the sections, so a Settings save keeps them as they were loaded.
	Extensions map[string]yaml.Node `yaml:"extensions,omitempty" json:"-"`
}

// ChecksConfig shapes how evaluation commands' output is kept.
type ChecksConfig struct {
	// LogBytes caps the log kept for each command: its head and, mostly,
	// its tail, with a marker for what was cut between them. Zero takes
	// DefaultCheckLogBytes.
	LogBytes int `yaml:"logBytes,omitempty" json:"logBytes,omitempty"`
}

// A check's log keeps at least MinCheckLogBytes, so its head and tail both
// hold some lines, and at most MaxCheckLogBytes, which Inkuna holds in
// memory while the command runs.
const (
	DefaultCheckLogBytes = 1 << 20
	MinCheckLogBytes     = 4 << 10
	MaxCheckLogBytes     = 64 << 20
)

type OrchestratorConfig struct {
	Runtime         domain.Runtime `yaml:"runtime" json:"runtime"`
	Model           string         `yaml:"model,omitempty" json:"model,omitempty"`
	ReasoningEffort string         `yaml:"reasoningEffort,omitempty" json:"reasoningEffort,omitempty"`
	// MaxTurns bounds the planning call's model turns; zero or absent means
	// unlimited, with only the planning timeout as the bound.
	MaxTurns int `yaml:"maxTurns,omitempty" json:"maxTurns,omitempty"`
	// ReadTools lets the staffing call read the repository: it runs in the
	// repository with read-only tools instead of in an empty directory with
	// none.
	ReadTools bool `yaml:"readTools,omitempty" json:"readTools,omitempty"`
	// Disabled falls back to deterministic rule-only assignment.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// RuntimesConfig declares headless CLIs by name. codex, claude, and copilot
// are built in: their entries only adjust the binary and exported secrets, and
// they exist whether or not the file lists them. Any other name is a runtime
// the user added, either a copy of a built-in (preset) or an argument template.
type RuntimesConfig map[domain.Runtime]RuntimeConfig

type RuntimeConfig struct {
	// Label is the name shown in the app; it defaults to the key.
	Label string `yaml:"label,omitempty" json:"label,omitempty"`
	// Preset runs this entry through a built-in adapter, which builds its own
	// arguments. The template fields below must then be empty.
	Preset domain.Runtime `yaml:"preset,omitempty" json:"preset,omitempty"`
	Binary string         `yaml:"binary,omitempty" json:"binary,omitempty"`
	// Args is the argument template for every run; ReadOnlyArgs or WriteArgs
	// is appended for the step's permission. Without WriteArgs the runtime
	// only runs read-only steps.
	Args         []string `yaml:"args,omitempty" json:"args,omitempty"`
	ReadOnlyArgs []string `yaml:"readOnlyArgs,omitempty" json:"readOnlyArgs,omitempty"`
	WriteArgs    []string `yaml:"writeArgs,omitempty" json:"writeArgs,omitempty"`
	// Stdin is "prompt" or "none"; Output is how stdout is read.
	Stdin  string `yaml:"stdin,omitempty" json:"stdin,omitempty"`
	Output string `yaml:"output,omitempty" json:"output,omitempty"`
	// VersionArgs is the health probe; it defaults to --version.
	VersionArgs []string `yaml:"versionArgs,omitempty" json:"versionArgs,omitempty"`
	// Secrets lists secret names exported into this runtime's worker
	// processes. Nothing is exported unless declared here.
	Secrets []string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	// UserConfig lets a built-in adapter load the user's own CLI setup
	// (settings, hooks, skills, memory, plugins, and for Codex its config.toml
	// with any MCP servers). It is off by default and refused on a template.
	UserConfig bool `yaml:"userConfig,omitempty" json:"userConfig,omitempty"`
	// UserMCP names the user's own MCP servers, as the CLI lists them, that
	// every step on this runtime is given with all their tools. It is the
	// only way a worker starts one of them, userConfig or not, and is
	// refused on a template.
	UserMCP []string `yaml:"userMcp,omitempty" json:"userMcp,omitempty"`
}

// Spec is the declaration in the form the runtime package runs.
func (entry RuntimeConfig) Spec(name domain.Runtime) agentruntime.Spec {
	return agentruntime.Spec{
		Name: name, Label: entry.Label, Preset: entry.Preset, Binary: strings.TrimSpace(entry.Binary),
		Args: entry.Args, ReadOnlyArgs: entry.ReadOnlyArgs, WriteArgs: entry.WriteArgs,
		Stdin: entry.Stdin, Output: entry.Output, VersionArgs: entry.VersionArgs, UserConfig: entry.UserConfig,
		UserMCP: append([]string(nil), entry.UserMCP...),
	}
}

// BuiltInRuntimes lists the preset names in the order the app shows them.
func BuiltInRuntimes() []domain.Runtime {
	return []domain.Runtime{domain.RuntimeClaude, domain.RuntimeCodex, domain.RuntimeCopilot}
}

// RuntimeNames lists every runtime the file makes available: the built-ins,
// then the ones it adds, by name.
func (file File) RuntimeNames() []domain.Runtime {
	names := BuiltInRuntimes()
	added := make([]string, 0, len(file.Runtimes))
	for name := range file.Runtimes {
		if !agentruntime.IsPreset(name) {
			added = append(added, string(name))
		}
	}
	sort.Strings(added)
	for _, name := range added {
		names = append(names, domain.Runtime(name))
	}
	return names
}

// DeclaresRuntime reports whether name is built in or added by the file.
func (file File) DeclaresRuntime(name domain.Runtime) bool {
	if agentruntime.IsPreset(name) {
		return true
	}
	_, declared := file.Runtimes[name]
	return declared
}

type ConcurrencyConfig struct {
	// MaxTasks bounds concurrently executing workflows.
	MaxTasks int `yaml:"maxTasks,omitempty" json:"maxTasks,omitempty"`
	// MaxWorkers bounds concurrently running steps inside one workflow.
	MaxWorkers int `yaml:"maxWorkers,omitempty" json:"maxWorkers,omitempty"`
}

type TimeoutsConfig struct {
	Planning string `yaml:"planning,omitempty" json:"planning,omitempty"`
	Worker   string `yaml:"worker,omitempty" json:"worker,omitempty"`
	Verifier string `yaml:"verifier,omitempty" json:"verifier,omitempty"`
	Approval string `yaml:"approval,omitempty" json:"approval,omitempty"`
	Health   string `yaml:"health,omitempty" json:"health,omitempty"`
	Auth     string `yaml:"auth,omitempty" json:"auth,omitempty"`
	Linear   string `yaml:"linear,omitempty" json:"linear,omitempty"`
}

type NotificationsConfig struct {
	Desktop bool `yaml:"desktop" json:"desktop"`
}

// SecretRef declares a secret by name. Source is advisory: the resolver always
// checks the environment first and then the keychain.
type SecretRef struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

type RepositoryConfig struct {
	Name           string         `yaml:"name" json:"name"`
	Path           string         `yaml:"path" json:"path"`
	DefaultRuntime domain.Runtime `yaml:"defaultRuntime" json:"defaultRuntime"`
	// Rules overrides .inkuna/rules.yaml; relative paths resolve against the
	// repository.
	Rules string `yaml:"rules,omitempty" json:"rules,omitempty"`
	// Docs is the documentation root, relative to the repository and confined
	// to it. Empty means docs.DefaultDir.
	Docs string `yaml:"docs,omitempty" json:"docs,omitempty"`
	// Tracker lowers, on this machine, what the repository's
	// .inkuna/tracker.yaml lets Inkuna write to its tracker.
	Tracker *TrackerOverride `yaml:"tracker,omitempty" json:"tracker,omitempty"`
}

type AgentsConfig struct {
	// Directories hold global agent definitions in addition to each
	// repository's .inkuna/agents directory.
	Directories []string `yaml:"directories,omitempty" json:"directories,omitempty"`
}

// StagesConfig declares the global stage library: shared stages and groups
// every repository's rules may use besides their own.
type StagesConfig struct {
	// Directories hold global stage library files (*.yaml, with stages and
	// groups in the rules schema and no rules). A definition in one
	// overrides a built-in one with the same name, and a repository's own
	// overrides it.
	Directories []string `yaml:"directories,omitempty" json:"directories,omitempty"`
}

type LinearConfig struct {
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
}

type GitHubConfig struct {
	Host     string `yaml:"host,omitempty" json:"host,omitempty"`
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	Binary   string `yaml:"binary,omitempty" json:"binary,omitempty"`
}

type RoutingConfig struct {
	// Bands replaces the built-in complexity-to-model bands for the runtimes
	// it names; a runtime left out keeps the built-in bands.
	Bands map[domain.Runtime][]routing.Band `yaml:"bands,omitempty" json:"bands,omitempty"`
}

// RemoteAgentConfig connects one external A2A agent as a read-only advisor.
// See docs/remote-a2a.md for what it is and is not given.
type RemoteAgentConfig struct {
	// Name is the agent name rules and threads use. It cannot match a
	// built-in agent; a global or repository agent of the same name wins.
	Name    string `yaml:"name" json:"name"`
	CardURL string `yaml:"cardUrl" json:"cardUrl"`
	// Enabled agents are visible to planning. Enabling needs a pinned card.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Secret names a declared secret presented as the agent's token. It is
	// never exported to worker processes.
	Secret string `yaml:"secret,omitempty" json:"secret,omitempty"`
	// Auto lets the orchestrator pick the agent for read-only auto stages.
	// Without it only a rule naming the agent uses it.
	Auto bool `yaml:"auto,omitempty" json:"auto,omitempty"`
	// SendArtifacts inlines upstream text artifacts, up to MaxArtifactBytes in
	// total. Off, the agent only sees their names, types, sizes, and digests.
	SendArtifacts    bool `yaml:"sendArtifacts,omitempty" json:"sendArtifacts,omitempty"`
	MaxArtifactBytes int  `yaml:"maxArtifactBytes,omitempty" json:"maxArtifactBytes,omitempty"`
	// AllowInsecure permits plain http and private or loopback addresses, for
	// developing an agent locally. The app never sets it.
	AllowInsecure bool `yaml:"allowInsecure,omitempty" json:"allowInsecure,omitempty"`
	// Description and Skills are copied from the card when it is pinned, so
	// planning can describe the agent without fetching anything.
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Skills      []string `yaml:"skills,omitempty" json:"skills,omitempty"`
	// Pinned is written by Inkuna when the user reviews the card.
	Pinned *remote.Pin `yaml:"pinned,omitempty" json:"pinned,omitempty"`
}

// Limits for artifact bodies sent to a remote agent.
const (
	DefaultRemoteArtifactBytes = 256 << 10
	MaxRemoteArtifactBytes     = 1 << 20
)

// ArtifactBytes is the effective cap on inlined artifact bodies.
func (entry RemoteAgentConfig) ArtifactBytes() int {
	if entry.MaxArtifactBytes <= 0 {
		return DefaultRemoteArtifactBytes
	}
	return entry.MaxArtifactBytes
}

// Usable reports whether planning may see the agent: the user enabled it and
// reviewed its card.
func (entry RemoteAgentConfig) Usable() bool { return entry.Enabled && entry.Pinned != nil }

// Repository returns the first entry declared for a repository path. The
// app and inkunactl both find a repository's entry through it, so they agree
// on which one applies.
func (file File) Repository(path string) (RepositoryConfig, bool) {
	path = filepath.Clean(path)
	for _, entry := range file.Repositories {
		if filepath.Clean(entry.Path) == path {
			return entry, true
		}
	}
	return RepositoryConfig{}, false
}

// RemoteAgent returns the named entry.
func (file File) RemoteAgent(name string) (RemoteAgentConfig, bool) {
	for _, entry := range file.RemoteAgents {
		if entry.Name == name {
			return entry, true
		}
	}
	return RemoteAgentConfig{}, false
}

// RemoteDefinitions describes the usable remote agents to the agent registry.
// Their card skills become part of the description: the registry's skills are
// local skill packages, which a remote agent never receives.
func (file File) RemoteDefinitions() []agent.Definition {
	definitions := make([]agent.Definition, 0, len(file.RemoteAgents))
	for _, entry := range file.RemoteAgents {
		if !entry.Usable() {
			continue
		}
		description := firstNonEmpty(entry.Description, "Remote A2A advisor")
		if len(entry.Skills) > 0 {
			description += " Skills: " + strings.Join(entry.Skills, ", ") + "."
		}
		definitions = append(definitions, agent.Definition{
			Name: entry.Name, Description: "Remote read-only advisor. " + description, Permission: domain.PermissionReadOnly, Source: agent.SourceRemote, Auto: entry.Auto,
		})
	}
	return definitions
}

// DefaultFile returns the configuration the setup wizard starts from.
func DefaultFile() File {
	return File{
		Version:      FileVersion,
		Orchestrator: OrchestratorConfig{Runtime: domain.RuntimeClaude},
		Runtimes: RuntimesConfig{
			domain.RuntimeCodex:   {Binary: "codex"},
			domain.RuntimeClaude:  {Binary: "claude"},
			domain.RuntimeCopilot: {Binary: "copilot"},
		},
		Concurrency:   ConcurrencyConfig{MaxTasks: 3, MaxWorkers: defaultMaxConcurrency},
		Timeouts:      TimeoutsConfig{Planning: "5m", Worker: "30m", Verifier: "15m", Approval: "15m", Health: "3s", Auth: "5m", Linear: "15s"},
		Notifications: NotificationsConfig{Desktop: true},
		Secrets: []SecretRef{
			{Name: EnvLinearAPIKey, Description: "Linear personal API key"},
		},
		Linear: LinearConfig{Endpoint: defaultLinearEndpoint},
		GitHub: GitHubConfig{Host: "github.com", Protocol: "https", Binary: "gh"},
	}
}

// FieldError is one validation problem. Field is the dotted path the settings
// view uses for the control, so the message can be shown beside it; list
// entries report the list itself ("secrets", "repositories").
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (problem FieldError) Error() string { return problem.Message }

// Validate rejects a file that would produce an unusable runtime.
func (file File) Validate() error {
	if problems := file.Problems(); len(problems) > 0 {
		return problems[0]
	}
	return nil
}

// Problems lists everything Validate would reject, in file order.
func (file File) Problems() []FieldError {
	problems := make([]FieldError, 0)
	report := func(field, format string, args ...any) {
		problems = append(problems, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	if file.Version != FileVersion {
		report("version", "unsupported inkuna.yaml version %d", file.Version)
	}
	if file.Orchestrator.Runtime != "" && !file.DeclaresRuntime(file.Orchestrator.Runtime) {
		report("orchestrator.runtime", "orchestrator runtime %q is not a declared runtime", file.Orchestrator.Runtime)
	}
	if bytes := file.Checks.LogBytes; bytes != 0 && (bytes < MinCheckLogBytes || bytes > MaxCheckLogBytes) {
		report("checks.logBytes", "checks.logBytes must be 0 for the default or between %d and %d bytes", MinCheckLogBytes, MaxCheckLogBytes)
	}
	for _, name := range file.Budgets.Invalid() {
		report("budgets."+name, "budgets.%s must be 0 for the default or at least %d bytes", name, domain.MinBudget)
	}
	for _, limit := range []struct {
		field string
		value int
	}{
		{"concurrency.maxTasks", file.Concurrency.MaxTasks},
		{"concurrency.maxWorkers", file.Concurrency.MaxWorkers},
		{"orchestrator.maxTurns", file.Orchestrator.MaxTurns},
	} {
		if limit.value < 0 {
			report(limit.field, "concurrency and turn limits must not be negative")
		}
	}
	seen := make(map[string]bool, len(file.Secrets))
	for _, secret := range file.Secrets {
		if err := secrets.ValidateName(secret.Name); err != nil {
			report("secrets", "%s", err.Error())
			continue
		}
		if seen[secret.Name] {
			report("secrets", "secret %q is declared twice", secret.Name)
		}
		seen[secret.Name] = true
	}
	// Map order is random; report runtimes in a stable order.
	declared := make([]string, 0, len(file.Runtimes))
	for name := range file.Runtimes {
		declared = append(declared, string(name))
	}
	sort.Strings(declared)
	for _, key := range declared {
		name := domain.Runtime(key)
		entry := file.Runtimes[name]
		if err := entry.Spec(name).Validate(); err != nil {
			report(fmt.Sprintf("runtimes.%s", name), "%s", err.Error())
		}
		for _, secret := range entry.Secrets {
			if !seen[secret] {
				report(fmt.Sprintf("runtimes.%s.secrets", name), "runtime secret %q must be declared in the secrets block", secret)
			}
		}
		// A step can be granted a declared server and given the user's own
		// at once, and a CLI cannot start two servers under one name.
		for _, server := range entry.UserMCP {
			if _, clash := file.MCPServer(server); clash {
				report(fmt.Sprintf("runtimes.%s.userMcp", name), "%q is also the name of an MCP server inkuna.yaml declares; rename one of them", server)
			}
		}
	}
	names := make(map[string]bool, len(file.Repositories))
	for _, repository := range file.Repositories {
		if strings.TrimSpace(repository.Name) == "" || strings.TrimSpace(repository.Path) == "" {
			report("repositories", "every repository needs a name and path")
			continue
		}
		if names[repository.Name] {
			report("repositories", "repository %q is declared twice", repository.Name)
		}
		names[repository.Name] = true
		if repository.DefaultRuntime != "" && !file.DeclaresRuntime(repository.DefaultRuntime) {
			report("repositories", "repository %q has invalid runtime %q", repository.Name, repository.DefaultRuntime)
		}
		if err := docs.ValidateDir(repository.Docs); err != nil {
			report("repositories", "repository %q: %s", repository.Name, err.Error())
		}
		for _, problem := range repository.trackerProblems() {
			report("repositories", "%s", problem)
		}
	}
	for _, directory := range file.Stages.Directories {
		if strings.TrimSpace(directory) == "" {
			report("stages.directories", "a stage library directory must not be blank")
			break
		}
	}
	if file.GitHub.Protocol != "" && file.GitHub.Protocol != "https" && file.GitHub.Protocol != "ssh" {
		report("github.protocol", "github protocol must be https or ssh")
	}
	for _, timeout := range []struct{ field, value string }{
		{"timeouts.planning", file.Timeouts.Planning},
		{"timeouts.worker", file.Timeouts.Worker},
		{"timeouts.verifier", file.Timeouts.Verifier},
		{"timeouts.approval", file.Timeouts.Approval},
		{"timeouts.health", file.Timeouts.Health},
		{"timeouts.auth", file.Timeouts.Auth},
		{"timeouts.linear", file.Timeouts.Linear},
	} {
		if _, err := parseOptionalDuration(timeout.value); err != nil {
			report(timeout.field, "%s", err.Error())
		}
	}
	// Map order is random; report routing problems in a stable runtime order.
	for _, runtimeName := range file.RuntimeNames() {
		if bands := file.Routing.Bands[runtimeName]; len(bands) > 0 {
			if err := routing.ValidateBands(bands); err != nil {
				report(fmt.Sprintf("routing.bands.%s", runtimeName), "%s routing: %s", runtimeName, err.Error())
			}
		}
	}
	for _, problem := range file.Pricing.Problems() {
		report(problem.Field, "%s", problem.Message)
	}
	problems = append(problems, file.remoteAgentProblems(seen)...)
	problems = append(problems, file.mcpServerProblems(seen)...)
	unknown := make([]string, 0)
	for runtimeName := range file.Routing.Bands {
		if !file.DeclaresRuntime(runtimeName) {
			unknown = append(unknown, string(runtimeName))
		}
	}
	sort.Strings(unknown)
	for _, runtimeName := range unknown {
		report("routing.bands", "routing bands name %q, which is not a declared runtime", runtimeName)
	}
	return problems
}

// remoteAgentProblems checks the remoteAgents block. Whether the card itself
// is acceptable was decided when it was pinned; this checks what a hand edit
// could break.
func (file File) remoteAgentProblems(declaredSecrets map[string]bool) []FieldError {
	problems := make([]FieldError, 0)
	report := func(format string, args ...any) {
		problems = append(problems, FieldError{Field: "remoteAgents", Message: fmt.Sprintf(format, args...)})
	}
	builtIn := make(map[string]bool)
	for _, definition := range agent.BuiltIn() {
		builtIn[definition.Name] = true
	}
	names := make(map[string]bool, len(file.RemoteAgents))
	for _, entry := range file.RemoteAgents {
		if !agent.ValidName(entry.Name) {
			report("remote agent name %q must be lower-case letters, digits, and dashes, and not a reserved name", entry.Name)
			continue
		}
		if builtIn[entry.Name] {
			report("remote agent %q has the name of a built-in agent", entry.Name)
		}
		if names[entry.Name] {
			report("remote agent %q is declared twice", entry.Name)
		}
		names[entry.Name] = true
		cardURL, err := remoteURL(entry.CardURL, entry.AllowInsecure)
		if err != nil {
			report("remote agent %q: card URL %s", entry.Name, err.Error())
		}
		if entry.Secret != "" && !declaredSecrets[entry.Secret] {
			report("remote agent %q uses secret %q, which must be declared in the secrets block", entry.Name, entry.Secret)
		}
		if entry.MaxArtifactBytes < 0 || entry.MaxArtifactBytes > MaxRemoteArtifactBytes {
			report("remote agent %q: maxArtifactBytes must be between 0 and %d", entry.Name, MaxRemoteArtifactBytes)
		}
		if entry.Pinned == nil {
			if entry.Enabled {
				report("remote agent %q cannot be enabled before its card is reviewed and pinned", entry.Name)
			}
			continue
		}
		endpoint, endpointErr := remoteURL(entry.Pinned.URL, entry.AllowInsecure)
		switch {
		case endpointErr != nil:
			report("remote agent %q: pinned endpoint %s", entry.Name, endpointErr.Error())
		case err == nil && !remote.SameOrigin(endpoint, cardURL):
			// The token goes to the pinned endpoint; it must be the origin the
			// user reviewed, not one a hand edit pointed it at.
			report("remote agent %q: the pinned endpoint is on a different origin than the card", entry.Name)
		}
		if !strings.HasPrefix(entry.Pinned.Digest, "sha256:") {
			report("remote agent %q: the pinned digest is not a sha256 digest", entry.Name)
		}
	}
	return problems
}

func remoteURL(raw string, allowInsecure bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("%q must be an absolute URL without credentials", raw)
	}
	if parsed.Scheme != "https" && (!allowInsecure || parsed.Scheme != "http") {
		return nil, fmt.Errorf("%q must use https", raw)
	}
	return parsed, nil
}

// SecretNames returns the declared secret names in file order.
func (file File) SecretNames() []string {
	names := make([]string, 0, len(file.Secrets))
	for _, secret := range file.Secrets {
		names = append(names, secret.Name)
	}
	return names
}

// DefaultPath returns where inkuna.yaml lives for the given data directory
// unless INKUNA_CONFIG points elsewhere.
func DefaultPath(dataDir string) string {
	if value := strings.TrimSpace(os.Getenv(EnvConfigFile)); value != "" {
		return value
	}
	return filepath.Join(dataDir, "inkuna.yaml")
}

// ReadFile parses inkuna.yaml. A missing file is not an error: it yields the
// defaults and reports exists=false so the UI can offer the setup wizard.
func ReadFile(path string) (File, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultFile(), false, nil
	}
	if err != nil {
		return File{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	file := DefaultFile()
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return File{}, true, fmt.Errorf("parse %s: %w", path, err)
	}
	// A configuration is exactly one document; trailing documents would be
	// silently ignored otherwise.
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return File{}, true, fmt.Errorf("parse %s: configuration must contain a single YAML document", path)
	}
	// A section is encoded again on every save, apart from the anchors its
	// aliases name, so each is expanded here: kept, an alias would be
	// written as a reference to an anchor that is gone or comes after it.
	for name, section := range file.Extensions {
		resolved, err := yamlnode.Resolve(&section, false)
		if err != nil {
			return File{}, true, fmt.Errorf("parse %s: extensions.%s: %w", path, name, err)
		}
		file.Extensions[name] = *resolved
	}
	if err := file.Validate(); err != nil {
		return File{}, true, fmt.Errorf("%s: %w", path, err)
	}
	return file, true, nil
}

// WriteFile persists inkuna.yaml with owner-only permissions. It refuses any
// file that fails validation so a broken configuration never reaches disk.
func WriteFile(path string, file File) error {
	if err := file.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := Encode(file)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// Encode is the content WriteFile writes for file, so a caller can name
// what it saved, by digest, without reading the file back after another
// save may have replaced it.
func Encode(file File) ([]byte, error) {
	data, err := yaml.Marshal(file)
	if err != nil {
		return nil, err
	}
	header := "# Inkuna configuration. Secret values are never stored here; the secrets\n# block only names credentials resolved from the OS keychain or environment.\n"
	return append([]byte(header), data...), nil
}
