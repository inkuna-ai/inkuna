package fakecli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The arguments a CLI is given hold JSON, spaces, empty strings, and
// newlines; each must reach the script as it was sent.
var arguments = []string{"--json-schema", `{"type":"object","note":"a \"quoted\" word"}`, "", "two words", "line one\nline two", `C:\Users\dev\file.json`, "--resume", "earlier"}

// script records each argument on its own NUL-ended line, keeps its input,
// and exits 3.
func script(directory string) string {
	return "#!/bin/sh\nfor argument in \"$@\"; do printf '%s\\0' \"$argument\" >> '" + filepath.Join(directory, "args") + "'; done\n" +
		"cat > '" + filepath.Join(directory, "stdin") + "'\necho done\nexit 3\n"
}

func check(t *testing.T, program, directory string) {
	t.Helper()
	command := exec.Command(program, arguments...)
	command.Stdin = strings.NewReader("the prompt")
	var stdout bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stdout
	err := command.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || strings.TrimSpace(stdout.String()) != "done" {
		t.Fatalf("run = %v, output %q; want exit 3 and done", err, stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(directory, "args"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"); !slices.Equal(got, arguments) {
		t.Fatalf("arguments = %q, want %q", got, arguments)
	}
	if stdin, err := os.ReadFile(filepath.Join(directory, "stdin")); err != nil || string(stdin) != "the prompt" {
		t.Fatalf("input = %q, %v", stdin, err)
	}
}

func TestAFakeCLIGetsItsArgumentsInputAndExitCode(t *testing.T) {
	directory := t.TempDir()
	check(t, Write(t, filepath.Join(directory, "fake"), script(directory)), directory)
}

// Windows runs a fake through the shim, which bash runs here as well, so
// the shim's way of passing arguments is checked on every platform.
func TestTheShimPassesArgumentsInputAndExitCodeThrough(t *testing.T) {
	if runtime.GOOS != "windows" {
		bash, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("no bash to run the shim's script with")
		}
		directory := t.TempDir()
		shim := filepath.Join(directory, "shim")
		build := exec.Command("go", "build", "-o", shim, "github.com/inkuna-ai/inkuna/internal/testutil/fakecli/shim")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build the shim: %v: %s", err, output)
		}
		check(t, writeShimmed(t, filepath.Join(directory, "fake"), script(directory), shim, bash), directory)
		return
	}
	directory := t.TempDir()
	check(t, Write(t, filepath.Join(directory, "fake"), script(directory)), directory)
}

// A fake written again at the same path replaces the first.
func TestAFakeCanBeWrittenAgainAtTheSamePath(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "fake")
	Write(t, path, "#!/bin/sh\necho first\n")
	program := Write(t, path, "#!/bin/sh\necho second\n")
	output, err := exec.Command(program).Output()
	if err != nil || strings.TrimSpace(string(output)) != "second" {
		t.Fatalf("output = %q, %v; want second", output, err)
	}
}

// A machine without sh is the only reason Write skips; findShell says so
// in a way Write can tell from a shim that failed to build.
func TestNoShellIsTheOneReasonToSkip(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := findShell(); !errors.Is(err, errNoShell) {
		t.Fatalf("findShell without sh or git = %v, want errNoShell", err)
	}
}
