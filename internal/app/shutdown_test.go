package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

// TestQuittingEndsACallMadeOutsideARun starts a call on a declared runtime,
// as planning does, and then saves the runtime again, which replaces its
// adapter. Quitting still ends the call: nothing else would, and its model
// would keep running.
func TestQuittingEndsACallMadeOutsideARun(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	t.Setenv(config.EnvConfigFile, "")
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repositoryPath, &workflowRuntime{})
	binary := fakecli.Write(t, filepath.Join(t.TempDir(), "slow-cli"), "#!/bin/sh\ncat > /dev/null\necho thinking\nsleep 300\n")
	declaration, err := json.Marshal(config.RuntimeConfig{Label: "Slow CLI", Binary: binary, Stdin: "prompt", Output: "text", ReadOnlyArgs: []string{"--read-only"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveRuntime("slow", string(declaration)); err != nil {
		t.Fatal(err)
	}
	service.runtimesMu.RLock()
	adapter := service.runtimes["slow"]
	service.runtimesMu.RUnlock()

	thinking := make(chan struct{}, 4)
	ended := make(chan error, 1)
	go func() {
		_, err := adapter.Start(context.Background(), agentruntime.RunRequest{
			RunID: "orchestrator-plan", WorkingDir: t.TempDir(), Prompt: []byte("plan"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
			Budget: domain.Budget{WallTimeSeconds: 600, MaxOutputBytes: 1 << 20},
		}, func(event domain.RunEvent) error {
			if event.Summary == "thinking" {
				thinking <- struct{}{}
			}
			return nil
		})
		ended <- err
	}()
	select {
	case <-thinking:
	case <-time.After(10 * time.Second):
		t.Fatal("the call did not start")
	}
	if _, err := service.SaveRuntime("slow", string(declaration)); err != nil {
		t.Fatal(err)
	}

	service.Shutdown(context.Background())
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("a call ended by quitting does not succeed")
		}
	case <-time.After(time.Second):
		t.Fatal("the call outlived quitting")
	}
}
