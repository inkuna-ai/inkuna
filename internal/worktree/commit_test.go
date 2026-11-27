package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitAllAndLastCommit(t *testing.T) {
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
	ctx := context.Background()
	base, err := Head(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := CommitAll(ctx, repository, "inkuna(worker): nothing"); err != nil || ok {
		t.Fatalf("clean tree must not commit: ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit, ok, err := CommitAll(ctx, repository, "inkuna(worker): add main")
	if err != nil || !ok {
		t.Fatalf("expected commit: ok=%v err=%v", ok, err)
	}
	if commit.Subject != "inkuna(worker): add main" || !strings.Contains(commit.Stat, "main.go") || !strings.Contains(commit.Diff, "+package main") {
		t.Fatalf("unexpected commit %+v", commit)
	}
	if !strings.Contains(commit.Author, "Inkuna") {
		t.Fatalf("commit author should be Inkuna, got %q", commit.Author)
	}
	last, err := LastCommit(ctx, repository, false)
	if err != nil || last.Hash != commit.Hash || last.Diff != "" {
		t.Fatalf("unexpected last commit %+v %v", last, err)
	}
	diff, truncated, err := DiffSince(ctx, repository, base)
	if err != nil || truncated || !strings.Contains(diff, "main.go") {
		t.Fatalf("unexpected diff %q %v %v", diff, truncated, err)
	}
}
