package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// machine fakes the programs on PATH and what each command line prints. A
// command line without an answer fails.
type machine struct {
	mu       sync.Mutex
	programs map[string]bool
	outputs  map[string]string
}

func (fake *machine) LookPath(name string) (string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.programs[name] {
		return name, nil
	}
	return "", errors.New("not found")
}

func (fake *machine) Output(_ context.Context, path string, args ...string) ([]byte, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if output, ok := fake.outputs[strings.Join(append([]string{path}, args...), " ")]; ok {
		return []byte(output), nil
	}
	return nil, errors.New("exit status 1")
}

func (fake *machine) add(program string, outputs ...string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.programs[program] = true
	for index := 0; index+1 < len(outputs); index += 2 {
		fake.outputs[outputs[index]] = outputs[index+1]
	}
}

// readyMac is a Mac with everything Inkuna needs, Claude Code as its
// provider.
func readyMac() *machine {
	fake := &machine{programs: map[string]bool{}, outputs: map[string]string{}}
	fake.add("brew")
	fake.add("xcode-select", "xcode-select -p", "/Library/Developer/CommandLineTools")
	fake.add("git", "git --version", "git version 2.50.1")
	fake.add("go", "go version", "go version go1.25.5 darwin/arm64")
	fake.add("node", "node --version", "v22.11.0")
	fake.add("npm", "npm --version", "10.9.0")
	fake.add("task", "task --version", "3.40.0")
	fake.add("gh", "gh --version", "gh version 2.62.0", "gh auth status", "Logged in")
	fake.add("claude", "claude --version", "2.0.14 (Claude Code)", "claude auth status", `{"loggedIn": true}`)
	return fake
}

type wizardRun struct {
	err    error
	output string
	ran    [][]string
	dirs   []string
}

// runWizard runs the wizard on fake with the answers typed in, and calls
// effect for each command it runs, to change the machine as the command
// would. Inkuna is already installed from the clone the wizard runs in, so
// the steps after the prerequisites have nothing to do.
func runWizard(t *testing.T, fake *machine, goos, answers string, effect func([]string), args ...string) wizardRun {
	t.Helper()
	return newRig(t, goos).installed().wizard(fake, answers, effect, args...)
}

func exitCode(err error) int {
	var failure exitError
	if errors.As(err, &failure) {
		return failure.code
	}
	if err != nil {
		return 1
	}
	return 0
}

func TestInstallCheckReportsWithoutAskingOrRunning(t *testing.T) {
	fake := readyMac()
	fake.programs["go"] = false
	got := runWizard(t, fake, "darwin", "y\ny\ny\n", nil, "--check")
	if exitCode(got.err) != 1 || len(got.ran) != 0 {
		t.Fatalf("exit %d, ran %v; want 1 and nothing run", exitCode(got.err), got.ran)
	}
	for _, want := range []string{"missing     Go: needs 1.25 or later", "install with brew install go", "Still missing:\n  - Go 1.25 or later"} {
		if !strings.Contains(got.output, want) {
			t.Errorf("output lacks %q:\n%s", want, got.output)
		}
	}
	if strings.Contains(got.output, "[y/N]") {
		t.Errorf("--check asked a question:\n%s", got.output)
	}
	if got := runWizard(t, readyMac(), "darwin", "", nil, "--check"); got.err != nil || !strings.Contains(got.output, "present     Go: 1.25.5") {
		t.Fatalf("a ready machine: err %v\n%s", got.err, got.output)
	}
}

func TestInstallRunsNothingWithoutAYes(t *testing.T) {
	fake := readyMac()
	fake.programs["go"] = false
	fake.programs["task"] = false
	delete(fake.outputs, "gh auth status")
	for _, answers := range []string{"", "n\nno\nn\n", "maybe\n\n\n"} {
		got := runWizard(t, fake, "darwin", answers, nil)
		if len(got.ran) != 0 {
			t.Fatalf("answers %q ran %v", answers, got.ran)
		}
		if exitCode(got.err) != 1 || !strings.Contains(got.output, "run inkunactl install again") {
			t.Fatalf("answers %q: exit %d\n%s", answers, exitCode(got.err), got.output)
		}
	}
}

func TestInstallRunsAUserLevelInstallOnYesAndChecksAgain(t *testing.T) {
	fake := readyMac()
	fake.programs["go"] = false
	got := runWizard(t, fake, "darwin", "y\n", func(argv []string) {
		if reflect.DeepEqual(argv, []string{"brew", "install", "go"}) {
			fake.add("go")
		}
	})
	if !reflect.DeepEqual(got.ran, [][]string{{"brew", "install", "go"}}) {
		t.Fatalf("ran %v", got.ran)
	}
	if got.err != nil || !strings.Contains(got.output, "Install Go with brew install go? [y/N]") ||
		!strings.Contains(got.output, "Checking again.") || !strings.Contains(got.output, "Everything Inkuna needs is installed.\n") {
		t.Fatalf("err %v\n%s", got.err, got.output)
	}
}

func TestInstallNeverRunsSudo(t *testing.T) {
	fake := &machine{programs: map[string]bool{}, outputs: map[string]string{}}
	fake.add("apt-get")
	fake.add("pkg-config")
	got := runWizard(t, fake, "linux", strings.Repeat("y\n", 20), nil)
	for _, argv := range got.ran {
		if argv[0] == "sudo" {
			t.Fatalf("ran %v", argv)
		}
	}
	for _, want := range []string{
		"This needs administrator rights, so run it yourself:\n    sudo apt-get install -y libwebkit2gtk-4.1-dev",
		"Install Go 1.25 or later from https://go.dev/doc/install",
		// Task installs with Go, which is not there yet.
		"It installs with go install github.com/go-task/task/v3/cmd/task@latest, once Go is installed.",
		"It installs with npm install -g @openai/codex, once npm (it comes with Node.js) is installed.",
	} {
		if !strings.Contains(got.output, want) {
			t.Errorf("output lacks %q:\n%s", want, got.output)
		}
	}
	if exitCode(got.err) != 1 {
		t.Fatalf("exit %d, want 1", exitCode(got.err))
	}
}

func TestInstallOffersSignInAndChecksAgain(t *testing.T) {
	fake := readyMac()
	delete(fake.outputs, "gh auth status")
	got := runWizard(t, fake, "darwin", "y\n", func(argv []string) {
		if reflect.DeepEqual(argv, []string{"gh", "auth", "login"}) {
			fake.add("gh", "gh auth status", "Logged in")
		}
	})
	if !reflect.DeepEqual(got.ran, [][]string{{"gh", "auth", "login"}}) || got.err != nil {
		t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
	if !strings.Contains(got.output, "Sign in to GitHub CLI with gh auth login? [y/N]") {
		t.Fatalf("output:\n%s", got.output)
	}
}

func TestInstallOffersProvidersOnlyWhenNoneIsUsable(t *testing.T) {
	fake := readyMac()
	got := runWizard(t, fake, "darwin", "y\ny\ny\n", nil)
	if len(got.ran) != 0 || got.err != nil || strings.Contains(got.output, "[y/N]") {
		t.Fatalf("with Claude Code signed in, ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
	fake.programs["claude"] = false
	got = runWizard(t, fake, "darwin", "n\ny\nn\n", func(argv []string) {
		if reflect.DeepEqual(argv, []string{"npm", "install", "-g", "@anthropic-ai/claude-code"}) {
			fake.add("claude")
		}
	})
	if !reflect.DeepEqual(got.ran, [][]string{{"npm", "install", "-g", "@anthropic-ai/claude-code"}}) || got.err != nil {
		t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
	for _, name := range []string{"Install Codex CLI", "Install Claude Code", "Install GitHub Copilot CLI"} {
		if !strings.Contains(got.output, name) {
			t.Errorf("output lacks %q:\n%s", name, got.output)
		}
	}
}

func TestInstallOffersCopilotSignInWhenItIsTheOnlyProvider(t *testing.T) {
	fake := readyMac()
	fake.programs["claude"] = false
	fake.add("copilot", "copilot --version", "GitHub Copilot CLI 1.0.105.")
	got := runWizard(t, fake, "darwin", "y\n", nil)
	if !reflect.DeepEqual(got.ran, [][]string{{"copilot", "login"}}) || got.err != nil {
		t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
	if !strings.Contains(got.output, "GitHub Copilot CLI cannot report whether it is signed in.") {
		t.Fatalf("output:\n%s", got.output)
	}
}

func TestInstallRejectsUnknownArguments(t *testing.T) {
	if got := runWizard(t, readyMac(), "darwin", "", nil, "--force"); got.err == nil || got.err.Error() != installUsage {
		t.Fatalf("err = %v", got.err)
	}
}
