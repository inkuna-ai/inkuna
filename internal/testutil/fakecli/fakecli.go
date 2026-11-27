// Package fakecli writes shell scripts that tests run in place of a CLI,
// such as Claude Code or Codex, on every platform. On macOS and Linux the
// script is the program. Windows cannot run a script as a program, so there
// the script is run by the shell Git for Windows ships, which Inkuna
// needs anyway, through a small shim program built once per test binary.
package fakecli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Write writes script as a program at path and returns the path to run,
// which on Windows is path with .exe added.
func Write(t testing.TB, path, script string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	shim, shell, err := built()
	// Only a machine without the shell skips: a shim that would not build
	// is a failure, or every fake CLI's coverage would vanish unnoticed.
	if errors.Is(err, errNoShell) {
		t.Skipf("no shell to run a fake CLI with on Windows: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return writeShimmed(t, path, script, shim, shell)
}

// writeShimmed writes script to be run through the shim with shell, and
// returns the shim's copy to run.
func writeShimmed(t testing.TB, path, script, shim, shell string) string {
	t.Helper()
	program := path + ".exe"
	// A test may write a fake at the same path again for another case.
	if err := os.Remove(program); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := copyFile(shim, program); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program+".sh", []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program+".shell", []byte(shell), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program+".run.sh", []byte(runner), 0o600); err != nil {
		t.Fatal(err)
	}
	return program
}

// runner is what the shim's shell runs: it sets the arguments the shim
// wrote, NUL-separated, and sources the script, as if the script had been
// run with them. Git for Windows' sh is bash, whose read takes an empty
// delimiter for NUL.
const runner = `set --
while IFS= read -r -d '' argument; do set -- "$@" "$argument"; done < "$FAKECLI_ARGS"
. "$FAKECLI_SCRIPT"
`

// errNoShell is why fakes cannot run on a machine without Git for Windows'
// sh, the one reason Write skips rather than fails.
var errNoShell = errors.New("no sh")

var (
	once                sync.Once
	shimPath, shellPath string
	buildErr            error
)

// built builds the shim and finds the shell, once.
func built() (string, string, error) {
	once.Do(func() {
		shellPath, buildErr = findShell()
		if buildErr != nil {
			return
		}
		directory, err := os.MkdirTemp("", "fakecli")
		if err != nil {
			buildErr = fmt.Errorf("make a directory for the shim: %w", err)
			return
		}
		shimPath = filepath.Join(directory, "shim.exe")
		command := exec.Command("go", "build", "-o", shimPath, "github.com/inkuna-ai/inkuna/internal/testutil/fakecli/shim")
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		if err := command.Run(); err != nil {
			buildErr = fmt.Errorf("build the shim: %w: %s", err, output.String())
		}
	})
	return shimPath, shellPath, buildErr
}

// findShell finds Git for Windows' sh: on PATH, or beside git, whose
// exec path is <Git>/mingw64/libexec/git-core and whose sh is <Git>/bin/sh.exe.
func findShell() (string, error) {
	if shell, err := exec.LookPath("sh"); err == nil {
		return shell, nil
	}
	output, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		return "", fmt.Errorf("%w: sh is not on PATH and git was not found: %w", errNoShell, err)
	}
	root := filepath.Clean(filepath.Join(strings.TrimSpace(string(output)), "..", "..", ".."))
	shell := filepath.Join(root, "bin", "sh.exe")
	if _, err := os.Stat(shell); err != nil {
		return "", fmt.Errorf("%w: sh is not on PATH or at %s", errNoShell, shell)
	}
	return shell, nil
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return err
	}
	return target.Close()
}
