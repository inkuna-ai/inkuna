package docs

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverResolvesEachKindOfLink(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "README.md"), "# Readme\n")
	write(t, filepath.Join(repo, "internal", "app.go"), "package app\n")
	write(t, filepath.Join(repo, "docs", "guide.md"), valid)
	write(t, filepath.Join(repo, "docs", "notes", "setup.md"), valid)
	write(t, filepath.Join(repo, "docs", "notes", "My Notes.md"), valid)
	write(t, filepath.Join(repo, "docs", "index.md"), valid+`
Inline [guide](guide.md), anchored [setup](notes/setup.md#install), and [encoded](notes/My%20Notes.md).
Reference-style [setup again][setup] and [shortcut].
Repository files: [readme](../README.md), [code](../internal/app.go?plain=1#L3).
Web: [GitHub](https://github.com/acme/bakery/blob/main/docs/a.md#top) and <https://example.com/x>.
Not followed: [this page](#rules), [query](?plain=1), [root](/docs/guide.md), [mail](mailto:a@example.com), ![diagram](guide.md).

    [indented code](missing-in-code.md)

`+"```"+`
[fenced code](missing-in-fence.md)
`+"```"+`

[setup]: ./notes/setup.md
[shortcut]: notes/../guide.md "Guide"
`)
	write(t, filepath.Join(repo, "docs", "notes", "broken.md"), `# No frontmatter

[gone](../gone.md), [escape](../../../outside.md), and [back](../guide.md).
`)

	index, err := Discover(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Doc{}
	for _, doc := range index {
		byPath[doc.Path] = doc
	}
	want := []Link{
		{Kind: LinkDoc, Path: "guide.md"},
		{Kind: LinkDoc, Path: "notes/setup.md"},
		{Kind: LinkDoc, Path: "notes/My Notes.md"},
		{Kind: LinkDoc, Path: "notes/setup.md"},
		{Kind: LinkDoc, Path: "guide.md"},
		{Kind: LinkFile, Path: "README.md"},
		{Kind: LinkFile, Path: "internal/app.go"},
		{Kind: LinkURL, URL: "https://github.com/acme/bakery/blob/main/docs/a.md"},
		{Kind: LinkURL, URL: "https://example.com/x"},
	}
	if got := byPath["index.md"]; !reflect.DeepEqual(got.Links, want) || len(got.Problems) != 0 {
		t.Fatalf("index.md links = %#v\nproblems = %v", got.Links, got.Problems)
	}

	broken := byPath["notes/broken.md"]
	wantProblems := []string{
		ProblemMissing,
		`broken link "../gone.md": no such file`,
		`broken link "../../../outside.md": leaves the repository`,
	}
	if !reflect.DeepEqual(broken.Problems, wantProblems) || !reflect.DeepEqual(broken.Links, []Link{{Kind: LinkDoc, Path: "guide.md"}}) {
		t.Fatalf("broken.md = %#v", broken)
	}
	for _, problem := range broken.Problems {
		if IsLinkProblem(problem) != (problem != ProblemMissing) {
			t.Errorf("IsLinkProblem(%q) = %v", problem, IsLinkProblem(problem))
		}
	}

	// Read resolves a doc's links the same way discovery does.
	content, err := Read(repo, "", "index.md")
	if err != nil || !reflect.DeepEqual(content.Doc, byPath["index.md"]) {
		t.Fatalf("Read(index.md) = %#v, %v", content.Doc, err)
	}
	content, err = Read(repo, "", "notes/broken.md")
	if err != nil || !reflect.DeepEqual(content.Doc, broken) {
		t.Fatalf("Read(notes/broken.md) = %#v, %v", content.Doc, err)
	}
}

func TestLinksResolveFromTheDeclaredDocsRoot(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "handbook", "a.md"), valid+"[b](b.md) and [hidden](.drafts/c.md)\n")
	write(t, filepath.Join(repo, "handbook", "b.md"), valid)
	write(t, filepath.Join(repo, "handbook", ".drafts", "c.md"), valid)
	write(t, filepath.Join(repo, "docs", "elsewhere.md"), valid)
	index, err := Discover(repo, "./handbook/")
	if err != nil {
		t.Fatal(err)
	}
	want := []Link{{Kind: LinkDoc, Path: "b.md"}, {Kind: LinkFile, Path: "handbook/.drafts/c.md"}}
	if len(index) != 2 || !reflect.DeepEqual(index[0].Links, want) {
		t.Fatalf("handbook index = %#v", index)
	}
}
