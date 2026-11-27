package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// syncedClone is a clone of a bare origin and a teammate's clone that has
// just pushed a commit to main, which the first clone has not fetched yet.
func syncedClone(t *testing.T) (clone, teammate string) {
	t.Helper()
	parent := t.TempDir()
	origin := filepath.Join(parent, "origin.git")
	clone, teammate = filepath.Join(parent, "repository"), filepath.Join(parent, "teammate")
	steps := []struct {
		directory string
		args      []string
	}{
		{parent, []string{"init", "-q", "--bare", "-b", "main", origin}},
		{parent, []string{"clone", "-q", origin, teammate}},
		{teammate, []string{"commit", "-q", "--allow-empty", "-m", "initial"}},
		{teammate, []string{"push", "-q", "origin", "HEAD:main"}},
		{parent, []string{"clone", "-q", origin, clone}},
		{teammate, []string{"commit", "-q", "--allow-empty", "-m", "merged elsewhere"}},
		{teammate, []string{"push", "-q", "origin", "HEAD:main"}},
	}
	for _, step := range steps {
		if err := runGit(step.directory, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	return clone, teammate
}

func branchNamed(t *testing.T, view BranchSyncView, name string) worktree.BranchSync {
	t.Helper()
	for _, branch := range view.Sync.Branches {
		if branch.Name == name {
			return branch
		}
	}
	t.Fatalf("no branch %s in %+v", name, view.Sync.Branches)
	return worktree.BranchSync{}
}

func TestBranchSyncFetchesAndFastForwardsOutsideActiveRuns(t *testing.T) {
	clone, _ := syncedClone(t)
	// The project points inside the repository, as projects may.
	inside := filepath.Join(clone, "app")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, inside, writingRuntime())

	view, err := service.ProjectBranches("project")
	if err != nil {
		t.Fatal(err)
	}
	if view.Sync.Base != "main" || !view.Sync.HasOrigin || branchNamed(t, view, "main").State != worktree.SyncSynced {
		t.Fatalf("before a fetch main looks synced: %+v", view)
	}

	view, err = service.FetchProject("project")
	if err != nil || view.FetchError != "" {
		t.Fatalf("fetch: %+v, %v", view, err)
	}
	if main := branchNamed(t, view, "main"); main.State != worktree.SyncBehind || main.Behind != 1 || !main.Updatable {
		t.Fatalf("after a fetch main is behind and can be updated: %+v", main)
	}

	service.runsMu.Lock()
	service.runs["run-writing"] = &liveExecution{view: WorkflowRunView{ID: "run-writing", ProjectID: "project", State: "running", Branch: "main", Worktree: clone}}
	service.runsMu.Unlock()
	view, err = service.ProjectBranches("project")
	if err != nil {
		t.Fatal(err)
	}
	if main := branchNamed(t, view, "main"); main.Updatable || !strings.Contains(main.Reason, "run-writing") {
		t.Fatalf("a branch an active run writes on is not offered: %+v", main)
	}
	if _, err := service.UpdateBranch("project", "main"); err == nil || !strings.Contains(err.Error(), "run run-writing is writing on main") {
		t.Fatalf("UpdateBranch during a run = %v", err)
	}

	service.runsMu.Lock()
	service.runs["run-writing"].view.State = "completed"
	service.runsMu.Unlock()
	view, err = service.UpdateBranch("project", "main")
	if err != nil {
		t.Fatal(err)
	}
	if view.Update == nil || view.Update.From == view.Update.To || branchNamed(t, view, "main").State != worktree.SyncSynced {
		t.Fatalf("a finished run no longer holds main: %+v", view)
	}
}

func TestFetchFailureKeepsTheLocalBranches(t *testing.T) {
	clone, _ := syncedClone(t)
	if err := runGit(clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git")); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, clone, writingRuntime())
	view, err := service.FetchProject("project")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(view.FetchError, "Could not fetch from origin") || len(view.Sync.Branches) != 1 {
		t.Fatalf("view = %+v", view)
	}
}

func TestUnreadableSettingsKeepTheBranchesAndSayWhy(t *testing.T) {
	// Windows has no search permission to take away: Chmod only sets a
	// directory read-only.
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot make a directory unsearchable")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}
	clone, _ := syncedClone(t)
	agents := filepath.Join(clone, ".inkuna", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "reviewer.md"), []byte("ours"), 0o644); err != nil {
		t.Fatal(err)
	}
	// git lists a directory it may read but not search, and nothing in it
	// can be opened, so the new agent cannot be read.
	if err := os.Chmod(agents, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agents, 0o755) })
	service := workflowTestService(t, clone, writingRuntime())
	view, err := service.ProjectBranches("project")
	if err != nil {
		t.Fatalf("the branches must still show: %v", err)
	}
	if len(view.Sync.Branches) != 1 || view.Settings == nil || view.Settings.Shareable || len(view.Settings.Changes) != 0 ||
		!strings.Contains(view.Settings.Reason, "could not be read") {
		t.Fatalf("view = %+v, settings %+v", view, view.Settings)
	}
}

func TestFetchFailureSaysHowToSignIn(t *testing.T) {
	signIn := errors.Join(worktree.ErrNotSignedIn, errors.New("terminal prompts disabled"))
	if message := fetchFailure(signIn, "github.com/inkuna-ai/inkuna"); !strings.Contains(message, "gh auth setup-git") {
		t.Fatalf("GitHub sign-in failure = %q", message)
	}
	if message := fetchFailure(signIn, "gitlab.example.com/team/app"); !strings.Contains(message, "gitlab.example.com") {
		t.Fatalf("other host sign-in failure = %q", message)
	}
}

func TestDeleteBranchRemovesAFinishedRunsWorktreeThroughTheRun(t *testing.T) {
	repository := branchingRepository(t, "version: 1\ntracker: github\nbranch:\n  base: main\n  pattern: \"{type}/{story}-{slug}\"\nworktrees:\n  enabled: true\n  location: ../{repo}-worktrees\n  cleanup: manual\n")
	service := workflowTestService(t, repository, writingRuntime())
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the login crash #42", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	view, err := service.ProjectBranches("project")
	if err != nil {
		t.Fatal(err)
	}
	branch := branchNamed(t, view, run.Branch)
	if !branch.Deletable || branch.Unique == 0 || !samePath(branch.CheckedOut, run.Worktree) {
		t.Fatalf("a finished run's branch can go, and its commit is only there: %+v", branch)
	}

	service.runsMu.Lock()
	service.runs["run-busy"] = &liveExecution{view: WorkflowRunView{ID: "run-busy", ProjectID: "project", State: "running", Branch: run.Branch}}
	service.runsMu.Unlock()
	view, err = service.ProjectBranches("project")
	if err != nil {
		t.Fatal(err)
	}
	if busy := branchNamed(t, view, run.Branch); busy.Deletable || !strings.Contains(busy.DeleteReason, "run-busy") {
		t.Fatalf("a branch an active run works on is not offered: %+v", busy)
	}
	if _, err := service.DeleteBranch("project", run.Branch, branch.Commit); err == nil || !strings.Contains(err.Error(), "run run-busy works on") {
		t.Fatalf("DeleteBranch during a run = %v", err)
	}
	service.runsMu.Lock()
	service.runs["run-busy"].view.State = "completed"
	service.runsMu.Unlock()

	view, err = service.DeleteBranch("project", run.Branch, branch.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if view.Deleted == nil || !samePath(view.Deleted.Worktree, run.Worktree) || view.Deleted.Unique != branch.Unique || branchExists(repository, run.Branch) {
		t.Fatalf("deleted = %+v", view.Deleted)
	}
	if _, err := os.Stat(run.Worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the run's worktree goes with its branch")
	}
	stored, err := service.store.Execution(context.Background(), run.ID)
	if err != nil || stored.WorktreeRemovedAt.IsZero() {
		t.Fatalf("the run records its worktree's removal: %+v, %v", stored, err)
	}
	if after, err := service.WorkflowRun(run.ID); err != nil || after.WorktreeRemovedAt == "" {
		t.Fatalf("the run view no longer offers Remove worktree: %+v, %v", after, err)
	}
}

func TestProjectSyncFetchesOnlyWhenStale(t *testing.T) {
	clone, teammate := syncedClone(t)
	service := workflowTestService(t, clone, writingRuntime())
	fetchHead := filepath.Join(clone, ".git", "FETCH_HEAD")
	age := func() {
		t.Helper()
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(fetchHead, old, old); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		service.syncMu.Lock()
		if service.syncAttempts != nil {
			service.syncAttempts["project"] = old
		}
		service.syncMu.Unlock()
	}
	age()

	local, err := service.ProjectSync("project", false)
	if err != nil {
		t.Fatal(err)
	}
	if local.Fetched || local.Summary.Checkout == nil || local.Summary.Checkout.State != worktree.SyncSynced {
		t.Fatalf("without fetchIfStale only local state is read: %+v", local)
	}
	fetched, err := service.ProjectSync("project", true)
	if err != nil {
		t.Fatal(err)
	}
	if !fetched.Fetched || fetched.Summary.Checkout.State != worktree.SyncBehind || fetched.Summary.Checkout.Behind != 1 || fetched.Summary.OutOfSync != 1 {
		t.Fatalf("a stale project is fetched first: %+v", fetched)
	}

	for _, args := range [][]string{{"commit", "-q", "--allow-empty", "-m", "merged again"}, {"push", "-q", "origin", "HEAD:main"}} {
		if err := runGit(teammate, args...); err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := service.ProjectSync("project", true)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Fetched || fresh.Summary.Checkout.Behind != 1 {
		t.Fatalf("a fetch minutes ago is fresh enough: %+v", fresh)
	}
	age()
	if again, err := service.ProjectSync("project", true); err != nil || !again.Fetched || again.Summary.Checkout.Behind != 2 {
		t.Fatalf("once stale it fetches again: %+v, %v", again, err)
	}

	if err := runGit(clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git")); err != nil {
		t.Fatal(err)
	}
	age()
	failed, err := service.ProjectSync("project", true)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Fetched || !strings.HasPrefix(failed.FetchError, "Could not fetch from origin") || failed.Summary.Checkout == nil {
		t.Fatalf("a failed fetch is reported beside the local summary: %+v", failed)
	}
	if retried, err := service.ProjectSync("project", true); err != nil || retried.FetchError != "" || retried.Fetched {
		t.Fatalf("a failed attempt also counts as recent, so it is not retried at once: %+v, %v", retried, err)
	}
}
