package diff

import (
	"strings"
	"testing"
)

func TestGeneratedPatternsFollowGitignore(t *testing.T) {
	for _, check := range []struct {
		pattern string
		matches []string
		misses  []string
	}{
		{"go.sum", []string{"go.sum", "tools/go.sum"}, []string{"go.sum.bak", "notgo.sum"}},
		{"/go.sum", []string{"go.sum"}, []string{"tools/go.sum"}},
		{"ui/dist/**", []string{"ui/dist/app.js", "ui/dist/assets/a.css"}, []string{"ui/dist", "x/ui/dist/a.js"}},
		{"docs/gen/", []string{"docs/gen/index.html"}, []string{"docs/gen", "docs/general.md"}},
		{"build", []string{"build", "build/out.js", "web/build/out.js"}, []string{"builder.go"}},
		{"*.pb.go", []string{"api.pb.go", "api/v1/client.pb.go"}, []string{"api.go"}},
		{"api/*.pb.go", []string{"api/client.pb.go"}, []string{"api/v1/client.pb.go"}},
		{"**/testdata/*.golden", []string{"testdata/a.golden", "internal/x/testdata/a.golden"}, []string{"testdata/deep/a.golden"}},
		{"a/**/b", []string{"a/b", "a/x/b", "a/x/y/b"}, []string{"ab"}},
		{"src/**.go", []string{"src/main.go"}, []string{"src/nested/file.go"}},
		{"a**/b", []string{"a/b", "ax/b"}, []string{"ax/y/b"}},
		{"**", []string{"a", "a/b/c"}, nil},
		{"file?.txt", []string{"file1.txt"}, []string{"file10.txt", "file/.txt"}},
		{"[!a]*.js", []string{"b.js"}, []string{"a.js"}},
		{`\!important`, []string{"!important"}, []string{"important"}},
		{"naïve.md", []string{"docs/café/naïve.md"}, []string{"naive.md"}},
	} {
		compiled, err := compilePattern(check.pattern)
		if err != nil {
			t.Fatalf("%s: %v", check.pattern, err)
		}
		for _, path := range check.matches {
			if !compiled.match.MatchString(path) {
				t.Errorf("%s should match %s", check.pattern, path)
			}
		}
		for _, path := range check.misses {
			if compiled.match.MatchString(path) {
				t.Errorf("%s should not match %s", check.pattern, path)
			}
		}
	}
}

func TestALaterRuleOverridesAnEarlierOne(t *testing.T) {
	rules, err := newGenerated(Review{Version: ReviewVersion, Generated: []string{"!go.sum", "docs/**", "!docs/keep.md"}})
	if err != nil {
		t.Fatal(err)
	}
	rules.attributes = map[string]bool{"api/a.pb.go": true, "yarn.lock": false}
	for path, want := range map[string]bool{
		"go.sum": false, "package-lock.json": true, "yarn.lock": false, "api/a.pb.go": true,
		"docs/a.md": true, "docs/keep.md": false, "main.go": false, "frontend/wailsjs/go/models.ts": true,
	} {
		if got := rules.flags(path); got != want {
			t.Errorf("%s: generated %v, want %v", path, got, want)
		}
	}
}

func TestReviewConfigIsStrict(t *testing.T) {
	review, err := ParseReview([]byte("version: 1\ngenerated:\n  - docs/gen/\n  - \"!go.sum\"\n"))
	if err != nil || len(review.Generated) != 2 {
		t.Fatalf("valid config: %+v, %v", review, err)
	}
	for name, config := range map[string]string{
		"unknown key":     "version: 1\ngenerated: []\ncollapse: []\n",
		"no version":      "generated: [a]\n",
		"later version":   "version: 2\n",
		"unclosed class":  "version: 1\ngenerated: [\"a[b\"]\n",
		"empty pattern":   "version: 1\ngenerated: [\"!\"]\n",
		"not a list":      "version: 1\ngenerated: docs\n",
		"pattern not str": "version: 1\ngenerated: [{a: b}]\n",
	} {
		if _, err := ParseReview([]byte(config)); err == nil {
			t.Errorf("%s: accepted %q", name, config)
		}
	}
}

func TestEachRepositoryWithoutAReviewConfigGetsTheDefaults(t *testing.T) {
	review, err := LoadReview(t.TempDir())
	if err != nil || review.Version != 0 || len(review.Generated) != 0 {
		t.Fatalf("missing config: %+v, %v", review, err)
	}
	if !strings.Contains(strings.Join(DefaultGenerated, " "), "ui/dist/**") {
		t.Fatal("ui/dist is generated in every repository")
	}
}
