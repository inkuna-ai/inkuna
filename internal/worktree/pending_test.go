package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPendingChangesListsAndDiscardThrowsAwayWhatACommitWouldTake(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "-q")
	configure(t, repository)
	if err := os.WriteFile(filepath.Join(repository, ".gitignore"), []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repository, "README.md", "readme")
	commitFile(t, repository, "OLD.md", "old")
	commitFile(t, repository, "WAS.md", "was")
	runGit(t, repository, "add", ".gitignore")
	runGit(t, repository, "commit", "-qm", "ignore build")
	write := func(name, content string) {
		t.Helper()
		full := filepath.Join(repository, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// What a step cut short might leave: an edit, a staged new file, a
	// rename, and untracked files in a new directory.
	write("README.md", "half done")
	write("staged.go", "package main\n")
	runGit(t, repository, "add", "staged.go")
	runGit(t, repository, "mv", "OLD.md", "NEW.md")
	// A rename git sees only in the worktree, against an intent-to-add path.
	if err := os.Rename(filepath.Join(repository, "WAS.md"), filepath.Join(repository, "MOVED.md")); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "-N", "MOVED.md")
	write("draft/notes.md", "notes")
	// Neither of these is a commit's to take, so neither is listed or deleted.
	write(OutDir+"/report.md", "report")
	write("build/out.bin", "binary")

	ctx := context.Background()
	changes, err := PendingChanges(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.Status+" "+change.Path)
	}
	slices.Sort(paths)
	want := []string{" M README.md", " R MOVED.md", "?? draft/notes.md", "A  staged.go", "R  NEW.md"}
	if !slices.Equal(paths, want) {
		t.Fatalf("pending changes %q, want %q", paths, want)
	}

	if err := Discard(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if left, err := PendingChanges(ctx, repository); err != nil || len(left) != 0 {
		t.Fatalf("nothing a commit would take is left: %+v %v", left, err)
	}
	if data, err := os.ReadFile(filepath.Join(repository, "README.md")); err != nil || string(data) != "readme" {
		t.Fatalf("README.md is back at its last commit: %q %v", data, err)
	}
	for _, name := range []string{"OLD.md", "WAS.md", OutDir + "/report.md", "build/out.bin"} {
		if _, err := os.Stat(filepath.Join(repository, name)); err != nil {
			t.Errorf("%s should be there: %v", name, err)
		}
	}
	for _, name := range []string{"NEW.md", "MOVED.md", "staged.go", "draft"} {
		if _, err := os.Stat(filepath.Join(repository, name)); !os.IsNotExist(err) {
			t.Errorf("%s should be gone: %v", name, err)
		}
	}
}

func TestDiscardRefusesToOverwriteAnIgnoredFileStagedForDeletion(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "-q")
	configure(t, repository)
	commitFile(t, repository, "app.log", "committed")
	commitFile(t, repository, ".gitignore", "*.log\n")
	// Untracked and ignored now, so nothing lists the file's own content.
	runGit(t, repository, "rm", "-q", "--cached", "app.log")
	if err := os.WriteFile(filepath.Join(repository, "app.log"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, err := Status(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	err = Discard(ctx, repository)
	var inTheWay *IgnoredInTheWayError
	if !errors.As(err, &inTheWay) || !slices.Equal(inTheWay.Paths, []string{"app.log"}) {
		t.Fatalf("a discard that would overwrite app.log is refused: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(repository, "app.log")); err != nil || string(data) != "local" {
		t.Fatalf("app.log keeps its content: %q %v", data, err)
	}
	if after, err := Status(ctx, repository); err != nil || after != before {
		t.Fatalf("a refused discard changes nothing: %q, was %q, %v", after, before, err)
	}
}

// checkoutState is what a discard and its PutBack must leave as it was: the
// index, every entry with its stage and blob, and the status.
func checkoutState(t *testing.T, repository string) string {
	t.Helper()
	ctx := context.Background()
	index, err := git(ctx, repository, "ls-files", "-s")
	if err != nil {
		t.Fatal(err)
	}
	status, err := Status(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	return index + "\n--\n" + status
}

// discardAndPutBack discards a checkout's changes after a snapshot, as a
// resume does, then puts them back, as a refused one does, and checks the
// checkout is as it was.
func discardAndPutBack(t *testing.T, repository string) {
	t.Helper()
	ctx := context.Background()
	before := checkoutState(t, repository)
	snapshot, err := TakeSnapshot(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if kept := checkoutState(t, repository); kept != before {
		t.Fatalf("a snapshot changes nothing in the checkout:\n%s\nwas\n%s", kept, before)
	}
	if err := Discard(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if left, err := Changes(ctx, repository); err != nil || left != "" {
		t.Fatalf("the discard left %q: %v", left, err)
	}
	if err := PutBack(ctx, repository, snapshot); err != nil {
		t.Fatal(err)
	}
	if after := checkoutState(t, repository); after != before {
		t.Fatalf("after putting back:\n%s\nwant\n%s", after, before)
	}
}

func TestPutBackRestoresWhatADiscardThrewAwayFilesAndIndex(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "-q")
	configure(t, repository)
	commitFile(t, repository, ".gitignore", OutDir+"/\n")
	commitFile(t, repository, "README.md", "readme")
	commitFile(t, repository, "PART.md", "v0")
	commitFile(t, repository, "STAGED.md", "staged v0")
	commitFile(t, repository, "GONE.md", "gone")
	commitFile(t, repository, "UNTRACK.md", "untrack")
	write := func(name, content string) {
		t.Helper()
		full := filepath.Join(repository, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Staged only: a new file, and an edit, each staged as it stands.
	write("staged.go", "package main\n")
	write("STAGED.md", "staged v1")
	runGit(t, repository, "add", "staged.go", "STAGED.md")
	// Partly staged: one version staged, a later one in the file.
	write("PART.md", "v1")
	runGit(t, repository, "add", "PART.md")
	write("PART.md", "v2")
	// Staged deletions: one gone from the checkout too, one kept untracked.
	runGit(t, repository, "rm", "-q", "GONE.md")
	runGit(t, repository, "rm", "-q", "--cached", "UNTRACK.md")
	// Unstaged and untracked changes, and OutDir, which is no discard's.
	write("README.md", "half done")
	write("draft/notes.md", "notes")
	write(OutDir+"/report.md", "report")

	discardAndPutBack(t, repository)
	ctx := context.Background()
	for name, want := range map[string]string{"PART.md": "v2", ":PART.md": "v1", ":STAGED.md": "staged v1", "README.md": "half done", "UNTRACK.md": "untrack", "draft/notes.md": "notes", OutDir + "/report.md": "report"} {
		var data []byte
		var err error
		if strings.HasPrefix(name, ":") {
			var shown string
			shown, err = git(ctx, repository, "show", name)
			data = []byte(shown)
		} else {
			data, err = os.ReadFile(filepath.Join(repository, name))
		}
		if err != nil || string(data) != want {
			t.Errorf("%s is %q, want %q: %v", name, data, want, err)
		}
	}
}

func TestPutBackRestoresAnUnmergedPathsStages(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "-q", "-b", "main")
	configure(t, repository)
	commitFile(t, repository, "f.txt", "base\n")
	runGit(t, repository, "switch", "-q", "-c", "other")
	commitFile(t, repository, "f.txt", "other\n")
	runGit(t, repository, "switch", "-q", "main")
	commitFile(t, repository, "f.txt", "main\n")
	command := exec.Command("git", "merge", "-q", "other")
	command.Dir = repository
	if err := command.Run(); err == nil {
		t.Fatal("the merge should conflict")
	}
	// A discard resolves the path to the last commit, as git restore does.
	discardAndPutBack(t, repository)
	if unmerged, err := git(context.Background(), repository, "ls-files", "-u"); err != nil || strings.Count(unmerged, "f.txt") != 3 {
		t.Fatalf("f.txt has its three stages again: %q %v", unmerged, err)
	}
}
