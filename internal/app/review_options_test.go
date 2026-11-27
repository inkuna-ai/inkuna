package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// targetIDs are the stages a review offers, in order.
func targetIDs(review ReviewView) string {
	ids := make([]string, 0, len(review.Targets))
	for _, target := range review.Targets {
		ids = append(ids, target.ID)
	}
	return strings.Join(ids, ",")
}

func TestAGateOffersApproveAndEveryStageThatWrites(t *testing.T) {
	service, _, run := pausedRun(t, domain.TaskDocumentation)
	review, err := service.RunReview(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// polish is after the gate: changes for it reach it once the gate is
	// approved and the round carries on.
	if !review.Approve || review.Round != 1 || targetIDs(review) != "draft,polish" || review.Target != "draft" || review.Refusal != "" {
		t.Fatalf("the gate offers approve, both writers, and its own target first: %+v", review)
	}
	if review.Targets[0].Title != "Draft" || review.Targets[0].Agent == "" {
		t.Fatalf("a target is named as planned: %+v", review.Targets[0])
	}
}

func TestAnApproveOnlyGateOffersOnlyApprove(t *testing.T) {
	service, _, run := pausedRun(t, domain.TaskResearch)
	review, err := service.RunReview(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Approve || len(review.Targets) != 0 || review.Target != "" || !strings.Contains(review.Refusal, "can only be approved") {
		t.Fatalf("a gate with nothing before it that writes can only be approved: %+v", review)
	}
}

func TestAGateWhoseWorktreeChangedOffersOnlyWhatGoWouldDo(t *testing.T) {
	// Approving the last gate runs nothing, so it needs no worktree.
	service, _, run := pausedRun(t, domain.TaskImplementation)
	if err := os.WriteFile(filepath.Join(run.Worktree, "STRAY.md"), []byte("stray"), 0o644); err != nil {
		t.Fatal(err)
	}
	review, err := service.RunReview(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Approve || len(review.Targets) != 0 || !strings.Contains(review.Refusal, "uncommitted changes") {
		t.Fatalf("the last gate can still be approved, but not sent back: %+v", review)
	}
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "implement", Text: "Again."}}}); err == nil || err.Error() != review.Refusal {
		t.Fatalf("RequestChanges refuses for the reason the review gave: %v", err)
	}

	// A gate with a stage after it runs that stage once approved.
	service, _, run = pausedRun(t, domain.TaskDocumentation)
	if err := os.WriteFile(filepath.Join(run.Worktree, "STRAY.md"), []byte("stray"), 0o644); err != nil {
		t.Fatal(err)
	}
	review, err = service.RunReview(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if review.Approve || len(review.Targets) != 0 || !strings.Contains(review.Refusal, "uncommitted changes") {
		t.Fatalf("neither approving nor sending back is offered: %+v", review)
	}
	if _, err := service.ApproveReview(run.ID); err == nil || err.Error() != review.Refusal {
		t.Fatalf("ApproveReview refuses for the reason the review gave: %v", err)
	}
}

func TestAnEndedRunOffersChangesUntilItsWorktreeCannotTakeThem(t *testing.T) {
	service, _, run := finishedBugfix(t)
	review, err := service.RunReview(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if review.Approve || review.Round != 1 || targetIDs(review) != "fix" || review.Target != "fix" || review.Refusal != "" {
		t.Fatalf("an ended run offers changes for the stage that writes: %+v", review)
	}
	// Go gives the same reason here that RequestChanges would.
	if err := os.WriteFile(filepath.Join(run.Worktree, "STRAY.md"), []byte("stray"), 0o644); err != nil {
		t.Fatal(err)
	}
	review, err = service.RunReview(run.ID)
	if err != nil || !strings.Contains(review.Refusal, "uncommitted changes") {
		t.Fatalf("a dirty worktree is refused with the reason: %+v %v", review, err)
	}
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Again."}}}); err == nil || err.Error() != review.Refusal {
		t.Fatalf("RequestChanges refuses for the reason the review gave: %v", err)
	}
}

func TestRunReviewWaitsUntilAnEndedRunHasStoredItsEnd(t *testing.T) {
	service, _, run := finishedBugfix(t)
	service.runsWG.Wait()
	ctx := context.Background()
	stored, err := service.store.Execution(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Stand the run back to the moment its view says it ended: SQLite still
	// says running, and its goroutine has not finished cleaning up.
	ended := stored
	stored.State = "running"
	if err := service.store.SaveExecution(ctx, stored); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	service.runsMu.Lock()
	service.runs[run.ID].done = done
	service.runsMu.Unlock()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = service.store.SaveExecution(context.Background(), ended)
		close(done)
	}()
	review, err := service.RunReview(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if review.Refusal != "" || targetIDs(review) != "fix" {
		t.Fatalf("the review reads the run once it has stored its end: %+v", review)
	}
}

func TestACancelledRunOffersNoReview(t *testing.T) {
	service, _, run := pausedRun(t, domain.TaskImplementation)
	if _, err := service.CancelWorkflowRun(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "cancelled")
	review, err := service.RunReview(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if review.Approve || len(review.Targets) != 0 || !strings.Contains(review.Refusal, "cancelled run cannot be sent back") {
		t.Fatalf("a cancelled run cannot be reviewed: %+v", review)
	}
}

func TestTheDefaultTargetIsTheRulesTargetForTheLastApproval(t *testing.T) {
	plan := domain.WorkflowSpec{Steps: []domain.WorkflowStep{
		{ID: "draft", Kind: domain.StepAgent, Permission: domain.PermissionWorktree},
		{ID: "check", Kind: domain.StepApproval, FeedbackTarget: "draft"},
		{ID: "polish", Kind: domain.StepAgent, Permission: domain.PermissionWorktree},
		{ID: "approval", Kind: domain.StepApproval},
	}}
	targets := []ReviewTarget{{ID: "draft"}, {ID: "polish"}}
	// The gate in the middle names draft, but the last approval names none.
	if got := defaultTarget(plan, nil, targets); got != "polish" {
		t.Fatalf("with no target on the last approval, the last stage that writes, got %q", got)
	}
	plan.Steps[3].FeedbackTarget = "draft"
	if got := defaultTarget(plan, nil, targets); got != "draft" {
		t.Fatalf("an ended run defaults to the last approval's target, got %q", got)
	}
	gates := []storage.InputRequest{{StepID: "approval", ReviewTarget: "polish"}}
	if got := defaultTarget(plan, gates, targets); got != "polish" {
		t.Fatalf("at a gate, the gate's own target, got %q", got)
	}
}
