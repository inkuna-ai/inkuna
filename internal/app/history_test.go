package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// stepRounds is each step's state and the round of the run shown, by step.
func stepRounds(steps []StepRunView) map[string]StepRunView {
	byID := make(map[string]StepRunView, len(steps))
	for _, step := range steps {
		byID[step.ID] = step
	}
	return byID
}

func TestRunRoundsKeepEachRoundsStepsEventsAndFeedback(t *testing.T) {
	service, _, run := finishedBugfix(t)
	firstCommits, err := service.store.Commits(context.Background(), run.ID)
	if err != nil || len(firstCommits) != 1 {
		t.Fatalf("round 1 committed fix once: %+v %v", firstCommits, err)
	}
	reopened, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Handle an empty name."}}})
	if err != nil {
		t.Fatal(err)
	}
	// The live view says which steps the round kept, and from when, and
	// shows the run's latest commit before the round makes one.
	if steps := stepRounds(reopened.Steps); steps["reproduce"].Round != 1 || steps["fix"].Round != 2 {
		t.Fatalf("reproduce is kept from round 1 and fix runs in round 2: %+v", reopened.Steps)
	}
	if reopened.LastCommit == nil || reopened.LastCommit.Hash != firstCommits[0].Hash {
		t.Fatalf("a reopened run shows its latest commit: %+v", reopened.LastCommit)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")

	rounds, err := service.RunRounds(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 2 {
		t.Fatalf("%d rounds, want 2", len(rounds))
	}
	first, second := rounds[0], rounds[1]
	if first.Since != "" || len(first.Feedback) != 0 || first.StartedAt == "" || first.FinishedAt == "" {
		t.Fatalf("round 1 started from nothing and was sent nothing: %+v", first)
	}
	for id, step := range stepRounds(first.Steps) {
		if step.Round != 1 || step.State != "completed" {
			t.Errorf("round 1 ran %s to completion: %+v", id, step)
		}
	}
	steps := stepRounds(second.Steps)
	if len(second.Steps) != 5 || steps["profile"].Round != 1 || steps["reproduce"].Round != 1 || steps["fix"].Round != 2 || steps["verify"].Round != 2 || steps["approval"].Round != 2 {
		t.Fatalf("round 2 re-ran fix and what follows and kept the rest: %+v", second.Steps)
	}
	if steps["fix"].Title != "Implement the fix" || steps["fix"].Kind != domain.StepAgent || steps["fix"].State != "completed" {
		t.Fatalf("a step shows as planned: %+v", steps["fix"])
	}
	if second.Since != firstCommits[0].Hash {
		t.Fatalf("round 2 built on round 1's commit %s, not %s", firstCommits[0].Hash, second.Since)
	}
	if len(second.Feedback) != 1 || second.Feedback[0].ID != "f1" || second.Feedback[0].Target != "fix" || second.Feedback[0].Status == storage.FeedbackOpen {
		t.Fatalf("round 2 keeps what it was sent and the reply: %+v", second.Feedback)
	}
	for _, event := range first.Events {
		if strings.Contains(event.RunID, ".r2.") {
			t.Fatalf("round 1 shows only its own events: %+v", event)
		}
	}
	if len(second.Events) == 0 {
		t.Fatal("round 2 keeps its events")
	}
	for _, event := range second.Events {
		if !strings.HasPrefix(event.RunID, run.ID+".r2.") {
			t.Fatalf("round 2 shows only its own events: %+v", event)
		}
	}

	// After a restart, the run detail still shows the steps and timeline of
	// the run's current round.
	after := restarted(t, service)
	stored, err := after.WorkflowRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Steps) != 5 || stepRounds(stored.Steps)["fix"].Round != 2 || len(stored.Events) != len(second.Events) || stored.LastCommit == nil {
		t.Fatalf("a stored run shows its current round: %d steps, %d events, commit %+v", len(stored.Steps), len(stored.Events), stored.LastCommit)
	}
}

func TestAnApprovedRoundKeepsItsTimeline(t *testing.T) {
	service, _, run := pausedRun(t, domain.TaskImplementation)
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	done := waitForWorkflowRun(t, service, run.ID, "completed")
	implemented := false
	for _, event := range done.Events {
		implemented = implemented || event.RunID == run.ID+"-implement"
	}
	if !implemented {
		t.Fatalf("the round carried on past its gate still shows what ran before it: %+v", done.Events)
	}
}

func TestMessagesShowWhatARoundSentItsTargetAndTheReplies(t *testing.T) {
	service, _, run := finishedBugfix(t)
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Handle an empty name."}}}); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	tasks, err := service.RunMessages(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, task := range tasks {
		step, _ := task.Task.Metadata["stepId"].(string)
		round := 0
		if len(task.Task.History) > 0 {
			round = metadataInt(task.Task.History[0].Metadata["round"])
		}
		if step == "fix" && round == 2 {
			target = task.Task.ID
			if len(task.Feedback) != 1 || task.Feedback[0].Text != "Handle an empty name." || task.Feedback[0].Status != "no-reply" {
				t.Fatalf("round 2's fix task carries its feedback and how it ended: %+v", task.Feedback)
			}
			continue
		}
		if len(task.Feedback) != 0 {
			t.Fatalf("only a round's target's task carries feedback: %s %+v", step, task.Feedback)
		}
	}
	if target == "" {
		t.Fatal("round 2 sent fix a task")
	}
	// The task is read back from storage once the run has left memory.
	thread, err := restarted(t, service).Thread(target)
	if err != nil || len(thread.Feedback) != 1 || thread.Feedback[0].ID != "f1" {
		t.Fatalf("the thread carries its feedback: %+v %v", thread.Feedback, err)
	}
}

// askingRules ask the user a question after the stage that writes, so a
// round that re-runs the stage asks it again.
const askingRules = `version: 1
rules:
  - name: asking
    when:
      kind: [implementation]
    then:
      stages:
        - id: implement
          title: Implement
          kind: agent
          agent: auto
          permission: worktree-write
        - id: choose
          kind: human-input
          prompt: Which format?
          dependsOn: [implement]
        - id: approval
          kind: approval
          dependsOn: [choose]
`

func TestARoundAsksAgainOfferingThePreviousAnswer(t *testing.T) {
	repository := branchingRepository(t, roundsBranching)
	if err := os.WriteFile(filepath.Join(repository, ".inkuna", "rules.yaml"), []byte(askingRules), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repository, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repository, "commit", "-q", "-m", "Asking rules"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repository, writingRuntime())
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Write the export", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	asked := func() InputView {
		t.Helper()
		waitForWorkflowRun(t, service, run.ID, "waiting")
		inbox, err := service.Inbox()
		if err != nil || len(inbox) != 1 {
			t.Fatalf("one question waits: %+v %v", inbox, err)
		}
		return inbox[0]
	}
	first := asked()
	if first.PreviousAnswer != "" {
		t.Fatalf("a first question offers no previous answer: %+v", first)
	}
	if _, err := service.AnswerInput(first.ID, "JSON"); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "implement", Text: "Add a header row."}}}); err != nil {
		t.Fatal(err)
	}
	again := asked()
	if again.ID == first.ID || again.PreviousAnswer != "JSON" {
		t.Fatalf("round 2 asks again, offering round 1's answer: %+v", again)
	}
	if _, err := service.AnswerInput(again.ID, again.PreviousAnswer); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
}

func TestARoundShowsStepsItNeverStartedAsPendingNotKept(t *testing.T) {
	at := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	ran := func(step string, round int, state string) storage.StepRun {
		return storage.StepRun{ID: step, StepID: step, Round: round, State: state, StartedAt: at, FinishedAt: at}
	}
	history := runHistory{
		execution: storage.Execution{ID: "run-1", State: "failed", Round: 2},
		plan: []domain.WorkflowStep{
			{ID: "plan", Kind: domain.StepAgent},
			{ID: "fix", Kind: domain.StepAgent, DependsOn: []string{"plan"}},
			{ID: "verify", Kind: domain.StepEvaluation, DependsOn: []string{"fix"}},
		},
		// Round 2 sent fix back, and fix failed before verify could start.
		runs:     []storage.StepRun{ran("plan", 1, "completed"), ran("fix", 1, "completed"), ran("verify", 1, "completed"), ran("fix", 2, "failed")},
		feedback: []storage.FeedbackItem{{Round: 2, ID: "f1", Target: "fix"}},
		last:     2,
	}
	steps := stepRounds(history.steps(2))
	if steps["plan"].Round != 1 || steps["plan"].State != "completed" {
		t.Fatalf("plan is kept from round 1: %+v", steps["plan"])
	}
	if steps["fix"].Round != 2 || steps["fix"].State != "failed" {
		t.Fatalf("fix ran in round 2 and failed: %+v", steps["fix"])
	}
	if steps["verify"].Round != 2 || steps["verify"].State != "pending" || steps["verify"].Summary != "" {
		t.Fatalf("verify was to run again in round 2, so it is pending there, not kept: %+v", steps["verify"])
	}
	for id, step := range stepRounds(history.steps(1)) {
		if step.Round != 1 || step.State != "completed" {
			t.Fatalf("round 1 still shows its own run of %s: %+v", id, step)
		}
	}
}

func TestMessagesHearOfTheRepliesOnceTheyAreStored(t *testing.T) {
	service, _, run := finishedBugfix(t)
	// Each thread update records whether the round's replies were stored by
	// then; the target's thread must hear of them once they were.
	var mu sync.Mutex
	settled := map[string]bool{}
	service.emitMu.Lock()
	service.emitter = func(name string, data any) {
		event, ok := data.(ThreadEvent)
		if !ok || name != EventMessages {
			return
		}
		items, err := service.store.Feedback(context.Background(), run.ID, 2)
		stored := err == nil && len(items) > 0
		for _, item := range items {
			stored = stored && item.Status != storage.FeedbackOpen
		}
		mu.Lock()
		settled[event.TaskID] = settled[event.TaskID] || stored
		mu.Unlock()
	}
	service.emitMu.Unlock()
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "fix", Text: "Handle an empty name."}}}); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	tasks, err := service.RunMessages(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, task := range tasks {
		if len(task.Feedback) > 0 {
			target = task.Task.ID
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if target == "" || !settled[target] {
		t.Fatalf("the target's thread %q was not told once its replies were stored: %v", target, settled)
	}
}

func TestApprovingALaterRoundRunsWhatFollowsTheGateAgain(t *testing.T) {
	service, runtime, run := pausedRun(t, domain.TaskDocumentation)
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "draft", Text: "Shorter intro."}}}); err != nil {
		t.Fatal(err)
	}
	waitForRound(t, service, run.ID, 2)
	// polish completed in round 1, but on round 1's draft; round 2 was to
	// run it again once its gate is approved.
	before := len(startedRuns(runtime))
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	ran := make([]string, 0)
	for _, request := range startedRuns(runtime)[before:] {
		ran = append(ran, request.RunID)
	}
	if len(ran) != 1 || ran[0] != run.ID+".r2.polish" {
		t.Fatalf("approving round 2 ran %v, want %s.r2.polish", ran, run.ID)
	}
}

func TestAStepARoundNeverStartedHoldsNoResultInLaterRounds(t *testing.T) {
	plan := domain.WorkflowSpec{Steps: []domain.WorkflowStep{
		{ID: "x", Kind: domain.StepAgent},
		{ID: "v1", Kind: domain.StepEvaluation, DependsOn: []string{"x"}},
		{ID: "y", Kind: domain.StepAgent},
		{ID: "v2", Kind: domain.StepEvaluation, DependsOn: []string{"y"}},
	}}
	ran := func(step string, round int, state string) storage.StepRun {
		return storage.StepRun{StepID: step, Round: round, State: state}
	}
	runs := []storage.StepRun{
		ran("x", 1, "completed"), ran("v1", 1, "completed"), ran("y", 1, "completed"), ran("v2", 1, "completed"),
		// Round 2 sent both writers back: x finished, y failed, and the
		// round stopped before v1 started.
		ran("x", 2, "completed"), ran("y", 2, "failed"),
	}
	feedback := []storage.FeedbackItem{{Round: 2, ID: "f1", Target: "x"}, {Round: 2, ID: "f2", Target: "y"}}
	held := standing(plan, runs, feedback, nil, 2)
	if !held["x"] || held["v1"] || held["y"] || held["v2"] {
		t.Fatalf("after round 2 only x holds a result; v1 last checked round 1's x: %v", held)
	}
	// Round 3 sends only y back, and still runs v1, which round 2 never did.
	feedback = append(feedback, storage.FeedbackItem{Round: 3, ID: "f1", Target: "y"})
	restart := roundRestart(plan, feedback, nil, held, 3)
	if restart["x"] || !restart["v1"] || !restart["y"] || !restart["v2"] {
		t.Fatalf("round 3 reruns v1, y, and v2, and keeps x: %v", restart)
	}
	history := runHistory{execution: storage.Execution{State: "completed", Round: 3}, plan: plan.Steps, runs: runs, feedback: feedback, last: 3}
	if steps := stepRounds(history.steps(3)); steps["v1"].State != "pending" || steps["v1"].Round != 3 || steps["x"].Round != 2 {
		t.Fatalf("round 3 shows v1 pending and x kept from round 2: %+v", history.steps(3))
	}
}
