package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
	workflowengine "github.com/inkuna-ai/inkuna/internal/workflow"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// Why a run cannot be resumed, where the reason is the run's state.
const (
	resumeBusy      = "this run is still running; resume it once it has stopped"
	resumeReviewing = "this run is paused for review; approve it, or send it back for changes"
	resumeCompleted = "this run completed, so nothing is left to resume; send it back for changes to run more of it"
	resumeNothing   = "every step of this run completed, so nothing is left to resume"
)

// ResumeView is what resuming a run would do now (docs/feedback-loop.md):
// the round it stopped in, the steps it would run again and the steps whose
// results it keeps, in the plan's order, and what is left uncommitted in its
// workspace, which the person keeps or discards. Refusal says why the run
// cannot be resumed, so the view shows Go's reason rather than an action Go
// would refuse.
type ResumeView struct {
	Round int `json:"round"`
	// Stopped says the run failed, was cancelled, or was interrupted: it is
	// a run Resume is for, whether or not it can be resumed now.
	Stopped bool         `json:"stopped"`
	Refusal string       `json:"refusal,omitempty"`
	Rerun   []ResumeStep `json:"rerun"`
	Kept    []ResumeStep `json:"kept"`
	// Workspace is the worktree, or with worktrees off the checkout, the run
	// works in; empty when it stopped before it made one.
	Workspace string                   `json:"workspace,omitempty"`
	Changes   []worktree.PendingChange `json:"changes"`
}

// ResumeStep is a step as the run left it: completed for one whose result a
// resume keeps; failed, cancelled, or interrupted for one that stopped;
// pending for one that never started in the round the run stopped in; and
// edited for one a rerun runs again because the person edited its plan,
// though it had completed.
type ResumeStep struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// ResumeRequest is how a person resumes a run: the round they saw it stop
// in, so a second resume of that round is refused rather than starting
// another, and what to do with what is uncommitted in its workspace. Discard
// throws that away first; Changes are the paths the person saw, and a
// discard is refused when the workspace no longer holds exactly those.
type ResumeRequest struct {
	Round   int      `json:"round"`
	Discard bool     `json:"discard"`
	Changes []string `json:"changes"`
}

// RunResume says what resuming a run would do now, or why it cannot be
// resumed. ResumeWorkflowRun checks all of this again, under the run's lock.
func (service *Service) RunResume(runID string) (ResumeView, error) {
	if err := service.ready(); err != nil {
		return ResumeView{}, err
	}
	ctx := service.context()
	execution, view, err := service.stoppedRun(runID)
	if err != nil || view.Refusal != "" {
		return view, err
	}
	plan, err := service.store.ExecutionPlan(ctx, runID)
	if err != nil {
		view.Refusal = fmt.Sprintf("this run cannot be resumed: %v", err)
		return view, nil
	}
	return service.resumeOutlook(ctx, execution, plan, nil, view)
}

// stoppedRun loads a run once it has settled, for a resume or a rerun, with
// the ResumeView those start from: its round, whether it stopped, and the
// refusal its state alone gives, if any.
func (service *Service) stoppedRun(runID string) (storage.Execution, ResumeView, error) {
	// A run shows as ended a moment before it has stored that, so its stored
	// state is read once it has.
	busy := false
	if err := service.settle(runID); errors.Is(err, errRunBusy) {
		busy = true
	} else if err != nil {
		return storage.Execution{}, ResumeView{}, err
	}
	execution, err := service.store.Execution(service.context(), runID)
	if errors.Is(err, domain.ErrNotFound) {
		return storage.Execution{}, ResumeView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return storage.Execution{}, ResumeView{}, fmt.Errorf("load run %s: %w", runID, err)
	}
	view := ResumeView{Round: max(execution.Round, 1), Rerun: []ResumeStep{}, Kept: []ResumeStep{}, Changes: []worktree.PendingChange{}}
	view.Stopped = !busy && slices.Contains(storage.ResumableStates, execution.State)
	if busy {
		view.Refusal = resumeBusy
	} else {
		view.Refusal = resumeStateRefusal(execution.State)
	}
	return execution, view, nil
}

// resumeOutlook fills in what resuming a run under plan would do now, the
// steps the person edited running again with everything after them, or
// says why it cannot.
func (service *Service) resumeOutlook(ctx context.Context, execution storage.Execution, plan domain.WorkflowSpec, edited []string, view ResumeView) (ResumeView, error) {
	scope, runs, err := service.resumeScope(ctx, execution, plan, edited)
	if err != nil {
		return ResumeView{}, err
	}
	view.Rerun, view.Kept = resumeSteps(execution, plan, runs, scope.Restart, edited)
	if len(scope.Restart) == 0 {
		view.Refusal = resumeNothing
		return view, nil
	}
	resumed, err := service.loadResumption(ctx, execution)
	if err != nil {
		view.Refusal = fmt.Sprintf("this run cannot be resumed: %v", err)
		return view, nil
	}
	if err := service.checkWorkspace(ctx, execution); err != nil {
		view.Refusal = err.Error()
		return view, nil
	}
	if err := service.checkRunnable(execution, plan, scope.Restart, resumed); err != nil {
		view.Refusal = err.Error()
		return view, nil
	}
	if hasWorkspace(execution) {
		view.Workspace = execution.Worktree
		if view.Changes, err = worktree.PendingChanges(ctx, execution.Worktree); err != nil {
			view.Changes = []worktree.PendingChange{}
			view.Refusal = fmt.Sprintf("read the run's worktree: %v", err)
		}
	}
	return view, nil
}

// ResumeWorkflowRun resumes a run that failed, was cancelled, or was
// interrupted, where it stopped: it starts a new round that runs the steps
// that stopped or never started, with everything after them, and keeps the
// commits on the run's branch and the results of the completed steps outside
// that set, on the same worktree under the plan the run was approved with
// (docs/feedback-loop.md). The steps that stopped in a round of feedback get
// that round's items again. What is uncommitted in the workspace is kept for
// the steps that run again, or thrown away first when the person chose to.
// A run it cannot resume is refused with the reason, and nothing changes.
func (service *Service) ResumeWorkflowRun(runID string, request ResumeRequest) (WorkflowRunView, error) {
	if err := service.ready(); err != nil {
		return WorkflowRunView{}, err
	}
	return service.resumeRun(runID, request, nil)
}

// resumeRun is ResumeWorkflowRun, or, given the plan a person approved for
// a rerun, the same resume under that plan: the steps they edited run again
// with everything after them, and the round records the plan it replaced
// (RerunWorkflowRun).
func (service *Service) resumeRun(runID string, request ResumeRequest, approved *domain.WorkflowSpec) (WorkflowRunView, error) {
	// A resume is a person's decision, though the run keeps the approver it
	// recorded when it started.
	principal, err := service.principal(service.context())
	if err != nil {
		return WorkflowRunView{}, err
	}
	// A run shows as ended a moment before it has stored that and cleaned up
	// its worktree; the resume waits for both rather than overlapping them.
	if err := service.settle(runID); errors.Is(err, errRunBusy) {
		return WorkflowRunView{}, errors.New(resumeBusy)
	} else if err != nil {
		return WorkflowRunView{}, err
	}
	service.workspaceMu.Lock()
	defer service.workspaceMu.Unlock()
	ctx := service.context()
	service.runsMu.RLock()
	again := service.runs[runID]
	active := again != nil && !runIdle(again.view.State)
	service.runsMu.RUnlock()
	if active {
		return WorkflowRunView{}, fmt.Errorf("this run was already resumed and is running round %d", again.round)
	}
	execution, err := service.store.Execution(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return WorkflowRunView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return WorkflowRunView{}, err
	}
	if refusal := resumeStateRefusal(execution.State); refusal != "" {
		return WorkflowRunView{}, errors.New(refusal)
	}
	round := max(execution.Round, 1)
	if request.Round != round {
		return WorkflowRunView{}, fmt.Errorf("this run is in round %d, and it was resumed from round %d; reload it", round, request.Round)
	}
	plan, err := service.store.ExecutionPlan(ctx, runID)
	if err != nil {
		return WorkflowRunView{}, fmt.Errorf("resume the run: %w", err)
	}
	// An approval that edits nothing resumes under the plan the run was
	// approved with, as a plain resume does.
	var edit *storage.PlanEdit
	var edited []string
	if approved != nil {
		revised, steps, err := service.approveRerun(ctx, plan, *approved)
		if err != nil {
			return WorkflowRunView{}, err
		}
		if len(steps) > 0 {
			edit = &storage.PlanEdit{Replaced: plan, Plan: revised, Steps: steps}
			plan, edited = revised, steps
		}
	}
	scope, runs, err := service.resumeScope(ctx, execution, plan, edited)
	if err != nil {
		return WorkflowRunView{}, err
	}
	if len(scope.Restart) == 0 {
		return WorkflowRunView{}, errors.New(resumeNothing)
	}
	resumed, err := service.resumable(ctx, execution, plan, scope.Restart, true)
	if err != nil {
		return WorkflowRunView{}, err
	}
	// From here a refusal must give back the checkout resumable claimed.
	refuse := func(err error) (WorkflowRunView, error) {
		service.releaseCheckout(runID)
		return WorkflowRunView{}, err
	}
	changes := []worktree.PendingChange{}
	if hasWorkspace(execution) {
		if changes, err = worktree.PendingChanges(ctx, execution.Worktree); err != nil {
			return refuse(fmt.Errorf("read the run's worktree: %w", err))
		}
	}
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	discard := request.Discard && len(changes) > 0
	if discard && !samePaths(paths, request.Changes) {
		return refuse(errors.New("what is uncommitted in the run's worktree changed since you looked; check it again before discarding it"))
	}
	resumed.notes = resumeNotes(execution, plan, runs, scope.Restart, paths, discard)
	// The steps that stopped in a round of feedback never finished their
	// items, so the new round sends those again.
	feedback, err := roundFeedback(ctx, service.store, execution)
	if err != nil {
		return refuse(err)
	}
	var carry []string
	carried := make(map[string][]storage.FeedbackItem)
	for _, step := range plan.Steps {
		if !scope.Restart[step.ID] || len(feedback[step.ID]) == 0 {
			continue
		}
		carry = append(carry, step.ID)
		// As StartResumeRound stores them in the new round: open again.
		for _, item := range feedback[step.ID] {
			item.Round, item.Status, item.Note = round+1, storage.FeedbackOpen, ""
			carried[step.ID] = append(carried[step.ID], item)
		}
	}
	// Discarded before the round starts, so no step of it ever sees them.
	// Nothing else can start the run meanwhile, as every way to do that
	// holds workspaceMu. A discard git cannot undo, so the changes are kept
	// in a snapshot first, and a resume refused once they are gone puts them
	// back and says so.
	var snapshot worktree.Snapshot
	if discard {
		if snapshot, err = worktree.TakeSnapshot(ctx, execution.Worktree); err != nil {
			return refuse(fmt.Errorf("keep a copy of the run's uncommitted changes before discarding them: %w", err))
		}
		if err := worktree.Discard(ctx, execution.Worktree); err != nil {
			err = fmt.Errorf("discard the run's uncommitted changes: %w", err)
			// A discard refused before it changed anything has nothing to put
			// back.
			var inTheWay *worktree.IgnoredInTheWayError
			if !errors.As(err, &inTheWay) {
				err = putBack(ctx, execution.Worktree, snapshot, err)
			}
			return refuse(err)
		}
	}
	started, err := service.store.StartResumeRound(ctx, runID, round, carry, discard, edit)
	if err == nil && !started {
		err = fmt.Errorf("this run was already resumed from round %d, or it changed state; reload it", round)
	}
	if err != nil {
		if discard {
			err = putBack(ctx, execution.Worktree, snapshot, err)
		}
		return refuse(err)
	}
	decision, summary := "run.resume", "Resumed the run"
	if approved != nil {
		decision, summary = "run.rerun", "Approved a rerun"
	}
	service.auditDecision(decision, principal, execution, summary, map[string]string{"steps": strings.Join(edited, ",")})
	return service.launchRound(execution, resumed, plan, round+1, scope, carried), nil
}

// putBack restores, from its snapshot, what a discard threw away for a
// resume that was then refused, files and index, and adds to the refusal
// whether it could: a refused resume leaves the workspace as it found it, or
// says where the changes are.
func putBack(ctx context.Context, path string, snapshot worktree.Snapshot, refusal error) error {
	if err := worktree.PutBack(ctx, path, snapshot); err != nil {
		return fmt.Errorf("%w; the uncommitted changes were discarded and could not all be put back (%w), but commit %s still holds their files", refusal, err, snapshot.Commit)
	}
	return fmt.Errorf("%w; the uncommitted changes were put back as they were", refusal)
}

// resumeStateRefusal is why a run in state cannot be resumed, or empty when
// it can.
func resumeStateRefusal(state string) string {
	switch {
	case slices.Contains(storage.ResumableStates, state):
		return ""
	case state == storage.Reviewing:
		return resumeReviewing
	case state == "completed":
		return resumeCompleted
	case !terminalRunState(state):
		return resumeBusy
	}
	return fmt.Sprintf("a %s run cannot be resumed", state)
}

// resumeScope is what a resume runs and what it keeps: every step without a
// completed result as the run stands, and every step the person edited for a
// rerun, runs again with everything after it, and every completed step
// outside that set keeps its result. It also returns the run's step runs,
// which say how each step stood when the run stopped.
func (service *Service) resumeScope(ctx context.Context, execution storage.Execution, plan domain.WorkflowSpec, edited []string) (workflowengine.Round, []storage.StepRun, error) {
	prior, err := service.currentResults(ctx, execution, plan)
	if err != nil {
		return workflowengine.Round{}, nil, err
	}
	runs, err := service.store.StepRuns(ctx, execution.ID)
	if err != nil {
		return workflowengine.Round{}, nil, fmt.Errorf("load the step runs of run %s: %w", execution.ID, err)
	}
	completed := make(map[string]bool, len(prior))
	for step := range prior {
		completed[step] = true
	}
	return workflowengine.Round{Prior: prior, Restart: workflowengine.RestartSet(plan, edited, completed)}, runs, nil
}

// stoppedRuns is each step's run in the round the run stopped in.
func stoppedRuns(execution storage.Execution, runs []storage.StepRun) map[string]storage.StepRun {
	round := max(execution.Round, 1)
	stopped := make(map[string]storage.StepRun)
	for _, run := range runs {
		if run.Round == round {
			stopped[run.StepID] = run
		}
	}
	return stopped
}

// stoppedState is how a step's run in the round the run stopped in ended:
// as it recorded, or, for one that never recorded an end, as its run did.
func stoppedState(run storage.StepRun, execution storage.Execution) string {
	if !run.FinishedAt.IsZero() {
		return run.State
	}
	switch execution.State {
	case domain.StoppedFailed, domain.StoppedCancelled:
		return execution.State
	}
	return domain.StoppedInterrupted
}

// resumeSteps lists, in the plan's order, the steps a resume runs again, as
// they stood when the run stopped, and the steps whose results it keeps. A
// step the person edited that had completed, or never started, shows as
// edited.
func resumeSteps(execution storage.Execution, plan domain.WorkflowSpec, runs []storage.StepRun, restart map[string]bool, edited []string) (rerun, kept []ResumeStep) {
	rerun, kept = []ResumeStep{}, []ResumeStep{}
	stopped := stoppedRuns(execution, runs)
	for _, step := range plan.Steps {
		if !restart[step.ID] {
			kept = append(kept, ResumeStep{ID: step.ID, Title: step.Title, State: "completed"})
			continue
		}
		state := "pending"
		if run, ok := stopped[step.ID]; ok {
			state = stoppedState(run, execution)
		}
		if slices.Contains(edited, step.ID) && (state == "pending" || state == domain.HandoffCompleted) {
			state = "edited"
		}
		rerun = append(rerun, ResumeStep{ID: step.ID, Title: step.Title, State: state})
	}
	return rerun, kept
}

// resumeNotes are what each agent step a resume runs again is told of its
// last attempt, for the steps that started in the round the run stopped in
// and did not finish it. Only a step that writes is told what was left
// uncommitted, since only such a step could have left it.
func resumeNotes(execution storage.Execution, plan domain.WorkflowSpec, runs []storage.StepRun, restart map[string]bool, changes []string, discarded bool) map[string]*domain.ResumeNote {
	notes := make(map[string]*domain.ResumeNote)
	stopped := stoppedRuns(execution, runs)
	for _, step := range plan.Steps {
		run, ok := stopped[step.ID]
		if !restart[step.ID] || step.Kind != domain.StepAgent || !ok || run.State == domain.HandoffCompleted {
			continue
		}
		state := stoppedState(run, execution)
		reason := ""
		if state == domain.StoppedFailed {
			reason = run.Summary
		}
		var kept []string
		dropped := false
		if step.Permission == domain.PermissionWorktree {
			if discarded {
				dropped = true
			} else {
				kept = changes
			}
		}
		notes[step.ID] = domain.NewResumeNote(max(execution.Round, 1), state, reason, kept, dropped)
	}
	return notes
}

// samePaths reports whether two lists hold the same paths, in any order.
func samePaths(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left, right = slices.Clone(left), slices.Clone(right)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
