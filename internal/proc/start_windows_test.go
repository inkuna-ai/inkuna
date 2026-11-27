//go:build windows

package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// readyPID waits for the process writing ready to be ready, and returns
// its process ID.
func readyPID(t *testing.T, ready string) uint32 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		text, _ := os.ReadFile(ready)
		if pid, found := strings.CutPrefix(strings.TrimSpace(string(text)), "ready "); found {
			number, err := strconv.ParseUint(pid, 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			return uint32(number)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was not written", ready)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ends says whether process pid ends within wait.
func ends(t *testing.T, pid uint32, wait time.Duration) bool {
	t.Helper()
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		// No such process: it has ended and been released.
		return true
	}
	defer func() { _ = windows.CloseHandle(process) }()
	event, err := windows.WaitForSingleObject(process, uint32(wait.Milliseconds()))
	if err != nil {
		t.Fatal(err)
	}
	return event == windows.WAIT_OBJECT_0
}

// TestWhatStartStartsEndsWithInkuna stands in for an Inkuna that started a
// worker with Start, and the worker a child of its own, and then crashed:
// the stand-in is ended at once, as a crash ends it. Windows ends the
// worker and its child with it.
func TestWhatStartStartsEndsWithInkuna(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	inkuna := exec.Command(os.Args[0])
	inkuna.Env = append(os.Environ(), "PROC_TEST_ROLE=inkuna", "PROC_TEST_READY="+ready)
	if err := inkuna.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		_ = inkuna.Wait()
		close(ended)
	}()
	worker, child := readyPID(t, ready+".worker"), readyPID(t, ready+".worker.child")
	t.Cleanup(func() {
		for _, pid := range []uint32{worker, child} {
			if process, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
				_ = windows.TerminateProcess(process, 1)
				_ = windows.CloseHandle(process)
			}
		}
	})
	if ends(t, worker, 200*time.Millisecond) {
		t.Fatal("the worker ended before Inkuna did: it did not run")
	}

	if err := inkuna.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-ended
	if !ends(t, worker, 5*time.Second) {
		t.Fatal("the worker outlived its Inkuna")
	}
	if !ends(t, child, 5*time.Second) {
		t.Fatal("the worker's child outlived its Inkuna")
	}
}
