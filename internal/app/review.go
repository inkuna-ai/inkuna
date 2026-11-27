package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/notify"
	"github.com/inkuna-ai/inkuna/internal/skill"
	"github.com/inkuna-ai/inkuna/internal/storage"
	workflowengine "github.com/inkuna-ai/inkuna/internal/workflow"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// stepChangesRequested is the state of a review gate's step run that sent
// the run back. It is not completed, so the gate is never reused: the next
// round pauses there again.
const stepChangesRequested = "changes-requested"

var errRunBusy = errors.New("the run is running")

// runIdle reports whether nothing of a run is running: it has ended, or it
// is paused at a review gate.
func runIdle(state string) bool {
	return terminalRunState(state) || state == storage.Reviewing
}

// settle waits until a run's goroutine has stored its state and cleaned up,
// so whatever is done to the run next never overlaps the end of what it was
// doing. A run that is still running is refused with errRunBusy. It must be
// called before workspaceMu is taken, since that cleanup takes it too.
func (service *Service) settle(runID string) error {
	service.runsMu.RLock()
	live := service.runs[runID]
	busy := live != nil && !runIdle(live.view.State)
	service.runsMu.RUnlock()
	if busy {
		return errRunBusy
	}
	if live != nil && live.done != nil {
		select {
		case <-live.done:
		case <-time.After(30 * time.Second):
			return errors.New("this run is still finishing; try again in a moment")
		}
	}
	return nil
}

// pauseForReview asks the person to review the run at each gate it is held
// at, then leaves it reviewing: nothing of it runs and it holds no task slot
// while it waits, across restarts, for ApproveReview or RequestChanges.
func (service *Service) pauseForReview(ctx context.Context, runID string, spec domain.WorkflowSpec, gates []string) error {
	service.runsMu.RLock()
	run := service.runs[runID]
	var round int
	var worktreePath string
	if run != nil {
		round, worktreePath = run.round, run.view.Worktree
	}
	service.runsMu.RUnlock()
	if run == nil {
		return fmt.Errorf("run %q was not found", runID)
	}
	steps := make(map[string]domain.WorkflowStep, len(spec.Steps))
	for _, step := range spec.Steps {
		steps[step.ID] = step
	}
	var started, raised []string
	// A pause that cannot finish withdraws what it asked, and the run ends
	// failed, or cancelled when that is why.
	fail := func(err error) error {
		state := "failed"
		if errors.Is(err, context.Canceled) {
			state = "cancelled"
		}
		for _, id := range started {
			_ = service.store.FinishRun(context.Background(), id, state, domain.HandoffResult{Version: domain.ContractVersion, Status: state, Summary: err.Error()})
		}
		for _, id := range raised {
			_, _ = service.bus.CancelTask(context.Background(), id)
		}
		if resolveErr := service.store.ResolveReviews(context.Background(), runID, storage.InputCanceled, ""); resolveErr != nil {
			return fmt.Errorf("%w (and its questions could not be withdrawn: %w)", err, resolveErr)
		}
		return err
	}
	for _, id := range gates {
		step := steps[id]
		gateRunID := stepRunID(runID, round, id)
		if err := service.store.StartRun(ctx, gateRunID, runID, spec.ID, id, round, spec.DefaultRuntime, worktreePath); err != nil {
			return fail(fmt.Errorf("start review %s: %w", id, err))
		}
		started = append(started, gateRunID)
		if err := service.store.SetRunState(ctx, gateRunID, storage.Reviewing); err != nil {
			return fail(fmt.Errorf("start review %s: %w", id, err))
		}
		if err := service.appendRunEvent(runID, syntheticEvent(gateRunID, 0, domain.EventLifecycle, "Waiting for review")); err != nil {
			return fail(err)
		}
		task, err := service.bus.SendMessage(ctx, agent.Human, a2a.MessageSendParams{
			Message:  a2a.Message{Role: a2a.RoleUser, ContextID: run.task.ID, Parts: []a2a.Part{a2a.TextPart(reviewQuestion(step, round))}},
			Metadata: map[string]any{"executionId": runID, "stepId": id, "askedBy": "rule:" + spec.RuleName, "review": true, "reviewTarget": step.FeedbackTarget},
		})
		if err != nil {
			return fail(fmt.Errorf("ask for review %s: %w", id, err))
		}
		raised = append(raised, task.ID)
		service.runsMu.Lock()
		run.a2aTasks = append(run.a2aTasks, task.ID)
		service.runsMu.Unlock()
		service.updateStep(runID, id, func(view *StepRunView) {
			view.A2ATaskID = task.ID
			view.State = storage.Reviewing
			view.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
		})
		// The human agent records the question before the task reports it
		// is waiting, so the run never shows reviewing without one.
		if _, err := service.bus.Wait(ctx, task.ID); err != nil {
			return fail(err)
		}
	}
	// Under the lock a cancel reads the state with, so a cancel either
	// stopped the round before it paused or finds it reviewing.
	service.runsMu.Lock()
	if run.ctx.Err() != nil {
		service.runsMu.Unlock()
		return fail(run.ctx.Err())
	}
	run.view.State = storage.Reviewing
	title := run.view.Title
	service.runsMu.Unlock()
	service.persistRun(runID)
	_ = service.dependenciesFor(runID).notifier.Notify(notify.Notification{Title: "Review needed", Body: title, Kind: "review-required", Reference: runID})
	service.emit("inkuna:inbox", runID)
	service.emit("inkuna:runs", runID)
	return nil
}

// reviewQuestion is what the inbox asks at a gate.
func reviewQuestion(step domain.WorkflowStep, round int) string {
	question := fmt.Sprintf("%s, round %d: approve the run", step.Title, round)
	if step.FeedbackTarget == "" {
		return question + ". No stage before this gate writes, so it can only be approved."
	}
	return question + ", or reply with what " + step.FeedbackTarget + " should change."
}

// ApproveReview approves a run paused at review gates, every gate it is
// paused at, and carries it on past them in the same round. A run that is
// not reviewing is refused with the reason, and nothing changes.
func (service *Service) ApproveReview(runID string) (WorkflowRunView, error) {
	if err := service.ready(); err != nil {
		return WorkflowRunView{}, err
	}
	principal, err := service.principal(service.context())
	if err != nil {
		return WorkflowRunView{}, err
	}
	if err := service.settle(runID); errors.Is(err, errRunBusy) {
		return WorkflowRunView{}, errors.New("this run is still running; approve it once it pauses for review")
	} else if err != nil {
		return WorkflowRunView{}, err
	}
	service.workspaceMu.Lock()
	defer service.workspaceMu.Unlock()
	ctx := service.context()
	execution, err := service.store.Execution(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return WorkflowRunView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return WorkflowRunView{}, err
	}
	if execution.State != storage.Reviewing {
		return WorkflowRunView{}, fmt.Errorf("this run is %s, not waiting for review", execution.State)
	}
	plan, err := service.store.ExecutionPlan(ctx, runID)
	if err != nil {
		return WorkflowRunView{}, fmt.Errorf("approve the run: %w", err)
	}
	gates, err := service.store.PendingReviews(ctx, runID)
	if err != nil {
		return WorkflowRunView{}, fmt.Errorf("load the run's reviews: %w", err)
	}
	prior, err := service.currentResults(ctx, execution, plan)
	if err != nil {
		return WorkflowRunView{}, err
	}
	approved := domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted, Summary: "Approved"}
	for _, gate := range gates {
		prior[gate.StepID] = approved
	}
	restart := afterApproval(plan, prior, gates)
	// Read before the run moves on, so a round never carries on without
	// what it was sent back with.
	feedback, err := roundFeedback(ctx, service.store, execution)
	if err != nil {
		return WorkflowRunView{}, err
	}
	resumed, err := service.resumable(ctx, execution, plan, restart, false)
	if err != nil {
		return WorkflowRunView{}, err
	}
	// Every check of Go's has passed; the run has not moved yet.
	moved := false
	err = service.decide(ctx, extension.Decision{Point: extension.PointReviewApprove, Project: execution.ProjectID, Actor: "user", Details: map[string]string{
		"run": runID, "task": execution.TaskID, "workflow": execution.WorkflowID, "round": strconv.Itoa(max(execution.Round, 1)), "gates": policyList(strings.Split(inputSteps(gates), ",")...),
		"approvedBy": execution.ApprovedBy,
	}}, &principal)
	if err == nil {
		moved, err = service.store.ResumeReview(ctx, runID, execution.Round)
	}
	if err == nil && !moved {
		err = errors.New("this run was already answered or cancelled; reload it")
	}
	if err != nil {
		service.releaseCheckout(runID)
		return WorkflowRunView{}, err
	}
	service.closeReviews(ctx, execution, gates, storage.InputAnswered, "Approved", domain.HandoffCompleted, approved)
	// The tracker changes of the steps before the gates are what the person
	// saw beside Approve; they are made apart from the round.
	service.applyApproved(ctx, execution, plan, gates)
	service.auditDecision("review.approve", principal, execution, "Approved the review", map[string]string{"gates": inputSteps(gates)})
	return service.launchRound(execution, resumed, plan, execution.Round, workflowengine.Round{Prior: prior, Restart: restart}, feedback), nil
}

// roundFeedback is what a round was sent back with, by target, so a target
// after the gate still gets its items when the round carries on past it.
func roundFeedback(ctx context.Context, store *storage.Store, execution storage.Execution) (map[string][]storage.FeedbackItem, error) {
	items, err := store.Feedback(ctx, execution.ID, execution.Round)
	if err != nil {
		return nil, fmt.Errorf("load the feedback of round %d: %w", execution.Round, err)
	}
	feedback := make(map[string][]storage.FeedbackItem)
	for _, item := range items {
		feedback[item.Target] = append(feedback[item.Target], item)
	}
	return feedback, nil
}

// resumption is what running more of a paused or ended run takes.
type resumption struct {
	task     domain.TaskSpec
	project  storage.Project
	registry agent.Registry
	packages []skill.Package
	// story is the run's story as it was kept, nil without one.
	story *runStory
	// notes are what each step a resume runs again is told of its own last
	// attempt, by step; nil for any other round.
	notes map[string]*domain.ResumeNote
}

// resumable checks that what a paused or ended run is about to run can run,
// before anything about the run changes, and loads what running it takes.
// Steps that will run need the run's worktree as it was left and the agents
// they were planned with, and with worktrees off they claim the checkout,
// which the caller releases if it then does not start them. A run with
// nothing left to run needs none of that. Only a resume, whose person chose
// what to do with them, runs on uncommitted changes (dirty).
func (service *Service) resumable(ctx context.Context, execution storage.Execution, plan domain.WorkflowSpec, restart map[string]bool, dirty bool) (resumption, error) {
	resumed, err := service.loadResumption(ctx, execution)
	if err != nil {
		return resumption{}, err
	}
	if len(restart) == 0 {
		return resumed, nil
	}
	check := service.checkReopenable
	if dirty {
		check = service.checkWorkspace
	}
	if err := check(ctx, execution); err != nil {
		return resumption{}, err
	}
	if err := service.checkRunnable(execution, plan, restart, resumed); err != nil {
		return resumption{}, err
	}
	// With worktrees off the round writes in the checkout, which only one run
	// may do at a time; claimed before the run changes so a refusal changes
	// nothing.
	if execution.Cleanup == domain.CleanupCheckout {
		if err := service.claimCheckout(resumed.project.Repository, execution.ID); err != nil {
			return resumption{}, err
		}
	}
	return resumed, nil
}

// loadResumption loads the run's task, project, agents, skills, and story.
func (service *Service) loadResumption(ctx context.Context, execution storage.Execution) (resumption, error) {
	var resumed resumption
	var err error
	if resumed.task, err = service.store.Task(ctx, execution.TaskID); err != nil {
		return resumption{}, fmt.Errorf("load the run's task: %w", err)
	}
	if resumed.project, err = service.store.Project(ctx, execution.ProjectID); err != nil {
		return resumption{}, fmt.Errorf("load the run's project: %w", err)
	}
	if err := refuseRemoved(resumed.project); err != nil {
		return resumption{}, err
	}
	if resumed.registry, _, err = service.agentRegistry(resumed.task.Repository); err != nil {
		return resumption{}, fmt.Errorf("load agents: %w", err)
	}
	if resumed.packages, err = skill.Discover(filepath.Join(resumed.task.Repository, ".inkuna", "skills")); err != nil {
		return resumption{}, fmt.Errorf("discover skills: %w", err)
	}
	// A story that cannot be read leaves the round without story updates;
	// the round itself still runs.
	if resumed.story, err = service.loadRunStory(ctx, execution.ID); err != nil {
		log.Printf("inkuna: load the story of run %s: %v", execution.ID, err)
	}
	return resumed, nil
}

// checkRunnable makes sure no other run is working on the run's branch and
// that every agent the steps to run again were planned with is still
// defined: planning checked them, and one removed since is refused now
// rather than halfway through the round.
func (service *Service) checkRunnable(execution storage.Execution, plan domain.WorkflowSpec, restart map[string]bool, resumed resumption) error {
	if execution.Branch != "" {
		if other := service.runOnBranch(resumed.project.ID, execution.Branch, execution.Worktree); other != "" && other != execution.ID {
			return fmt.Errorf("run %s is working on %s; try again once it has ended", other, execution.Branch)
		}
	}
	for _, step := range plan.Steps {
		if _, ok := resumed.registry.Get(step.Agent); restart[step.ID] && step.Kind == domain.StepAgent && !ok {
			return fmt.Errorf("agent %q of step %s is no longer defined", step.Agent, step.ID)
		}
	}
	return nil
}

// closeReviews ends the review gates a run was paused at, once the run has
// moved on from them: each gate's step run finishes as stepState with
// result, its A2A task ends, and its question in the inbox is resolved as
// inputState with answer. The run has already changed state, so a failure
// here is logged rather than undoing it.
func (service *Service) closeReviews(ctx context.Context, execution storage.Execution, gates []storage.InputRequest, inputState, answer, stepState string, result domain.HandoffResult) {
	for _, gate := range gates {
		gateRunID := stepRunID(execution.ID, execution.Round, gate.StepID)
		if err := service.store.FinishRun(ctx, gateRunID, stepState, result); err != nil {
			log.Printf("inkuna: close review %s of run %s: %v", gate.StepID, execution.ID, err)
		}
		_ = service.appendRunEvent(execution.ID, syntheticEvent(gateRunID, 0, domain.EventComplete, result.Summary))
		if gate.A2ATaskID == "" {
			continue
		}
		if err := service.closeGateTask(ctx, gate.A2ATaskID, answer, inputState == storage.InputCanceled); err != nil {
			log.Printf("inkuna: close review task %s of run %s: %v", gate.A2ATaskID, execution.ID, err)
		}
	}
	if err := service.store.ResolveReviews(ctx, execution.ID, inputState, answer); err != nil {
		log.Printf("inkuna: resolve reviews of run %s: %v", execution.ID, err)
	}
	service.emit("inkuna:inbox", execution.ID)
}

// closeGateTask ends a review gate's A2A task, completed with what the
// person decided or cancelled with the run. A gate asked before Inkuna
// restarted is no longer on the bus, so it is put back first.
func (service *Service) closeGateTask(ctx context.Context, taskID, answer string, cancelled bool) error {
	service.ensureBus()
	if _, err := service.bus.GetTask(taskID, 0); errors.Is(err, a2a.ErrUnknownTask) {
		stored, err := service.store.A2ATask(ctx, taskID)
		if err != nil {
			return err
		}
		if err := service.bus.Restore(stored); err != nil {
			return err
		}
	}
	if cancelled {
		_, err := service.bus.CancelTask(ctx, taskID)
		return err
	}
	if err := service.bus.AddArtifact(ctx, taskID, a2a.Artifact{Name: "answer", Description: "Answer from the user", Parts: []a2a.Part{a2a.TextPart(answer)}}); err != nil {
		return err
	}
	return service.bus.UpdateStatus(ctx, taskID, a2a.StateCompleted, &a2a.Message{Role: a2a.RoleUser, Parts: []a2a.Part{a2a.TextPart(answer)}})
}

// cancelReview cancels a run paused at review gates: there is nothing to
// stop, so the run ends as cancelled at once and its gates close. A run that
// was approved or sent back meanwhile is cancelled as the running run it
// now is.
func (service *Service) cancelReview(runID string) (WorkflowRunView, error) {
	if err := service.settle(runID); err != nil && !errors.Is(err, errRunBusy) {
		return WorkflowRunView{}, err
	}
	service.workspaceMu.Lock()
	ctx := service.context()
	cancelled, err := service.store.CancelReview(ctx, runID)
	if err != nil {
		service.workspaceMu.Unlock()
		return WorkflowRunView{}, err
	}
	if !cancelled {
		service.workspaceMu.Unlock()
		// An Approve or Request changes got there first, and the round it
		// started is cancelled as any running round is.
		service.runsMu.RLock()
		live := service.runs[runID]
		stale := live != nil && live.view.State == storage.Reviewing
		service.runsMu.RUnlock()
		if stale {
			return WorkflowRunView{}, errors.New("the run's state changed while cancelling; reload it and try again")
		}
		return service.cancelLive(runID)
	}
	execution, err := service.store.Execution(ctx, runID)
	if err != nil {
		service.workspaceMu.Unlock()
		return WorkflowRunView{}, err
	}
	gates, err := service.store.PendingReviews(ctx, runID)
	if err != nil {
		log.Printf("inkuna: load reviews of cancelled run %s: %v", runID, err)
	}
	service.closeReviews(ctx, execution, gates, storage.InputCanceled, "", "cancelled", domain.HandoffResult{Version: domain.ContractVersion, Status: "cancelled", Summary: "Review cancelled"})
	service.finishRun(runID, "cancelled", "")
	service.reportStory(runID)
	// Cleanup removes the worktree under the listener policy, and takes
	// workspaceMu to do it.
	service.workspaceMu.Unlock()
	if project, err := service.store.Project(ctx, execution.ProjectID); err == nil {
		service.cleanupAfterRun(runID, project)
	}
	return service.WorkflowRun(runID)
}

// answerReview is a reply to a review gate in the inbox: it requests
// changes from the gate's target with the reply as the feedback.
func (service *Service) answerReview(input storage.InputRequest, answer string) (InputView, error) {
	if input.ReviewTarget == "" {
		return InputView{}, errors.New("this gate can only be approved, since no stage before it writes; approve it, or cancel the run")
	}
	run, err := service.requestChanges(input.ExecutionID, FeedbackRequest{Items: []FeedbackRequestItem{{Target: input.ReviewTarget, Text: answer}}}, input.ID)
	if err != nil {
		return InputView{}, err
	}
	resolved, err := service.store.Input(service.context(), input.ID)
	if err != nil {
		return InputView{}, err
	}
	return InputView{InputRequest: resolved, Permissions: resolved.Permissions, RunTitle: run.Title}, nil
}

// reviewAnswer is how a request for changes reads in the inbox, item by
// item.
func reviewAnswer(items []storage.FeedbackItem) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, item.Target+": "+item.Text)
	}
	return strings.Join(lines, "\n")
}
