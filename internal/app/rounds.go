package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/skill"
	"github.com/inkuna-ai/inkuna/internal/storage"
	workflowengine "github.com/inkuna-ai/inkuna/internal/workflow"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// cancelledRefusal is why a cancelled run is not sent back for changes.
const cancelledRefusal = "a cancelled run cannot be sent back; resume it, or plan the task again"

// Feedback a person sends is held to these sizes, which bound what a request
// stores. What a target is sent of it is bounded by the feedback budget,
// which cuts an item too long for its share and never drops one.
const (
	maxFeedbackItemBytes = 16 << 10
	maxFeedbackBytes     = 32 << 10
)

// FeedbackRequest is what a person sends a run back with: what to fix, by
// target step, and the round they reviewed, so a second send for that round
// is refused instead of starting another.
type FeedbackRequest struct {
	Round int                   `json:"round"`
	Items []FeedbackRequestItem `json:"items"`
}

// FeedbackRequestItem is one thing to fix and the step to fix it.
type FeedbackRequestItem struct {
	Target string `json:"target"`
	Text   string `json:"text"`
}

// RequestChanges sends a run that ended or is paused at a review gate back
// to the steps that wrote it, with what to fix, and runs those steps and
// every step after them again on the same branch, reusing every other
// step's result (docs/feedback-loop.md). A run it cannot reopen is refused
// with the reason, and nothing changes.
func (service *Service) RequestChanges(runID string, request FeedbackRequest) (WorkflowRunView, error) {
	return service.requestChanges(runID, request, "")
}

// requestChanges is RequestChanges, or, given a review question, a reply to
// it: that review must still be open, with the run paused at it, when the
// round changes, and the reply is for the round the run is paused in.
func (service *Service) requestChanges(runID string, request FeedbackRequest, review string) (WorkflowRunView, error) {
	if err := service.ready(); err != nil {
		return WorkflowRunView{}, err
	}
	principal, err := service.principal(service.context())
	if err != nil {
		return WorkflowRunView{}, err
	}
	// A run shows as ended a moment before it has stored that and cleaned up
	// its worktree; the next round waits for both rather than overlapping
	// them.
	if err := service.settle(runID); errors.Is(err, errRunBusy) {
		return WorkflowRunView{}, errors.New("this run is still running; send changes once it has ended or paused for review")
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
		return WorkflowRunView{}, fmt.Errorf("this run was already sent back and is running round %d", again.round)
	}
	execution, err := service.store.Execution(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return WorkflowRunView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return WorkflowRunView{}, err
	}
	reviewing := execution.State == storage.Reviewing
	// Every way out of reviewing holds workspaceMu while it closes the
	// run's reviews, so a review still open here is the one the run is
	// paused at.
	if review != "" {
		input, err := service.store.Input(ctx, review)
		if err != nil {
			return WorkflowRunView{}, err
		}
		if !reviewing || input.ExecutionID != runID || input.State != storage.InputPending {
			return WorkflowRunView{}, errors.New("this review was already answered or the run moved on; reload it")
		}
		request.Round = execution.Round
	}
	switch {
	case execution.State == "cancelled":
		return WorkflowRunView{}, errors.New(cancelledRefusal)
	case !reviewing && !slices.Contains(storage.ReopenableStates, execution.State):
		return WorkflowRunView{}, fmt.Errorf("a %s run cannot be sent back", execution.State)
	case request.Round != execution.Round:
		return WorkflowRunView{}, fmt.Errorf("this run is in round %d, and the changes were written for round %d; reload it", execution.Round, request.Round)
	}
	plan, err := service.store.ExecutionPlan(ctx, runID)
	if err != nil {
		return WorkflowRunView{}, fmt.Errorf("reopen the run: %w", err)
	}
	items, targets, err := feedbackItems(plan, request.Items)
	if err != nil {
		return WorkflowRunView{}, err
	}
	// The run's draft comments go with the person's text, each an item for
	// the stage it names.
	used := 0
	for _, item := range items {
		used += len(item.Text)
	}
	comments, commented, err := service.commentItems(ctx, runID, plan, used)
	if err != nil {
		return WorkflowRunView{}, err
	}
	items = append(items, comments...)
	for _, target := range commented {
		if !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	if len(items) == 0 {
		return WorkflowRunView{}, errors.New("say what to change")
	}
	var gates []storage.InputRequest
	if reviewing {
		if gates, err = service.store.PendingReviews(ctx, runID); err != nil {
			return WorkflowRunView{}, fmt.Errorf("load the run's reviews: %w", err)
		}
		if approveOnly(gates) {
			return WorkflowRunView{}, errors.New("this run is paused at a gate that can only be approved, since no stage before it writes; approve it, or cancel the run")
		}
	}
	prior, err := service.currentResults(ctx, execution, plan)
	if err != nil {
		return WorkflowRunView{}, err
	}
	completed := make(map[string]bool, len(prior))
	for step := range prior {
		completed[step] = true
	}
	// A gate the run is paused at has no completed result, so it runs, and
	// pauses, again.
	restart := workflowengine.RestartSet(plan, targets, completed)
	resumed, err := service.resumable(ctx, execution, plan, restart, false)
	if err != nil {
		return WorkflowRunView{}, err
	}
	started, err := service.store.StartRound(ctx, runID, execution.Round, items)
	if err == nil && !started {
		err = fmt.Errorf("this run was already sent back from round %d, or it changed state; reload it", execution.Round)
	}
	if err != nil {
		service.releaseCheckout(runID)
		return WorkflowRunView{}, err
	}
	round := execution.Round + 1
	if reviewing {
		service.closeReviews(ctx, execution, gates, storage.InputAnswered, reviewAnswer(items), stepChangesRequested,
			domain.HandoffResult{Version: domain.ContractVersion, Status: stepChangesRequested, Summary: fmt.Sprintf("Changes requested for round %d", round)})
	}
	feedback := make(map[string][]storage.FeedbackItem, len(targets))
	for _, item := range items {
		item.Round = round
		feedback[item.Target] = append(feedback[item.Target], item)
	}
	service.auditDecision("review.changes", principal, execution, fmt.Sprintf("Sent the run back for round %d", round),
		map[string]string{"targets": strings.Join(targets, ","), "items": strconv.Itoa(len(items)), "review": review})
	return service.launchRound(execution, resumed, plan, round, workflowengine.Round{Prior: prior, Restart: restart}, feedback), nil
}

// currentResults is the result of each step that holds a completed one as
// the run stands now (standing), which a round reuses instead of running the
// step. A step's latest run is the one that holds it.
func (service *Service) currentResults(ctx context.Context, execution storage.Execution, plan domain.WorkflowSpec) (map[string]domain.HandoffResult, error) {
	results, err := service.store.StepResults(ctx, execution.ID)
	if err != nil {
		return nil, fmt.Errorf("load the results of run %s: %w", execution.ID, err)
	}
	runs, err := service.store.StepRuns(ctx, execution.ID)
	if err != nil {
		return nil, fmt.Errorf("load the step runs of run %s: %w", execution.ID, err)
	}
	feedback, err := service.store.RunFeedback(ctx, execution.ID)
	if err != nil {
		return nil, fmt.Errorf("load the feedback of run %s: %w", execution.ID, err)
	}
	resumes, err := service.store.ResumedRounds(ctx, execution.ID)
	if err != nil {
		return nil, err
	}
	held := standing(plan, runs, feedback, storage.EditedRounds(resumes), max(execution.Round, 1))
	for step := range results {
		if !held[step] {
			delete(results, step)
		}
	}
	return results, nil
}

// approveOnly reports whether every gate a run is paused at can only be
// approved: none has a writable stage before it to send changes to.
func approveOnly(gates []storage.InputRequest) bool {
	for _, gate := range gates {
		if gate.ReviewTarget != "" {
			return false
		}
	}
	return len(gates) > 0
}

// feedbackItems checks what a person sent and names each item: f1, f2, …
// in the order given. Every target must be a step of the run that writes.
func feedbackItems(plan domain.WorkflowSpec, requested []FeedbackRequestItem) ([]storage.FeedbackItem, []string, error) {
	steps := make(map[string]domain.WorkflowStep, len(plan.Steps))
	for _, step := range plan.Steps {
		steps[step.ID] = step
	}
	items := make([]storage.FeedbackItem, 0, len(requested))
	var targets []string
	total := 0
	for index, request := range requested {
		text := strings.TrimSpace(request.Text)
		step, ok := steps[request.Target]
		switch {
		case text == "":
			return nil, nil, fmt.Errorf("feedback for %s is empty", request.Target)
		case len(text) > maxFeedbackItemBytes:
			return nil, nil, fmt.Errorf("feedback for %s is %d bytes; at most %d", request.Target, len(text), maxFeedbackItemBytes)
		case !ok:
			return nil, nil, fmt.Errorf("%q is not a step of this run", request.Target)
		case step.Kind != domain.StepAgent || step.Permission != domain.PermissionWorktree:
			return nil, nil, fmt.Errorf("step %s does not write, so it cannot take feedback", step.ID)
		}
		if total += len(text); total > maxFeedbackBytes {
			return nil, nil, fmt.Errorf("the feedback is over %d bytes in all", maxFeedbackBytes)
		}
		items = append(items, storage.FeedbackItem{ID: fmt.Sprintf("f%d", index+1), Target: step.ID, From: storage.FeedbackFromHuman, Text: text})
		if !slices.Contains(targets, step.ID) {
			targets = append(targets, step.ID)
		}
	}
	return items, targets, nil
}

// checkReopenable makes sure the run's worktree and branch still hold what
// the run left, so a round builds on its work, and that nothing is left
// uncommitted there for the round's first commit to sweep in.
func (service *Service) checkReopenable(ctx context.Context, execution storage.Execution) error {
	if err := service.checkWorkspace(ctx, execution); err != nil {
		return err
	}
	if !hasWorkspace(execution) {
		return nil
	}
	status, err := worktree.Changes(ctx, execution.Worktree)
	if err != nil {
		return fmt.Errorf("read the run's worktree: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("%s has uncommitted changes; commit or discard them first", execution.Worktree)
	}
	return nil
}

// hasWorkspace reports whether the run created its worktree, or took its
// branch in the checkout, before it stopped.
func hasWorkspace(execution storage.Execution) bool {
	return execution.Worktree != "" || execution.Branch != ""
}

// checkWorkspace makes sure the run's worktree and branch still hold what the
// run left, whatever is uncommitted there. A round adds commits on top of the
// run's anchor, its last commit or else the commit it started from, and
// commits made by hand on top of that are kept. Nothing is repaired: each
// refusal says what changed. A run that never created its workspace has
// nothing to check, and its round creates one as its first would have.
func (service *Service) checkWorkspace(ctx context.Context, execution storage.Execution) error {
	if !hasWorkspace(execution) {
		return nil
	}
	if !execution.WorktreeRemovedAt.IsZero() {
		return errors.New("the run's worktree was removed, so there is nothing to build on; plan the task again")
	}
	if _, err := os.Stat(execution.Worktree); err != nil {
		return fmt.Errorf("the run's worktree %s is gone; plan the task again", execution.Worktree)
	}
	branch, err := worktree.CurrentBranch(ctx, execution.Worktree)
	if err != nil {
		return fmt.Errorf("read the run's branch: %w", err)
	}
	if branch != execution.Branch {
		return fmt.Errorf("%s is on %q, not the run's branch %s; switch it back first", execution.Worktree, branch, execution.Branch)
	}
	anchor := execution.BaseCommit
	commits, err := service.store.Commits(ctx, execution.ID)
	if err != nil {
		return fmt.Errorf("load the run's commits: %w", err)
	}
	if len(commits) > 0 {
		anchor = commits[len(commits)-1].Hash
	}
	if anchor == "" {
		return errors.New("this run made no commit and did not record the commit it started from, so Inkuna cannot tell whether its branch moved")
	}
	if held, err := worktree.Contains(ctx, execution.Worktree, anchor, "HEAD"); err != nil || !held {
		return fmt.Errorf("branch %s no longer holds the run's commit %s; it was rewritten or reset", execution.Branch, shortHash(anchor))
	}
	return nil
}

// launchRound puts a run that ended or paused back in memory, in round, and
// runs it as StartWorkflow runs a first round: a new round after a request
// for changes, or the rest of the same round after an approval. The steps
// outside the restart set show as completed, with the results it reuses.
func (service *Service) launchRound(execution storage.Execution, resumed resumption, plan domain.WorkflowSpec, round int, scope workflowengine.Round, feedback map[string][]storage.FeedbackItem) WorkflowRunView {
	project, task, registry := resumed.project, resumed.task, resumed.registry
	skills := make(map[string]string, len(resumed.packages))
	descriptions := make(map[string]string, len(resumed.packages))
	briefs := make(map[string]skillBrief, len(resumed.packages))
	for _, current := range resumed.packages {
		skills[current.ID] = current.Body
		descriptions[current.ID] = current.Description
		briefs[current.ID] = skillBrief{description: current.Description, onDemand: current.Manifest.Delivery == skill.DeliveryOnDemand}
	}
	service.ensureBus()
	service.registerAgents(registry, descriptions)
	docs := map[string]runDocs{project.Repository: service.runDocsIn(project, project.Repository)}
	if execution.Worktree != "" && !samePath(execution.Worktree, project.Repository) {
		docs[execution.Worktree] = service.runDocsIn(project, execution.Worktree)
	}
	// A step that runs again may ask again, so what it asked before is not
	// made.
	service.discardTrackerChanges(service.context(), execution.ID, scope.Restart, round)
	view := historyView(execution)
	view.State, view.Error, view.FinishedAt, view.Round, view.Live = "queued", "", "", round, true
	view.RuleName, view.Orchestrated = plan.RuleName, plan.Orchestrated
	view.Events = make([]domain.RunEvent, 0)
	view.Steps = stepViews(plan, round)
	// What the view shows of the run before this round is best effort: the
	// round runs whether or not it could be read.
	ctx := service.context()
	kept := map[string]int{}
	if runs, err := service.store.StepRuns(ctx, execution.ID); err == nil {
		kept = latestRounds(runs)
	}
	for index := range view.Steps {
		if result, reused := scope.Prior[view.Steps[index].ID]; reused && !scope.Restart[view.Steps[index].ID] {
			view.Steps[index].State = "completed"
			view.Steps[index].Summary = truncateSummary(result.Summary, 280)
			if from := kept[view.Steps[index].ID]; from > 0 {
				view.Steps[index].Round = from
			}
		}
	}
	// An approval carries on the round it paused in, whose timeline so far
	// is kept; a new round starts an empty one.
	if round == execution.Round {
		if events, _, err := service.store.RoundEvents(ctx, execution.ID, round, liveRunEventLimit); err == nil {
			view.Events = events
		}
	}
	if commits, err := service.store.Commits(ctx, execution.ID); err == nil && len(commits) > 0 {
		view.LastCommit = commitView(commits[len(commits)-1])
	}
	for _, step := range plan.Steps {
		if step.Kind == domain.StepAgent {
			view.Runtime, view.Agent = step.Runtime, step.Agent
			if step.ModelSelection != nil {
				view.Model = step.ModelSelection.Model
			}
			break
		}
	}
	results := make(map[string]domain.HandoffResult, len(scope.Prior))
	for step, result := range scope.Prior {
		results[step] = result
	}
	runContext, cancel := context.WithCancel(service.context())
	run := &liveExecution{
		view: view, started: time.Now().UTC(), ctx: runContext, cancel: cancel,
		active: make(map[string]agentruntime.AgentRuntime), registry: registry, skills: skills, skillBriefs: briefs, docs: docs,
		results: results, contexts: make(map[string]domain.ContextEnvelope), outbound: make(map[string]string), calls: make(map[string]domain.UsageRecord), sequences: make(map[string]int64),
		project: project, task: task, spec: plan, deps: service.snapshotDependencies(), round: round, feedback: feedback, done: make(chan struct{}),
		story: resumed.story, resumed: resumed.notes,
	}
	service.runsMu.Lock()
	if service.runs == nil {
		service.runs = make(map[string]*liveExecution)
	}
	service.runs[execution.ID] = run
	slots := service.taskSlots
	shown := copyRunView(run.view)
	service.runsMu.Unlock()
	service.persistRun(execution.ID)
	service.runsWG.Add(1)
	go func() {
		defer service.runsWG.Done()
		defer close(run.done)
		// An approval at the last gate leaves nothing to run, and finishing
		// the run need not wait for a slot.
		if len(scope.Restart) > 0 {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-runContext.Done():
				// The checkout was claimed before the round queued, and only
				// executeWorkflow would release it.
				service.releaseCheckout(execution.ID)
				service.finishRun(execution.ID, "cancelled", "")
				service.reportStory(execution.ID)
				return
			}
		}
		service.executeWorkflow(runContext, execution.ID, project, task, plan, scope)
	}()
	service.emit("inkuna:runs", execution.ID)
	return shown
}
