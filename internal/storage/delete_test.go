package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// globalTables hold nothing a task's delete removes: they are shared, or,
// like kept_worktrees, record what outlives a deleted task. pull_requests
// is keyed by branch, and a check forgets a link once no run is on it.
// The outbox keeps what was queued for a deleted task until its sinks have
// it, and the installation, sinks, and backfills belong to the data
// directory. Every other table must be emptied of a task's rows when it goes.
var globalTables = []string{"projects", "skills", "staffing_decisions", "artifacts", "schema_migrations", "kept_worktrees", "pull_requests",
	"installation", "outbox", "outbox_sinks", "outbox_backfills"}

func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func historyStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), Project{ID: "project", Name: "Project", Repository: t.TempDir(), DefaultRuntime: domain.RuntimeClaude}); err != nil {
		t.Fatal(err)
	}
	return store
}

// fillTask gives a task a row in every table a task owns, with a run per
// id in runs. Every id it makes starts with the task's id, so a test can
// look for any trace of it. shared is a blob the task indexes that another
// task may index too.
func fillTask(t *testing.T, store *Store, task string, runs []string, shared string) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.DB().ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	now := "2026-11-01T00:00:00Z"
	spec := domain.TaskSpec{Version: 1, ID: task, Kind: domain.TaskImplementation, Objective: "Objective of " + task, Repository: ".", Budget: domain.Budget{WallTimeSeconds: 60}}
	workflow := domain.WorkflowSpec{Version: 1, ID: task + "-plan", TaskID: task, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{
		{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Permission: domain.PermissionWorktree, AllowedPaths: []string{"."}, Budget: domain.Budget{WallTimeSeconds: 10}},
		{ID: "approval", Title: "Approval", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, DependsOn: []string{"implement"}, Budget: domain.Budget{WallTimeSeconds: 10}},
	}}
	check(store.SaveTask(ctx, "project", "planned", spec))
	check(store.ApproveWorkflow(ctx, workflow))
	exec(`INSERT INTO approvals(id,task_id,kind,payload_json,state,created_at) VALUES(?,?,'plan','{}','approved',?)`, task+"-approval", task, now)
	exec(`INSERT INTO github_links(task_id,repository,issue_number,issue_url,marker,created_at) VALUES(?,'inkuna-ai/inkuna',64,'https://github.com/inkuna-ai/inkuna/issues/64',?,?)`, task, task+"-marker", now)
	// Planning belongs to the task and to none of its runs.
	exec(`INSERT INTO usage(task_id,execution_id,step_id,call_id,turn,kind,agent,runtime,model,input_tokens,cache_write_tokens,cache_read_tokens,output_tokens,turns,prompt_bytes,outcome,started_at,finished_at)
VALUES(?,NULL,'plan',?,1,'orchestrator','orchestrator','claude','claude-opus-5-5',1,0,0,1,1,1,'completed',?,?)`, task, task+"-plan-call", now, now)
	check(store.RecordArtifact(ctx, ArtifactRecord{Digest: digestOf(task + " decision"), TaskID: task, StepID: "plan", Kind: "decision", Name: "orchestrator-decision.json", Size: 10}))
	check(store.SaveA2ATask(ctx, a2a.Task{ID: task + "-planner", ContextID: task, Agent: "orchestrator", Status: a2a.TaskStatus{State: a2a.StateCompleted},
		History: []a2a.Message{{MessageID: task + "-planner-1", Role: a2a.RoleUser, ContextID: task, Parts: []a2a.Part{a2a.TextPart("plan it")}}}}))
	for index, run := range runs {
		stepRun := run + "-implement"
		check(store.SaveExecution(ctx, Execution{ID: run, WorkflowID: workflow.ID, TaskID: task, ProjectID: "project", State: "completed", Title: "Title of " + run,
			Branch: "feature/" + run, StartedAt: time.Date(2026, 11, 1, 0, index, 0, 0, time.UTC)}))
		check(store.SaveExecutionStory(ctx, run, []byte(`{"id":"64"}`)))
		check(store.StartRun(ctx, stepRun, run, workflow.ID, "implement", 1, domain.RuntimeClaude, ""))
		check(store.AppendEvent(ctx, domain.RunEvent{Version: 1, RunID: stepRun, Sequence: 1, Timestamp: time.Now(), Kind: domain.EventLifecycle, Summary: "started"}))
		check(store.SaveInput(ctx, InputRequest{ID: run + "-input", ExecutionID: run, StepID: "implement", A2ATaskID: run + "-question", Agent: "implementer", Question: "Which?", State: InputAnswered}))
		check(store.SaveA2ATask(ctx, a2a.Task{ID: run + "-worker", ContextID: task, Agent: "implementer", Status: a2a.TaskStatus{State: a2a.StateCompleted}, Metadata: map[string]any{"executionId": run},
			History: []a2a.Message{{MessageID: run + "-worker-1", Role: a2a.RoleUser, ContextID: task, Parts: []a2a.Part{a2a.TextPart("do it")}}}}))
		// A question's task names its run only on its input.
		check(store.SaveA2ATask(ctx, a2a.Task{ID: run + "-question", ContextID: task, Agent: "human", Status: a2a.TaskStatus{State: a2a.StateCompleted},
			History: []a2a.Message{{MessageID: run + "-question-1", Role: a2a.RoleAgent, ContextID: task, Parts: []a2a.Part{a2a.TextPart("Which?")}}}}))
		check(store.RecordArtifact(ctx, ArtifactRecord{Digest: digestOf(run), TaskID: task, ExecutionID: run, StepID: "implement", Kind: "diff", Name: "implement.diff", Size: 20}))
		check(store.SaveCommit(ctx, CommitRecord{ExecutionID: run, StepID: "implement", Round: 1, Hash: run + "-commit", Subject: "Implement", Stat: ""}))
		exec(`INSERT INTO doc_reads(task_id,step_id,run_id,project_id,agent,runtime,docs_dir,doc_path,tool,opened_at) VALUES(?,'implement',?,'project','implementer','claude','docs','docs/a.md','Read',?)`, task, run, now)
		exec(`INSERT INTO usage(task_id,execution_id,step_id,call_id,turn,kind,agent,runtime,model,input_tokens,cache_write_tokens,cache_read_tokens,output_tokens,turns,prompt_bytes,outcome,started_at,finished_at)
VALUES(?,?,'implement',?,1,'worker','implementer','claude','claude-opus-5-5',1,0,0,1,1,1,'completed',?,?)`, task, run, run+"-call", now, now)
		exec(`INSERT INTO viewed_files(execution_id,path,blob,viewed_at) VALUES(?,'main.go',?,?)`, run, run+"-blob", now)
		exec(`INSERT INTO checks(id,execution_id,step_id,step_run_id,position,argv,status) VALUES(?,?,'verify',?,0,'["go","test"]','passed')`, run+"-check", run, run+"-verify")
		exec(`INSERT INTO feedback(execution_id,round,id,target,author,text,created_at) VALUES(?,2,?,'implement','person','Fix it',?)`, run, run+"-feedback", now)
		exec(`INSERT INTO comment_threads(execution_id,id,path,side,line,end_line,base,commit_hash,round,created_at) VALUES(?,?,'main.go','new',1,1,'base','head',1,?)`, run, run+"-thread", now)
		exec(`INSERT INTO comments(execution_id,id,thread_id,body,target,created_at,updated_at) VALUES(?,?,?,'Rename this','implement',?,?)`, run, run+"-comment", run+"-thread", now, now)
		exec(`INSERT INTO resumes(execution_id,round,created_at) VALUES(?,2,?)`, run, now)
		exec(`INSERT INTO tracker_changes(execution_id,id,round,step_id,step_run_id,position,kind,change_json,state,created_at,updated_at) VALUES(?,?,1,'implement',?,0,'comment','{}','applied',?,?)`, run, run+"-change", stepRun, now, now)
	}
	if shared != "" {
		check(store.RecordArtifact(ctx, ArtifactRecord{Digest: shared, TaskID: task, ExecutionID: runs[0], StepID: "implement", Kind: "context", Name: "implement-context.json", Size: 30}))
	}
}

// tables lists every table in the database.
func tables(t *testing.T, store *Store) []string {
	t.Helper()
	rows, err := store.DB().Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

// traces counts, per table, the rows in which any column holds text.
func traces(t *testing.T, store *Store, text string) map[string]int {
	t.Helper()
	found := make(map[string]int)
	for _, table := range tables(t, store) {
		rows, err := store.DB().Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		var conditions []string
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				t.Fatal(err)
			}
			conditions = append(conditions, fmt.Sprintf(`instr(CAST("%s" AS TEXT), ?) > 0`, column))
		}
		_ = rows.Close()
		args := make([]any, len(conditions))
		for index := range args {
			args[index] = text
		}
		var count int
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM "`+table+`" WHERE `+strings.Join(conditions, " OR "), args...).Scan(&count); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if count > 0 {
			found[table] = count
		}
	}
	return found
}

func TestDeletingATaskRemovesEveryRowItOwnsAndKeepsASharedBlob(t *testing.T) {
	store := historyStore(t)
	shared := digestOf("the same context for both")
	fillTask(t, store, "alpha", []string{"alpha-run1", "alpha-run2"}, shared)
	fillTask(t, store, "beta", []string{"beta-run1"}, shared)
	before := traces(t, store, "alpha")
	for _, table := range tables(t, store) {
		if !slices.Contains(globalTables, table) && before[table] == 0 {
			t.Errorf("the fixture gives task alpha no row in %s; a table a task owns must be filled here and deleted with it, or listed in globalTables", table)
		}
	}
	others := traces(t, store, "beta")

	removals, err := store.DeleteTasks(context.Background(), []string{"alpha", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removals) != 1 || removals[0].TaskID != "alpha" {
		t.Fatalf("removals = %+v", removals)
	}
	if left := traces(t, store, "alpha"); len(left) > 0 {
		t.Fatalf("rows of the deleted task are left: %v", left)
	}
	if after := traces(t, store, "beta"); fmt.Sprint(after) != fmt.Sprint(others) {
		t.Fatalf("the other task's rows changed:\n%v\nwant\n%v", after, others)
	}
	var orphans int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("foreign key check found %d rows (%v)", orphans, err)
	}

	removal := removals[0]
	counted := make(map[string]int64)
	for _, rows := range removal.Rows {
		counted[rows.Table] = rows.Rows
	}
	for table := range before {
		if counted[table] == 0 {
			t.Errorf("the removal does not count %s", table)
		}
	}
	if counted["executions"] != 2 || counted["runs"] != 2 || counted["a2a_tasks"] != 5 || counted["usage"] != 3 || counted["tasks"] != 1 {
		t.Fatalf("rows = %+v", removal.Rows)
	}
	if len(removal.Runs) != 2 || removal.Runs[0].ID != "alpha-run1" || string(removal.Runs[0].Story) != `{"id":"64"}` || removal.Runs[1].Branch != "feature/alpha-run2" {
		t.Fatalf("runs = %+v", removal.Runs)
	}
	if len(removal.GitHubLinks) != 1 || removal.GitHubLinks[0].Issue != 64 {
		t.Fatalf("links = %+v", removal.GitHubLinks)
	}
	want := []string{digestOf("alpha decision"), digestOf("alpha-run1"), digestOf("alpha-run2"), shared}
	slices.Sort(want)
	if fmt.Sprint(removal.Artifacts) != fmt.Sprint(want) {
		t.Fatalf("artifacts = %v, want %v", removal.Artifacts, want)
	}
	for digest, kept := range map[string]bool{shared: true, digestOf("alpha-run1"): false, digestOf("beta-run1"): true} {
		if referenced, err := store.ArtifactReferenced(context.Background(), digest); err != nil || referenced != kept {
			t.Errorf("ArtifactReferenced(%s) = %v, %v; want %v", digest[:8], referenced, err, kept)
		}
	}
}

func TestADeleteIsRefusedWholeWhenAnyRunHasNotEnded(t *testing.T) {
	store := historyStore(t)
	ctx := context.Background()
	fillTask(t, store, "alpha", []string{"alpha-run1"}, "")
	fillTask(t, store, "beta", []string{"beta-run1", "beta-run2"}, "")
	before := traces(t, store, "alpha")
	refused := func(ids []string, wantRun, wantReason string) {
		t.Helper()
		_, err := store.DeleteTasks(ctx, ids)
		var refusal *DeleteRefusedError
		if !errors.As(err, &refusal) || refusal.TaskID != "beta" || refusal.RunID != wantRun || !strings.Contains(err.Error(), wantReason) {
			t.Fatalf("DeleteTasks(%v) = %v, want run %s refused for %q", ids, err, wantRun, wantReason)
		}
		if after := traces(t, store, "alpha"); fmt.Sprint(after) != fmt.Sprint(before) {
			t.Fatalf("a refused delete removed rows: %v, want %v", after, before)
		}
	}
	for state, reason := range map[string]string{
		"running":    "is running",
		"queued":     "is queued",
		"waiting":    "is waiting for an answer or a permission",
		"reviewing":  "is paused for review",
		"cancelling": "is being cancelled",
		"someday":    "is someday",
	} {
		if _, err := store.DB().Exec(`UPDATE executions SET state=? WHERE id='beta-run2'`, state); err != nil {
			t.Fatal(err)
		}
		refused([]string{"alpha", "beta"}, "beta-run2", reason)
	}
	if _, err := store.DB().Exec(`UPDATE executions SET state='completed' WHERE id='beta-run2'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE tracker_changes SET state=? WHERE execution_id='beta-run1'`, TrackerChangeApplying); err != nil {
		t.Fatal(err)
	}
	refused([]string{"alpha", "beta"}, "beta-run1", "still making its tracker changes")

	if _, err := store.DeleteTasks(ctx, []string{"alpha", "gamma"}); !errors.Is(err, domain.ErrNotFound) || !strings.Contains(err.Error(), "gamma") {
		t.Fatalf("an unknown task = %v", err)
	}
	if _, err := store.DeleteTasks(ctx, []string{" "}); err == nil {
		t.Fatal("a delete must name a task")
	}
	if after := traces(t, store, "alpha"); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("a refused delete removed rows: %v", after)
	}
}

func TestTaskStandingsSayWhatADeleteWouldRefuse(t *testing.T) {
	store := historyStore(t)
	ctx := context.Background()
	fillTask(t, store, "alpha", []string{"alpha-run1"}, "")
	fillTask(t, store, "beta", []string{"beta-run1", "beta-run2"}, "")
	fillTask(t, store, "gamma", []string{"gamma-run1"}, "")
	if _, err := store.DB().Exec(`UPDATE executions SET state='running' WHERE id='beta-run2'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE tracker_changes SET state=? WHERE execution_id='gamma-run1'`, TrackerChangeApplying); err != nil {
		t.Fatal(err)
	}
	standings, err := store.TaskStandings(ctx, []string{"alpha", "beta", "gamma", "missing", " alpha "})
	if err != nil {
		t.Fatal(err)
	}
	if got := standings["alpha"]; got != (TaskStanding{Runs: 1}) {
		t.Fatalf("alpha = %+v, want one run and no refusal", got)
	}
	if got := standings["beta"]; got.Runs != 2 || got.Refused != "task beta cannot be deleted: run beta-run2 is running; delete it once the run has ended" {
		t.Fatalf("beta = %+v", got)
	}
	if got := standings["gamma"]; got.Runs != 1 || !strings.Contains(got.Refused, "still making its tracker changes") {
		t.Fatalf("gamma = %+v", got)
	}
	if _, ok := standings["missing"]; ok || len(standings) != 3 {
		t.Fatalf("a task without runs is left out: %+v", standings)
	}
	// The standing and the delete agree.
	if _, err := store.DeleteTasks(ctx, []string{"beta"}); err == nil || err.Error() != standings["beta"].Refused {
		t.Fatalf("DeleteTasks(beta) = %v, want %q", err, standings["beta"].Refused)
	}
}

func TestDeletingARunLeavesItsTaskAndItsOtherRuns(t *testing.T) {
	store := historyStore(t)
	ctx := context.Background()
	fillTask(t, store, "alpha", []string{"alpha-run1", "alpha-run2"}, "")
	// The second run stored a check log the first run's check then logged
	// too: one row indexes it, under the second run.
	log := digestOf("ok\n")
	if err := store.RecordArtifact(ctx, ArtifactRecord{Digest: log, TaskID: "alpha", ExecutionID: "alpha-run2", StepID: "verify", Kind: "check", Name: "verify.log", Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE checks SET log_digest=? WHERE execution_id='alpha-run1'`, log); err != nil {
		t.Fatal(err)
	}
	first := traces(t, store, "alpha-run1")
	plan := traces(t, store, "alpha-plan")

	removal, err := store.DeleteRun(ctx, "alpha-run2")
	if err != nil {
		t.Fatal(err)
	}
	if left := traces(t, store, "alpha-run2"); len(left) > 0 {
		t.Fatalf("rows of the deleted run are left: %v", left)
	}
	// The shared log's row moves to the run that still uses it.
	first["task_artifacts"]++
	if after := traces(t, store, "alpha-run1"); fmt.Sprint(after) != fmt.Sprint(first) {
		t.Fatalf("the task's other run changed: %v, want %v", after, first)
	}
	if referenced, err := store.ArtifactReferenced(ctx, log); err != nil || !referenced {
		t.Fatalf("the log the other run uses stays indexed: %v, %v", referenced, err)
	}
	if after := traces(t, store, "alpha-plan"); after["workflows"] != plan["workflows"] || after["a2a_tasks"] != plan["a2a_tasks"] {
		t.Fatalf("the plan and its planning thread stay: %v, want %v", after, plan)
	}
	if _, err := store.Task(ctx, "alpha"); err != nil {
		t.Fatalf("the task stays: %v", err)
	}
	if removal.TaskID != "alpha" || len(removal.Runs) != 1 || removal.Runs[0].ID != "alpha-run2" || fmt.Sprint(removal.Artifacts) != fmt.Sprint([]string{digestOf("alpha-run2")}) {
		t.Fatalf("removal = %+v", removal)
	}

	_, err = store.DeleteRun(ctx, "alpha-run1")
	var refusal *DeleteRefusedError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "only run; delete the task instead") {
		t.Fatalf("the only run = %v", err)
	}
	if _, err := store.DB().Exec(`UPDATE executions SET state='reviewing' WHERE id='alpha-run1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteRun(ctx, "alpha-run1"); !errors.As(err, &refusal) || !strings.Contains(err.Error(), "paused for review") {
		t.Fatalf("a paused run = %v", err)
	}
	if _, err := store.DeleteRun(ctx, "alpha-run9"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown run = %v", err)
	}
}

func TestHistoryTasksListsEachTaskWithItsRuns(t *testing.T) {
	store := historyStore(t)
	ctx := context.Background()
	fillTask(t, store, "alpha", []string{"alpha-run1", "alpha-run2"}, "")
	fillTask(t, store, "beta", []string{"beta-run1"}, "")
	if _, err := store.DB().Exec(`UPDATE executions SET state='reviewing' WHERE id='beta-run1'`); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTask(ctx, "project", "planned", domain.TaskSpec{Version: 1, ID: "gamma", Title: "Never ran", Kind: domain.TaskResearch, Objective: "Look around", Repository: ".", Budget: domain.Budget{WallTimeSeconds: 60}}); err != nil {
		t.Fatal(err)
	}

	named, err := store.HistoryTasks(ctx, []string{"beta", "alpha", "gamma", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, task := range named {
		got = append(got, fmt.Sprintf("%s %q runs=%d finished=%v", task.ID, task.Title, len(task.Runs), task.Finished()))
	}
	want := []string{`beta "Title of beta-run1" runs=1 finished=false`, `alpha "Title of alpha-run2" runs=2 finished=true`, `gamma "Never ran" runs=0 finished=true`}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("named tasks\n%v\nwant\n%v", got, want)
	}
	if named[1].Runs[0].ID != "alpha-run1" {
		t.Fatalf("runs are oldest first: %+v", named[1].Runs)
	}
	all, err := store.HistoryTasks(ctx, nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("every task = %+v, %v", all, err)
	}
	if _, err := store.HistoryTasks(ctx, []string{"alpha", "delta"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown task = %v", err)
	}
}
