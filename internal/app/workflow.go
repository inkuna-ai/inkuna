package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/notify"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	"github.com/inkuna-ai/inkuna/internal/planner"
	"github.com/inkuna-ai/inkuna/internal/repository"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/internal/skill"
	"github.com/inkuna-ai/inkuna/internal/storage"
	workflowengine "github.com/inkuna-ai/inkuna/internal/workflow"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// WorkflowRequestView is a task as the composer sends it. Title names the
// task; left blank, planning writes one from Objective, the task's details.
type WorkflowRequestView struct {
	ProjectID          string          `json:"projectId"`
	Title              string          `json:"title,omitempty"`
	Objective          string          `json:"objective"`
	AcceptanceCriteria []string        `json:"acceptanceCriteria"`
	Constraints        []string        `json:"constraints"`
	Labels             []string        `json:"labels,omitempty"`
	Kind               domain.TaskKind `json:"kind"`
	SourceKind         string          `json:"sourceKind"`
	SourceReference    string          `json:"sourceReference,omitempty"`
	// RuleName, when set, bypasses rule matching: the named rule (or
	// "default") shapes the workflow.
	RuleName string `json:"ruleName,omitempty"`
	// Restaff asks the orchestrator to staff the auto stages even when an
	// identical plan's decision is kept, and keeps the new one instead.
	Restaff bool `json:"restaff,omitempty"`
}

// AgentCandidateView is an agent the approval screen may put on a step. It
// carries what the picker shows, never the agent's instructions.
type AgentCandidateView struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Permission  domain.Permission `json:"permission"`
	Runtime     domain.Runtime    `json:"runtime,omitempty"`
	Model       string            `json:"model,omitempty"`
	Source      agent.Source      `json:"source"`
	TaskKinds   []domain.TaskKind `json:"taskKinds,omitempty"`
}

type WorkflowPlanView struct {
	Project      ProjectView           `json:"project"`
	Task         domain.TaskSpec       `json:"task"`
	Workflow     domain.WorkflowSpec   `json:"workflow"`
	RuleName     string                `json:"ruleName"`
	Orchestrated bool                  `json:"orchestrated"`
	Decision     orchestrator.Decision `json:"decision"`
	Notes        []string              `json:"notes,omitempty"`
	Events       []domain.RunEvent     `json:"events,omitempty"`
	// Candidates are the agents the approval screen may assign to a step.
	Candidates []AgentCandidateView `json:"candidates,omitempty"`
	// Usage is what each orchestrator call made while planning consumed:
	// staffing, and proposing branching configuration when it was missing.
	Usage []domain.UsageRecord `json:"usage"`
	// StaffingReused is when the decision that staffed this plan was made,
	// for an identical earlier plan, in RFC 3339; empty when the
	// orchestrator was asked, or not used.
	StaffingReused string `json:"staffingReused,omitempty"`
	// MCPServers are the MCP servers inkuna.yaml declares, without their
	// secrets, so the approval screen can say what each step's grant is.
	MCPServers []domain.MCPServer `json:"mcpServers,omitempty"`
}

type StepRunView struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Kind       domain.StepKind `json:"kind"`
	Agent      string          `json:"agent,omitempty"`
	Runtime    domain.Runtime  `json:"runtime,omitempty"`
	Model      string          `json:"model,omitempty"`
	State      string          `json:"state"`
	A2ATaskID  string          `json:"a2aTaskId,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	StartedAt  string          `json:"startedAt,omitempty"`
	FinishedAt string          `json:"finishedAt,omitempty"`
	// Round is the round of the run shown: the run's current round, or an
	// earlier one whose result a later round kept.
	Round int `json:"round,omitempty"`
	// From is the shared stage or group the step was planned from, as the
	// approved plan recorded it.
	From *domain.StepOrigin `json:"from,omitempty"`
}

type CommitView struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"shortHash"`
	Author    string `json:"author"`
	Date      string `json:"date"`
	Subject   string `json:"subject"`
	Body      string `json:"body,omitempty"`
	Stat      string `json:"stat"`
	StepID    string `json:"stepId,omitempty"`
}

type WorkflowRunView struct {
	ID           string         `json:"id"`
	WorkflowID   string         `json:"workflowId"`
	TaskID       string         `json:"taskId"`
	ProjectID    string         `json:"projectId"`
	State        string         `json:"state"`
	Title        string         `json:"title"`
	Runtime      domain.Runtime `json:"runtime,omitempty"`
	Model        string         `json:"model,omitempty"`
	Agent        string         `json:"agent,omitempty"`
	RuleName     string         `json:"ruleName,omitempty"`
	Orchestrated bool           `json:"orchestrated"`
	Worktree     string         `json:"worktree,omitempty"`
	Branch       string         `json:"branch,omitempty"`
	// BaseCommit is the commit the run's workspace started from, which its
	// changes are measured against.
	BaseCommit string `json:"baseCommit,omitempty"`
	// Cleanup is the worktree cleanup policy the run follows (domain.Cleanup*).
	Cleanup string `json:"cleanup,omitempty"`
	// WorktreeRemovedAt is set once Inkuna removed the run's worktree; the
	// branch and its commits remain.
	WorktreeRemovedAt string `json:"worktreeRemovedAt,omitempty"`
	// WorkspaceNotes say what setup and cleanup did, such as a worktree kept
	// because it had uncommitted changes.
	WorkspaceNotes []string          `json:"workspaceNotes,omitempty"`
	StartedAt      string            `json:"startedAt"`
	FinishedAt     string            `json:"finishedAt,omitempty"`
	Error          string            `json:"error,omitempty"`
	Steps          []StepRunView     `json:"steps"`
	LastCommit     *CommitView       `json:"lastCommit,omitempty"`
	PendingInputs  int               `json:"pendingInputs"`
	Live           bool              `json:"live"`
	Events         []domain.RunEvent `json:"events"`
	// TaskCost is what every call made for the run's task cost so far,
	// across all its runs and its planning; absent when none is recorded.
	TaskCost *domain.CallTotals `json:"taskCost,omitempty"`
	// Checks counts the run's evaluation commands by how they ended.
	Checks *CheckTotals `json:"checks,omitempty"`
	// Round is the run's current feedback round, from 1.
	Round int `json:"round"`
	// Story is the story the run is for in its repository's tracker; only
	// a run's detail carries it.
	Story *RunStoryView `json:"story,omitempty"`
	// TrackerChanges are the changes the run's agents asked of its tracker,
	// in the order they asked; only a run's detail carries them.
	TrackerChanges []TrackerChangeView `json:"trackerChanges,omitempty"`
	// DeleteRefused says why the run's task cannot be deleted now, as
	// DeleteTasks would refuse it, or is empty when it can; only the runs
	// list carries it.
	DeleteRefused string `json:"deleteRefused,omitempty"`
	// PullRequest is the pull request the run's branch was opened in, as
	// the last check found it; absent when none was found.
	PullRequest *PullRequestView `json:"pullRequest,omitempty"`
	// RerunOf is the run this one started over from, when it is a rerun.
	RerunOf string `json:"rerunOf,omitempty"`
}

type liveExecution struct {
	view       WorkflowRunView
	started    time.Time
	ctx        context.Context
	cancel     context.CancelFunc
	active     map[string]agentruntime.AgentRuntime
	worktreeMu sync.Mutex
	registry   agent.Registry
	skills     map[string]string
	results    map[string]domain.HandoffResult
	contexts   map[string]domain.ContextEnvelope
	// skillBriefs holds each skill's description and delivery, for skills
	// sent by reference.
	skillBriefs map[string]skillBrief
	// outbound holds, per step, exactly what was sent to a remote advisor.
	outbound map[string]string
	// calls holds, by call id, the worker and peer calls that have started
	// and whose usage is not stored yet.
	calls     map[string]domain.UsageRecord
	sequences map[string]int64
	// writeMu serializes writable steps so each commit contains exactly one
	// step's changes and two agents never edit the shared worktree at once.
	writeMu  sync.Mutex
	a2aTasks []string
	project  storage.Project
	task     domain.TaskSpec
	spec     domain.WorkflowSpec
	// docs holds, under runsMu, the doc index of each directory the run's
	// steps work in: the checkout's, taken when the run starts, and the
	// worktree's, taken when it is created from a commit. A step gets its own
	// directory's index, so it is never told about a doc it cannot open.
	docs map[string]runDocs
	// deps is captured when the run starts so a configuration reload cannot
	// swap runtimes, secrets, or the notifier underneath an active run.
	deps runDependencies
	// round is the feedback round running, from 1, and feedback what this
	// round was sent back with, by target step.
	round    int
	feedback map[string][]storage.FeedbackItem
	// resumed is, in a round a person started by resuming the run, what
	// each step that stopped in the round before is told of that attempt.
	resumed map[string]*domain.ResumeNote
	// outChecked is set, under runsMu, once the run has checked whether the
	// repository ignores .inkuna/out, so it warns at most once.
	outChecked bool
	// story is the run's story and the tracker settings it was approved
	// under, nil without one; its Started is read and set under runsMu.
	story *runStory
	// done closes once the round's goroutine has returned, its state stored
	// and its worktree cleaned up, so a next round never overlaps the end
	// of this one.
	done chan struct{}
}

// stepRunID names a step's run in a round. Round 1 keeps <run>-<step>; a
// later round is <run>.r<N>.<step>. A stage id can be any text, so no suffix
// on <run>-<step> would be safe, but a run id holds no dot, so no later
// round's id can equal a round-1 id (docs/feedback-loop.md).
func stepRunID(executionID string, round int, stepID string) string {
	if round <= 1 {
		return executionID + "-" + stepID
	}
	return fmt.Sprintf("%s.r%d.%s", executionID, round, stepID)
}

// liveStepRunID is a step's run in its run's current round.
func (service *Service) liveStepRunID(executionID, stepID string) string {
	service.runsMu.RLock()
	defer service.runsMu.RUnlock()
	round := 1
	if run := service.runs[executionID]; run != nil {
		round = run.round
	}
	return stepRunID(executionID, round, stepID)
}

// runDocs is one working directory's doc index, or why it could not be
// read. The index only informs workers, so a run whose docs cannot be read
// goes ahead without it and each agent step says why.
type runDocs struct {
	briefs      []domain.DocBrief
	unavailable string
}

func (service *Service) runDocsIn(project storage.Project, workingDirectory string) runDocs {
	briefs, err := service.docBriefs(project, workingDirectory)
	if err != nil {
		return runDocs{unavailable: "This step runs without the doc index: " + err.Error()}
	}
	return runDocs{briefs: briefs}
}

// runDependencies is the configuration-derived state a run relies on.
type runDependencies struct {
	runtimes       map[domain.Runtime]agentruntime.AgentRuntime
	notifier       notify.Notifier
	secrets        secrets.Resolver
	secretNames    []string
	runtimeSecrets map[domain.Runtime][]string
	// remoteAgents are the usable remote advisors with the pins they had when
	// the run started.
	remoteAgents map[string]config.RemoteAgentConfig
	budgets      domain.Budgets
	// checkLogBytes caps each evaluation command's kept log.
	checkLogBytes int
	// mcpServers are the MCP servers inkuna.yaml declared when the run
	// started, by name.
	mcpServers map[string]config.MCPServerConfig
}

// snapshotDependencies copies the current configuration-derived state.
func (service *Service) snapshotDependencies() runDependencies {
	deps := runDependencies{
		runtimes:       service.declaredAdapters(),
		notifier:       service.notifier,
		secrets:        service.secrets,
		secretNames:    append([]string(nil), service.config.SecretNames()...),
		runtimeSecrets: make(map[domain.Runtime][]string, len(service.config.RuntimeSecrets)),
		remoteAgents:   remoteAgentsByName(service.config.File),
		budgets:        service.config.File.Budgets.WithDefaults(),
		checkLogBytes:  service.config.CheckLogBytes,
		mcpServers:     make(map[string]config.MCPServerConfig, len(service.config.File.MCPServers)),
	}
	for _, entry := range service.config.File.MCPServers {
		deps.mcpServers[entry.Name] = entry
	}
	if deps.checkLogBytes <= 0 {
		deps.checkLogBytes = config.DefaultCheckLogBytes
	}
	for runtimeName, names := range service.config.RuntimeSecrets {
		deps.runtimeSecrets[runtimeName] = append([]string(nil), names...)
	}
	if deps.notifier == nil {
		deps.notifier = notify.Noop{}
	}
	return deps
}

// dependenciesFor returns the snapshot for a run, or the live configuration
// when the run is unknown.
func (service *Service) dependenciesFor(executionID string) runDependencies {
	service.runsMu.RLock()
	run := service.runs[executionID]
	service.runsMu.RUnlock()
	if run != nil && run.deps.runtimes != nil {
		return run.deps
	}
	return service.snapshotDependencies()
}

func (deps runDependencies) runtime(name domain.Runtime) (agentruntime.AgentRuntime, error) {
	current, ok := deps.runtimes[name]
	if !ok || current == nil {
		return nil, fmt.Errorf("unsupported runtime %q", name)
	}
	return current, nil
}

const (
	liveRunEventLimit        = 1000
	retainedTerminalRunLimit = 50
	planningEventLimit       = 200
)

// PlanWorkflow builds and persists a workflow: rules choose the flow, the
// orchestrator staffs it, and routing picks the models. The plan is not
// executable until StartWorkflow receives the user's approval.
func (service *Service) PlanWorkflow(request WorkflowRequestView) (WorkflowPlanView, error) {
	if err := service.ready(); err != nil {
		return WorkflowPlanView{}, err
	}
	if strings.TrimSpace(request.ProjectID) == "" {
		return WorkflowPlanView{}, errors.New("choose a project")
	}
	// A title alone is the task's details too; details alone get a title.
	if strings.TrimSpace(request.Objective) == "" {
		request.Objective = request.Title
	}
	if strings.TrimSpace(request.Objective) == "" {
		return WorkflowPlanView{}, errors.New("write a title or the details of the task")
	}
	if request.Kind == "" {
		request.Kind = domain.TaskImplementation
	}
	if request.SourceKind == "" {
		request.SourceKind = "request"
	}
	if request.SourceKind != "request" && request.SourceKind != "github" && request.SourceKind != "linear" {
		return WorkflowPlanView{}, fmt.Errorf("unsupported task source %q", request.SourceKind)
	}
	ctx, cancel := context.WithTimeout(service.context(), service.config.PlanningTimeout)
	defer cancel()
	// Asked before anything is spent: a task is created for someone, and
	// the task records who.
	principal, err := service.principal(ctx)
	if err != nil {
		return WorkflowPlanView{}, err
	}
	project, err := service.store.Project(ctx, request.ProjectID)
	if err != nil {
		return WorkflowPlanView{}, fmt.Errorf("load project: %w", err)
	}
	if err := refuseRemoved(project); err != nil {
		return WorkflowPlanView{}, err
	}
	// Before anything is spent: a definition this build cannot apply as
	// its source requires refuses every plan in the project.
	shared, err := service.checkSources(ctx, project)
	if err != nil {
		return WorkflowPlanView{}, err
	}
	profile, err := repository.Inspect(ctx, project.Repository)
	if err != nil {
		return WorkflowPlanView{}, fmt.Errorf("profile repository: %w", err)
	}
	packages, err := skill.Discover(filepath.Join(profile.Root, ".inkuna", "skills"))
	if err != nil {
		return WorkflowPlanView{}, fmt.Errorf("discover skills: %w", err)
	}
	document, _, _, err := service.loadRules(project)
	if err != nil {
		return WorkflowPlanView{}, err
	}
	registry, shadowed, err := service.agentRegistry(profile.Root)
	if err != nil {
		return WorkflowPlanView{}, fmt.Errorf("load agents: %w", err)
	}
	task := domain.TaskSpec{
		Version:            domain.ContractVersion,
		ID:                 newID("task"),
		Kind:               request.Kind,
		Objective:          strings.TrimSpace(request.Objective),
		Source:             domain.TaskSource{Kind: request.SourceKind, Reference: strings.TrimSpace(request.SourceReference), Repository: project.Repository},
		AcceptanceCriteria: cleanEntries(request.AcceptanceCriteria),
		Constraints:        cleanEntries(request.Constraints),
		Labels:             cleanEntries(request.Labels),
		Repository:         profile.Root,
		Priority:           1,
		Budget:             domain.Budget{WallTimeSeconds: int(service.config.WorkerTimeout / time.Second), MaxOutputBytes: 4 << 20},
		Metadata:           map[string]string{"created": time.Now().UTC().Format(time.RFC3339Nano)},
	}
	workingDir := profile.Root
	if !service.config.Orchestrator.ReadTools {
		if workingDir, err = service.orchestratorDir(); err != nil {
			return WorkflowPlanView{}, err
		}
	}
	var planningEvents []domain.RunEvent
	var planningMu sync.Mutex
	input := planner.Input{
		Task: task, Repository: profile, Skills: packages, Rules: document, Registry: registry, DefaultRuntime: project.DefaultRuntime,
		Disabled: service.config.Orchestrator.Disabled, Routing: service.routingPolicy(), Runtimes: service.declaredRuntimes(),
		MCPRuntimes: service.mcpRuntimes(), MCPServers: service.config.File.MCPServerNames(), TrackerKinds: service.allowedTrackerKinds(project.Repository),
		RuleName: strings.TrimSpace(request.RuleName), Cache: staffingCache{store: service.store}, BypassCache: request.Restaff,
		Options: orchestrator.Options{
			Runtime: service.config.Orchestrator.Runtime, Model: service.config.Orchestrator.Model, ReasoningEffort: service.config.Orchestrator.ReasoningEffort,
			MaxTurns: service.config.Orchestrator.MaxTurns, WallTimeSeconds: int(service.config.PlanningTimeout / time.Second),
			WorkingDir: workingDir, RunID: "orchestrator-" + task.ID, ReadTools: service.config.Orchestrator.ReadTools,
		},
		Sink: func(event domain.RunEvent) error {
			planningMu.Lock()
			defer planningMu.Unlock()
			event.Payload = nil
			if len(planningEvents) < planningEventLimit {
				planningEvents = append(planningEvents, event)
			}
			return nil
		},
	}
	if !service.config.Orchestrator.Disabled {
		if runtime, err := service.runtimeFor(service.config.Orchestrator.Runtime); err == nil {
			input.Orchestrator = orchestrator.RuntimeRemote{Runtime: runtime}
		}
	}
	// A story outside its team's current cycle is refused before anything is
	// spent, with the title the person wrote; a title planning writes is
	// checked too, in case it names another story.
	asked := task
	asked.Title = strings.TrimSpace(request.Title)
	checked, err := service.requireCurrentCycle(ctx, project.Repository, asked, "")
	if err != nil {
		return WorkflowPlanView{}, err
	}
	// The task is saved only once planning succeeds, so its calls name who
	// they were made for themselves.
	origin := storage.UsageOrigin{Project: project.ID, Principal: principal.ID}
	// The title comes before staffing and branching, which both read it.
	titler, _ := input.Orchestrator.(orchestrator.TitleProposer)
	titleNotes, titleCall, err := titleTask(ctx, &task, request.Title, titler, input.Options, input.Sink)
	usageNotes := service.recordPlanningCall(ctx, origin, task.ID, input.Options, "title", titleCall)
	if err != nil {
		return WorkflowPlanView{}, err
	}
	if _, err := service.requireCurrentCycle(ctx, project.Repository, task, checked); err != nil {
		return WorkflowPlanView{}, err
	}
	input.Task = task
	// Each orchestrator call is recorded as soon as it returns: it was paid
	// for however planning ends, and planning can still fail after it, the
	// task unsaved, when the call used up the planning deadline.
	result, err := planner.Build(ctx, input)
	usageNotes = append(usageNotes, service.recordPlanningCall(ctx, origin, task.ID, input.Options, "staffing", result.Staffing)...)
	if err != nil {
		return WorkflowPlanView{}, err
	}
	proposer, _ := input.Orchestrator.(orchestrator.BranchingProposer)
	var branchingCall *orchestrator.Call
	result.Workflow.Branching, branchingCall, err = service.planBranching(ctx, profile.Root, task, result.Workflow, proposer, input.Options, input.Sink)
	usageNotes = append(usageNotes, service.recordPlanningCall(ctx, origin, task.ID, input.Options, "branching", branchingCall)...)
	if err != nil {
		return WorkflowPlanView{}, err
	}
	result.Notes = append(titleNotes, result.Notes...)
	result.Notes = append(result.Notes, shared.Notes(time.Now())...)
	result.Workflow.Sources = shared.Snapshots()
	for _, name := range shadowed {
		result.Notes = append(result.Notes, fmt.Sprintf("remote agent %q is not used here: a local agent has the same name", name))
	}
	configureWorkflow(&result.Workflow, service.config.MaxConcurrency, profile, service.config.PlanningTimeout, service.config.WorkerTimeout, service.config.VerifierTimeout, service.config.ApprovalTimeout)
	if err := service.store.CreateTask(ctx, project.ID, "planned", principal.ID, task); err != nil {
		return WorkflowPlanView{}, fmt.Errorf("save task: %w", err)
	}
	// The servers the view below shows, and the shared stages and groups the
	// plan expanded, so approval can tell if one changes.
	result.Workflow.MCPServerDigests = mcpServerDigests(service.config.File)
	if result.Workflow.StageDigests, err = stageDigests(document, result.RuleName); err != nil {
		return WorkflowPlanView{}, err
	}
	result.Workflow.UserMCP = service.userMCPByRuntime()
	applyUserServers(&result.Workflow, result.Workflow.UserMCP)
	if err := service.store.SaveWorkflow(ctx, "planned", result.Workflow); err != nil {
		return WorkflowPlanView{}, fmt.Errorf("save workflow: %w", err)
	}
	if decision, err := json.Marshal(result.Decision); err == nil && len(result.Decision.Assignments) > 0 {
		_, _ = service.storeArtifact(ctx, task.ID, "", "plan", agent.Orchestrator, "decision", "orchestrator-decision.json", "application/json", strings.NewReader(string(decision)))
	}
	usage, err := service.store.PlanningUsage(context.WithoutCancel(ctx), task.ID)
	if err != nil {
		usage, usageNotes = []domain.UsageRecord{}, append(usageNotes, "The orchestrator's usage could not be read: "+err.Error())
	}
	result.Notes = append(result.Notes, usageNotes...)
	planningMu.Lock()
	events := append([]domain.RunEvent(nil), planningEvents...)
	planningMu.Unlock()
	view := WorkflowPlanView{
		Project: projectView(project), Task: task, Workflow: result.Workflow, RuleName: result.RuleName, Orchestrated: result.Orchestrated,
		Decision: result.Decision, Notes: result.Notes, Events: events, Candidates: agentCandidates(registry), Usage: usage,
	}
	if !result.Reused.IsZero() {
		view.StaffingReused = result.Reused.UTC().Format(time.RFC3339)
	}
	for _, entry := range service.config.File.MCPServers {
		view.MCPServers = append(view.MCPServers, entry.Brief())
	}
	return view, nil
}

// agentCandidates lists every worker the approval screen may assign,
// sorted by name.
func agentCandidates(registry agent.Registry) []AgentCandidateView {
	workers := registry.Workers()
	candidates := make([]AgentCandidateView, 0, len(workers))
	for _, definition := range workers {
		candidates = append(candidates, AgentCandidateView{
			Name: definition.Name, Description: definition.Description, Permission: definition.Permission, Runtime: definition.Runtime,
			Model: definition.Model, Source: definition.Source, TaskKinds: definition.TaskKinds,
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	return candidates
}

// StartWorkflow records the approval and schedules execution. The approved
// workflow is rebuilt from the persisted plan and the user's edits (agent,
// runtime, complexity, permission, commands, and allowed paths per agent
// step, and commands per evaluation step) exactly as ReviseWorkflow shows
// it; an approval that no longer matches that derivation is refused. Runs
// beyond the configured task limit wait in the queued state.
func (service *Service) StartWorkflow(workflowJSON string) (WorkflowRunView, error) {
	if err := service.ready(); err != nil {
		return WorkflowRunView{}, err
	}
	var approved domain.WorkflowSpec
	if err := json.Unmarshal([]byte(workflowJSON), &approved); err != nil {
		return WorkflowRunView{}, fmt.Errorf("decode workflow: %w", err)
	}
	return service.startApproved(approved, nil, "")
}

// reserveStart claims workflowID for one approval, refusing when it has a
// run that has not ended or another approval holds it. The run registered
// later is checked again, which also covers a run started by a resume.
func (service *Service) reserveStart(workflowID string) (func(), error) {
	service.runsMu.Lock()
	defer service.runsMu.Unlock()
	if service.starting[workflowID] {
		return nil, fmt.Errorf("workflow %q is already being started", workflowID)
	}
	for _, current := range service.runs {
		if current.view.WorkflowID == workflowID && !terminalRunState(current.view.State) {
			return nil, fmt.Errorf("workflow %q already has an active run", workflowID)
		}
	}
	if service.starting == nil {
		service.starting = make(map[string]bool)
	}
	service.starting[workflowID] = true
	return func() {
		service.runsMu.Lock()
		defer service.runsMu.Unlock()
		delete(service.starting, workflowID)
	}, nil
}

// startApproved is StartWorkflow for an approval made against base: the
// persisted plan when base is nil, else the plan of the run rerunOf, which
// the new run starts over from and records.
func (service *Service) startApproved(approved domain.WorkflowSpec, base *domain.WorkflowSpec, rerunOf string) (WorkflowRunView, error) {
	if err := domain.ValidateRoutedWorkflow(approved); err != nil {
		return WorkflowRunView{}, err
	}
	// Asked before configMu is taken, so a slow provider holds up no save;
	// the run records who approved it.
	principal, err := service.principal(service.context())
	if err != nil {
		return WorkflowRunView{}, err
	}
	// Asked again, as before a plan, since a source may have come to
	// enforce something since this one was made; and before configMu, so a
	// slow source holds up no save either.
	if service.hasSources() {
		if project, err := service.store.ProjectForTask(service.context(), approved.TaskID); err == nil {
			if _, err := service.checkSources(service.context(), project); err != nil {
				return WorkflowRunView{}, err
			}
		}
	}
	// The approval is checked against the configuration and the run copies
	// its dependencies from it, so no save may land between the two.
	service.configMu.RLock()
	locked := true
	unlock := func() {
		if locked {
			locked = false
			service.configMu.RUnlock()
		}
	}
	defer unlock()
	ctx := service.context()
	var revision approvalRevision
	if base == nil {
		revision, err = service.reviseApproval(ctx, approved)
	} else {
		revision, err = service.reviseFrom(ctx, *base, approved)
	}
	if err != nil {
		return WorkflowRunView{}, err
	}
	if err := checkApprovalCurrent(revision.spec, approved); err != nil {
		return WorkflowRunView{}, err
	}
	spec, task, registry := revision.spec, revision.task, revision.registry
	project, err := service.store.ProjectForTask(ctx, spec.TaskID)
	if err != nil {
		return WorkflowRunView{}, fmt.Errorf("load workflow project: %w", err)
	}
	if err := refuseRemoved(project); err != nil {
		return WorkflowRunView{}, err
	}
	checkout := false
	if plan := spec.Branching; plan != nil {
		checkout = !plan.Worktree && hasWritableStep(spec)
		if checkout {
			if _, err := checkoutReady(ctx, project.Repository, plan.Base); err != nil {
				return WorkflowRunView{}, err
			}
		}
	}
	// Reserved before the policies are asked, so they are never asked about
	// a start Go will refuse, a second approval of the same workflow
	// included; released once the run is registered, or the start fails.
	release, err := service.reserveStart(spec.ID)
	if err != nil {
		return WorkflowRunView{}, err
	}
	defer release()
	// Every check of Go's has passed, and nothing is written yet.
	if err := service.decide(ctx, planDecision(project.ID, spec, rerunOf), &principal); err != nil {
		return WorkflowRunView{}, err
	}
	if plan := spec.Branching; plan != nil {
		// Approval is what writes proposed configuration, into the repository
		// root planning read it from.
		if len(plan.Proposed) > 0 {
			written, err := branching.Apply(task.Repository, plan.Proposed)
			if err != nil {
				return WorkflowRunView{}, fmt.Errorf("write %s: %w", branching.RelativePath, err)
			}
			if checkout && len(written) > 0 {
				return WorkflowRunView{}, fmt.Errorf("wrote the proposed values to %s; a run without worktrees needs a clean checkout, so commit that file and approve again", branching.RelativePath)
			}
		}
	}
	skills := make(map[string]string, len(revision.packages))
	descriptions := make(map[string]string, len(revision.packages))
	briefs := make(map[string]skillBrief, len(revision.packages))
	for _, current := range revision.packages {
		skills[current.ID] = current.Body
		descriptions[current.ID] = current.Description
		briefs[current.ID] = skillBrief{description: current.Description, onDemand: current.Manifest.Delivery == skill.DeliveryOnDemand}
	}
	checkoutDocs := service.runDocsIn(project, project.Repository)
	// Read at approval, before any step can change the checkout's settings.
	story := service.approvedStory(project.Repository, task, spec)
	if err := service.store.ApproveWorkflow(ctx, spec); err != nil {
		return WorkflowRunView{}, fmt.Errorf("approve workflow: %w", err)
	}
	service.ensureBus()
	service.registerAgents(registry, descriptions)

	runID := newID("run")
	runContext, cancel := context.WithCancel(service.context())
	started := time.Now().UTC()
	run := &liveExecution{
		view: WorkflowRunView{
			ID: runID, WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: project.ID, State: "queued", Title: task.DisplayTitle(),
			RuleName: spec.RuleName, Orchestrated: spec.Orchestrated, Live: true,
			StartedAt: started.Format(time.RFC3339Nano), Events: make([]domain.RunEvent, 0), Steps: stepViews(spec, 1), Round: 1,
			RerunOf: rerunOf,
		},
		round:   1,
		done:    make(chan struct{}),
		started: started, ctx: runContext, cancel: cancel,
		active: make(map[string]agentruntime.AgentRuntime), registry: registry, skills: skills, skillBriefs: briefs, docs: map[string]runDocs{project.Repository: checkoutDocs},
		results: make(map[string]domain.HandoffResult), contexts: make(map[string]domain.ContextEnvelope), outbound: make(map[string]string), calls: make(map[string]domain.UsageRecord), sequences: make(map[string]int64),
		project: project, task: task, spec: spec, deps: service.snapshotDependencies(), story: story,
	}
	for _, step := range spec.Steps {
		if step.Kind == domain.StepAgent {
			run.view.Runtime = step.Runtime
			run.view.Agent = step.Agent
			if step.ModelSelection != nil {
				run.view.Model = step.ModelSelection.Model
			}
			break
		}
	}
	service.runsMu.Lock()
	if service.runs == nil {
		service.runs = make(map[string]*liveExecution)
	}
	for _, current := range service.runs {
		if current.view.WorkflowID == spec.ID && !terminalRunState(current.view.State) {
			service.runsMu.Unlock()
			cancel()
			return WorkflowRunView{}, fmt.Errorf("workflow %q already has an active run", spec.ID)
		}
	}
	service.runs[runID] = run
	slots := service.taskSlots
	view := copyRunView(run.view)
	record := executionRecord(run.view)
	record.ApprovedBy = principal.ID
	service.runsMu.Unlock()
	// Only now, with the run registered, may a project removal, which takes
	// configMu and then reads the runs, go ahead: it sees this run and
	// refuses, or it ran before refuseRemoved above and this start refused.
	unlock()
	err = service.store.SaveExecution(ctx, record)
	if err == nil {
		err = service.store.SaveExecutionPlan(ctx, runID, spec)
	}
	if err == nil && story != nil {
		err = service.saveRunStory(ctx, runID, *story)
	}
	if err != nil {
		cancel()
		service.runsMu.Lock()
		delete(service.runs, runID)
		service.runsMu.Unlock()
		return WorkflowRunView{}, fmt.Errorf("record execution: %w", err)
	}
	service.auditDecision("plan.approve", principal, record, "Approved the plan", map[string]string{"workflow": spec.ID, "rule": spec.RuleName, "rerunOf": rerunOf})
	service.runsWG.Add(1)
	go func() {
		defer service.runsWG.Done()
		defer close(run.done)
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-runContext.Done():
			service.finishRun(runID, "cancelled", "")
			return
		}
		service.executeWorkflow(runContext, runID, project, task, spec, workflowengine.Round{Restart: allSteps(spec)})
	}()
	service.emit("inkuna:runs", runID)
	return view, nil
}

// WorkflowRuns lists live runs first and then persisted history.
func (service *Service) WorkflowRuns() ([]WorkflowRunView, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	service.runsMu.RLock()
	views := make([]WorkflowRunView, 0, len(service.runs))
	seen := make(map[string]bool, len(service.runs))
	for _, run := range service.runs {
		view := copyRunView(run.view)
		view.Events = nil
		views = append(views, view)
		seen[run.view.ID] = true
	}
	service.runsMu.RUnlock()
	executions, err := service.store.Executions(service.context(), 200)
	if err != nil {
		return nil, err
	}
	for _, execution := range executions {
		if seen[execution.ID] {
			continue
		}
		views = append(views, historyView(execution))
	}
	sort.Slice(views, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339Nano, views[i].StartedAt)
		right, _ := time.Parse(time.RFC3339Nano, views[j].StartedAt)
		return left.After(right)
	})
	service.attachTaskCosts(views)
	service.attachCheckTotals(views)
	service.attachDeleteRefusals(views)
	service.attachPullRequests(views)
	return views, nil
}

func (service *Service) WorkflowRun(runID string) (WorkflowRunView, error) {
	service.runsMu.RLock()
	run := service.runs[runID]
	service.runsMu.RUnlock()
	if run == nil {
		if service.store == nil {
			return WorkflowRunView{}, fmt.Errorf("run %q was not found", runID)
		}
		execution, err := service.store.Execution(service.context(), runID)
		if errors.Is(err, domain.ErrNotFound) {
			return WorkflowRunView{}, fmt.Errorf("run %q was not found", runID)
		}
		if err != nil {
			return WorkflowRunView{}, err
		}
		{
			view := historyView(execution)
			view.Events = []domain.RunEvent{}
			// A run no longer in memory shows its current round as SQLite
			// keeps it.
			ctx := service.context()
			if history, err := service.loadHistory(ctx, execution); err == nil {
				if round, err := history.round(ctx, service.store, view.Round); err == nil {
					view.Steps, view.Events = round.Steps, round.Events
				}
			}
			if commits, err := service.store.Commits(ctx, runID); err == nil && len(commits) > 0 {
				view.LastCommit = commitView(commits[len(commits)-1])
			}
			views := []WorkflowRunView{view}
			service.attachTaskCosts(views)
			service.attachCheckTotals(views)
			service.attachPullRequests(views)
			service.attachStory(&views[0])
			return views[0], nil
		}
	}
	service.runsMu.RLock()
	views := []WorkflowRunView{copyRunView(run.view)}
	service.runsMu.RUnlock()
	service.attachTaskCosts(views)
	service.attachCheckTotals(views)
	service.attachPullRequests(views)
	service.attachStory(&views[0])
	return views[0], nil
}

func (service *Service) LatestWorkflowRun() (WorkflowRunView, error) {
	service.runsMu.RLock()
	defer service.runsMu.RUnlock()
	var latest *liveExecution
	for _, run := range service.runs {
		if latest == nil || run.started.After(latest.started) {
			latest = run
		}
	}
	if latest == nil {
		return WorkflowRunView{}, errors.New("no workflow run is available")
	}
	return copyRunView(latest.view), nil
}

func (service *Service) CancelWorkflowRun(runID string) (WorkflowRunView, error) {
	// A run paused at a review gate has nothing running to stop, and after a
	// restart it is not in memory at all.
	service.runsMu.RLock()
	run := service.runs[runID]
	reviewing := run == nil || run.view.State == storage.Reviewing
	service.runsMu.RUnlock()
	if reviewing && service.store != nil {
		return service.cancelReview(runID)
	}
	return service.cancelLive(runID)
}

// cancelLive stops a run that is in memory and running.
func (service *Service) cancelLive(runID string) (WorkflowRunView, error) {
	service.runsMu.Lock()
	run := service.runs[runID]
	if run == nil {
		service.runsMu.Unlock()
		return WorkflowRunView{}, fmt.Errorf("run %q was not found", runID)
	}
	if terminalRunState(run.view.State) {
		view := copyRunView(run.view)
		service.runsMu.Unlock()
		return view, nil
	}
	// The run paused for review since CancelWorkflowRun looked; its
	// goroutine has ended, so only the review's cancel can end it.
	if run.view.State == storage.Reviewing && service.store != nil {
		service.runsMu.Unlock()
		return service.cancelReview(runID)
	}
	run.view.State = "cancelling"
	run.cancel()
	active := make(map[string]agentruntime.AgentRuntime, len(run.active))
	for stepRunID, current := range run.active {
		active[stepRunID] = current
	}
	a2aTasks := append([]string(nil), run.a2aTasks...)
	bus := service.bus
	view := copyRunView(run.view)
	service.runsMu.Unlock()
	for stepRunID, current := range active {
		_ = current.Cancel(stepRunID)
	}
	if bus != nil {
		for _, id := range a2aTasks {
			_, _ = bus.CancelTask(context.Background(), id)
		}
	}
	if service.store != nil {
		_ = service.store.CancelPendingInputs(context.Background(), runID)
	}
	service.emit("inkuna:inbox", runID)
	return view, nil
}

// allSteps is the restart set of a run's first round: every step.
func allSteps(spec domain.WorkflowSpec) map[string]bool {
	all := make(map[string]bool, len(spec.Steps))
	for _, step := range spec.Steps {
		all[step.ID] = true
	}
	return all
}

func (service *Service) executeWorkflow(ctx context.Context, runID string, project storage.Project, task domain.TaskSpec, spec domain.WorkflowSpec, round workflowengine.Round) {
	service.updateRun(runID, func(view *WorkflowRunView) { view.State = "running" })
	service.persistRun(runID)
	if spec.Branching != nil && spec.Branching.Cleanup == domain.CleanupPrune {
		service.pruneWorktrees(ctx, project, runID)
	}
	// Approving the plan approved its approval stages, except review gates,
	// which pause the run for a person each round (docs/feedback-loop.md).
	approved := make(map[string]bool)
	for _, step := range spec.Steps {
		if step.Kind == domain.StepApproval && !step.Review {
			approved[step.ID] = true
		}
	}
	var err error
	for {
		_, err = workflowengine.ExecuteRound(ctx, spec, approved, round, func(stepContext context.Context, step domain.WorkflowStep) (domain.HandoffResult, error) {
			return service.executeWorkflowStep(stepContext, runID, project, task, spec, step)
		})
		// A failed check whose rule sends it back starts the next round in
		// place, holding the run's slot, until a stage reaches its cap.
		next, again, loopErr := service.checkLoop(ctx, runID, spec, err)
		if !again {
			err = loopErr
			break
		}
		round = next
	}
	// The round has made its commits; its threads follow them before the run
	// pauses or ends, which is when a person can write more.
	service.runsMu.RLock()
	current := 0
	if live := service.runs[runID]; live != nil {
		current = live.view.Round
	}
	service.runsMu.RUnlock()
	service.outdateComments(runID, max(current, 1))
	var paused *workflowengine.PausedError
	if errors.As(err, &paused) && ctx.Err() == nil {
		if err = service.pauseForReview(ctx, runID, spec, paused.Gates); err == nil {
			// A paused run writes nothing, so the checkout is free until
			// the person answers.
			service.releaseCheckout(runID)
			return
		}
	}
	state := "completed"
	errorMessage := ""
	if err != nil {
		state = "failed"
		errorMessage = err.Error()
		if ctx.Err() != nil {
			state = "cancelled"
			errorMessage = ""
		}
	}
	// A completed run's leftovers go before the checkout is free, so they
	// are never the next run's files.
	if state == "completed" {
		service.cleanOut(runID, project)
	}
	// The checkout is free before the run reports finished, so a run started
	// the moment this one ends can claim it.
	service.releaseCheckout(runID)
	service.finishRun(runID, state, errorMessage)
	service.reportStory(runID)
	service.cleanupAfterRun(runID, project)
	service.evictTerminalRuns()
}

func hasWritableStep(spec domain.WorkflowSpec) bool {
	for _, step := range spec.Steps {
		if step.Kind == domain.StepAgent && step.Permission == domain.PermissionWorktree {
			return true
		}
	}
	return false
}

func (service *Service) finishRun(runID, state, errorMessage string) {
	service.updateRun(runID, func(view *WorkflowRunView) {
		view.State = state
		view.Error = errorMessage
		view.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		for index := range view.Steps {
			if view.Steps[index].State == "running" || view.Steps[index].State == "waiting" || view.Steps[index].State == storage.Reviewing {
				view.Steps[index].State = state
			}
		}
	})
	service.persistRun(runID)
	service.releaseAssignments(runID)
	title := "Task " + state
	kind := "completed"
	if state == "failed" {
		kind = "failed"
	}
	service.runsMu.RLock()
	run := service.runs[runID]
	body := ""
	if run != nil {
		body = run.view.Title
	}
	service.runsMu.RUnlock()
	if state == "completed" || state == "failed" {
		_ = service.dependenciesFor(runID).notifier.Notify(notify.Notification{Title: title, Body: body, Kind: kind, Reference: runID})
	}
	service.emit("inkuna:runs", runID)
}

func (service *Service) executeWorkflowStep(ctx context.Context, executionID string, project storage.Project, task domain.TaskSpec, spec domain.WorkflowSpec, step domain.WorkflowStep) (domain.HandoffResult, error) {
	runtimeName := step.Runtime
	if runtimeName == "" {
		runtimeName = spec.DefaultRuntime
	}
	workingDirectory := project.Repository
	service.runsMu.RLock()
	run := service.runs[executionID]
	if run != nil && run.view.Worktree != "" {
		workingDirectory = run.view.Worktree
	}
	service.runsMu.RUnlock()
	if run == nil {
		return domain.HandoffResult{}, fmt.Errorf("run %q was not found", executionID)
	}
	var workspaceNotes []string
	if step.Permission == domain.PermissionWorktree {
		// Worktree initialization always acquires worktreeMu before runsMu.
		run.worktreeMu.Lock()
		service.runsMu.RLock()
		workingDirectory = run.view.Worktree
		service.runsMu.RUnlock()
		if workingDirectory == "" {
			prepared, err := service.prepareWorkspace(ctx, executionID, project, spec, step)
			if err != nil {
				run.worktreeMu.Unlock()
				return domain.HandoffResult{}, err
			}
			workingDirectory = prepared.path
			workspaceNotes = prepared.notes
			// Read before any worker commits, so it is where the run started.
			// Without it the run's changes fall back to the parent of its
			// first commit.
			base, err := worktree.Head(ctx, prepared.path)
			if err != nil {
				workspaceNotes = append(workspaceNotes, "Could not record the commit this run starts from: "+err.Error())
			}
			// Indexed before any step can see the worktree, so before a worker
			// changes it.
			worktreeDocs := service.runDocsIn(project, prepared.path)
			service.runsMu.Lock()
			run.docs[prepared.path] = worktreeDocs
			service.runsMu.Unlock()
			service.updateRun(executionID, func(view *WorkflowRunView) {
				view.Worktree = prepared.path
				view.Branch = prepared.branch
				view.BaseCommit = base
				view.Cleanup = prepared.cleanup
			})
			service.persistRun(executionID)
		}
		run.worktreeMu.Unlock()
	}
	service.runsMu.RLock()
	round := run.round
	service.runsMu.RUnlock()
	stepRunID := stepRunID(executionID, round, step.ID)
	if err := service.store.StartRun(ctx, stepRunID, executionID, spec.ID, step.ID, round, runtimeName, workingDirectory); err != nil {
		return domain.HandoffResult{}, fmt.Errorf("start step run: %w", err)
	}
	for _, note := range workspaceNotes {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventLifecycle, note))
	}
	// The run's first step to start moves its story to In Progress, under
	// that step's run on the timeline.
	service.startStory(ctx, run, stepRunID)
	service.updateStep(executionID, step.ID, func(view *StepRunView) {
		view.State = "running"
		view.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	})
	service.emit("inkuna:runs", executionID)

	var result domain.HandoffResult
	var runErr error
	switch step.Kind {
	case domain.StepAgent:
		if step.Permission == domain.PermissionWorktree {
			run.writeMu.Lock()
			result, runErr = service.executeAgentStep(ctx, run, stepRunID, workingDirectory, step, runtimeName)
			run.writeMu.Unlock()
		} else {
			result, runErr = service.executeAgentStep(ctx, run, stepRunID, workingDirectory, step, runtimeName)
		}
	case domain.StepScript:
		result, runErr = service.executeProfileStep(ctx, executionID, stepRunID, project.Repository)
	case domain.StepEvaluation:
		var inputs humanInputsFile
		if inputs, runErr = service.humanInputs(ctx, executionID, spec, step); runErr == nil {
			result, runErr = service.executeEvaluationStep(ctx, executionID, stepRunID, workingDirectory, inputs, step)
		}
	case domain.StepHumanInput:
		result, runErr = service.executeHumanInputStep(ctx, run, stepRunID, step)
	case domain.StepApproval:
		result = domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted, Summary: "Approval recorded"}
		runErr = service.recordSyntheticStep(executionID, stepRunID, "Approval recorded")
	default:
		runErr = fmt.Errorf("unsupported step kind %q", step.Kind)
	}
	// What a step asks of the tracker is checked and kept apart from its
	// handoff, and never passed on to the steps after it.
	if step.Kind == domain.StepAgent && runErr == nil && len(result.TrackerChanges) > 0 {
		service.keepTrackerChanges(ctx, run, step, stepRunID, round, result.TrackerChanges)
	}
	result.TrackerChanges = nil
	state := "completed"
	if runErr != nil {
		state = "failed"
		// A round's target that failed keeps the replies it gave.
		result = domain.HandoffResult{Version: domain.ContractVersion, Status: state, Summary: runErr.Error(), Addressed: result.Addressed}
		if ctx.Err() != nil {
			state = "cancelled"
			result.Status = state
			result.Summary = "Step cancelled"
		}
	}
	if finishErr := service.store.FinishRun(context.Background(), stepRunID, state, result); finishErr != nil && runErr == nil {
		runErr = finishErr
	}
	service.runsMu.RLock()
	targeted := len(run.feedback[step.ID]) > 0
	service.runsMu.RUnlock()
	if targeted {
		service.settleFeedback(executionID, stepRunID, round, step.ID, result.Addressed)
	}
	service.runsMu.Lock()
	run.results[step.ID] = result
	service.runsMu.Unlock()
	service.updateStep(executionID, step.ID, func(view *StepRunView) {
		view.State = state
		view.Summary = truncateSummary(result.Summary, 280)
		view.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	})
	service.emit("inkuna:runs", executionID)
	return result, runErr
}

// executeAgentStep sends the step to its agent over the A2A bus and waits
// for the task to end. Pauses for human input and peer exchanges happen
// inside the agent handler; this side only observes the task lifecycle.
func (service *Service) executeAgentStep(ctx context.Context, run *liveExecution, stepRunID, workingDirectory string, step domain.WorkflowStep, runtimeName domain.Runtime) (domain.HandoffResult, error) {
	executionID := run.view.ID
	definition, ok := run.registry.Get(step.Agent)
	if !ok {
		return domain.HandoffResult{}, fmt.Errorf("agent %q is not defined", step.Agent)
	}
	service.runsMu.RLock()
	dependencies := make([]domain.HandoffResult, 0, len(step.DependsOn))
	for _, dependency := range step.DependsOn {
		if result, ok := run.results[dependency]; ok && result.Summary != "" {
			dependencies = append(dependencies, result)
		}
	}
	docs := run.docs[workingDirectory]
	service.runsMu.RUnlock()
	if definition.Remote() {
		return service.executeRemoteStep(ctx, run, stepRunID, step, definition, dependencies)
	}
	service.restoreOutputs(executionID, stepRunID, workingDirectory, dependencies)
	envelope, err := domain.BuildContext(run.task, step, dependencies, run.skills, docs.briefs)
	if err != nil {
		return domain.HandoffResult{}, err
	}
	if docs.unavailable != "" {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventLifecycle, docs.unavailable))
	}
	envelope.Runtime = runtimeName
	envelope.Agent = definition.Name
	envelope.AgentInstructions = definition.Instructions
	if envelope.Metadata == nil {
		envelope.Metadata = map[string]string{}
	}
	envelope.Metadata["peers"] = strings.Join(peerNames(run.registry, definition.Name), ",")
	envelope.Metadata[domain.MetadataPermission] = string(step.Permission)
	service.runsMu.RLock()
	items, round := run.feedback[step.ID], run.round
	service.runsMu.RUnlock()
	service.runsMu.RLock()
	envelope.Resumed = run.resumed[step.ID]
	service.runsMu.RUnlock()
	if len(items) > 0 {
		envelope.Feedback = service.roundFeedback(ctx, executionID, stepRunID, round, items)
		if envelope.PreviousRound, err = service.previousRound(ctx, executionID, step.ID, round); err != nil {
			_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, "The step's earlier runs could not be read, so this round is sent no digest of them: "+err.Error()))
		}
		// A stopped attempt is the resume note's to tell, which says what it
		// left uncommitted rather than that it committed it.
		if envelope.Resumed != nil && envelope.PreviousRound != nil && envelope.PreviousRound.Round == envelope.Resumed.Round {
			envelope.PreviousRound = nil
		}
	}
	environment, err := secretEnvironment(run.deps, runtimeName, definition)
	if err != nil {
		return domain.HandoffResult{}, err
	}
	servers, briefs, err := mcpGrant(run.deps, step)
	if err != nil {
		return domain.HandoffResult{}, err
	}
	var userAdapter agentruntime.AgentRuntime
	var userNames []string
	if len(step.UserMCP) > 0 {
		if userAdapter, err = run.deps.runtime(runtimeName); err != nil {
			return domain.HandoffResult{}, err
		}
		// A run sent back after the user took a server off userMcp no longer
		// gives it, whatever the plan it was approved with says.
		userNames = make([]string, 0, len(step.UserMCP))
		for _, name := range step.UserMCP {
			if slices.Contains(userAdapter.Capabilities().UserMCP, name) {
				userNames = append(userNames, name)
			} else {
				_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, fmt.Sprintf("Your MCP server %q is no longer in runtime %s's userMcp, so the step runs without it", name, runtimeName)))
			}
		}
	}
	if len(step.MCPServers) > 0 || len(userNames) > 0 {
		// The servers the step will have are known, and none has started.
		if err := service.decide(ctx, mcpDecision(run, executionID, step, definition.Name, runtimeName, userNames), nil); err != nil {
			return domain.HandoffResult{}, err
		}
	}
	envelope.MCPServers = briefs
	var dropped []domain.TrackerKind
	if envelope.Tracker, dropped = service.trackerGrant(run, step); len(dropped) > 0 {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventLifecycle,
			fmt.Sprintf("The repository's tracker settings no longer allow %s changes, so this step may not ask for them", domain.JoinTrackerKinds(dropped, ", "))))
	}
	var userServers, pendingSignIn []agentruntime.UserServer
	if userAdapter != nil {
		userServers, pendingSignIn, envelope.UserMCPServers = service.resolveUserServers(ctx, userAdapter, executionID, stepRunID, workingDirectory, userNames)
	}
	// The record and the message are the envelope as its first turn sends
	// it, within the budgets. The assignment keeps it whole: every turn
	// applies the budgets to the whole texts, so a later turn that has to
	// cut more still counts and stores from the originals.
	files := skillFiles(workingDirectory, step.AllowedPaths, envelope.SkillExcerpts)
	envelope = domain.IndexSkills(envelope, onDemandSkills(run.skillBriefs, files))
	sent, err := domain.ApplyBudgets(envelope, run.deps.budgets, service.fullTextFor(ctx, run.task.ID, executionID, step.ID, stepRunID, definition.Name, 1, files))
	if err != nil {
		return domain.HandoffResult{}, err
	}
	service.runsMu.Lock()
	run.contexts[step.ID] = sent
	service.runsMu.Unlock()
	if data, err := json.MarshalIndent(sent, "", "  "); err == nil {
		_, _ = service.storeArtifact(ctx, run.task.ID, executionID, step.ID, definition.Name, "context", step.ID+"-context.json", "application/json", strings.NewReader(string(data)))
	}
	assignment := &workerAssignment{
		id: newID("assignment"), executionID: executionID, stepRunID: stepRunID, step: step, task: run.task, envelope: envelope,
		definition: definition, runtimeName: runtimeName, workingDir: workingDirectory, environment: environment, skillFiles: files,
		servers: servers, userServers: userServers, pendingSignIn: pendingSignIn,
	}
	// A stopped attempt's session, if it kept one, ends partway through work
	// it never finished, so a resumed step starts a new session instead.
	if envelope.Feedback != nil && envelope.Resumed == nil {
		service.resumeRound(ctx, run, assignment, round)
	}
	service.assignMu.Lock()
	service.assignments[assignment.id] = assignment
	service.assignMu.Unlock()
	data, err := envelopeData(sent)
	if err != nil {
		return domain.HandoffResult{}, err
	}
	return service.dispatchAssignment(ctx, run, assignment, []a2a.Part{a2a.TextPart(run.task.Objective), a2a.DataPart(data)})
}

// executeRemoteStep sends a step to a remote advisor. It gets the advisor
// handoff instead of the context envelope, and no working directory, runtime,
// or environment exists for it.
func (service *Service) executeRemoteStep(ctx context.Context, run *liveExecution, stepRunID string, step domain.WorkflowStep, definition agent.Definition, dependencies []domain.HandoffResult) (domain.HandoffResult, error) {
	entry, ok := run.deps.remoteAgents[definition.Name]
	if !ok {
		return domain.HandoffResult{}, fmt.Errorf("remote agent %q was not enabled when this run started", definition.Name)
	}
	if step.Permission != domain.PermissionReadOnly {
		return domain.HandoffResult{}, fmt.Errorf("remote agent %q cannot be given step %q, which writes to the worktree", definition.Name, step.ID)
	}
	parts, err := service.advisorParts(ctx, run, step, entry, dependencies)
	if err != nil {
		return domain.HandoffResult{}, err
	}
	assignment := &workerAssignment{
		id: newID("assignment"), executionID: run.view.ID, stepRunID: stepRunID, step: step, task: run.task, definition: definition, remote: &entry, runtimeName: domain.RuntimeRemote,
	}
	service.assignMu.Lock()
	service.assignments[assignment.id] = assignment
	service.assignMu.Unlock()
	return service.dispatchAssignment(ctx, run, assignment, parts)
}

// dispatchAssignment sends an assignment's first message over the bus and
// waits for the task to end.
func (service *Service) dispatchAssignment(ctx context.Context, run *liveExecution, assignment *workerAssignment, parts []a2a.Part) (domain.HandoffResult, error) {
	executionID, step, definition, stepRunID := run.view.ID, assignment.step, assignment.definition, assignment.stepRunID
	message := a2a.Message{Role: a2a.RoleUser, ContextID: run.task.ID, Parts: parts}
	// A round's target is told, beside its envelope, which round sent it
	// back and who from: the user, a check, or both.
	if feedback := assignment.envelope.Feedback; feedback != nil {
		from := make([]string, 0, 2)
		for _, item := range feedback.Items {
			if !slices.Contains(from, item.From) {
				from = append(from, item.From)
			}
		}
		message.Metadata = map[string]any{"round": feedback.Round, "from": from}
	}
	a2aTask, err := service.bus.SendMessage(run.ctx, definition.Name, a2a.MessageSendParams{
		Message:  message,
		Metadata: map[string]any{"assignment": assignment.id, "executionId": executionID, "stepId": step.ID},
	})
	if err != nil {
		return domain.HandoffResult{}, fmt.Errorf("dispatch to %s: %w", definition.Name, err)
	}
	service.runsMu.Lock()
	run.a2aTasks = append(run.a2aTasks, a2aTask.ID)
	service.runsMu.Unlock()
	// Any early exit after dispatch must stop the A2A task so no handler keeps
	// running for a step that already failed.
	settled := false
	defer func() {
		if !settled {
			_, _ = service.bus.CancelTask(context.Background(), a2aTask.ID)
		}
	}()
	service.updateStep(executionID, step.ID, func(view *StepRunView) { view.A2ATaskID = a2aTask.ID })
	if err := service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventLifecycle, fmt.Sprintf("A2A task %s sent to %s", a2aTask.ID, definition.Name))); err != nil {
		return domain.HandoffResult{}, err
	}
	// A step that ends without its handoff still keeps the replies its
	// turns gave on its feedback.
	var unfinished domain.HandoffResult
	final, err := service.bus.WaitTerminal(ctx, a2aTask.ID)
	if err != nil {
		unfinished.Addressed = assignment.replies()
		return unfinished, err
	}
	settled = true
	assignment.mu.Lock()
	handoff := assignment.handoff
	assignment.mu.Unlock()
	switch final.Status.State {
	case a2a.StateCompleted:
		if handoff == nil {
			handoff = &domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted, Summary: "Agent completed the approved task"}
		}
	case a2a.StateCanceled:
		unfinished.Addressed = assignment.replies()
		if ctx.Err() != nil {
			return unfinished, ctx.Err()
		}
		return unfinished, errors.New("agent task was cancelled")
	default:
		reason := "agent task " + string(final.Status.State)
		if final.Status.Message != nil && final.Status.Message.Text() != "" {
			reason = final.Status.Message.Text()
		}
		unfinished.Addressed = assignment.replies()
		return unfinished, errors.New(reason)
	}
	if step.Permission == domain.PermissionWorktree {
		service.commitStep(ctx, run, step, stepRunID, handoff)
	}
	return *handoff, nil
}

// executeHumanInputStep asks the user a rule-defined question through the
// A2A human agent and blocks until the inbox answers it.
func (service *Service) executeHumanInputStep(ctx context.Context, run *liveExecution, stepRunID string, step domain.WorkflowStep) (domain.HandoffResult, error) {
	executionID := run.view.ID
	if err := service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventLifecycle, "Waiting for human input: "+truncateSummary(step.Prompt, 200))); err != nil {
		return domain.HandoffResult{}, err
	}
	a2aTask, err := service.bus.SendMessage(run.ctx, agent.Human, a2a.MessageSendParams{
		Message:  a2a.Message{Role: a2a.RoleUser, ContextID: run.task.ID, Parts: []a2a.Part{a2a.TextPart(step.Prompt)}},
		Metadata: map[string]any{"executionId": executionID, "stepId": step.ID, "askedBy": "rule:" + run.spec.RuleName, "stage": true},
	})
	if err != nil {
		return domain.HandoffResult{}, err
	}
	service.runsMu.Lock()
	run.a2aTasks = append(run.a2aTasks, a2aTask.ID)
	service.runsMu.Unlock()
	service.updateStep(executionID, step.ID, func(view *StepRunView) {
		view.A2ATaskID = a2aTask.ID
		view.State = "waiting"
	})
	final, err := service.bus.WaitTerminal(ctx, a2aTask.ID)
	if err != nil {
		_, _ = service.bus.CancelTask(context.Background(), a2aTask.ID)
		return domain.HandoffResult{}, err
	}
	if final.Status.State != a2a.StateCompleted {
		return domain.HandoffResult{}, fmt.Errorf("human input %s", final.Status.State)
	}
	if err := service.confirmAnswered(ctx, a2aTask.ID); err != nil {
		return domain.HandoffResult{}, err
	}
	answer := ""
	for _, artifact := range final.Artifacts {
		for _, part := range artifact.Parts {
			if part.Kind == a2a.PartText {
				answer = part.Text
			}
		}
	}
	summary := "Human answered: " + truncateSummary(answer, 200)
	if err := service.appendRunEvent(executionID, syntheticEvent(stepRunID, 1, domain.EventComplete, summary)); err != nil {
		return domain.HandoffResult{}, err
	}
	return domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted, Summary: summary, Decisions: []string{"Human input (" + step.Prompt + "): " + answer}}, nil
}

func (service *Service) executeProfileStep(ctx context.Context, executionID, stepRunID, repositoryPath string) (domain.HandoffResult, error) {
	if err := service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventLifecycle, "Profiling repository")); err != nil {
		return domain.HandoffResult{}, err
	}
	profile, err := repository.Inspect(ctx, repositoryPath)
	if err != nil {
		return domain.HandoffResult{}, err
	}
	summary := fmt.Sprintf("Profiled %s at %s", filepath.Base(profile.Root), profile.Head)
	if err := service.appendRunEvent(executionID, syntheticEvent(stepRunID, 1, domain.EventComplete, summary)); err != nil {
		return domain.HandoffResult{}, err
	}
	return domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted, Summary: summary}, nil
}

func (service *Service) recordSyntheticStep(executionID, stepRunID, summary string) error {
	if err := service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventLifecycle, summary)); err != nil {
		return err
	}
	return service.appendRunEvent(executionID, syntheticEvent(stepRunID, 1, domain.EventComplete, summary))
}

// appendRunEvent persists an event and mirrors it into the live view. Events
// from several sources (runtime stream, A2A lifecycle, commits) share one
// step run, so the sequence is assigned here to keep the primary key unique
// and the timeline ordered.
func (service *Service) appendRunEvent(executionID string, event domain.RunEvent) error {
	service.runsMu.RLock()
	run := service.runs[executionID]
	counted := run != nil && run.sequences[event.RunID] > 0
	service.runsMu.RUnlock()
	// A run not in memory, such as one paused at a review gate before a
	// restart, numbers after what its step run already stored, and so does
	// a round's first event on a step run of an earlier round, such as a
	// tracker change that step asked for, made once the run was approved.
	var last int64
	if !counted {
		var err error
		if last, err = service.store.LastEventSequence(context.Background(), event.RunID); err != nil {
			return err
		}
	}
	service.runsMu.Lock()
	if run != nil {
		if run.sequences == nil {
			run.sequences = make(map[string]int64)
		}
		run.sequences[event.RunID] = max(run.sequences[event.RunID], last) + 1
		event.Sequence = run.sequences[event.RunID]
	} else {
		event.Sequence = last + 1
	}
	service.runsMu.Unlock()
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if err := service.store.AppendEvent(context.Background(), event); err != nil {
		return err
	}
	service.updateRun(executionID, func(view *WorkflowRunView) {
		view.Events = append(view.Events, event)
		if len(view.Events) > liveRunEventLimit {
			view.Events = append([]domain.RunEvent(nil), view.Events[len(view.Events)-liveRunEventLimit:]...)
		}
	})
	return nil
}

func (service *Service) runtimeFor(name domain.Runtime) (agentruntime.AgentRuntime, error) {
	service.runtimesMu.RLock()
	adapter := service.runtimes[name]
	service.runtimesMu.RUnlock()
	if adapter != nil {
		return adapter, nil
	}
	return nil, fmt.Errorf("runtime %q is not declared in inkuna.yaml", name)
}

// declaredRuntimes is what the planner checks a plan against: every runtime
// by name, and whether it may be given a step that writes.
func (service *Service) declaredRuntimes() map[domain.Runtime]bool {
	adapters := service.declaredAdapters()
	declared := make(map[domain.Runtime]bool, len(adapters))
	for name, adapter := range adapters {
		declared[name] = adapter.Capabilities().WorktreeWrite
	}
	return declared
}

// mcpRuntimes names the declared runtimes that can be given MCP servers.
func (service *Service) mcpRuntimes() map[domain.Runtime]bool {
	adapters := service.declaredAdapters()
	runtimes := make(map[domain.Runtime]bool, len(adapters))
	for name, adapter := range adapters {
		runtimes[name] = adapter.Capabilities().MCPServers
	}
	return runtimes
}

// declaredAdapters copies the registry under its lock, so callers can range
// over the result while a configuration save installs a new one.
func (service *Service) declaredAdapters() map[domain.Runtime]agentruntime.AgentRuntime {
	service.runtimesMu.RLock()
	defer service.runtimesMu.RUnlock()
	adapters := make(map[domain.Runtime]agentruntime.AgentRuntime, len(service.runtimes))
	for name, adapter := range service.runtimes {
		adapters[name] = adapter
	}
	return adapters
}

func (service *Service) updateRun(runID string, update func(*WorkflowRunView)) {
	service.runsMu.Lock()
	defer service.runsMu.Unlock()
	if run := service.runs[runID]; run != nil {
		update(&run.view)
	}
}

func (service *Service) updateStep(runID, stepID string, update func(*StepRunView)) {
	service.updateRun(runID, func(view *WorkflowRunView) {
		for index := range view.Steps {
			if view.Steps[index].ID == stepID {
				update(&view.Steps[index])
				return
			}
		}
	})
}

// persistRun mirrors the live header into SQLite so history survives restarts.
func (service *Service) persistRun(runID string) {
	if service.store == nil {
		return
	}
	service.persistMu.Lock()
	defer service.persistMu.Unlock()
	service.runsMu.RLock()
	run := service.runs[runID]
	var record storage.Execution
	// A warning is kept with a step run (events reference one), so one the
	// run itself gives goes with the last of its steps, in plan order, that
	// started.
	warnOn := ""
	if run != nil {
		record = executionRecord(run.view)
		for _, step := range run.view.Steps {
			if step.StartedAt != "" {
				warnOn = stepRunID(run.view.ID, run.round, step.ID)
			}
		}
	}
	service.runsMu.RUnlock()
	if run == nil {
		return
	}
	// A save that fails, as when the database stays busy past its timeout,
	// would leave the stored run behind the one in memory: one that finished
	// would come back as interrupted after a restart. So it is tried once
	// more, and when that fails too the run's timeline says so.
	err := service.store.SaveExecution(context.Background(), record)
	if err == nil {
		return
	}
	log.Printf("inkuna: save run %s: %v; trying again", runID, err)
	if err = service.store.SaveExecution(context.Background(), record); err == nil {
		return
	}
	log.Printf("inkuna: save run %s again: %v", runID, err)
	warning := fmt.Sprintf("The run's state (%s) was not saved: %v. Until a later save succeeds, a restart shows the run as it was last saved", record.State, err)
	if record.State != "running" {
		warning += ", which can be interrupted although it ended"
	}
	if warnOn == "" {
		return
	}
	if err := service.appendRunEvent(runID, syntheticEvent(warnOn, 0, domain.EventWarning, warning+".")); err != nil {
		log.Printf("inkuna: say on run %s that it was not saved: %v", runID, err)
	}
}

func executionRecord(view WorkflowRunView) storage.Execution {
	started, _ := time.Parse(time.RFC3339Nano, view.StartedAt)
	record := storage.Execution{ID: view.ID, WorkflowID: view.WorkflowID, TaskID: view.TaskID, ProjectID: view.ProjectID, State: view.State, Title: view.Title,
		Worktree: view.Worktree, Branch: view.Branch, BaseCommit: view.BaseCommit, Cleanup: view.Cleanup, Round: view.Round, Error: view.Error, StartedAt: started, RerunOf: view.RerunOf}
	if view.FinishedAt != "" {
		record.FinishedAt, _ = time.Parse(time.RFC3339Nano, view.FinishedAt)
	}
	if view.WorktreeRemovedAt != "" {
		record.WorktreeRemovedAt, _ = time.Parse(time.RFC3339Nano, view.WorktreeRemovedAt)
	}
	return record
}

// historyView is the run view of a stored execution that is no longer live.
func historyView(execution storage.Execution) WorkflowRunView {
	view := WorkflowRunView{ID: execution.ID, WorkflowID: execution.WorkflowID, TaskID: execution.TaskID, ProjectID: execution.ProjectID, State: execution.State, Title: execution.Title,
		Worktree: execution.Worktree, Branch: execution.Branch, BaseCommit: execution.BaseCommit, Cleanup: execution.Cleanup, Round: max(execution.Round, 1), Error: execution.Error, StartedAt: execution.StartedAt.Format(time.RFC3339Nano), Steps: []StepRunView{},
		RerunOf: execution.RerunOf}
	if !execution.FinishedAt.IsZero() {
		view.FinishedAt = execution.FinishedAt.Format(time.RFC3339Nano)
	}
	if !execution.WorktreeRemovedAt.IsZero() {
		view.WorktreeRemovedAt = execution.WorktreeRemovedAt.Format(time.RFC3339Nano)
	}
	return view
}

func (service *Service) evictTerminalRuns() {
	service.runsMu.Lock()
	defer service.runsMu.Unlock()
	terminalCount := 0
	for _, run := range service.runs {
		if terminalRunState(run.view.State) {
			terminalCount++
		}
	}
	for terminalCount > retainedTerminalRunLimit {
		var oldestID string
		var oldest *liveExecution
		for id, run := range service.runs {
			if !terminalRunState(run.view.State) || oldest != nil && !run.started.Before(oldest.started) {
				continue
			}
			oldestID, oldest = id, run
		}
		delete(service.runs, oldestID)
		terminalCount--
	}
}

func (service *Service) cancelAllRuns() {
	service.runsMu.Lock()
	type activeRun struct {
		run      *liveExecution
		runtimes map[string]agentruntime.AgentRuntime
	}
	runs := make([]activeRun, 0, len(service.runs))
	for _, run := range service.runs {
		if !terminalRunState(run.view.State) {
			active := make(map[string]agentruntime.AgentRuntime, len(run.active))
			for stepRunID, current := range run.active {
				active[stepRunID] = current
			}
			runs = append(runs, activeRun{run: run, runtimes: active})
		}
	}
	service.runsMu.Unlock()
	for _, current := range runs {
		current.run.cancel()
		for stepRunID, runtime := range current.runtimes {
			_ = runtime.Cancel(stepRunID)
		}
	}
}

func stepViews(spec domain.WorkflowSpec, round int) []StepRunView {
	views := make([]StepRunView, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		view := StepRunView{ID: step.ID, Title: step.Title, Kind: step.Kind, Agent: step.Agent, Runtime: step.Runtime, State: "pending", Round: round, From: step.From.Clone()}
		if step.ModelSelection != nil {
			view.Model = step.ModelSelection.Model
		}
		views = append(views, view)
	}
	return views
}

// commitView is how the run header shows a commit Inkuna recorded.
func commitView(record storage.CommitRecord) *CommitView {
	return &CommitView{Hash: record.Hash, ShortHash: shortHash(record.Hash), Subject: record.Subject, Stat: record.Stat, Date: record.CreatedAt.Format(time.RFC3339), StepID: record.StepID}
}

func copyRunView(view WorkflowRunView) WorkflowRunView {
	events := make([]domain.RunEvent, len(view.Events))
	for index, event := range view.Events {
		event.Payload = nil
		events[index] = event
	}
	view.Events = events
	view.Steps = append([]StepRunView(nil), view.Steps...)
	view.WorkspaceNotes = append([]string(nil), view.WorkspaceNotes...)
	if view.LastCommit != nil {
		commit := *view.LastCommit
		view.LastCommit = &commit
	}
	return view
}

func terminalRunState(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled" || state == "interrupted"
}

func syntheticEvent(runID string, sequence int64, kind domain.EventKind, summary string) domain.RunEvent {
	return domain.RunEvent{Version: domain.ContractVersion, RunID: runID, Sequence: sequence, Timestamp: time.Now().UTC(), Kind: kind, Summary: summary}
}

func cleanEntries(values []string) []string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			clean = append(clean, value)
		}
	}
	return clean
}

func truncateSummary(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// validateApprovalChanges refuses an approved workflow whose structure
// differs from the persisted plan. On agent steps the approval screen may
// change the agent, runtime, complexity, permission, commands, and allowed
// paths, and on evaluation steps the commands; Go checks those and
// re-derives what follows from them (skills, model, turn budget,
// provenance). Everything else, and every other step, must be exactly as
// planned.
func validateApprovalChanges(planned, approved domain.WorkflowSpec) error {
	if planned.Version != approved.Version || planned.ID != approved.ID || planned.TaskID != approved.TaskID ||
		planned.DefaultRuntime != approved.DefaultRuntime || planned.RoutingPolicyVersion != approved.RoutingPolicyVersion ||
		planned.MaxConcurrency != approved.MaxConcurrency || !planned.CreatedAt.Equal(approved.CreatedAt) || len(planned.Steps) != len(approved.Steps) ||
		planned.RuleName != approved.RuleName || planned.Orchestrated != approved.Orchestrated {
		return errors.New("approved workflow structure does not match the persisted plan")
	}
	for index := range planned.Steps {
		expected := normalizeStep(planned.Steps[index])
		actual := normalizeStep(approved.Steps[index])
		switch planned.Steps[index].Kind {
		case domain.StepAgent:
			expected, actual = approvalStepStructure(expected), approvalStepStructure(actual)
			if !reflect.DeepEqual(expected, actual) {
				return fmt.Errorf("approved step %q changes fields outside staffing, access, and model routing", planned.Steps[index].ID)
			}
		case domain.StepEvaluation:
			expected.Commands, expected.AccessEdits = nil, nil
			actual.Commands, actual.AccessEdits = nil, nil
			if !reflect.DeepEqual(expected, actual) {
				return fmt.Errorf("approved step %q is an evaluation step, and only its commands can be changed at approval", planned.Steps[index].ID)
			}
		default:
			if !reflect.DeepEqual(expected, actual) {
				return fmt.Errorf("approved step %q is not an agent or evaluation step and cannot be changed at approval", planned.Steps[index].ID)
			}
		}
	}
	return nil
}

// approvalStepStructure blanks what approval may change on an agent step and
// what Go derives from those changes.
func approvalStepStructure(step domain.WorkflowStep) domain.WorkflowStep {
	step.Agent = ""
	step.Runtime = ""
	step.Complexity = nil
	step.ModelSelection = nil
	step.SelectedSkills = nil
	step.Permission = ""
	step.Commands = nil
	step.AllowedPaths = nil
	step.AccessEdits = nil
	// The user's own servers follow the runtime (applyUserServers), and the
	// tracker kinds the agent.
	step.UserMCP = nil
	step.TrackerKinds = nil
	step.Budget.MaxTurns = 0
	if len(step.Metadata) > 0 {
		metadata := make(map[string]string, len(step.Metadata))
		for key, value := range step.Metadata {
			if key != "agentSource" && key != "assignment" {
				metadata[key] = value
			}
		}
		step.Metadata = metadata
		if len(metadata) == 0 {
			step.Metadata = nil
		}
	}
	return step
}

// normalizeStep makes empty and absent collections compare equal.
func normalizeStep(step domain.WorkflowStep) domain.WorkflowStep {
	if len(step.DependsOn) == 0 {
		step.DependsOn = nil
	}
	if len(step.SelectedSkills) == 0 {
		step.SelectedSkills = nil
	}
	if len(step.AllowedPaths) == 0 {
		step.AllowedPaths = nil
	}
	if len(step.Commands) == 0 {
		step.Commands = nil
	}
	if len(step.ExpectedOutputs) == 0 {
		step.ExpectedOutputs = nil
	}
	if len(step.Metadata) == 0 {
		step.Metadata = nil
	}
	if len(step.MCPServers) == 0 {
		step.MCPServers = nil
	}
	if len(step.UserMCP) == 0 {
		step.UserMCP = nil
	}
	if len(step.TrackerKinds) == 0 {
		step.TrackerKinds = nil
	}
	if len(step.AccessEdits) == 0 {
		step.AccessEdits = nil
	}
	return step
}

func configureWorkflow(spec *domain.WorkflowSpec, maxConcurrency int, profile repository.Profile, planning, worker, verifier, approval time.Duration) {
	spec.MaxConcurrency = maxConcurrency
	for index := range spec.Steps {
		step := &spec.Steps[index]
		switch step.Kind {
		case domain.StepScript:
			step.Budget.WallTimeSeconds = int(planning / time.Second)
		case domain.StepAgent:
			step.Budget.WallTimeSeconds = int(worker / time.Second)
			step.Budget.MaxOutputBytes = 4 << 20
		case domain.StepEvaluation:
			step.Budget.WallTimeSeconds = int(verifier / time.Second)
			if len(step.Commands) == 0 {
				step.Commands = verificationCommands(profile)
			}
		case domain.StepApproval, domain.StepHumanInput:
			step.Budget.WallTimeSeconds = int(approval / time.Second)
		}
	}
}

func verificationCommands(profile repository.Profile) [][]string {
	taskDirectories := manifestDirectories(profile.Manifests, "Taskfile.yml")
	if len(taskDirectories) > 0 {
		commands := make([][]string, 0, len(taskDirectories))
		for _, directory := range taskDirectories {
			if directory == "." {
				commands = append(commands, []string{"task", "test"})
			} else {
				commands = append(commands, []string{"task", "--dir", directory, "test"})
			}
		}
		return commands
	}
	goDirectories := manifestDirectories(profile.Manifests, "go.mod")
	packageDirectories := manifestDirectories(profile.Manifests, "package.json")
	commands := make([][]string, 0, len(goDirectories)+len(packageDirectories))
	for _, directory := range goDirectories {
		if directory == "." {
			commands = append(commands, []string{"go", "test", "./..."})
		} else {
			commands = append(commands, []string{"go", "-C", directory, "test", "./..."})
		}
	}
	for _, directory := range packageDirectories {
		if directory == "." {
			commands = append(commands, []string{"npm", "test", "--", "--run"})
		} else {
			commands = append(commands, []string{"npm", "--prefix", directory, "test", "--", "--run"})
		}
	}
	return commands
}

func manifestDirectories(manifests []string, name string) []string {
	directories := make(map[string]bool)
	for _, manifest := range manifests {
		clean := filepath.Clean(filepath.FromSlash(manifest))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.Base(clean) != name {
			continue
		}
		directories[filepath.ToSlash(filepath.Dir(clean))] = true
	}
	result := make([]string, 0, len(directories))
	for directory := range directories {
		result = append(result, directory)
	}
	sort.Strings(result)
	return result
}
