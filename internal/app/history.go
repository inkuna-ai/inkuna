package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
	workflowengine "github.com/inkuna-ai/inkuna/internal/workflow"
)

// RoundView is one round of a run as SQLite keeps it, so the run detail can
// show any round, and a run no longer in memory still shows its steps and
// timeline.
type RoundView struct {
	Round int `json:"round"`
	// Since is the commit the round built on, which the Changes view shows
	// what changed after: the run's last commit from an earlier round, else
	// the commit its workspace started from. It is empty for round 1 and
	// for a run that recorded neither.
	Since      string `json:"since,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	// Steps is every step of the run's plan as the round left it: its run
	// in this round, or else its latest run from an earlier round, whose
	// Round says which, or else pending.
	Steps []StepRunView `json:"steps"`
	// Events are the round's latest events, its steps' and their peers', in
	// the order they happened; HiddenEvents counts the earlier ones left
	// out.
	Events       []domain.RunEvent `json:"events"`
	HiddenEvents int               `json:"hiddenEvents,omitempty"`
	// Feedback is what the run was sent back into this round with, and how
	// each target replied. A resumed round sends again what the steps that
	// stopped were sent and never finished.
	Feedback []storage.FeedbackItem `json:"feedback"`
	// Resumed says a person started the round by resuming the run after it
	// stopped, and Discarded that they threw away what the stopped steps had
	// left uncommitted.
	Resumed   bool `json:"resumed,omitempty"`
	Discarded bool `json:"discarded,omitempty"`
	// Edited names the steps whose plan the person edited when they resumed
	// the run into this round, which it ran again under the edited plan.
	Edited []string `json:"edited,omitempty"`
}

// RunRounds lists a run's rounds, oldest first, as SQLite keeps them.
func (service *Service) RunRounds(runID string) ([]RoundView, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	ctx := service.context()
	execution, err := service.execution(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}
	history, err := service.loadHistory(ctx, execution)
	if err != nil {
		return nil, err
	}
	rounds := make([]RoundView, 0, history.last)
	for round := 1; round <= history.last; round++ {
		view, err := history.round(ctx, service.store, round)
		if err != nil {
			return nil, err
		}
		rounds = append(rounds, view)
	}
	return rounds, nil
}

// runHistory is what a run's rounds are built from.
type runHistory struct {
	execution storage.Execution
	plan      []domain.WorkflowStep
	// plans are the plans rounds before a resume under an edited plan ran
	// under, by round; any other round ran under plan.
	plans    map[int][]domain.WorkflowStep
	runs     []storage.StepRun
	feedback []storage.FeedbackItem
	commits  []storage.CommitRecord
	// resumes are the rounds a person resumed the run into, whether they
	// discarded what was left uncommitted, and the steps they edited.
	resumes map[int]storage.ResumedRound
	// last is the latest round, the run's own or the latest a step ran in.
	last int
}

func (service *Service) loadHistory(ctx context.Context, execution storage.Execution) (runHistory, error) {
	history := runHistory{execution: execution, last: max(execution.Round, 1)}
	// A run from before plans were kept, whose task was planned again
	// since, has no plan to name its steps; its rows still list them.
	if plan, err := service.store.ExecutionPlan(ctx, execution.ID); err == nil {
		history.plan = plan.Steps
	}
	var err error
	if history.runs, err = service.store.StepRuns(ctx, execution.ID); err != nil {
		return runHistory{}, fmt.Errorf("load the run's steps: %w", err)
	}
	if history.feedback, err = service.store.RunFeedback(ctx, execution.ID); err != nil {
		return runHistory{}, fmt.Errorf("load the run's feedback: %w", err)
	}
	if history.commits, err = service.store.Commits(ctx, execution.ID); err != nil {
		return runHistory{}, fmt.Errorf("load the run's commits: %w", err)
	}
	if history.resumes, err = service.store.ResumedRounds(ctx, execution.ID); err != nil {
		return runHistory{}, err
	}
	for _, run := range history.runs {
		history.last = max(history.last, run.Round)
	}
	// Only a resume under an edited plan replaces a run's plan, so the rounds
	// before one ran under the plan it replaced.
	edited := 0
	for round, resumed := range history.resumes {
		if len(resumed.Edited) > 0 {
			edited = max(edited, round)
		}
	}
	for round := 1; round < edited; round++ {
		plan, err := service.store.RoundPlan(ctx, execution.ID, round)
		if err != nil {
			return runHistory{}, err
		}
		if history.plans == nil {
			history.plans = make(map[int][]domain.WorkflowStep)
		}
		history.plans[round] = plan.Steps
	}
	return history, nil
}

// planOf is the plan round ran under.
func (history runHistory) planOf(round int) []domain.WorkflowStep {
	if steps, ok := history.plans[round]; ok {
		return steps
	}
	return history.plan
}

// edited is, for each round a resume under an edited plan started, the
// steps the person edited.
func (history runHistory) edited() map[int][]string {
	return storage.EditedRounds(history.resumes)
}

// round builds one round from the history, reading its events.
func (history runHistory) round(ctx context.Context, store *storage.Store, round int) (RoundView, error) {
	view := RoundView{Round: round, Steps: history.steps(round), Feedback: []storage.FeedbackItem{}}
	resumed, ok := history.resumes[round]
	view.Resumed, view.Discarded, view.Edited = ok, resumed.Discarded, resumed.Edited
	events, hidden, err := store.RoundEvents(ctx, history.execution.ID, round, liveRunEventLimit)
	if err != nil {
		return RoundView{}, fmt.Errorf("load the events of round %d: %w", round, err)
	}
	view.Events, view.HiddenEvents = events, hidden
	for _, item := range history.feedback {
		if item.Round == round {
			view.Feedback = append(view.Feedback, item)
		}
	}
	if round > 1 {
		view.Since = history.execution.BaseCommit
		// Commits are oldest first, so the last one before the round is
		// the tip Inkuna left before it.
		for _, commit := range history.commits {
			if commit.Round < round {
				view.Since = commit.Hash
			}
		}
	}
	var started, finished time.Time
	ended := true
	for _, run := range history.runs {
		if run.Round != round {
			continue
		}
		if started.IsZero() || run.StartedAt.Before(started) {
			started = run.StartedAt
		}
		if run.FinishedAt.IsZero() {
			ended = false
		} else if run.FinishedAt.After(finished) {
			finished = run.FinishedAt
		}
	}
	if !started.IsZero() {
		view.StartedAt = started.Format(time.RFC3339Nano)
	}
	// A round is over once a later one started or the run ended.
	if !finished.IsZero() && ended && (round < history.last || terminalRunState(history.execution.State)) {
		view.FinishedAt = finished.Format(time.RFC3339Nano)
	}
	return view, nil
}

// steps is every step as round left it, in the plan's order, then any step
// the plan no longer names.
func (history runHistory) steps(round int) []StepRunView {
	latest := make(map[string]storage.StepRun)
	var order []string
	for _, run := range history.runs {
		if run.Round > round {
			continue
		}
		if _, seen := latest[run.StepID]; !seen {
			order = append(order, run.StepID)
		}
		latest[run.StepID] = run
	}
	rerun := history.restart(round)
	plan := history.planOf(round)
	views := make([]StepRunView, 0, len(plan))
	planned := make(map[string]bool, len(plan))
	for _, step := range plan {
		planned[step.ID] = true
		view := StepRunView{ID: step.ID, Title: step.Title, Kind: step.Kind, Agent: step.Agent, Runtime: step.Runtime, State: "pending", Round: round, From: step.From.Clone()}
		if step.ModelSelection != nil {
			view.Model = step.ModelSelection.Model
		}
		// A step the round was to run again but never started, as when the
		// round failed before it, is pending in the round, not kept.
		if run, ok := latest[step.ID]; ok && (run.Round == round || !rerun[step.ID]) {
			history.fill(&view, run)
		}
		views = append(views, view)
	}
	for _, id := range order {
		if !planned[id] {
			view := StepRunView{ID: id, Title: id}
			history.fill(&view, latest[id])
			views = append(views, view)
		}
	}
	return views
}

// restart is the steps a round after the first was to run, as RequestChanges
// chose them from how the run stood before it. Round 1 runs every step.
func (history runHistory) restart(round int) map[string]bool {
	if round < 2 || len(history.plan) == 0 {
		return nil
	}
	plan := domain.WorkflowSpec{Steps: history.plan}
	edited := history.edited()
	return roundRestart(plan, history.feedback, edited, standing(plan, history.runs, history.feedback, edited, round-1), round)
}

// standing reports which steps hold a completed result as a run stands at
// the end of round: those whose run in the latest round that ran them
// completed, unless a round since was to run them again. A step a round was
// to run but never started, because the round failed, was cancelled, or is
// paused at a gate before it, holds none, even when an earlier round's run
// of it completed, since that run checked or built on work the round has
// changed. edited is the steps a resume under an edited plan ran again, by
// the round it started (storage.EditedRounds).
func standing(plan domain.WorkflowSpec, runs []storage.StepRun, feedback []storage.FeedbackItem, edited map[int][]string, round int) map[string]bool {
	completed := make(map[string]bool, len(plan.Steps))
	for current := 1; current <= round; current++ {
		restart := roundRestart(plan, feedback, edited, completed, current)
		ran := make(map[string]string)
		for _, run := range runs {
			if run.Round == current {
				ran[run.StepID] = run.State
			}
		}
		next := make(map[string]bool, len(plan.Steps))
		for _, step := range plan.Steps {
			if state, ok := ran[step.ID]; ok {
				next[step.ID] = state == "completed"
			} else if !restart[step.ID] {
				next[step.ID] = completed[step.ID]
			}
		}
		completed = next
	}
	return completed
}

// roundRestart is the steps a round runs, given which held a completed
// result before it: its targets, which are the steps it was sent feedback
// for or, after a resume under an edited plan, the steps the person edited;
// every step without a completed result; and everything after those, which
// in round 1 is every step.
func roundRestart(plan domain.WorkflowSpec, feedback []storage.FeedbackItem, edited map[int][]string, completed map[string]bool, round int) map[string]bool {
	targets := slices.Clone(edited[round])
	for _, item := range feedback {
		if item.Round == round && !slices.Contains(targets, item.Target) {
			targets = append(targets, item.Target)
		}
	}
	return workflowengine.RestartSet(plan, targets, completed)
}

// fill shows a step's run on its step. A run that never ended, in a round
// that is over, ended with its run, as the live view shows it, or was cut
// short by a restart.
func (history runHistory) fill(view *StepRunView, run storage.StepRun) {
	view.Round, view.State, view.Summary = run.Round, run.State, truncateSummary(run.Summary, 280)
	if run.Runtime != "" {
		view.Runtime = run.Runtime
	}
	view.StartedAt = run.StartedAt.Format(time.RFC3339Nano)
	if !run.FinishedAt.IsZero() {
		view.FinishedAt = run.FinishedAt.Format(time.RFC3339Nano)
		return
	}
	switch {
	case run.Round < history.last:
		view.State = "interrupted"
	case terminalRunState(history.execution.State):
		view.State = history.execution.State
	}
}

// latestRounds is the round of each step's latest run, which the live view
// shows a reused result as kept from.
func latestRounds(runs []storage.StepRun) map[string]int {
	rounds := make(map[string]int, len(runs))
	for _, run := range runs {
		rounds[run.StepID] = max(rounds[run.StepID], run.Round)
	}
	return rounds
}
