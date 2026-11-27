package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/pricing"
	"github.com/inkuna-ai/inkuna/internal/routing"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

const (
	EnvDataDir               = "INKUNA_DATA_DIR"
	EnvLinearAPIKey          = "LINEAR_API_KEY"
	EnvLinearEndpoint        = "INKUNA_LINEAR_ENDPOINT"
	EnvLinearTimeout         = "INKUNA_LINEAR_TIMEOUT"
	EnvHealthTimeout         = "INKUNA_HEALTH_TIMEOUT"
	EnvAuthenticationTimeout = "INKUNA_AUTHENTICATION_TIMEOUT"
	EnvMaxConcurrency        = "INKUNA_MAX_CONCURRENCY"
	EnvMaxTasks              = "INKUNA_MAX_TASKS"
	EnvCheckLogBytes         = "INKUNA_CHECK_LOG_BYTES"
	EnvPlanningTimeout       = "INKUNA_PLANNING_TIMEOUT"
	EnvWorkerTimeout         = "INKUNA_WORKER_TIMEOUT"
	EnvVerifierTimeout       = "INKUNA_VERIFIER_TIMEOUT"
	EnvApprovalTimeout       = "INKUNA_APPROVAL_TIMEOUT"
	EnvOrchestratorRuntime   = "INKUNA_ORCHESTRATOR_RUNTIME"
	EnvOrchestratorModel     = "INKUNA_ORCHESTRATOR_MODEL"
	EnvDesktopNotifications  = "INKUNA_DESKTOP_NOTIFICATIONS"
	EnvCodexBinary           = "INKUNA_CODEX_BINARY"
	EnvClaudeBinary          = "INKUNA_CLAUDE_BINARY"
	EnvCopilotBinary         = "INKUNA_COPILOT_BINARY"
	EnvGitBinary             = "INKUNA_GIT_BINARY"
	EnvGitHubBinary          = "INKUNA_GITHUB_BINARY"
	EnvGitHubHost            = "INKUNA_GITHUB_HOST"
	EnvGitHubGitProtocol     = "INKUNA_GITHUB_GIT_PROTOCOL"
	EnvGoBinary              = "INKUNA_GO_BINARY"
	EnvNodeBinary            = "INKUNA_NODE_BINARY"
	EnvNPMBinary             = "INKUNA_NPM_BINARY"
	EnvStageDirectories      = "INKUNA_STAGE_DIRECTORIES" // replaces stages.directories, separated like PATH
	defaultLinearEndpoint    = "https://api.linear.app/graphql"
	defaultMaxConcurrency    = 2
	defaultMaxTasks          = 3
	defaultPlanningTimeout   = 5 * time.Minute
	defaultWorkerTimeout     = 30 * time.Minute
	defaultVerifierTimeout   = 15 * time.Minute
	defaultApprovalTimeout   = 15 * time.Minute
	defaultHealthTimeout     = 3 * time.Second
	defaultAuthTimeout       = 5 * time.Minute
	defaultLinearTimeout     = 15 * time.Second
)

type Binaries struct {
	Codex   string
	Claude  string
	Copilot string
	Git     string
	GitHub  string
	Go      string
	Node    string
	NPM     string
}

// Config is the effective runtime configuration: defaults, overlaid by
// inkuna.yaml, overlaid by the process environment.
type Config struct {
	DataDir        string
	ConfigPath     string
	ConfigExists   bool
	File           File
	LinearAPIKey   string
	LinearEndpoint string
	LinearTimeout  time.Duration
	HealthTimeout  time.Duration
	AuthTimeout    time.Duration
	MaxConcurrency int
	MaxTasks       int
	// CheckLogBytes caps each evaluation command's kept log.
	CheckLogBytes        int
	PlanningTimeout      time.Duration
	WorkerTimeout        time.Duration
	VerifierTimeout      time.Duration
	ApprovalTimeout      time.Duration
	Orchestrator         OrchestratorConfig
	DesktopNotifications bool
	Binaries             Binaries
	GitHubHost           string
	GitHubProtocol       string
	RuntimeSecrets       map[domain.Runtime][]string
	// Runtimes is every available runtime, built-ins first: the file's
	// declarations with binaries resolved through defaults and environment.
	Runtimes         []agentruntime.Spec
	AgentDirectories []string
	// StageDirectories are the global stage library directories:
	// INKUNA_STAGE_DIRECTORIES, else stages.directories, else <dataDir>/stages.
	StageDirectories []string
	// RoutingPolicy is the built-in policy with inkuna.yaml's bands applied.
	RoutingPolicy routing.Policy
	// Pricing is the built-in pricing table with inkuna.yaml's prices
	// applied.
	Pricing pricing.Table
}

// Load resolves configuration. Environment variables always win over
// inkuna.yaml so a single launch can be adjusted without editing the file.
func Load() (Config, error) {
	EnsureSearchPath()
	dataDir := strings.TrimSpace(os.Getenv(EnvDataDir))
	if dataDir == "" {
		var err error
		if dataDir, err = DefaultDataDir(); err != nil {
			return Config{}, err
		}
	}
	path := DefaultPath(dataDir)
	file, exists, err := ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	// A default-location file whose dataDir points elsewhere is a locator
	// written by the setup wizard: the real configuration lives in that
	// directory, so follow it when the environment did not choose one.
	if exists && strings.TrimSpace(os.Getenv(EnvDataDir)) == "" && strings.TrimSpace(os.Getenv(EnvConfigFile)) == "" && strings.TrimSpace(file.DataDir) != "" && file.DataDir != dataDir {
		dataDir = file.DataDir
		redirected := filepath.Join(dataDir, "inkuna.yaml")
		if target, targetExists, err := ReadFile(redirected); err != nil {
			return Config{}, err
		} else if targetExists {
			file, path = target, redirected
		}
	}
	return FromFile(dataDir, path, exists, file)
}

// DefaultDataDir is where Inkuna keeps its data when neither the environment
// nor a locator names another directory. Startup always looks here first.
func DefaultDataDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user configuration directory: %w", err)
	}
	return filepath.Join(root, "Inkuna"), nil
}

// FromFile builds the effective configuration from an already-parsed file and
// the current environment. Tests use it to avoid touching the user's disk.
func FromFile(dataDir, path string, exists bool, file File) (Config, error) {
	if err := file.Validate(); err != nil {
		return Config{}, err
	}
	defaults := DefaultFile()
	configuration := Config{
		DataDir:              dataDir,
		ConfigPath:           path,
		ConfigExists:         exists,
		File:                 file,
		LinearAPIKey:         strings.TrimSpace(os.Getenv(EnvLinearAPIKey)),
		LinearEndpoint:       envOrDefault(EnvLinearEndpoint, firstNonEmpty(file.Linear.Endpoint, defaultLinearEndpoint)),
		LinearTimeout:        defaultLinearTimeout,
		HealthTimeout:        defaultHealthTimeout,
		AuthTimeout:          defaultAuthTimeout,
		MaxConcurrency:       firstPositive(file.Concurrency.MaxWorkers, defaultMaxConcurrency),
		MaxTasks:             firstPositive(file.Concurrency.MaxTasks, defaultMaxTasks),
		CheckLogBytes:        firstPositive(file.Checks.LogBytes, DefaultCheckLogBytes),
		PlanningTimeout:      defaultPlanningTimeout,
		WorkerTimeout:        defaultWorkerTimeout,
		VerifierTimeout:      defaultVerifierTimeout,
		ApprovalTimeout:      defaultApprovalTimeout,
		Orchestrator:         file.Orchestrator,
		DesktopNotifications: file.Notifications.Desktop,
		GitHubHost:           envOrDefault(EnvGitHubHost, firstNonEmpty(file.GitHub.Host, "github.com")),
		GitHubProtocol:       strings.ToLower(envOrDefault(EnvGitHubGitProtocol, firstNonEmpty(file.GitHub.Protocol, "https"))),
		Binaries: Binaries{
			Codex:   envOrDefault(EnvCodexBinary, firstNonEmpty(file.Runtimes[domain.RuntimeCodex].Binary, defaults.Runtimes[domain.RuntimeCodex].Binary)),
			Claude:  envOrDefault(EnvClaudeBinary, firstNonEmpty(file.Runtimes[domain.RuntimeClaude].Binary, defaults.Runtimes[domain.RuntimeClaude].Binary)),
			Copilot: envOrDefault(EnvCopilotBinary, firstNonEmpty(file.Runtimes[domain.RuntimeCopilot].Binary, defaults.Runtimes[domain.RuntimeCopilot].Binary)),
			Git:     envOrDefault(EnvGitBinary, "git"),
			GitHub:  envOrDefault(EnvGitHubBinary, firstNonEmpty(file.GitHub.Binary, "gh")),
			Go:      envOrDefault(EnvGoBinary, "go"),
			Node:    envOrDefault(EnvNodeBinary, "node"),
			NPM:     envOrDefault(EnvNPMBinary, "npm"),
		},
		RuntimeSecrets:   make(map[domain.Runtime][]string, len(file.Runtimes)),
		AgentDirectories: append([]string(nil), file.Agents.Directories...),
		RoutingPolicy:    routing.DefaultPolicy().WithBands(file.Routing.Bands),
		Pricing:          pricing.Default().With(file.Pricing),
	}
	builtInBinaries := map[domain.Runtime]string{
		domain.RuntimeCodex: configuration.Binaries.Codex, domain.RuntimeClaude: configuration.Binaries.Claude, domain.RuntimeCopilot: configuration.Binaries.Copilot,
	}
	for _, name := range file.RuntimeNames() {
		entry := file.Runtimes[name]
		spec := entry.Spec(name)
		if binary, builtIn := builtInBinaries[name]; builtIn {
			spec.Binary = binary
		}
		configuration.Runtimes = append(configuration.Runtimes, spec)
		configuration.RuntimeSecrets[name] = append([]string(nil), entry.Secrets...)
	}
	if configuration.Orchestrator.Runtime == "" {
		configuration.Orchestrator.Runtime = domain.RuntimeClaude
	}
	if configuration.Orchestrator.MaxTurns < 0 {
		configuration.Orchestrator.MaxTurns = 0 // unlimited: the planning timeout bounds the call
	}
	if value := strings.TrimSpace(os.Getenv(EnvOrchestratorRuntime)); value != "" {
		configuration.Orchestrator.Runtime = domain.Runtime(value)
		if !file.DeclaresRuntime(configuration.Orchestrator.Runtime) {
			return Config{}, fmt.Errorf("%s must name a declared runtime", EnvOrchestratorRuntime)
		}
	}
	if value := strings.TrimSpace(os.Getenv(EnvOrchestratorModel)); value != "" {
		configuration.Orchestrator.Model = value
	}
	if value := strings.TrimSpace(os.Getenv(EnvDesktopNotifications)); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("%s must be a boolean", EnvDesktopNotifications)
		}
		configuration.DesktopNotifications = enabled
	}
	if len(configuration.AgentDirectories) == 0 {
		configuration.AgentDirectories = []string{filepath.Join(dataDir, "agents")}
	}
	configuration.StageDirectories = stageDirectories(dataDir, file.Stages.Directories)

	var err error
	timeouts := []struct {
		target   *time.Duration
		env      string
		fileVal  string
		fallback time.Duration
	}{
		{&configuration.LinearTimeout, EnvLinearTimeout, file.Timeouts.Linear, defaultLinearTimeout},
		{&configuration.HealthTimeout, EnvHealthTimeout, file.Timeouts.Health, defaultHealthTimeout},
		{&configuration.AuthTimeout, EnvAuthenticationTimeout, file.Timeouts.Auth, defaultAuthTimeout},
		{&configuration.PlanningTimeout, EnvPlanningTimeout, file.Timeouts.Planning, defaultPlanningTimeout},
		{&configuration.WorkerTimeout, EnvWorkerTimeout, file.Timeouts.Worker, defaultWorkerTimeout},
		{&configuration.VerifierTimeout, EnvVerifierTimeout, file.Timeouts.Verifier, defaultVerifierTimeout},
		{&configuration.ApprovalTimeout, EnvApprovalTimeout, file.Timeouts.Approval, defaultApprovalTimeout},
	}
	for _, entry := range timeouts {
		fallback := entry.fallback
		if parsed, parseErr := parseOptionalDuration(entry.fileVal); parseErr != nil {
			return Config{}, parseErr
		} else if parsed > 0 {
			fallback = parsed
		}
		if *entry.target, err = duration(entry.env, fallback); err != nil {
			return Config{}, err
		}
	}
	if configuration.MaxConcurrency, err = positiveInt(EnvMaxConcurrency, configuration.MaxConcurrency); err != nil {
		return Config{}, err
	}
	if configuration.MaxTasks, err = positiveInt(EnvMaxTasks, configuration.MaxTasks); err != nil {
		return Config{}, err
	}
	if configuration.CheckLogBytes, err = positiveInt(EnvCheckLogBytes, configuration.CheckLogBytes); err != nil {
		return Config{}, err
	}
	if configuration.CheckLogBytes < MinCheckLogBytes || configuration.CheckLogBytes > MaxCheckLogBytes {
		return Config{}, fmt.Errorf("%s must be between %d and %d bytes", EnvCheckLogBytes, MinCheckLogBytes, MaxCheckLogBytes)
	}
	if configuration.GitHubProtocol != "https" && configuration.GitHubProtocol != "ssh" {
		return Config{}, fmt.Errorf("%s must be https or ssh", EnvGitHubGitProtocol)
	}
	if strings.TrimSpace(configuration.DataDir) == "" {
		return Config{}, errors.New("inkuna data directory is required")
	}
	return configuration, nil
}

// SecretNames lists every secret the configuration declares.
func (configuration Config) SecretNames() []string { return configuration.File.SecretNames() }

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func parseOptionalDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("timeout %q must be a positive Go duration", value)
	}
	return parsed, nil
}

func duration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return parsed, nil
}

func positiveInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

// stageDirectories resolves the global stage library directories. The
// environment variable replaces the file's list, as every override does,
// and entries it leaves blank are dropped.
func stageDirectories(dataDir string, declared []string) []string {
	directories := declared
	if value := strings.TrimSpace(os.Getenv(EnvStageDirectories)); value != "" {
		directories = nil
		for _, entry := range filepath.SplitList(value) {
			if entry = strings.TrimSpace(entry); entry != "" {
				directories = append(directories, entry)
			}
		}
	}
	if len(directories) == 0 {
		return []string{filepath.Join(dataDir, "stages")}
	}
	return slices.Clone(directories)
}
