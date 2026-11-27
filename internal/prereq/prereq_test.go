package prereq

import (
	"context"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// fakeProbe answers from tables: programs on PATH, and the output of each
// command line. A command line it has no answer for fails, as a CLI that
// errors would.
type fakeProbe struct {
	mu       sync.Mutex
	programs map[string]bool
	outputs  map[string]string
	failures map[string]string
	ran      []string
}

func newFake() *fakeProbe {
	return &fakeProbe{programs: map[string]bool{}, outputs: map[string]string{}, failures: map[string]string{}}
}

func (fake *fakeProbe) LookPath(name string) (string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.programs[name] {
		return "/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func (fake *fakeProbe) Output(_ context.Context, path string, args ...string) ([]byte, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	line := strings.Join(append([]string{strings.TrimPrefix(path, "/bin/")}, args...), " ")
	fake.ran = append(fake.ran, line)
	if output, ok := fake.outputs[line]; ok {
		return []byte(output), nil
	}
	return []byte(fake.failures[line]), errors.New("exit status 1")
}

// install puts a program on PATH answering command lines with outputs.
func (fake *fakeProbe) install(program string, outputs map[string]string) *fakeProbe {
	fake.programs[program] = true
	for line, output := range outputs {
		fake.outputs[line] = output
	}
	return fake
}

const signedInClaude = `{"loggedIn": true, "authMethod": "claude.ai"}`

// everything is a machine with every prerequisite of goos, new enough and
// signed in.
func everything(goos string) *fakeProbe {
	fake := newFake().
		install("git", map[string]string{"git --version": "git version 2.50.1"}).
		install("go", map[string]string{"go version": "go version go1.25.5 " + goos + "/amd64"}).
		install("node", map[string]string{"node --version": "v22.11.0"}).
		install("npm", map[string]string{"npm --version": "10.9.0"}).
		install("task", map[string]string{"task --version": "Task version: v3.40.0"}).
		install("gh", map[string]string{"gh --version": "gh version 2.62.0 (2024-11-14)", "gh auth status": "Logged in"}).
		install("codex", map[string]string{"codex --version": "codex-cli 0.46.0", "codex login status": "Logged in using ChatGPT"}).
		install("claude", map[string]string{"claude --version": "2.0.14 (Claude Code)", "claude auth status": signedInClaude}).
		install("copilot", map[string]string{"copilot --version": "GitHub Copilot CLI 1.0.105."})
	switch goos {
	case "darwin":
		fake.install("brew", nil).install("xcode-select", map[string]string{"xcode-select -p": "/Library/Developer/CommandLineTools"})
	case "linux":
		fake.install("apt-get", nil).install("cc", map[string]string{"cc --version": "cc (Ubuntu 13.2.0-23ubuntu4) 13.2.0"}).
			install("pkg-config", map[string]string{"pkg-config --modversion gtk+-3.0": "3.24.41", "pkg-config --modversion webkit2gtk-4.1": "2.44.0"})
	case "windows":
		fake.install("winget", nil).install("reg", map[string]string{
			"reg query " + webView2Keys[0] + " /v pv": "\r\n" + webView2Keys[0] + "\r\n    pv    REG_SZ    131.0.2903.70\r\n",
		})
	}
	return fake
}

// nothing is a machine with none of Inkuna's prerequisites, but with the
// platform's package manager and the tools the checks themselves use.
func nothing(goos string) *fakeProbe {
	fake := newFake()
	switch goos {
	case "darwin":
		fake.install("brew", nil).install("xcode-select", nil)
	case "linux":
		fake.install("apt-get", nil).install("pkg-config", nil)
	case "windows":
		fake.install("winget", nil).install("reg", nil)
	}
	return fake
}

func check(probe Probe, goos string) Report {
	return Check(context.Background(), probe, Options{OS: goos})
}

func byID(t *testing.T, report Report, id string) Item {
	t.Helper()
	for _, item := range report.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("no item %q in %+v", id, report.Items)
	return Item{}
}

func ids(report Report) []string {
	var listed []string
	for _, item := range report.Items {
		listed = append(listed, item.ID)
	}
	return listed
}

func TestEveryPlatformListsItsOwnDesktopDependencies(t *testing.T) {
	common := []string{"git", "go", "node", "npm", "task"}
	tail := []string{"gh", "codex", "claude", "copilot"}
	for goos, desktop := range map[string][]string{
		"darwin":  {"xcode"},
		"linux":   {"cc", "gtk", "webkitgtk"},
		"windows": {"webview2"},
	} {
		want := append(append(append([]string{}, common...), desktop...), tail...)
		if got := ids(check(everything(goos), goos)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s items = %v, want %v", goos, got, want)
		}
	}
}

func TestAMachineWithEverythingIsReady(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		report := check(everything(goos), goos)
		for _, item := range report.Items {
			if item.Status != Present {
				t.Errorf("%s: %s is %s (%s), want present", goos, item.ID, item.Status, item.Detail)
			}
		}
		if !report.Ready() || !report.ProviderSignedIn() {
			t.Errorf("%s: not ready: %v", goos, report.Missing())
		}
	}
	report := check(everything("windows"), "windows")
	for id, version := range map[string]string{"go": "1.25.5", "node": "22.11.0", "task": "3.40.0", "gh": "2.62.0", "claude": "2.0.14", "copilot": "1.0.105", "webview2": "131.0.2903.70"} {
		if got := byID(t, report, id).Version; got != version {
			t.Errorf("%s version = %q, want %q", id, got, version)
		}
	}
	if detail := byID(t, report, "gh").Detail; detail != "2.62.0, signed in" {
		t.Errorf("gh detail = %q", detail)
	}
}

func TestMissingItemsShowThePlatformsInstallCommand(t *testing.T) {
	cases := []struct {
		goos, id string
		want     Step
	}{
		{"darwin", "go", Step{Command: []string{"brew", "install", "go"}}},
		{"darwin", "task", Step{Command: []string{"brew", "install", "go-task"}}},
		{"darwin", "gh", Step{Command: []string{"brew", "install", "gh"}}},
		{"darwin", "xcode", Step{Command: []string{"xcode-select", "--install"}}},
		{"windows", "go", Step{Command: []string{"winget", "install", "--id", "GoLang.Go", "--exact", "--source", "winget"}}},
		{"windows", "node", Step{Command: []string{"winget", "install", "--id", "OpenJS.NodeJS.LTS", "--exact", "--source", "winget"}}},
		{"windows", "webview2", Step{Command: []string{"winget", "install", "--id", "Microsoft.EdgeWebView2Runtime", "--exact", "--source", "winget"}}},
		{"linux", "git", Step{Command: []string{"sudo", "apt-get", "install", "-y", "git"}, Admin: true}},
		{"linux", "gtk", Step{Command: []string{"sudo", "apt-get", "install", "-y", "pkg-config", "libgtk-3-dev"}, Admin: true}},
		{"linux", "webkitgtk", Step{Command: []string{"sudo", "apt-get", "install", "-y", "libwebkit2gtk-4.1-dev"}, Admin: true}},
		{"linux", "cc", Step{Command: []string{"sudo", "apt-get", "install", "-y", "build-essential"}, Admin: true}},
		// Go is not installed yet, so installing Task with it waits.
		{"linux", "task", Step{Command: []string{"go", "install", "github.com/go-task/task/v3/cmd/task@latest"}, Note: "This puts task in $(go env GOPATH)/bin, which must be on PATH.", Waits: "Go"}},
		{"linux", "go", Step{Note: "Install Go 1.25 or later from https://go.dev/doc/install; distribution packages are often older."}},
		// npm comes with Node.js, which is not installed yet either.
		{"darwin", "copilot", Step{Command: []string{"npm", "install", "-g", "@github/copilot"}, Waits: "npm (it comes with Node.js)"}},
		{"windows", "codex", Step{Command: []string{"npm", "install", "-g", "@openai/codex"}, Waits: "npm (it comes with Node.js)"}},
		{"linux", "claude", Step{Command: []string{"npm", "install", "-g", "@anthropic-ai/claude-code"}, Waits: "npm (it comes with Node.js)"}},
	}
	reports := map[string]Report{}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		reports[goos] = check(nothing(goos), goos)
	}
	for _, test := range cases {
		item := byID(t, reports[test.goos], test.id)
		if item.Status != Missing {
			t.Errorf("%s %s status = %s, want missing", test.goos, test.id, item.Status)
		}
		if len(item.Install) != 1 {
			t.Errorf("%s %s install = %+v, want one step", test.goos, test.id, item.Install)
			continue
		}
		got := item.Install[0]
		got.needs = ""
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s %s step = %+v, want %+v", test.goos, test.id, got, test.want)
		}
	}
	missing := reports["darwin"].Missing()
	if want := "a signed-in provider CLI: Codex CLI, Claude Code, or GitHub Copilot CLI"; missing[len(missing)-1] != want {
		t.Errorf("last missing = %q, want %q", missing[len(missing)-1], want)
	}
}

func TestOnlySudoCommandsAreAdminAndNoneIsRunnable(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, item := range check(nothing(goos), goos).Items {
			for _, step := range item.Install {
				usesSudo := len(step.Command) > 0 && step.Command[0] == "sudo"
				if usesSudo != step.Admin {
					t.Errorf("%s %s: admin = %v for %v", goos, item.ID, step.Admin, step.Command)
				}
				if step.Admin && step.Runnable() {
					t.Errorf("%s %s: an admin step is runnable", goos, item.ID)
				}
			}
		}
	}
}

func TestAStepWaitsForAMissingPackageManager(t *testing.T) {
	fake := newFake() // no Homebrew
	step := byID(t, check(fake, "darwin"), "go").Install[0]
	if step.Waits != "Homebrew (https://brew.sh)" || step.Runnable() {
		t.Fatalf("step = %+v, want it to wait for Homebrew", step)
	}
	fake = newFake() // no winget
	step = byID(t, check(fake, "windows"), "git").Install[0]
	if !strings.HasPrefix(step.Waits, "winget") || step.Runnable() {
		t.Fatalf("step = %+v, want it to wait for winget", step)
	}
}

func TestOtherLinuxPackageManagersGetTheirOwnCommands(t *testing.T) {
	dnf := nothing("linux")
	dnf.programs["apt-get"] = false
	dnf.install("dnf", nil)
	report := check(dnf, "linux")
	if got := byID(t, report, "webkitgtk").Install[0].Command; !reflect.DeepEqual(got, []string{"sudo", "dnf", "install", "-y", "webkit2gtk4.1-devel"}) {
		t.Errorf("dnf webkit = %v", got)
	}
	if got := byID(t, report, "gh").Install[0].Command; !reflect.DeepEqual(got, []string{"sudo", "dnf", "install", "-y", "gh"}) {
		t.Errorf("dnf gh = %v", got)
	}
	pacman := nothing("linux")
	pacman.programs["apt-get"] = false
	pacman.install("pacman", nil)
	if got := byID(t, check(pacman, "linux"), "gtk").Install[0].Command; !reflect.DeepEqual(got, []string{"sudo", "pacman", "-S", "--needed", "pkgconf", "gtk3"}) {
		t.Errorf("pacman gtk = %v", got)
	}
	unknown := nothing("linux")
	unknown.programs["apt-get"] = false
	step := byID(t, check(unknown, "linux"), "gtk").Install[0]
	if len(step.Command) != 0 || !strings.Contains(step.Note, "package manager") {
		t.Errorf("unknown distribution gtk = %+v, want a note", step)
	}
}

func TestOldVersionsAreTooOld(t *testing.T) {
	cases := map[string][2]string{
		"go":   {"go version", "go version go1.22.1 linux/amd64"},
		"node": {"node --version", "v20.11.0"},
		"task": {"task --version", "Task version: v2.8.1"},
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		fake := everything(goos)
		for _, answer := range cases {
			fake.outputs[answer[0]] = answer[1]
		}
		report := check(fake, goos)
		for id := range cases {
			if item := byID(t, report, id); item.Status != TooOld {
				t.Errorf("%s %s = %s (%s), want too old", goos, id, item.Status, item.Detail)
			}
		}
		missing := report.Missing()
		want := []string{"Go 1.25 or later (found 1.22.1)", "Node.js 22 or later (found 20.11.0)", "Task 3 or later (found 2.8.1)"}
		if !reflect.DeepEqual(missing, want) {
			t.Errorf("%s missing = %v, want %v", goos, missing, want)
		}
		if remedy := byID(t, report, "go").Remedy(); remedy == "" {
			t.Errorf("%s: a too-old Go has no remedy", goos)
		}
	}
}

func TestAnUnreadableVersionIsAnError(t *testing.T) {
	fake := everything("darwin")
	fake.outputs["go version"] = "garbled"
	item := byID(t, check(fake, "darwin"), "go")
	if item.Status != Broken || !strings.Contains(item.Detail, "garbled") {
		t.Fatalf("go = %+v, want an error naming the output", item)
	}
	delete(fake.outputs, "node --version")
	fake.failures["node --version"] = "dyld: library not loaded"
	item = byID(t, check(fake, "darwin"), "node")
	if item.Status != Broken || !strings.Contains(item.Detail, "dyld") {
		t.Fatalf("node = %+v, want an error naming the failure", item)
	}
}

func TestSignedOutCLIsOfferTheirSignIn(t *testing.T) {
	fake := everything("linux")
	delete(fake.outputs, "gh auth status")
	fake.failures["gh auth status"] = "You are not logged into any GitHub hosts."
	delete(fake.outputs, "codex login status")
	fake.outputs["claude auth status"] = `{"loggedIn": false}`
	report := check(fake, "linux")
	for id, signIn := range map[string][]string{"gh": {"gh", "auth", "login"}, "codex": {"codex", "login"}, "claude": {"claude", "auth", "login"}} {
		item := byID(t, report, id)
		if item.Status != SignedOut || !reflect.DeepEqual(item.SignIn, signIn) {
			t.Errorf("%s = %+v, want signed out with %v", id, item, signIn)
		}
	}
	if !strings.Contains(byID(t, report, "gh").Detail, "not logged into") {
		t.Errorf("gh detail = %q", byID(t, report, "gh").Detail)
	}
	if remedy := byID(t, report, "gh").Remedy(); remedy != "sign in with gh auth login" {
		t.Errorf("gh remedy = %q", remedy)
	}
	// Copilot cannot say whether it is signed in, so it keeps a provider
	// usable, but none is verified. GitHub CLI is required either way.
	if !report.ProviderUsable() || report.ProviderSignedIn() {
		t.Errorf("usable = %v, signed in = %v", report.ProviderUsable(), report.ProviderSignedIn())
	}
	if want := []string{"GitHub CLI, signed in"}; !reflect.DeepEqual(report.Missing(), want) {
		t.Errorf("missing = %v, want %v", report.Missing(), want)
	}
}

func TestAProviderMustBeInstalledAndSignedIn(t *testing.T) {
	fake := everything("darwin")
	fake.programs["copilot"] = false
	fake.programs["codex"] = false
	fake.outputs["claude auth status"] = `{"loggedIn": false}`
	report := check(fake, "darwin")
	if report.Ready() || report.ProviderUsable() {
		t.Fatalf("a signed-out Claude Code alone must not be enough: %v", report.Missing())
	}
	if got := report.Missing(); !reflect.DeepEqual(got, []string{"a signed-in provider CLI: Codex CLI, Claude Code, or GitHub Copilot CLI"}) {
		t.Fatalf("missing = %v", got)
	}
	fake.outputs["claude auth status"] = signedInClaude
	if report := check(fake, "darwin"); !report.Ready() || !report.ProviderSignedIn() {
		t.Fatalf("a signed-in Claude Code is enough: %v", report.Missing())
	}
}

func TestConfiguredBinariesAreTheOnesChecked(t *testing.T) {
	fake := everything("darwin")
	fake.programs["gh"] = false
	fake.install("/opt/gh", map[string]string{"/opt/gh --version": "gh version 2.62.0", "/opt/gh auth status": "Logged in"})
	report := Check(context.Background(), fake, Options{OS: "darwin", Binaries: config.Binaries{GitHub: "/opt/gh"}})
	item := byID(t, report, "gh")
	if item.Status != Present || !reflect.DeepEqual(item.SignIn, []string{"/opt/gh", "auth", "login"}) {
		t.Fatalf("gh = %+v", item)
	}
}

func TestWebView2IsReadFromEachRegistryLocation(t *testing.T) {
	fake := everything("windows")
	delete(fake.outputs, "reg query "+webView2Keys[0]+" /v pv")
	// An uninstalled runtime can leave a 0.0.0.0 version behind.
	fake.outputs["reg query "+webView2Keys[1]+" /v pv"] = "    pv    REG_SZ    0.0.0.0\r\n"
	fake.outputs["reg query "+webView2Keys[2]+" /v pv"] = "    pv    REG_SZ    130.0.1\r\n"
	if item := byID(t, check(fake, "windows"), "webview2"); item.Status != Present || item.Version != "130.0.1" {
		t.Fatalf("webview2 = %+v, want the per-user install", item)
	}
	delete(fake.outputs, "reg query "+webView2Keys[2]+" /v pv")
	if item := byID(t, check(fake, "windows"), "webview2"); item.Status != Missing {
		t.Fatalf("webview2 = %+v, want missing", item)
	}
}

func TestLinuxLibrariesMissingFromPkgConfigAreMissing(t *testing.T) {
	fake := everything("linux")
	delete(fake.outputs, "pkg-config --modversion webkit2gtk-4.1")
	if item := byID(t, check(fake, "linux"), "webkitgtk"); item.Status != Missing {
		t.Fatalf("webkitgtk = %+v, want missing", item)
	}
	fake.programs["pkg-config"] = false
	item := byID(t, check(fake, "linux"), "gtk")
	if item.Status != Missing || !strings.Contains(item.Detail, "pkg-config") {
		t.Fatalf("gtk = %+v, want missing because pkg-config is", item)
	}
}

func TestChecksNeverRunAnInstallOrSignIn(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, fake := range []*fakeProbe{nothing(goos), everything(goos)} {
			check(fake, goos)
			for _, line := range fake.ran {
				if strings.Contains(line, "install") || strings.HasSuffix(line, "login") {
					t.Errorf("%s: a check ran %q", goos, line)
				}
			}
		}
	}
}

func TestGoMinimumFollowsGoMod(t *testing.T) {
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	directive := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(data)
	if directive == nil {
		t.Fatal("go.mod has no go directive")
	}
	if compareVersions(string(directive[1]), GoMinimum) != 0 {
		t.Fatalf("GoMinimum = %s, go.mod says %s", GoMinimum, directive[1])
	}
	if got := GoDirective(data); got != string(directive[1]) {
		t.Fatalf("GoDirective = %q, want %s", got, directive[1])
	}
}

func TestGoDirective(t *testing.T) {
	for goMod, want := range map[string]string{
		"module example.com/a\n\ngo 1.26.1\n\nrequire x v1\n": "1.26.1",
		"module example.com/a\r\n\r\ngo 1.27\r\n":             "1.27",
		"module example.com/a\n":                              "",
		"module example.com/a\n\ntoolchain go1.26\n":          "",
		"module example.com/a\n\ngo 1.26.1 // the minimum\n":  "1.26.1",
		"module example.com/a\n\ngo\t1.26.2\n":                "1.26.2",
	} {
		if got := GoDirective([]byte(goMod)); got != want {
			t.Errorf("GoDirective(%q) = %q, want %q", goMod, got, want)
		}
	}
}

// inkunactl update checks the Go the commit it builds needs, which can be
// newer than this build's GoMinimum, never older.
func TestTheSourcesGoMinimumRaisesTheCheck(t *testing.T) {
	check := func(minimum string) Item {
		return byID(t, Check(context.Background(), everything("linux"), Options{OS: "linux", GoMinimum: minimum}), "go")
	}
	if item := check("1.26"); item.Status != TooOld || item.Minimum != "1.26" || !strings.Contains(item.Remedy(), "Install Go 1.26 or later") {
		t.Fatalf("Go 1.25.5 for a source that needs 1.26: %+v, remedy %q", item, item.Remedy())
	}
	if item := check("1.21"); item.Status != Present || item.Minimum != GoMinimum {
		t.Fatalf("an older source minimum lowered the check: %+v", item)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"1.25.0", "1.25", 0}, {"1.25.5", "1.25", 1}, {"1.9", "1.25", -1}, {"22", "22.0.0", 0}, {"24.4.1", "22", 1}, {"2.8.1", "3", -1},
	} {
		if got := compareVersions(test.left, test.right); got != test.want {
			t.Errorf("compareVersions(%s, %s) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestCommandLineQuotesArgumentsWithSpaces(t *testing.T) {
	if got := CommandLine([]string{"npm", "install", "-g", "a b"}); got != `npm install -g "a b"` {
		t.Fatalf("CommandLine = %s", got)
	}
}

func TestAWrapperRuntimeIsCheckedWithItsVersionCommand(t *testing.T) {
	fake := everything("darwin")
	fake.programs["claude"] = false
	fake.install("claude-wrapper", map[string]string{
		"claude-wrapper version --short": "2.0.14",
		"claude-wrapper auth status":     signedInClaude,
	})
	configuration := config.Config{
		Binaries: config.Binaries{Claude: "claude-wrapper"},
		Runtimes: []agentruntime.Spec{
			{Name: domain.RuntimeClaude, Binary: "claude-wrapper", VersionArgs: []string{"version", "--short"}},
			// A declared runtime that is not built in is Doctor's to check.
			{Name: "claude-work", VersionArgs: []string{"--ver"}},
		},
	}
	options := OptionsFor(configuration)
	if !reflect.DeepEqual(options.VersionArgs, map[string][]string{"claude": {"version", "--short"}}) {
		t.Fatalf("version args = %v", options.VersionArgs)
	}
	options.OS = "darwin"
	if item := byID(t, Check(context.Background(), fake, options), "claude"); item.Status != Present || item.Version != "2.0.14" {
		t.Fatalf("claude = %+v, want the wrapper's version command used", item)
	}
}
