package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestMigrationsAndConcurrentEvents(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	project := Project{ID: "project", Name: "Project", Repository: t.TempDir(), DefaultRuntime: domain.RuntimeCodex}
	if err := store.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	loadedProject, err := store.Project(ctx, project.ID)
	if err != nil || loadedProject.Repository != project.Repository {
		t.Fatalf("load project: project=%#v err=%v", loadedProject, err)
	}
	task := domain.TaskSpec{Version: 1, ID: "task", Kind: domain.TaskImplementation, Objective: "Test", Repository: project.Repository, Budget: domain.Budget{WallTimeSeconds: 60}}
	if err := store.SaveTask(ctx, project.ID, "approved", task); err != nil {
		t.Fatal(err)
	}
	loadedTask, err := store.Task(ctx, task.ID)
	if err != nil || loadedTask.Objective != task.Objective {
		t.Fatalf("load task: task=%#v err=%v", loadedTask, err)
	}
	projectForTask, err := store.ProjectForTask(ctx, task.ID)
	if err != nil || projectForTask.ID != project.ID {
		t.Fatalf("load task project: project=%#v err=%v", projectForTask, err)
	}
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeCodex, MaxConcurrency: 2, CreatedAt: time.Now().UTC(), Steps: []domain.WorkflowStep{{ID: "step", Title: "Step", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 60}}}}
	if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
		t.Fatal(err)
	}
	loadedWorkflow, err := store.Workflow(ctx, workflow.ID)
	if err != nil || loadedWorkflow.TaskID != task.ID {
		t.Fatalf("load workflow: workflow=%#v err=%v", loadedWorkflow, err)
	}
	if err := store.ApproveWorkflow(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	var approvedAt sql.NullString
	if err := store.DB().QueryRowContext(ctx, `SELECT approved_at FROM workflows WHERE id=?`, workflow.ID).Scan(&approvedAt); err != nil || !approvedAt.Valid || approvedAt.String == "" {
		t.Fatalf("approve workflow timestamp: approved_at=%#v err=%v", approvedAt, err)
	}
	if err := store.StartRun(ctx, "run", "", workflow.ID, "step", 1, domain.RuntimeCodex, ""); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 1; index <= 20; index++ {
		group.Add(1)
		go func(sequence int) {
			defer group.Done()
			event := domain.RunEvent{Version: 1, RunID: "run", Sequence: int64(sequence), Timestamp: time.Now().UTC(), Kind: domain.EventMessage, Summary: fmt.Sprintf("event %d", sequence)}
			if err := store.AppendEvent(ctx, event); err != nil {
				t.Errorf("append: %v", err)
			}
		}(index)
	}
	group.Wait()
	count, err := store.EventCount(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if count != 20 {
		t.Fatalf("got %d events", count)
	}
	if err := store.FinishRun(ctx, "run", "completed", domain.HandoffResult{Version: 1, Status: "completed", Summary: "done"}); err != nil {
		t.Fatal(err)
	}
	var runState string
	if err := store.DB().QueryRowContext(ctx, `SELECT state FROM runs WHERE id='run'`).Scan(&runState); err != nil || runState != "completed" {
		t.Fatalf("finish run: state=%q err=%v", runState, err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migration is not idempotent: %v", err)
	}
	copilotProject := Project{ID: "copilot-project", Name: "Copilot Project", Repository: filepath.Join(t.TempDir(), "copilot"), DefaultRuntime: domain.RuntimeCopilot}
	if err := store.UpsertProject(ctx, copilotProject); err != nil {
		t.Fatalf("Copilot runtime was not accepted by the migrated schema: %v", err)
	}
}

func TestCopilotMigrationPreservesExistingProjectReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `
PRAGMA foreign_keys=ON;
CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL);
INSERT INTO schema_migrations VALUES ('001_initial.sql', '2026-01-01T00:00:00Z');
CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, repository TEXT NOT NULL UNIQUE, default_runtime TEXT NOT NULL CHECK (default_runtime IN ('codex', 'claude')), settings_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL);
CREATE TABLE tasks (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), state TEXT NOT NULL, kind TEXT NOT NULL, spec_json TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE runs (id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL, step_id TEXT NOT NULL, runtime TEXT NOT NULL, state TEXT NOT NULL, worktree TEXT, started_at TEXT NOT NULL, finished_at TEXT, handoff_json TEXT);
CREATE TABLE workflows (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), state TEXT NOT NULL, spec_json TEXT NOT NULL, created_at TEXT NOT NULL, approved_at TEXT);
CREATE TABLE approvals (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), kind TEXT NOT NULL, payload_json TEXT NOT NULL, state TEXT NOT NULL, created_at TEXT NOT NULL, resolved_at TEXT);
-- The rest of 001_initial.sql, as it created them, so later migrations
-- find every table 001 made.
CREATE TABLE steps ( workflow_id TEXT NOT NULL REFERENCES workflows(id), id TEXT NOT NULL, state TEXT NOT NULL, spec_json TEXT NOT NULL, PRIMARY KEY (workflow_id, id) );
CREATE TABLE events ( run_id TEXT NOT NULL REFERENCES runs(id), sequence INTEGER NOT NULL, event_json TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY (run_id, sequence) );
CREATE TABLE artifacts ( digest TEXT PRIMARY KEY, media_type TEXT NOT NULL, size INTEGER NOT NULL, name TEXT, created_at TEXT NOT NULL );
CREATE TABLE skills ( id TEXT PRIMARY KEY, metadata_json TEXT NOT NULL, source_path TEXT NOT NULL, updated_at TEXT NOT NULL );
CREATE TABLE github_links ( task_id TEXT PRIMARY KEY REFERENCES tasks(id), repository TEXT NOT NULL, issue_number INTEGER NOT NULL, issue_url TEXT NOT NULL, marker TEXT NOT NULL UNIQUE, last_status TEXT, created_at TEXT NOT NULL );
INSERT INTO projects VALUES ('project', 'Legacy', '/tmp/legacy', 'codex', '{}', '2026-01-01T00:00:00Z');
INSERT INTO tasks VALUES ('task', 'project', 'approved', 'implementation', '{}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`
	if _, err := database.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.UpsertProject(context.Background(), Project{ID: "copilot", Name: "Copilot", Repository: "/tmp/copilot", DefaultRuntime: domain.RuntimeCopilot}); err != nil {
		t.Fatal(err)
	}
	// Runtime names are declared in inkuna.yaml; the database accepts any
	// non-empty one and Go checks it against the declarations.
	if err := store.UpsertProject(context.Background(), Project{ID: "gemini", Name: "Gemini", Repository: "/tmp/gemini", DefaultRuntime: "gemini"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProject(context.Background(), Project{ID: "blank", Name: "Blank", Repository: "/tmp/blank"}); err == nil {
		t.Fatal("a project without a default runtime must be refused")
	}
	var projectID string
	if err := store.DB().QueryRow(`SELECT project_id FROM tasks WHERE id='task'`).Scan(&projectID); err != nil || projectID != "project" {
		t.Fatalf("task reference was not preserved: project=%q err=%v", projectID, err)
	}
	var violations int
	if err := store.DB().QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("foreign key violations after the rebuilds: %d, %v", violations, err)
	}
}

func TestTaskAndWorkflowTranslateMissingRows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.Task(context.Background(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing task error = %v", err)
	}
	if _, err := store.Workflow(context.Background(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing workflow error = %v", err)
	}
}

func TestRemovedProjectsLeaveTheConnectedListAndKeepTheirRow(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	kept := Project{ID: "kept", Name: "Kept", Repository: "/src/kept", DefaultRuntime: domain.RuntimeCodex, Listed: true}
	removed := Project{ID: "gone", Name: "Gone", Repository: "/src/gone", DefaultRuntime: domain.RuntimeCodex, Listed: true, RemovedAt: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)}
	for _, project := range []Project{kept, removed} {
		if err := store.UpsertProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	connected, err := store.Projects(ctx)
	if err != nil || len(connected) != 1 || connected[0].ID != "kept" || !connected[0].Listed || connected[0].Removed() {
		t.Fatalf("Projects lists only the connected project: %#v, %v", connected, err)
	}
	all, err := store.AllProjects(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("AllProjects lists removed projects too: %#v, %v", all, err)
	}
	read, err := store.Project(ctx, "gone")
	if err != nil || !read.Removed() || !read.RemovedAt.Equal(removed.RemovedAt) {
		t.Fatalf("Project reads a removed project with when it went: %#v, %v", read, err)
	}
	// Connecting it again clears the mark on the same row.
	read.RemovedAt = time.Time{}
	if err := store.UpsertProject(ctx, read); err != nil {
		t.Fatal(err)
	}
	if read, err = store.Project(ctx, "gone"); err != nil || read.Removed() {
		t.Fatalf("a restored project is connected: %#v, %v", read, err)
	}
	if id, state, err := store.ProjectActiveRun(ctx, "kept"); err != nil || id != "" || state != "" {
		t.Fatalf("a project with no runs has no active run: %q %q %v", id, state, err)
	}
	if tasks, err := store.ProjectTaskIDs(ctx, "kept"); err != nil || len(tasks) != 0 {
		t.Fatalf("a project with no tasks lists none: %v, %v", tasks, err)
	}
}
