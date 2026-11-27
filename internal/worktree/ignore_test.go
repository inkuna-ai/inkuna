package worktree

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// outRepository is a repository with one commit and a file in OutDir that
// nothing ignores yet.
func outRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(filepath.Join(repository, ".inkuna", "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "-q")
	configure(t, repository)
	commitFile(t, repository, "README.md", "readme")
	if err := os.WriteFile(filepath.Join(repository, ".inkuna", "out", "plan.md"), []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestInkunasCommitsLeaveOutDirOutEvenWhenItIsNotIgnored(t *testing.T) {
	repository := outRepository(t)
	ctx := context.Background()
	if _, ok, err := CommitAll(ctx, repository, "inkuna(plan): nothing but the plan"); err != nil || ok {
		t.Fatalf("a plan alone is nothing to commit: ok=%v err=%v", ok, err)
	}
	if changes, err := Changes(ctx, repository); err != nil || changes != "" {
		t.Fatalf("Changes leaves out OutDir: %q %v", changes, err)
	}
	if status, err := Status(ctx, repository); err != nil || !strings.Contains(status, ".inkuna/") {
		t.Fatalf("Status still shows what removal would delete: %q %v", status, err)
	}
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit, ok, err := CommitAll(ctx, repository, "inkuna(implement): add main")
	if err != nil || !ok {
		t.Fatalf("expected a commit: ok=%v err=%v", ok, err)
	}
	if strings.Contains(commit.Stat, ".inkuna") || !strings.Contains(commit.Stat, "main.go") {
		t.Fatalf("the commit took OutDir: %s", commit.Stat)
	}
}

func TestInkunasCommitsWorkWhenOutDirIsIgnored(t *testing.T) {
	repository := outRepository(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(repository, ".gitignore"), []byte(".inkuna/out/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", ".gitignore")
	runGit(t, repository, "commit", "-qm", "ignore .inkuna/out")
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit, ok, err := CommitAll(ctx, repository, "inkuna(implement): add main")
	if err != nil || !ok {
		t.Fatalf("an ignored OutDir must not stop the commit: ok=%v err=%v", ok, err)
	}
	if strings.Contains(commit.Stat, ".inkuna") || !strings.Contains(commit.Stat, "main.go") {
		t.Fatalf("the commit took OutDir: %s", commit.Stat)
	}
	// A file there that a commit made before Inkuna tracks keeps what HEAD
	// has; Inkuna's commit leaves its change out.
	runGit(t, repository, "add", "-f", ".inkuna/out/plan.md")
	runGit(t, repository, "commit", "-qm", "track the plan")
	if err := os.WriteFile(filepath.Join(repository, ".inkuna", "out", "plan.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := CommitAll(ctx, repository, "inkuna(plan): nothing to commit"); err != nil || ok {
		t.Fatalf("a change in OutDir alone is nothing to commit: ok=%v err=%v", ok, err)
	}
}

func TestIgnoreOutAddsTheLineOnceAndKeepsTheFile(t *testing.T) {
	repository := outRepository(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(repository, ".gitignore"), []byte("*.log"), 0o640); err != nil {
		t.Fatal(err)
	}
	if ignored, err := OutIgnored(ctx, repository); err != nil || ignored {
		t.Fatalf("nothing ignores OutDir yet: %v %v", ignored, err)
	}
	// git names the repository by its real path; on macOS the temporary
	// directory is behind a symbolic link.
	real, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	path, added, err := IgnoreOut(ctx, filepath.Join(repository, ".inkuna"))
	if err != nil || !added || path != filepath.Join(real, ".gitignore") {
		t.Fatalf("IgnoreOut from a subdirectory: %s %v %v", path, added, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(data); !strings.HasPrefix(text, "*.log\n") || !strings.HasSuffix(text, "\n.inkuna/out/\n") {
		t.Fatalf("the line follows what was there: %q", text)
	}
	// Windows files have no Unix permissions to keep.
	if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o640) {
		t.Fatalf("the file keeps its mode: %v %v", info.Mode(), err)
	}
	if ignored, err := OutIgnored(ctx, repository); err != nil || !ignored {
		t.Fatalf("OutDir is ignored now: %v %v", ignored, err)
	}
	if _, added, err := IgnoreOut(ctx, repository); err != nil || added {
		t.Fatalf("a second run changes nothing: %v %v", added, err)
	}
	entries, err := os.ReadDir(repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gitignore-") || strings.HasPrefix(entry.Name(), "..gitignore") {
			t.Fatalf("a temporary file was left: %s", entry.Name())
		}
	}
}

func TestIgnoreOutDoesNotCountAnIgnoreOutsideTheRepository(t *testing.T) {
	repository := outRepository(t)
	ctx := context.Background()
	exclude := filepath.Join(repository, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte(".inkuna/out/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ignored, err := OutIgnored(ctx, repository); err != nil || !ignored {
		t.Fatalf("info/exclude ignores it here: %v %v", ignored, err)
	}
	// A clone does not get .git/info/exclude, so the repository's own
	// .gitignore still needs the line.
	if _, added, err := IgnoreOut(ctx, repository); err != nil || !added {
		t.Fatalf("IgnoreOut adds it to .gitignore: %v %v", added, err)
	}
	if data, err := os.ReadFile(filepath.Join(repository, ".gitignore")); err != nil || string(data) != "# Files Inkuna and its agents leave for later steps (inkunactl init).\n.inkuna/out/\n" {
		t.Fatalf("a new .gitignore: %q %v", data, err)
	}
}
