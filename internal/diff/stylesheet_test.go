package diff

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var update = flag.Bool("update", false, "rewrite frontend/src/diff.css from Stylesheet")

// The app imports its copy of the stylesheet; run
// go test ./internal/diff -run Stylesheet -update after changing it.
func TestTheAppsStylesheetIsCurrent(t *testing.T) {
	path := filepath.Join("..", "..", filepath.FromSlash(StylesheetPath))
	if *update {
		if err := os.WriteFile(path, []byte(Stylesheet()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copied, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != Stylesheet() {
		t.Fatalf("%s is out of date; run go test ./internal/diff -run Stylesheet -update", StylesheetPath)
	}
}

// Each color's fallback is the value its token has in the app, so the
// HTML page, which has no tokens, looks like the app.
func TestStylesheetColorsAreTheAppsTokens(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "index.css"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, match := range regexp.MustCompile(`(--color-[a-z0-9-]+):\s*(#[0-9a-fA-F]{6});`).FindAllStringSubmatch(string(source), -1) {
		tokens[match[1]] = match[2]
	}
	uses := regexp.MustCompile(`var\((--[a-z0-9-]+), (#[0-9a-fA-F]{6})\)`).FindAllStringSubmatch(Stylesheet(), -1)
	if len(uses) == 0 {
		t.Fatal("the stylesheet uses no tokens")
	}
	for _, use := range uses {
		if tokens[use[1]] != use[2] {
			t.Errorf("%s falls back to %s, but the app sets it to %q", use[1], use[2], tokens[use[1]])
		}
	}
}
