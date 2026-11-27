package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/artifact"
	inkunaclaude "github.com/inkuna-ai/inkuna/internal/claude"
	"github.com/inkuna-ai/inkuna/internal/clock"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/filelock"
	inkunagithub "github.com/inkuna-ai/inkuna/internal/github"
	"github.com/inkuna-ai/inkuna/internal/linear"
	"github.com/inkuna-ai/inkuna/internal/notify"
	"github.com/inkuna-ai/inkuna/internal/prereq"
	"github.com/inkuna-ai/inkuna/internal/proc"
	"github.com/inkuna-ai/inkuna/internal/routing"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/internal/skill"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/version"
	"github.com/inkuna-ai/inkuna/pkg/extension"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Service is the Wails-bound application facade. Every exported method is a
// binding callable from the React view.
type Service struct {
	ctx     context.Context
	dataDir string
	// discovered is set when startup found the data directory itself (default
	// location, locator, or environment) rather than being handed one. Only
	// then may a save touch the files startup reads to find it.
	discovered bool
	// relocatedTo holds the configuration files this process wrote into a
	// newly chosen data directory, which later saves may keep current.
	relocatedTo map[string]bool
	// extensions is what the build's extensions registered, fixed by New.
	extensions *extensionRegistry
	// outbox delivers queued records to the registered sinks; nil in a
	// build without one.
	outbox *outboxDelivery
	// a2aStates is the last state of each unfinished A2A task an audit
	// event was recorded for, so a task's snapshots record its changes of
	// state rather than every message.
	a2aStatesMu sync.Mutex
	a2aStates   map[string]a2a.TaskState
	// answerers are who answered each permission request still being
	// delivered, by input ID, so the policies are asked about the person
	// who allowed it rather than whoever is asked for later.
	answerersMu sync.Mutex
	answerers   map[string]extension.Principal
	// agentFilesMu is held by an agent save from the check that its name
	// is free to the write, and by a deletion, so two saves cannot both find
	// a name free, which the policies' answer would otherwise leave seconds
	// to do, and the second silently replace the first.
	agentFilesMu sync.Mutex
	// auditWG tracks the audit events of saves and deletions still being
	// queued (auditChange).
	auditWG sync.WaitGroup
	// sourcesWG tracks the refresh of the definition sources' snapshots
	// that startup begins, which sourcesCancel stops.
	sourcesWG     sync.WaitGroup
	sourcesCancel context.CancelFunc
	config        config.Config
	// configMu orders a configuration save against starting a run: a save
	// holds it to replace config, and StartWorkflow holds it from checking
	// the approval until the run has copied its dependencies, so a run
	// starts with the configuration it was approved against.
	configMu  sync.RWMutex
	store     *storage.Store
	artifacts *artifact.Store
	// runtimes holds one adapter per declared runtime, keyed by name. A save
	// replaces the map while runs and bindings read it, so runtimesMu guards
	// every access; the map itself is never edited after it is installed.
	runtimesMu sync.RWMutex
	runtimes   map[domain.Runtime]agentruntime.AgentRuntime
	// built is every adapter a configuration installed, the ones a save
	// replaced included, since a call that started under one may still be
	// running. Shutdown closes them all.
	built       []agentruntime.AgentRuntime
	claudeAuth  inkunaclaude.Runner
	github      inkunagithub.Runner
	http        *http.Client
	secretStore secrets.Store
	secrets     secrets.Resolver
	notifier    notify.Notifier
	bus         *a2a.Bus
	authMu      sync.RWMutex
	linearKey   string
	linearID    string
	linearGen   uint64
	// linearAccess is what Linear said the key of linearAccessGen may
	// write, kept so Connections asks once per key.
	linearAccess    *WriteAccessView
	linearAccessGen uint64
	// trackerMu guards lastTrackerWrites: the last write this process tried
	// on each tracker, which Connections shows when the credential's own
	// answer says nothing.
	trackerMu         sync.Mutex
	lastTrackerWrites map[string]domain.TrackerWrite
	runsMu            sync.RWMutex
	// reportMu lets one result comment be written at a time, so the ends of
	// two rounds of a run never both make its one comment.
	reportMu sync.Mutex
	// trackerApplyMu lets one batch of agents' tracker changes be applied
	// at a time, an approval's or a retry's, so they reach a tracker in the
	// order they were asked for.
	trackerApplyMu sync.Mutex
	// persistMu orders a run's saves as their copies of its view were
	// taken, so a save copied before the run ended never lands after the
	// one that stored the end.
	persistMu sync.Mutex
	// workspaceMu serializes sending a run back with removing a worktree.
	// RequestChanges holds it from checking the run's worktree until the run
	// is active again, and a removal holds it while it checks the run is
	// still finished and removes, so a round never starts on a worktree
	// being removed, and two sends for one run never both claim its
	// checkout.
	workspaceMu sync.Mutex
	// artifactMu is held for reading while a blob is stored and indexed, and
	// for writing while a delete checks that no task indexes a blob and
	// removes it, so content stored again in between keeps its blob.
	artifactMu sync.RWMutex
	runsWG     sync.WaitGroup
	runs       map[string]*liveExecution
	// starting are workflows whose approval is between its first check for
	// an active run and registering its run, so two approvals of one
	// workflow cannot both ask the policies. Guarded by runsMu.
	starting map[string]bool
	// checkouts maps a project checkout to the run writing in it when
	// worktrees are off; one run at a time may.
	checkouts map[string]string
	// syncAttempts holds, under syncMu, when each project last tried to
	// fetch from origin.
	syncAttempts map[string]time.Time
	// branchMu serializes choosing a run's branch name and creating it, so
	// two runs starting together cannot both take the same free name.
	branchMu sync.Mutex
	// syncMu serializes fetching, fast-forwarding, and deleting branches,
	// so none of them measures refs another is still moving.
	syncMu sync.Mutex
	// pullsMu lets one pull request check run at a time, and guards
	// pullChecks, when each project's pull requests were last read.
	pullsMu     sync.Mutex
	pullChecks  map[string]time.Time
	taskSlots   chan struct{}
	assignMu    sync.RWMutex
	assignments map[string]*workerAssignment
	emitMu      sync.RWMutex
	emitter     func(name string, data any)
	initErr     error
	// unlockData releases the data directory's lock, which this Inkuna
	// holds from opening the store until it quits.
	unlockData func()
	// liveChecks holds the logs of checks whose commands are running, so
	// the view can read them before they are stored.
	checksMu   sync.Mutex
	liveChecks map[string]*checkLog
}

type HealthCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Detail says what Go found and, when the check is not ready, how to
	// fix it.
	Detail   string `json:"detail"`
	Required bool   `json:"required"`
	// Action is the screen of the app that fixes the check, when one does:
	// HealthConnections or HealthSetup. A fix outside the app, such as an
	// install or a CLI's own sign-in, is in Detail instead.
	Action string `json:"action,omitempty"`
}

// The screens a health check's Action can name.
const (
	HealthConnections = "connections"
	HealthSetup       = "setup"
)

// connectedPrerequisites are the prerequisites whose sign-in the
// Connections screen makes.
var connectedPrerequisites = map[string]bool{"gh": true, "claude": true}

type Dashboard struct {
	Projects []ProjectView `json:"projects"`
	// RemovedProjects name the removed projects whose history is kept, for
	// the views that list that history; New request and Projects leave them
	// out.
	RemovedProjects []ProjectView `json:"removedProjects"`
	Health          []HealthCheck `json:"health"`
	Defaults        DefaultsView  `json:"defaults"`
	Setup           SetupView     `json:"setup"`
	PendingInputs   int           `json:"pendingInputs"`
	ActiveRuns      int           `json:"activeRuns"`
}

type SetupView struct {
	ConfigPath   string `json:"configPath"`
	ConfigExists bool   `json:"configExists"`
	DataDir      string `json:"dataDir"`
}

type DefaultsView struct {
	MaxConcurrency  int            `json:"maxConcurrency"`
	MaxTasks        int            `json:"maxTasks"`
	PlanningSeconds int            `json:"planningSeconds"`
	WorkerSeconds   int            `json:"workerSeconds"`
	VerifierSeconds int            `json:"verifierSeconds"`
	ApprovalSeconds int            `json:"approvalSeconds"`
	Orchestrator    domain.Runtime `json:"orchestrator"`
	RoutingPolicy   routing.Policy `json:"routingPolicy"`
}

type ProjectView struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Repository     string         `json:"repository"`
	DefaultRuntime domain.Runtime `json:"defaultRuntime"`
	CreatedAt      string         `json:"createdAt"`
	// RemovedAt is when the project was removed; empty while it is connected.
	RemovedAt string `json:"removedAt,omitempty"`
}

type SkillView struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	TaskKinds            []domain.TaskKind `json:"taskKinds"`
	RequiredCapabilities []string          `json:"requiredCapabilities"`
}

type LinearIssueView struct {
	ID            string          `json:"id"`
	Identifier    string          `json:"identifier"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	URL           string          `json:"url"`
	Priority      int             `json:"priority"`
	DueDate       string          `json:"dueDate,omitempty"`
	Team          string          `json:"team"`
	State         string          `json:"state"`
	Project       string          `json:"project,omitempty"`
	Parent        string          `json:"parent,omitempty"`
	Labels        []string        `json:"labels"`
	SuggestedKind domain.TaskKind `json:"suggestedKind"`
}

type ProviderAuthenticationView struct {
	Provider      string `json:"provider"`
	Authenticated bool   `json:"authenticated"`
	Account       string `json:"account,omitempty"`
	Detail        string `json:"detail"`
	// Write says whether the credential can write to the tracker, for
	// GitHub and Linear once authenticated.
	Write *WriteAccessView `json:"write,omitempty"`
}

type AuthenticationView struct {
	GitHub ProviderAuthenticationView `json:"github"`
	Claude ProviderAuthenticationView `json:"claude"`
	Linear ProviderAuthenticationView `json:"linear"`
}

// New builds the service for dataDir ("" discovers it) after registering
// extensions in order. The zero product is Inkuna's own; with it and no
// extensions the service is exactly Inkuna's.
func New(dataDir string, product extension.Product, extensions []extension.Extension) (*Service, error) {
	// Registering first refuses a broken build before anything on disk is
	// read, whatever the configuration says.
	registry, err := registerExtensions(product, extensions)
	if err != nil {
		return nil, fmt.Errorf("register extensions: %w", err)
	}
	configuration, err := config.Load()
	if err != nil {
		return nil, err
	}
	// An extension's section is wrong the way any other line of inkuna.yaml
	// can be, so it refuses startup the same way.
	if registry.unknownSections, err = configureExtensions(configuration.ConfigPath, extensions, configuration.File.Extensions); err != nil {
		return nil, err
	}
	discovered := dataDir == ""
	if discovered {
		dataDir = configuration.DataDir
	}
	configuration.DataDir = dataDir
	service := &Service{
		dataDir:     dataDir,
		discovered:  discovered,
		http:        &http.Client{Timeout: configuration.LinearTimeout},
		linearKey:   configuration.LinearAPIKey,
		runs:        make(map[string]*liveExecution),
		assignments: make(map[string]*workerAssignment),
		secretStore: registry.secretStoreOr(secrets.Keyring{}),
		extensions:  registry,
	}
	service.applyConfig(configuration)
	return service, nil
}

// applyConfig installs a configuration and rebuilds everything derived from
// it. Runs already in flight keep the runtimes they started with.
func (service *Service) applyConfig(configuration config.Config) {
	service.config = configuration
	runtimes := make(map[domain.Runtime]agentruntime.AgentRuntime, len(configuration.Runtimes))
	for _, spec := range configuration.Runtimes {
		spec.Instance = service.instance()
		// config.FromFile validated every declaration; one that still fails is
		// left out, and naming it reports an undeclared runtime.
		if adapter, err := agentruntime.New(spec); err == nil {
			runtimes[spec.Name] = adapter
		}
	}
	service.runtimesMu.Lock()
	service.runtimes = runtimes
	for _, adapter := range runtimes {
		service.built = append(service.built, adapter)
	}
	service.runtimesMu.Unlock()
	service.claudeAuth = inkunaclaude.CLI{Binary: configuration.Binaries.Claude}
	service.github = inkunagithub.CLI{Binary: configuration.Binaries.GitHub}
	if service.secretStore == nil {
		service.secretStore = secrets.NewMemory()
	}
	service.secrets = secrets.NewResolver(service.secretStore)
	// An extension's notifiers get every notification, whether or not the
	// desktop shows them.
	var own notify.Notifier = notify.Noop{}
	if configuration.DesktopNotifications {
		own = notify.Desktop{AppName: "Inkuna"}
	}
	service.notifier = service.extensions.notifierWith(own)
	slots := configuration.MaxTasks
	if slots <= 0 {
		slots = 1
	}
	// Replacing the semaphore while runs hold tokens would let old and new
	// runs exceed the limit together, so a new capacity only takes effect
	// once no run is active. A run paused for review holds none.
	service.runsMu.Lock()
	active := 0
	for _, run := range service.runs {
		if !runIdle(run.view.State) {
			active++
		}
	}
	if service.taskSlots == nil || (active == 0 && cap(service.taskSlots) != slots) {
		service.taskSlots = make(chan struct{}, slots)
	}
	service.runsMu.Unlock()
}

func (service *Service) Startup(ctx context.Context) {
	service.ctx = ctx
	if ctx != nil && ctx.Value("frontend") != nil {
		service.emitMu.Lock()
		service.emitter = func(name string, data any) { wailsruntime.EventsEmit(ctx, name, data) }
		service.emitMu.Unlock()
	}
	service.initErr = service.initialize()
	// Adopting may save inkuna.yaml, which takes configMu. It runs here, not
	// in initialize, because a save can be what opens the store, and it
	// holds configMu while it does.
	if service.initErr == nil {
		service.adoptProjects(service.context())
		service.refreshSources()
	}
}

func (service *Service) Shutdown(context.Context) {
	service.cancelAllRuns()
	service.closeRuntimes()
	service.runsWG.Wait()
	// After the runs and the changes still being audited, so what they
	// recorded last is queued, and before the store closes under the loops.
	service.auditWG.Wait()
	if service.sourcesCancel != nil {
		service.sourcesCancel()
	}
	service.sourcesWG.Wait()
	service.stopSinks()
	if service.store != nil {
		_ = service.store.Close()
	}
	if service.unlockData != nil {
		service.unlockData()
		service.unlockData = nil
	}
}

// closeRuntimes ends every CLI Inkuna started. A run's workers end with the
// run, but nothing else ends a call made outside one, such as planning, and
// the app would quit with that model still running and billing.
func (service *Service) closeRuntimes() {
	service.runtimesMu.RLock()
	built := slices.Clone(service.built)
	service.runtimesMu.RUnlock()
	var closing sync.WaitGroup
	for _, adapter := range built {
		if closer, ok := adapter.(agentruntime.Closer); ok {
			closing.Go(closer.Close)
		}
	}
	closing.Wait()
}

func (service *Service) Dashboard() (Dashboard, error) {
	if err := service.ready(); err != nil {
		return Dashboard{}, err
	}
	ctx := service.context()
	projects, err := service.store.AllProjects(ctx)
	if err != nil {
		return Dashboard{}, err
	}
	views, removed := make([]ProjectView, 0, len(projects)), make([]ProjectView, 0)
	for _, project := range projects {
		if project.Removed() {
			removed = append(removed, projectView(project))
		} else {
			views = append(views, projectView(project))
		}
	}
	pending, err := service.store.Inputs(ctx, storage.InputPending, 0)
	if err != nil {
		return Dashboard{}, err
	}
	service.runsMu.RLock()
	active := 0
	for _, run := range service.runs {
		if !terminalRunState(run.view.State) {
			active++
		}
	}
	service.runsMu.RUnlock()
	return Dashboard{Projects: views, RemovedProjects: removed, Health: service.Doctor(), Defaults: DefaultsView{
		MaxConcurrency:  service.config.MaxConcurrency,
		MaxTasks:        service.config.MaxTasks,
		PlanningSeconds: int(service.config.PlanningTimeout / time.Second),
		WorkerSeconds:   int(service.config.WorkerTimeout / time.Second),
		VerifierSeconds: int(service.config.VerifierTimeout / time.Second),
		ApprovalSeconds: int(service.config.ApprovalTimeout / time.Second),
		Orchestrator:    service.config.Orchestrator.Runtime,
		RoutingPolicy:   service.routingPolicy(),
	}, Setup: SetupView{ConfigPath: service.config.ConfigPath, ConfigExists: service.config.ConfigExists, DataDir: service.dataDir},
		PendingInputs: len(pending), ActiveRuns: active}, nil
}

// routingPolicy is the configured policy; a configuration that was not built
// by config.FromFile carries none and gets the built-in one.
func (service *Service) routingPolicy() routing.Policy {
	if service.config.RoutingPolicy.Version == 0 {
		return routing.DefaultPolicy()
	}
	return service.config.RoutingPolicy
}

// Doctor reports Inkuna's build, its configuration, and every dependency.
// The prerequisites come from internal/prereq, the checks `inkunactl
// install` runs, so the app, doctor, and install agree on what is missing
// and how to install it.
func (service *Service) Doctor() []HealthCheck {
	result := make([]HealthCheck, 0, 24)
	// Not a dependency, but doctor's output is what a user pastes into a bug
	// report, so it says which build they are running.
	result = append(result, HealthCheck{Name: "Inkuna", Status: "ready", Detail: version.Current().String(), Required: false})
	configStatus, configDetail := "missing", "run the setup wizard to create "+service.config.ConfigPath
	if service.config.ConfigExists {
		configStatus, configDetail = "ready", service.config.ConfigPath
	}
	configuration := HealthCheck{Name: "Configuration", Status: configStatus, Detail: configDetail, Required: false}
	if !service.config.ConfigExists {
		configuration.Action = HealthSetup
	}
	result = append(result, configuration)
	report := prereq.Check(service.context(), prereq.System{}, prereq.OptionsFor(service.config))
	for _, item := range report.Items {
		result = append(result, prerequisiteHealth(item))
	}
	// Declared runtimes are optional: a task only needs the ones its steps
	// use. The built-in CLIs were checked as prerequisites above, with any
	// versionArgs they declare.
	for _, spec := range service.config.Runtimes {
		if !slices.Contains(config.BuiltInRuntimes(), spec.Name) {
			result = append(result, service.runtimeHealth(spec))
		}
	}
	orchestratorStatus := "ready"
	orchestratorDetail := "orchestrator runs on " + string(service.config.Orchestrator.Runtime)
	if service.config.Orchestrator.Disabled {
		orchestratorStatus, orchestratorDetail = "missing", "orchestrator disabled; deterministic staffing is used"
	} else if _, err := service.runtimeFor(service.config.Orchestrator.Runtime); err != nil {
		orchestratorStatus, orchestratorDetail = "error", err.Error()
	} else if _, lookErr := exec.LookPath(service.binaryFor(service.config.Orchestrator.Runtime)); lookErr != nil {
		orchestratorStatus, orchestratorDetail = "error", string(service.config.Orchestrator.Runtime)+" binary is not installed; planning falls back to deterministic staffing"
	}
	result = append(result, HealthCheck{Name: "Orchestrator", Status: orchestratorStatus, Detail: orchestratorDetail, Required: false})
	linear := HealthCheck{Name: "Linear", Status: "ready", Detail: "Linear credential is available; access is verified when an issue is imported", Required: false}
	if service.linearCredential() == "" {
		linear.Status, linear.Action = "missing", HealthConnections
		linear.Detail = "connect Linear, or store " + config.EnvLinearAPIKey + " in the keychain or environment, to import Linear issues"
	}
	result = append(result, linear)
	for _, status := range service.secrets.Statuses(service.config.SecretNames()) {
		check := HealthCheck{Name: "Secret " + status.Name, Status: "ready", Detail: "available from " + string(status.Source), Required: false}
		if !status.Available {
			check.Status, check.Detail, check.Action = "missing", "not set; add it in Setup", HealthSetup
			if status.Detail != "" {
				check.Status, check.Detail = "error", status.Detail
			}
		}
		result = append(result, check)
	}
	result = append(result, service.extensionHealth()...)
	return append(result, service.sinkHealth()...)
}

// binaryOverrides say where each prerequisite's program can be pointed
// elsewhere, for a missing one installed outside PATH.
var binaryOverrides = map[string]string{
	"git":     "the " + config.EnvGitBinary + " environment variable",
	"go":      "the " + config.EnvGoBinary + " environment variable",
	"node":    "the " + config.EnvNodeBinary + " environment variable",
	"npm":     "the " + config.EnvNPMBinary + " environment variable",
	"gh":      "github.binary in inkuna.yaml",
	"codex":   "runtimes.codex.binary in inkuna.yaml",
	"claude":  "runtimes.claude.binary in inkuna.yaml",
	"copilot": "runtimes.copilot.binary in inkuna.yaml",
}

// prerequisiteHealth shows one prerequisite as a health row. Signed out
// reads as missing, like a missing credential; too old or unreadable is an
// error.
func prerequisiteHealth(item prereq.Item) HealthCheck {
	check := HealthCheck{Name: item.Name, Status: "error", Detail: item.Detail, Required: item.Required}
	switch item.Status {
	case prereq.Present:
		check.Status = "ready"
	case prereq.Missing, prereq.SignedOut:
		check.Status = "missing"
	}
	if remedy := item.Remedy(); remedy != "" {
		check.Detail += "; " + remedy
	}
	if override, ok := binaryOverrides[item.ID]; ok && item.Status == prereq.Missing {
		check.Detail += "; or set its path with " + override
	}
	// Connections signs in to these; another CLI signs in in a terminal,
	// as its detail says.
	if item.Status == prereq.SignedOut && connectedPrerequisites[item.ID] {
		check.Action = HealthConnections
	}
	return check
}

// runtimeHealth checks a declared runtime's CLI with its version command.
func (service *Service) runtimeHealth(spec agentruntime.Spec) HealthCheck {
	check := HealthCheck{Name: runtimeLabel(spec), Status: "missing", Required: false}
	binary := service.binaryFor(spec.Name)
	path, err := exec.LookPath(binary)
	if err != nil {
		check.Detail = binary + " was not found in PATH; install it or set its absolute path with runtimes." + string(spec.Name) + ".binary in inkuna.yaml"
		return check
	}
	versionArgs := spec.VersionArgs
	if len(versionArgs) == 0 {
		versionArgs = []string{"--version"}
	}
	ctx, cancel := context.WithTimeout(service.context(), service.config.HealthTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, versionArgs...).CombinedOutput()
	check.Status, check.Detail = "ready", string(output)
	if err != nil {
		check.Status = "error"
	}
	return check
}

// binaryFor is the executable a declared runtime starts. A preset without its
// own binary runs the built-in CLI of that name.
func (service *Service) binaryFor(name domain.Runtime) string {
	for _, spec := range service.config.Runtimes {
		if spec.Name != name {
			continue
		}
		if spec.Binary != "" {
			return spec.Binary
		}
		if spec.Preset != "" {
			return string(spec.Preset)
		}
	}
	return string(name)
}

var builtInRuntimeLabels = map[domain.Runtime]string{
	domain.RuntimeCodex: "Codex", domain.RuntimeClaude: "Claude Code", domain.RuntimeCopilot: "GitHub Copilot",
}

// runtimeLabel is the name the app shows for a runtime.
func runtimeLabel(spec agentruntime.Spec) string {
	if strings.TrimSpace(spec.Label) != "" {
		return strings.TrimSpace(spec.Label)
	}
	if label, builtIn := builtInRuntimeLabels[spec.Name]; builtIn {
		return label
	}
	return string(spec.Name)
}

func (service *Service) Skills(projectRoot string) ([]SkillView, error) {
	packages, err := skill.Discover(filepath.Join(projectRoot, ".inkuna", "skills"))
	if err != nil {
		return nil, err
	}
	views := make([]SkillView, 0, len(packages))
	for _, current := range packages {
		views = append(views, SkillView{ID: current.ID, Name: current.Name, Description: current.Description, TaskKinds: current.Manifest.TaskKinds, RequiredCapabilities: current.Manifest.RequiredCapabilities})
	}
	return views, nil
}

func (service *Service) ImportLinearIssue(reference string) (LinearIssueView, error) {
	client, err := service.linearClient(service.linearCredential())
	if err != nil {
		return LinearIssueView{}, err
	}
	ctx, cancel := context.WithTimeout(service.context(), service.config.LinearTimeout)
	defer cancel()
	issue, err := client.Issue(ctx, reference)
	if err != nil {
		return LinearIssueView{}, err
	}
	view := LinearIssueView{ID: issue.ID, Identifier: issue.Identifier, Title: issue.Title, Description: issue.Description, URL: issue.URL, Priority: issue.Priority, DueDate: issue.DueDate, Team: issue.Team.Name, State: issue.State.Name, Labels: issue.Labels, SuggestedKind: suggestedKind(issue.Labels)}
	if issue.Project != nil {
		view.Project = issue.Project.Name
	}
	if issue.Parent != nil {
		view.Parent = issue.Parent.Identifier + ": " + issue.Parent.Title
	}
	return view, nil
}

func (service *Service) AuthenticationStatus() AuthenticationView {
	return AuthenticationView{
		GitHub: service.githubAuthenticationStatus(),
		Claude: service.claudeAuthenticationStatus(),
		Linear: service.linearAuthenticationStatus(),
	}
}

func (service *Service) AuthenticateClaude() (ProviderAuthenticationView, error) {
	ctx, cancel := context.WithTimeout(service.context(), service.config.AuthTimeout)
	defer cancel()
	status, err := inkunaclaude.Authenticate(ctx, service.claudeAuth)
	if err != nil {
		return ProviderAuthenticationView{}, err
	}
	return claudeAuthenticationView(status, nil), nil
}

func (service *Service) AuthenticateGitHub() (ProviderAuthenticationView, error) {
	ctx, cancel := context.WithTimeout(service.context(), service.config.AuthTimeout)
	defer cancel()
	status, err := inkunagithub.Authenticate(ctx, service.github, service.config.GitHubHost, service.config.GitHubProtocol)
	if err != nil {
		return ProviderAuthenticationView{}, err
	}
	view := githubAuthenticationView(status, nil)
	view.Write = service.githubWriteAccess(status)
	return view, nil
}

func (service *Service) AuthenticateLinear(apiKey string) (ProviderAuthenticationView, error) {
	key := strings.TrimSpace(apiKey)
	client, err := service.linearClient(key)
	if err != nil {
		return ProviderAuthenticationView{}, err
	}
	service.authMu.Lock()
	service.linearGen++
	generation := service.linearGen
	service.authMu.Unlock()
	ctx, cancel := context.WithTimeout(service.context(), service.config.LinearTimeout)
	defer cancel()
	viewer, err := client.Viewer(ctx)
	if err != nil {
		return ProviderAuthenticationView{}, fmt.Errorf("authenticate Linear: %w", err)
	}
	account := strings.TrimSpace(viewer.Name)
	if account == "" {
		account = strings.TrimSpace(viewer.Email)
	}
	service.authMu.Lock()
	if service.linearGen != generation {
		service.authMu.Unlock()
		return ProviderAuthenticationView{}, errors.New("linear authentication request was superseded")
	}
	service.linearKey = key
	service.linearID = account
	service.authMu.Unlock()
	return ProviderAuthenticationView{Provider: "linear", Authenticated: true, Account: account, Detail: "Authenticated for this Inkuna session", Write: service.linearWriteAccess(client, generation)}, nil
}

func (service *Service) ForgetLinearAuthentication() ProviderAuthenticationView {
	service.authMu.Lock()
	service.linearGen++
	service.linearKey = ""
	service.linearID = ""
	service.linearAccess = nil
	service.authMu.Unlock()
	return ProviderAuthenticationView{Provider: "linear", Detail: "Not authenticated"}
}

func (service *Service) githubAuthenticationStatus() ProviderAuthenticationView {
	ctx, cancel := context.WithTimeout(service.context(), service.config.HealthTimeout)
	defer cancel()
	status, err := inkunagithub.AuthenticationStatus(ctx, service.github, service.config.GitHubHost)
	view := githubAuthenticationView(status, err)
	if err == nil {
		view.Write = service.githubWriteAccess(status)
	}
	return view
}

func (service *Service) claudeAuthenticationStatus() ProviderAuthenticationView {
	ctx, cancel := context.WithTimeout(service.context(), service.config.HealthTimeout)
	defer cancel()
	status, err := inkunaclaude.AuthenticationStatus(ctx, service.claudeAuth)
	return claudeAuthenticationView(status, err)
}

func claudeAuthenticationView(status inkunaclaude.AuthStatus, err error) ProviderAuthenticationView {
	view := ProviderAuthenticationView{Provider: "claude", Authenticated: status.Authenticated}
	if err != nil {
		view.Detail = "Claude Code authentication is unavailable: " + err.Error()
	} else if status.Authenticated {
		view.Detail = "Authenticated through Claude Code"
		if status.Method != "" {
			view.Detail += " via " + status.Method
		}
	} else {
		view.Detail = "Not authenticated through Claude Code"
	}
	return view
}

func githubAuthenticationView(status inkunagithub.AuthStatus, err error) ProviderAuthenticationView {
	view := ProviderAuthenticationView{Provider: "github", Authenticated: status.Authenticated, Account: status.Account}
	if err != nil {
		view.Detail = "GitHub authentication is unavailable: " + err.Error()
	} else if status.Authenticated {
		view.Detail = "Authenticated through gh for " + status.Host
	} else {
		view.Detail = "Not authenticated through gh"
	}
	return view
}

func (service *Service) linearAuthenticationStatus() ProviderAuthenticationView {
	key, identity, generation := service.linearCredentials()
	if key == "" {
		return ProviderAuthenticationView{Provider: "linear", Detail: "Not authenticated"}
	}
	client, err := service.linearClient(key)
	if err != nil {
		return ProviderAuthenticationView{Provider: "linear", Detail: err.Error()}
	}
	if identity != "" {
		return ProviderAuthenticationView{Provider: "linear", Authenticated: true, Account: identity, Detail: "Authenticated for this Inkuna session", Write: service.linearWriteAccess(client, generation)}
	}
	ctx, cancel := context.WithTimeout(service.context(), service.config.LinearTimeout)
	defer cancel()
	viewer, err := client.Viewer(ctx)
	if err != nil {
		return ProviderAuthenticationView{Provider: "linear", Detail: "Linear authentication failed: " + err.Error()}
	}
	account := strings.TrimSpace(viewer.Name)
	if account == "" {
		account = strings.TrimSpace(viewer.Email)
	}
	service.authMu.Lock()
	if service.linearKey == key && service.linearGen == generation {
		service.linearID = account
	}
	service.authMu.Unlock()
	return ProviderAuthenticationView{Provider: "linear", Authenticated: true, Account: account, Detail: "Authenticated from process environment", Write: service.linearWriteAccess(client, generation)}
}

func (service *Service) linearClient(key string) (*linear.Client, error) {
	return linear.NewWithEndpoint(key, service.config.LinearEndpoint, service.http)
}

// linearCredential prefers a session key, then the environment, then the
// keychain entry declared in inkuna.yaml.
func (service *Service) linearCredential() string {
	key, _, _ := service.linearCredentials()
	if key != "" {
		return key
	}
	if service.secrets.Store == nil && service.secrets.Environ == nil {
		return ""
	}
	value, _, err := service.secrets.Resolve(config.EnvLinearAPIKey)
	if err != nil {
		return ""
	}
	return value
}

func (service *Service) linearCredentials() (string, string, uint64) {
	service.authMu.RLock()
	defer service.authMu.RUnlock()
	return service.linearKey, service.linearID, service.linearGen
}

func (service *Service) ValidateWorkflow(workflowJSON string) error {
	var workflow domain.WorkflowSpec
	if err := json.Unmarshal([]byte(workflowJSON), &workflow); err != nil {
		return err
	}
	return domain.ValidateWorkflow(workflow)
}

func (service *Service) initialize() error {
	if service.store != nil {
		if err := service.startSinks(); err != nil {
			return fmt.Errorf("start the sinks: %w", err)
		}
		service.ensureBus()
		return nil
	}
	// Before the store opens: opening it marks every live run interrupted,
	// and another Inkuna on this data directory is still running its own.
	unlock, err := lockDataDir(service.dataDir)
	if err != nil {
		return err
	}
	// Holding the lock, and before a run is marked interrupted and can be
	// resumed beside them: what carries this directory's mark now was left
	// running by an Inkuna that crashed.
	if ended, err := proc.EndLeftOver(service.instance(), leftOverGrace); err != nil {
		log.Printf("inkuna: look for workers a crash left running: %v", err)
	} else if ended > 0 {
		log.Printf("inkuna: ended %d worker(s) a crash left running", ended)
	}
	store, err := storage.Open(filepath.Join(service.dataDir, "inkuna.db"))
	if err != nil {
		unlock()
		return err
	}
	artifacts, err := artifact.New(filepath.Join(service.dataDir, "artifacts"))
	if err != nil {
		_ = store.Close()
		unlock()
		return err
	}
	service.store = store
	service.artifacts = artifacts
	if err := store.MarkInterruptedExecutions(context.Background()); err != nil {
		_ = store.Close()
		service.store = nil
		unlock()
		return fmt.Errorf("reconcile interrupted runs: %w", err)
	}
	// Before the bus and any run, so nothing is recorded before the outbox
	// is on.
	if err := service.startSinks(); err != nil {
		_ = store.Close()
		service.store = nil
		unlock()
		return fmt.Errorf("start the sinks: %w", err)
	}
	service.unlockData = unlock
	service.ensureBus()
	return nil
}

// leftOverGrace is how long a worker a crash left running has to stop when
// asked, at the next startup, before it is killed.
const leftOverGrace = 2 * time.Second

// DataDirLockFile is the file in the data directory whose lock the Inkuna
// using that directory holds.
const DataDirLockFile = "inkuna.lock"

// ErrDataDirInUse is the startup error while another Inkuna holds the data
// directory's lock.
var ErrDataDirInUse = errors.New("another Inkuna is using this data directory")

// InstanceID names the Inkuna using service's data directory, for the
// desktop's single-instance lock: a second launch on the same directory
// brings the first one's window forward instead, while Inkunas on different
// directories run side by side. It is a function rather than a method, so
// Wails does not bind it.
func InstanceID(service *Service) string {
	dir := service.dataDir
	if absolute, err := filepath.Abs(dir); err == nil {
		dir = absolute
	}
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return "inkuna-" + hex.EncodeToString(sum[:8])
}

// instance is the ID every worker and check this Inkuna starts carries in
// its environment (proc.Mark). A service without a data directory, as some
// tests build, marks nothing: it holds no lock, so the mark would prove
// nothing about who started a process.
func (service *Service) instance() string {
	if service.dataDir == "" {
		return ""
	}
	return InstanceID(service)
}

// lockDataDir takes the data directory's lock, so only one Inkuna at a time
// opens its store. The second would mark the first one's live runs
// interrupted and cancel the questions waiting in its inbox, with the first
// one's workers still running.
func lockDataDir(dataDir string) (func(), error) {
	unlock, err := filelock.Lock(filepath.Join(dataDir, DataDirLockFile))
	if errors.Is(err, filelock.ErrLocked) {
		return nil, fmt.Errorf("%w (%s): quit it first, or point this one at another directory with %s", ErrDataDirInUse, dataDir, config.EnvDataDir)
	}
	if err != nil {
		return nil, fmt.Errorf("lock the data directory: %w", err)
	}
	return unlock, nil
}

// ensureBus creates the A2A bus on first use and registers the human agent.
func (service *Service) ensureBus() {
	service.runsMu.Lock()
	defer service.runsMu.Unlock()
	if service.bus != nil {
		return
	}
	options := []a2a.Option{a2a.WithErrorHandler(func(err error) { log.Printf("inkuna a2a: %v", err) }), a2a.WithObserver(service.notifyThread), a2a.WithObserver(service.auditTask)}
	if service.store != nil {
		options = append(options, a2a.WithPersister(service.store))
	}
	service.bus = a2a.NewBus(options...)
	_ = service.bus.RegisterAgent(agent.HumanCard(), service.serveHumanTask)
	if service.assignments == nil {
		service.assignments = make(map[string]*workerAssignment)
	}
	if service.taskSlots == nil {
		service.taskSlots = make(chan struct{}, 1)
	}
	if service.notifier == nil {
		service.notifier = notify.Noop{}
	}
	if service.secrets.Environ == nil {
		if service.secretStore == nil {
			service.secretStore = secrets.NewMemory()
		}
		service.secrets = secrets.NewResolver(service.secretStore)
	}
}

func (service *Service) ready() error {
	if service.initErr != nil {
		return service.initErr
	}
	if service.store == nil {
		service.initErr = service.initialize()
	}
	return service.initErr
}

func (service *Service) context() context.Context {
	if service.ctx != nil {
		return service.ctx
	}
	return context.Background()
}

// emit pushes a change notification to the desktop view when Wails is
// running; tests and inkunactl have no frontend and skip it.
func (service *Service) emit(name string, data any) {
	service.emitMu.RLock()
	emitter := service.emitter
	service.emitMu.RUnlock()
	if emitter != nil {
		emitter(name, data)
	}
}

// newID is prefix and the time, which never repeats in a process (clock.Now),
// so IDs made in a row stay unique and in order.
func newID(prefix string) string { return fmt.Sprintf("%s-%d", prefix, clock.Now().UnixNano()) }

func projectView(project storage.Project) ProjectView {
	view := ProjectView{ID: project.ID, Name: project.Name, Repository: project.Repository, DefaultRuntime: project.DefaultRuntime, CreatedAt: project.CreatedAt.Format(time.RFC3339Nano)}
	if project.Removed() {
		view.RemovedAt = project.RemovedAt.Format(time.RFC3339Nano)
	}
	return view
}

func suggestedKind(labels []string) domain.TaskKind {
	for _, label := range labels {
		switch strings.ToLower(label) {
		case "bug", "defect":
			return domain.TaskBugfix
		case "documentation", "docs":
			return domain.TaskDocumentation
		case "research", "spike":
			return domain.TaskResearch
		}
	}
	return domain.TaskImplementation
}
