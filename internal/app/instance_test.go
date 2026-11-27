package app

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/proc"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// openedService builds a service on dataDir and opens its store, as the
// desktop does when it starts.
func openedService(t *testing.T, dataDir string) (*Service, error) {
	t.Helper()
	service, err := New(dataDir, extension.Product{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service, service.ready()
}

// TestASecondInkunaLeavesTheFirstOnesRunsAlone starts a second Inkuna on a
// data directory the first is using, with one of the first one's runs live.
// The second is refused before it opens the store, so the run is not marked
// interrupted. Once the first quits, the directory opens again.
func TestASecondInkunaLeavesTheFirstOnesRunsAlone(t *testing.T) {
	t.Setenv(config.EnvConfigFile, "")
	dataDir := t.TempDir()
	first, err := openedService(t, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repository := t.TempDir()
	task := domain.TaskSpec{Version: 1, ID: "task", Kind: domain.TaskImplementation, Objective: "Test", Repository: repository, Budget: domain.Budget{WallTimeSeconds: 60}}
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{
		{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Permission: domain.PermissionWorktree, AllowedPaths: []string{"."}, Budget: domain.Budget{WallTimeSeconds: 10}},
	}}
	for _, err := range []error{
		first.store.UpsertProject(ctx, storage.Project{ID: "project", Name: "Project", Repository: repository, DefaultRuntime: domain.RuntimeClaude}),
		first.store.SaveTask(ctx, "project", "approved", task),
		first.store.ApproveWorkflow(ctx, workflow),
		first.store.SaveExecution(ctx, storage.Execution{ID: "live", WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: "project", State: "running", Title: "A live run"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	second, err := openedService(t, dataDir)
	if !errors.Is(err, ErrDataDirInUse) {
		t.Fatalf("a second Inkuna on the directory: %v, want ErrDataDirInUse", err)
	}
	if second.store != nil {
		t.Fatal("the second Inkuna does not open the store")
	}
	if execution, err := first.store.Execution(ctx, "live"); err != nil || execution.State != "running" {
		t.Fatalf("the first one's run is still live: %+v %v", execution, err)
	}
	other, err := openedService(t, t.TempDir())
	if err != nil {
		t.Fatalf("an Inkuna on another directory starts: %v", err)
	}
	// Before the directory is removed: Windows removes no open database.
	other.Shutdown(ctx)

	first.Shutdown(ctx)
	third, err := openedService(t, dataDir)
	if err != nil {
		t.Fatalf("the directory opens once the first Inkuna quit: %v", err)
	}
	third.Shutdown(ctx)
}

func TestInstancesAreNamedByTheirDataDirectory(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	if InstanceID(&Service{dataDir: first}) != InstanceID(&Service{dataDir: first + "/."}) {
		t.Fatal("one directory, however it is written, is one instance")
	}
	if InstanceID(&Service{dataDir: first}) == InstanceID(&Service{dataDir: second}) {
		t.Fatal("two directories are two instances")
	}
}

// startLeftOver starts the test binary as a worker a crashed Inkuna left
// running, marked with mark, in a process group of its own as Inkuna starts
// one. ended closes once it is gone; waiting on it reaps it, as launchd or
// init would once its Inkuna was.
func startLeftOver(t *testing.T, mark string) (ended <-chan struct{}) {
	t.Helper()
	command := proc.Command(context.Background(), os.Args[0], leftOverWorker)
	command.Env = append(os.Environ(), mark)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = proc.Kill(command)
		<-done
	})
	return done
}

// TestStartingEndsTheWorkersACrashLeftRunning leaves a worker marked by
// the data directory's Inkuna running, as a crash does, and one marked by
// an Inkuna on another directory. Starting on the directory ends the first,
// before any run could be resumed beside it, and leaves the second alone.
func TestStartingEndsTheWorkersACrashLeftRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ends the workers when Inkuna dies, not at the next start")
	}
	t.Setenv(config.EnvConfigFile, "")
	dataDir, elsewhere := t.TempDir(), t.TempDir()
	left := startLeftOver(t, proc.Mark(InstanceID(&Service{dataDir: dataDir})))
	other := startLeftOver(t, proc.Mark(InstanceID(&Service{dataDir: elsewhere})))

	service, err := openedService(t, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown(context.Background())
	select {
	case <-left:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker a crash left running outlived the next start")
	}
	select {
	case <-other:
		t.Fatal("a worker of an Inkuna on another directory was ended")
	default:
	}
}
