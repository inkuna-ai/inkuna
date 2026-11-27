package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/notify"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// gatedRules pause an implementation at a review gate after the stage that
// writes it, a research task at a gate with nothing before it to send
// changes to, and documentation at a gate between two stages that write.
const gatedRules = `version: 1
rules:
  - name: gated
    when:
      kind: [implementation]
    then:
      stages:
        - id: implement
          title: Implement
          kind: agent
          agent: auto
          permission: worktree-write
        - id: approval
          kind: approval
          review: true
          dependsOn: [implement]
  - name: look
    when:
      kind: [research]
    then:
      stages:
        - id: look
          title: Look
          kind: agent
          agent: auto
          permission: read-only
        - id: approval
          kind: approval
          review: true
          dependsOn: [look]
  - name: middle
    when:
      kind: [documentation]
    then:
      stages:
        - id: draft
          title: Draft
          kind: agent
          agent: auto
          permission: worktree-write
        - id: check
          kind: approval
          review: true
          dependsOn: [draft]
        - id: polish
          title: Polish
          kind: agent
          agent: auto
          permission: worktree-write
          dependsOn: [check]
        - id: approval
          kind: approval
          dependsOn: [polish]
`

// pausedRun plans and starts a task of kind under gatedRules and waits for
// it to pause at its gate.
func pausedRun(t *testing.T, kind domain.TaskKind) (*Service, *workflowRuntime, WorkflowRunView) {
	t.Helper()
	repository := branchingRepository(t, roundsBranching)
	path := filepath.Join(repository, ".inkuna", "rules.yaml")
	if err := os.WriteFile(path, []byte(gatedRules), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repository, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repository, "commit", "-q", "-m", "Gated rules"); err != nil {
		t.Fatal(err)
	}
	runtime := writingRuntime()
	service := workflowTestService(t, repository, runtime)
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Tidy the readme", Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, storage.Reviewing)
	// The view turns reviewing a moment before the run has stored it and
	// asked for the review.
	if err := service.settle(run.ID); err != nil {
		t.Fatal(err)
	}
	// The bus stores a task a moment after it changes. A real restart ends
	// the process that would store it late; here it lives on, so a test
	// that restarts waits for it rather than have it overwrite what the
	// restarted service stores.
	gate := review(t, service, run.ID)
	deadline := time.Now().Add(10 * time.Second)
	for taskState(t, service, gate.A2ATaskID) != a2a.StateInputRequired {
		if time.Now().After(deadline) {
			t.Fatal("the gate's task was never stored as waiting")
		}
		time.Sleep(time.Millisecond)
	}
	return service, runtime, run
}

// review is the one gate a paused run waits at.
func review(t *testing.T, service *Service, runID string) storage.InputRequest {
	t.Helper()
	reviews, err := service.store.PendingReviews(context.Background(), runID)
	if err != nil || len(reviews) != 1 {
		t.Fatalf("one pending review: %+v %v", reviews, err)
	}
	return reviews[0]
}

// gateRunState is a gate's step run as stored.
func gateRunState(t *testing.T, service *Service, stepRunID string) string {
	t.Helper()
	var state string
	if err := service.store.DB().QueryRow(`SELECT state FROM runs WHERE id=?`, stepRunID).Scan(&state); err != nil {
		t.Fatalf("%s: %v", stepRunID, err)
	}
	return state
}

func taskState(t *testing.T, service *Service, taskID string) a2a.TaskState {
	t.Helper()
	task, err := service.store.A2ATask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	return task.Status.State
}

func TestAReviewGatePausesTheRunUntilItIsApproved(t *testing.T) {
	service, runtime, run := pausedRun(t, domain.TaskImplementation)
	for _, step := range run.Steps {
		if step.ID == "approval" && (step.State != storage.Reviewing || step.A2ATaskID == "") {
			t.Fatalf("the gate is reviewing, with its thread: %+v", step)
		}
	}
	gate := review(t, service, run.ID)
	if gate.StepID != "approval" || gate.ReviewTarget != "implement" || !strings.Contains(gate.Question, "round 1: approve") {
		t.Fatalf("the gate asks for a review, naming its target: %+v", gate)
	}
	if gateRunState(t, service, run.ID+"-approval") != storage.Reviewing {
		t.Fatal("the gate's step run is reviewing")
	}
	thread, err := service.Thread(gate.A2ATaskID)
	if err != nil || thread.PendingInput == nil || thread.PendingInput.Expired || !thread.PendingInput.Review {
		t.Fatalf("Messages can answer the review: %+v %v", thread.PendingInput, err)
	}
	notified := false
	for _, sent := range service.notifier.(*notify.Recorder).Sent() {
		notified = notified || sent.Kind == "review-required" && sent.Reference == run.ID
	}
	if !notified {
		t.Fatal("a paused run asks for its review")
	}

	// A reviewing run holds no task slot: with one slot, another run starts
	// and finishes while this one waits.
	service.runsMu.Lock()
	service.taskSlots = make(chan struct{}, 1)
	service.runsMu.Unlock()
	_, other, err := planAndStart(t, service, WorkflowRequestView{Objective: "Look around", Kind: domain.TaskResearch})
	if err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, other.ID, storage.Reviewing)

	before := len(startedRuns(runtime))
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	done := waitForWorkflowRun(t, service, run.ID, "completed")
	if done.Round != 1 || len(startedRuns(runtime)) != before {
		t.Fatalf("approving the last gate runs nothing more and ends round 1: %+v", done)
	}
	resolved, err := service.store.Input(context.Background(), gate.ID)
	if err != nil || resolved.State != storage.InputAnswered || resolved.Answer != "Approved" {
		t.Fatalf("the review is answered: %+v %v", resolved, err)
	}
	if gateRunState(t, service, run.ID+"-approval") != "completed" || taskState(t, service, gate.A2ATaskID) != a2a.StateCompleted {
		t.Fatal("the gate and its thread complete")
	}
	if _, err := service.ApproveReview(run.ID); err == nil || !strings.Contains(err.Error(), "not waiting for review") {
		t.Fatalf("a run approved once is not approved again: %v", err)
	}
}

func TestAReplyAtAGateRunsTheNextRoundAndPausesAgain(t *testing.T) {
	service, runtime, run := pausedRun(t, domain.TaskImplementation)
	gate := review(t, service, run.ID)
	before := len(startedRuns(runtime))
	if _, err := service.AnswerInput(gate.ID, "Keep the title."); err != nil {
		t.Fatal(err)
	}
	again := waitForRound(t, service, run.ID, 2)
	if again.State != storage.Reviewing || again.Branch != run.Branch {
		t.Fatalf("round 2 pauses at the gate again, on the same branch: %+v", again)
	}
	started := startedRuns(runtime)[before:]
	if len(started) != 1 || started[0].RunID != run.ID+".r2.implement" || !strings.Contains(string(started[0].Prompt), "[f1] Keep the title.") {
		t.Fatalf("round 2 ran implement with the reply as its feedback: %d runs", len(started))
	}
	commits, err := service.store.Commits(context.Background(), run.ID)
	if err != nil || len(commits) != 2 || commits[1].Round != 2 {
		t.Fatalf("round 2 committed: %+v %v", commits, err)
	}
	answered, err := service.store.Input(context.Background(), gate.ID)
	if err != nil || answered.State != storage.InputAnswered || answered.Answer != "implement: Keep the title." {
		t.Fatalf("the round 1 review is answered with the changes: %+v %v", answered, err)
	}
	if gateRunState(t, service, run.ID+"-approval") != stepChangesRequested || taskState(t, service, gate.A2ATaskID) != a2a.StateCompleted {
		t.Fatal("the round 1 gate sent the run back and its thread completed")
	}
	next := review(t, service, run.ID)
	if next.ID == gate.ID || !strings.Contains(next.Question, "round 2: approve") {
		t.Fatalf("round 2 asks again: %+v", next)
	}
	if _, err := service.AnswerInput(gate.ID, "Again."); err == nil {
		t.Fatal("the round 1 review cannot be answered twice")
	}
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	if done := waitForWorkflowRun(t, service, run.ID, "completed"); done.Round != 2 {
		t.Fatalf("round %d", done.Round)
	}
}

// waitForRound waits until a run is in round and idle there.
func waitForRound(t *testing.T, service *Service, runID string, round int) WorkflowRunView {
	t.Helper()
	for {
		run := waitForWorkflowRun(t, service, runID, storage.Reviewing)
		if run.Round == round {
			if err := service.settle(runID); err != nil {
				t.Fatal(err)
			}
			return run
		}
		if run.Round > round {
			t.Fatalf("run is in round %d, past %d", run.Round, round)
		}
	}
}

func TestAReviewSurvivesARestart(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		service, _, run := pausedRun(t, domain.TaskImplementation)
		gate := review(t, service, run.ID)
		after := restarted(t, service)
		if err := after.store.MarkInterruptedExecutions(context.Background()); err != nil {
			t.Fatal(err)
		}
		if execution, err := after.store.Execution(context.Background(), run.ID); err != nil || execution.State != storage.Reviewing {
			t.Fatalf("a restart leaves a reviewing run paused: %+v %v", execution, err)
		}
		thread, err := after.Thread(gate.A2ATaskID)
		if err != nil || thread.PendingInput == nil || thread.PendingInput.Expired {
			t.Fatalf("its review can still be answered: %+v %v", thread.PendingInput, err)
		}
		if _, err := after.ApproveReview(run.ID); err != nil {
			t.Fatal(err)
		}
		waitForWorkflowRun(t, after, run.ID, "completed")
		if taskState(t, after, gate.A2ATaskID) != a2a.StateCompleted {
			t.Fatal("the gate's thread, asked before the restart, completes")
		}
	})
	t.Run("cancel", func(t *testing.T) {
		service, _, run := pausedRun(t, domain.TaskImplementation)
		gate := review(t, service, run.ID)
		after := restarted(t, service)
		cancelled, err := after.CancelWorkflowRun(run.ID)
		if err != nil || cancelled.State != "cancelled" {
			t.Fatalf("a reviewing run is cancelled after a restart: %+v %v", cancelled, err)
		}
		if input, err := after.store.Input(context.Background(), gate.ID); err != nil || input.State != storage.InputCanceled {
			t.Fatalf("its review is withdrawn: %+v %v", input, err)
		}
		if gateRunState(t, after, run.ID+"-approval") != "cancelled" || taskState(t, after, gate.A2ATaskID) != a2a.StateCanceled {
			t.Fatal("the gate and its thread are cancelled")
		}
		if _, err := after.ApproveReview(run.ID); err == nil {
			t.Fatal("a cancelled run is not approved")
		}
	})
}

func TestCancellingAReviewingRunEndsIt(t *testing.T) {
	service, _, run := pausedRun(t, domain.TaskImplementation)
	gate := review(t, service, run.ID)
	cancelled, err := service.CancelWorkflowRun(run.ID)
	if err != nil || cancelled.State != "cancelled" || cancelled.FinishedAt == "" {
		t.Fatalf("cancel ends a reviewing run at once: %+v %v", cancelled, err)
	}
	for _, step := range cancelled.Steps {
		if step.ID == "approval" && step.State != "cancelled" {
			t.Fatalf("the gate is cancelled with the run: %+v", step)
		}
	}
	if input, err := service.store.Input(context.Background(), gate.ID); err != nil || input.State != storage.InputCanceled {
		t.Fatalf("its review is withdrawn: %+v %v", input, err)
	}
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "implement", Text: "Again."}}}); err == nil {
		t.Fatal("a cancelled run is not sent back")
	}
}

func TestAnApproveOnlyGateRefusesChanges(t *testing.T) {
	service, _, run := pausedRun(t, domain.TaskResearch)
	gate := review(t, service, run.ID)
	if gate.ReviewTarget != "" || !strings.Contains(gate.Question, "can only be approved") {
		t.Fatalf("the gate has no target and says so: %+v", gate)
	}
	if _, err := service.AnswerInput(gate.ID, "Look deeper."); err == nil || !strings.Contains(err.Error(), "can only be approved") {
		t.Fatalf("a reply to an approve-only gate is refused: %v", err)
	}
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "look", Text: "Look deeper."}}}); err == nil {
		t.Fatal("changes to a run with nothing that writes are refused")
	}
	if pending := review(t, service, run.ID); pending.State != storage.InputPending {
		t.Fatalf("a refusal leaves the review open: %+v", pending)
	}
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
}

func TestATargetAfterTheGateGetsItsFeedbackOnceApproved(t *testing.T) {
	service, runtime, run := pausedRun(t, domain.TaskDocumentation)
	before := len(startedRuns(runtime))
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{
		{Target: "draft", Text: "Shorter intro."},
		{Target: "polish", Text: "Fix the headings."},
	}}); err != nil {
		t.Fatal(err)
	}
	// Round 2 redrafts and pauses at the gate again, before polish.
	waitForRound(t, service, run.ID, 2)
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	prompts := make(map[string]string)
	for _, request := range startedRuns(runtime)[before:] {
		prompts[request.RunID] = string(request.Prompt)
	}
	if !strings.Contains(prompts[run.ID+".r2.draft"], "[f1] Shorter intro.") {
		t.Fatalf("draft got its feedback: %v", prompts)
	}
	if polish := prompts[run.ID+".r2.polish"]; !strings.Contains(polish, "[f2] Fix the headings.") || strings.Contains(polish, "Shorter intro.") {
		t.Fatalf("polish, run after the approval, got its own feedback only:\n%s", polish)
	}
}

func TestACancelThatFindsTheRunPausedEndsIt(t *testing.T) {
	service, _, run := pausedRun(t, domain.TaskImplementation)
	// What CancelWorkflowRun does when it saw the run running and the run
	// paused before the cancel took hold.
	cancelled, err := service.cancelLive(run.ID)
	if err != nil || cancelled.State != "cancelled" {
		t.Fatalf("the paused run is cancelled, not left cancelling: %+v %v", cancelled, err)
	}
	if execution, err := service.store.Execution(context.Background(), run.ID); err != nil || execution.State != "cancelled" {
		t.Fatalf("stored: %+v %v", execution, err)
	}
}

func TestALateReplyToAnApprovedReviewIsRefused(t *testing.T) {
	service, runtime, run := pausedRun(t, domain.TaskImplementation)
	// The reply read the review while it was open; the approval lands
	// first and ends the run.
	stale := review(t, service, run.ID)
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	// The view finishes before the record is saved; the run's goroutine
	// has saved it by the time it is done.
	service.runsWG.Wait()
	before := len(startedRuns(runtime))
	if _, err := service.answerReview(stale, "Change it after all."); err == nil || !strings.Contains(err.Error(), "already answered") {
		t.Fatalf("a reply to a closed review starts nothing: %v", err)
	}
	execution, err := service.store.Execution(context.Background(), run.ID)
	if err != nil || execution.State != "completed" || execution.Round != 1 || len(startedRuns(runtime)) != before {
		t.Fatalf("the approved run is unchanged: %+v %v", execution, err)
	}
}

func TestApprovalWithoutItsRoundsFeedbackChangesNothing(t *testing.T) {
	service, _, run := pausedRun(t, domain.TaskImplementation)
	// The round's feedback cannot be read.
	if _, err := service.store.DB().Exec(`ALTER TABLE feedback RENAME TO feedback_gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveReview(run.ID); err == nil || !strings.Contains(err.Error(), "load the feedback") {
		t.Fatalf("the approval is refused with the read error: %v", err)
	}
	execution, err := service.store.Execution(context.Background(), run.ID)
	if err != nil || execution.State != storage.Reviewing {
		t.Fatalf("the run is still paused: %+v %v", execution, err)
	}
	if gate := review(t, service, run.ID); gate.State != storage.InputPending {
		t.Fatalf("its review is still open: %+v", gate)
	}
}
