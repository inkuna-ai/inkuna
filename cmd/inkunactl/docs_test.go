package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	inkuna "github.com/inkuna-ai/inkuna/internal/app"
)

const validDoc = "---\ntitle: Guide\nlastUpdated: 2026-10-17\nauthor: Brandon Manning\ntags: [guide]\nstatus: current\n---\n\n# Guide\n"

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// declareRepository writes the isolated inkuna.yaml with one repository
// entry.
func declareRepository(t *testing.T, path, docsDir string) {
	t.Helper()
	content := "version: 1\nrepositories:\n  - name: bakery\n    path: " + path + "\n    docs: " + docsDir + "\n"
	if err := os.WriteFile(os.Getenv("INKUNA_CONFIG"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listDocs(t *testing.T, repository string) inkuna.DocIndexView {
	t.Helper()
	var output bytes.Buffer
	if err := run([]string{"docs", "list", repository}, nil, &output); err != nil {
		t.Fatal(err)
	}
	var view inkuna.DocIndexView
	if err := json.Unmarshal(output.Bytes(), &view); err != nil {
		t.Fatalf("docs list output %q: %v", output.String(), err)
	}
	return view
}

func paths(view inkuna.DocIndexView) []string {
	result := []string{}
	for _, doc := range view.Docs {
		result = append(result, doc.Path)
	}
	return result
}

func validate(args ...string) (string, int) {
	var output bytes.Buffer
	err := run(append([]string{"docs", "validate"}, args...), nil, &output)
	var failure exitError
	switch {
	case err == nil:
		return output.String(), 0
	case errors.As(err, &failure):
		return output.String(), failure.code
	default:
		return output.String(), 1
	}
}

func TestDocsListHonoursTheConfiguredDocsRoot(t *testing.T) {
	isolate(t)
	declared, undeclared := t.TempDir(), t.TempDir()
	files := map[string]string{"handbook/guide.md": validDoc, "handbook/notes/todo.md": "# Todo\n", "docs/default.md": validDoc}
	writeFiles(t, declared, files)
	writeFiles(t, undeclared, files)
	declareRepository(t, declared, "handbook")

	view := listDocs(t, declared)
	if view.Path != filepath.Join(declared, "handbook") || !reflect.DeepEqual(paths(view), []string{"guide.md", "notes/todo.md"}) {
		t.Fatalf("declared repository index = %+v", view)
	}
	if guide := view.Docs[0]; guide.Title != "Guide" || guide.Status != "current" || !reflect.DeepEqual(guide.Tags, []string{"guide"}) || len(guide.Problems) != 0 {
		t.Fatalf("guide.md frontmatter = %+v", guide)
	}
	if todo := view.Docs[1]; !reflect.DeepEqual(todo.Problems, []string{"missing frontmatter"}) {
		t.Fatalf("todo.md problems = %v", todo.Problems)
	}
	if view := listDocs(t, undeclared); view.Path != filepath.Join(undeclared, "docs") || !reflect.DeepEqual(paths(view), []string{"default.md"}) {
		t.Fatalf("a repository inkuna.yaml does not declare reads docs/: %+v", view)
	}

	t.Chdir(declared)
	if view := listDocs(t, "."); view.Path != filepath.Join(declared, "handbook") {
		t.Fatalf("a relative repository path must match its inkuna.yaml entry: %+v", view)
	}
	for _, args := range [][]string{{"docs"}, {"docs", "list"}, {"docs", "list", "a", "b"}, {"docs", "show", "."}} {
		if err := run(args, nil, &bytes.Buffer{}); err == nil {
			t.Errorf("%v should be refused", args)
		}
	}
}

func TestDocsListAppliesTheMainCheckoutsEntryToItsWorktrees(t *testing.T) {
	isolate(t)
	repository := cloned(t, "")
	writeFiles(t, repository, map[string]string{"handbook/guide.md": validDoc})
	git(t, repository, "add", "-A")
	git(t, repository, "commit", "-qm", "handbook")
	tree := filepath.Join(filepath.Dir(repository), "bakery-topic")
	git(t, repository, "worktree", "add", "-q", "-b", "topic", tree)
	declareRepository(t, git(t, repository, "rev-parse", "--show-toplevel"), "handbook")

	if view := listDocs(t, tree); view.Path != filepath.Join(tree, "handbook") || !reflect.DeepEqual(paths(view), []string{"guide.md"}) {
		t.Fatalf("worktree index = %+v", view)
	}
}

func TestDocsValidateReportsEachProblemAndExitsByOutcome(t *testing.T) {
	isolate(t)
	repository := t.TempDir()
	writeFiles(t, repository, map[string]string{
		"docs/good.md": validDoc,
		"docs/bad.md":  "---\ntitle: \"\"\nlastUpdated: 2026-10-17\nauthor: Brandon Manning\ntags: [guide]\nstatus: final\n---\n",
		"docs/bare.md": "# Bare\n",
		"docs/yaml.md": "---\ntitle: Guide\ntags: [guide\n---\n",
		"docs/link.md": validDoc + "See [good](good.md) and [gone](notes/gone.md).\n",
	})
	output, code := validate(repository)
	want := []string{
		`docs/bad.md: title is required`,
		`docs/bad.md: status "final" must be draft, current, or deprecated`,
		`docs/bare.md: missing frontmatter`,
		`docs/link.md: broken link "notes/gone.md": no such file`,
		`docs/yaml.md: frontmatter: yaml: line 1: did not find expected ',' or ']'`,
		`docs/: 5 docs, 4 failing`,
	}
	if code != docsInvalid || output != strings.Join(want, "\n")+"\n" {
		t.Fatalf("exit %d, output:\n%s", code, output)
	}

	for _, name := range []string{"bad.md", "bare.md", "yaml.md", "link.md"} {
		if err := os.Remove(filepath.Join(repository, "docs", name)); err != nil {
			t.Fatal(err)
		}
	}
	if output, code := validate(repository); code != 0 || output != "docs/: 1 doc, 0 failing\n" {
		t.Fatalf("clean docs: exit %d, output %q", code, output)
	}
	if output, code := validate(t.TempDir()); code != 0 || output != "docs/: 0 docs, 0 failing\n" {
		t.Fatalf("a repository without docs: exit %d, output %q", code, output)
	}

	notADirectory := t.TempDir()
	writeFiles(t, notADirectory, map[string]string{"docs": "not a directory"})
	for name, args := range map[string][]string{
		"no repository":        {},
		"two repositories":     {repository, repository},
		"unknown flag":         {"--strict", repository},
		"docs root is a file":  {notADirectory},
		"changed-since no git": {"--changed-since", "main", repository},
	} {
		if _, code := validate(args...); code != docsCannotRun {
			t.Errorf("%s: exit %d, want %d", name, code, docsCannotRun)
		}
	}
	declareRepository(t, repository, "../outside")
	if _, code := validate(repository); code != docsCannotRun {
		t.Errorf("an invalid inkuna.yaml: exit %d, want %d", code, docsCannotRun)
	}
}

func TestDocsValidateChangedSinceRequiresFrontmatterOnlyOnTouchedDocs(t *testing.T) {
	isolate(t)
	repository := cloned(t, "")
	writeFiles(t, repository, map[string]string{
		"docs/legacy.md":    "# Legacy\n",
		"docs/committed.md": "# Committed\n",
		"docs/dirty.md":     "# Dirty\n",
		"docs/upstream.md":  "# Upstream\n",
		"docs/broken.md":    "---\ntitle: Broken\n",
		"docs/good.md":      validDoc,
		"docs/restored.md":  "# Restored\n",
		"docs/reverted.md":  "# Reverted\n",
		// Broken links in docs topic leaves alone are not topic's to fix.
		"docs/linked.md": validDoc + "[gone](gone.md)\n",
	})
	writeFiles(t, repository, map[string]string{"docs/legacy.md": "# Legacy\n\n[old](old.md)\n"})
	git(t, repository, "add", "-A")
	git(t, repository, "commit", "-qm", "docs")
	base := git(t, repository, "rev-parse", "HEAD")
	git(t, repository, "checkout", "-qb", "topic")
	writeFiles(t, repository, map[string]string{"docs/committed.md": "# Committed\n\nMore in [gone](gone.md).\n", "docs/reverted.md": "# Reverted\n\nMore.\n"})
	git(t, repository, "commit", "-qam", "edit committed.md and reverted.md")
	// main moves on after topic leaves it; its change is not topic's.
	git(t, repository, "checkout", "-q", "main")
	writeFiles(t, repository, map[string]string{"docs/upstream.md": "# Upstream\n\nMore.\n"})
	git(t, repository, "commit", "-qam", "edit upstream.md")
	git(t, repository, "checkout", "-q", "topic")
	writeFiles(t, repository, map[string]string{
		"docs/dirty.md":     "# Dirty\n\nMore.\n",
		"docs/staged.md":    "# Staged\n",
		"docs/untracked.md": "# Untracked\n",
		"notes/outside.md":  "# Outside the docs root\n",
	})
	git(t, repository, "add", "docs/staged.md")
	// Both end up matching the base on disk, but the branch changed each:
	// one is staged and restored on disk, the other committed and then
	// reverted in the index and on disk.
	writeFiles(t, repository, map[string]string{"docs/restored.md": "# Restored\n\nMore.\n"})
	git(t, repository, "add", "docs/restored.md")
	writeFiles(t, repository, map[string]string{"docs/restored.md": "# Restored\n"})
	git(t, repository, "checkout", base, "--", "docs/reverted.md")

	output, code := validate("--changed-since", "main", repository)
	want := []string{
		`docs/broken.md: frontmatter is not closed`,
		`docs/committed.md: missing frontmatter`,
		`docs/committed.md: broken link "gone.md": no such file`,
		`docs/dirty.md: missing frontmatter`,
		`docs/restored.md: missing frontmatter`,
		`docs/reverted.md: missing frontmatter`,
		`docs/staged.md: missing frontmatter`,
		`docs/untracked.md: missing frontmatter`,
		`docs/: 11 docs, 7 failing, 3 unchanged since main with missing frontmatter or broken links`,
	}
	if code != docsInvalid || output != strings.Join(want, "\n")+"\n" {
		t.Fatalf("exit %d, output:\n%s", code, output)
	}

	git(t, repository, "add", "-A")
	git(t, repository, "commit", "-qm", "wip")
	if output, code := validate("--changed-since", "HEAD", repository); code != docsInvalid || !strings.HasSuffix(output, "docs/: 11 docs, 1 failing, 9 unchanged since HEAD with missing frontmatter or broken links\n") {
		t.Fatalf("invalid frontmatter fails even when unchanged: exit %d, output:\n%s", code, output)
	}
	for _, ref := range []string{"no-such-ref", "--upload-pack=touch"} {
		if _, code := validate("--changed-since="+ref, repository); code != docsCannotRun {
			t.Errorf("--changed-since=%s: exit %d, want %d", ref, code, docsCannotRun)
		}
	}
}
