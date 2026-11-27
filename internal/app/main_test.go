package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// failSSH and hangSSH are the arguments that make the test binary stand in
// for ssh: one fails at once, the other waits as an ssh does on a locked
// agent, long past any fetch timeout a test sets.
const (
	failSSH = "--inkuna-fail-ssh"
	hangSSH = "--inkuna-hang-ssh"
)

// leftOverWorker makes the test binary stand in for a worker a crashed
// Inkuna left running: it runs until it is ended. It is the test binary
// rather than a shell's sleep, since macOS hides the environment, and with
// it the mark, of its own system binaries.
const leftOverWorker = "--inkuna-left-over-worker"

// sshHang is how long the hanging ssh waits: past settleTimeout, so a run
// that waited on it never settles, however slow the machine.
const sshHang = 2 * settleTimeout

// TestMain keeps git off the network. Tests give their repositories GitHub
// origins, such as git@github.com:Acme/App.git, so origin parses as GitHub,
// and starting a run fetches its base from origin. Over SSH that fetch
// waited on the machine's own agent: a locked or prompting agent held the
// run in "running" until the test gave up. Git runs this binary as its SSH
// command instead, which fails at once, so every fetch fails as it does
// offline, which runs already handle. The binary is the helper so nothing
// else need be on PATH, on any platform; the marker is an argument rather
// than a variable because tests run this binary as other helpers too.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case failSSH:
			fmt.Fprintln(os.Stderr, "ssh is disabled in the internal/app tests")
			os.Exit(255)
		case hangSSH:
			time.Sleep(sshHang)
			os.Exit(255)
		case leftOverWorker:
			time.Sleep(time.Hour)
			os.Exit(0)
		}
	}
	command, err := sshCommand(failSSH)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for name, value := range map[string]string{"GIT_SSH_COMMAND": command, "GIT_TERMINAL_PROMPT": "0"} {
		if err := os.Setenv(name, value); err != nil {
			fmt.Fprintln(os.Stderr, "set", name+":", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// sshCommand is a GIT_SSH_COMMAND that runs the test binary with marker.
// Git runs it through its shell, Git for Windows' included, so the path is
// quoted for sh and written with forward slashes.
func sshCommand(marker string) (string, error) {
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		return "", fmt.Errorf("find the test binary for GIT_SSH_COMMAND: %w", err)
	}
	return "'" + strings.ReplaceAll(filepath.ToSlash(binary), "'", `'\''`) + "' " + marker, nil
}
