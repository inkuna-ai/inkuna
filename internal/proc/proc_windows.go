//go:build windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// Configure leaves the command as it is: Windows has no process group to
// signal, so Terminate ends the tree by parent.
func Configure(command *exec.Cmd) {}

// Terminate ends the command and every process it started. A process that
// has already ended is not an error.
func Terminate(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	// taskkill ships with every Windows. It is run from System32 by its full
	// path, so nothing earlier on PATH can stand in for it.
	taskkill := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
	if err := exec.Command(taskkill, "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run(); err == nil {
		return nil
	}
	// taskkill fails when the process has already ended, or when it cannot
	// run; the process itself is still ended.
	err := command.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

// Kill ends the command and every process it started at once. Terminate
// already does, with taskkill's /F.
func Kill(command *exec.Cmd) error {
	return Terminate(command)
}

// Alive says whether anything the command started may still be running.
// Terminate ends the whole tree at once on Windows, so nothing is.
func Alive(command *exec.Cmd) bool {
	return false
}
