package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCreateIsolatedWorktree(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "-q")
	runGit(t, repository, "config", "user.email", "inkuna@example.invalid")
	runGit(t, repository, "config", "user.name", "Inkuna Test")
	runGit(t, repository, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "README.md")
	runGit(t, repository, "commit", "-qm", "initial")
	manager := Manager{Repository: repository, Root: filepath.Join(t.TempDir(), "worktrees")}
	created, err := manager.Create(context.Background(), "task-123", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(created.Path, "README.md")); err != nil {
		t.Fatal(err)
	}
	if created.Branch != "inkuna/task-123" {
		t.Fatalf("unexpected branch %q", created.Branch)
	}
	if _, err := manager.Create(context.Background(), "../escape", "HEAD"); err == nil {
		t.Fatal("unsafe task id accepted")
	}
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
