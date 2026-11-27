package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// migratedTo builds a database with the migrations up to and including
// last applied, as an install from before the next one would have it.
func migratedTo(t *testing.T, path, last string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.Name() > last {
			break
		}
		contents, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(string(contents)); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		if _, err := database.Exec(`INSERT INTO schema_migrations VALUES (?, ?)`, entry.Name(), "2026-10-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestTheRoundsMigrationGivesEveryStepRunItsRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.db")
	database := migratedTo(t, path, "016_checks.sql")
	// Two runs of one workflow, one with a peer row that stores its parent's
	// step id, and a row from before runs were recorded.
	for _, statement := range []string{
		`INSERT INTO executions(id,workflow_id,task_id,project_id,state,title,started_at) VALUES ('run-100','workflow-1','task-1','project','completed','One','2026-10-01T00:00:00Z')`,
		`INSERT INTO executions(id,workflow_id,task_id,project_id,state,title,started_at) VALUES ('run-1000','workflow-1','task-1','project','completed','Two','2026-10-02T00:00:00Z')`,
		`INSERT INTO runs(id,workflow_id,step_id,runtime,state,started_at) VALUES ('run-100-implement','workflow-1','implement','claude','completed','2026-10-01T00:00:00Z')`,
		`INSERT INTO runs(id,workflow_id,step_id,runtime,state,started_at) VALUES ('run-100-implement-peer1-reviewer','workflow-1','implement','claude','completed','2026-10-01T00:00:00Z')`,
		`INSERT INTO runs(id,workflow_id,step_id,runtime,state,started_at) VALUES ('run-1000-implement','workflow-1','implement','claude','completed','2026-10-02T00:00:00Z')`,
		`INSERT INTO runs(id,workflow_id,step_id,runtime,state,started_at) VALUES ('old-implement','workflow-1','implement','claude','completed','2026-09-01T00:00:00Z')`,
		`INSERT INTO commits(execution_id,step_id,hash,subject,stat,created_at) VALUES ('run-100','implement','abc','inkuna(implement): one','','2026-10-01T00:00:00Z')`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	want := map[string]string{"run-100-implement": "run-100", "run-100-implement-peer1-reviewer": "run-100", "run-1000-implement": "run-1000", "old-implement": ""}
	for id, execution := range want {
		var got string
		var round int
		if err := store.DB().QueryRow(`SELECT execution_id, round FROM runs WHERE id=?`, id).Scan(&got, &round); err != nil || got != execution || round != 1 {
			t.Errorf("%s: run %q round %d, want %q round 1 (%v)", id, got, round, execution, err)
		}
	}
	commits, err := store.Commits(context.Background(), "run-100")
	if err != nil || len(commits) != 1 || commits[0].Round != 1 || commits[0].Hash != "abc" {
		t.Fatalf("commits keep their rows, in round 1: %+v %v", commits, err)
	}
}

func roundsStore(t *testing.T) (*Store, domain.WorkflowSpec) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	repository := t.TempDir()
	task := domain.TaskSpec{Version: 1, ID: "task-1", Kind: domain.TaskImplementation, Objective: "Test", Repository: repository, Budget: domain.Budget{WallTimeSeconds: 60}}
	spec := domain.WorkflowSpec{Version: 1, ID: "workflow-1", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{
		{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Permission: domain.PermissionWorktree, AllowedPaths: []string{"."}, Budget: domain.Budget{WallTimeSeconds: 10}},
		{ID: "approval", Title: "Approval", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, DependsOn: []string{"implement"}, Budget: domain.Budget{WallTimeSeconds: 10}},
	}}
	for _, err := range []error{
		store.UpsertProject(ctx, Project{ID: "project", Name: "Project", Repository: repository, DefaultRuntime: domain.RuntimeClaude}),
		store.SaveTask(ctx, "project", "approved", task),
		store.ApproveWorkflow(ctx, spec),
		store.SaveExecution(ctx, Execution{ID: "run-1", WorkflowID: spec.ID, TaskID: task.ID, ProjectID: "project", State: "completed", Title: "Run"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return store, spec
}

func TestARoundStartsOnceAndKeepsItsFeedback(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	items := []FeedbackItem{{ID: "f1", Target: "implement", From: FeedbackFromHuman, Text: "Handle an empty name."}}
	if started, err := store.StartRound(ctx, "run-1", 1, items); err != nil || !started {
		t.Fatalf("start round 2: %v %v", started, err)
	}
	execution, err := store.Execution(ctx, "run-1")
	if err != nil || execution.Round != 2 || execution.State != "queued" {
		t.Fatalf("the run is queued in round 2: %+v %v", execution, err)
	}
	// A second send for round 1, from a double click or another window,
	// starts nothing.
	if started, err := store.StartRound(ctx, "run-1", 1, items); err != nil || started {
		t.Fatalf("a second start for the same round: %v %v", started, err)
	}
	if started, err := store.StartRound(ctx, "run-1", 2, items); err != nil || started {
		t.Fatalf("a queued run is not sent back again: %v %v", started, err)
	}
	feedback, err := store.Feedback(ctx, "run-1", 2)
	if err != nil || len(feedback) != 1 || feedback[0].Status != FeedbackOpen || feedback[0].Text != "Handle an empty name." || feedback[0].Round != 2 {
		t.Fatalf("feedback %+v %v", feedback, err)
	}
	cancelled := execution
	cancelled.State, cancelled.Round = "cancelled", 2
	if err := store.SaveExecution(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	if started, err := store.StartRound(ctx, "run-1", 2, items); err != nil || started {
		t.Fatalf("a cancelled run is not sent back: %v %v", started, err)
	}
}

func TestAStoppedRunResumesOnceInARoundWithoutFeedback(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	if started, err := store.StartResumeRound(ctx, "run-1", 1, nil, false, nil); err != nil || started {
		t.Fatalf("a completed run has nothing to resume: %v %v", started, err)
	}
	for round, state := range []string{"failed", "cancelled", "interrupted"} {
		round++
		execution, err := store.Execution(ctx, "run-1")
		if err != nil {
			t.Fatal(err)
		}
		execution.State, execution.Round, execution.Error, execution.FinishedAt = state, round, "it stopped", time.Now().UTC()
		if err := store.SaveExecution(ctx, execution); err != nil {
			t.Fatal(err)
		}
		if started, err := store.StartResumeRound(ctx, "run-1", round, nil, round == 2, nil); err != nil || !started {
			t.Fatalf("resume a %s run: %v %v", state, started, err)
		}
		resumed, err := store.Execution(ctx, "run-1")
		if err != nil || resumed.Round != round+1 || resumed.State != "queued" || resumed.Error != "" || !resumed.FinishedAt.IsZero() {
			t.Fatalf("a resumed %s run is queued in round %d with its error cleared: %+v %v", state, round+1, resumed, err)
		}
		// A second resume of the same round, from a double click or another
		// window, starts nothing.
		if started, err := store.StartResumeRound(ctx, "run-1", round, nil, false, nil); err != nil || started {
			t.Fatalf("a second resume of round %d: %v %v", round, started, err)
		}
		if feedback, err := store.Feedback(ctx, "run-1", round+1); err != nil || len(feedback) != 0 {
			t.Fatalf("a resumed round has no feedback: %+v %v", feedback, err)
		}
	}
	resumes, err := store.ResumedRounds(ctx, "run-1")
	if err != nil || len(resumes) != 3 || !resumes[3].Discarded || resumes[2].Discarded || resumes[4].Discarded {
		t.Fatalf("each resume is recorded with whether its changes were discarded: %v %v", resumes, err)
	}
}

func TestAResumeSendsAgainTheFeedbackItsStoppedTargetsNeverFinished(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	items := []FeedbackItem{{ID: "f1", Target: "implement", From: FeedbackFromHuman, Text: "Handle an empty name."}}
	if started, err := store.StartRound(ctx, "run-1", 1, items); err != nil || !started {
		t.Fatalf("start round 2: %v %v", started, err)
	}
	// The target failed in round 2 before it replied.
	if _, err := store.SettleFeedback(ctx, "run-1", 2, "implement", nil); err != nil {
		t.Fatal(err)
	}
	execution, err := store.Execution(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	execution.State = "failed"
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if started, err := store.StartResumeRound(ctx, "run-1", 2, []string{"implement"}, false, nil); err != nil || !started {
		t.Fatalf("resume round 2: %v %v", started, err)
	}
	carried, err := store.Feedback(ctx, "run-1", 3)
	if err != nil || len(carried) != 1 || carried[0].ID != "f1" || carried[0].Target != "implement" || carried[0].Text != "Handle an empty name." || carried[0].Status != FeedbackOpen || carried[0].From != FeedbackFromHuman {
		t.Fatalf("round 3 sends f1 again, open: %+v %v", carried, err)
	}
	if before, err := store.Feedback(ctx, "run-1", 2); err != nil || len(before) != 1 || before[0].Status != domain.FeedbackNoReply {
		t.Fatalf("round 2 keeps its item as it ended: %+v %v", before, err)
	}
}

func TestStepResultsAreEachStepsLatestOwnResult(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	finish := func(id, step string, round int, state, summary string) {
		t.Helper()
		if err := store.StartRun(ctx, id, "run-1", spec.ID, step, round, domain.RuntimeClaude, ""); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishRun(ctx, id, state, domain.HandoffResult{Version: 1, Status: state, Summary: summary}); err != nil {
			t.Fatal(err)
		}
	}
	finish("run-1-implement", "implement", 1, "completed", "round 1")
	finish("run-1-implement-peer1-reviewer", "implement", 1, "completed", "a peer's reply")
	finish("run-1.r2.implement", "implement", 2, "completed", "round 2")
	finish("run-1.r2.implement-peer1-reviewer", "implement", 2, "failed", "a peer's failure")
	// approval passed in round 1 and failed in round 2: its latest attempt
	// decides, so it has no result to reuse.
	finish("run-1-approval", "approval", 1, "completed", "approved in round 1")
	finish("run-1.r2.approval", "approval", 2, "failed", "failed in round 2")
	results, err := store.StepResults(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results["implement"].Summary != "round 2" {
		t.Fatalf("only implement's latest attempt completed: %+v", results)
	}
}

func TestARunKeepsThePlanItWasApprovedWith(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	// A run from before plans were kept reads its workflow while that is
	// still the plan it was approved with: approved before the run started.
	if plan, err := store.ExecutionPlan(ctx, "run-1"); err != nil || len(plan.Steps) != 2 {
		t.Fatalf("approved before the run started: %+v %v", plan, err)
	}
	// Approved again, or planned again, it is no longer what the run ran.
	time.Sleep(2 * time.Millisecond)
	if err := store.ApproveWorkflow(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecutionPlan(ctx, "run-1"); err == nil || !strings.Contains(err.Error(), "planned again") {
		t.Fatalf("a plan approved after the run started is not the run's: %v", err)
	}
	if err := store.SaveWorkflow(ctx, "planned", spec); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecutionPlan(ctx, "run-1"); err == nil || !strings.Contains(err.Error(), "planned again") {
		t.Fatalf("a run whose task was planned again has no plan to reopen: %v", err)
	}
	// A run that kept its plan is unaffected.
	if err := store.SaveExecutionPlan(ctx, "run-1", spec); err != nil {
		t.Fatal(err)
	}
	if plan, err := store.ExecutionPlan(ctx, "run-1"); err != nil || plan.ID != spec.ID {
		t.Fatalf("kept plan: %+v %v", plan, err)
	}
}

func TestAReviewingRunKeepsItsReviewAcrossARestart(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	execution, err := store.Execution(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	execution.State = Reviewing
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	for _, input := range []InputRequest{
		{ID: "review", ExecutionID: "run-1", StepID: "approval", Agent: "human", Question: "Review round 1", State: InputPending, Review: true, ReviewTarget: "implement"},
		{ID: "asked", ExecutionID: "run-1", StepID: "implement", Agent: "human", Question: "Which name?", State: InputAnswered, Answer: "Ada"},
	} {
		if err := store.SaveInput(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MarkInterruptedExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	if execution, err := store.Execution(ctx, "run-1"); err != nil || execution.State != Reviewing {
		t.Fatalf("a reviewing run is not interrupted: %+v %v", execution, err)
	}
	reviews, err := store.PendingReviews(ctx, "run-1")
	if err != nil || len(reviews) != 1 || reviews[0].ReviewTarget != "implement" || !reviews[0].Review {
		t.Fatalf("its review is still open: %+v %v", reviews, err)
	}
	// What a review sends back reaches only its targets.
	if answered, err := store.AnsweredInputs(ctx, "run-1"); err != nil || len(answered) != 1 || answered[0].ID != "asked" {
		t.Fatalf("answered questions leave reviews out: %+v %v", answered, err)
	}

	// Approve and Cancel each move a reviewing run once, and not a run
	// that has moved on.
	if moved, err := store.ResumeReview(ctx, "run-1", 2); err != nil || moved {
		t.Fatalf("an approval for another round changes nothing: %v %v", moved, err)
	}
	if moved, err := store.ResumeReview(ctx, "run-1", 1); err != nil || !moved {
		t.Fatalf("resume: %v %v", moved, err)
	}
	if moved, err := store.CancelReview(ctx, "run-1"); err != nil || moved {
		t.Fatalf("a run that was approved is not cancelled as a review: %v %v", moved, err)
	}
	execution.State = Reviewing
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if started, err := store.StartRound(ctx, "run-1", 1, nil); err != nil || !started {
		t.Fatalf("a reviewing run is sent back: %v %v", started, err)
	}
	if err := store.ResolveReviews(ctx, "run-1", InputAnswered, "implement: Again."); err != nil {
		t.Fatal(err)
	}
	if reviews, err := store.PendingReviews(ctx, "run-1"); err != nil || len(reviews) != 0 {
		t.Fatalf("resolved: %+v %v", reviews, err)
	}
	if input, err := store.Input(ctx, "review"); err != nil || input.Answer != "implement: Again." || input.ResolvedAt.IsZero() {
		t.Fatalf("the review records its answer: %+v %v", input, err)
	}
}

func TestACheckRoundStartsOnlyWithinEachFailedStagesCap(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	execution, err := store.Execution(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	execution.State = "running"
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	for _, check := range []CheckRecord{
		{ID: "run-1-verify-0", StepID: "verify", StepRunID: "run-1-verify"},
		{ID: "run-1-lint-0", StepID: "lint", StepRunID: "run-1-lint"},
	} {
		check.ExecutionID, check.Status, check.Argv = "run-1", CheckFailed, []string{"go", "test"}
		if err := store.SaveCheck(ctx, check); err != nil {
			t.Fatal(err)
		}
	}
	failed := func(check string) []FeedbackItem {
		return []FeedbackItem{{ID: "k1", Target: "implement", From: FeedbackFromCheck, Text: "go test exited with code 1", CheckID: check, Tail: "FAIL"}}
	}
	verify := []StageCap{{Stage: "verify", Max: 2}}
	round := func(want int) {
		t.Helper()
		execution, err := store.Execution(ctx, "run-1")
		if err != nil || execution.Round != want || execution.State != "running" {
			t.Fatalf("run is in round %d, %s, want round %d running: %v", execution.Round, execution.State, want, err)
		}
	}
	for current, used := range []int{1, 2} {
		started, err := store.StartCheckRound(ctx, "run-1", current+1, failed("run-1-verify-0"), verify)
		if err != nil || !started.Started || started.Over != "" || started.Used["verify"] != used {
			t.Fatalf("round %d starts and counts %d: %+v %v", current+2, used, started, err)
		}
	}
	round(3)
	items, err := store.Feedback(ctx, "run-1", 3)
	if err != nil || len(items) != 1 || items[0].CheckID != "run-1-verify-0" || items[0].Tail != "FAIL" || items[0].From != FeedbackFromCheck {
		t.Fatalf("a check's item keeps its check and tail: %+v %v", items, err)
	}
	// A third automatic round would take verify past its cap of 2.
	over, err := store.StartCheckRound(ctx, "run-1", 3, failed("run-1-verify-0"), verify)
	if err != nil || over.Started || over.Over != "verify" || over.Used["verify"] != 2 {
		t.Fatalf("verify is over its cap: %+v %v", over, err)
	}
	round(3)
	if stale, err := store.StartCheckRound(ctx, "run-1", 2, failed("run-1-lint-0"), []StageCap{{Stage: "lint", Max: 2}}); err != nil || stale.Started {
		t.Fatalf("a round the run has left starts nothing: %+v %v", stale, err)
	}
	round(3)

	// A person's round gives every stage its cap again.
	execution.State, execution.Round = "failed", 3
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if started, err := store.StartRound(ctx, "run-1", 3, []FeedbackItem{{ID: "f1", Target: "implement", From: FeedbackFromHuman, Text: "Try again."}}); err != nil || !started {
		t.Fatalf("a person's round starts: %v %v", started, err)
	}
	execution.State, execution.Round = "running", 4
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	started, err := store.StartCheckRound(ctx, "run-1", 4, failed("run-1-verify-0"), verify)
	if err != nil || !started.Started || started.Used["verify"] != 1 {
		t.Fatalf("after a person's round verify has its cap again: %+v %v", started, err)
	}
	// One round sent back by two stages counts for both, and either over its
	// cap stops it.
	both := append(failed("run-1-verify-0"), FeedbackItem{ID: "k2", Target: "implement", From: FeedbackFromCheck, Text: "lint failed", CheckID: "run-1-lint-0"})
	stopped, err := store.StartCheckRound(ctx, "run-1", 5, both, []StageCap{{Stage: "verify", Max: 2}, {Stage: "lint", Max: 1}})
	if err != nil || !stopped.Started || stopped.Used["verify"] != 2 || stopped.Used["lint"] != 1 {
		t.Fatalf("both stages count the round: %+v %v", stopped, err)
	}
	round(6)
	if over, err := store.StartCheckRound(ctx, "run-1", 6, both, []StageCap{{Stage: "verify", Max: 5}, {Stage: "lint", Max: 1}}); err != nil || over.Started || over.Over != "lint" {
		t.Fatalf("lint over its cap stops the round for both: %+v %v", over, err)
	}
	round(6)

	// So does resuming the run, though the round it starts sends the stopped
	// round's check items again: a person started it, and it is not a round
	// the checks started.
	execution.State, execution.Round = "failed", 6
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if started, err := store.StartResumeRound(ctx, "run-1", 6, []string{"implement"}, false, nil); err != nil || !started {
		t.Fatalf("resume round 6: %v %v", started, err)
	}
	if carried, err := store.Feedback(ctx, "run-1", 7); err != nil || len(carried) != 2 {
		t.Fatalf("round 7 sends both check items again: %+v %v", carried, err)
	}
	execution.State, execution.Round = "running", 7
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if again, err := store.StartCheckRound(ctx, "run-1", 7, both, []StageCap{{Stage: "verify", Max: 2}, {Stage: "lint", Max: 1}}); err != nil || !again.Started || again.Used["lint"] != 1 || again.Used["verify"] != 1 {
		t.Fatalf("after a resume both stages have their caps again: %+v %v", again, err)
	}
}

// TestACheckPauseIsKeptAndNeverAnAnswerForWorkers checks that check_pause
// is stored and read back, and that the answer is never handed to workers.
func TestACheckPauseIsKeptAndNeverAnAnswerForWorkers(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	for _, input := range []InputRequest{
		{ID: "pause", ExecutionID: "run-1", StepID: "verify", Agent: "human", Question: "verify could not run its checks", Options: []string{"Re-run checks", "Fail the run"}, State: InputAnswered, Answer: "Re-run checks", CheckPause: true},
		{ID: "asked", ExecutionID: "run-1", StepID: "implement", Agent: "human", Question: "Which name?", State: InputAnswered, Answer: "Ada"},
	} {
		if err := store.SaveInput(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	if pause, err := store.Input(ctx, "pause"); err != nil || !pause.CheckPause || len(pause.Options) != 2 {
		t.Fatalf("the pause is read back as one: %+v %v", pause, err)
	}
	if listed, err := store.Inputs(ctx, InputAnswered, 10); err != nil || len(listed) != 2 || !slices.ContainsFunc(listed, func(input InputRequest) bool { return input.CheckPause }) {
		t.Fatalf("the inbox lists the pause as one: %+v %v", listed, err)
	}
	if answered, err := store.AnsweredInputs(ctx, "run-1"); err != nil || len(answered) != 1 || answered[0].ID != "asked" {
		t.Fatalf("answered questions leave check pauses out: %+v %v", answered, err)
	}
}

// TestPermissionsAndStagesAreKept checks that what a permission request asks
// for and whether a human-input stage asked are stored and read back.
func TestPermissionsAndStagesAreKept(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	asks := []PermissionAsk{{Command: "go test ./..."}, {Tool: "WebFetch", Detail: "https://example.com"}}
	for _, input := range []InputRequest{
		{ID: "permission", ExecutionID: "run-1", StepID: "plan", A2ATaskID: "task-1", Agent: "researcher", Question: "researcher was denied 2 things", Options: []string{"Allow for this run", "Deny"}, State: InputPending, Permissions: asks},
		{ID: "stage", ExecutionID: "run-1", StepID: "confirm", A2ATaskID: "task-2", Agent: "human", Question: "Approve the plan?", State: InputPending, Stage: true},
	} {
		if err := store.SaveInput(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	if permission, err := store.Input(ctx, "permission"); err != nil || !slices.Equal(permission.Permissions, asks) || permission.Stage {
		t.Fatalf("the permission request keeps what it asks for: %+v %v", permission, err)
	}
	if stage, err := store.TaskInputs(ctx, "task-2"); err != nil || len(stage) != 1 || !stage[0].Stage || len(stage[0].Permissions) != 0 {
		t.Fatalf("the stage's question is read back as one: %+v %v", stage, err)
	}
	listed, err := store.Inputs(ctx, InputPending, 10)
	if err != nil || len(listed) != 2 || !slices.ContainsFunc(listed, func(input InputRequest) bool { return input.Stage }) || !slices.ContainsFunc(listed, func(input InputRequest) bool { return len(input.Permissions) == 2 }) {
		t.Fatalf("the inbox lists both as they were saved: %+v %v", listed, err)
	}
}
