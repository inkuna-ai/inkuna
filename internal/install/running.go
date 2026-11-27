package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Running reports whether a process is running program, the app's
// executable (Layout.AppProgram). An update refuses to replace an app that
// is running: the swap's renames would succeed, but the running app would
// carry on as the old build beside a database the new one migrates, and on
// Windows the old build could not be removed until it quits.
//
// It lists the processes the user can see, through the operating system
// rather than a command: /proc on Linux, sysctl on macOS, and a Toolhelp
// snapshot on Windows. A process is running program when its executable is
// the same file, so a path through a symbolic link, or Windows' short
// names, still matches, or, for a relative path, when it has the same name.
// A program that is not there is not running.
func Running(program string) (bool, error) {
	target, err := os.Stat(program)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check %s: %w", program, err)
	}
	executables, err := processExecutables()
	if err != nil {
		return false, fmt.Errorf("list running programs: %w", err)
	}
	return runningIn(executables, program, target), nil
}

// runningIn reports whether one of the processes' executables is program,
// whose file is target.
func runningIn(executables []string, program string, target fs.FileInfo) bool {
	for _, executable := range executables {
		// macOS gives the path a program was started with, which is
		// relative when it was started as ./Inkuna, and relative to a
		// working directory it does not give. Such a process counts when
		// its name matches: a false match only stops an update, a miss
		// would replace a running app.
		if !filepath.IsAbs(executable) {
			if filepath.Base(executable) == filepath.Base(program) {
				return true
			}
			continue
		}
		// A process that ended, or one of another user's, cannot be
		// read, and is not this user's app.
		if info, err := os.Stat(executable); err == nil && os.SameFile(info, target) {
			return true
		}
	}
	return false
}
