package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// accessTestPlan persists a plan with a read-only step, a writable one whose
// writes are kept to internal/, and a check after them that sends its
// failures back to the writable step, in a repository a writable step can
// make a worktree of.
func accessTestPlan(t *testing.T) (*Service, *workflowRuntime, domain.WorkflowSpec) {
	t.Helper()
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	runtime := &workflowRuntime{}
	service := workflowTestService(t, repositoryPath, runtime)
	task := domain.TaskSpec{
		Version: domain.ContractVersion, ID: "task", Kind: domain.TaskBugfix, Objective: "Fix the crash",
		Source: domain.TaskSource{Kind: "request"}, Repository: ".", Priority: 1, Budget: domain.Budget{WallTimeSeconds: 60},
	}
	if err := service.store.SaveTask(context.Background(), "project", "planned", task); err != nil {
		t.Fatal(err)
	}
	agentStep := func(id, agentName string, permission domain.Permission) domain.WorkflowStep {
		return domain.WorkflowStep{
			ID: id, Title: id, Kind: domain.StepAgent, Runtime: domain.RuntimeCodex, Permission: permission, Agent: agentName,
			Complexity:     &domain.ComplexityAssessment{Score: 4, Rationale: "planned"},
			ModelSelection: &domain.ModelSelection{Model: "gpt-5", Source: "policy", Rationale: "test route"},
			Budget:         domain.Budget{WallTimeSeconds: 60, MaxOutputBytes: 1024},
		}
	}
	reproduce := agentStep("reproduce", "researcher", domain.PermissionReadOnly)
	reproduce.Commands = [][]string{{"go", "test", "./..."}}
	fix := agentStep("fix", "implementer", domain.PermissionWorktree)
	fix.DependsOn, fix.AllowedPaths = []string{"reproduce"}, []string{"internal/"}
	verify := domain.WorkflowStep{
		ID: "verify", Title: "verify", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, DependsOn: []string{"fix"},
		Commands: [][]string{passingCheck()}, OnFailure: domain.FailureFeedback, FeedbackTarget: "fix", MaxRounds: 1,
		Budget: domain.Budget{WallTimeSeconds: 60},
	}
	return service, runtime, savePlanned(t, service, domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeCodex,
		RoutingPolicyVersion: service.routingPolicy().Version, MaxConcurrency: 1, CreatedAt: time.Now().UTC(),
		Steps: []domain.WorkflowStep{reproduce, fix, verify},
	})
}

// passingCheck is a check command that passes: the test binary, running no
// tests.
func passingCheck() []string {
	return []string{os.Args[0], "-test.run=^$"}
}

// editStep returns the plan with one step changed, as the approval screen
// would send it.
func editStep(spec domain.WorkflowSpec, id string, change func(*domain.WorkflowStep)) domain.WorkflowSpec {
	edited := spec
	edited.Steps = make([]domain.WorkflowStep, len(spec.Steps))
	for index, step := range spec.Steps {
		edited.Steps[index] = cloneStep(step)
		if step.ID == id {
			change(&edited.Steps[index])
		}
	}
	return edited
}

func encodeSpec(t *testing.T, spec domain.WorkflowSpec) string {
	t.Helper()
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func stepOf(spec domain.WorkflowSpec, id string) domain.WorkflowStep {
	for _, step := range spec.Steps {
		if step.ID == id {
			return step
		}
	}
	return domain.WorkflowStep{}
}

func TestApprovalWidensAStepToWriteOnlyForAWritableAgent(t *testing.T) {
	service, runtime, planned := accessTestPlan(t)

	keepsResearcher := editStep(planned, "reproduce", func(step *domain.WorkflowStep) { step.Permission = domain.PermissionWorktree })
	if _, err := service.ReviseWorkflow(encodeSpec(t, keepsResearcher)); err == nil || !strings.Contains(err.Error(), `agent "researcher" is read-only`) {
		t.Fatalf("a read-only agent must not be put on a worktree-write step, got %v", err)
	}
	if _, err := service.StartWorkflow(encodeSpec(t, editStep(keepsResearcher, "reproduce", func(step *domain.WorkflowStep) { step.AllowedPaths = []string{"."} }))); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("approval must refuse it too, got %v", err)
	}

	writable := editStep(planned, "reproduce", func(step *domain.WorkflowStep) {
		step.Agent, step.Permission = "implementer", domain.PermissionWorktree
	})
	writable = editStep(writable, "verify", func(step *domain.WorkflowStep) {
		step.Commands = append(step.Commands, append(passingCheck(), "-test.count=1"))
	})
	revised, err := service.ReviseWorkflow(encodeSpec(t, writable))
	if err != nil {
		t.Fatal(err)
	}
	reproduce := stepOf(revised, "reproduce")
	if reproduce.Permission != domain.PermissionWorktree || !slices.Equal(reproduce.AllowedPaths, []string{"."}) {
		t.Fatalf("the step was not widened: %+v", reproduce)
	}
	if len(reproduce.AccessEdits) != 2 || reproduce.AccessEdits[0].Note != "Permission set to worktree-write at approval; the plan said read-only." || !reproduce.AccessEdits[0].Widens {
		t.Fatalf("the widening must be recorded on the step: %+v", reproduce.AccessEdits)
	}
	if verify := stepOf(revised, "verify"); len(verify.Commands) != 2 || len(verify.AccessEdits) != 1 || !verify.AccessEdits[0].Widens {
		t.Fatalf("the added check must be kept and recorded: %+v", verify)
	}
	stored, err := service.store.Workflow(context.Background(), planned.ID)
	if err != nil || stepOf(stored, "reproduce").Permission != domain.PermissionReadOnly {
		t.Fatalf("revising must not change the persisted plan: %+v %v", stepOf(stored, "reproduce"), err)
	}

	run := startSpec(t, service, revised)
	waitForWorkflowRun(t, service, run.ID, "completed")
	requests := started(runtime)
	if len(requests) != 2 || requests[0].Permission != domain.PermissionWorktree {
		t.Fatalf("the widened step must run with write access: %+v", requests)
	}
	checks, err := service.RunChecks(run.ID)
	if err != nil || len(checks) != 2 {
		t.Fatalf("the approved checks must run: %+v %v", checks, err)
	}
	approved, err := service.store.Workflow(context.Background(), planned.ID)
	if err != nil || len(stepOf(approved, "reproduce").AccessEdits) != 2 {
		t.Fatalf("the approved plan keeps the provenance: %+v %v", stepOf(approved, "reproduce"), err)
	}
}

func TestApprovalRefusesShellCommandsAndPathsOutsideTheRepository(t *testing.T) {
	service, _, planned := accessTestPlan(t)
	for says, edited := range map[string]domain.WorkflowSpec{
		"runs a shell": editStep(planned, "fix", func(step *domain.WorkflowStep) {
			step.Commands = [][]string{{"sh", "-c", "go test ./... && go vet ./..."}}
		}),
		"whole command line": editStep(planned, "verify", func(step *domain.WorkflowStep) {
			step.Commands = [][]string{{"task deps && task ci"}}
		}),
		"permission checks off": editStep(planned, "reproduce", func(step *domain.WorkflowStep) {
			step.Commands = [][]string{{"codex", "exec", "--yolo"}}
		}),
		"outside the repository": editStep(planned, "fix", func(step *domain.WorkflowStep) {
			step.AllowedPaths = []string{"internal/", "../elsewhere"}
		}),
	} {
		if _, err := service.ReviseWorkflow(encodeSpec(t, edited)); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("revising: got %v, want an error saying %q", err, says)
		}
		if _, err := service.StartWorkflow(encodeSpec(t, edited)); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("approving: got %v, want an error saying %q", err, says)
		}
	}
}

func TestAStaleAccessApprovalIsRefused(t *testing.T) {
	service, _, planned := accessTestPlan(t)
	narrowed := editStep(planned, "reproduce", func(step *domain.WorkflowStep) { step.Commands = nil })
	narrowed = editStep(narrowed, "fix", func(step *domain.WorkflowStep) { step.AllowedPaths = []string{" internal/app "} })
	if _, err := service.StartWorkflow(encodeSpec(t, narrowed)); err == nil || !strings.Contains(err.Error(), "approved access is out of date") {
		t.Fatalf("an approval that shows paths Go would not use must be refused, got %v", err)
	}
	revised, err := service.ReviseWorkflow(encodeSpec(t, narrowed))
	if err != nil {
		t.Fatal(err)
	}
	if fix := stepOf(revised, "fix"); !slices.Equal(fix.AllowedPaths, []string{"internal/app"}) || len(fix.AccessEdits) != 1 || fix.AccessEdits[0].Widens {
		t.Fatalf("a path inside the plan's narrows the step: %+v", fix)
	}
	if _, err := service.StartWorkflow(encodeSpec(t, revised)); err != nil {
		t.Fatalf("the revised approval must start: %v", err)
	}
}

func TestApprovalKeepsAFeedbackTargetWritable(t *testing.T) {
	service, _, planned := accessTestPlan(t)
	edited := editStep(planned, "fix", func(step *domain.WorkflowStep) { step.Permission = domain.PermissionReadOnly })
	if _, err := service.ReviseWorkflow(encodeSpec(t, edited)); err == nil || !strings.Contains(err.Error(), `step "fix" must stay worktree-write: "verify" sends its feedback to it`) {
		t.Fatalf("narrowing the step feedback goes to must be refused, got %v", err)
	}
}

func TestApprovalCannotMakeAStepWriteInAPlanThatNamesNoBranch(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	planned := workflowTestSpec(t, service)
	edited := editStep(planned, "worker", func(step *domain.WorkflowStep) {
		step.Agent, step.Permission = "implementer", domain.PermissionWorktree
	})
	if _, err := service.ReviseWorkflow(encodeSpec(t, edited)); err == nil || !strings.Contains(err.Error(), "names no branch") {
		t.Fatalf("a plan where nothing writes cannot gain a writable step, got %v", err)
	}
	// Worktrees off: the run would write in the checkout, which needs no
	// branch named.
	planned.Branching = &domain.BranchPlan{Worktree: false, Base: "main"}
	if err := service.store.SaveWorkflow(context.Background(), "planned", planned); err != nil {
		t.Fatal(err)
	}
	edited.Branching = planned.Branching
	if _, err := service.ReviseWorkflow(encodeSpec(t, edited)); err != nil {
		t.Fatalf("with worktrees off a step may be made writable: %v", err)
	}
}

func TestAnEvaluationStepChangesOnlyItsCommandsAtApproval(t *testing.T) {
	planned := domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeClaude,
		RoutingPolicyVersion: 1, MaxConcurrency: 1, CreatedAt: time.Now().UTC(),
		Steps: []domain.WorkflowStep{{
			ID: "verify", Title: "Verify", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly,
			Commands: [][]string{{"task", "ci"}}, Budget: domain.Budget{WallTimeSeconds: 60},
		}},
	}
	commands := editStep(planned, "verify", func(step *domain.WorkflowStep) {
		step.Commands = [][]string{{"task", "deps"}, {"task", "ci"}}
		step.AccessEdits = []domain.AccessEdit{{Field: domain.AccessCommands, Note: "shown by the view"}}
	})
	if err := validateApprovalChanges(planned, commands); err != nil {
		t.Fatalf("an evaluation step's commands may change: %v", err)
	}
	other := editStep(planned, "verify", func(step *domain.WorkflowStep) { step.ContinueOnFailure = true })
	if err := validateApprovalChanges(planned, other); err == nil || !strings.Contains(err.Error(), "only its commands") {
		t.Fatalf("anything else on an evaluation step must be refused, got %v", err)
	}
}
