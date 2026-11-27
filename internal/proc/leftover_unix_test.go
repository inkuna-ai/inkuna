//go:build linux || darwin

package proc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// worker is a stand-in for a process a crashed Inkuna left running.
type worker struct {
	pid   int
	ready string
	ended <-chan struct{}
}

// start starts command in a process group of its own, as Inkuna starts a
// worker, and waits for the test binary in it to be ready (leftOver in
// proc_test.go). Waiting on it in the background reaps it, as launchd or
// init would once its Inkuna was gone.
func start(t *testing.T, command *exec.Cmd, ready string) worker {
	t.Helper()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = Kill(command)
		<-done
	})
	readyPID(t, ready)
	return worker{pid: command.Process.Pid, ready: ready, ended: done}
}

// startMarked starts the test binary as a left-over worker marked with id,
// or unmarked for "", with the roles' variables in options.
func startMarked(t *testing.T, id string, options ...string) worker {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	command := Command(context.Background(), os.Args[0])
	command.Env = append(append(os.Environ(), "PROC_TEST_ROLE=leftover", "PROC_TEST_READY="+ready), options...)
	if id != "" {
		command.Env = append(command.Env, Mark(id))
	}
	return start(t, command, ready)
}

// readyPID waits for the process writing ready to be ready, and returns
// its process ID.
func readyPID(t *testing.T, ready string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		text, _ := os.ReadFile(ready)
		if pid, found := strings.CutPrefix(strings.TrimSpace(string(text)), "ready "); found {
			number, err := strconv.Atoi(pid)
			if err != nil {
				t.Fatal(err)
			}
			return number
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func uniqueID() string {
	return fmt.Sprintf("test-%d-%d", os.Getpid(), time.Now().UnixNano())
}

func gone(t *testing.T, what string, ended <-chan struct{}) {
	t.Helper()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s outlived EndLeftOver", what)
	}
}

// goneAll waits for every process numbered pid, a group's for a negative
// one, to end. Once its parent is gone, launchd or init reaps it, not at
// once, and signal 0 still reaches it until then, so on Linux, where the
// state is readable, a process that has ended and waits to be reaped counts
// as gone; a busy CI runner can take longer than the wait to reap it.
func goneAll(t *testing.T, what string, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); running(pid) != ""; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s outlived EndLeftOver: %s", what, running(pid))
		}
	}
}

// running names the processes numbered pid, a group's for a negative one,
// that have not ended, or returns "" when none has.
func running(pid int) string {
	if runtime.GOOS != "linux" {
		if syscall.Kill(pid, 0) == nil {
			return fmt.Sprintf("signal 0 still reaches %d", pid)
		}
		return ""
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "read /proc: " + err.Error()
	}
	var left []string
	for _, entry := range entries {
		member, err := strconv.Atoi(entry.Name())
		if err != nil || pid > 0 && member != pid {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue // ended while the directory was read
		}
		// pid (comm) state ppid pgrp ...; comm may hold spaces and
		// parentheses, so the fields are read after its last ")".
		stat := string(data)
		fields := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:])
		if len(fields) < 3 || fields[0] == "Z" || fields[0] == "X" {
			continue
		}
		if pid < 0 && fields[2] != strconv.Itoa(-pid) {
			continue
		}
		left = append(left, fmt.Sprintf("%d (%s)", member, fields[0]))
	}
	return strings.Join(left, ", ")
}

func endLeftOver(t *testing.T, id string, want int) {
	t.Helper()
	count, err := EndLeftOver(id, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("found %d marked processes, want %d", count, want)
	}
}

// TestEndLeftOverEndsWhatACrashedInkunaLeftRunning stands in for what a
// crashed Inkuna leaves: a marked worker that stops when asked, one that
// ignores it, and the latter's child, which dropped the mark from its
// environment but stays in its group. All three end, and a process marked
// by an Inkuna on another directory keeps running.
func TestEndLeftOverEndsWhatACrashedInkunaLeftRunning(t *testing.T) {
	id := uniqueID()
	polite := startMarked(t, id)
	stubborn := startMarked(t, id, "PROC_TEST_IGNORE_TERM=1", "PROC_TEST_CHILD=1", "PROC_TEST_CHILD_UNMARK=1", "PROC_TEST_CHILD_IGNORE_TERM=1")
	other := startMarked(t, id+"-elsewhere")

	start := time.Now()
	endLeftOver(t, id, 2)
	gone(t, "a worker that stops when asked", polite.ended)
	gone(t, "a worker that ignores the request", stubborn.ended)
	goneAll(t, "the unmarked child in the worker's group", -stubborn.pid)
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("EndLeftOver took %s with a 1s grace", elapsed)
	}
	select {
	case <-other.ended:
		t.Fatal("a process marked by another Inkuna was ended")
	default:
	}
}

// TestEndLeftOverEndsTheGroupOfAWorkerWhoseLeaderEnded leaves a worker's
// child running, marked, after the worker itself ended, with an unmarked
// grandchild in the same group. Only the child carries the mark, and the
// group goes with it.
func TestEndLeftOverEndsTheGroupOfAWorkerWhoseLeaderEnded(t *testing.T) {
	id := uniqueID()
	leader := startMarked(t, id, "PROC_TEST_EXIT=1", "PROC_TEST_CHILD=1", "PROC_TEST_CHILD_IGNORE_TERM=1",
		"PROC_TEST_CHILD_CHILD=1", "PROC_TEST_CHILD_CHILD_UNMARK=1", "PROC_TEST_CHILD_CHILD_IGNORE_TERM=1")
	<-leader.ended
	endLeftOver(t, id, 1)
	goneAll(t, "the group of a worker whose leader ended", -leader.pid)
}

// TestEndLeftOverEndsAGroupWhoseLeaderHidesItsEnvironment runs a marked
// worker under /bin/sh, as a check runs its commands under /usr/bin/make:
// macOS hides the leader's environment, mark and all. The group still
// ends.
func TestEndLeftOverEndsAGroupWhoseLeaderHidesItsEnvironment(t *testing.T) {
	id := uniqueID()
	ready := filepath.Join(t.TempDir(), "ready")
	// The shell outlives the worker, as make outlives a command that failed.
	command := Command(context.Background(), "/bin/sh", "-c", `"$0" & wait; sleep 300`, os.Args[0])
	command.Env = append(os.Environ(), "PROC_TEST_ROLE=leftover", "PROC_TEST_READY="+ready, Mark(id))
	shell := start(t, command, ready)
	count, err := EndLeftOver(id, time.Second)
	if err != nil || count == 0 {
		t.Fatalf("EndLeftOver = %d, %v", count, err)
	}
	gone(t, "a shell leading a marked worker", shell.ended)
	goneAll(t, "the shell's group", -shell.pid)
}

// TestEndLeftOverLeavesAGroupInkunaDidNotStart puts a marked process in a
// group led by one that shows an environment without the mark. The marked
// process ends; the group's leader is not Inkuna's, and keeps running.
func TestEndLeftOverLeavesAGroupInkunaDidNotStart(t *testing.T) {
	id := uniqueID()
	leader := startMarked(t, "", "PROC_TEST_CHILD=1", "PROC_TEST_CHILD_MARK="+id)
	child := readyPID(t, leader.ready+".child")
	endLeftOver(t, id, 1)
	goneAll(t, "the marked process", child)
	select {
	case <-leader.ended:
		t.Fatal("the leader of a group Inkuna did not start was ended")
	default:
	}
}

func TestEndLeftOverWithoutAnIDEndsNothing(t *testing.T) {
	unmarked := startMarked(t, "")
	if count, err := EndLeftOver("", time.Second); count != 0 || err != nil {
		t.Fatalf("EndLeftOver(\"\") = %d, %v", count, err)
	}
	select {
	case <-unmarked.ended:
		t.Fatal("an unmarked process was ended")
	default:
	}
}
