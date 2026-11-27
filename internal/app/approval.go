package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/planner"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/skill"
)

// approvalRevision is a planned workflow rebuilt around the user's edits,
// with the registry and skills it was derived from.
type approvalRevision struct {
	spec     domain.WorkflowSpec
	task     domain.TaskSpec
	registry agent.Registry
	packages []skill.Package
}

// ReviseWorkflow rebuilds a planned workflow around the approval screen's
// edits: on each agent step the agent, the runtime, the complexity score,
// the permission, the commands its worker may run without asking, and its
// allowed paths, and on each evaluation step its commands. Go checks each
// edit as it would a rule's and derives everything that follows (the
// agent's skills, pinned runtime or model, turn budget, permission checks,
// model routing, and a note of each change to a step's access) exactly as
// planning did, so the screen shows what would run and StartWorkflow can
// hold the approval to it. Nothing is persisted.
func (service *Service) ReviseWorkflow(workflowJSON string) (domain.WorkflowSpec, error) {
	if err := service.ready(); err != nil {
		return domain.WorkflowSpec{}, err
	}
	var edited domain.WorkflowSpec
	if err := json.Unmarshal([]byte(workflowJSON), &edited); err != nil {
		return domain.WorkflowSpec{}, fmt.Errorf("decode workflow: %w", err)
	}
	revision, err := service.reviseApproval(service.context(), edited)
	if err != nil {
		return domain.WorkflowSpec{}, err
	}
	return revision.spec, nil
}

// reviseApproval loads the persisted plan behind an edited workflow and
// returns it rebuilt for those edits.
func (service *Service) reviseApproval(ctx context.Context, edited domain.WorkflowSpec) (approvalRevision, error) {
	planned, err := service.store.Workflow(ctx, edited.ID)
	if err != nil {
		return approvalRevision{}, fmt.Errorf("load planned workflow: %w", err)
	}
	// Only a first approval is held to the definitions as they are now. A
	// rerun starts from a plan already approved, which keeps the expansion
	// it was approved with.
	if err := service.checkStagesUnchanged(ctx, planned); err != nil {
		return approvalRevision{}, err
	}
	return service.reviseFrom(ctx, planned, edited)
}

// stageDigests digests each shared stage and group the named rule of
// document uses, through its groups, as it resolves now.
func stageDigests(document rules.Document, ruleName string) (map[string]string, error) {
	used, err := document.Uses(ruleName)
	if err != nil {
		return nil, fmt.Errorf("rule %q: %w", ruleName, err)
	}
	if len(used) == 0 {
		return nil, nil
	}
	digests := make(map[string]string, len(used))
	for _, definition := range used {
		digests[definition.Name] = definition.Digest()
	}
	return digests, nil
}

// checkStagesUnchanged refuses a plan whose rule used a shared stage or
// group that has changed since the plan was made: edited, removed, or now
// resolved to another definition, as a global or repository one added
// since overrides it. The plan shows the stages as they were expanded then,
// and approving it would approve a definition nobody has seen as it is now.
func (service *Service) checkStagesUnchanged(ctx context.Context, planned domain.WorkflowSpec) error {
	if len(planned.StageDigests) == 0 {
		return nil
	}
	project, err := service.store.ProjectForTask(ctx, planned.TaskID)
	if err != nil {
		return fmt.Errorf("load workflow project: %w", err)
	}
	document, _, path, err := service.loadRules(project)
	if err != nil {
		return fmt.Errorf("%w; fix it and plan the task again", err)
	}
	current := make(map[string]rules.Definition)
	if used, err := document.Uses(planned.RuleName); err == nil {
		for _, definition := range used {
			current[definition.Name] = definition
		}
	}
	names := make([]string, 0, len(planned.StageDigests))
	for name := range planned.StageDigests {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		definition, ok := current[name]
		if !ok {
			return fmt.Errorf("rule %q no longer uses %q, which the plan expanded, as %s defines it now; plan the task again", planned.RuleName, name, path)
		}
		if definition.Digest() != planned.StageDigests[name] {
			return fmt.Errorf("%s changed after the plan was made, so the plan no longer shows what it would run; plan the task again", describeDefinition(definition, path))
		}
	}
	return nil
}

// describeDefinition names a definition and where it now lives, for a
// refused approval.
func describeDefinition(definition rules.Definition, rulesPath string) string {
	kind := "shared stage"
	if definition.Group != nil {
		kind = "group"
	}
	switch definition.Scope {
	case rules.ScopeBuiltin:
		return fmt.Sprintf("built-in %s %q", kind, definition.Name)
	case rules.ScopeGlobal:
		return fmt.Sprintf("%s %q in %s", kind, definition.Name, definition.File)
	}
	return fmt.Sprintf("%s %q in %s", kind, definition.Name, rulesPath)
}

// reviseFrom returns planned rebuilt for the edits in edited, checked as an
// approval is: planned is the persisted plan for a first approval, and the
// plan a run ran under for a rerun of it.
func (service *Service) reviseFrom(ctx context.Context, planned, edited domain.WorkflowSpec) (approvalRevision, error) {
	if err := validateApprovalChanges(planned, edited); err != nil {
		return approvalRevision{}, err
	}
	task, err := service.store.Task(ctx, planned.TaskID)
	if err != nil {
		return approvalRevision{}, fmt.Errorf("load workflow task: %w", err)
	}
	registry, _, err := service.agentRegistry(task.Repository)
	if err != nil {
		return approvalRevision{}, fmt.Errorf("load agents: %w", err)
	}
	packages, err := skill.Discover(filepath.Join(task.Repository, ".inkuna", "skills"))
	if err != nil {
		return approvalRevision{}, fmt.Errorf("discover skills: %w", err)
	}
	available := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		available[pkg.ID] = true
	}
	policy := service.routingPolicy()
	if policy.Version != planned.RoutingPolicyVersion {
		return approvalRevision{}, fmt.Errorf("the routing policy changed since planning (now v%d, planned with v%d); plan the task again", policy.Version, planned.RoutingPolicyVersion)
	}
	servers := service.config.File.MCPServerNames()
	revised := planned
	revised.Steps = make([]domain.WorkflowStep, len(planned.Steps))
	for index, step := range planned.Steps {
		revised.Steps[index] = cloneStep(step)
		shown := edited.Steps[index]
		switch step.Kind {
		case domain.StepEvaluation:
			if err := planner.EditCommands(&revised.Steps[index], shown.Commands); err != nil {
				return approvalRevision{}, err
			}
		case domain.StepAgent:
			edit := planner.Edit{
				Agent: shown.Agent, Runtime: shown.Runtime,
				Access: &planner.Access{Permission: shown.Permission, Commands: shown.Commands, AllowedPaths: shown.AllowedPaths},
			}
			if shown.Complexity != nil {
				edit.Score = shown.Complexity.Score
			}
			if err := planner.Restaff(&revised.Steps[index], edit, registry, available, servers, planned.TrackerKinds); err != nil {
				return approvalRevision{}, err
			}
		}
	}
	if err := checkApprovedWrites(planned, revised); err != nil {
		return approvalRevision{}, err
	}
	if err := policy.Apply(&revised); err != nil {
		return approvalRevision{}, err
	}
	if err := planner.CheckRuntimes(revised, service.declaredRuntimes()); err != nil {
		return approvalRevision{}, err
	}
	mcpRuntimes := service.mcpRuntimes()
	// Planning noted an agent's servers left off a template runtime; the
	// plan shows that, so here they are only left off again.
	if _, err := planner.DropUnsupportedServers(&revised, mcpRuntimes); err != nil {
		return approvalRevision{}, err
	}
	if err := planner.CheckServers(revised, mcpRuntimes, servers); err != nil {
		return approvalRevision{}, err
	}
	if err := checkServersUnchanged(revised, service.config.File); err != nil {
		return approvalRevision{}, err
	}
	if err := planner.CheckTrackerGates(revised); err != nil {
		return approvalRevision{}, err
	}
	// A changed runtime takes its own servers of the user's, as the plan
	// recorded them, and the plan must still be what userMcp says.
	applyUserServers(&revised, planned.UserMCP)
	if err := checkUserServersUnchanged(revised, service.userMCPByRuntime()); err != nil {
		return approvalRevision{}, err
	}
	// The checks above give each refusal its reason; this one holds the
	// rebuilt plan to the contract every plan meets.
	if err := domain.ValidateWorkflow(revised); err != nil {
		return approvalRevision{}, err
	}
	return approvalRevision{spec: revised, task: task, registry: registry, packages: packages}, nil
}

// checkApprovedWrites refuses an approval that changes which steps write in
// a way the plan cannot carry: a step that feedback is sent to made
// read-only, since feedback only goes to a step that can act on it, and a
// step made writable in a plan where none was, which named no branch for
// the run to write on when worktrees are on.
func checkApprovedWrites(planned, revised domain.WorkflowSpec) error {
	writable := make(map[string]bool, len(revised.Steps))
	for _, step := range revised.Steps {
		writable[step.ID] = step.Kind == domain.StepAgent && step.Permission == domain.PermissionWorktree
	}
	for _, step := range revised.Steps {
		if step.FeedbackTarget != "" && !writable[step.FeedbackTarget] {
			return fmt.Errorf("step %q must stay worktree-write: %q sends its feedback to it", step.FeedbackTarget, step.ID)
		}
	}
	plan := planned.Branching
	if hasWritableStep(planned) || (plan != nil && (!plan.Worktree || plan.Branch != "")) {
		return nil
	}
	for _, step := range revised.Steps {
		if writable[step.ID] {
			return fmt.Errorf("step %q cannot be made worktree-write here: no step of this plan writes, so it names no branch for the run to write on; choose a workflow rule with a writable stage instead", step.ID)
		}
	}
	return nil
}

// mcpServerDigests digests each MCP server inkuna.yaml declares
// (mcpServerDigest).
func mcpServerDigests(file config.File) map[string]string {
	if len(file.MCPServers) == 0 {
		return nil
	}
	digests := make(map[string]string, len(file.MCPServers))
	for _, entry := range file.MCPServers {
		digests[entry.Name] = mcpServerDigest(entry)
	}
	return digests
}

// mcpServerDigest digests a declared server: what the approval screen shows
// of it, and which secrets it is given, by name, since pointing a variable
// or the bearer token at another secret changes what the server can do. A
// secret's value is left out, so a rotated credential keeps the digest.
func mcpServerDigest(entry config.MCPServerConfig) string {
	// A string and a map of strings always encode, the map in key order.
	data, _ := json.Marshal(struct {
		Shown  string            `json:"shown"`
		Env    map[string]string `json:"env,omitempty"`
		Bearer string            `json:"bearer,omitempty"`
	}{entry.Brief().Digest(), entry.Env, entry.Bearer})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// checkServersUnchanged refuses a plan with a step granted an MCP server the
// approval screen did not show as it is now: one inkuna.yaml changed after
// planning, under the same name or not, or declared only since. The run
// would start the server as it is now, not as it was approved.
func checkServersUnchanged(spec domain.WorkflowSpec, file config.File) error {
	for _, step := range spec.Steps {
		for _, name := range step.MCPServers {
			entry, declared := file.MCPServer(name)
			if !declared {
				continue // CheckServers has refused it.
			}
			if shown, ok := spec.MCPServerDigests[name]; !ok || shown != mcpServerDigest(entry) {
				return fmt.Errorf("MCP server %q changed in inkuna.yaml after the plan was made, so step %q would not be given the server the plan shows; plan the task again", name, step.ID)
			}
		}
	}
	return nil
}

// checkApprovalCurrent refuses an approval whose agent and evaluation steps
// no longer show what Go derives for their edits: the view is stale because
// an agent, a skill, or a routing band changed after the plan was shown, or
// it was never revised for an access edit Go settles itself, such as the
// paths a step made read-only gives up.
func checkApprovalCurrent(revised, approved domain.WorkflowSpec) error {
	for index := range revised.Steps {
		want, got := revised.Steps[index], approved.Steps[index]
		switch want.Kind {
		case domain.StepEvaluation:
			if !sameCommands(want.Commands, got.Commands) {
				return fmt.Errorf("step %q: the approved commands are out of date; review the plan again", want.ID)
			}
			continue
		case domain.StepAgent:
		default:
			continue
		}
		if want.Agent != got.Agent || want.Runtime != got.Runtime || modelOf(want) != modelOf(got) ||
			scoreOf(want) != scoreOf(got) || !sameStrings(want.SelectedSkills, got.SelectedSkills) || !sameStrings(want.MCPServers, got.MCPServers) ||
			!sameStrings(want.UserMCP, got.UserMCP) || domain.JoinTrackerKinds(want.TrackerKinds, ",") != domain.JoinTrackerKinds(got.TrackerKinds, ",") {
			return fmt.Errorf("step %q: the approved staffing is out of date for its agent, runtime, and complexity; review the plan again", want.ID)
		}
		if want.Permission != got.Permission || !sameCommands(want.Commands, got.Commands) || !sameStrings(want.AllowedPaths, got.AllowedPaths) {
			return fmt.Errorf("step %q: the approved access is out of date for its permission, commands, and allowed paths; review the plan again", want.ID)
		}
	}
	return nil
}

func sameCommands(left, right [][]string) bool {
	return slices.EqualFunc(left, right, sameStrings)
}

func modelOf(step domain.WorkflowStep) string {
	if step.ModelSelection == nil {
		return ""
	}
	return step.ModelSelection.Model + "/" + step.ModelSelection.ReasoningEffort
}

func scoreOf(step domain.WorkflowStep) int {
	if step.Complexity == nil {
		return 0
	}
	return step.Complexity.Score
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// cloneStep copies a step so rebuilding it cannot touch the persisted plan.
func cloneStep(step domain.WorkflowStep) domain.WorkflowStep {
	step.DependsOn = append([]string(nil), step.DependsOn...)
	step.SelectedSkills = append([]string(nil), step.SelectedSkills...)
	step.MCPServers = append([]string(nil), step.MCPServers...)
	step.UserMCP = append([]string(nil), step.UserMCP...)
	step.AllowedPaths = append([]string(nil), step.AllowedPaths...)
	step.ExpectedOutputs = append([]string(nil), step.ExpectedOutputs...)
	step.SystemExitCodes = append([]int(nil), step.SystemExitCodes...)
	if step.Commands != nil {
		commands := make([][]string, len(step.Commands))
		for index, command := range step.Commands {
			commands[index] = append([]string(nil), command...)
		}
		step.Commands = commands
	}
	if step.Complexity != nil {
		complexity := *step.Complexity
		complexity.Signals = append([]string(nil), complexity.Signals...)
		step.Complexity = &complexity
	}
	if step.ModelSelection != nil {
		selection := *step.ModelSelection
		step.ModelSelection = &selection
	}
	step.AccessEdits = append([]domain.AccessEdit(nil), step.AccessEdits...)
	step.From = step.From.Clone()
	if step.Metadata != nil {
		metadata := make(map[string]string, len(step.Metadata))
		for key, value := range step.Metadata {
			metadata[key] = value
		}
		step.Metadata = metadata
	}
	return step
}
