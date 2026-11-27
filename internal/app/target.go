package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/diff"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// roundFeedback is what a round's target is sent back with: its own items,
// and what the run's branch has changed so far, which Inkuna lists itself
// so a worker that may not run git still sees it (docs/feedback-loop.md).
// The items are sent whatever happens to the list, which a run that has
// not written yet does not have.
func (service *Service) roundFeedback(ctx context.Context, executionID, stepRunID string, round int, items []storage.FeedbackItem) *domain.RoundFeedback {
	feedback := &domain.RoundFeedback{Round: round, Items: make([]domain.FeedbackItem, 0, len(items))}
	var threads []storage.CommentThread
	for _, item := range items {
		sent := domain.FeedbackItem{ID: item.ID, From: item.From, Text: item.Text}
		// A line comment is sent where it is and the diff around it, as they
		// were stored when it was sent, with what its thread said before; a
		// thread that cannot be read leaves that out.
		if item.CommentID != "" {
			if threads == nil {
				threads, _ = service.store.CommentThreads(ctx, executionID)
			}
			sent.Comment = &domain.LineComment{Path: item.Path, Line: item.Line, EndLine: item.EndLine, Side: item.Side, Excerpt: item.Excerpt, Thread: commentThread(threads, item)}
		}
		// A failed check is sent as the command, how it ended, and the end
		// of its log, which its stored text only summarises.
		if item.CheckID != "" {
			if check, err := service.store.Check(ctx, item.CheckID); err == nil {
				sent.Text = ""
				sent.Check = &domain.FailedCheck{Command: check.Argv, ExitCode: check.ExitCode, Error: check.Error, OutputTail: item.Tail}
			}
		}
		feedback.Items = append(feedback.Items, sent)
	}
	changes, err := service.branchChanges(ctx, executionID)
	if err != nil {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventLifecycle, "The feedback is sent without the branch's changes so far: "+err.Error()))
		return feedback
	}
	feedback.Changes = changes
	return feedback
}

// branchChanges lists what a run's branch has changed from the commit its
// workspace started from to its tip, as the Changes view's branch scope
// does.
func (service *Service) branchChanges(ctx context.Context, executionID string) (*domain.BranchChanges, error) {
	ctx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	repository, between, review, err := service.runDiff(ctx, executionID, diff.Scope{Kind: diff.ScopeBranch})
	if err != nil {
		return nil, err
	}
	change, err := diff.Changes(ctx, repository, between, review)
	if err != nil {
		return nil, fmt.Errorf("list the branch's changes: %w", err)
	}
	files := make([]domain.ChangedFile, 0, len(change.Files))
	for _, file := range change.Files {
		files = append(files, domain.ChangedFile{Path: file.Path, Additions: file.Additions, Deletions: file.Deletions, Binary: file.Binary})
	}
	return domain.NewBranchChanges(change.From, change.To, files), nil
}

// previousRound digests a round's target's own last run of its step, for a
// round that starts without that run's session: the latest run before this
// round that completed, or failing that the latest that ended, with a line
// for each of the step's other runs. A step that never ran before has none.
func (service *Service) previousRound(ctx context.Context, executionID, stepID string, round int) (*domain.RoundDigest, error) {
	rounds, err := service.store.StepRounds(ctx, executionID, stepID)
	if err != nil {
		return nil, fmt.Errorf("load the step's earlier runs: %w", err)
	}
	items, err := service.store.TargetFeedback(ctx, executionID, stepID)
	if err != nil {
		return nil, fmt.Errorf("load the step's earlier feedback: %w", err)
	}
	sent := make(map[int][]storage.FeedbackItem)
	for _, item := range items {
		sent[item.Round] = append(sent[item.Round], item)
	}
	before := make([]storage.StepRound, 0, len(rounds))
	last := -1
	for _, run := range rounds {
		if run.Round >= round {
			continue
		}
		before = append(before, run)
		if run.State == "completed" || last < 0 || before[last].State != "completed" {
			last = len(before) - 1
		}
	}
	if last < 0 {
		return nil, nil
	}
	earlier := make([]string, 0, len(before)-1)
	for index, run := range before {
		if index != last {
			earlier = append(earlier, roundLine(run, sent[run.Round]))
		}
	}
	replies := make([]string, 0, len(sent[before[last].Round]))
	for _, item := range sent[before[last].Round] {
		replies = append(replies, replyLine(item, 160, 240))
	}
	return domain.NewRoundDigest(before[last].Round, before[last].Handoff, replies, earlier), nil
}

// roundLine is one of a step's runs in a line: its round, how it ended
// when it did not complete, what it was sent back with and how it replied,
// each held short, and its summary.
func roundLine(run storage.StepRound, items []storage.FeedbackItem) string {
	line := fmt.Sprintf("Round %d", run.Round)
	if run.State != "completed" {
		line += " (" + run.State + ")"
	}
	if len(items) > 0 {
		replies := make([]string, 0, len(items))
		for _, item := range items {
			replies = append(replies, replyLine(item, 60, 80))
		}
		line += ", sent back with " + strings.Join(replies, "; ")
	}
	return line + ": " + strings.TrimSpace(run.Handoff.Summary)
}

// replyLine is a feedback item and the target's reply on it, with what it
// asked and the reply's note held to the given lengths, so a target that
// starts without its session knows what an id stood for and why it was
// answered as it was.
func replyLine(item storage.FeedbackItem, asked, note int) string {
	line := fmt.Sprintf("%s %s: %q", item.ID, strings.ReplaceAll(item.Status, "-", " "), truncateSummary(item.Text, asked))
	if text := strings.TrimSpace(item.Note); text != "" {
		line += fmt.Sprintf(" → %q", truncateSummary(text, note))
	}
	return line
}

// resumeRound lets a round's target continue in the session its last run of
// the step left, which holds that run's own work and nothing of any other
// agent's, when that session was kept, on the same runtime, in the same
// working directory (docs/feedback-loop.md). The round's first turn is then
// sent only its feedback and the new handoffs of the dependencies that ran
// again this round; runWorker still sends the whole envelope, with its
// PreviousRound, when the session's cache has expired or the session cannot
// be resumed.
func (service *Service) resumeRound(ctx context.Context, run *liveExecution, assignment *workerAssignment, round int) {
	cold := func(reason string) {
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventLifecycle, fmt.Sprintf("Round %d starts a new session with a digest of the step's last run: %s", round, reason)))
	}
	last, found, err := service.store.LastSession(ctx, assignment.executionID, assignment.step.ID, round)
	switch {
	case err != nil:
		cold("its session could not be read: " + err.Error())
		return
	case !found:
		cold("that run kept no session to resume")
		return
	case last.Runtime != assignment.runtimeName:
		cold(fmt.Sprintf("that run's session is %s's, and this round runs on %s", last.Runtime, assignment.runtimeName))
		return
	case !samePath(last.WorkingDir, assignment.workingDir):
		cold("that run worked in another directory")
		return
	}
	resume := &domain.ContextEnvelope{Feedback: assignment.envelope.Feedback}
	for _, dependency := range assignment.step.DependsOn {
		rounds, err := service.store.StepRounds(ctx, assignment.executionID, dependency)
		if err != nil {
			cold("what ran before it this round could not be read: " + err.Error())
			return
		}
		if len(rounds) == 0 || rounds[len(rounds)-1].Round != round {
			continue
		}
		service.runsMu.RLock()
		result, ok := run.results[dependency]
		service.runsMu.RUnlock()
		if ok && result.Summary != "" {
			resume.DependencySummary = append(resume.DependencySummary, result.Summary)
			resume.DependencyOutputs = append(resume.DependencyOutputs, result.Outputs...)
			resume.RelevantDecisions = append(resume.RelevantDecisions, result.Decisions...)
			resume.RelevantRisks = append(resume.RelevantRisks, result.Risks...)
		}
	}
	assignment.mu.Lock()
	assignment.session, assignment.sessionUsage, assignment.cachedUntil, assignment.roundResume = last.Session, last.Usage, last.CacheUntil, resume
	assignment.mu.Unlock()
}

// settleFeedback records a round's target's replies on its items once its
// step has ended, every item it did not reply on as no-reply, and says on
// the timeline how each item stands.
func (service *Service) settleFeedback(executionID, stepRunID string, round int, stepID string, replies []domain.Addressed) {
	items, err := service.store.SettleFeedback(context.Background(), executionID, round, stepID, replies)
	if err != nil {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, "The replies on the feedback were not recorded: "+err.Error()))
		return
	}
	statuses := make([]string, 0, len(items))
	for _, item := range items {
		statuses = append(statuses, item.ID+" "+strings.ReplaceAll(item.Status, "-", " "))
	}
	if len(statuses) > 0 {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventMessage, "Replies on the feedback: "+strings.Join(statuses, ", ")))
	}
	// The target's thread shows these replies, which are stored after its
	// task last changed and told Messages so, so it is told again.
	if task, ok := service.stepTask(executionID, stepID); ok {
		service.notifyThread(task)
	}
}

// stepTask is the A2A task a step's run in the run's current round was
// sent as, while the run is in memory.
func (service *Service) stepTask(executionID, stepID string) (a2a.Task, bool) {
	service.runsMu.RLock()
	id := ""
	if run := service.runs[executionID]; run != nil {
		for _, step := range run.view.Steps {
			if step.ID == stepID {
				id = step.A2ATaskID
			}
		}
	}
	service.runsMu.RUnlock()
	if id == "" || service.bus == nil {
		return a2a.Task{}, false
	}
	task, err := service.bus.GetTask(id, 0)
	return task, err == nil
}
