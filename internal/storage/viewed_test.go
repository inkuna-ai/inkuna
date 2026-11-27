package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestViewedMarksAreKeptPerBlob(t *testing.T) {
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
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{{ID: "worker", Title: "Work", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
	if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"run", "other"} {
		if err := store.SaveExecution(ctx, Execution{ID: id, WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: project.ID, State: "completed", Title: "Run"}); err != nil {
			t.Fatal(err)
		}
	}

	first, second := ViewedFile{Path: "a.go", Blob: "1111"}, ViewedFile{Path: "a.go", Blob: "2222"}
	for _, file := range []ViewedFile{first, second, first} {
		if err := store.SetViewed(ctx, "run", file, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetViewed(ctx, "other", ViewedFile{Path: "b.go", Blob: "3333"}, true); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ViewedFiles(ctx, "run"); err != nil || !reflect.DeepEqual(got, []ViewedFile{first, second}) {
		t.Fatalf("marking twice keeps one mark per blob: %#v, %v", got, err)
	}
	if err := store.SetViewed(ctx, "run", second, false); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ViewedFiles(ctx, "run"); err != nil || !reflect.DeepEqual(got, []ViewedFile{first}) {
		t.Fatalf("clearing a mark leaves the file's other blobs: %#v, %v", got, err)
	}
	if err := store.SetViewed(ctx, "missing", first, true); err == nil {
		t.Fatal("a mark on a run that does not exist must be refused")
	}
	if err := store.SetViewed(ctx, "run", ViewedFile{Path: "a.go"}, true); err == nil {
		t.Fatal("a mark needs a blob")
	}
}
