package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
	workflowengine "github.com/inkuna-ai/inkuna/internal/workflow"
)

// ReviewView is what the review bar offers on a run: Approve at a review
// gate, and Request changes for the stages Go would send changes to, with
// the one it picks first. Refusal says why changes cannot be sent now, so
// the bar shows Go's reason rather than a form Go would refuse.
type ReviewView struct {
	// Round is the round being reviewed, which a request names.
	Round   int            `json:"round"`
	Approve bool           `json:"approve"`
	Targets []ReviewTarget `json:"targets"`
	Target  string         `json:"target,omitempty"`
	Refusal string         `json:"refusal,omitempty"`
	// Drafts counts the draft line comments a request for changes sends.
	Drafts int `json:"drafts"`
	// TrackerChanges are the changes the steps before the paused gates
	// asked of the tracker, which Approve makes.
	TrackerChanges []TrackerChangeView `json:"trackerChanges"`
}

// ReviewTarget is a stage changes can be sent to.
type ReviewTarget struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Agent string `json:"agent"`
}

// RunReview says what can be done to review a run now. Requesting changes
// checks all of this again, under the run's lock.
func (service *Service) RunReview(runID string) (ReviewView, error) {
	if err := service.ready(); err != nil {
		return ReviewView{}, err
	}
	ctx := service.context()
	// A run shows as ended a moment before it has stored that, so its
	// stored state is read once it has, as RequestChanges reads it.
	busy := false
	if err := service.settle(runID); errors.Is(err, errRunBusy) {
		busy = true
	} else if err != nil {
		return ReviewView{}, err
	}
	execution, err := service.store.Execution(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return ReviewView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return ReviewView{}, fmt.Errorf("load run %s: %w", runID, err)
	}
	view := ReviewView{Round: max(execution.Round, 1), Targets: []ReviewTarget{}, TrackerChanges: []TrackerChangeView{}}
	threads, err := service.store.CommentThreads(ctx, runID)
	if err != nil {
		return ReviewView{}, err
	}
	for _, thread := range threads {
		if _, ok := thread.Draft(); ok {
			view.Drafts++
		}
	}
	reviewing := execution.State == storage.Reviewing
	switch {
	case busy:
		view.Refusal = "this run is still running; send changes once it has ended or paused for review"
		return view, nil
	case execution.State == "cancelled":
		view.Refusal = cancelledRefusal
		return view, nil
	case !reviewing && !slices.Contains(storage.ReopenableStates, execution.State):
		view.Refusal = fmt.Sprintf("a %s run cannot be sent back", execution.State)
		return view, nil
	}
	plan, err := service.store.ExecutionPlan(ctx, runID)
	if err != nil {
		view.Refusal = fmt.Sprintf("this run cannot be sent back: %v", err)
		return view, nil
	}
	var gates []storage.InputRequest
	if reviewing {
		if gates, err = service.store.PendingReviews(ctx, runID); err != nil {
			return ReviewView{}, fmt.Errorf("load the reviews of run %s: %w", runID, err)
		}
		view.Approve = true
		view.TrackerChanges = service.pendingReviewChanges(ctx, runID, plan, gates)
	}
	for _, step := range reviewTargets(plan) {
		view.Targets = append(view.Targets, ReviewTarget{ID: step.ID, Title: step.Title, Agent: step.Agent})
	}
	view.Target = defaultTarget(plan, gates, view.Targets)
	switch {
	case approveOnly(gates):
		view.Targets, view.Target = []ReviewTarget{}, ""
		view.Refusal = "no stage before this gate writes, so it can only be approved"
	case len(view.Targets) == 0:
		view.Refusal = "no stage of this run writes, so there is nothing to send changes to"
	}
	if view.Refusal != "" && !reviewing {
		return view, nil
	}
	// Changes run their targets in the run's worktree, and so does an
	// approval with steps after its gate, so both need the worktree as the
	// run left it, as resumable checks.
	refusal := service.reopenRefusal(ctx, execution)
	if refusal == "" {
		return view, nil
	}
	if view.Refusal == "" {
		view.Targets, view.Target, view.Refusal = []ReviewTarget{}, "", refusal
	}
	if reviewing {
		prior, err := service.currentResults(ctx, execution, plan)
		if err != nil {
			return ReviewView{}, err
		}
		if len(afterApproval(plan, prior, gates)) > 0 {
			view.Approve, view.Refusal = false, refusal
		}
	}
	return view, nil
}

// reopenRefusal is why a run's workspace cannot take more of the run, or
// empty when it can.
func (service *Service) reopenRefusal(ctx context.Context, execution storage.Execution) string {
	if err := service.checkReopenable(ctx, execution); err != nil {
		return err.Error()
	}
	return ""
}

// afterApproval is what approving the gates a run is paused at runs next:
// every step without a completed result once the gates have theirs.
func afterApproval(plan domain.WorkflowSpec, prior map[string]domain.HandoffResult, gates []storage.InputRequest) map[string]bool {
	completed := make(map[string]bool, len(prior)+len(gates))
	for step := range prior {
		completed[step] = true
	}
	for _, gate := range gates {
		completed[gate.StepID] = true
	}
	return workflowengine.RestartSet(plan, nil, completed)
}

// reviewTargets are the stages a request for changes may name: the run's
// agent stages that write, as feedbackItems requires. At a gate that
// includes a stage after it, which gets its items once the gate is
// approved and the round carries on to it.
func reviewTargets(plan domain.WorkflowSpec) []domain.WorkflowStep {
	targets := make([]domain.WorkflowStep, 0)
	for _, step := range plan.Steps {
		if step.Kind == domain.StepAgent && step.Permission == domain.PermissionWorktree {
			targets = append(targets, step)
		}
	}
	return targets
}

// defaultTarget is the stage the bar picks first: at gates, the first gate's
// target; on an ended run, the target the rule gives the run's last approval
// stage, as for any reopen; else the last stage that writes, the nearest the
// end of the run.
func defaultTarget(plan domain.WorkflowSpec, gates []storage.InputRequest, targets []ReviewTarget) string {
	offered := func(id string) bool {
		return slices.ContainsFunc(targets, func(target ReviewTarget) bool { return target.ID == id })
	}
	for _, gate := range gates {
		if offered(gate.ReviewTarget) {
			return gate.ReviewTarget
		}
	}
	if len(gates) == 0 {
		for _, step := range slices.Backward(plan.Steps) {
			if step.Kind != domain.StepApproval {
				continue
			}
			if offered(step.FeedbackTarget) {
				return step.FeedbackTarget
			}
			break
		}
	}
	if len(targets) == 0 {
		return ""
	}
	return targets[len(targets)-1].ID
}
