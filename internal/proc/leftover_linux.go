package proc

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
)

// markedProcesses lists the processes whose environment holds entry. A
// process of another user, or one that ended while it was read, cannot be
// read and is skipped.
func markedProcesses(entry string) ([]marked, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	want := []byte(entry)
	found := make([]marked, 0)
	for _, dirEntry := range entries {
		pid, err := strconv.Atoi(dirEntry.Name())
		if err != nil {
			continue
		}
		if variables, readable := environmentOf(pid); !readable || !holds(variables, want) {
			continue
		}
		pgid, err := syscall.Getpgid(pid)
		if err != nil {
			continue
		}
		found = append(found, marked{pid: pid, pgid: pgid})
	}
	return found, nil
}

// environmentOf reads a process's environment, which a process of another
// user, or one that has ended, does not show. A zombie's is empty.
func environmentOf(pid int) ([][]byte, bool) {
	environ, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return nil, false
	}
	variables := make([][]byte, 0, 32)
	for _, variable := range bytes.Split(environ, []byte{0}) {
		if len(variable) > 0 {
			variables = append(variables, variable)
		}
	}
	return variables, true
}
