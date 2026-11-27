package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestATaskKeepsTheCreatorItWasCreatedBy(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	task, err := store.Task(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	task.ID = "task-2"
	if err := store.CreateTask(ctx, "project", "planned", "ada", task); err != nil {
		t.Fatal(err)
	}
	// Renaming the plan saves the task again, and must keep who created it.
	task.Title = "Renamed"
	if err := store.SaveTask(ctx, "project", "planned", task); err != nil {
		t.Fatal(err)
	}
	if creator, err := store.TaskCreator(ctx, "task-2"); err != nil || creator != "ada" {
		t.Fatalf("TaskCreator(task-2) = %q, %v; want ada", creator, err)
	}
	if saved, err := store.Task(ctx, "task-2"); err != nil || saved.Title != "Renamed" {
		t.Fatalf("the later save is kept: %+v %v", saved, err)
	}
	if creator, err := store.TaskCreator(ctx, "task-1"); err != nil || creator != "" {
		t.Fatalf("a task saved without a creator names none: %q %v", creator, err)
	}
	if _, err := store.TaskCreator(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("TaskCreator(missing) = %v, want not found", err)
	}
}

func TestARunKeepsTheApproverItStartedWith(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	run := Execution{ID: "run-2", WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: "running", Title: "Run", ApprovedBy: "ada"}
	if err := store.SaveExecution(ctx, run); err != nil {
		t.Fatal(err)
	}
	// Saving the run as it moves on, from a view that does not carry the
	// approver, never changes who approved it.
	run.State, run.ApprovedBy = "completed", ""
	if err := store.SaveExecution(ctx, run); err != nil {
		t.Fatal(err)
	}
	if saved, err := store.Execution(ctx, "run-2"); err != nil || saved.ApprovedBy != "ada" || saved.State != "completed" {
		t.Fatalf("the run names who approved it: %+v %v", saved, err)
	}
}

// Rows written before principals were recorded name no one after the
// migration, rather than someone who never acted.
func TestPrincipalMigrationLeavesExistingRowsEmpty(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	before := `
ALTER TABLE tasks DROP COLUMN created_by;
ALTER TABLE executions DROP COLUMN approved_by;
DELETE FROM schema_migrations WHERE version = '034_principals.sql';`
	if _, err := store.DB().ExecContext(ctx, before); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if creator, err := store.TaskCreator(ctx, "task-1"); err != nil || creator != "" {
		t.Fatalf("an existing task's creator = %q, %v; want empty", creator, err)
	}
	if run, err := store.Execution(ctx, "run-1"); err != nil || run.ApprovedBy != "" {
		t.Fatalf("an existing run's approver = %q, %v; want empty", run.ApprovedBy, err)
	}
}
