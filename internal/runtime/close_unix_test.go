//go:build !windows

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

// Windows ends a process tree at once, with no request to ignore, so this
// is Unix's alone.

// TestClosingKillsWhatAWorkerLeftRunning has a worker whose child ignores
// the request to stop, with its output elsewhere, so the call ends without
// it. Close still kills it once the grace has passed.
func TestClosingKillsWhatAWorkerLeftRunning(t *testing.T) {
	grace := closeGrace
	closeGrace = 300 * time.Millisecond
	t.Cleanup(func() { closeGrace = grace })
	child := filepath.Join(t.TempDir(), "child")
	// The child notes its pid only once it ignores the request, and the
	// worker says it is ready only then, so Close never comes first.
	agent, ended := startUntilClosed(t, "sh -c 'trap \"\" TERM; echo $$ > \""+child+"\"; exec sleep 300' </dev/null >/dev/null 2>&1 &\n"+
		"while [ ! -s '"+child+"' ]; do sleep 0.05; done\necho ready\nwait\n")
	data, err := os.ReadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	begun := time.Now()
	agent.(Closer).Close()
	if waited := time.Since(begun); waited < closeGrace {
		t.Fatalf("Close waited for the child the worker left, so it returns after the grace, not after %v", waited)
	}
	waitEnded(t, ended)
	// The child, killed, is reaped by whichever process adopted it.
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; {
		if time.Now().After(deadline) {
			t.Fatal("the child the worker left outlived Close")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestACancelledCallReturnsThoughAChildHoldsItsOutput has a worker's child
// ignore the request to stop while it keeps the worker's output open, as a
// process taskkill cannot reach on Windows does. The call still returns once
// the worker itself has ended.
func TestACancelledCallReturnsThoughAChildHoldsItsOutput(t *testing.T) {
	child := filepath.Join(t.TempDir(), "child")
	t.Cleanup(func() {
		if data, err := os.ReadFile(child); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	binary := fakecli.Write(t, filepath.Join(t.TempDir(), "fake-worker"), "#!/bin/sh\n"+
		"sh -c 'trap \"\" TERM; echo $$ > \""+child+"\"; exec sleep 300' &\n"+
		"while [ ! -s '"+child+"' ]; do sleep 0.05; done\necho ready\nwait\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 16)
	ended := make(chan error, 1)
	go func() {
		_, err := NewCodex(binary).Start(ctx, RunRequest{
			RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
			Budget: domain.Budget{WallTimeSeconds: 600, MaxOutputBytes: 1 << 20},
		}, func(event domain.RunEvent) error {
			if event.Summary != "worker started" {
				ready <- struct{}{}
			}
			return nil
		})
		ended <- err
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not start")
	}
	cancel()
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("a cancelled call does not succeed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled call waited on its child's output")
	}
}

// TestAWorkerWhoseEventCouldNotBeStoredIsKilledAndReaped has a worker that
// ignores the request to stop. When its event cannot be stored, the call
// kills it after the grace and waits on it, so it neither runs on nor is
// left a zombie.
func TestAWorkerWhoseEventCouldNotBeStoredIsKilledAndReaped(t *testing.T) {
	grace := closeGrace
	closeGrace = 300 * time.Millisecond
	t.Cleanup(func() { closeGrace = grace })
	pidFile := filepath.Join(t.TempDir(), "pid")
	agent := NewCodex(fakecli.Write(t, filepath.Join(t.TempDir(), "fake-worker"),
		"#!/bin/sh\ntrap '' TERM\necho $$ > '"+pidFile+"'\necho '{\"type\":\"thread.started\"}'\nexec sleep 300\n"))
	begun := time.Now()
	_, err := agent.Start(context.Background(), RunRequest{
		RunID: "store-fails", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
		Budget: domain.Budget{WallTimeSeconds: 600, MaxOutputBytes: 1 << 20},
	}, func(event domain.RunEvent) error {
		if event.Summary == "worker started" {
			return nil
		}
		return errors.New("disk is full")
	})
	if err == nil || !strings.Contains(err.Error(), "store run event: disk is full") {
		t.Fatalf("the call says the event was not stored: %v", err)
	}
	if waited := time.Since(begun); waited > 10*time.Second {
		t.Fatalf("the call waited %v for a worker that ignores the request to stop", waited)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	// Signal 0 reaches a zombie too, so only a reaped worker is gone.
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the worker must be killed and reaped once the call returns: %v", err)
	}
}

// TestAChildAStoppedWorkerLeftIsKilled has a worker that stops when asked
// while a child it started ignores the request, with its output elsewhere.
// The call ends once the worker has, and the child is still killed once the
// grace has passed, since nothing else could reach it after the call.
func TestAChildAStoppedWorkerLeftIsKilled(t *testing.T) {
	grace := closeGrace
	closeGrace = 300 * time.Millisecond
	t.Cleanup(func() { closeGrace = grace })
	child := filepath.Join(t.TempDir(), "child")
	agent := NewCodex(fakecli.Write(t, filepath.Join(t.TempDir(), "fake-worker"), "#!/bin/sh\n"+
		"sh -c 'trap \"\" TERM; echo $$ > \""+child+"\"; exec sleep 300' </dev/null >/dev/null 2>&1 &\n"+
		"while [ ! -s '"+child+"' ]; do sleep 0.05; done\necho '{\"type\":\"thread.started\"}'\nwait\n"))
	_, err := agent.Start(context.Background(), RunRequest{
		RunID: "store-fails", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
		Budget: domain.Budget{WallTimeSeconds: 600, MaxOutputBytes: 1 << 20},
	}, func(event domain.RunEvent) error {
		if event.Summary == "worker started" {
			return nil
		}
		return errors.New("disk is full")
	})
	if err == nil || !strings.Contains(err.Error(), "store run event: disk is full") {
		t.Fatalf("the call says the event was not stored: %v", err)
	}
	data, err := os.ReadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	// The child, killed, is reaped by whichever process adopted it.
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("the child the stopped worker left outlived the call")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
