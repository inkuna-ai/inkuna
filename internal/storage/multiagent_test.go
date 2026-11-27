package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestMultiAgentTables(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	project := Project{ID: "project", Name: "Project", Repository: t.TempDir(), DefaultRuntime: domain.RuntimeClaude}
	if err := store.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	task := domain.TaskSpec{Version: 1, ID: "task", Kind: domain.TaskImplementation, Objective: "Test", Repository: project.Repository, Budget: domain.Budget{WallTimeSeconds: 60}}
	if err := store.SaveTask(ctx, project.ID, "approved", task); err != nil {
		t.Fatal(err)
	}
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{{ID: "approval", Title: "Approve", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
	if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
		t.Fatal(err)
	}
	execution := Execution{ID: "run-1", WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: project.ID, State: "running", Title: "Test"}
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	input := InputRequest{ID: "input-1", ExecutionID: execution.ID, StepID: "worker", Agent: "implementer", Question: "Which database?", Options: []string{"sqlite", "postgres"}, State: InputPending}
	if err := store.SaveInput(ctx, input); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Inputs(ctx, InputPending, 0)
	if err != nil || len(pending) != 1 || pending[0].Options[1] != "postgres" {
		t.Fatalf("unexpected pending inputs %+v %v", pending, err)
	}
	claimed, err := store.ClaimInput(ctx, input.ID, "sqlite")
	if err != nil || !claimed {
		t.Fatalf("first claim must succeed: %v %v", claimed, err)
	}
	if again, err := store.ClaimInput(ctx, input.ID, "postgres"); err != nil || again {
		t.Fatalf("second claim must fail: %v %v", again, err)
	}
	if delivering, err := store.Input(ctx, input.ID); err != nil || delivering.State != InputDelivering || delivering.Answer != "sqlite" {
		t.Fatalf("claim did not record delivering state: %+v %v", delivering, err)
	}
	if restored, err := store.FinishDelivery(ctx, input.ID, false); err != nil || !restored {
		t.Fatalf("restore to pending must succeed: %v %v", restored, err)
	}
	if pendingAgain, err := store.Input(ctx, input.ID); err != nil || pendingAgain.State != InputPending || pendingAgain.Answer != "" {
		t.Fatalf("restore did not clear the answer: %+v %v", pendingAgain, err)
	}
	if _, err := store.ClaimInput(ctx, input.ID, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if finished, err := store.FinishDelivery(ctx, input.ID, true); err != nil || !finished {
		t.Fatalf("finish must succeed: %v %v", finished, err)
	}
	loaded, err := store.Input(ctx, input.ID)
	if err != nil || loaded.Answer != "sqlite" || loaded.State != InputAnswered || loaded.ResolvedAt.IsZero() {
		t.Fatalf("unexpected answered input %+v %v", loaded, err)
	}
	if again, err := store.FinishDelivery(ctx, input.ID, true); err != nil || again {
		t.Fatalf("finishing a non-delivering input must be a no-op: %v %v", again, err)
	}
	cancelled := InputRequest{ID: "input-2", ExecutionID: execution.ID, StepID: "worker", Agent: "implementer", Question: "Retry?", State: InputPending}
	if err := store.SaveInput(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInput(ctx, cancelled.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelPendingInputs(ctx, execution.ID); err != nil {
		t.Fatal(err)
	}
	if changed, err := store.FinishDelivery(ctx, cancelled.ID, true); err != nil || changed {
		t.Fatalf("a cancelled delivery must not be finalized: %v %v", changed, err)
	}
	if after, err := store.Input(ctx, cancelled.ID); err != nil || after.State != InputCanceled {
		t.Fatalf("cancellation must be preserved: %+v %v", after, err)
	}
	a2aTask := a2a.Task{ID: "a2a-1", ContextID: task.ID, Agent: "implementer", Status: a2a.TaskStatus{State: a2a.StateWorking, Timestamp: time.Now()}}
	if err := store.SaveA2ATask(ctx, a2aTask); err != nil {
		t.Fatal(err)
	}
	a2aTask.Status.State = a2a.StateCompleted
	if err := store.SaveA2ATask(ctx, a2aTask); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.A2ATasks(ctx, task.ID)
	if err != nil || len(tasks) != 1 || tasks[0].Status.State != a2a.StateCompleted {
		t.Fatalf("unexpected a2a tasks %+v %v", tasks, err)
	}
	record := ArtifactRecord{Digest: "abc", TaskID: task.ID, ExecutionID: execution.ID, StepID: "worker", Agent: "implementer", Kind: "handoff", Name: "handoff.json", MediaType: "application/json", Size: 12}
	if err := store.RecordArtifact(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordArtifact(ctx, record); err != nil {
		t.Fatalf("re-recording must be idempotent: %v", err)
	}
	artifacts, err := store.TaskArtifacts(ctx, task.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].Agent != "implementer" {
		t.Fatalf("unexpected artifacts %+v %v", artifacts, err)
	}
	if _, err := store.ArtifactRecord(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCommit(ctx, CommitRecord{ExecutionID: execution.ID, StepID: "worker", Agent: "implementer", Hash: "deadbeef", Subject: "inkuna(worker): change", Stat: "1 file changed"}); err != nil {
		t.Fatal(err)
	}
	commits, err := store.Commits(ctx, execution.ID)
	if err != nil || len(commits) != 1 || commits[0].Hash != "deadbeef" || commits[0].Agent != "implementer" {
		t.Fatalf("unexpected commits %+v %v", commits, err)
	}
	if single, err := store.Execution(ctx, execution.ID); err != nil || single.Title != "Test" {
		t.Fatalf("keyed execution lookup failed: %+v %v", single, err)
	}
	if _, err := store.Execution(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing execution must report not found, got %v", err)
	}
	if err := store.MarkInterruptedExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	executions, err := store.Executions(ctx, 10)
	if err != nil || len(executions) != 1 || executions[0].State != "interrupted" || executions[0].FinishedAt.IsZero() {
		t.Fatalf("unexpected executions after interrupt %+v %v", executions, err)
	}
}

func TestA2AThreadsKeepEveryMessageOnce(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	asked := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	task := a2a.Task{ID: "a2a-1", ContextID: "task", Agent: "implementer", Status: a2a.TaskStatus{State: a2a.StateInputRequired},
		History: []a2a.Message{
			{MessageID: "m1", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.TextPart("Implement it")}, Timestamp: asked},
			{MessageID: "m2", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.TextPart("Which database?")}, Timestamp: asked.Add(time.Second)},
		}}
	if err := store.SaveA2ATask(ctx, task); err != nil {
		t.Fatal(err)
	}
	// A later snapshot repeats the history and appends the answer.
	task.History = append(task.History, a2a.Message{MessageID: "m3", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.TextPart("sqlite")}, Timestamp: asked.Add(2 * time.Second)})
	task.Status.State = a2a.StateCompleted
	if err := store.SaveA2ATask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveA2ATask(ctx, a2a.Task{ID: "a2a-0", ContextID: "task", Agent: "reviewer", Status: a2a.TaskStatus{State: a2a.StateWorking}}); err != nil {
		t.Fatal(err)
	}

	messages, err := store.A2AMessages(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[0].MessageID != "m1" || messages[2].Text() != "sqlite" {
		t.Fatalf("messages = %+v, want m1..m3 in order exactly once", messages)
	}
	threads, err := store.A2AThreads(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 || threads[0].TaskID != "a2a-0" || threads[1].TaskID != task.ID {
		t.Fatalf("threads = %+v, want most recently updated first", threads)
	}
	if got := threads[1]; got.MessageCount != 3 || got.LastMessage.Text() != "sqlite" || got.State != string(a2a.StateCompleted) {
		t.Fatalf("thread summary = %+v", got)
	}
	if threads[0].MessageCount != 0 || threads[0].LastMessage.MessageID != "" {
		t.Fatalf("empty thread summary = %+v", threads[0])
	}
	loaded, err := store.A2ATask(ctx, task.ID)
	if err != nil || len(loaded.History) != 3 {
		t.Fatalf("A2ATask = %+v, %v", loaded, err)
	}
	if _, err := store.A2ATask(ctx, "absent"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}
}

func TestThreadLookupsAreKeyedByTaskNotCappedByHistory(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	project := Project{ID: "project", Name: "Project", Repository: t.TempDir(), DefaultRuntime: domain.RuntimeClaude}
	if err := store.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// More executions and pending questions than the capped list queries
	// return, so the oldest task would be invisible to them.
	const total = 520
	for index := 0; index < total; index++ {
		id := fmt.Sprintf("%04d", index)
		task := domain.TaskSpec{Version: 1, ID: "task-" + id, Kind: domain.TaskImplementation, Objective: "Test", Repository: project.Repository, Budget: domain.Budget{WallTimeSeconds: 60}}
		if err := store.SaveTask(ctx, project.ID, "approved", task); err != nil {
			t.Fatal(err)
		}
		workflow := domain.WorkflowSpec{Version: 1, ID: "workflow-" + id, TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: start, Steps: []domain.WorkflowStep{{ID: "approval", Title: "Approve", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
		if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
			t.Fatal(err)
		}
		at := start.Add(time.Duration(index) * time.Minute)
		if err := store.SaveExecution(ctx, Execution{ID: "run-" + id, WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: project.ID, State: "running", Title: "Run " + id, StartedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveInput(ctx, InputRequest{ID: "input-" + id, ExecutionID: "run-" + id, StepID: "worker", A2ATaskID: "a2a-" + id, Agent: "implementer", Question: "Q" + id, State: InputPending, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	// The oldest task also has a rerun and a second, later question.
	if err := store.SaveExecution(ctx, Execution{ID: "run-0000-again", WorkflowID: "workflow-0000", TaskID: "task-0000", ProjectID: project.ID, State: "running", Title: "Rerun", StartedAt: start.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveInput(ctx, InputRequest{ID: "input-0000-later", ExecutionID: "run-0000", StepID: "worker", A2ATaskID: "a2a-0000", Agent: "implementer", Question: "Later", State: InputPending, CreatedAt: start.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	runs, err := store.LatestExecutionsByTask(ctx, []string{"task-0000", "task-0519", "task-absent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs["task-0000"].ID != "run-0000-again" || runs["task-0519"].Title != "Run 0519" {
		t.Fatalf("LatestExecutionsByTask = %#v", runs)
	}
	pending, err := store.OldestPendingInputs(ctx, []string{"a2a-0000", "a2a-0519", "a2a-absent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending["a2a-0000"].ID != "input-0000" || pending["a2a-0519"].Question != "Q0519" {
		t.Fatalf("OldestPendingInputs = %#v", pending)
	}
	// Chunked IN lists cover more ids than one statement takes.
	all := make([]string, 0, total)
	for index := 0; index < total; index++ {
		all = append(all, fmt.Sprintf("a2a-%04d", index))
	}
	if everything, err := store.OldestPendingInputs(ctx, all); err != nil || len(everything) != total {
		t.Fatalf("chunked lookup returned %d of %d: %v", len(everything), total, err)
	}
	if none, err := store.LatestExecutionsByTask(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("an empty lookup must be empty: %#v %v", none, err)
	}
}

func TestExecutionsRememberBranchCleanupAndRemoval(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	project := Project{ID: "project", Name: "Project", Repository: t.TempDir(), DefaultRuntime: domain.RuntimeClaude}
	if err := store.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	task := domain.TaskSpec{Version: 1, ID: "task", Kind: domain.TaskImplementation, Objective: "Test", Repository: project.Repository, Budget: domain.Budget{WallTimeSeconds: 60}}
	if err := store.SaveTask(ctx, project.ID, "approved", task); err != nil {
		t.Fatal(err)
	}
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{{ID: "approval", Title: "Approve", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
	if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	for index, id := range []string{"run-a", "run-b", "run-c"} {
		execution := Execution{ID: id, WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: project.ID, State: "completed", Title: id, StartedAt: start.Add(time.Duration(index) * time.Second),
			Worktree: "/worktrees/" + id, Branch: "feature/" + id, BaseCommit: "base-" + id, Cleanup: domain.CleanupPrune}
		if id == "run-c" {
			execution.Worktree = ""
		}
		if err := store.SaveExecution(ctx, execution); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.Execution(ctx, "run-a")
	if err != nil || removed.Branch != "feature/run-a" || removed.BaseCommit != "base-run-a" || removed.Cleanup != domain.CleanupPrune || !removed.WorktreeRemovedAt.IsZero() {
		t.Fatalf("execution did not round-trip: %#v %v", removed, err)
	}
	removed.WorktreeRemovedAt = time.Now().UTC()
	if err := store.SaveExecution(ctx, removed); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ExecutionsWithWorktrees(ctx, project.ID)
	if err != nil || len(pending) != 1 || pending[0].ID != "run-b" {
		t.Fatalf("only run-b still has a worktree: %#v %v", pending, err)
	}
	reloaded, err := store.Execution(ctx, "run-a")
	if err != nil || reloaded.WorktreeRemovedAt.IsZero() {
		t.Fatalf("removal time was not stored: %#v %v", reloaded, err)
	}
}
