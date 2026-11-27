//go:build !windows

package proc

import (
	"bytes"
	"errors"
	"os"
	"syscall"
	"time"
)

// marked is a process found carrying a mark.
type marked struct {
	pid  int
	pgid int
}

// EndLeftOver ends every process of this user marked with instance ID id,
// with the process group of each, and says how many it found. Only one
// Inkuna uses a data directory at a time, so one that holds the
// directory's lock and has started nothing yet calls it to end what an
// earlier one left running when it crashed: its CLIs would keep working,
// and billing, beside the workers a resumed run starts. It asks first, and
// kills what is still running after grace.
func EndLeftOver(id string, grace time.Duration) (int, error) {
	if id == "" {
		return 0, nil
	}
	entry := []byte(Mark(id))
	targets, err := findLeftOver(entry)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}
	groups := leftOverGroups(targets, entry)
	for _, process := range targets {
		_ = signalProcess(process.pid, syscall.SIGTERM)
	}
	for _, group := range groups {
		_ = signalProcess(-group, syscall.SIGTERM)
	}
	deadline := time.Now().Add(grace)
	for anyAlive(targets, groups) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// A process that stopped when asked may have had its number reused by
	// now, so only what still carries the mark is killed, with what it
	// started while stopping, and the groups are judged again. A group with
	// a process still in it keeps its number, which no new process can take.
	still, err := findLeftOver(entry)
	if err != nil {
		return len(targets), err
	}
	for _, process := range still {
		_ = signalProcess(process.pid, syscall.SIGKILL)
	}
	for _, group := range leftOverGroups(append(still, targets...), entry) {
		_ = signalProcess(-group, syscall.SIGKILL)
	}
	return len(targets), nil
}

// findLeftOver lists the processes carrying entry, but for this one and its
// group: those carry the mark only when an Inkuna on this directory started
// this one, and are not left over.
func findLeftOver(entry []byte) ([]marked, error) {
	found, err := markedProcesses(string(entry))
	if err != nil {
		return nil, err
	}
	self, group := os.Getpid(), syscall.Getpgrp()
	targets := make([]marked, 0, len(found))
	for _, process := range found {
		if process.pid <= 1 || process.pid == self || process.pgid == group {
			continue
		}
		targets = append(targets, process)
	}
	return targets, nil
}

// leftOverGroups is the process groups of targets that are left over too.
// Every worker and check leads a group of its own, and what it starts that
// drops the mark from its environment stays in it, so a marked process's
// group is ended with it: one whose leader carries the mark, has ended, or
// hides its environment, as macOS's own binaries do (a check run by
// /usr/bin/make leads a group of marked go processes). A group whose leader
// shows an environment without the mark is not one Inkuna started, and is
// left alone.
func leftOverGroups(targets []marked, entry []byte) []int {
	own := syscall.Getpgrp()
	seen := make(map[int]bool, len(targets))
	groups := make([]int, 0, len(targets))
	for _, process := range targets {
		group := process.pgid
		if group <= 1 || group == own || seen[group] {
			continue
		}
		seen[group] = true
		if exists(group) {
			if variables, readable := environmentOf(group); readable && len(variables) > 0 && !holds(variables, entry) {
				continue
			}
		}
		groups = append(groups, group)
	}
	return groups
}

func signalProcess(pid int, signal syscall.Signal) error {
	err := syscall.Kill(pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// exists says whether a process has number pid.
func exists(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func anyAlive(targets []marked, groups []int) bool {
	for _, process := range targets {
		if exists(process.pid) {
			return true
		}
	}
	for _, group := range groups {
		if exists(-group) {
			return true
		}
	}
	return false
}

// holds says whether variables holds entry exactly.
func holds(variables [][]byte, entry []byte) bool {
	for _, variable := range variables {
		if bytes.Equal(variable, entry) {
			return true
		}
	}
	return false
}
