package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTreeNestsDocsByFolderAndSortsEachLevel(t *testing.T) {
	index := []Doc{
		{Path: "README.md"},
		{Path: "a-b/y.md"},
		{Path: "guide/z.md"},
		{Path: "guide/deep/b.md"},
		{Path: "guide/a.md"},
		{Path: "api/x.md"},
	}
	tree := Tree(index)
	names := func(folders []Folder) []string {
		result := []string{}
		for _, folder := range folders {
			result = append(result, folder.Name)
		}
		return result
	}
	paths := func(docs []Doc) []string {
		result := []string{}
		for _, doc := range docs {
			result = append(result, doc.Path)
		}
		return result
	}
	if tree.Name != "" || tree.Path != "" || !reflect.DeepEqual(paths(tree.Docs), []string{"README.md"}) {
		t.Fatalf("root = %+v", tree)
	}
	if got := names(tree.Folders); !reflect.DeepEqual(got, []string{"a-b", "api", "guide"}) {
		t.Fatalf("root folders = %v", got)
	}
	guide := tree.Folders[2]
	if guide.Path != "guide" || !reflect.DeepEqual(paths(guide.Docs), []string{"guide/a.md", "guide/z.md"}) {
		t.Fatalf("guide = %+v", guide)
	}
	if len(guide.Folders) != 1 || guide.Folders[0].Path != "guide/deep" || !reflect.DeepEqual(paths(guide.Folders[0].Docs), []string{"guide/deep/b.md"}) {
		t.Fatalf("guide/deep = %+v", guide.Folders)
	}
	if empty := Tree(nil); empty.Folders == nil || empty.Docs == nil {
		t.Fatalf("an empty tree must encode lists, not null: %#v", empty)
	}
}

func TestReadReturnsTheBodyAfterTheFrontmatter(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "handbook", "rules.md"), valid+"\nBody.\n")
	write(t, filepath.Join(repo, "handbook", "notes", "todo.md"), "# Todo\n")
	write(t, filepath.Join(repo, "handbook", "open.md"), "---\ntitle: Open\n")

	content, err := Read(repo, "handbook", "rules.md")
	if err != nil {
		t.Fatal(err)
	}
	if content.Doc.Path != "rules.md" || content.Doc.Title != "Rules engine" || len(content.Doc.Problems) != 0 || content.Markdown != "# Rules engine\n\nBody.\n" {
		t.Fatalf("rules.md = %#v", content)
	}
	content, err = Read(repo, "handbook", "notes/todo.md")
	if err != nil || content.Markdown != "# Todo\n" || !reflect.DeepEqual(content.Doc.Problems, []string{ProblemMissing}) {
		t.Fatalf("a doc without frontmatter keeps its content: %#v, %v", content, err)
	}
	content, err = Read(repo, "handbook", "open.md")
	if err != nil || content.Markdown != "---\ntitle: Open\n" || !reflect.DeepEqual(content.Doc.Problems, []string{"frontmatter is not closed"}) {
		t.Fatalf("unclosed frontmatter stays in the Markdown: %#v, %v", content, err)
	}
}

func TestReadRefusesPathsTheIndexCouldNotList(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "docs", "guide.md"), valid)
	write(t, filepath.Join(repo, "docs", ".drafts", "secret.md"), valid)
	write(t, filepath.Join(repo, "docs", "notes.txt"), "notes")
	write(t, filepath.Join(repo, "README.md"), valid)
	if err := os.Mkdir(filepath.Join(repo, "docs", "folder.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "docs", "huge.md"), strings.Repeat("x", MaxBytes+1))
	for path, want := range map[string]string{
		"":                  "required",
		"/etc/passwd.md":    "relative",
		"../README.md":      "escapes",
		"a/../../x.md":      "escapes",
		"a/..":              "escapes",
		"./guide.md":        "not clean",
		"a//guide.md":       "not clean",
		`a\guide.md`:        "forward slashes",
		".drafts/secret.md": "hidden entry",
		"notes.txt":         "not a Markdown file",
		"missing.md":        "does not exist",
		"folder.md":         "not a regular file",
		"huge.md":           "larger than 2 MiB",
	} {
		if _, err := Read(repo, "", path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Read(%q) = %v, want an error containing %q", path, err, want)
		}
	}
	if _, err := Read(repo, "../elsewhere", "guide.md"); err == nil || !strings.Contains(err.Error(), "escapes the repository") {
		t.Errorf("an escaping docs root must be refused, got %v", err)
	}
	if _, err := Read(repo, "missing", "guide.md"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a missing docs root must be reported, got %v", err)
	}
}

func TestReadRefusesSymbolicLinksBelowTheDocsRoot(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "leak.md"), valid)
	repo := t.TempDir()
	write(t, filepath.Join(repo, "docs", "guide.md"), valid)
	if err := os.Symlink(filepath.Join(outside, "leak.md"), filepath.Join(repo, "docs", "leak.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, link := range []struct{ target, name string }{
		{outside, "more"},
		{filepath.Join(repo, "docs", "guide.md"), "alias.md"},
		{filepath.Join(repo, "docs"), "self"},
	} {
		if err := os.Symlink(link.target, filepath.Join(repo, "docs", link.name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"leak.md", "more/leak.md", "alias.md", "self/guide.md"} {
		if _, err := Read(repo, "", path); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("Read(%q) = %v, want a symbolic link refusal", path, err)
		}
	}
	// A repository reached through a link still reads its own docs.
	linked := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, linked); err != nil {
		t.Fatal(err)
	}
	if content, err := Read(linked, "", "guide.md"); err != nil || content.Doc.Title != "Rules engine" {
		t.Fatalf("linked repository = %#v, %v", content, err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAMissingRepositoryIsAnErrorNotAnEmptyIndex(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "gone")
	if index, err := Discover(repo, ""); err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("Discover on a missing repository = %#v, %v; want a repository error", index, err)
	}
	if _, err := Read(repo, "", "guide.md"); err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("Read on a missing repository = %v; want a repository error", err)
	}
	// A missing docs root inside an existing repository still indexes as empty.
	if index, err := Discover(t.TempDir(), "handbook"); err != nil || len(index) != 0 {
		t.Fatalf("missing docs root = %#v, %v; want an empty index", index, err)
	}
}
