package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/branching"
)

// pullList is gh pr list's answer: each entry a pull request opened from a
// branch, by state, from this repository unless fork is set.
func pullList(t *testing.T, pulls ...map[string]any) string {
	t.Helper()
	data, err := json.Marshal(pulls)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func pull(number int, branch, state string, fork bool) map[string]any {
	entry := map[string]any{"number": number, "title": "Pull request " + branch, "url": "https://github.com/acme/app/pull/" + branch, "state": state, "headRefName": branch, "isCrossRepository": fork}
	if state == "MERGED" {
		entry["mergedAt"] = "2026-11-04T10:00:00Z"
	}
	return entry
}

func TestAMergedPullRequestIsLinkedAndItsCleanWorktreeGoesOnlyUnderMergedCleanup(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	if err := runGit(repository, "remote", "add", "origin", "https://github.com/acme/app.git"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repository, writingRuntime())
	gh := &scriptedGitHub{answers: map[string]func([]string) (string, error){}}
	service.github = gh

	merged := finishedTask(t, service, "Fix the login crash #42")
	dirty := finishedTask(t, service, "Fix the logout crash #43")
	open := finishedTask(t, service, "Fix the signup crash #44")
	forked := finishedTask(t, service, "Fix the export crash #45")
	active := finishedTask(t, service, "Fix the import crash #46")
	if err := os.WriteFile(filepath.Join(dirty.Worktree, "SCRATCH.md"), []byte("not committed"), 0o644); err != nil {
		t.Fatal(err)
	}
	gh.answer("pr list", pullList(t,
		pull(12, merged.Branch, "MERGED", false),
		pull(13, dirty.Branch, "MERGED", false),
		pull(14, open.Branch, "OPEN", false),
		pull(15, forked.Branch, "MERGED", true),
		pull(16, active.Branch, "MERGED", false),
	))

	// Under manual cleanup a check only links the pull requests.
	check, err := service.CheckPullRequests(false)
	if err != nil || check.Checked != 1 || check.Linked != 4 || len(check.Removed) != 0 {
		t.Fatalf("check = %+v, %v", check, err)
	}
	runs, err := service.WorkflowRuns()
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]WorkflowRunView, len(runs))
	for _, run := range runs {
		byID[run.ID] = run
	}
	if link := byID[merged.ID].PullRequest; link == nil || link.Number != 12 || link.State != "MERGED" || link.MergedAt == "" {
		t.Fatalf("the merged run's pull request = %+v", link)
	}
	if link := byID[open.ID].PullRequest; link == nil || link.State != "OPEN" || link.MergedAt != "" {
		t.Fatalf("the open run's pull request = %+v", link)
	}
	if link := byID[forked.ID].PullRequest; link != nil {
		t.Fatalf("a fork's pull request of the same branch name was linked: %+v", link)
	}
	if detail, err := service.WorkflowRun(merged.ID); err != nil || detail.PullRequest == nil || detail.PullRequest.Number != 12 {
		t.Fatalf("the run's detail carries its pull request: %+v, %v", detail.PullRequest, err)
	}
	if _, err := os.Stat(merged.Worktree); err != nil {
		t.Fatalf("manual cleanup removed a worktree: %v", err)
	}

	// A check a moment later reads nothing unless a person asks.
	calls := len(gh.ran("pr", "list"))
	if check, err := service.CheckPullRequests(false); err != nil || check.Checked != 0 || len(gh.ran("pr", "list")) != calls {
		t.Fatalf("a fresh check read again: %+v, %v", check, err)
	}

	// Choosing merged cleanup clears the runs from before at the next check.
	// One sent back is active again, so its worktree stays.
	merging := strings.Replace(manualWorktrees, "cleanup: manual", "cleanup: "+string(branching.CleanupMerged), 1)
	if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(branching.RelativePath)), []byte(merging), 0o644); err != nil {
		t.Fatal(err)
	}
	service.updateRun(active.ID, func(view *WorkflowRunView) { view.State = "queued" })
	defer service.updateRun(active.ID, func(view *WorkflowRunView) { view.State = "completed" })
	check, err = service.CheckPullRequests(true)
	if err != nil || check.Checked != 1 || len(check.Removed) != 1 || check.Removed[0] != merged.Worktree {
		t.Fatalf("check = %+v, %v", check, err)
	}
	if _, err := os.Stat(merged.Worktree); !errors.Is(err, os.ErrNotExist) || !branchExists(repository, merged.Branch) {
		t.Fatalf("the merged run's clean worktree goes and its branch stays: %v", err)
	}
	if len(check.Problems) != 1 || !strings.Contains(check.Problems[0], dirty.Worktree) || !strings.Contains(check.Problems[0], "uncommitted changes") {
		t.Fatalf("a dirty worktree is kept and says why: %v", check.Problems)
	}
	for _, kept := range []WorkflowRunView{dirty, open, forked, active} {
		if _, err := os.Stat(kept.Worktree); err != nil {
			t.Fatalf("%s's worktree was removed: %v", kept.Title, err)
		}
	}
	if after, err := service.WorkflowRun(merged.ID); err != nil || after.WorktreeRemovedAt == "" {
		t.Fatalf("the removal is recorded on the run: %+v, %v", after.WorktreeRemovedAt, err)
	}
	if !taskExists(t, service, merged.TaskID) {
		t.Fatal("a merge deleted the task's history")
	}
}

func TestAPullRequestCheckSaysWhyItCouldNotRead(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	if err := runGit(repository, "remote", "add", "origin", "https://github.com/acme/app.git"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repository, writingRuntime())
	gh := &scriptedGitHub{answers: map[string]func([]string) (string, error){
		"pr list": func([]string) (string, error) { return "HTTP 401: Bad credentials", errors.New("exit status 1") },
	}}
	service.github = gh
	finishedTask(t, service, "Fix the login crash #42")
	check, err := service.CheckPullRequests(true)
	if err != nil || check.Checked != 0 || len(check.Problems) != 1 || !strings.Contains(check.Problems[0], "Bad credentials") {
		t.Fatalf("check = %+v, %v", check, err)
	}
}
