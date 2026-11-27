//go:build !windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Configure starts the command in a process group of its own, so Terminate
// can signal everything it starts.
func Configure(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Terminate asks the command's process group to stop. A process that has
// already ended is not an error.
func Terminate(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// Kill ends the command's process group at once, for one that did not stop
// when asked. A process that has already ended is not an error.
func Kill(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// Alive says whether a process is still in the command's process group,
// which outlives the command when a process it started kept running.
func Alive(command *exec.Cmd) bool {
	if command.Process == nil {
		return false
	}
	return syscall.Kill(-command.Process.Pid, 0) == nil
}
