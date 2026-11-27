package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

const roundsBranching = "version: 1\ntracker: github\nbranch:\n  base: main\n  pattern: \"{type}/{story}-{slug}\"\nworktrees:\n  enabled: true\n  location: ../{repo}-worktrees\n  cleanup: manual\n"

// finishedBugfix runs the built-in bugfix flow (profile, reproduce, fix,
// verify, approval) to the end with a runtime that writes a file for each
// writable step, so each such step commits.
func finishedBugfix(t *testing.T) (*Service, *workflowRuntime, WorkflowRunView) {
	t.Helper()
	repository := branchingRepository(t, roundsBranching)
	runtime := writingRuntime()
	service := workflowTestService(t, repository, runtime)
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the login crash #42", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	return service, runtime, waitForWorkflowRun(t, service, run.ID, "completed")
}

// startedRuns lists the step runs the runtime was asked to run.
func startedRuns(runtime *workflowRuntime) []agentruntime.RunRequest {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return append([]agentruntime.RunRequest(nil), runtime.started...)
}

// restarted is the service as the app would be after quitting and opening
// again: the same database and data directory, and nothing in memory. Like
// Shutdown, it first waits for before's runs, which show as ended a moment
// before they have stored that.
func restarted(t *testing.T, before *Service) *Service {
	t.Helper()
	before.runsWG.Wait()
	service := &Service{
		dataDir: before.dataDir, store: before.store, artifacts: before.artifacts, runtimes: before.runtimes, runs: make(map[string]*liveExecution),
		config: before.config, secretStore: before.secretStore, notifier: before.notifier, secrets: before.secrets,
	}
	service.ensureBus()
	t.Cleanup(func() {
		service.cancelAllRuns()
		service.runsWG.Wait()
	})
	return service
}

func TestRequestChangesRunsTheTargetAndWhatFollowsOnTheSameBranch(t *testing.T) {
	service, runtime, run := finishedBugfix(t)
	before := len(startedRuns(runtime))
	// A commit made by hand on top of the run's is kept; the round builds on it.
	if err := os.WriteFile(filepath.Join(run.Worktree, "NOTES.md"), []byte("by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(run.Worktree, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(run.Worktree, "commit", "-q", "-m", "Notes by hand"); err != nil {
		t.Fatal(err)
	}

	reopened, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Handle an empty name."}}})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Round != 2 || reopened.State != "queued" {
		t.Fatalf("reopened %+v", reopened)
	}
	for _, step := range reopened.Steps {
		if step.ID == "reproduce" && step.State != "completed" {
			t.Errorf("reproduce is reused, so it shows completed: %+v", step)
		}
	}
	done := waitForWorkflowRun(t, service, run.ID, "completed")
	if done.Round != 2 || done.Branch != run.Branch || done.Worktree != run.Worktree {
		t.Fatalf("round 2 ran on the same branch and worktree: %+v", done)
	}

	// Only fix ran again among the agent steps, as <run>.r2.fix, and its
	// prompt carried the feedback.
	again := startedRuns(runtime)[before:]
	if len(again) != 1 || again[0].RunID != run.ID+".r2.fix" {
		ids := make([]string, 0, len(again))
		for _, request := range again {
			ids = append(ids, request.RunID)
		}
		t.Fatalf("round 2 ran %v, want only %s.r2.fix", ids, run.ID)
	}
	if prompt := string(again[0].Prompt); !strings.Contains(prompt, "# Feedback (round 2)") || !strings.Contains(prompt, "[f1] Handle an empty name.") {
		t.Fatalf("the target's prompt lacks its feedback:\n%s", prompt)
	}

	commits, err := service.store.Commits(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := commits[len(commits)-1]
	if last.StepID != "fix" || last.Round != 2 || !strings.HasSuffix(last.Subject, "(round 2)") || len(commits) != 2 {
		t.Fatalf("round 2 committed fix again: %+v", commits)
	}
	feedback, err := service.store.Feedback(context.Background(), run.ID, 2)
	if err != nil || len(feedback) != 1 || feedback[0].ID != "f1" || feedback[0].Target != "fix" {
		t.Fatalf("feedback %+v %v", feedback, err)
	}
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Again."}}}); err == nil || !strings.Contains(err.Error(), "in round 2") {
		t.Fatalf("changes written for round 1 are refused once the run is in round 2: %v", err)
	}
}

func TestAnEndedRunReopensAfterARestart(t *testing.T) {
	service, runtime, run := finishedBugfix(t)
	after := restarted(t, service)
	before := len(startedRuns(runtime))
	if _, err := after.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Log the failure."}}}); err != nil {
		t.Fatal(err)
	}
	done := waitForWorkflowRun(t, after, run.ID, "completed")
	if done.Round != 2 {
		t.Fatalf("round %d", done.Round)
	}
	again := startedRuns(runtime)[before:]
	if len(again) != 1 || again[0].RunID != run.ID+".r2.fix" || !strings.Contains(string(again[0].Prompt), "[f1] Log the failure.") {
		t.Fatalf("after a restart, round 2 ran %d steps", len(again))
	}
}

func TestRequestChangesRefusesWhatItCannotBuildOn(t *testing.T) {
	service, _, run := finishedBugfix(t)
	send := func(target string) error {
		_, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: target, Text: "Change it."}}})
		return err
	}
	unchanged := func(t *testing.T) {
		t.Helper()
		execution, err := service.store.Execution(context.Background(), run.ID)
		if err != nil || execution.Round != 1 || execution.State != "completed" {
			t.Fatalf("a refusal changes nothing: %+v %v", execution, err)
		}
	}
	if err := send("reproduce"); err == nil || !strings.Contains(err.Error(), "does not write") {
		t.Fatalf("a read-only step cannot take feedback: %v", err)
	}
	if err := send("nowhere"); err == nil || !strings.Contains(err.Error(), "not a step of this run") {
		t.Fatalf("a missing step: %v", err)
	}
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1}); err == nil {
		t.Fatal("nothing to change is refused")
	}

	scratch := filepath.Join(run.Worktree, "SCRATCH.md")
	if err := os.WriteFile(scratch, []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := send("fix"); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("a dirty worktree is refused: %v", err)
	}
	if err := os.Remove(scratch); err != nil {
		t.Fatal(err)
	}
	unchanged(t)

	// Reset back to where the run started: its commits are gone from the
	// branch.
	if err := runGit(run.Worktree, "reset", "-q", "--hard", run.BaseCommit); err != nil {
		t.Fatal(err)
	}
	if err := send("fix"); err == nil || !strings.Contains(err.Error(), "no longer holds the run's commit") {
		t.Fatalf("a rewritten branch is refused: %v", err)
	}
	unchanged(t)

	execution, err := service.store.Execution(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	execution.State = "cancelled"
	if err := service.store.SaveExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}
	service.runsMu.Lock()
	delete(service.runs, run.ID)
	service.runsMu.Unlock()
	if err := send("fix"); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("a cancelled run is refused: %v", err)
	}
}

func TestAWorktreeIsNotRemovedOnceItsRunIsSentBack(t *testing.T) {
	service, runtime, run := finishedBugfix(t)
	ctx := context.Background()
	// What a removal read while the run still looked finished.
	stale, err := service.store.Execution(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := service.store.Project(ctx, stale.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	write := runtime.handle
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if strings.Contains(request.RunID, ".r2.") {
			select {
			case <-release:
			case <-ctx.Done():
				return agentruntime.RunResult{}, ctx.Err()
			}
		}
		return write(ctx, request)
	}
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Again."}}}); err != nil {
		t.Fatal(err)
	}
	if err := service.removeWorktree(ctx, project, stale); err == nil || !strings.Contains(err.Error(), "active again") {
		t.Fatalf("a removal that saw the run finished must not take the worktree of its new round: %v", err)
	}
	if _, err := os.Stat(run.Worktree); err != nil {
		t.Fatalf("the worktree should still be there: %v", err)
	}
	close(release)
	waitForWorkflowRun(t, service, run.ID, "completed")
}

func TestCancellingAQueuedRoundReleasesTheCheckout(t *testing.T) {
	repository := branchingRepository(t, "version: 1\ntracker: none\nbranch:\n  base: main\n  pattern: \"{type}/{slug}\"\nworktrees:\n  enabled: false\n")
	if err := runGit(repository, "switch", "-q", "-c", "work"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repository, writingRuntime())
	plan, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Tidy the readme", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	target := ""
	for _, step := range plan.Workflow.Steps {
		if step.Kind == domain.StepAgent && step.Permission == domain.PermissionWorktree {
			target = step.ID
		}
	}
	// Every task slot is taken, so the round waits in the queue.
	service.runsMu.Lock()
	service.taskSlots = make(chan struct{}, 1)
	service.taskSlots <- struct{}{}
	service.runsMu.Unlock()
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: target, Text: "Again."}}}); err != nil {
		t.Fatal(err)
	}
	if err := service.claimCheckout(repository, "other-run"); err == nil {
		t.Fatal("the queued round holds the checkout")
	}
	if _, err := service.CancelWorkflowRun(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "cancelled")
	if err := service.claimCheckout(repository, "other-run"); err != nil {
		t.Fatalf("a cancelled round must release the checkout: %v", err)
	}
}
