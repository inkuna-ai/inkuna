package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
	workflowengine "github.com/inkuna-ai/inkuna/internal/workflow"
)

// checkTail is how much of a failed check's kept log its feedback item
// carries: the end, where a failure is reported, from the start of a line
// (docs/feedback-loop.md). The whole log stays the check's.
const checkTail = 4 << 10

// ansiEscape matches the terminal escapes a check keeps its colours with,
// which a target is sent without.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?|\x1b[@-Z\\\\-_]")

// The answers a check pause offers, in the order Messages shows them.
const (
	pauseRerun = "Re-run checks"
	pauseSend  = "Send to the writer"
	pauseFail  = "Fail the run"
)

// stageFailure is an opt-in evaluation stage that failed in a round, with
// its checks that failed.
type stageFailure struct {
	step   domain.WorkflowStep
	checks []storage.CheckRecord
}

// failedCheck is a check record as a feedback item and a pause report it.
func failedCheck(check storage.CheckRecord) domain.FailedCheck {
	return domain.FailedCheck{Command: check.Argv, ExitCode: check.ExitCode, Error: check.Error}
}

// system lists the stage's failed checks that only a person can fix, by
// its rule's feedback.systemExitCodes; with none named, there are none.
func (failure stageFailure) system() []storage.CheckRecord {
	var system []storage.CheckRecord
	for _, check := range failure.checks {
		if failedCheck(check).System(failure.step.SystemExitCodes) {
			system = append(system, check)
		}
	}
	return system
}

// checkLoop decides what a round that failed does next. When every step
// that failed is an evaluation stage whose rule sends its failures back
// (onFailure: feedback), and each failed on its checks, it starts the next
// round in place, with an item per failed check for its stage's target,
// and returns what that round runs. Otherwise it returns err for the run to
// fail with, or, when one more round would take a failed stage past its
// feedback.maxRounds, an error that names the stage and its cap.
//
// A failed check the writer cannot fix, by its stage's
// feedback.systemExitCodes, first pauses the run for the person, and their
// answer decides: re-run the stages' checks in a round that sends nothing
// back and is not automatic, send every failure back as above, or fail.
func (service *Service) checkLoop(ctx context.Context, runID string, spec domain.WorkflowSpec, err error) (workflowengine.Round, bool, error) {
	var failed *workflowengine.FailedError
	if !errors.As(err, &failed) || ctx.Err() != nil {
		return workflowengine.Round{}, false, err
	}
	steps := make(map[string]domain.WorkflowStep, len(spec.Steps))
	for _, step := range spec.Steps {
		steps[step.ID] = step
	}
	// A failure in a stage that did not opt in fails the run as it always
	// did, even beside one that did.
	for _, id := range failed.Steps {
		if step := steps[id]; step.Kind != domain.StepEvaluation || step.OnFailure != domain.FailureFeedback || step.FeedbackTarget == "" {
			return workflowengine.Round{}, false, err
		}
	}
	service.runsMu.RLock()
	run := service.runs[runID]
	round := 0
	if run != nil {
		round = run.round
	}
	service.runsMu.RUnlock()
	if run == nil {
		return workflowengine.Round{}, false, err
	}
	var failures []stageFailure
	for _, step := range spec.Steps {
		if !slices.Contains(failed.Steps, step.ID) {
			continue
		}
		checks, checksErr := service.store.StepRunChecks(ctx, stepRunID(runID, round, step.ID))
		if checksErr != nil {
			return workflowengine.Round{}, false, fmt.Errorf("%w (and its checks could not be read to send them back: %w)", err, checksErr)
		}
		failure := stageFailure{step: step}
		for _, check := range checks {
			if check.Status == storage.CheckFailed {
				failure.checks = append(failure.checks, check)
			}
		}
		// A stage that failed without a failed check, as when its inputs
		// could not be written, has nothing a target could fix.
		if len(failure.checks) == 0 {
			return workflowengine.Round{}, false, err
		}
		failures = append(failures, failure)
	}
	if paused := slices.ContainsFunc(failures, func(failure stageFailure) bool { return len(failure.system()) > 0 }); paused {
		answer, pauseErr := service.pauseForChecks(ctx, run, spec, round, failures)
		if pauseErr != nil {
			if ctx.Err() != nil {
				return workflowengine.Round{}, false, err
			}
			return workflowengine.Round{}, false, fmt.Errorf("%w (and the run could not pause for you: %w)", err, pauseErr)
		}
		switch answer {
		case pauseRerun:
			return service.rerunChecks(ctx, runID, spec, round, failures, err)
		case pauseFail:
			return workflowengine.Round{}, false, fmt.Errorf("%w; you chose to fail the run rather than re-run its checks", err)
		case pauseSend:
			// Every failure goes back below, as a code failure.
		default:
			// AnswerInput takes only the pause's own answers, so this is a
			// bug; nothing is sent back on a guess.
			return workflowengine.Round{}, false, fmt.Errorf("%w (and the pause was answered %q, which it does not offer)", err, answer)
		}
	}
	var items []storage.FeedbackItem
	var caps []storage.StageCap
	for _, failure := range failures {
		step := failure.step
		for _, check := range failure.checks {
			items = append(items, storage.FeedbackItem{
				ID: fmt.Sprintf("k%d", len(items)+1), Target: step.FeedbackTarget, From: storage.FeedbackFromCheck,
				Text: strings.Join(check.Argv, " ") + " " + failedCheck(check).Ended(), CheckID: check.ID, Tail: service.checkTail(check),
			})
		}
		caps = append(caps, storage.StageCap{Stage: step.ID, Max: max(step.MaxRounds, 1)})
	}
	// What the next round runs is read before it is stored, so a read that
	// fails never leaves a round with no run.
	scope, scopeErr := service.nextRound(ctx, runID, spec, round, items)
	if scopeErr != nil {
		return workflowengine.Round{}, false, fmt.Errorf("%w (and its failures could not be sent back: %w)", err, scopeErr)
	}
	started, startErr := service.store.StartCheckRound(ctx, runID, round, items, caps)
	switch {
	case startErr != nil:
		return workflowengine.Round{}, false, fmt.Errorf("%w (and its failures could not be sent back: %w)", err, startErr)
	case started.Over != "":
		step := steps[started.Over]
		return workflowengine.Round{}, false, fmt.Errorf("%w; %s has sent its failures back %d times, its cap (feedback.maxRounds), so the run stops", err, step.ID, max(step.MaxRounds, 1))
	case !started.Started:
		return workflowengine.Round{}, false, err
	}
	next := round + 1
	// The round said what it sent back on the stage that failed, before the
	// run moves on to the next.
	for index, stage := range caps {
		step := failures[index].step
		checks := "a failed check"
		if sent := len(failures[index].checks); sent > 1 {
			checks = fmt.Sprintf("%d failed checks", sent)
		}
		_ = service.appendRunEvent(runID, syntheticEvent(stepRunID(runID, round, step.ID), 0, domain.EventMessage,
			fmt.Sprintf("Round %d sends %s back to %s (automatic round %d of %d)", next, checks, step.FeedbackTarget, started.Used[step.ID], stage.Max)))
	}
	service.loopRound(ctx, runID, next, items, scope)
	return scope, true, nil
}

// rerunChecks starts the round a person asked for at a check pause: it
// runs the failed stages' checks again, against the worktree as it is now,
// and everything after them, and sends nothing back. It is not an automatic
// round, so it counts toward no stage's feedback.maxRounds, and since a
// person only re-ran the checks it does not reset those counts either.
func (service *Service) rerunChecks(ctx context.Context, runID string, spec domain.WorkflowSpec, round int, failures []stageFailure, err error) (workflowengine.Round, bool, error) {
	scope, scopeErr := service.nextRound(ctx, runID, spec, round, nil)
	if scopeErr != nil {
		return workflowengine.Round{}, false, fmt.Errorf("%w (and its checks could not be run again: %w)", err, scopeErr)
	}
	started, startErr := service.store.StartCheckRound(ctx, runID, round, nil, nil)
	switch {
	case startErr != nil:
		return workflowengine.Round{}, false, fmt.Errorf("%w (and its checks could not be run again: %w)", err, startErr)
	case !started.Started:
		return workflowengine.Round{}, false, err
	}
	next := round + 1
	for _, failure := range failures {
		_ = service.appendRunEvent(runID, syntheticEvent(stepRunID(runID, round, failure.step.ID), 0, domain.EventMessage,
			fmt.Sprintf("Round %d runs the checks of %s again, as you chose; it sends nothing back and is not an automatic round", next, failure.step.ID)))
	}
	service.loopRound(ctx, runID, next, nil, scope)
	return scope, true, nil
}

// pauseForChecks asks the person what a round whose checks failed in a way
// the writer cannot fix does next, and waits for the answer: one question,
// in Messages, on the first stage with such a failure, naming each one's
// command, how it ended, and the end of its log, and the code failures
// beside them, which wait for the answer since a broken environment may
// have caused them too. The run is waiting meanwhile, as for any question,
// and holds its task slot; a restart interrupts it, and resuming it runs
// the checks again.
func (service *Service) pauseForChecks(ctx context.Context, run *liveExecution, spec domain.WorkflowSpec, round int, failures []stageFailure) (string, error) {
	runID := run.view.ID
	var stage string
	var stages, targets []string
	system, waiting := 0, 0
	var about strings.Builder
	for _, failure := range failures {
		step := failure.step
		broken := failure.system()
		if len(broken) == 0 {
			continue
		}
		if stage == "" {
			stage = step.ID
		}
		stages = append(stages, step.ID)
		if !slices.Contains(targets, step.FeedbackTarget) {
			targets = append(targets, step.FeedbackTarget)
		}
		for _, check := range broken {
			system++
			fmt.Fprintf(&about, "%s: %s %s\n", step.ID, strings.Join(check.Argv, " "), failedCheck(check).Ended())
			if tail := strings.TrimSpace(service.checkTail(check)); tail != "" {
				about.WriteString(tail + "\n")
			}
			about.WriteString("\n")
		}
	}
	for _, failure := range failures {
		for _, check := range failure.checks {
			if failedCheck(check).System(failure.step.SystemExitCodes) {
				continue
			}
			if waiting == 0 {
				about.WriteString("Waiting for your answer, since the environment may have caused them too:\n")
			}
			waiting++
			fmt.Fprintf(&about, "%s: %s %s\n", failure.step.ID, strings.Join(check.Argv, " "), failedCheck(check).Ended())
		}
	}
	checks := "a check"
	if system > 1 {
		checks = fmt.Sprintf("%d checks", system)
	}
	// The human agent puts the question on the stage's timeline as it
	// raises it, which says the run paused and why.
	question := fmt.Sprintf("Round %d: %s of %s failed in a way %s cannot fix. Fix what broke, then re-run the checks; or send the failures to %s anyway, or fail the run.",
		round, checks, joinNames(stages), joinNames(targets), joinNames(targets))
	task, err := service.bus.SendMessage(ctx, agent.Human, a2a.MessageSendParams{
		Message: a2a.Message{Role: a2a.RoleUser, ContextID: run.task.ID, Parts: []a2a.Part{
			a2a.TextPart(question),
			// As JSON would decode them, which is how the human agent reads a
			// question's options.
			a2a.DataPart(map[string]any{"options": []any{pauseRerun, pauseSend, pauseFail}, "context": strings.TrimSpace(about.String())}),
		}},
		Metadata: map[string]any{"executionId": runID, "stepId": stage, "askedBy": "rule:" + spec.RuleName, "checkPause": true},
	})
	if err != nil {
		return "", fmt.Errorf("ask what to do about the checks: %w", err)
	}
	service.runsMu.Lock()
	run.a2aTasks = append(run.a2aTasks, task.ID)
	service.runsMu.Unlock()
	service.updateStep(runID, stage, func(view *StepRunView) { view.A2ATaskID = task.ID })
	final, err := service.bus.WaitTerminal(ctx, task.ID)
	if err != nil {
		_, _ = service.bus.CancelTask(context.Background(), task.ID)
		return "", err
	}
	if final.Status.State != a2a.StateCompleted {
		return "", fmt.Errorf("the question about the checks was %s", final.Status.State)
	}
	if err := service.confirmAnswered(ctx, task.ID); err != nil {
		return "", err
	}
	answer := ""
	for _, artifact := range final.Artifacts {
		for _, part := range artifact.Parts {
			if part.Kind == a2a.PartText {
				answer = part.Text
			}
		}
	}
	// The run carries on from here whatever the answer, and the stage is
	// failed in this round; the answer's own bookkeeping may not have
	// caught up with the task yet.
	service.updateRun(runID, func(view *WorkflowRunView) {
		if view.State == "waiting" {
			view.State = "running"
		}
		for index := range view.Steps {
			if view.Steps[index].ID == stage {
				view.Steps[index].State = "failed"
			}
		}
	})
	service.persistRun(runID)
	return answer, nil
}

// joinNames lists names in prose: "a", "a and b", "a, b, and c".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
}

// nextRound is what a round a check loop starts after round runs again and
// what it keeps, chosen as RequestChanges chooses them: the targets of its
// items, every step without a completed result, and everything after them.
func (service *Service) nextRound(ctx context.Context, runID string, spec domain.WorkflowSpec, round int, items []storage.FeedbackItem) (workflowengine.Round, error) {
	prior, err := service.currentResults(ctx, storage.Execution{ID: runID, Round: round}, spec)
	if err != nil {
		return workflowengine.Round{}, err
	}
	completed := make(map[string]bool, len(prior))
	for step := range prior {
		completed[step] = true
	}
	var targets []string
	for _, item := range items {
		if !slices.Contains(targets, item.Target) {
			targets = append(targets, item.Target)
		}
	}
	return workflowengine.Round{Prior: prior, Restart: workflowengine.RestartSet(spec, targets, completed)}, nil
}

// loopRound moves the live run into the round a check loop started, as
// launchRound sets up a new round: the view shows it, with the steps it
// keeps as kept, and a step it runs again holds nothing from before until
// it does.
func (service *Service) loopRound(ctx context.Context, runID string, next int, items []storage.FeedbackItem, scope workflowengine.Round) {
	feedback := make(map[string][]storage.FeedbackItem)
	for _, item := range items {
		item.Round = next
		feedback[item.Target] = append(feedback[item.Target], item)
	}
	kept := map[string]int{}
	if runs, err := service.store.StepRuns(ctx, runID); err == nil {
		kept = latestRounds(runs)
	}
	results := make(map[string]domain.HandoffResult, len(scope.Prior))
	for step, result := range scope.Prior {
		results[step] = result
	}
	// A step that runs again may ask again, so what it asked before is not
	// made.
	service.discardTrackerChanges(ctx, runID, scope.Restart, next)
	service.runsMu.Lock()
	if run := service.runs[runID]; run != nil {
		run.round, run.feedback, run.results, run.resumed = next, feedback, results, nil
		run.view.Round, run.view.Error = next, ""
		run.view.Events = make([]domain.RunEvent, 0)
		for index, step := range run.view.Steps {
			if scope.Restart[step.ID] {
				run.view.Steps[index] = StepRunView{ID: step.ID, Title: step.Title, Kind: step.Kind, Agent: step.Agent, Runtime: step.Runtime, Model: step.Model, State: "pending", Round: next, From: step.From}
				delete(run.contexts, step.ID)
				delete(run.outbound, step.ID)
			} else if from := kept[step.ID]; from > 0 {
				run.view.Steps[index].Round = from
			}
		}
	}
	service.runsMu.Unlock()
	service.persistRun(runID)
	service.emit("inkuna:runs", runID)
}

// checkTail is the end of a failed check's kept log, without its colours,
// as its feedback item sends it. A log that cannot be read sends nothing
// more; the item still names the command and how it ended.
func (service *Service) checkTail(check storage.CheckRecord) string {
	if check.LogDigest == "" {
		return ""
	}
	text, err := service.readCheckLog(check)
	if err != nil {
		return ""
	}
	return lastBytes(ansiEscape.ReplaceAllString(text, ""), checkTail)
}
