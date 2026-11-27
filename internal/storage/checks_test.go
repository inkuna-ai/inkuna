package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestChecksKeepTheirOrderAndAreSettledWhenInkunaStops(t *testing.T) {
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
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{{ID: "verify", Title: "Verify", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
	if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveExecution(ctx, Execution{ID: "run", WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: project.ID, State: "running", Title: "Run"}); err != nil {
		t.Fatal(err)
	}

	started := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	exit := 1
	checks := []CheckRecord{
		{ID: "run-verify-0", ExecutionID: "run", StepID: "verify", StepRunID: "run-verify", Position: 0, Argv: []string{"task", "lint"}, Status: CheckFailed, ExitCode: &exit, StartedAt: started, FinishedAt: started.Add(2 * time.Second), LogDigest: "abc", LogBytes: 2048, LogCut: true},
		{ID: "run-verify-1", ExecutionID: "run", StepID: "verify", StepRunID: "run-verify", Position: 1, Argv: []string{"task", "test"}, Status: CheckQueued},
		{ID: "run-verify-2", ExecutionID: "run", StepID: "verify", StepRunID: "run-verify", Position: 2, Argv: []string{"task", "build"}, Status: CheckQueued},
	}
	for _, check := range checks {
		if err := store.SaveCheck(ctx, check); err != nil {
			t.Fatal(err)
		}
	}
	running := checks[1]
	running.Status, running.StartedAt = CheckRunning, started.Add(3*time.Second)
	if err := store.SaveCheck(ctx, running); err != nil {
		t.Fatal(err)
	}
	got, err := store.Checks(ctx, "run")
	if err != nil || len(got) != 3 || !reflect.DeepEqual(got[0], checks[0]) || got[1].Status != CheckRunning || got[1].ExitCode != nil {
		t.Fatalf("checks %+v, %v", got, err)
	}
	if one, err := store.Check(ctx, "run-verify-0"); err != nil || one.Argv[1] != "lint" {
		t.Fatalf("check %+v, %v", one, err)
	}
	if byRun, err := store.ChecksFor(ctx, []string{"run", "other"}); err != nil || len(byRun["run"]) != 3 || len(byRun["other"]) != 0 {
		t.Fatalf("checks by run %+v, %v", byRun, err)
	}

	if err := store.MarkInterruptedExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = store.Checks(ctx, "run")
	if err != nil || got[0].Status != CheckFailed || got[1].Status != CheckCancelled || got[1].FinishedAt.IsZero() || got[2].Status != CheckSkipped || !got[2].FinishedAt.IsZero() {
		t.Fatalf("after a restart %+v, %v", got, err)
	}
	if err := store.SaveCheck(ctx, CheckRecord{ID: "orphan", ExecutionID: "missing", StepID: "verify", StepRunID: "x", Status: CheckQueued}); err == nil {
		t.Fatal("a check of a run that does not exist must be refused")
	}
}
