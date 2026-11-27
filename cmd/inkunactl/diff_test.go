package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestDiffWritesARunsPageAndOpensItWhenAsked(t *testing.T) {
	data := t.TempDir()
	t.Setenv("INKUNA_DATA_DIR", data)
	t.Setenv("INKUNA_CONFIG", filepath.Join(data, "inkuna.yaml"))
	repository := t.TempDir()
	gitIn(t, repository, "init", "-q")
	if err := os.WriteFile(filepath.Join(repository, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repository, "add", "-A")
	gitIn(t, repository, "commit", "-q", "-m", "base")
	base := gitIn(t, repository, "rev-parse", "HEAD")
	gitIn(t, repository, "switch", "-q", "-c", "feature/x")
	if err := os.WriteFile(filepath.Join(repository, "a.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repository, "commit", "-q", "-am", "inkuna(implement): add b")
	step := gitIn(t, repository, "rev-parse", "HEAD")

	store, err := storage.Open(filepath.Join(data, "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task := domain.TaskSpec{Version: 1, ID: "task-1", Kind: domain.TaskImplementation, Objective: "Add b", Repository: repository, Budget: domain.Budget{WallTimeSeconds: 60}}
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow-1", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Agent: "implementer", Permission: domain.PermissionWorktree, AllowedPaths: []string{"."}, Budget: domain.Budget{WallTimeSeconds: 10}}}}
	for _, err := range []error{
		store.UpsertProject(ctx, storage.Project{ID: "project", Name: "Project", Repository: repository, DefaultRuntime: domain.RuntimeClaude}),
		store.SaveTask(ctx, "project", "approved", task),
		store.SaveWorkflow(ctx, "approved", workflow),
		store.SaveExecution(ctx, storage.Execution{ID: "run-1", WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: "project", State: "completed", Title: "Add b", Branch: "feature/x", BaseCommit: base}),
		store.SaveCommit(ctx, storage.CommitRecord{ExecutionID: "run-1", StepID: "implement", Agent: "implementer", Hash: step, Subject: "inkuna(implement): add b"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = store.Close()

	var opened []string
	original := openPage
	openPage = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	t.Cleanup(func() { openPage = original })

	var output bytes.Buffer
	if err := run([]string{"diff", "run-1"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(data, "reports", "run-1.html")
	if strings.TrimSpace(output.String()) != want || len(opened) != 0 {
		t.Fatalf("printed %q, opened %v", output.String(), opened)
	}
	page, err := os.ReadFile(want)
	if err != nil || !strings.Contains(string(page), "<strong>1 file changed</strong>") || !strings.Contains(string(page), "Made by inkunactl diff at") {
		t.Fatalf("page %d bytes, %v", len(page), err)
	}

	out := filepath.Join(t.TempDir(), "step.html")
	output.Reset()
	if err := run([]string{"diff", "--step", "implement", "--out", out, "--open", "--split", "run-1"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if page, err := os.ReadFile(out); err != nil || !strings.Contains(string(page), "Commit "+step[:7]) || !strings.Contains(string(page), `class="layout-split"`) {
		t.Fatalf("step page %v", err)
	}
	if len(opened) != 1 || opened[0] != out {
		t.Fatalf("opened %v", opened)
	}

	for name, args := range map[string][]string{
		"no run":         {"diff"},
		"both scopes":    {"diff", "--commit", step, "--step", "implement", "run-1"},
		"unknown run":    {"diff", "run-2"},
		"unknown step":   {"diff", "--step", "review", "run-1"},
		"off the branch": {"diff", "--commit", base, "run-1"},
	} {
		if err := run(args, nil, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
