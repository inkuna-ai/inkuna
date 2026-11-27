package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// stoppedBugfix starts the built-in bugfix flow (profile, reproduce, fix,
// verify, approval) with a runtime that writes a file for each writable
// step, as writingRuntime does, except that the first run of fix is handled
// by stop instead.
func stoppedBugfix(t *testing.T, stop func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error)) (*Service, *workflowRuntime, WorkflowRunView) {
	t.Helper()
	repository := branchingRepository(t, roundsBranching)
	runtime := writingRuntime()
	write := runtime.handle
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if strings.HasSuffix(request.RunID, "-fix") {
			return stop(ctx, request)
		}
		return write(ctx, request)
	}
	service := workflowTestService(t, repository, runtime)
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the login crash #42", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	return service, runtime, run
}

// halfDone writes a file the way a step cut short leaves one: uncommitted.
func halfDone(t *testing.T, request agentruntime.RunRequest) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(request.WorkingDir, "HALF.md"), []byte("half done"), 0o644); err != nil {
		t.Error(err)
	}
}

// agentRunsSince lists the ids of the step runs the runtime was asked for
// after the first before of them.
func agentRunsSince(runtime *workflowRuntime, before int) ([]string, []agentruntime.RunRequest) {
	requests := startedRuns(runtime)[before:]
	ids := make([]string, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.RunID)
	}
	return ids, requests
}

// resumeStates is each step of a resume view as id=state.
func resumeStates(steps []ResumeStep) string {
	states := make([]string, 0, len(steps))
	for _, step := range steps {
		states = append(states, step.ID+"="+step.State)
	}
	return strings.Join(states, ",")
}

func TestResumeRunsTheStoppedStepAndWhatFollowsOnTheSameWorktree(t *testing.T) {
	service, runtime, run := stoppedBugfix(t, func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		halfDone(t, request)
		return agentruntime.RunResult{}, errors.New("go test ./... was denied")
	})
	run = waitForWorkflowRun(t, service, run.ID, "failed")

	view, err := service.RunResume(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Refusal != "" || view.Round != 1 || view.Workspace != run.Worktree {
		t.Fatalf("a failed run with its worktree can be resumed: %+v", view)
	}
	if got := resumeStates(view.Rerun); got != "fix=failed,verify=pending,approval=pending" {
		t.Fatalf("resume runs %s", got)
	}
	if got := resumeStates(view.Kept); got != "profile=completed,reproduce=completed" {
		t.Fatalf("resume keeps %s", got)
	}
	if len(view.Changes) != 1 || view.Changes[0].Path != "HALF.md" || view.Changes[0].Status != "??" {
		t.Fatalf("the change fix left is listed: %+v", view.Changes)
	}

	before := len(startedRuns(runtime))
	resumed, err := service.ResumeWorkflowRun(run.ID, ResumeRequest{Round: 1})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Round != 2 || resumed.State != "queued" {
		t.Fatalf("resumed %+v", resumed)
	}
	done := waitForWorkflowRun(t, service, run.ID, "completed")
	if done.Round != 2 || done.Branch != run.Branch || done.Worktree != run.Worktree {
		t.Fatalf("round 2 ran on the same branch and worktree: %+v", done)
	}
	ids, again := agentRunsSince(runtime, before)
	if len(ids) != 1 || ids[0] != run.ID+".r2.fix" {
		t.Fatalf("round 2 ran %v, want only %s.r2.fix", ids, run.ID)
	}
	prompt := string(again[0].Prompt)
	for _, want := range []string{"# Resumed", "in round 1, and it failed", "It failed with: go test ./... was denied", "kept by the user: `HALF.md`"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the resumed step's prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "# Feedback") {
		t.Fatalf("a resume sends no feedback of its own:\n%s", prompt)
	}

	// The step's commit took what its stopped attempt left.
	commits, err := service.store.Commits(context.Background(), run.ID)
	if err != nil || len(commits) != 1 || commits[0].StepID != "fix" || commits[0].Round != 2 || !strings.Contains(commits[0].Stat, "HALF.md") {
		t.Fatalf("round 2 committed fix with HALF.md: %+v %v", commits, err)
	}
	rounds, err := service.RunRounds(run.ID)
	if err != nil || len(rounds) != 2 || rounds[0].Resumed || !rounds[1].Resumed || rounds[1].Discarded {
		t.Fatalf("round 2 shows as a resume: %+v %v", rounds, err)
	}
	if _, err := service.ResumeWorkflowRun(run.ID, ResumeRequest{Round: 1}); err == nil || !strings.Contains(err.Error(), "completed") {
		t.Fatalf("a completed run is not resumed: %v", err)
	}
	if view, err := service.RunResume(run.ID); err != nil || view.Refusal != resumeCompleted {
		t.Fatalf("a completed run says why it cannot be resumed: %+v %v", view, err)
	}
}

func TestACancelledRunResumesWithItsLeftoversDiscarded(t *testing.T) {
	running := make(chan struct{})
	service, runtime, run := stoppedBugfix(t, func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		halfDone(t, request)
		close(running)
		<-ctx.Done()
		return agentruntime.RunResult{}, ctx.Err()
	})
	select {
	case <-running:
	case <-time.After(30 * time.Second):
		t.Fatal("fix never started")
	}
	if view, err := service.RunResume(run.ID); err != nil || view.Refusal != resumeBusy {
		t.Fatalf("a running run cannot be resumed: %+v %v", view, err)
	}
	if _, err := service.ResumeWorkflowRun(run.ID, ResumeRequest{Round: 1}); err == nil || err.Error() != resumeBusy {
		t.Fatalf("a running run is refused: %v", err)
	}
	if _, err := service.CancelWorkflowRun(run.ID); err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "cancelled")
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Again."}}}); err == nil || !strings.Contains(err.Error(), "resume it") {
		t.Fatalf("a cancelled run is still not sent back, and says it can be resumed: %v", err)
	}

	view, err := service.RunResume(run.ID)
	if err != nil || view.Refusal != "" {
		t.Fatalf("a cancelled run can be resumed: %+v %v", view, err)
	}
	if got := resumeStates(view.Rerun); got != "fix=cancelled,verify=pending,approval=pending" {
		t.Fatalf("resume runs %s", got)
	}
	// What the person saw is no longer all there is.
	if err := os.WriteFile(filepath.Join(run.Worktree, "MINE.md"), []byte("by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeWorkflowRun(run.ID, ResumeRequest{Round: 1, Discard: true, Changes: []string{"HALF.md"}}); err == nil || !strings.Contains(err.Error(), "changed since you looked") {
		t.Fatalf("a discard of changes the person never saw is refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(run.Worktree, "MINE.md")); err != nil {
		t.Fatalf("a refused discard throws nothing away: %v", err)
	}
	if execution, err := service.store.Execution(context.Background(), run.ID); err != nil || execution.State != "cancelled" || execution.Round != 1 {
		t.Fatalf("a refusal changes nothing: %+v %v", execution, err)
	}

	before := len(startedRuns(runtime))
	var leftover error
	write := runtime.handle
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if request.RunID == run.ID+".r2.fix" {
			_, leftover = os.Stat(filepath.Join(request.WorkingDir, "HALF.md"))
		}
		return write(ctx, request)
	}
	if _, err := service.ResumeWorkflowRun(run.ID, ResumeRequest{Round: 1, Discard: true, Changes: []string{"MINE.md", "HALF.md"}}); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	if !os.IsNotExist(leftover) {
		t.Fatalf("the resumed step started without the discarded file: %v", leftover)
	}
	ids, again := agentRunsSince(runtime, before)
	if len(ids) != 1 || ids[0] != run.ID+".r2.fix" {
		t.Fatalf("round 2 ran %v", ids)
	}
	if prompt := string(again[0].Prompt); !strings.Contains(prompt, "it was cancelled before it finished") || !strings.Contains(prompt, "The user discarded the changes it left") {
		t.Fatalf("the resumed step is told it was cancelled and its changes discarded:\n%s", prompt)
	}
	if rounds, err := service.RunRounds(run.ID); err != nil || len(rounds) != 2 || !rounds[1].Resumed || !rounds[1].Discarded {
		t.Fatalf("round 2 records the discard: %+v %v", rounds, err)
	}
}

func TestAResumeAfterARestartSendsTheStoppedTargetItsFeedbackAgain(t *testing.T) {
	service, runtime, run := finishedBugfix(t)
	write := runtime.handle
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if request.RunID == run.ID+".r2.fix" {
			return agentruntime.RunResult{}, errors.New("the model ran out of turns")
		}
		return write(ctx, request)
	}
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Handle an empty name."}}}); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "failed")
	// Inkuna quits; the run shows as interrupted when it opens again.
	after := restarted(t, service)
	execution, err := after.store.Execution(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	execution.State = "interrupted"
	if err := after.store.SaveExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}

	before := len(startedRuns(runtime))
	if _, err := after.ResumeWorkflowRun(run.ID, ResumeRequest{Round: 2}); err != nil {
		t.Fatal(err)
	}
	if done := waitForWorkflowRun(t, after, run.ID, "completed"); done.Round != 3 {
		t.Fatalf("round %d", done.Round)
	}
	ids, again := agentRunsSince(runtime, before)
	if len(ids) != 1 || ids[0] != run.ID+".r3.fix" {
		t.Fatalf("round 3 ran %v", ids)
	}
	prompt := string(again[0].Prompt)
	for _, want := range []string{"# Feedback (round 3)", "[f1] Handle an empty name.", "# Resumed", "in round 2, and it failed", "# Previous round", "in round 1"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the resumed target's prompt lacks %q:\n%s", want, prompt)
		}
	}
	feedback, err := after.store.Feedback(context.Background(), run.ID, 3)
	if err != nil || len(feedback) != 1 || feedback[0].ID != "f1" || feedback[0].Status == storage.FeedbackOpen {
		t.Fatalf("round 3 sent f1 again and settled it: %+v %v", feedback, err)
	}
}

func TestResumeRefusesAWorkspaceThatNoLongerHoldsTheRun(t *testing.T) {
	service, _, run := stoppedBugfix(t, func(context.Context, agentruntime.RunRequest) (agentruntime.RunResult, error) {
		return agentruntime.RunResult{}, errors.New("the step failed")
	})
	run = waitForWorkflowRun(t, service, run.ID, "failed")
	ctx := context.Background()
	refused := func(t *testing.T, want string) {
		t.Helper()
		view, err := service.RunResume(run.ID)
		if err != nil || !strings.Contains(view.Refusal, want) {
			t.Fatalf("the view says %q: %+v %v", want, view, err)
		}
		if _, err := service.ResumeWorkflowRun(run.ID, ResumeRequest{Round: 1}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("resume is refused with %q: %v", want, err)
		}
		if execution, err := service.store.Execution(ctx, run.ID); err != nil || execution.State != "failed" || execution.Round != 1 {
			t.Fatalf("a refusal changes nothing: %+v %v", execution, err)
		}
	}

	if err := runGit(run.Worktree, "switch", "-q", "-c", "elsewhere"); err != nil {
		t.Fatal(err)
	}
	refused(t, "not the run's branch")
	if err := runGit(run.Worktree, "switch", "-q", run.Branch); err != nil {
		t.Fatal(err)
	}

	// The commit the run started from, rewritten: the branch no longer holds
	// it.
	if run.BaseCommit == "" {
		t.Fatal("the run recorded no commit it started from")
	}
	if err := runGit(run.Worktree, "commit", "-q", "--amend", "--allow-empty", "-m", "rewritten"); err != nil {
		t.Fatal(err)
	}
	refused(t, "no longer holds the run's commit")
	if err := runGit(run.Worktree, "reset", "-q", "--hard", run.BaseCommit); err != nil {
		t.Fatal(err)
	}
	if view, err := service.RunResume(run.ID); err != nil || view.Refusal != "" {
		t.Fatalf("put back, the run can be resumed again: %+v %v", view, err)
	}

	execution, err := service.store.Execution(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := service.store.Project(ctx, execution.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.removeWorktree(ctx, project, execution); err != nil {
		t.Fatal(err)
	}
	refused(t, "worktree was removed")
	if _, err := service.ResumeWorkflowRun(run.ID, ResumeRequest{Round: 2}); err == nil || !strings.Contains(err.Error(), "reload it") {
		t.Fatalf("a resume written for another round is refused: %v", err)
	}
}

func TestResumeNotesSayHowEachStoppedStepEnded(t *testing.T) {
	plan := domain.WorkflowSpec{Steps: []domain.WorkflowStep{
		{ID: "write", Kind: domain.StepAgent, Permission: domain.PermissionWorktree},
		{ID: "review", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly},
		{ID: "later", Kind: domain.StepAgent, Permission: domain.PermissionWorktree, DependsOn: []string{"write"}},
		{ID: "done", Kind: domain.StepAgent, Permission: domain.PermissionWorktree},
	}}
	finished := time.Now()
	runs := []storage.StepRun{
		{StepID: "write", Round: 1, State: "failed", Summary: "earlier", FinishedAt: finished},
		{StepID: "write", Round: 2, State: "running"},
		{StepID: "review", Round: 2, State: "failed", Summary: "denied", FinishedAt: finished},
		{StepID: "done", Round: 2, State: "completed", FinishedAt: finished},
	}
	restart := map[string]bool{"write": true, "review": true, "later": true}
	notes := resumeNotes(storage.Execution{Round: 2, State: "interrupted"}, plan, runs, restart, []string{"a.go"}, false)
	if len(notes) != 2 {
		t.Fatalf("only the steps that stopped in round 2 get a note: %+v", notes)
	}
	if write := notes["write"]; write.Round != 2 || write.Stopped != domain.StoppedInterrupted || write.Reason != "" || len(write.Kept) != 1 || write.Discarded {
		t.Fatalf("write was cut short when Inkuna stopped, and keeps a.go: %+v", write)
	}
	if review := notes["review"]; review.Stopped != domain.StoppedFailed || review.Reason != "denied" || len(review.Kept) != 0 {
		t.Fatalf("review failed, and as it cannot write is told of no changes: %+v", review)
	}
	discarded := resumeNotes(storage.Execution{Round: 2, State: "cancelled"}, plan, runs, restart, []string{"a.go"}, true)
	if write := discarded["write"]; write.Stopped != domain.StoppedCancelled || len(write.Kept) != 0 || !write.Discarded {
		t.Fatalf("write was cancelled and its changes discarded: %+v", write)
	}
}

func TestADiscardThatWouldOverwriteAnIgnoredFileRefusesTheResumeAndChangesNothing(t *testing.T) {
	service, _, run := stoppedBugfix(t, func(context.Context, agentruntime.RunRequest) (agentruntime.RunResult, error) {
		return agentruntime.RunResult{}, errors.New("the step failed")
	})
	run = waitForWorkflowRun(t, service, run.ID, "failed")
	// README.md is staged for deletion and now ignored, so its own content
	// is listed nowhere, and restoring the last commit would overwrite it.
	if err := os.WriteFile(filepath.Join(run.Worktree, ".gitignore"), []byte("README.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(run.Worktree, "rm", "-q", "--cached", "README.md"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.Worktree, "README.md"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := service.RunResume(run.ID)
	if err != nil || view.Refusal != "" || len(view.Changes) != 2 {
		t.Fatalf("the resume is offered with both changes: %+v %v", view, err)
	}
	paths := []string{view.Changes[0].Path, view.Changes[1].Path}
	_, err = service.ResumeWorkflowRun(run.ID, ResumeRequest{Round: 1, Discard: true, Changes: paths})
	if err == nil || !strings.Contains(err.Error(), "would overwrite ignored files staged for deletion: README.md") || strings.Contains(err.Error(), "put back") {
		t.Fatalf("the discard is refused before it changes anything: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(run.Worktree, "README.md")); err != nil || string(data) != "local" {
		t.Fatalf("README.md keeps its content: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(run.Worktree, ".gitignore")); err != nil {
		t.Fatalf(".gitignore is still there: %v", err)
	}
	if execution, err := service.store.Execution(context.Background(), run.ID); err != nil || execution.State != "failed" || execution.Round != 1 {
		t.Fatalf("a refusal changes nothing: %+v %v", execution, err)
	}
}

func TestARefusalAfterADiscardPutsTheChangesBackOrSaysWhereTheyAre(t *testing.T) {
	repository := branchingRepository(t, "")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("half done"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "HALF.md"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// One version of README.md staged, a later one in the file.
	if err := runGit(repository, "add", "README.md"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := worktree.TakeSnapshot(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := worktree.Discard(ctx, repository); err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("this run was already resumed from round 1, or it changed state; reload it")
	err = putBack(ctx, repository, snapshot, refusal)
	if !errors.Is(err, refusal) || !strings.HasSuffix(err.Error(), "; the uncommitted changes were put back as they were") {
		t.Fatalf("the refusal says the changes were put back: %v", err)
	}
	if staged := gitOutput(t, repository, "show", ":README.md"); staged != "half done" {
		t.Fatalf("README.md's staged version is put back: %q", staged)
	}
	for name, want := range map[string]string{"README.md": "done", "HALF.md": "half"} {
		if data, err := os.ReadFile(filepath.Join(repository, name)); err != nil || string(data) != want {
			t.Fatalf("%s is %q, want %q: %v", name, data, want, err)
		}
	}
	missing := strings.Repeat("0", 40)
	if err := putBack(ctx, repository, worktree.Snapshot{Commit: missing}, refusal); !errors.Is(err, refusal) || !strings.Contains(err.Error(), "could not all be put back") || !strings.Contains(err.Error(), "commit "+missing+" still holds their files") {
		t.Fatalf("a refusal whose changes cannot be put back names the commit holding them: %v", err)
	}
}
