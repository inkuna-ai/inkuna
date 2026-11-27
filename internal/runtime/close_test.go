package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

// startUntilClosed starts script as a worker that would run far longer than
// the test, and returns its runtime once the script said it is ready, with a
// channel of what Start returns. The script says so with a line on stdout,
// which the runtime records as an event of its own.
func startUntilClosed(t *testing.T, script string) (AgentRuntime, <-chan error) {
	t.Helper()
	agent := NewCodex(fakecli.Write(t, filepath.Join(t.TempDir(), "fake-worker"), "#!/bin/sh\n"+script))
	started := make(chan struct{}, 16)
	ended := make(chan error, 1)
	go func() {
		_, err := agent.Start(context.Background(), RunRequest{
			RunID: "planning", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
			Budget: domain.Budget{WallTimeSeconds: 600, MaxOutputBytes: 1 << 20},
		}, func(event domain.RunEvent) error {
			if event.Summary != "worker started" {
				started <- struct{}{}
			}
			return nil
		})
		ended <- err
	}()
	select {
	case <-started:
	case err := <-ended:
		t.Fatalf("the worker ended before it was closed: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not start")
	}
	return agent, ended
}

func waitEnded(t *testing.T, ended <-chan error) {
	t.Helper()
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("a worker ended by Close does not succeed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the worker outlived Close")
	}
}

// TestClosingARuntimeEndsItsCallsAndRefusesNewOnes is a planning call still
// running when the app quits: Close ends it and waits for it, and a call
// after Close never starts.
func TestClosingARuntimeEndsItsCallsAndRefusesNewOnes(t *testing.T) {
	// A process caught between fork and exec can miss the request to stop
	// and be killed after the grace, so the grace is kept short.
	grace := closeGrace
	closeGrace = time.Second
	t.Cleanup(func() { closeGrace = grace })
	agent, ended := startUntilClosed(t, "echo ready\nsleep 300\n")
	closed := make(chan struct{})
	go func() {
		agent.(Closer).Close()
		close(closed)
	}()
	waitEnded(t, ended)
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return once its calls ended")
	}
	_, err := agent.Start(context.Background(), RunRequest{RunID: "after", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 600, MaxOutputBytes: 1 << 20}}, func(domain.RunEvent) error { return nil })
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("a call after Close is refused, not %v", err)
	}
}

// copilotRecording is a fake Copilot that notes each time it is run in the
// file it returns. Its server listing waits for as long as the test does.
func copilotRecording(t *testing.T) (AgentRuntime, string) {
	t.Helper()
	runs := filepath.Join(t.TempDir(), "runs")
	binary := fakecli.Write(t, filepath.Join(t.TempDir(), "fake-copilot"), "#!/bin/sh\necho \"$1\" >> '"+runs+"'\nsleep 300\n")
	return NewCopilot(binary), runs
}

// TestAClosedCopilotListsNoServers: a call after Close is refused before
// Copilot is asked for its servers, so nothing starts at all.
func TestAClosedCopilotListsNoServers(t *testing.T) {
	agent, runs := copilotRecording(t)
	agent.(Closer).Close()
	_, err := agent.Start(context.Background(), RunRequest{RunID: "after", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 600, MaxOutputBytes: 1 << 20}}, func(domain.RunEvent) error { return nil })
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("a call after Close is refused, not %v", err)
	}
	if _, err := os.Stat(runs); !os.IsNotExist(err) {
		t.Fatalf("Copilot was run after Close: %v", err)
	}
}

// TestClosingEndsACallStillListingServers closes Copilot while a call waits
// on its server listing, before any worker started.
func TestClosingEndsACallStillListingServers(t *testing.T) {
	agent, runs := copilotRecording(t)
	ended := make(chan error, 1)
	go func() {
		_, err := agent.Start(context.Background(), RunRequest{RunID: "planning", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 600, MaxOutputBytes: 1 << 20}}, func(domain.RunEvent) error { return nil })
		ended <- err
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if data, _ := os.ReadFile(runs); strings.Contains(string(data), "mcp") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server listing did not start")
		}
	}
	agent.(Closer).Close()
	waitEnded(t, ended)
}
