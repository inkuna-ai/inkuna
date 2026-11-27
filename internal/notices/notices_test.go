package notices

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite THIRD_PARTY_NOTICES")

// TestTheNoticesAreCurrent regenerates THIRD_PARTY_NOTICES from what the app
// is built from now. It fails when a dependency changed without the file,
// or when a shipped package has no license text to put in it. task notices
// rewrites the file.
func TestTheNoticesAreCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "frontend", "node_modules")); err != nil {
		t.Skip("frontend/node_modules is not installed; run task deps")
	}
	generated, err := Generate(root, Targets)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, Path)
	if *update {
		if err := os.WriteFile(path, generated, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(committed), "\r\n", "\n") != string(generated) {
		t.Fatalf("%s is out of date; run task notices", Path)
	}
	for _, shipped := range []string{"github.com/wailsapp/wails/v2 ", "github.com/wailsapp/go-webview2 ", "github.com/godbus/dbus/v5 ", "Go standard library", "\nmermaid ", "\nreact ", "\ntailwindcss ", "\nvite "} {
		if !strings.Contains(string(generated), shipped) {
			t.Errorf("the notices leave out %q", strings.TrimSpace(shipped))
		}
	}
	for _, devOnly := range []string{"\neslint ", "\nvitest ", "\ntypescript "} {
		if strings.Contains(string(generated), devOnly) {
			t.Errorf("the notices list %q, which the app does not ship", strings.TrimSpace(devOnly))
		}
	}
}

// writeFrontend lays out a package lock and node_modules under a temporary
// frontend directory: files maps a path under it to its contents.
func writeFrontend(t *testing.T, lock string, files map[string]string) string {
	t.Helper()
	frontend := t.TempDir()
	files["package-lock.json"] = lock
	for name, body := range files {
		path := filepath.Join(frontend, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return frontend
}

func TestNPMPackagesAreTheProductionOnesAndTheBundledTools(t *testing.T) {
	frontend := writeFrontend(t, `{"packages":{
		"": {"name":"inkuna-frontend"},
		"node_modules/react": {"version":"19.1.1","license":"MIT"},
		"node_modules/a/node_modules/react": {"version":"19.1.1","license":"MIT"},
		"node_modules/a": {"version":"1.0.0","license":{"type":"ISC"}},
		"node_modules/a/node_modules/b": {"version":"2.0.0","license":"BSD-3-Clause"},
		"node_modules/eslint": {"version":"9.0.0","license":"MIT","dev":true},
		"node_modules/vite": {"version":"7.3.6","license":"MIT","dev":true},
		"node_modules/x/node_modules/vite": {"version":"6.0.0","license":"MIT","dev":true}
	}}`, map[string]string{
		"node_modules/react/LICENSE":                  "MIT react\r\n",
		"node_modules/a/node_modules/react/LICENSE":   "MIT react\r\n",
		"node_modules/a/LICENSE.md":                   "ISC a",
		"node_modules/a/NOTICE":                       "a notice\n\n",
		"node_modules/a/license-update.mjs":           "export {}",
		"node_modules/a/node_modules/b/LICENSE-BSD":   "BSD b",
		"node_modules/eslint/LICENSE":                 "MIT eslint",
		"node_modules/vite/LICENSE.md":                "MIT vite",
		"node_modules/x/node_modules/vite/LICENSE.md": "MIT old vite",
	})
	components, err := NPMPackages(frontend)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, component := range components {
		files := []string{}
		for _, text := range component.Texts {
			files = append(files, text.File+"="+text.Body)
		}
		got = append(got, component.Name+" "+component.Version+" "+component.License+" "+strings.Join(files, ","))
	}
	want := []string{
		"a 1.0.0 ISC LICENSE.md=ISC a\n,NOTICE=a notice\n",
		"b 2.0.0 BSD-3-Clause LICENSE-BSD=BSD b\n",
		"react 19.1.1 MIT LICENSE=MIT react\n",
		"vite 7.3.6 MIT LICENSE.md=MIT vite\n",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("packages =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A package that ships no license file but states it in its README gets
// that section, and nothing after it.
func TestAPackageWithoutALicenseFileGetsItsReadmeSection(t *testing.T) {
	frontend := writeFrontend(t, `{"packages":{"node_modules/fastdom": {"version":"1.0.12"}}}`, map[string]string{
		"node_modules/fastdom/README.md": "# fastdom\n\nUsage.\n\n## License\n\n(The MIT License)\n\nCopyright (c) 2016 Wilson Page\n\n### Terms\n\nPermission is hereby granted.\n\n## Contributing\n\nSend a PR.\n",
	})
	components, err := NPMPackages(frontend)
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 1 || len(components[0].Texts) != 1 {
		t.Fatalf("components = %#v", components)
	}
	text := components[0].Texts[0]
	if text.File != "README.md (License section)" || text.Body != "(The MIT License)\n\nCopyright (c) 2016 Wilson Page\n\n### Terms\n\nPermission is hereby granted.\n" {
		t.Fatalf("text = %#v", text)
	}
}

// A shipped package with no license text anywhere stops the generation, so
// it cannot ship without one.
func TestAPackageWithNoLicenseTextIsAnError(t *testing.T) {
	frontend := writeFrontend(t, `{"packages":{"node_modules/mystery": {"version":"0.1.0"}}}`, map[string]string{
		"node_modules/mystery/README.md": "# mystery\n\nNo license here.\n",
		"node_modules/mystery/index.js":  "",
	})
	_, err := NPMPackages(frontend)
	if !errors.Is(err, errNoLicense) || !strings.Contains(err.Error(), "mystery 0.1.0") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderNamesEachComponentAndItsFiles(t *testing.T) {
	out := string(Render(
		[]Component{{Name: "Go standard library", Texts: []Text{{File: "LICENSE", Body: "BSD go\n"}}}},
		[]Component{{Name: "react", Version: "19.1.1", License: "MIT", Texts: []Text{{File: "LICENSE", Body: "MIT react\n"}}}},
	))
	for _, want := range []string{"\nGo standard library\n\n--- LICENSE\n\nBSD go\n", "\nreact 19.1.1\nLicense: MIT\n\n--- LICENSE\n\nMIT react\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("the notices lack %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Go modules") > strings.Index(out, "npm packages") {
		t.Error("the Go modules come first")
	}
}

// Homebrew moves Go's LICENSE from GOROOT (its libexec) to the directory
// above; the notices read it there, so they match those of any other Go.
func TestTheStandardLibrarysLicenseIsFoundWhereHomebrewMovesIt(t *testing.T) {
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	official := filepath.Join(t.TempDir(), "go")
	write(official, "LICENSE", "the license\n")
	write(official, "PATENTS", "the grant\n")
	keg := t.TempDir()
	write(keg, "LICENSE", "the license\n")
	write(keg, "README.md", "a formula\n")
	write(filepath.Join(keg, "libexec"), "PATENTS", "the grant\n")

	want, err := standardLicense(official)
	if err != nil {
		t.Fatal(err)
	}
	got, err := standardLicense(filepath.Join(keg, "libexec"))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Homebrew's Go gives %+v (%v), the official one %+v", got, err, want)
	}
	if len(want) != 2 || want[0].File != "LICENSE" || want[1].File != "PATENTS" {
		t.Fatalf("texts %+v", want)
	}
}
