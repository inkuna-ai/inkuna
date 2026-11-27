package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// baseFetchTimeout bounds the fetch of a run's base as its first writable
// step starts. It is a variable so tests can wait less than the default.
var baseFetchTimeout = worktree.FetchTimeout

// planBranching derives where a planned run works from the repository's
// branching configuration. When the configuration is missing or lacks
// required keys and the run has writable steps, the orchestrator proposes
// the missing values from the repository's branches; Go validates each one,
// infers any it rejects, and the plan carries them so approval writes them.
// It also returns that proposal's call, which is nil when none was made.
func (service *Service) planBranching(ctx context.Context, root string, task domain.TaskSpec, workflow domain.WorkflowSpec, proposer orchestrator.BranchingProposer, options orchestrator.Options, sink agentruntime.EventSink) (*domain.BranchPlan, *orchestrator.Call, error) {
	writable := false
	for _, step := range workflow.Steps {
		if step.Kind == domain.StepAgent && step.Permission == domain.PermissionWorktree {
			writable = true
		}
	}
	loaded, err := branching.Load(root)
	if err != nil {
		return nil, nil, err
	}
	if !writable {
		if !loaded.Exists {
			return nil, nil, nil
		}
		// Nothing is named or created; the plan only carries the cleanup
		// policy so prune still sweeps when this run starts.
		return &domain.BranchPlan{Worktree: loaded.Config.Worktrees.Enabled, Base: loaded.Config.Branch.Base, Cleanup: string(loaded.Config.Worktrees.Cleanup)}, nil, nil
	}
	config := loaded.Config
	plan := &domain.BranchPlan{}
	var made *orchestrator.Call
	if len(loaded.Missing) > 0 {
		evidence, err := worktree.Evidence(ctx, root)
		if err != nil {
			return nil, made, fmt.Errorf("inspect branches for the branching config: %w", err)
		}
		var offer *branching.Proposal
		if proposer != nil {
			options.RunID = "branching-" + task.ID
			proposal, call, err := proposer.ProposeBranching(ctx, orchestrator.BranchingRequest{Missing: loaded.Missing, Evidence: evidence}, options, sink)
			if call.Outcome != "" {
				made = &call
			}
			if err != nil {
				plan.Notes = append(plan.Notes, "The orchestrator could not propose the branching config ("+err.Error()+"); values were inferred from the repository.")
			} else {
				offer = &proposal
			}
		}
		var notes []string
		config, plan.Proposed, notes = branching.Resolve(config, loaded.Missing, offer, evidence)
		plan.Notes = append(plan.Notes, notes...)
		plan.Notes = append(plan.Notes, fmt.Sprintf("Approving this plan writes these values to %s; commit it to keep them.", branching.RelativePath))
	}
	if err := config.Validate(); err != nil {
		return nil, made, fmt.Errorf("branching config: %w", err)
	}
	plan.Worktree, plan.Base, plan.MaxLength = config.Worktrees.Enabled, config.Branch.Base, config.Branch.MaxLength
	if !config.Worktrees.Enabled {
		plan.Cleanup = domain.CleanupCheckout
		return plan, made, nil
	}
	plan.Cleanup = string(config.Worktrees.Cleanup)
	if plan.Branch, err = config.ForTask(task); err != nil {
		return nil, made, fmt.Errorf("name the run's branch: %w", err)
	}
	if plan.Root, err = config.Root(root, service.dataDir); err != nil {
		return nil, made, err
	}
	return plan, made, nil
}

// checkoutReady guards a run that works in the project checkout instead of
// a worktree. Each step's commit stages everything, so the checkout must be
// clean or the user's own edits would land in Inkuna's commit; it must be
// on a branch; and that branch may not be the base, since committing there
// would integrate unreviewed work. It returns the checked-out branch.
func checkoutReady(ctx context.Context, repository, base string) (string, error) {
	branch, err := worktree.CurrentBranch(ctx, repository)
	if err != nil {
		return "", err
	}
	if branch == "" {
		return "", errors.New("worktrees are off and the checkout has a detached HEAD; check out the branch this run should commit to")
	}
	if branch == base {
		return "", fmt.Errorf("worktrees are off and the checkout is on the base branch %s; switch to a working branch first (inkunactl branch new creates one)", base)
	}
	status, err := worktree.Changes(ctx, repository)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) != "" {
		return "", fmt.Errorf("worktrees are off and the checkout has uncommitted changes, which the run's commits would include; commit or stash them first:\n%s", status)
	}
	return branch, nil
}

// claimCheckout lets one run at a time write in a project checkout.
func (service *Service) claimCheckout(repository, runID string) error {
	key := filepath.Clean(repository)
	service.runsMu.Lock()
	defer service.runsMu.Unlock()
	if service.checkouts == nil {
		service.checkouts = make(map[string]string)
	}
	if holder, ok := service.checkouts[key]; ok && holder != runID {
		return fmt.Errorf("run %s is already writing in %s; with worktrees off only one run at a time can", holder, repository)
	}
	service.checkouts[key] = runID
	return nil
}

func (service *Service) releaseCheckout(runID string) {
	service.runsMu.Lock()
	defer service.runsMu.Unlock()
	for key, holder := range service.checkouts {
		if holder == runID {
			delete(service.checkouts, key)
		}
	}
}

// workspace is where a run's writable steps work and what setting it up
// noticed along the way.
type workspace struct {
	path, branch, cleanup string
	notes                 []string
}

// prepareWorkspace sets up the first writable step's working directory from
// the approved branch plan: a new branch in a new worktree, or the project
// checkout as it is. Plans from before branching configuration keep the
// original inkuna/<run>-<step> worktree in the data directory.
func (service *Service) prepareWorkspace(ctx context.Context, executionID string, project storage.Project, spec domain.WorkflowSpec, step domain.WorkflowStep) (workspace, error) {
	plan := spec.Branching
	if plan == nil || plan.Branch == "" && plan.Worktree {
		created, err := (worktree.Manager{Repository: project.Repository, Root: filepath.Join(service.dataDir, "worktrees")}).Create(ctx, executionID+"-"+step.ID, "HEAD")
		if err != nil {
			return workspace{}, err
		}
		return workspace{path: created.Path, branch: created.Branch, cleanup: domain.CleanupManual}, nil
	}
	if !plan.Worktree {
		if err := service.claimCheckout(project.Repository, executionID); err != nil {
			return workspace{}, err
		}
		branch, err := checkoutReady(ctx, project.Repository, plan.Base)
		if err != nil {
			service.releaseCheckout(executionID)
			return workspace{}, err
		}
		return workspace{path: project.Repository, branch: branch, cleanup: domain.CleanupCheckout, notes: []string{"Working in the checkout on " + branch + " (worktrees are off)"}}, nil
	}
	result := workspace{cleanup: plan.Cleanup}
	if worktree.HasOrigin(ctx, project.Repository) {
		if err := worktree.Fetch(ctx, project.Repository, plan.Base, baseFetchTimeout); err != nil {
			result.notes = append(result.notes, "Could not fetch origin/"+plan.Base+"; starting from the last fetched copy: "+err.Error())
		}
	}
	start, err := worktree.StartPoint(ctx, project.Repository, plan.Base)
	if err != nil {
		return workspace{}, err
	}
	service.branchMu.Lock()
	defer service.branchMu.Unlock()
	name, err := branching.Unique(plan.Branch, plan.MaxLength, func(candidate string) bool {
		if worktree.BranchTaken(ctx, project.Repository, candidate) {
			return true
		}
		_, statErr := os.Stat(branching.Path(plan.Root, candidate))
		return statErr == nil
	})
	if err != nil {
		return workspace{}, err
	}
	if name != plan.Branch {
		result.notes = append(result.notes, fmt.Sprintf("Branch %s was taken; using %s", plan.Branch, name))
	}
	created, err := worktree.AddBranch(ctx, project.Repository, name, branching.Path(plan.Root, name), start)
	if err != nil {
		return workspace{}, err
	}
	result.path, result.branch = created.Path, created.Branch
	result.notes = append(result.notes, fmt.Sprintf("Created %s from %s in %s", created.Branch, start, created.Path))
	return result, nil
}

// cleanupAfterRun removes the run's worktree under the listener policy, now
// that the run has finished.
func (service *Service) cleanupAfterRun(runID string, project storage.Project) {
	execution, err := service.execution(context.Background(), runID)
	if err != nil || execution.Cleanup != domain.CleanupListener || execution.Worktree == "" || !execution.WorktreeRemovedAt.IsZero() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := service.removeWorktree(ctx, project, execution); err != nil {
		service.noteWorkspace(runID, "Kept the worktree: "+err.Error())
	}
}

// pruneWorktrees removes the worktrees of the project's finished runs, as
// the prune policy does when a run starts. Worktrees with uncommitted
// changes and checkouts are kept; branches always are.
func (service *Service) pruneWorktrees(ctx context.Context, project storage.Project, currentRunID string) {
	executions, err := service.store.ExecutionsWithWorktrees(ctx, project.ID)
	if err != nil {
		service.noteWorkspace(currentRunID, "Could not list worktrees to prune: "+err.Error())
		return
	}
	removed := 0
	for _, execution := range executions {
		if live, err := service.execution(ctx, execution.ID); err == nil {
			execution = live
		}
		if execution.ID == currentRunID || !terminalRunState(execution.State) || !removable(project, execution) {
			continue
		}
		if err := service.removeWorktree(ctx, project, execution); err != nil {
			service.noteWorkspace(currentRunID, fmt.Sprintf("Kept the worktree of run %s: %v", execution.ID, err))
			continue
		}
		removed++
	}
	_ = worktree.Prune(ctx, project.Repository)
	if removed > 0 {
		service.noteWorkspace(currentRunID, fmt.Sprintf("Pruned %d finished run worktree(s); their branches are kept", removed))
	}
}

// removable reports whether Inkuna created the run's worktree and may
// remove it: never the project checkout itself.
func removable(project storage.Project, execution storage.Execution) bool {
	return execution.Worktree != "" && execution.Cleanup != domain.CleanupCheckout &&
		filepath.Clean(execution.Worktree) != filepath.Clean(project.Repository) && execution.WorktreeRemovedAt.IsZero()
}

// removeWorktree removes one run's worktree and records it, keeping the
// branch. The live view and the stored record are both updated so a later
// persist of the live view cannot bring the worktree back.
func (service *Service) removeWorktree(ctx context.Context, project storage.Project, execution storage.Execution) error {
	if !removable(project, execution) {
		return errors.New("this run has no worktree Inkuna can remove")
	}
	// Callers saw the run finished, but it may have been sent back since;
	// workspaceMu keeps it from being sent back while this checks and
	// removes.
	service.workspaceMu.Lock()
	defer service.workspaceMu.Unlock()
	current, err := service.execution(ctx, execution.ID)
	if err != nil {
		return fmt.Errorf("check the run before removing its worktree: %w", err)
	}
	if !terminalRunState(current.State) {
		return errors.New("the run was sent back and is active again, so its worktree stays")
	}
	if err := worktree.Remove(ctx, project.Repository, execution.Worktree); err != nil {
		if errors.Is(err, worktree.ErrDirty) {
			return fmt.Errorf("%s has uncommitted changes; commit or discard them first", execution.Worktree)
		}
		return err
	}
	removedAt := time.Now().UTC()
	execution.WorktreeRemovedAt = removedAt
	service.runsMu.Lock()
	if run := service.runs[execution.ID]; run != nil {
		run.view.WorktreeRemovedAt = removedAt.Format(time.RFC3339Nano)
		execution = executionRecord(run.view)
	}
	service.runsMu.Unlock()
	if err := service.store.SaveExecution(ctx, execution); err != nil {
		return fmt.Errorf("record worktree removal: %w", err)
	}
	service.emit("inkuna:runs", execution.ID)
	return nil
}

// RemoveRunWorktree removes a finished run's worktree on request, as the
// manual policy expects; under the others it frees one before its time.
// Only the checkout directory goes; the branch and its commits stay.
func (service *Service) RemoveRunWorktree(runID string) (WorkflowRunView, error) {
	if err := service.ready(); err != nil {
		return WorkflowRunView{}, err
	}
	ctx := service.context()
	execution, err := service.execution(ctx, runID)
	if err != nil {
		return WorkflowRunView{}, err
	}
	if !terminalRunState(execution.State) {
		return WorkflowRunView{}, errors.New("the run is still active; remove its worktree once it finishes")
	}
	project, err := service.store.Project(ctx, execution.ProjectID)
	if err != nil {
		return WorkflowRunView{}, fmt.Errorf("load project: %w", err)
	}
	if err := service.removeWorktree(ctx, project, execution); err != nil {
		return WorkflowRunView{}, err
	}
	return service.WorkflowRun(runID)
}

// execution returns a run's record from its live view while it is in memory,
// which is ahead of the stored copy, and from storage otherwise.
func (service *Service) execution(ctx context.Context, runID string) (storage.Execution, error) {
	service.runsMu.RLock()
	run := service.runs[runID]
	var record storage.Execution
	if run != nil {
		record = executionRecord(run.view)
	}
	service.runsMu.RUnlock()
	if run != nil {
		return record, nil
	}
	return service.store.Execution(ctx, runID)
}

// noteWorkspace records what cleanup or setup did on the live run view.
func (service *Service) noteWorkspace(runID, note string) {
	service.updateRun(runID, func(view *WorkflowRunView) {
		view.WorkspaceNotes = append(view.WorkspaceNotes, note)
	})
	service.emit("inkuna:runs", runID)
}
