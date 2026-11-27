// Package planner turns a task into an approved-ready workflow: the rules
// engine shapes the DAG, the agent registry supplies candidates, the
// orchestrator (a headless model) staffs "auto" stages and scores every agent
// stage, and the routing policy picks a model for every agent step. Every
// model decision is validated by Go before it reaches the approval screen.
package planner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	"github.com/inkuna-ai/inkuna/internal/repository"
	"github.com/inkuna-ai/inkuna/internal/routing"
	"github.com/inkuna-ai/inkuna/internal/rules"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/skill"
)

// Input gathers everything the planner needs.
type Input struct {
	Task           domain.TaskSpec
	Repository     repository.Profile
	Skills         []skill.Package
	Rules          rules.Document
	Registry       agent.Registry
	DefaultRuntime domain.Runtime
	// Orchestrator is optional; nil or Disabled falls back to deterministic
	// staffing.
	Orchestrator orchestrator.Remote
	Options      orchestrator.Options
	Disabled     bool
	// Routing is the complexity-to-model policy; the zero value uses the
	// built-in policy.
	Routing routing.Policy
	// Runtimes names every declared runtime and whether it may run
	// worktree-write steps. Nil means the three built-in runtimes.
	Runtimes map[domain.Runtime]bool
	// MCPRuntimes names the runtimes a step granted MCP servers may run on;
	// nil means the three built-in runtimes. MCPServers is the set of
	// servers inkuna.yaml declares.
	MCPRuntimes map[domain.Runtime]bool
	MCPServers  map[string]bool
	// TrackerKinds are the kinds of tracker change the project lets agents
	// ask for: its tracker.yaml, as this machine's inkuna.yaml narrows it.
	// None leaves every step without any.
	TrackerKinds []domain.TrackerKind
	// RuleName, when set, is the rule the user chose instead of letting the
	// rules match ("default" names the default flow).
	RuleName string
	// Sink receives the orchestrator's run events so the UI can show them.
	Sink agentruntime.EventSink
	// Cache, when set, holds staffing decisions by the call that made them, so
	// a plan that would ask the orchestrator the same question again, such as
	// a replan under another rule with the same auto stages, reuses the
	// answer. BypassCache asks again, and the new answer replaces the old.
	// A call with read tools is never cached: it answers from files the
	// key does not cover.
	Cache       DecisionCache
	BypassCache bool
	Now         time.Time
}

// DecisionCache keeps validated staffing decisions by orchestrator.Key.
type DecisionCache interface {
	// Recall returns the decision stored under key and when it was made.
	Recall(ctx context.Context, key string) (orchestrator.Decision, time.Time, bool, error)
	// Remember stores a decision under key, replacing any already there.
	Remember(ctx context.Context, key string, decision orchestrator.Decision) error
}

// Result is the planned workflow plus provenance for the approval screen.
type Result struct {
	Workflow     domain.WorkflowSpec   `json:"workflow"`
	RuleName     string                `json:"ruleName"`
	Decision     orchestrator.Decision `json:"decision"`
	Orchestrated bool                  `json:"orchestrated"`
	Notes        []string              `json:"notes,omitempty"`
	// Staffing is the orchestrator call that staffed the auto stages, kept
	// whatever became of its decision, and returned even when Build fails
	// after it, because the call was paid for; nil when no model was called.
	Staffing *orchestrator.Call `json:"-"`
	// Reused is when the stored decision this plan reused was made; it is
	// zero when the orchestrator was asked, or not used.
	Reused time.Time `json:"-"`
}

// Build produces the workflow. It never fails just because the orchestrator
// is unavailable: the deterministic fallback keeps the harness usable.
func Build(ctx context.Context, input Input) (Result, error) {
	if err := domain.ValidateTask(input.Task); err != nil {
		return Result{}, err
	}
	if input.DefaultRuntime == "" {
		return Result{}, errors.New("default runtime is required")
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	initial := orchestrator.AssessComplexity(input.Task)
	ruleName, flow, notes, err := chooseRule(input, initial.Score)
	if err != nil {
		return Result{}, err
	}
	workflow, err := flow.Workflow(input.Task, ruleName, input.DefaultRuntime, now)
	if err != nil {
		return Result{}, fmt.Errorf("rule %s: %w", ruleName, err)
	}
	available := make(map[string]bool, len(input.Skills))
	for _, pkg := range input.Skills {
		available[pkg.ID] = true
	}
	// A step from the repository's own definition names the rules file it
	// is in, as one from a global library names its file.
	for index := range workflow.Steps {
		if from := workflow.Steps[index].From; from != nil && from.Scope == rules.ScopeRepository && from.File == "" {
			from.File = input.Rules.Source
		}
	}
	replacements, err := input.Rules.Replaced(ruleName)
	if err != nil {
		return Result{}, fmt.Errorf("rule %s: %w", ruleName, err)
	}
	for _, replacement := range replacements {
		notes = append(notes, replacement.Note())
	}
	result := Result{RuleName: ruleName, Notes: notes}
	for _, step := range workflow.Steps {
		if step.Review && step.FeedbackTarget == "" {
			result.Notes = append(result.Notes, fmt.Sprintf("Review gate %q has no writable agent stage upstream for %s tasks, so it can only be approved.", step.ID, input.Task.Kind))
		}
	}

	// A stage that names a server this machine does not declare cannot run
	// as the repository asked, so it is not planned.
	for _, step := range workflow.Steps {
		for _, server := range domain.StageServers(step) {
			if !input.MCPServers[server] {
				return Result{}, fmt.Errorf("rule %s: stage %q names MCP server %q, which inkuna.yaml does not declare", ruleName, step.ID, server)
			}
		}
	}

	// Resolve explicitly named agents, and brief every agent stage: the
	// orchestrator staffs those left as "auto" and scores them all, each for
	// its own work, since a stage that only writes a short note needs less
	// than the one that implements the task.
	stages := make([]orchestrator.StageBrief, 0)
	for index := range workflow.Steps {
		step := &workflow.Steps[index]
		if step.Kind != domain.StepAgent {
			continue
		}
		brief := orchestrator.StageBrief{ID: step.ID, Title: step.Title, Permission: step.Permission, DependsOn: step.DependsOn, Skills: step.SelectedSkills, Runtime: step.Runtime}
		if step.Agent == "" || step.Agent == agent.Auto {
			step.Agent = agent.Auto
			stages = append(stages, brief)
			continue
		}
		definition, ok := input.Registry.Get(step.Agent)
		if !ok || step.Agent == agent.Orchestrator {
			return Result{}, fmt.Errorf("rule %s names unknown agent %q for stage %q", ruleName, step.Agent, step.ID)
		}
		notes, err := applyAgent(step, definition, available, input.MCPServers, "rule "+ruleName+" names "+definition.Name)
		if err != nil {
			return Result{}, err
		}
		// The skills stay the stage's own, which Validate keeps; the runtime
		// is the agent's when it pins one.
		brief.Agent, brief.Runtime = definition.Name, step.Runtime
		stages = append(stages, brief)
		result.Notes = append(result.Notes, notes...)
		if notes, err = grantTrackerKinds(step, definition, input.TrackerKinds); err != nil {
			return Result{}, err
		}
		result.Notes = append(result.Notes, notes...)
	}

	// Past staffing, a failure still reports the orchestrator's call.
	fail := func(err error) (Result, error) { return Result{Staffing: result.Staffing}, err }
	if len(stages) > 0 {
		staffed := staff(ctx, input, stages, available, policy(input))
		result.Decision, result.Staffing, result.Reused = staffed.decision, staffed.call, staffed.reused
		result.Orchestrated = staffed.orchestrated
		result.Notes = append(result.Notes, staffed.notes...)
		byStage := make(map[string]orchestrator.Assignment, len(staffed.decision.Assignments))
		for _, assignment := range staffed.decision.Assignments {
			byStage[assignment.StageID] = assignment
		}
		for index := range workflow.Steps {
			step := &workflow.Steps[index]
			if step.Kind != domain.StepAgent {
				continue
			}
			assignment, ok := byStage[step.ID]
			if !ok {
				if len(result.Notes) > 0 {
					return fail(fmt.Errorf("stage %q was not staffed: %s", step.ID, strings.Join(result.Notes, "; ")))
				}
				return fail(fmt.Errorf("stage %q was not staffed", step.ID))
			}
			step.Complexity = &domain.ComplexityAssessment{Score: assignment.Complexity.Score, Rationale: assignment.Complexity.Rationale, Signals: assignment.Complexity.Signals}
			if step.Agent != agent.Auto {
				continue
			}
			definition, _ := input.Registry.Get(assignment.Agent)
			step.SelectedSkills = assignment.Skills
			notes, err := applyAgent(step, definition, available, input.MCPServers, assignment.Rationale)
			if err != nil {
				return fail(err)
			}
			result.Notes = append(result.Notes, notes...)
			if notes, err = grantTrackerKinds(step, definition, input.TrackerKinds); err != nil {
				return fail(err)
			}
			result.Notes = append(result.Notes, notes...)
		}
	}
	workflow.Orchestrated = result.Orchestrated
	if err := policy(input).Apply(&workflow); err != nil {
		return fail(err)
	}
	if err := CheckRuntimes(workflow, input.Runtimes); err != nil {
		return fail(err)
	}
	dropped, err := DropUnsupportedServers(&workflow, input.MCPRuntimes)
	if err != nil {
		return fail(err)
	}
	result.Notes = append(result.Notes, dropped...)
	if err := CheckServers(workflow, input.MCPRuntimes, input.MCPServers); err != nil {
		return fail(err)
	}
	if err := CheckTrackerGates(workflow); err != nil {
		return fail(err)
	}
	workflow.TrackerKinds = domain.SortedTrackerKinds(input.TrackerKinds)
	result.Workflow = workflow
	return result, nil
}

// chooseRule picks the flow: the user's explicit choice first, then a label
// naming a rule, then the first matching condition, then the default. The
// notes tell the approval screen when something other than a condition
// decided.
func chooseRule(input Input, complexity int) (string, rules.Flow, []string, error) {
	matched, flow := input.Rules.Match(input.Task, complexity)
	chosen := strings.TrimSpace(input.RuleName)
	if chosen != "" {
		named, ok := input.Rules.Named(chosen)
		if !ok {
			return "", rules.Flow{}, nil, fmt.Errorf("workflow rule %q is not defined for this repository", chosen)
		}
		if strings.EqualFold(chosen, matched) {
			return matched, named, nil, nil
		}
		name := canonicalRuleName(input.Rules, chosen)
		return name, named, []string{fmt.Sprintf("workflow %q chosen by the user; the rules would have matched %q", name, matched)}, nil
	}
	if _, ok := input.Rules.LabelRule(input.Task.Labels); ok {
		return matched, flow, []string{fmt.Sprintf("workflow %q selected by a task label", matched)}, nil
	}
	return matched, flow, nil, nil
}

// canonicalRuleName returns the rule's own spelling of a name matched
// ignoring case.
func canonicalRuleName(document rules.Document, name string) string {
	for _, rule := range document.Rules {
		if strings.EqualFold(strings.TrimSpace(name), rule.Name) {
			return rule.Name
		}
	}
	return rules.DefaultName
}

// Edit is what the approval screen may change on an agent step.
type Edit struct {
	Agent   string
	Runtime domain.Runtime
	Score   int
	// Access is the step's permission, commands, and allowed paths as the
	// screen shows them; nil keeps the plan's.
	Access *Access
}

// Restaff rebuilds a planned agent step around an approval edit. The agent,
// runtime, complexity score, and access are the user's; the agent's skills
// and MCP servers, pinned runtime and model, turn budget, permission
// ceiling, and provenance are derived again exactly as at planning, so
// approval cannot give a step anything planning would refuse. Routing runs
// afterwards, as for a fresh plan. A non-agent step is left alone. servers
// is the set of MCP servers inkuna.yaml declares, and kinds the tracker
// kinds the project allowed when the plan was made
// (WorkflowSpec.TrackerKinds).
func Restaff(step *domain.WorkflowStep, edit Edit, registry agent.Registry, available, servers map[string]bool, kinds []domain.TrackerKind) error {
	if step.Kind != domain.StepAgent {
		return nil
	}
	if edit.Agent == "" || edit.Agent == agent.Auto {
		return fmt.Errorf("agent step %q has not been staffed", step.ID)
	}
	definition, ok := registry.Get(edit.Agent)
	if !ok || edit.Agent == agent.Orchestrator {
		return fmt.Errorf("agent %q for step %q is not defined", edit.Agent, step.ID)
	}
	if edit.Score < 1 || edit.Score > 10 {
		return fmt.Errorf("step %q needs a complexity score from 1 to 10", step.ID)
	}
	if step.Complexity == nil {
		return fmt.Errorf("agent step %q was planned without a complexity assessment", step.ID)
	}
	if edit.Access != nil {
		if err := applyAccess(step, *edit.Access, definition); err != nil {
			return err
		}
	}
	rationale := ""
	if step.Agent != definition.Name {
		// The planned agent's own skills and turn budget leave with it; the
		// stage's skills stay and the new agent's are merged below.
		if previous, ok := registry.Get(step.Agent); ok {
			step.SelectedSkills = exclude(step.SelectedSkills, previous.Skills)
		}
		step.Budget.MaxTurns = 0
		rationale = fmt.Sprintf("chosen at approval; the plan assigned %s", step.Agent)
	}
	complexity := *step.Complexity
	if complexity.Score != edit.Score {
		complexity.Rationale = fmt.Sprintf("set to %d at approval; the plan scored %d: %s", edit.Score, complexity.Score, complexity.Rationale)
		complexity.Score = edit.Score
	}
	step.Complexity = &complexity
	step.Runtime = edit.Runtime
	if step.Runtime == domain.RuntimeRemote && !definition.Remote() {
		step.Runtime = "" // routing falls back to the workflow's default runtime
	}
	step.ModelSelection = nil
	// The grant is the stage's servers and the agent's, derived again, and
	// so are its tracker kinds.
	step.MCPServers = domain.StageServers(*step)
	if _, err := applyAgent(step, definition, available, servers, rationale); err != nil {
		return err
	}
	_, err := grantTrackerKinds(step, definition, kinds)
	return err
}

// grantTrackerKinds gives an agent step the kinds of tracker change its
// stage and its agent ask for, as far as the project allows them, and notes
// those it asked for that the project does not allow: a step gets only what
// the project allows, as it gets only the permission its stage allows. A
// remote advisor is given none, and cannot staff a stage that asks for
// kinds the project allows.
func grantTrackerKinds(step *domain.WorkflowStep, definition agent.Definition, allowed []domain.TrackerKind) ([]string, error) {
	stage := domain.StageTrackerKinds(*step)
	if definition.Remote() {
		step.TrackerKinds = nil
		for _, kind := range stage {
			if slices.Contains(allowed, kind) {
				return nil, fmt.Errorf("remote agent %q is an advisor on another machine and cannot be given stage %q, which asks for tracker changes", definition.Name, step.ID)
			}
		}
		return nil, nil
	}
	var granted, refused []domain.TrackerKind
	for _, kind := range domain.SortedTrackerKinds(append(stage, definition.Tracker...)) {
		if slices.Contains(allowed, kind) {
			granted = append(granted, kind)
		} else {
			refused = append(refused, kind)
		}
	}
	step.TrackerKinds = granted
	if len(refused) == 0 {
		return nil, nil
	}
	return []string{fmt.Sprintf("Step %q asks for %s tracker changes, which the repository's tracker settings do not allow here, so it may not ask for them.", step.ID, domain.JoinTrackerKinds(refused, ", "))}, nil
}

// CheckTrackerGates refuses a plan with a step that may ask for tracker
// changes and no review gate after it: its changes wait for a person's
// approval at such a gate, where they are listed beside Approve, and
// without one nobody would see them.
func CheckTrackerGates(workflow domain.WorkflowSpec) error {
	dependents := make(map[string][]string, len(workflow.Steps))
	for _, step := range workflow.Steps {
		for _, dependency := range step.DependsOn {
			dependents[dependency] = append(dependents[dependency], step.ID)
		}
	}
	gates := make(map[string]bool)
	for _, step := range workflow.Steps {
		if step.Kind == domain.StepApproval && step.Review {
			gates[step.ID] = true
		}
	}
	for _, step := range workflow.Steps {
		if len(step.TrackerKinds) == 0 {
			continue
		}
		reached, gated := map[string]bool{}, false
		for queue := dependents[step.ID]; len(queue) > 0 && !gated; {
			next := queue[0]
			queue = queue[1:]
			if reached[next] {
				continue
			}
			reached[next], gated = true, gates[next]
			queue = append(queue, dependents[next]...)
		}
		if !gated {
			return fmt.Errorf("step %q may ask for %s tracker changes, but no review gate follows it to show them before they are made; add an approval stage with review: true after it, or take tracker off the stage and its agent", step.ID, domain.JoinTrackerKinds(step.TrackerKinds, ", "))
		}
	}
	return nil
}

func exclude(values, removed []string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if !contains(removed, value) {
			kept = append(kept, value)
		}
	}
	return kept
}

// CheckRuntimes refuses a plan that names a runtime the configuration does
// not declare, or gives a read-only runtime a step that writes. Agents, rules,
// and the project default only carry a name, so this is where it is resolved;
// approval may change a step's runtime, so it is checked again at start.
func CheckRuntimes(workflow domain.WorkflowSpec, runtimes map[domain.Runtime]bool) error {
	if runtimes == nil {
		runtimes = map[domain.Runtime]bool{domain.RuntimeCodex: true, domain.RuntimeClaude: true, domain.RuntimeCopilot: true}
	}
	for _, step := range workflow.Steps {
		if step.Kind != domain.StepAgent {
			continue
		}
		if step.Runtime == domain.RuntimeRemote {
			// No CLI runs. Whether the step's agent really is remote is checked
			// where the registry is known; here only the permission can be.
			if step.Permission == domain.PermissionWorktree {
				return fmt.Errorf("step %q writes to the worktree, which a remote agent cannot do", step.ID)
			}
			continue
		}
		canWrite, declared := runtimes[step.Runtime]
		if !declared {
			return fmt.Errorf("step %q uses runtime %q, which is not declared in inkuna.yaml", step.ID, step.Runtime)
		}
		if step.Permission == domain.PermissionWorktree && !canWrite {
			return fmt.Errorf("step %q writes to the worktree, but runtime %q declares no write arguments and is read-only", step.ID, step.Runtime)
		}
	}
	return nil
}

// DropUnsupportedServers leaves an agent's MCP servers off a step whose
// runtime cannot be given them, as an argument template cannot, and says
// so; the agent may serve machines whose runtimes differ. A stage that names
// servers on such a runtime is refused, since the repository asked for them.
// Run it after routing, which settles each step's runtime. mcpRuntimes nil
// means the three built-in runtimes.
func DropUnsupportedServers(workflow *domain.WorkflowSpec, mcpRuntimes map[domain.Runtime]bool) ([]string, error) {
	var notes []string
	for index := range workflow.Steps {
		step := &workflow.Steps[index]
		if len(step.MCPServers) == 0 || step.Kind != domain.StepAgent || step.Runtime == domain.RuntimeRemote || takesServers(mcpRuntimes, step.Runtime) {
			continue
		}
		if len(domain.StageServers(*step)) > 0 {
			return nil, fmt.Errorf("stage %q names MCP servers, but runs on runtime %q, an argument template, which cannot be given them; choose a built-in runtime", step.ID, step.Runtime)
		}
		notes = append(notes, fmt.Sprintf("Step %q runs on runtime %q, which cannot be given MCP servers, so it runs without %s, which its agent names.", step.ID, step.Runtime, strings.Join(step.MCPServers, ", ")))
		step.MCPServers = nil
	}
	return notes, nil
}

// CheckServers refuses a plan that grants a step an MCP server inkuna.yaml
// does not declare, or grants one to a step whose runtime cannot be given
// it. The declaration may change after planning, and approval may change a
// step's runtime, so it is checked again at start. mcpRuntimes nil means the
// three built-in runtimes.
func CheckServers(workflow domain.WorkflowSpec, mcpRuntimes map[domain.Runtime]bool, declared map[string]bool) error {
	for _, step := range workflow.Steps {
		if len(step.MCPServers) == 0 {
			continue
		}
		if step.Kind != domain.StepAgent || step.Runtime == domain.RuntimeRemote {
			return fmt.Errorf("step %q is granted MCP servers, which only a local agent step is given", step.ID)
		}
		if !takesServers(mcpRuntimes, step.Runtime) {
			return fmt.Errorf("step %q is granted MCP servers, but runtime %q is an argument template, which cannot be given them; choose a built-in runtime", step.ID, step.Runtime)
		}
		for _, server := range step.MCPServers {
			if !declared[server] {
				return fmt.Errorf("step %q is granted MCP server %q, which inkuna.yaml no longer declares; plan the task again", step.ID, server)
			}
		}
	}
	return nil
}

// takesServers reports whether a runtime can be given MCP servers.
func takesServers(mcpRuntimes map[domain.Runtime]bool, runtime domain.Runtime) bool {
	if mcpRuntimes == nil {
		return runtime == domain.RuntimeCodex || runtime == domain.RuntimeClaude || runtime == domain.RuntimeCopilot
	}
	return mcpRuntimes[runtime]
}

// staffing is how the auto stages were staffed: the decision, whether it was
// the orchestrator's, the notes for the plan, the call made, if any, and when
// a reused decision was made.
type staffing struct {
	decision     orchestrator.Decision
	orchestrated bool
	notes        []string
	call         *orchestrator.Call
	reused       time.Time
}

// policy is the routing policy the plan is made under.
func policy(input Input) routing.Policy {
	if input.Routing.Version == 0 {
		return routing.DefaultPolicy()
	}
	return input.Routing
}

func staff(ctx context.Context, input Input, stages []orchestrator.StageBrief, available map[string]bool, policy routing.Policy) staffing {
	staffed := staffing{notes: make([]string, 0)}
	fallback := func(reason string) staffing {
		decision, err := orchestrator.Fallback(input.Task, stages, input.Registry, available)
		if err != nil {
			staffed.notes = append(staffed.notes, "fallback failed: "+err.Error())
			return staffed
		}
		staffed.decision = decision
		if reason != "" {
			staffed.notes = append(staffed.notes, reason)
		}
		return staffed
	}
	if input.Disabled || input.Orchestrator == nil {
		// With every agent named by the rule nothing was staffed, and each
		// step's score says it is deterministic.
		if !slices.ContainsFunc(stages, func(stage orchestrator.StageBrief) bool { return stage.Agent == "" }) {
			return fallback("")
		}
		return fallback("orchestrator disabled; deterministic staffing used")
	}
	candidates := input.Registry.ForKind(input.Task.Kind)
	request := orchestrator.Request{
		Task: orchestrator.NewTaskBrief(input.Task), Repository: orchestrator.NewRepositoryBrief(input.Repository), Runtime: input.DefaultRuntime, Stages: stages,
		Agents: make([]orchestrator.AgentBrief, 0, len(candidates)), Skills: offeredSkills(input.Task.Kind, stages, candidates, input.Skills),
	}
	for _, definition := range candidates {
		request.Agents = append(request.Agents, orchestrator.NewAgentBrief(definition))
	}
	request.Routing = routingBrief(policy, request)
	options := input.Options
	if options.Instructions == "" {
		if definition, ok := input.Registry.Get(agent.Orchestrator); ok {
			options.Instructions = definition.Instructions
		}
	}
	key := ""
	if input.Cache != nil && !options.ReadTools {
		var err error
		if key, err = orchestrator.Key(request, options); err != nil {
			staffed.notes = append(staffed.notes, "earlier staffing decisions could not be looked up: "+err.Error())
		}
	}
	if key != "" && !input.BypassCache {
		stored, decidedAt, found, err := input.Cache.Recall(ctx, key)
		if err != nil {
			staffed.notes = append(staffed.notes, "an earlier staffing decision could not be read: "+err.Error())
		}
		if found {
			// A stored decision is checked again, as every decision is; one Go
			// no longer accepts is asked for again instead.
			if decision, err := orchestrator.Validate(stored, stages, input.Registry, available); err == nil {
				staffed.reused = decidedAt
				return staffed.orchestratedBy(decision)
			}
		}
	}
	raw, call, err := input.Orchestrator.Decide(ctx, request, options, input.Sink)
	if call.Outcome != "" {
		staffed.call = &call
	}
	if err != nil {
		return fallback("orchestrator failed: " + err.Error())
	}
	decision, err := orchestrator.Validate(raw, stages, input.Registry, available)
	if err != nil {
		return fallback("orchestrator decision rejected: " + err.Error())
	}
	if key != "" {
		if err := input.Cache.Remember(ctx, key, decision); err != nil {
			staffed.notes = append(staffed.notes, "the staffing decision could not be kept for an identical plan: "+err.Error())
		}
	}
	return staffed.orchestratedBy(decision)
}

// routingBrief is the policy's bands for each runtime a stage may run on:
// the plan's, one a stage names, and one a candidate agent pins.
func routingBrief(policy routing.Policy, request orchestrator.Request) map[domain.Runtime][]routing.Band {
	runtimes := []domain.Runtime{request.Runtime}
	for _, stage := range request.Stages {
		runtimes = append(runtimes, stage.Runtime)
	}
	for _, candidate := range request.Agents {
		runtimes = append(runtimes, candidate.Runtime)
	}
	brief := make(map[domain.Runtime][]routing.Band)
	for _, runtimeName := range runtimes {
		if bands, ok := policy.Bands[runtimeName]; ok && runtimeName != "" {
			brief[runtimeName] = bands
		}
	}
	if len(brief) == 0 {
		return nil
	}
	return brief
}

// orchestratedBy records a validated decision of the orchestrator's.
func (staffed staffing) orchestratedBy(decision orchestrator.Decision) staffing {
	staffed.decision, staffed.orchestrated = decision, true
	if decision.Notes != "" {
		staffed.notes = append(staffed.notes, "orchestrator: "+decision.Notes)
	}
	return staffed
}

// offeredSkills lists, by id and description, the skills the orchestrator may
// choose from: those whose taskKinds include the task's, and those a stage or
// a candidate agent names, whose descriptions say what the stage needs or the
// agent knows. Any other skill is left out of the request.
func offeredSkills(kind domain.TaskKind, stages []orchestrator.StageBrief, candidates []agent.Definition, packages []skill.Package) []orchestrator.SkillBrief {
	offered := make(map[string]bool)
	for _, id := range skill.Route(kind, packages) {
		offered[id] = true
	}
	for _, stage := range stages {
		for _, id := range stage.Skills {
			offered[id] = true
		}
	}
	for _, definition := range candidates {
		for _, id := range definition.Skills {
			offered[id] = true
		}
	}
	briefs := make([]orchestrator.SkillBrief, 0, len(offered))
	for _, pkg := range packages {
		if offered[pkg.ID] {
			briefs = append(briefs, orchestrator.SkillBrief{ID: pkg.ID, Description: pkg.Description})
		}
	}
	sort.Slice(briefs, func(i, j int) bool { return briefs[i].ID < briefs[j].ID })
	return briefs
}

// applyAgent copies an agent's runtime, model, permission ceiling, skills,
// and MCP servers onto a step. Rule stages may narrow but never widen the
// agent's permission. An agent's server that inkuna.yaml does not declare
// is left out, and the returned notes say so.
func applyAgent(step *domain.WorkflowStep, definition agent.Definition, available, servers map[string]bool, rationale string) ([]string, error) {
	step.Agent = definition.Name
	if definition.Remote() && step.Permission == domain.PermissionWorktree {
		return nil, fmt.Errorf("remote agent %q is a read-only advisor and cannot be given stage %q, which writes to the worktree", definition.Name, step.ID)
	}
	if definition.Remote() && len(step.MCPServers) > 0 {
		return nil, fmt.Errorf("remote agent %q is an advisor on another machine and cannot be given stage %q's MCP servers", definition.Name, step.ID)
	}
	if step.Permission == domain.PermissionWorktree && definition.Permission != domain.PermissionWorktree {
		return nil, fmt.Errorf("agent %q is read-only but stage %q requires worktree access", definition.Name, step.ID)
	}
	switch {
	case definition.Remote():
		// Rule-selected skills are local instructions; none of them are sent.
		step.Runtime, step.SelectedSkills = domain.RuntimeRemote, nil
	case step.Runtime == domain.RuntimeRemote:
		return nil, fmt.Errorf("stage %q names the remote runtime, which only a remote agent uses; %q runs locally", step.ID, definition.Name)
	case definition.Runtime != "":
		step.Runtime = definition.Runtime
	}
	seen := make(map[string]bool)
	merged := make([]string, 0, len(step.SelectedSkills)+len(definition.Skills))
	for _, id := range append(append([]string(nil), step.SelectedSkills...), definition.Skills...) {
		if !available[id] {
			if strings.TrimSpace(id) != "" && contains(definition.Skills, id) {
				continue // agent-declared skill absent from this repository
			}
			return nil, fmt.Errorf("stage %q selects unavailable skill %q", step.ID, id)
		}
		if !seen[id] {
			seen[id] = true
			merged = append(merged, id)
		}
	}
	sort.Strings(merged)
	step.SelectedSkills = merged
	var notes []string
	granted := append([]string(nil), step.MCPServers...)
	for _, server := range definition.MCP {
		if !servers[server] {
			notes = append(notes, fmt.Sprintf("Agent %s names MCP server %q, which inkuna.yaml does not declare, so step %q runs without it.", definition.Name, server, step.ID))
			continue
		}
		granted = append(granted, server)
	}
	step.MCPServers = domain.SortedServerNames(granted)
	if definition.MaxTurns > 0 && step.Budget.MaxTurns == 0 {
		step.Budget.MaxTurns = definition.MaxTurns
	}
	if step.Metadata == nil {
		step.Metadata = map[string]string{}
	}
	step.Metadata["agentSource"] = string(definition.Source)
	if rationale != "" {
		step.Metadata["assignment"] = rationale
	}
	if definition.Model != "" {
		step.ModelSelection = &domain.ModelSelection{Model: definition.Model, ReasoningEffort: definition.ReasoningEffort, Source: "override", Rationale: "agent " + definition.Name + " pins its model"}
	}
	return notes, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
