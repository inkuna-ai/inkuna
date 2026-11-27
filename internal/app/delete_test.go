package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

const manualWorktrees = "version: 1\ntracker: github\nbranch:\n  base: main\n  pattern: \"{type}/{story}-{slug}\"\nworktrees:\n  enabled: true\n  location: ../{repo}-worktrees\n  cleanup: manual\n"

// finishedTask plans and runs a task to the end and returns its run.
func finishedTask(t *testing.T, service *Service, objective string) WorkflowRunView {
	t.Helper()
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: objective, Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	return settledRun(t, service, run.ID, "completed")
}

// settledRun waits for a run to reach state and then for its goroutine to
// finish storing its end. A delete refuses a run whose view says it ended
// while that goroutine is still finishing, so a test that deletes right
// after the view changes would race it.
func settledRun(t *testing.T, service *Service, runID, state string) WorkflowRunView {
	t.Helper()
	waitForWorkflowRun(t, service, runID, state)
	if err := service.settle(runID); err != nil {
		t.Fatal(err)
	}
	run, err := service.WorkflowRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func taskExists(t *testing.T, service *Service, taskID string) bool {
	t.Helper()
	_, err := service.store.Task(context.Background(), taskID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

func TestARunningOrPausedTaskIsRefusedAndNothingIsDeleted(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	runtime := writingRuntime()
	writing := runtime.handle
	// Once hold is set, a step runs until it is cancelled.
	var hold atomic.Bool
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if hold.Load() {
			<-ctx.Done()
			return agentruntime.RunResult{}, ctx.Err()
		}
		return writing(ctx, request)
	}
	service := workflowTestService(t, repository, runtime)
	finished := finishedTask(t, service, "Fix the login crash #42")
	hold.Store(true)
	_, running, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the logout crash #43", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	running = waitForWorkflowRun(t, service, running.ID, "running")
	refused := func(want string, taskIDs ...string) {
		t.Helper()
		_, err := service.DeleteTasks(taskIDs, DeleteOptions{RemoveWorktrees: true})
		var refusal *storage.DeleteRefusedError
		if !errors.As(err, &refusal) || !strings.Contains(err.Error(), want) {
			t.Fatalf("DeleteTasks(%v) = %v, want a refusal saying %q", taskIDs, err, want)
		}
		for _, id := range taskIDs {
			if !taskExists(t, service, id) {
				t.Fatalf("a refused delete removed task %s", id)
			}
		}
		if _, err := os.Stat(finished.Worktree); err != nil {
			t.Fatalf("a refused delete removed a worktree: %v", err)
		}
	}
	refused("run "+running.ID+" is running", finished.TaskID, running.TaskID)
	// The store can still say queued after the run in memory has started, and
	// the preview gives memory's reason, as the delete does.
	if _, err := service.store.DB().Exec(`UPDATE executions SET state='queued' WHERE id=?`, running.ID); err != nil {
		t.Fatal(err)
	}
	if previews, err := service.PreviewDeleteTasks([]string{finished.TaskID, running.TaskID}); err != nil || previews[0].Refused != "" || !strings.Contains(previews[1].Refused, "is running") {
		t.Fatalf("the preview says which task cannot go: %+v, %v", previews, err)
	}
	if _, err := service.CancelWorkflowRun(running.ID); err != nil {
		t.Fatal(err)
	}
	settledRun(t, service, running.ID, "cancelled")

	// A run paused for a question or for review is not running, and is
	// refused all the same, whether memory or the store says so.
	service.runsMu.Lock()
	service.runs[finished.ID].view.State = "waiting"
	service.runsMu.Unlock()
	refused("is waiting for an answer or a permission", finished.TaskID)
	service.runsMu.Lock()
	service.runs[finished.ID].view.State = "completed"
	// Ended, but its goroutine is still storing the end.
	live := service.runs[finished.ID]
	done := live.done
	live.done = make(chan struct{})
	service.runsMu.Unlock()
	refused("is still finishing", finished.TaskID)
	service.runsMu.Lock()
	live.done = done
	delete(service.runs, finished.ID)
	service.runsMu.Unlock()
	if _, err := service.store.DB().Exec(`UPDATE executions SET state='reviewing' WHERE id=?`, finished.ID); err != nil {
		t.Fatal(err)
	}
	refused("is paused for review", finished.TaskID)
	if _, err := service.store.DB().Exec(`UPDATE executions SET state='completed' WHERE id=?`, finished.ID); err != nil {
		t.Fatal(err)
	}

	report, err := service.DeleteTasks([]string{finished.TaskID, running.TaskID}, DeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tasks) != 2 || taskExists(t, service, finished.TaskID) || taskExists(t, service, running.TaskID) {
		t.Fatalf("both tasks go once neither runs: %+v", report)
	}
	if _, err := service.WorkflowRun(finished.ID); err == nil {
		t.Fatal("a deleted run is no longer listed")
	}
}

func TestAWorktreeGoesOnlyWhenAskedAndABranchOnlyAtTheConfirmedCommit(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	service := workflowTestService(t, repository, writingRuntime())
	ctx := context.Background()

	kept := finishedTask(t, service, "Fix the login crash #42")
	other := finishedTask(t, service, "Fix the signup crash #44")
	// A blob the other task stores too stays when the first goes.
	records, err := service.store.TaskArtifacts(ctx, kept.TaskID)
	if err != nil || len(records) == 0 {
		t.Fatalf("artifacts = %+v, %v", records, err)
	}
	blob, err := service.artifacts.Open(records[0].Digest)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(blob)
	_ = blob.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.storeArtifact(ctx, other.TaskID, other.ID, "implement", "", "context", "copy", "text/plain", strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}

	previews, err := service.PreviewDeleteTasks([]string{kept.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	preview := previews[0].Runs[0]
	if previews[0].Refused != "" || preview.Worktree == nil || !preview.Worktree.Removable || !samePath(preview.Worktree.Path, kept.Worktree) ||
		preview.Branch == nil || !preview.Branch.Deletable || preview.Branch.Name != kept.Branch || preview.Branch.Unique == 0 {
		t.Fatalf("preview = %+v %+v %+v", previews[0], preview.Worktree, preview.Branch)
	}

	report, err := service.DeleteTasks([]string{kept.TaskID}, DeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	task := report.Tasks[0]
	if len(task.Worktrees) != 1 || task.Worktrees[0].Removed || task.Worktrees[0].Reason != "Not asked to remove it." {
		t.Fatalf("worktrees = %+v", task.Worktrees)
	}
	if len(task.Branches) != 1 || task.Branches[0].Deleted || task.Branches[0].Unique == 0 || task.Branches[0].Reason != "Not asked to delete it." {
		t.Fatalf("branches = %+v", task.Branches)
	}
	if _, err := os.Stat(kept.Worktree); err != nil || !branchExists(repository, kept.Branch) {
		t.Fatalf("without being asked, the worktree and branch stay: %v", err)
	}
	if task.Blobs == 0 || task.BlobBytes == 0 || task.KeptBlobs == 0 {
		t.Fatalf("blobs freed %d (%d bytes), kept %d", task.Blobs, task.BlobBytes, task.KeptBlobs)
	}
	if blob, err := service.artifacts.Open(records[0].Digest); err != nil {
		t.Fatalf("the shared blob stays: %v", err)
	} else {
		_ = blob.Close()
	}
	if len(task.Links) != 1 || task.Links[0] != (DroppedLink{Tracker: "github", ID: "42"}) {
		t.Fatalf("the run's story is reported as a link dropped here: %+v", task.Links)
	}

	// Asked to remove worktrees, a clean one goes and one with changes
	// stays; an unmerged branch stays unless it is confirmed.
	removed := finishedTask(t, service, "Fix the reset crash #45")
	dirty := finishedTask(t, service, "Fix the profile crash #46")
	if err := os.WriteFile(filepath.Join(dirty.Worktree, "SCRATCH.md"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err = service.DeleteTasks([]string{removed.TaskID, dirty.TaskID}, DeleteOptions{RemoveWorktrees: true})
	if err != nil {
		t.Fatal(err)
	}
	if outcome := report.Tasks[0].Worktrees; len(outcome) != 1 || !outcome[0].Removed {
		t.Fatalf("a clean worktree goes when asked: %+v", outcome)
	}
	if _, err := os.Stat(removed.Worktree); !errors.Is(err, os.ErrNotExist) || !branchExists(repository, removed.Branch) {
		t.Fatalf("the worktree goes and its unmerged branch stays: %v", err)
	}
	if outcome := report.Tasks[1].Worktrees; len(outcome) != 1 || outcome[0].Removed || !strings.Contains(outcome[0].Reason, "uncommitted changes") {
		t.Fatalf("a worktree with changes stays: %+v", outcome)
	}
	if _, err := os.Stat(filepath.Join(dirty.Worktree, "SCRATCH.md")); err != nil {
		t.Fatalf("its changes are kept: %v", err)
	}

	// A branch goes only at the commit the person confirmed, under its own
	// project, and its clean worktree with it.
	deleted := finishedTask(t, service, "Fix the search crash #47")
	moved := finishedTask(t, service, "Fix the export crash #48")
	elsewhere := finishedTask(t, service, "Fix the import crash #49")
	previews, err = service.PreviewDeleteTasks([]string{deleted.TaskID, moved.TaskID, elsewhere.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	branch := previews[0].Runs[0].Branch
	if branch.ProjectID != deleted.ProjectID {
		t.Fatalf("a branch preview names its project: %+v", branch)
	}
	confirmed := map[string]map[string]string{
		deleted.ProjectID: {deleted.Branch: branch.Commit, moved.Branch: "0000000"},
		"another-project": {elsewhere.Branch: previews[2].Runs[0].Branch.Commit},
	}
	report, err = service.DeleteTasks([]string{deleted.TaskID, moved.TaskID, elsewhere.TaskID}, DeleteOptions{DeleteBranches: confirmed})
	if err != nil {
		t.Fatal(err)
	}
	if outcome := report.Tasks[0].Branches; len(outcome) != 1 || !outcome[0].Deleted || outcome[0].Unique == 0 || branchExists(repository, deleted.Branch) {
		t.Fatalf("a confirmed branch goes: %+v", outcome)
	}
	if outcome := report.Tasks[0].Worktrees; len(outcome) != 1 || !outcome[0].Removed {
		t.Fatalf("the worktree its branch was checked out in goes with it: %+v", outcome)
	}
	if outcome := report.Tasks[1].Branches; len(outcome) != 1 || outcome[0].Deleted || !strings.Contains(outcome[0].Reason, "now points at") || !branchExists(repository, moved.Branch) {
		t.Fatalf("a branch confirmed at another commit stays: %+v", outcome)
	}
	if outcome := report.Tasks[1].Worktrees; len(outcome) != 1 || outcome[0].Removed {
		t.Fatalf("so does its worktree: %+v", outcome)
	}
	if outcome := report.Tasks[2].Branches; len(outcome) != 1 || outcome[0].Deleted || outcome[0].Reason != "Not asked to delete it." || !branchExists(repository, elsewhere.Branch) {
		t.Fatalf("a branch confirmed under another project stays: %+v", outcome)
	}
}

func TestDeleteRunLeavesTheTaskAndItsOtherRun(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	service := workflowTestService(t, repository, writingRuntime())
	plan, first, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the login crash #42", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	first = settledRun(t, service, first.ID, "completed")
	encoded, err := json.Marshal(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	second = settledRun(t, service, second.ID, "completed")
	if second.TaskID != first.TaskID {
		t.Fatalf("both runs are of one task: %s, %s", first.TaskID, second.TaskID)
	}

	report, err := service.DeleteRun(first.ID, DeleteOptions{RemoveWorktrees: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].RunID != first.ID || len(report.Tasks[0].Worktrees) != 1 || !report.Tasks[0].Worktrees[0].Removed {
		t.Fatalf("report = %+v", report)
	}
	if _, err := service.WorkflowRun(first.ID); err == nil {
		t.Fatal("the deleted run is gone")
	}
	if !taskExists(t, service, first.TaskID) {
		t.Fatal("the task stays")
	}
	if _, err := service.WorkflowRun(second.ID); err != nil {
		t.Fatalf("its other run stays: %v", err)
	}
	if _, err := service.DeleteRun(second.ID, DeleteOptions{}); err == nil || !strings.Contains(err.Error(), "only run") {
		t.Fatalf("the task's only run = %v", err)
	}
}

func TestAWorktreeAnotherRunRecordsIsKept(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	service := workflowTestService(t, repository, writingRuntime())
	deleted := finishedTask(t, service, "Fix the login crash #42")
	other := finishedTask(t, service, "Fix the signup crash #44")
	// The other run worked in the same directory, made again after it was
	// removed outside Inkuna.
	if _, err := service.store.DB().Exec(`UPDATE executions SET worktree=? WHERE id=?`, deleted.Worktree, other.ID); err != nil {
		t.Fatal(err)
	}
	service.runsMu.Lock()
	delete(service.runs, other.ID)
	service.runsMu.Unlock()

	previews, err := service.PreviewDeleteTasks([]string{deleted.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	if tree := previews[0].Runs[0].Worktree; tree == nil || tree.Removable || !strings.Contains(tree.Reason, other.ID) {
		t.Fatalf("the preview keeps it: %+v", tree)
	}
	report, err := service.DeleteTasks([]string{" " + deleted.TaskID + " "}, DeleteOptions{RemoveWorktrees: true})
	if err != nil {
		t.Fatal(err)
	}
	if outcome := report.Tasks[0].Worktrees; len(outcome) != 1 || outcome[0].Removed || !strings.Contains(outcome[0].Reason, "works in it too") {
		t.Fatalf("worktrees = %+v", outcome)
	}
	if _, err := os.Stat(deleted.Worktree); err != nil {
		t.Fatalf("the worktree stays: %v", err)
	}
	// It is the other run's, not one the delete kept.
	if usage, err := service.StorageUsage(); err != nil || len(usage.Kept) != 0 {
		t.Fatalf("kept = %+v, %v", usage.Kept, err)
	}
}

func TestTheRunsListSaysWhichTasksCannotBeDeleted(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	runtime := writingRuntime()
	writing := runtime.handle
	var hold atomic.Bool
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if hold.Load() {
			<-ctx.Done()
			return agentruntime.RunResult{}, ctx.Err()
		}
		return writing(ctx, request)
	}
	service := workflowTestService(t, repository, runtime)
	finished := finishedTask(t, service, "Fix the login crash #42")
	hold.Store(true)
	_, running, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the logout crash #43", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	running = waitForWorkflowRun(t, service, running.ID, "running")
	t.Cleanup(func() { _, _ = service.CancelWorkflowRun(running.ID) })

	refusals := func() map[string]string {
		t.Helper()
		views, err := service.WorkflowRuns()
		if err != nil {
			t.Fatal(err)
		}
		found := make(map[string]string)
		for _, view := range views {
			found[view.ID] = view.DeleteRefused
		}
		return found
	}
	listed := refusals()
	if listed[finished.ID] != "" || !strings.Contains(listed[running.ID], "run "+running.ID+" is running") {
		t.Fatalf("refusals = %+v", listed)
	}
	// Memory is ahead of the store: a run whose end is still being stored
	// keeps its task.
	service.runsMu.Lock()
	live := service.runs[finished.ID]
	done := live.done
	live.done = make(chan struct{})
	service.runsMu.Unlock()
	if reason := refusals()[finished.ID]; !strings.Contains(reason, "is still finishing") {
		t.Fatalf("a finishing run's task = %q", reason)
	}
	service.runsMu.Lock()
	live.done = done
	service.runsMu.Unlock()
}

func TestPreviewDeleteRunShowsOneRunAndRefusesATasksOnlyRun(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	service := workflowTestService(t, repository, writingRuntime())
	plan, first, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the login crash #42", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	first = settledRun(t, service, first.ID, "completed")
	encoded, err := json.Marshal(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	second = settledRun(t, service, second.ID, "completed")

	preview, err := service.PreviewDeleteRun(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Refused != "" || preview.TaskID != first.TaskID || len(preview.Runs) != 1 || preview.Runs[0].RunID != first.ID {
		t.Fatalf("preview = %+v", preview)
	}
	if tree := preview.Runs[0].Worktree; tree == nil || !samePath(tree.Path, first.Worktree) || !tree.Removable || tree.Bytes == 0 {
		t.Fatalf("the run's worktree, with its size: %+v", tree)
	}
	if _, err := service.DeleteRun(first.ID, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	preview, err = service.PreviewDeleteRun(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Refused, "is the task's only run; delete the task instead") {
		t.Fatalf("the task's only run = %+v", preview)
	}
	if _, err := service.PreviewDeleteRun("missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown run = %v", err)
	}
}

func TestAWorktreeADeleteKeptIsListedAndRemovedFromSettings(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	service := workflowTestService(t, repository, writingRuntime())
	kept := finishedTask(t, service, "Fix the login crash #42")
	listedRun := finishedTask(t, service, "Fix the signup crash #44")

	before, err := service.StorageUsage()
	if err != nil {
		t.Fatal(err)
	}
	if before.Worktrees != 2 || before.WorktreeBytes == 0 || before.Artifacts == 0 || before.ArtifactBytes == 0 || len(before.Kept) != 0 {
		t.Fatalf("usage = %+v", before)
	}
	if _, err := service.DeleteTasks([]string{kept.TaskID}, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	usage, err := service.StorageUsage()
	if err != nil {
		t.Fatal(err)
	}
	if usage.Worktrees != 2 || len(usage.Kept) != 1 {
		t.Fatalf("the kept worktree is still counted and is listed: %+v", usage)
	}
	record := usage.Kept[0]
	if !samePath(record.Path, kept.Worktree) || record.Branch != kept.Branch || record.TaskID != kept.TaskID || record.RunID != kept.ID ||
		record.Reason != "Not asked to remove it." || record.Bytes == 0 || record.ProjectName == "" || record.KeptAt == "" {
		t.Fatalf("kept = %+v", record)
	}

	// Only a clean one goes, and only one a delete kept.
	scratch := filepath.Join(record.Path, "SCRATCH.md")
	if err := os.WriteFile(scratch, []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveKeptWorktree(record.Path); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("a worktree with changes = %v", err)
	}
	if err := os.Remove(scratch); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveKeptWorktree(listedRun.Worktree); err == nil || !strings.Contains(err.Error(), "no delete kept a worktree") {
		t.Fatalf("a run's own worktree = %v", err)
	}
	if err := service.RemoveKeptWorktree(record.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(kept.Worktree); !errors.Is(err, os.ErrNotExist) || !branchExists(repository, kept.Branch) {
		t.Fatalf("the worktree goes and its branch stays: %v", err)
	}
	if usage, err := service.StorageUsage(); err != nil || len(usage.Kept) != 0 || usage.Worktrees != 1 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	if _, err := os.Stat(listedRun.Worktree); err != nil {
		t.Fatalf("the other run's worktree stays: %v", err)
	}

	// A kept worktree removed outside Inkuna is no longer listed.
	gone := finishedTask(t, service, "Fix the reset crash #45")
	if _, err := service.DeleteTasks([]string{gone.TaskID}, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone.Worktree); err != nil {
		t.Fatal(err)
	}
	if usage, err := service.StorageUsage(); err != nil || len(usage.Kept) != 0 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	if _, err := service.store.KeptWorktreeAt(context.Background(), gone.Worktree); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("its record is dropped: %v", err)
	}
}
