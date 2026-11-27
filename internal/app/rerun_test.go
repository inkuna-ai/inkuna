package app

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// deniedOnce is a stop for stoppedBugfix: fix's first turn is refused
// go test ./... and fails, and every later run of fix writes and completes.
func deniedOnce(t *testing.T) func(context.Context, agentruntime.RunRequest) (agentruntime.RunResult, error) {
	var denied atomic.Bool
	return func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if denied.CompareAndSwap(false, true) {
			return agentruntime.RunResult{
				Output:  handoffJSON(t, domain.HandoffResult{Status: domain.HandoffFailed, Summary: "Could not run the tests"}),
				Denials: []domain.PermissionDenial{{Tool: "Bash", Command: "go test ./...", Detail: "go test ./..."}},
			}, nil
		}
		return writingRuntime().handle(ctx, request)
	}
}

// failedOnADeniedCommand runs the bugfix flow until fix is refused
// go test ./..., declines it in the inbox, and returns the failed run.
func failedOnADeniedCommand(t *testing.T) (*Service, *workflowRuntime, WorkflowRunView) {
	t.Helper()
	service, runtime, run := stoppedBugfix(t, deniedOnce(t))
	request := waitForInbox(t, service, 1)[0]
	if _, err := service.AnswerInput(request.ID, permissionDeny); err != nil {
		t.Fatal(err)
	}
	return service, runtime, settledRun(t, service, run.ID, "failed")
}

func encodeWorkflow(t *testing.T, spec domain.WorkflowSpec) string {
	t.Helper()
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// applySuggestion makes a suggestion's edit on the plan, as the approval
// screen does when the person accepts it.
func applySuggestion(spec domain.WorkflowSpec, suggestion PlanSuggestion) domain.WorkflowSpec {
	return editStep(spec, suggestion.StepID, func(step *domain.WorkflowStep) {
		if suggestion.Permission != "" {
			step.Permission = suggestion.Permission
		}
		if suggestion.Command != nil {
			step.Commands = append(step.Commands, suggestion.Command)
		}
	})
}

func TestARunThatFailedOnADeniedCommandResumesWithTheSuggestedFix(t *testing.T) {
	service, runtime, run := failedOnADeniedCommand(t)

	failure, err := service.RunFailure(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(failure.Steps) != 1 || failure.Steps[0].StepID != "fix" || failure.Steps[0].State != "failed" || failure.Steps[0].Error != "Could not run the tests" {
		t.Fatalf("the failing step and its error: %+v", failure.Steps)
	}
	denials := failure.Steps[0].Denials
	if len(denials) != 1 || denials[0].Command != "go test ./..." || !denials[0].Grantable || denials[0].Write {
		t.Fatalf("the command the step was refused, which the person declined: %+v", denials)
	}
	if len(failure.Suggestions) != 1 || failure.Suggestions[0].Text != "Allow go test ./... on step fix" || !slices.Equal(failure.Suggestions[0].Command, []string{"go", "test", "./..."}) {
		t.Fatalf("the suggested fix: %+v", failure.Suggestions)
	}

	rerun, err := service.PlanRerun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rerun.Resume.Refusal != "" || resumeStates(rerun.Resume.Rerun) != "fix=failed,verify=pending,approval=pending" || len(rerun.Plan.Candidates) == 0 {
		t.Fatalf("a rerun starts from the run's plan and can resume it: %+v", rerun.Resume)
	}
	revision, err := service.ReviseRerun(run.ID, encodeWorkflow(t, applySuggestion(rerun.Plan.Workflow, failure.Suggestions[0])))
	if err != nil {
		t.Fatal(err)
	}
	fix := stepOf(revision.Workflow, "fix")
	if !slices.Equal(revision.Edited, []string{"fix"}) || len(fix.Commands) == 0 || len(fix.AccessEdits) != 1 || !fix.AccessEdits[0].Widens {
		t.Fatalf("Go rebuilds the step with the command and notes the widening: %+v %+v", revision.Edited, fix)
	}
	if got := resumeStates(revision.Resume.Rerun); got != "fix=failed,verify=pending,approval=pending" {
		t.Fatalf("the resume under the edited plan runs %s", got)
	}

	before := len(startedRuns(runtime))
	resumed, err := service.RerunWorkflowRun(run.ID, RerunRequest{Round: 1, Workflow: encodeWorkflow(t, revision.Workflow)})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != run.ID || resumed.Round != 2 {
		t.Fatalf("an edited resume is a new round of the same run: %+v", resumed)
	}
	done := settledRun(t, service, run.ID, "completed")
	if done.Worktree != run.Worktree || done.Branch != run.Branch {
		t.Fatalf("the round ran on the run's worktree and branch: %+v", done)
	}
	ids, requests := agentRunsSince(runtime, before)
	if len(ids) != 1 || ids[0] != run.ID+".r2.fix" || !slices.ContainsFunc(requests[0].Commands, func(command []string) bool { return strings.Join(command, " ") == "go test ./..." }) {
		t.Fatalf("only fix ran again, allowed the command: %v %+v", ids, requests)
	}
	rounds, err := service.RunRounds(run.ID)
	if err != nil || len(rounds) != 2 || !rounds[1].Resumed || !slices.Equal(rounds[1].Edited, []string{"fix"}) {
		t.Fatalf("round 2 shows the edited resume: %+v %v", rounds, err)
	}
	ctx := context.Background()
	if first, err := service.store.RoundPlan(ctx, run.ID, 1); err != nil || len(stepOf(first, "fix").Commands) != len(stepOf(rerun.Plan.Workflow, "fix").Commands) {
		t.Fatalf("round 1 keeps the plan it ran: %+v %v", stepOf(first, "fix"), err)
	}
	if failure, err := service.RunFailure(run.ID); err != nil || failure.Stopped || len(failure.Steps) != 0 || len(failure.Suggestions) != 0 {
		t.Fatalf("a completed run has no failure: %+v %v", failure, err)
	}
}

func TestARunThatStoppedWithoutAFailedStepCanStillBeRerun(t *testing.T) {
	service, _, run := finishedBugfix(t)
	// Inkuna quit after the run's last step finished, before the run did.
	after := restarted(t, service)
	ctx := context.Background()
	execution, err := after.store.Execution(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	execution.State = domain.StoppedInterrupted
	if err := after.store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	failure, err := after.RunFailure(run.ID)
	if err != nil || !failure.Stopped || len(failure.Steps) != 0 {
		t.Fatalf("the run stopped though no step says why: %+v %v", failure, err)
	}
	if _, err := after.PlanRerun(run.ID); err != nil {
		t.Fatalf("it can be rerun: %v", err)
	}
}

func TestAnEditedStepThatCompletedRunsAgainWithWhatFollows(t *testing.T) {
	service, runtime, run := failedOnADeniedCommand(t)
	rerun, err := service.PlanRerun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	edited := editStep(rerun.Plan.Workflow, "reproduce", func(step *domain.WorkflowStep) {
		step.Commands = append(step.Commands, []string{"go", "test", "-run", "TestLogin", "./..."})
	})
	revision, err := service.ReviseRerun(run.ID, encodeWorkflow(t, edited))
	if err != nil {
		t.Fatal(err)
	}
	if got := resumeStates(revision.Resume.Rerun); got != "reproduce=edited,fix=failed,verify=pending,approval=pending" {
		t.Fatalf("the edited step runs again with what follows: %s", got)
	}
	if got := resumeStates(revision.Resume.Kept); got != "profile=completed" {
		t.Fatalf("the resume keeps %s", got)
	}
	before := len(startedRuns(runtime))
	if _, err := service.RerunWorkflowRun(run.ID, RerunRequest{Round: 1, Workflow: encodeWorkflow(t, revision.Workflow)}); err != nil {
		t.Fatal(err)
	}
	settledRun(t, service, run.ID, "completed")
	if ids, _ := agentRunsSince(runtime, before); !slices.Equal(ids, []string{run.ID + ".r2.reproduce", run.ID + ".r2.fix"}) {
		t.Fatalf("round 2 ran %v", ids)
	}
}

func TestAnEditedStepThatNeverStartedKeepsNoResult(t *testing.T) {
	plan := domain.WorkflowSpec{Steps: []domain.WorkflowStep{{ID: "a"}, {ID: "b", DependsOn: []string{"a"}}}}
	runs := []storage.StepRun{{StepID: "a", Round: 1, State: "completed"}, {StepID: "b", Round: 1, State: "failed"}}
	// Round 2 was a resume that edited a and was cancelled before a started.
	if held := standing(plan, runs, nil, map[int][]string{2: {"a"}}, 2); held["a"] {
		t.Fatalf("a step its edit ran again holds no result until it runs: %v", held)
	}
	if held := standing(plan, runs, nil, nil, 2); !held["a"] {
		t.Fatalf("without the edit a keeps its result: %v", held)
	}
}

func TestStartOverRunsEveryStepInAFreshWorktree(t *testing.T) {
	service, runtime, run := failedOnADeniedCommand(t)
	rerun, err := service.PlanRerun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := len(startedRuns(runtime))
	started, err := service.RerunWorkflowRun(run.ID, RerunRequest{Round: 1, Workflow: encodeWorkflow(t, rerun.Plan.Workflow), StartOver: true})
	if err != nil {
		t.Fatal(err)
	}
	if started.ID == run.ID || started.RerunOf != run.ID {
		t.Fatalf("starting over is a new run that names the old one: %+v", started)
	}
	done := settledRun(t, service, started.ID, "completed")
	if done.RerunOf != run.ID || done.Worktree == "" || done.Worktree == run.Worktree || done.Branch == run.Branch {
		t.Fatalf("the new run has its own worktree and branch: %+v", done)
	}
	if ids, _ := agentRunsSince(runtime, before); !slices.Equal(ids, []string{started.ID + "-reproduce", started.ID + "-fix"}) {
		t.Fatalf("starting over runs every step: %v", ids)
	}
	old, err := service.WorkflowRun(run.ID)
	if err != nil || old.State != "failed" || old.Round != 1 || old.RerunOf != "" {
		t.Fatalf("the old run stays as it was: %+v %v", old, err)
	}
	if _, err := service.RerunWorkflowRun(started.ID, RerunRequest{Round: 1, Workflow: encodeWorkflow(t, rerun.Plan.Workflow), StartOver: true}); err == nil || !strings.Contains(err.Error(), notStopped) {
		t.Fatalf("a completed run is not rerun: %v", err)
	}
}

func TestATasksStateIsItsLatestRunsAcrossARerun(t *testing.T) {
	service, _, run := failedOnADeniedCommand(t)
	expect := func(want string) {
		t.Helper()
		if state, err := service.store.TaskState(service.context(), run.TaskID); err != nil || state != want {
			t.Fatalf("task state = %q, %v; want %q", state, err, want)
		}
	}
	expect("failed")
	rerun, err := service.PlanRerun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	started, err := service.RerunWorkflowRun(run.ID, RerunRequest{Round: 1, Workflow: encodeWorkflow(t, rerun.Plan.Workflow), StartOver: true})
	if err != nil {
		t.Fatal(err)
	}
	settledRun(t, service, started.ID, "completed")
	expect("completed")
}

func TestResumeIsRefusedWhenTheWorktreeIsGoneButStartOverIsNot(t *testing.T) {
	service, _, run := failedOnADeniedCommand(t)
	if err := os.RemoveAll(run.Worktree); err != nil {
		t.Fatal(err)
	}
	rerun, err := service.PlanRerun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rerun.Resume.Refusal, "is gone") {
		t.Fatalf("the rerun says why it cannot resume: %+v", rerun.Resume)
	}
	plan := encodeWorkflow(t, rerun.Plan.Workflow)
	if _, err := service.RerunWorkflowRun(run.ID, RerunRequest{Round: 1, Workflow: plan}); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatalf("resume must be refused: %v", err)
	}
	started, err := service.RerunWorkflowRun(run.ID, RerunRequest{Round: 1, Workflow: plan, StartOver: true})
	if err != nil {
		t.Fatal(err)
	}
	settledRun(t, service, started.ID, "completed")
}

func TestARerunIsHeldToTheChecksOfAnApproval(t *testing.T) {
	service, _, run := failedOnADeniedCommand(t)
	rerun, err := service.PlanRerun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	shell := editStep(rerun.Plan.Workflow, "fix", func(step *domain.WorkflowStep) {
		step.Commands = [][]string{{"sh", "-c", "go test ./..."}}
	})
	if _, err := service.ReviseRerun(run.ID, encodeWorkflow(t, shell)); err == nil || !strings.Contains(err.Error(), "runs a shell") {
		t.Fatalf("a rerun's edits are checked as an approval's: %v", err)
	}
	for _, startOver := range []bool{false, true} {
		if _, err := service.RerunWorkflowRun(run.ID, RerunRequest{Round: 1, Workflow: encodeWorkflow(t, shell), StartOver: startOver}); err == nil || !strings.Contains(err.Error(), "runs a shell") {
			t.Fatalf("start over %v: the rerun must be refused, got %v", startOver, err)
		}
	}
	// An approval that does not show Go's rebuilding of its edits is stale.
	stale := editStep(rerun.Plan.Workflow, "fix", func(step *domain.WorkflowStep) { step.AllowedPaths = []string{" . "} })
	if _, err := service.RerunWorkflowRun(run.ID, RerunRequest{Round: 1, Workflow: encodeWorkflow(t, stale)}); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("a stale rerun approval must be refused: %v", err)
	}
	if _, err := service.RerunWorkflowRun(run.ID, RerunRequest{Round: 3, Workflow: encodeWorkflow(t, rerun.Plan.Workflow)}); err == nil || !strings.Contains(err.Error(), "reload it") {
		t.Fatalf("a rerun of another round must be refused: %v", err)
	}
}

func TestAReadOnlyStepRefusedAWriteIsSuggestedWriteAccess(t *testing.T) {
	step := domain.WorkflowStep{ID: "reproduce", Kind: domain.StepAgent, Agent: "researcher", Permission: domain.PermissionReadOnly, Commands: [][]string{{"go", "test", "./..."}}}
	denials := []DenialView{
		{PermissionDenial: domain.PermissionDenial{Tool: "Edit", Detail: "main.go"}, Write: true},
		{PermissionDenial: domain.PermissionDenial{Tool: "Write", Detail: "main_test.go"}, Write: true},
		{PermissionDenial: domain.PermissionDenial{Tool: "Bash", Command: "go test ./..."}, Grantable: true},
		{PermissionDenial: domain.PermissionDenial{Tool: "Bash", Command: "go test ./... | tee out"}, Grantable: true},
	}
	suggestions := suggestFixes(step, denials, true)
	if len(suggestions) != 1 || suggestions[0].Permission != domain.PermissionWorktree || !strings.Contains(suggestions[0].Text, "its agent researcher is read-only") {
		t.Fatalf("one write suggestion, nothing for a listed or unallowable command: %+v", suggestions)
	}
}
