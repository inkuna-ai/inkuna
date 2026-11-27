package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/diff"
	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

func TestARunsChangesAreListedFromTheCommitItStartedFrom(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, "greet.go"), []byte("package greet\n\nconst Greeting = \"Hello\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	base := gitOutput(t, repositoryPath, "rev-parse", "HEAD")
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if err := os.WriteFile(filepath.Join(request.WorkingDir, "greet.go"), []byte("package greet\n\nconst Greeting = \"Hello there\"\n"), 0o644); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Changed the greeting"})}, nil
	}
	service := workflowTestService(t, repositoryPath, runtime)
	spec := workflowTestSpec(t, service)
	spec.Steps[0].Agent = "implementer"
	spec.Steps[0].Permission = domain.PermissionWorktree
	spec.Steps[0].AllowedPaths = []string{"."}
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.BaseCommit != base || completed.LastCommit == nil {
		t.Fatalf("the run should start from %s: %#v", base, completed)
	}
	if stored, err := service.store.Execution(context.Background(), run.ID); err != nil || stored.BaseCommit != base {
		t.Fatalf("the base commit was not stored: %#v %v", stored, err)
	}

	// A commit made in the main checkout afterwards is not on the run's branch.
	if err := os.WriteFile(filepath.Join(repositoryPath, "other.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "-m", "later work"); err != nil {
		t.Fatal(err)
	}
	later := gitOutput(t, repositoryPath, "rev-parse", "HEAD")

	change, err := service.RunChanges(run.ID, diff.Scope{Kind: diff.ScopeBranch})
	if err != nil {
		t.Fatal(err)
	}
	if change.From != base || change.To != completed.LastCommit.Hash || len(change.Files) != 1 || change.Files[0].Path != "greet.go" || change.Additions != 1 || change.Deletions != 1 {
		t.Fatalf("branch change %+v", change)
	}
	patch, err := service.RunFilePatch(run.ID, diff.Scope{Kind: diff.ScopeRange, From: change.From, To: change.To}, "greet.go", false)
	if err != nil || len(patch.Hunks) != 1 || patch.Language != "Go" {
		t.Fatalf("file patch %+v, %v", patch, err)
	}
	if commit, err := service.RunChanges(run.ID, diff.Scope{Kind: diff.ScopeCommit, Commit: completed.LastCommit.Hash}); err != nil || len(commit.Files) != 1 {
		t.Fatalf("commit scope %+v, %v", commit, err)
	}
	if _, err := service.RunChanges(run.ID, diff.Scope{Kind: diff.ScopeCommit, Commit: later}); err == nil || !strings.Contains(err.Error(), "not on branch") {
		t.Fatalf("a commit off the run's branch must be refused: %v", err)
	}
}

func gitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func TestARunsCommitsExcerptsAndViewedMarks(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	var original strings.Builder
	original.WriteString("package greet\n")
	for number := 2; number <= 30; number++ {
		original.WriteString("const c" + strings.Repeat("x", number) + " = 1\n")
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, "greet.go"), []byte(original.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		changed := strings.Replace(original.String(), " = 1\n", " = 2\n", 1)
		if err := os.WriteFile(filepath.Join(request.WorkingDir, "greet.go"), []byte(changed), 0o644); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Changed a constant"})}, nil
	}
	service := workflowTestService(t, repositoryPath, runtime)
	spec := workflowTestSpec(t, service)
	spec.Steps[0].Agent = "implementer"
	spec.Steps[0].Permission = domain.PermissionWorktree
	spec.Steps[0].AllowedPaths = []string{"."}
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)
	completed := waitForWorkflowRun(t, service, run.ID, "completed")

	// A commit made by hand on the run's branch is listed without a step.
	byHand := completed.Worktree
	if _, err := os.Stat(byHand); err != nil {
		byHand = filepath.Join(t.TempDir(), "by-hand")
		if err := runGit(repositoryPath, "worktree", "add", byHand, completed.Branch); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(byHand, "notes.txt"), []byte("by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(byHand, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(byHand, "commit", "-m", "by hand"); err != nil {
		t.Fatal(err)
	}
	commits, err := service.RunCommits(run.ID)
	if err != nil || len(commits) != 2 || commits[0].Hash != completed.LastCommit.Hash || commits[0].StepID != "worker" || commits[0].Agent != "implementer" || commits[0].External || commits[1].Subject != "by hand" || commits[1].StepID != "" || !commits[1].External {
		t.Fatalf("commits %#v, %v", commits, err)
	}
	if step, byHand := commits[0], commits[1]; step.Files != 1 || step.Additions != 1 || step.Deletions != 1 || byHand.Files != 1 || byHand.Additions != 1 || byHand.Deletions != 0 {
		t.Fatalf("commit counts %+v %+v", step, byHand)
	}
	// A commit recorded before agents were kept takes its step's agent from
	// the run's workflow.
	if err := service.store.SaveCommit(context.Background(), storage.CommitRecord{ExecutionID: run.ID, StepID: "worker", Hash: commits[0].Hash, Subject: commits[0].Subject}); err != nil {
		t.Fatal(err)
	}
	if again, err := service.RunCommits(run.ID); err != nil || again[0].Agent != "implementer" {
		t.Fatalf("agent from the workflow %#v, %v", again, err)
	}

	change, err := service.RunChanges(run.ID, diff.Scope{Kind: diff.ScopeCommit, Commit: commits[0].Hash})
	if err != nil || len(change.Files) != 1 || change.Files[0].Blob == "" {
		t.Fatalf("commit change %#v, %v", change, err)
	}
	between := diff.Scope{Kind: diff.ScopeRange, From: change.From, To: change.To}
	excerpt, err := service.RunFileExcerpt(run.ID, between, "greet.go", 10, 10, 2)
	if err != nil || len(excerpt.Lines) != 2 || excerpt.Lines[0].New != 10 || excerpt.NewTotal != 30 {
		t.Fatalf("excerpt %#v, %v", excerpt, err)
	}

	file := change.Files[0]
	if err := service.SetFileViewed(run.ID, file.Path, file.Blob, true); err != nil {
		t.Fatal(err)
	}
	if viewed, err := service.RunViewed(run.ID); err != nil || len(viewed) != 1 || viewed[0].Path != file.Path || viewed[0].Blob != file.Blob {
		t.Fatalf("viewed %#v, %v", viewed, err)
	}
	for name, mark := range map[string]func() error{
		"a blob that is not a hash": func() error { return service.SetFileViewed(run.ID, file.Path, "HEAD", true) },
		"no path":                   func() error { return service.SetFileViewed(run.ID, "", file.Blob, true) },
		"another run":               func() error { return service.SetFileViewed("missing", file.Path, file.Blob, true) },
	} {
		if err := mark(); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if err := service.SetFileViewed(run.ID, file.Path, file.Blob, false); err != nil {
		t.Fatal(err)
	}
	if viewed, err := service.RunViewed(run.ID); err != nil || len(viewed) != 0 {
		t.Fatalf("the mark should be cleared: %#v, %v", viewed, err)
	}
}
