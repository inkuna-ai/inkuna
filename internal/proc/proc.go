// Package proc starts subprocesses that end together with every process
// they start. exec.CommandContext kills only the process it started, so a
// git fetch's remote helper, a check's test runner, or a CLI's workers
// would keep running after a timeout or a cancel, holding the working
// directory and the output pipe open.
package proc

import (
	"context"
	"os/exec"
	"time"
)

// WaitDelay bounds how long Wait waits for the output after the process
// has ended, should a descendant that escaped Terminate still hold it open.
const WaitDelay = 5 * time.Second

// Command is exec.CommandContext for a process that may start others: when
// ctx ends, Terminate ends the whole tree.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...)
	Configure(command)
	command.Cancel = func() error { return Terminate(command) }
	command.WaitDelay = WaitDelay
	return command
}
