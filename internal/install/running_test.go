package install

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const envRunningHelper = "INKUNA_TEST_RUNNING_HELPER"

// TestRunningHelper is the program the tests start: this test binary,
// copied, waiting to be stopped.
func TestRunningHelper(t *testing.T) {
	if os.Getenv(envRunningHelper) != "1" {
		return
	}
	time.Sleep(time.Minute)
}

// program copies this test binary to path, so a test can start it there.
func program(t *testing.T, path string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// start runs a copy of this test binary until the test ends.
func start(t *testing.T, path string) {
	t.Helper()
	command := exec.Command(path, "-test.run=^TestRunningHelper$")
	command.Env = append(os.Environ(), envRunningHelper+"=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
}

func TestRunningFindsTheProgramByFile(t *testing.T) {
	dir := t.TempDir()
	running := program(t, filepath.Join(dir, "running", "Inkuna"))
	idle := program(t, filepath.Join(dir, "idle", "Inkuna"))
	start(t, running)
	if got, err := Running(running); err != nil || !got {
		t.Fatalf("Running(%s) = %v, %v; want true", running, got, err)
	}
	if got, err := Running(idle); err != nil || got {
		t.Fatalf("Running(%s), a copy no one started, = %v, %v; want false", idle, got, err)
	}
	if got, err := Running(filepath.Join(dir, "missing")); err != nil || got {
		t.Fatalf("Running(a missing program) = %v, %v; want false", got, err)
	}
	// Another path to the same file is the same program.
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Dir(running), link); err != nil {
		t.Logf("symbolic links are not available: %v", err)
		return
	}
	if got, err := Running(filepath.Join(link, filepath.Base(running))); err != nil || !got {
		t.Fatalf("Running through a link = %v, %v; want true", got, err)
	}
}

// inkunactl update runs as the installed inkunactl, so it replaces a
// running program: Windows lets a running program be renamed, though not
// overwritten or deleted, and Place and SwapPrevious only rename.
func TestPlaceAndSwapReplaceARunningProgram(t *testing.T) {
	dir := t.TempDir()
	installed := program(t, filepath.Join(dir, "bin", "inkunactl"))
	start(t, installed)
	build := filepath.Join(dir, "build")
	writeFile(t, build, "new")
	if _, err := Place(build, installed); err != nil {
		t.Fatalf("replacing the running program: %v", err)
	}
	if got := readFile(t, installed); got != "new" {
		t.Fatalf("installed = %q", got)
	}
	// Back again, while the program that was replaced still runs.
	if err := SwapPrevious(installed); err != nil {
		t.Fatalf("swapping the running program back: %v", err)
	}
	if got := readFile(t, installed+PreviousSuffix); got != "new" {
		t.Fatalf("previous = %q", got)
	}
}

// macOS gives a program started as ./Inkuna by that relative path, whose
// directory it does not say. It counts by name, so the app is not replaced.
func TestRunningMatchesARelativePathByName(t *testing.T) {
	program := filepath.Join(t.TempDir(), "Inkuna.app", "Contents", "MacOS", "Inkuna")
	writeFile(t, program, "app")
	target, err := os.Stat(program)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		executables []string
		want        bool
	}{
		{[]string{"/usr/bin/login", "Inkuna"}, true},
		{[]string{"./Contents/MacOS/Inkuna"}, true},
		{[]string{"./inkunactl", "Inkuna-helper"}, false},
		{[]string{program}, true},
		{[]string{filepath.Join(filepath.Dir(program), "..", "MacOS", "Inkuna")}, true},
	} {
		if got := runningIn(test.executables, program, target); got != test.want {
			t.Errorf("runningIn(%q) = %v, want %v", test.executables, got, test.want)
		}
	}
}
