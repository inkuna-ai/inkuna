//go:build !windows

package proc

import "os/exec"

// Start starts command. On Windows it also joins a job that ends with
// Inkuna; elsewhere, what a crash left running ends at the next start
// (EndLeftOver).
func Start(command *exec.Cmd) error {
	return command.Start()
}
