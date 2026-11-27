//go:build !windows

// The search path is widened only on macOS and Linux (EnsureSearchPath), and
// these tests use Unix paths; path_windows_test.go covers Windows.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWidenSearchPathAppendsExistingDirectoriesOnce(t *testing.T) {
	exists := func(path string) bool { return path != "/missing" }
	current := strings.Join([]string{"/usr/bin", "/opt/homebrew/bin"}, string(filepath.ListSeparator))
	got := widenSearchPath(current, []string{"/opt/homebrew/bin", "/missing", "/usr/local/bin", "/usr/local/bin/"}, exists)
	want := strings.Join([]string{"/usr/bin", "/opt/homebrew/bin", "/usr/local/bin"}, string(filepath.ListSeparator))
	if got != want {
		t.Fatalf("widenSearchPath = %q, want %q", got, want)
	}
}

func TestWidenSearchPathKeepsExplicitOrderFirst(t *testing.T) {
	got := widenSearchPath("/custom/bin", []string{"/opt/homebrew/bin"}, func(string) bool { return true })
	if !strings.HasPrefix(got, "/custom/bin"+string(filepath.ListSeparator)) {
		t.Fatalf("existing PATH entries must stay first, got %q", got)
	}
}

func TestSearchPathCandidatesIncludeHomeAndGopath(t *testing.T) {
	environ := func(name string) string {
		if name == "GOPATH" {
			return "/go/path"
		}
		return ""
	}
	candidates := searchPathCandidates("/home/dev", environ)
	for _, want := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/go/path/bin", "/home/dev/.local/bin", "/home/dev/.claude/local", "/home/dev/.npm-global/bin"} {
		if !contains(candidates, want) {
			t.Fatalf("candidates %v missing %q", candidates, want)
		}
	}
}

// A Mac with both Homebrew node and nvm: the shell runs nvm's, so a desktop
// launch must too, or a project's gates run on a node it does not pin.
func TestSearchPathCandidatesPutVersionManagersBeforeSystemDirectories(t *testing.T) {
	home := t.TempDir()
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v24.4.1", "bin")
	aliasDir := filepath.Join(home, ".nvm", "alias")
	for _, directory := range []string{nvmBin, aliasDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	defaultAlias := filepath.Join(aliasDir, "default")
	if err := os.WriteFile(defaultAlias, []byte("24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidates := searchPathCandidates(home, func(string) string { return "" })
	managed := []string{
		nvmBin,
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".asdf", "shims"),
		filepath.Join(home, ".local", "share", "mise", "shims"),
		filepath.Join(home, ".local", "share", "fnm", "aliases", "default", "bin"),
	}
	for _, manager := range managed {
		for _, system := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin"} {
			managerAt, systemAt := indexOf(candidates, manager), indexOf(candidates, system)
			if managerAt < 0 || systemAt < 0 || managerAt > systemAt {
				t.Fatalf("candidates %v must probe %q before %q", candidates, manager, system)
			}
		}
	}
	separator := string(filepath.ListSeparator)
	got := widenSearchPath("/usr/bin"+separator+"/bin", candidates, func(path string) bool { return path == nvmBin || path == "/opt/homebrew/bin" })
	if want := strings.Join([]string{"/usr/bin", "/bin", nvmBin, "/opt/homebrew/bin"}, separator); got != want {
		t.Fatalf("widenSearchPath = %q, want %q", got, want)
	}

	// A default that names no installed version ("system", one that is not
	// installed, or none) means the shell runs the node outside nvm, so nvm's
	// newest version stays the last resort it was before.
	for _, alias := range []string{"system", "20", ""} {
		if alias == "" {
			if err := os.Remove(defaultAlias); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(defaultAlias, []byte(alias+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		candidates = searchPathCandidates(home, func(string) string { return "" })
		if last := candidates[len(candidates)-1]; last != nvmBin {
			t.Fatalf("with nvm's default on %q, candidates %v must end with %q", alias, candidates, nvmBin)
		}
	}
}

func TestSearchPathCandidatesUseFirstGopathEntry(t *testing.T) {
	first, second := filepath.Join("/go", "first"), filepath.Join("/go", "second")
	environ := func(name string) string {
		if name == "GOPATH" {
			return first + string(filepath.ListSeparator) + second
		}
		return ""
	}
	candidates := searchPathCandidates("", environ)
	if !contains(candidates, filepath.Join(first, "bin")) {
		t.Fatalf("candidates %v missing the first GOPATH entry's bin", candidates)
	}
	for _, unwanted := range []string{filepath.Join(second, "bin"), filepath.Join(first+string(filepath.ListSeparator)+second, "bin")} {
		if contains(candidates, unwanted) {
			t.Fatalf("candidates %v should not contain %q", candidates, unwanted)
		}
	}
}

func TestNvmDefaultBinResolvesAliases(t *testing.T) {
	nvmDir := t.TempDir()
	for _, version := range []string{"v18.20.0", "v24.4.1", "v22.1.0"} {
		if err := os.MkdirAll(filepath.Join(nvmDir, "versions", "node", version, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(nvmDir, "alias"), 0o755); err != nil {
		t.Fatal(err)
	}
	aliases := map[string]string{
		"lts/*":       "lts/jod",
		"lts/jod":     "v22.1.0",
		"lts/missing": "v20.0.0",
		"work":        "team",
		"team":        "18",
		"loop-a":      "loop-b",
		"loop-b":      "loop-a",
	}
	for name, target := range aliases {
		path := filepath.Join(nvmDir, "alias", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(target+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		"node": "v24.4.1", "stable": "v24.4.1", "22": "v22.1.0", "v18.20.0": "v18.20.0",
		// lts/* follows alias/lts/* -> lts/jod -> v22.1.0, not the newest install.
		"lts/*": "v22.1.0", "lts/jod": "v22.1.0",
		// A custom alias chain: work -> team -> 18.
		"work": "v18.20.0",
	}
	// Defaults that name no installed version, "system" (the node outside
	// nvm) among them, fall back to the newest installed version and say so.
	fallbacks := []string{"", "99", "lts/missing", "unknown", "loop-a", "../../escape", "v", "system"}
	check := func(alias, want string, wantResolved bool) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(nvmDir, "alias", "default"), []byte(alias+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, resolved := nvmDefaultBin(nvmDir)
		if filepath.Base(filepath.Dir(got)) != want || resolved != wantResolved {
			t.Fatalf("alias %q resolved to %q (resolved = %v), want version %s (resolved = %v)", alias, got, resolved, want, wantResolved)
		}
	}
	for alias, want := range cases {
		check(alias, want, true)
	}
	for _, alias := range fallbacks {
		check(alias, "v24.4.1", false)
	}
	if err := os.Remove(filepath.Join(nvmDir, "alias", "default")); err != nil {
		t.Fatal(err)
	}
	if got, resolved := nvmDefaultBin(nvmDir); filepath.Base(filepath.Dir(got)) != "v24.4.1" || resolved {
		t.Fatalf("with no default alias, got %q (resolved = %v), want the newest version, unresolved", got, resolved)
	}
	if got, _ := nvmDefaultBin(filepath.Join(nvmDir, "absent")); got != "" {
		t.Fatalf("missing nvm directory should resolve to nothing, got %q", got)
	}
}

func TestEnsureSearchPathIsIdempotent(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	first := EnsureSearchPath()
	second := EnsureSearchPath()
	if first != second {
		t.Fatalf("EnsureSearchPath changed PATH on a second call: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "/usr/bin") {
		t.Fatalf("PATH must keep its original entries first, got %q", first)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func indexOf(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}
	return -1
}
