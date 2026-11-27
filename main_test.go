package main

import (
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// The shipped page renders repository content, so it must not be able to
// load anything from another server.
func TestShippedPageKeepsContentLoadsInsideTheApp(t *testing.T) {
	page, err := assets.ReadFile("ui/dist/index.html")
	// Go alone can test a checkout without a frontend build; CI always
	// builds first, so there a missing page is a failure, not a skip.
	if errors.Is(err, fs.ErrNotExist) && os.Getenv("CI") == "" {
		t.Skip("ui/dist is not built; task build or task ci builds it")
	}
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`http-equiv="Content-Security-Policy"\s+content="([^"]+)"`).FindSubmatch(page)
	if match == nil {
		t.Fatal("ui/dist/index.html declares no Content-Security-Policy; rebuild it with task build")
	}
	directives := map[string][]string{}
	for _, directive := range strings.Split(string(match[1]), ";") {
		if fields := strings.Fields(directive); len(fields) > 0 {
			directives[fields[0]] = fields[1:]
		}
	}
	allowed := map[string]bool{"'self'": true, "data:": true, "blob:": true, "'unsafe-inline'": true, "'none'": true}
	for _, name := range []string{"default-src", "img-src", "style-src", "font-src", "media-src", "object-src"} {
		sources, ok := directives[name]
		if !ok || len(sources) == 0 {
			t.Errorf("%s is not restricted", name)
		}
		for _, source := range sources {
			if !allowed[source] {
				t.Errorf("%s allows %s", name, source)
			}
		}
	}
	// Markup that slipped past a sanitizer, in a Mermaid diagram from a
	// repository's docs, must not run a script: it could call every bound
	// method, and send what it read out. Each of these takes exactly what is
	// written here, and nothing inline.
	for name, want := range map[string]string{
		"script-src": "'self'", "connect-src": "'self'", "frame-src": "'none'", "base-uri": "'none'", "form-action": "'none'",
	} {
		if got := strings.Join(directives[name], " "); got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
}

func TestFrontendBuiltSaysHowToBuildIt(t *testing.T) {
	if err := frontendBuilt(fstest.MapFS{"ui/README.md": {}}); err == nil || !strings.Contains(err.Error(), "task build") {
		t.Fatalf("a binary without the frontend must say how to build it: %v", err)
	}
	if err := frontendBuilt(fstest.MapFS{"ui/dist/index.html": {}}); err != nil {
		t.Fatal(err)
	}
}
