package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// fixtureTask is a task with one run, which tasksFixture saves.
type fixtureTask struct{ id, state, worktree, branch string }

// tasksFixture is a database with a finished task, whose run left a worktree
// on a branch with a commit of its own, a task whose run is running, and
// any more tasks given.
func tasksFixture(t *testing.T, more ...fixtureTask) (repository, worktree string) {
	t.Helper()
	isolate(t)
	parent := t.TempDir()
	repository = filepath.Join(parent, "bakery")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "init", "-q", "-b", "main")
	git(t, repository, "commit", "-q", "--allow-empty", "-m", "initial")
	// A clone records origin's default branch, which is the base a branch
	// is deleted against; without one, no branch can be deleted.
	git(t, repository, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	worktree = filepath.Join(parent, "bakery-worktrees", "fix")
	git(t, repository, "worktree", "add", "-q", "-b", "bug/42-fix", worktree, "main")
	git(t, worktree, "commit", "-q", "--allow-empty", "-m", "fix")

	store, err := storage.Open(filepath.Join(os.Getenv("INKUNA_DATA_DIR"), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err := store.UpsertProject(ctx, storage.Project{ID: "project", Name: "Bakery", Repository: repository, DefaultRuntime: domain.RuntimeClaude}); err != nil {
		t.Fatal(err)
	}
	for index, task := range append([]fixtureTask{
		{"task-done", "completed", worktree, "bug/42-fix"},
		{"task-busy", "running", "", ""},
	}, more...) {
		spec := domain.TaskSpec{Version: 1, ID: task.id, Kind: domain.TaskBugfix, Objective: "Objective of " + task.id, Repository: repository, Budget: domain.Budget{WallTimeSeconds: 60}}
		workflow := domain.WorkflowSpec{Version: 1, ID: task.id + "-plan", TaskID: task.id, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(),
			Steps: []domain.WorkflowStep{{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
		started := time.Date(2026, 11, 1, 9, index, 0, 0, time.UTC)
		for _, err := range []error{
			store.SaveTask(ctx, "project", "planned", spec),
			store.SaveWorkflow(ctx, "approved", workflow),
			store.SaveExecution(ctx, storage.Execution{ID: task.id + "-run", WorkflowID: workflow.ID, TaskID: task.id, ProjectID: "project", State: task.state, Title: "Run of " + task.id,
				Worktree: task.worktree, Branch: task.branch, Cleanup: domain.CleanupManual, StartedAt: started, FinishedAt: started.Add(time.Minute)}),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return repository, worktree
}

func TestTasksListShowsEachTaskAndOnlyFinishedOnesWhenAsked(t *testing.T) {
	tasksFixture(t)
	var output bytes.Buffer
	if err := run([]string{"tasks", "list"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "task-busy") || !strings.Contains(lines[1], "running") || !strings.HasPrefix(lines[2], "task-done") || !strings.Contains(lines[2], "Run of task-done") {
		t.Fatalf("list:\n%s", output.String())
	}
	output.Reset()
	if err := run([]string{"tasks", "list", "--finished"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "task-busy") || !strings.Contains(output.String(), "task-done") || !strings.Contains(output.String(), "completed") {
		t.Fatalf("list --finished:\n%s", output.String())
	}
}

func TestTasksDeleteAsksFirstAndAgainForEachBranch(t *testing.T) {
	repository, worktree := tasksFixture(t)
	tip, full := git(t, repository, "rev-parse", "--short", "bug/42-fix"), git(t, repository, "rev-parse", "bug/42-fix")
	var output bytes.Buffer
	if err := run([]string{"tasks", "delete", "task-done", "task-busy"}, strings.NewReader("y\n"), &output); err == nil || !strings.Contains(output.String(), "run task-busy-run is running") {
		t.Fatalf("a running task refuses the delete: %v\n%s", err, output.String())
	}
	output.Reset()
	if err := run([]string{"tasks", "delete", "--remove-worktrees", "task-done"}, strings.NewReader("n\n"), &output); err != nil || !strings.Contains(output.String(), "Nothing was deleted.") {
		t.Fatalf("answering no deletes nothing: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "worktree "+worktree+" will be removed") || !strings.Contains(output.String(), "branch bug/42-fix at "+tip+" stays (--delete-branches") {
		t.Fatalf("the preview says what would go:\n%s", output.String())
	}
	output.Reset()
	if err := run([]string{"tasks", "delete", "task-done", "--remove-worktrees", "--delete-branches"}, strings.NewReader("y\ny\n"), &output); err != nil {
		t.Fatalf("%v\n%s", err, output.String())
	}
	for _, want := range []string{
		"Delete branch bug/42-fix at " + tip + "? 1 commit is only on it and would be lost.",
		"Deleted task task-done:",
		"removed worktree " + worktree,
		"deleted branch bug/42-fix; until git collects its commits, git branch bug/42-fix " + full + " brings it back",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, output.String())
		}
	}
	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the worktree is gone: %v", err)
	}
	if branches := git(t, repository, "branch", "--list", "bug/42-fix"); branches != "" {
		t.Fatalf("the branch is gone: %q", branches)
	}
	output.Reset()
	if err := run([]string{"tasks", "list", "--finished"}, nil, &output); err != nil || !strings.Contains(output.String(), "No finished tasks.") {
		t.Fatalf("the task is gone: %v\n%s", err, output.String())
	}
}

func TestTasksDeleteAsksOnceForABranchTwoTasksShare(t *testing.T) {
	repository, _ := tasksFixture(t, fixtureTask{"task-again", "completed", "", "bug/42-fix"})
	var output bytes.Buffer
	if err := run([]string{"tasks", "delete", "task-done", "task-again", "--remove-worktrees", "--delete-branches"}, strings.NewReader("y\ny\n"), &output); err != nil {
		t.Fatalf("%v\n%s", err, output.String())
	}
	if asked := strings.Count(output.String(), "Delete branch bug/42-fix at"); asked != 1 {
		t.Fatalf("asked %d times about the shared branch:\n%s", asked, output.String())
	}
	if branches := git(t, repository, "branch", "--list", "bug/42-fix"); branches != "" {
		t.Fatalf("the one answer deletes it: %q\n%s", branches, output.String())
	}
}
