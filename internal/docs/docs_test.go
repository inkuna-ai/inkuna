package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const valid = "---\ntitle: Rules engine\nlastUpdated: 2026-09-24\nauthor: Brandon Manning\ntags: [rules, planning]\nstatus: current\n---\n# Rules engine\n"

func TestParse(t *testing.T) {
	cases := map[string]struct {
		content  string
		want     Doc
		problems []string
	}{
		"valid": {content: valid, want: Doc{Title: "Rules engine", LastUpdated: "2026-09-24", Author: "Brandon Manning", Tags: []string{"rules", "planning"}, Status: StatusCurrent}},
		"crlf": {
			content: strings.ReplaceAll(valid, "\n", "\r\n"),
			want:    Doc{Title: "Rules engine", LastUpdated: "2026-09-24", Author: "Brandon Manning", Tags: []string{"rules", "planning"}, Status: StatusCurrent},
		},
		"quoted date": {
			content: strings.Replace(valid, "lastUpdated: 2026-09-24", `lastUpdated: "2026-09-24"`, 1),
			want:    Doc{Title: "Rules engine", LastUpdated: "2026-09-24", Author: "Brandon Manning", Tags: []string{"rules", "planning"}, Status: StatusCurrent},
		},
		"missing frontmatter":  {content: "# Just a heading\n", want: Doc{Tags: []string{}}, problems: []string{"missing frontmatter"}},
		"unclosed frontmatter": {content: "---\ntitle: x\n", want: Doc{Tags: []string{}}, problems: []string{"frontmatter is not closed"}},
		// A key for another tool is ignored, not a problem.
		"unknown key": {
			content: strings.Replace(valid, "status: current", "status: current\nowner: docs-team\nnav: {order: 2}", 1),
			want:    Doc{Title: "Rules engine", LastUpdated: "2026-09-24", Author: "Brandon Manning", Tags: []string{"rules", "planning"}, Status: StatusCurrent},
		},
		"bad status": {
			content:  strings.Replace(valid, "status: current", "status: final", 1),
			want:     Doc{Title: "Rules engine", LastUpdated: "2026-09-24", Author: "Brandon Manning", Tags: []string{"rules", "planning"}, Status: "final"},
			problems: []string{`status "final" must be draft, current, or deprecated`},
		},
		"bad date": {
			content:  strings.Replace(valid, "lastUpdated: 2026-09-24", "lastUpdated: 24/09/2026", 1),
			want:     Doc{Title: "Rules engine", LastUpdated: "24/09/2026", Author: "Brandon Manning", Tags: []string{"rules", "planning"}, Status: StatusCurrent},
			problems: []string{`lastUpdated "24/09/2026" must be a YYYY-MM-DD date`},
		},
		"empty fields": {
			content: "---\ntitle: \"\"\ntags: []\n---\nbody",
			want:    Doc{Tags: []string{}},
			problems: []string{
				"title is required", "author is required", "tags must be a non-empty list",
				`status "" must be draft, current, or deprecated`, `lastUpdated "" must be a YYYY-MM-DD date`,
			},
		},
		"empty header": {
			content: "---\n---\nbody",
			want:    Doc{Tags: []string{}},
			problems: []string{
				"title is required", "author is required", "tags must be a non-empty list",
				`status "" must be draft, current, or deprecated`, `lastUpdated "" must be a YYYY-MM-DD date`,
			},
		},
	}
	for name, tc := range cases {
		got := Parse(tc.content)
		tc.want.Problems = tc.problems
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s:\n got %#v\nwant %#v", name, got, tc.want)
		}
	}
}

func TestDiscoverWalksNestedDirectoriesAndKeepsBadDocs(t *testing.T) {
	repo := t.TempDir()
	files := map[string]string{
		"docs/rules.md":              valid,
		"docs/design/index.md":       strings.Replace(valid, "title: Rules engine", "title: Design index", 1),
		"docs/notes.md":              "no frontmatter here",
		"docs/README.MD":             valid,
		"docs/image.png":             "not markdown",
		"docs/.drafts/secret.md":     valid,
		"docs/.hidden.md":            valid,
		"README.md":                  valid,
		"docs/design/assets/.keep":   "",
		"docs/design/assets/raw.txt": "x",
	}
	for name, content := range files {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	index, err := Discover(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(index))
	for _, doc := range index {
		paths = append(paths, doc.Path)
	}
	if want := []string{"README.MD", "design/index.md", "notes.md", "rules.md"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if index[1].Title != "Design index" || len(index[1].Problems) != 0 {
		t.Fatalf("nested doc = %#v", index[1])
	}
	if !reflect.DeepEqual(index[2].Problems, []string{"missing frontmatter"}) {
		t.Fatalf("bad doc should be indexed with a problem: %#v", index[2])
	}
}

func TestDiscoverHonoursDeclaredDirectory(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "handbook", "guide.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := Discover(repo, "handbook")
	if err != nil || len(index) != 1 || index[0].Path != "guide.md" {
		t.Fatalf("index = %#v, err = %v", index, err)
	}
	if index, err := Discover(repo, ""); err != nil || len(index) != 0 {
		t.Fatalf("missing docs dir should give an empty index: %#v, %v", index, err)
	}
	if _, err := Discover(repo, "../elsewhere"); err == nil {
		t.Fatal("escaping docs dir should fail")
	}
	if err := os.WriteFile(filepath.Join(repo, "docs"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(repo, ""); err == nil {
		t.Fatal("a docs root that is a file should fail")
	}
}

func TestValidateDir(t *testing.T) {
	for _, declared := range []string{"", "docs", "documentation/handbook", "./docs", "docs/../wiki"} {
		if err := ValidateDir(declared); err != nil {
			t.Fatalf("%q: unexpected error %v", declared, err)
		}
	}
	for _, declared := range []string{"..", "../docs", "docs/../../x", "/etc/docs"} {
		if err := ValidateDir(declared); err == nil {
			t.Fatalf("%q: expected error", declared)
		}
	}
}

func TestDiscoverRejectsSymlinksThatLeaveTheRepository(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "leak.md"), []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	// docs itself is a link outside the repository.
	if err := os.Symlink(outside, filepath.Join(repo, "docs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Discover(repo, ""); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("linked docs root must be rejected, got %v", err)
	}
	// An intermediate component of the declared root is the link.
	if err := os.Symlink(outside, filepath.Join(repo, "shared")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(outside, "handbook"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(repo, "shared/handbook"); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("linked parent must be rejected, got %v", err)
	}
	// A link inside the docs tree is skipped, not followed.
	if err := os.Mkdir(filepath.Join(repo, "wiki"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "leak.md"), filepath.Join(repo, "wiki", "leak.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "wiki", "more")); err != nil {
		t.Fatal(err)
	}
	if index, err := Discover(repo, "wiki"); err != nil || len(index) != 0 {
		t.Fatalf("links inside the tree must be skipped: %#v, %v", index, err)
	}
	// A repository reached through a link still indexes its own docs.
	linkedRepo := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, linkedRepo); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "wiki", "ok.md"), []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if index, err := Discover(linkedRepo, "wiki"); err != nil || len(index) != 1 || index[0].Path != "ok.md" {
		t.Fatalf("linked repository = %#v, %v", index, err)
	}
}
