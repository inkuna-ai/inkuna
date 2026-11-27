package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestDocReadsByTaskAndByDoc(t *testing.T) {
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
	for _, id := range []string{"a", "b"} {
		task := domain.TaskSpec{Version: 1, ID: "task-" + id, Kind: domain.TaskImplementation, Objective: "Test", Repository: project.Repository, Budget: domain.Budget{WallTimeSeconds: 60}}
		if err := store.SaveTask(ctx, project.ID, "approved", task); err != nil {
			t.Fatal(err)
		}
		workflow := domain.WorkflowSpec{Version: 1, ID: "workflow-" + id, TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{{ID: "worker", Title: "Work", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
		if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveExecution(ctx, Execution{ID: "run-" + id, WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: project.ID, State: "running", Title: "Run " + id}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	read := func(task, doc string, minute int) domain.DocRead {
		return domain.DocRead{TaskID: "task-" + task, StepID: "worker", RunID: "run-" + task, ProjectID: project.ID, Agent: "implementer", Runtime: domain.RuntimeClaude, DocsDir: "docs", DocPath: doc, Tool: "Read", OpenedAt: start.Add(time.Duration(minute) * time.Minute)}
	}
	// The same path under a docs root that has since moved is another doc.
	moved := read("b", "guide.md", 4)
	moved.DocsDir = "handbook"
	recorded := []domain.DocRead{read("a", "guide.md", 0), read("a", "notes/", 1), read("b", "guide.md", 2), read("a", "guide.md", 3), moved}
	for _, entry := range recorded {
		if err := store.RecordDocRead(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	byTask, err := store.DocReadsForTask(ctx, "task-a")
	if err != nil || !reflect.DeepEqual(byTask, []domain.DocRead{recorded[0], recorded[1], recorded[3]}) {
		t.Fatalf("task-a reads = %#v, %v", byTask, err)
	}
	byDoc, err := store.DocReadsForDoc(ctx, project.ID, "docs", "guide.md", 0)
	if err != nil || !reflect.DeepEqual(byDoc, []domain.DocRead{recorded[3], recorded[2], recorded[0]}) {
		t.Fatalf("guide.md reads = %#v, %v", byDoc, err)
	}
	if handbook, err := store.DocReadsForDoc(ctx, project.ID, "handbook", "guide.md", 0); err != nil || !reflect.DeepEqual(handbook, []domain.DocRead{moved}) {
		t.Fatalf("handbook/guide.md reads = %#v, %v", handbook, err)
	}
	if limited, err := store.DocReadsForDoc(ctx, project.ID, "docs", "guide.md", 1); err != nil || len(limited) != 1 || limited[0] != recorded[3] {
		t.Fatalf("limited reads = %#v, %v", limited, err)
	}
	if none, err := store.DocReadsForDoc(ctx, "other", "docs", "guide.md", 0); err != nil || len(none) != 0 {
		t.Fatalf("another project's reads = %#v, %v", none, err)
	}
	if err := store.RecordDocRead(ctx, domain.DocRead{TaskID: "task-a", RunID: "run-a", ProjectID: project.ID, DocsDir: "docs"}); err == nil {
		t.Fatal("a read without a doc path must be refused")
	}
	if err := store.RecordDocRead(ctx, domain.DocRead{TaskID: "task-a", RunID: "run-a", ProjectID: project.ID, DocPath: "guide.md"}); err == nil {
		t.Fatal("a read without its docs directory must be refused")
	}
	if err := store.RecordDocRead(ctx, read("missing", "guide.md", 5)); err == nil {
		t.Fatal("a read of a run that does not exist must be refused")
	}
	var indexes int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('doc_reads_task','doc_reads_doc')`).Scan(&indexes); err != nil || indexes != 2 {
		t.Fatalf("doc_reads indexes = %d, %v", indexes, err)
	}
}
