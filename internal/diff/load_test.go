package diff

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// repo is a scratch git repository for tests.
type repo struct {
	t    *testing.T
	path string
}

func newRepo(t *testing.T) repo {
	t.Helper()
	result := repo{t: t, path: t.TempDir()}
	result.git("init", "-q", "-b", "main")
	return result
}

func (repo repo) git(args ...string) string {
	repo.t.Helper()
	command := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	command.Dir = repo.path
	output, err := command.CombinedOutput()
	if err != nil {
		repo.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// commit writes the files (an empty value deletes one) and commits them,
// returning the new commit's hash.
func (repo repo) commit(message string, files map[string]string) string {
	repo.t.Helper()
	for name, content := range files {
		path := filepath.Join(repo.path, filepath.FromSlash(name))
		if content == "" {
			if err := os.Remove(path); err != nil {
				repo.t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			repo.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			repo.t.Fatal(err)
		}
	}
	repo.git("add", "-A")
	repo.git("commit", "-q", "--allow-empty", "-m", message)
	return repo.git("rev-parse", "HEAD")
}

const greet = `package greet

/*
Hello greets someone. The comment runs over
several lines, so a hunk can start inside it.
*/
func Hello(name string) string {
	return "Hello, " + name
}
`

func TestChangesListsFilesWithTheirCountsAndGeneratedFlags(t *testing.T) {
	repo := newRepo(t)
	base := repo.commit("base", map[string]string{"greet.go": greet, "go.sum": "a v1\n", "notes.txt": "keep\n"})
	repo.git("switch", "-q", "-c", "feature")
	tip := repo.commit("work", map[string]string{
		"greet.go":            strings.Replace(greet, `"Hello, "`, `"Hello there, "`, 1),
		"go.sum":              "a v1\nb v2\n",
		"ui/dist/app.js":      "bundle\n",
		"api/client.pb.go":    "package api\n",
		"docs/gen/index.html": "<p>made</p>\n",
		".gitattributes":      "api/*.pb.go linguist-generated\n",
		"notes.txt":           "",
	})
	review := Review{Version: ReviewVersion, Generated: []string{"!go.sum", "docs/gen/"}}
	change, err := Changes(context.Background(), repo.path, Range{From: base, To: tip}, review)
	if err != nil {
		t.Fatal(err)
	}
	if change.From != base || change.To != tip {
		t.Fatalf("range %s..%s", change.From, change.To)
	}
	files := make(map[string]File)
	for _, file := range change.Files {
		files[file.Path] = file
	}
	for path, want := range map[string]bool{
		"greet.go": false, "go.sum": false, "ui/dist/app.js": true, "api/client.pb.go": true, "docs/gen/index.html": true, ".gitattributes": false, "notes.txt": false,
	} {
		if file, ok := files[path]; !ok || file.Generated != want {
			t.Errorf("%s: %+v, want generated %v", path, file, want)
		}
	}
	if greet := files["greet.go"]; greet.Status != StatusModified || greet.Additions != 1 || greet.Deletions != 1 {
		t.Errorf("greet.go: %+v", greet)
	}
	if notes := files["notes.txt"]; notes.Status != StatusDeleted || notes.Deletions != 1 {
		t.Errorf("notes.txt: %+v", notes)
	}
	if change.Additions != 6 || change.Deletions != 2 {
		t.Errorf("totals +%d -%d, want +6 -2", change.Additions, change.Deletions)
	}
}

func TestPatchHighlightsWholeFilesAndMarksTheWordsThatChanged(t *testing.T) {
	repo := newRepo(t)
	base := repo.commit("base", map[string]string{"greet.go": greet + strings.Repeat("// filler\n", 10)})
	tip := repo.commit("work", map[string]string{"greet.go": strings.Replace(greet, `"Hello, "`, `"Hello there, "`, 1) + strings.Repeat("// filler\n", 10)})
	patch, err := Patch(context.Background(), repo.path, Range{From: base, To: tip}, Review{}, "greet.go", false)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Language != "Go" || patch.TooLarge || len(patch.Hunks) != 1 || patch.File.Additions != 1 || patch.File.Deletions != 1 {
		t.Fatalf("patch %+v", patch)
	}
	lines := patch.Hunks[0].Lines
	// The hunk starts inside the block comment, which is still a comment
	// because the file was tokenized whole.
	if first := lines[0]; first.Text != "several lines, so a hunk can start inside it." || len(first.Spans) != 1 || first.Spans[0].Class != "tok-comment" {
		t.Fatalf("first line %+v", first)
	}
	var deleted, added Line
	for _, line := range lines {
		switch line.Kind {
		case LineDeleted:
			deleted = line
		case LineAdded:
			added = line
		}
	}
	if strings.TrimSpace(changedText(added)) != "there" {
		t.Errorf("added line changed %q in %+v", changedText(added), added.Spans)
	}
	if changedText(deleted) != "" {
		t.Errorf("the deleted line lost nothing the added one lacks, but changed %q", changedText(deleted))
	}
	for _, line := range []Line{deleted, added} {
		var joined strings.Builder
		keyword := false
		for _, span := range line.Spans {
			joined.WriteString(span.Text)
			keyword = keyword || span.Class == "tok-keyword" && span.Text == "return"
		}
		if joined.String() != line.Text || !keyword {
			t.Errorf("spans %+v do not join to %q with return as a keyword", line.Spans, line.Text)
		}
	}
}

func changedText(line Line) string {
	var changed strings.Builder
	for _, span := range line.Spans {
		if span.Changed {
			changed.WriteString(span.Text)
		}
	}
	return changed.String()
}

func TestATooLargePatchIsNeverCutShort(t *testing.T) {
	repo := newRepo(t)
	base := repo.commit("base", map[string]string{"keep.txt": "x\n"})
	big := strings.Repeat("line\n", SoftLines+5)
	huge := strings.Repeat("l\n", HardLines+5)
	tip := repo.commit("big", map[string]string{"big.txt": big, "huge.txt": huge})
	between := Range{From: base, To: tip}

	patch, err := Patch(context.Background(), repo.path, between, Review{}, "big.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if !patch.TooLarge || !patch.Loadable || len(patch.Hunks) != 0 || patch.Lines <= SoftLines || patch.Reason == "" || patch.File.Additions != SoftLines+5 {
		t.Fatalf("over the soft cap: %+v", patch)
	}
	patch, err = Patch(context.Background(), repo.path, between, Review{}, "big.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if patch.TooLarge || len(patch.Hunks) != 1 || len(patch.Hunks[0].Lines) != SoftLines+5 || patch.File.Additions != SoftLines+5 {
		t.Fatalf("loaded anyway: too large %v, %d hunks", patch.TooLarge, len(patch.Hunks))
	}
	patch, err = Patch(context.Background(), repo.path, between, Review{}, "huge.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if !patch.TooLarge || patch.Loadable || len(patch.Hunks) != 0 {
		t.Fatalf("over the hard cap: too large %v, loadable %v, %d hunks", patch.TooLarge, patch.Loadable, len(patch.Hunks))
	}
}

func TestPatchFollowsARenameAndRefusesAFileThatDidNotChange(t *testing.T) {
	repo := newRepo(t)
	base := repo.commit("base", map[string]string{"old.go": greet})
	repo.git("mv", "old.go", "new.go")
	tip := repo.commit("rename", map[string]string{"new.go": strings.Replace(greet, "Hello,", "Hi,", 1)})
	patch, err := Patch(context.Background(), repo.path, Range{From: base, To: tip}, Review{}, "new.go", false)
	if err != nil {
		t.Fatal(err)
	}
	if patch.File.Status != StatusRenamed || patch.File.OldPath != "old.go" || len(patch.Hunks) != 1 {
		t.Fatalf("rename: %+v", patch.File)
	}
	if _, err := Patch(context.Background(), repo.path, Range{From: base, To: tip}, Review{}, "elsewhere.go", false); err == nil {
		t.Fatal("a file outside the change must be refused")
	}
}

func TestPatchesLoadsEachFileAsPatchDoes(t *testing.T) {
	repo := newRepo(t)
	base := repo.commit("base", map[string]string{"old.go": greet, "go.sum": "a v1\n"})
	repo.git("mv", "old.go", "new.go")
	tip := repo.commit("work", map[string]string{
		"new.go":           strings.Replace(greet, "Hello,", "Hi,", 1),
		"go.sum":           "a v1\nb v2\n",
		"api/client.pb.go": "package api\n",
		".gitattributes":   "api/*.pb.go linguist-generated\n",
	})
	between, review := Range{From: base, To: tip}, Review{Version: ReviewVersion}
	paths := []string{"new.go", "api/client.pb.go", "go.sum", ".gitattributes"}
	patches, err := Patches(context.Background(), repo.path, between, review, paths, false)
	if err != nil {
		t.Fatal(err)
	}
	for index, path := range paths {
		one, err := Patch(context.Background(), repo.path, between, review, path, false)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(patches[index], one) {
			t.Errorf("%s: Patches gave %+v, Patch %+v", path, patches[index].File, one.File)
		}
	}
	if !patches[1].File.Generated || patches[0].File.OldPath != "old.go" {
		t.Fatalf("generated %v, renamed from %q", patches[1].File.Generated, patches[0].File.OldPath)
	}
	if _, err := Patches(context.Background(), repo.path, between, review, []string{"new.go", "elsewhere.go"}, false); err == nil {
		t.Fatal("a file outside the change must be refused")
	}
}

func TestACopysPatchLeavesOutItsSourcesOwnChanges(t *testing.T) {
	repo := newRepo(t)
	var original, rewritten strings.Builder
	for number := range SoftLines {
		fmt.Fprintf(&original, "line %d\n", number)
		fmt.Fprintf(&rewritten, "rewritten %d\n", number)
	}
	base := repo.commit("base", map[string]string{"source.txt": original.String()})
	// The copy changes one line, while its source is rewritten whole: a
	// patch over the soft cap on its own.
	tip := repo.commit("copy", map[string]string{"copy.txt": original.String() + "extra\n", "source.txt": rewritten.String()})
	patch, err := Patch(context.Background(), repo.path, Range{From: base, To: tip}, Review{}, "copy.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if patch.File.Status != StatusCopied || patch.File.OldPath != "source.txt" || patch.TooLarge || len(patch.Hunks) != 1 || patch.File.Additions != 1 {
		t.Fatalf("copy: too large %v, %d hunks, file %+v", patch.TooLarge, len(patch.Hunks), patch.File)
	}
}
