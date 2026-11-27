package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/diff"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// writingRuntime edits a file in the working directory of every writable
// step, so each one leaves a commit behind.
func writingRuntime() *workflowRuntime {
	return &workflowRuntime{handle: func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if request.Permission == domain.PermissionWorktree {
			name := filepath.Join(request.WorkingDir, "CHANGE-"+request.RunID+".md")
			if err := os.WriteFile(name, []byte("change"), 0o644); err != nil {
				return agentruntime.RunResult{}, err
			}
		}
		return agentruntime.RunResult{}, nil
	}}
}

// branchingRepository is a git repository on main with one commit, inside
// its own parent directory so sibling worktree locations stay in the test.
func branchingRepository(t *testing.T, config string) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repository, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		path := filepath.Join(repository, filepath.FromSlash(branching.RelativePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runGit(repository, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repository, "commit", "-q", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	return repository
}

func planAndStart(t *testing.T, service *Service, request WorkflowRequestView) (WorkflowPlanView, WorkflowRunView, error) {
	t.Helper()
	request.ProjectID = "project"
	plan, err := service.PlanWorkflow(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	return plan, run, err
}

func waitForRemoval(t *testing.T, service *Service, runID string) WorkflowRunView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		run, err := service.WorkflowRun(runID)
		if err != nil {
			t.Fatal(err)
		}
		// removeWorktree marks the live view before it saves the record, so
		// the view alone can show a removal the store does not have yet.
		stored, err := service.store.Execution(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if run.WorktreeRemovedAt != "" && !stored.WorktreeRemovedAt.IsZero() {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("worktree of run %s was not removed: %#v", runID, run)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func branchExists(repository, branch string) bool {
	command := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	command.Dir = repository
	return command.Run() == nil
}

func TestMissingBranchingConfigIsProposedAndWrittenOnApproval(t *testing.T) {
	repository := branchingRepository(t, "")
	// The project points at a subdirectory; the configuration still belongs
	// at the repository root, where planning reads it.
	subdirectory := filepath.Join(repository, "app")
	if err := os.Mkdir(subdirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, subdirectory, writingRuntime())
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Add the login page", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	branchPlan := plan.Workflow.Branching
	if branchPlan == nil || branchPlan.Branch != "feature/add-the-login-page" || branchPlan.Base != "main" || branchPlan.Cleanup != domain.CleanupManual ||
		branchPlan.Proposed["tracker"] != "none" || branchPlan.Root != filepath.Join(service.dataDir, "worktrees", "repository") {
		t.Fatalf("unexpected branch plan: %#v", branchPlan)
	}
	if !strings.Contains(strings.Join(branchPlan.Notes, "\n"), "inferred from the repository") {
		t.Fatalf("without an orchestrator the plan should say the values were inferred: %v", branchPlan.Notes)
	}
	if _, err := os.Stat(filepath.Join(repository, filepath.FromSlash(branching.RelativePath))); !os.IsNotExist(err) {
		t.Fatal("planning must not write the configuration")
	}
	encoded, err := json.Marshal(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := branching.Load(repository)
	if err != nil || !loaded.Complete() || loaded.Config.Branch.Pattern != "{type}/{slug}" {
		t.Fatalf("approval should write a complete configuration: %#v %v", loaded, err)
	}
	if _, err := os.Stat(filepath.Join(subdirectory, filepath.FromSlash(branching.RelativePath))); !os.IsNotExist(err) {
		t.Fatal("the configuration must be written at the repository root, not the project directory")
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	want := filepath.Join(service.dataDir, "worktrees", "repository", "feature", "add-the-login-page")
	if run.Branch != "feature/add-the-login-page" || run.Worktree != want || run.Cleanup != domain.CleanupManual || run.LastCommit == nil {
		t.Fatalf("unexpected run: %#v", run)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal("the manual policy keeps the worktree after the run")
	}

	removed, err := service.RemoveRunWorktree(run.ID)
	if err != nil || removed.WorktreeRemovedAt == "" {
		t.Fatalf("RemoveRunWorktree = %#v, %v", removed, err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) || !branchExists(repository, run.Branch) {
		t.Fatal("manual removal must delete the directory and keep the branch")
	}
	if change, err := service.RunChanges(run.ID, diff.Scope{Kind: diff.ScopeCommit, Commit: run.LastCommit.Hash}); err != nil || len(change.Files) == 0 {
		t.Fatalf("the run's commit must stay readable after removal: %#v %v", change, err)
	}
	if _, err := service.RemoveRunWorktree(run.ID); err == nil {
		t.Fatal("removing twice must be refused")
	}
}

type fakeProposer struct {
	proposal branching.Proposal
	request  orchestrator.BranchingRequest
}

func (fake *fakeProposer) ProposeBranching(_ context.Context, request orchestrator.BranchingRequest, options orchestrator.Options, _ agentruntime.EventSink) (branching.Proposal, orchestrator.Call, error) {
	fake.request = request
	return fake.proposal, orchestrator.Call{RunID: options.RunID, Outcome: domain.OutcomeCompleted}, nil
}

func TestPlanBranchingValidatesTheOrchestratorsProposal(t *testing.T) {
	repository := branchingRepository(t, "version: 1\nbranch:\n  maxLength: 40\nworktrees:\n  cleanup: listener\n")
	service := workflowTestService(t, repository, writingRuntime())
	proposer := &fakeProposer{proposal: branching.Proposal{Tracker: branching.TrackerLinear, Base: "develop", Pattern: "{type}/{story}-{slug}"}}
	task := domain.TaskSpec{Kind: domain.TaskImplementation, Objective: "MAD-7: Invite a carer", Labels: []string{"enabler"}}
	workflow := domain.WorkflowSpec{Steps: []domain.WorkflowStep{{ID: "worker", Kind: domain.StepAgent, Permission: domain.PermissionWorktree}}}
	plan, _, err := service.planBranching(context.Background(), repository, task, workflow, proposer, orchestrator.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(proposer.request.Missing, ",") != "tracker,branch.base,branch.pattern" || proposer.request.Evidence.CurrentBranch != "main" {
		t.Fatalf("the orchestrator should see the missing keys and the evidence: %#v", proposer.request)
	}
	if plan.Proposed["tracker"] != "linear" || plan.Proposed["branch.base"] != "main" || plan.Branch != "enabler/MAD-7-invite-a-carer" || plan.Cleanup != domain.CleanupListener || plan.MaxLength != 40 {
		t.Fatalf("valid values should be used and a base the repository lacks replaced: %#v", plan)
	}
	if _, present := plan.Proposed["worktrees.location"]; present {
		t.Fatal("an existing file only gains its missing required keys")
	}
	readOnly := domain.WorkflowSpec{Steps: []domain.WorkflowStep{{ID: "worker", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly}}}
	proposer.request = orchestrator.BranchingRequest{}
	plan, _, err = service.planBranching(context.Background(), repository, task, readOnly, proposer, orchestrator.Options{}, nil)
	if err != nil || plan == nil || plan.Proposed != nil || plan.Cleanup != domain.CleanupListener || proposer.request.Missing != nil {
		t.Fatalf("a read-only run carries only the cleanup policy and asks for nothing: %#v %v", plan, err)
	}
}

func TestListenerRemovesWorktreeWhenRunFinishes(t *testing.T) {
	repository := branchingRepository(t, "version: 1\ntracker: github\nbranch:\n  base: main\n  pattern: \"{type}/{story}-{slug}\"\nworktrees:\n  enabled: true\n  location: ../{repo}-worktrees\n  cleanup: listener\n")
	service := workflowTestService(t, repository, writingRuntime())
	plan, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the login crash #42", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Workflow.Branching.Proposed != nil || plan.Workflow.Branching.Branch != "bug/42-fix-the-login-crash" {
		t.Fatalf("a complete configuration needs no proposal: %#v", plan.Workflow.Branching)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	run = waitForRemoval(t, service, run.ID)
	parent, err := filepath.EvalSymlinks(filepath.Dir(repository))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(parent, "repository-worktrees", "bug", "42-fix-the-login-crash")
	if run.Worktree != want || run.Cleanup != domain.CleanupListener {
		t.Fatalf("unexpected run: %#v", run)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) || !branchExists(repository, "bug/42-fix-the-login-crash") {
		t.Fatal("the listener must remove the directory and keep the branch")
	}
	stored, err := service.store.Execution(context.Background(), run.ID)
	if err != nil || stored.WorktreeRemovedAt.IsZero() || stored.Branch != "bug/42-fix-the-login-crash" {
		t.Fatalf("removal was not persisted: %#v %v", stored, err)
	}

	// The same objective again gets a numbered branch rather than failing.
	_, again, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the login crash #42", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	again = waitForWorkflowRun(t, service, again.ID, "completed")
	if again.Branch != "bug/42-fix-the-login-crash-2" {
		t.Fatalf("a taken branch should be numbered, got %q", again.Branch)
	}
}

func TestPruneSweepsFinishedRunsWhenANewRunStarts(t *testing.T) {
	repository := branchingRepository(t, "version: 1\ntracker: none\nbranch:\n  base: main\n  pattern: \"{type}/{slug}\"\nworktrees:\n  enabled: true\n  cleanup: prune\n")
	service := workflowTestService(t, repository, writingRuntime())
	_, first, err := planAndStart(t, service, WorkflowRequestView{Objective: "First change", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	first = waitForWorkflowRun(t, service, first.ID, "completed")
	if _, err := os.Stat(first.Worktree); err != nil {
		t.Fatal("prune keeps a finished run's worktree until the next run")
	}
	_, second, err := planAndStart(t, service, WorkflowRequestView{Objective: "Second change", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	second = waitForWorkflowRun(t, service, second.ID, "completed")
	waitForRemoval(t, service, first.ID)
	if _, err := os.Stat(first.Worktree); !os.IsNotExist(err) || !branchExists(repository, first.Branch) {
		t.Fatal("the next run must prune the first run's worktree and keep its branch")
	}
	if _, err := os.Stat(second.Worktree); err != nil {
		t.Fatal("the running run's own worktree must survive the prune")
	}
	if !strings.Contains(strings.Join(second.WorkspaceNotes, "\n"), "Pruned 1") {
		t.Fatalf("the run should say what it pruned: %v", second.WorkspaceNotes)
	}
}

func TestCheckoutRunWithProposedConfigAsksToCommitItFirst(t *testing.T) {
	repository := branchingRepository(t, "version: 1\nworktrees:\n  enabled: false\n")
	if err := runGit(repository, "switch", "-q", "-c", "work"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repository, writingRuntime())
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Tidy the readme", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "commit that file and approve again") {
		t.Fatalf("approval should write the config and ask for it to be committed, got %v", err)
	}
	if loaded, err := branching.Load(repository); err != nil || !loaded.Complete() || loaded.Config.Worktrees.Enabled {
		t.Fatalf("the proposed keys should be written beside the existing ones: %#v %v", loaded, err)
	}
	if err := runGit(repository, "commit", "-q", "-am", "branching config"); err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if run = waitForWorkflowRun(t, service, run.ID, "completed"); run.Branch != "work" {
		t.Fatalf("the run should commit on work: %#v", run)
	}
}

func TestCheckoutRunsCommitOnTheCurrentBranchOnlyWhenSafe(t *testing.T) {
	repository := branchingRepository(t, "version: 1\ntracker: none\nbranch:\n  base: main\n  pattern: \"{type}/{slug}\"\nworktrees:\n  enabled: false\n")
	service := workflowTestService(t, repository, writingRuntime())
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Tidy the readme", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Workflow.Branching.Worktree || plan.Workflow.Branching.Cleanup != domain.CleanupCheckout || plan.Workflow.Branching.Branch != "" {
		t.Fatalf("worktrees off should plan a checkout run: %#v", plan.Workflow.Branching)
	}
	encoded, err := json.Marshal(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "base branch main") {
		t.Fatalf("a run on the base branch must be refused, got %v", err)
	}
	if err := runGit(repository, "switch", "-q", "-c", "work"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("a dirty checkout must be refused, got %v", err)
	}
	if err := os.Remove(filepath.Join(repository, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	if run.Worktree != repository || run.Branch != "work" || run.Cleanup != domain.CleanupCheckout || run.LastCommit == nil {
		t.Fatalf("unexpected checkout run: %#v", run)
	}
	command := exec.Command("git", "log", "-1", "--format=%an", "work")
	command.Dir = repository
	if author, err := command.Output(); err != nil || strings.TrimSpace(string(author)) != "Inkuna" {
		t.Fatalf("the run should have committed on work: %q %v", author, err)
	}
	if _, err := service.RemoveRunWorktree(run.ID); err == nil {
		t.Fatal("the project checkout must never be removed")
	}
	if err := service.claimCheckout(repository, "other-run"); err != nil {
		t.Fatalf("the checkout should be released after the run: %v", err)
	}
}

func TestRunStartsFromTheLastFetchedCopyWhenOriginNeverAnswers(t *testing.T) {
	repository := branchingRepository(t, "version: 1\ntracker: none\nbranch:\n  base: main\n  pattern: \"{type}/{slug}\"\nworktrees:\n  enabled: true\n  cleanup: manual\n")
	// The last fetch brought in a commit on origin's main that the local
	// main does not have yet.
	if err := os.WriteFile(filepath.Join(repository, "FETCHED.md"), []byte("merged"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "FETCHED.md"},
		{"commit", "-q", "-m", "merged elsewhere"},
		{"update-ref", "refs/remotes/origin/main", "HEAD"},
		{"reset", "-q", "--hard", "HEAD~1"},
		{"remote", "add", "origin", "ssh://git@example.invalid/app.git"},
	} {
		if err := runGit(repository, args...); err != nil {
			t.Fatal(err)
		}
	}
	hang, err := sshCommand(hangSSH)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", hang)
	// Naming the variant skips git's `ssh -G` probe, so the fetch waits in
	// the connection, where ssh holds git's stderr, as it does on a locked
	// agent.
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	previous := baseFetchTimeout
	baseFetchTimeout = 300 * time.Millisecond
	t.Cleanup(func() { baseFetchTimeout = previous })

	service := workflowTestService(t, repository, writingRuntime())
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Add the export", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	// The ssh outlasts the wait, so the run completes only if it gave up on
	// origin. Timing the run itself failed on slow Windows runners, though
	// the fetch had given up on time; worktree's own test times the fetch.
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	if _, err := os.Stat(filepath.Join(run.Worktree, "FETCHED.md")); err != nil {
		t.Fatalf("the run must start from the last fetched origin/main: %v", err)
	}
	// What setup noticed is an event on the first writable step.
	events, _, err := service.store.RoundEvents(context.Background(), run.ID, run.Round, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var summaries []string
	for _, event := range events {
		summaries = append(summaries, event.Summary)
	}
	if !strings.Contains(strings.Join(summaries, "\n"), "Could not fetch origin/main; starting from the last fetched copy: fetch timed out: origin did not answer within 300ms") {
		t.Fatalf("the run should say the fetch timed out: %q", summaries)
	}
}
