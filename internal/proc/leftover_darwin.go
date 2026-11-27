package proc

import (
	"bytes"
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

// markedProcesses lists this user's processes whose environment holds
// entry. One that ended while it was read, or that macOS will not show, is
// skipped.
func markedProcesses(entry string) ([]marked, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	want := []byte(entry)
	found := make([]marked, 0)
	for _, process := range processes {
		pid := int(process.Proc.P_pid)
		if variables, readable := environmentOf(pid); !readable || !holds(variables, want) {
			continue
		}
		found = append(found, marked{pid: pid, pgid: int(process.Eproc.Pgid)})
	}
	return found, nil
}

// environmentOf reads a process's environment out of kern.procargs2: the
// argument count, the executable's path, NUL padding, the arguments, and
// then the environment, each entry ending in a NUL, up to an empty one.
// macOS shows its own system binaries' arguments without their
// environment, and a process of another user not at all.
func environmentOf(pid int) ([][]byte, bool) {
	procargs, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(procargs) < 4 {
		return nil, false
	}
	argc := int(binary.LittleEndian.Uint32(procargs))
	rest := procargs[4:]
	path := bytes.IndexByte(rest, 0)
	if path < 0 {
		return nil, false
	}
	fields := bytes.Split(bytes.TrimLeft(rest[path:], "\x00"), []byte{0})
	variables := make([][]byte, 0, 32)
	if len(fields) <= argc {
		return variables, true
	}
	for _, field := range fields[argc:] {
		if len(field) == 0 {
			break
		}
		variables = append(variables, field)
	}
	return variables, true
}
