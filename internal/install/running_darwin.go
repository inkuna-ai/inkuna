package install

import (
	"bytes"
	"os"

	"golang.org/x/sys/unix"
)

// processExecutables are the executables of the user's own processes: the
// path each was started from, which kern.procargs2 gives after argc.
func processExecutables() ([]string, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	var executables []string
	for _, process := range processes {
		arguments, err := unix.SysctlRaw("kern.procargs2", int(process.Proc.P_pid))
		if err != nil || len(arguments) < 5 {
			// The process ended, or is a zombie with no arguments left.
			continue
		}
		path, _, _ := bytes.Cut(arguments[4:], []byte{0})
		if len(path) > 0 {
			executables = append(executables, string(path))
		}
	}
	return executables, nil
}
