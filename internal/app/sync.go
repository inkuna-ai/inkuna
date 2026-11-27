package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// fetchTimeout bounds a fetch from origin; a remote that never answers
// fails rather than leaving the view waiting.
const fetchTimeout = 2 * time.Minute

// syncFreshness is how long a fetch, or a failed attempt, keeps the project
// cards from fetching again. The cards show on every visit to Projects.
const syncFreshness = 10 * time.Minute

// BranchSyncView is a project's local branches measured against origin.
// FetchError reports a failed fetch beside the local state, which is still
// worth showing offline; Update, Deleted, and Shared are what UpdateBranch,
// DeleteBranch, and ShareSettings just did. Settings lists the settings
// edits sitting uncommitted in the project checkout, nil when there are
// none. Sync is a named field rather than embedded because the Wails model
// generator drops the element types of an embedded struct's slices.
type BranchSyncView struct {
	ProjectID  string                   `json:"projectId"`
	Sync       worktree.SyncStatus      `json:"sync"`
	Settings   *worktree.SettingsStatus `json:"settings,omitempty"`
	FetchError string                   `json:"fetchError,omitempty"`
	Update     *worktree.BranchUpdate   `json:"update,omitempty"`
	Deleted    *worktree.BranchDeletion `json:"deleted,omitempty"`
	Shared     *SettingsShare           `json:"shared,omitempty"`
}

// ProjectSyncView is how a project's repository stands against origin, for
// its card on Projects. Fetched says this call fetched origin first.
type ProjectSyncView struct {
	ProjectID  string               `json:"projectId"`
	Summary    worktree.SyncSummary `json:"summary"`
	Fetched    bool                 `json:"fetched,omitempty"`
	FetchError string               `json:"fetchError,omitempty"`
}

// ProjectBranches measures a project's local branches against what the last
// fetch brought in from origin, without touching the network.
func (service *Service) ProjectBranches(projectID string) (BranchSyncView, error) {
	ctx, cancel := context.WithTimeout(service.context(), 30*time.Second)
	defer cancel()
	project, root, err := service.syncProject(ctx, projectID)
	if err != nil {
		return BranchSyncView{}, err
	}
	return service.branchSync(ctx, project, root)
}

// FetchProject fetches every branch of the project's origin, then measures
// its branches again. Only remote-tracking refs move; a failed fetch is
// reported in the view, not as an error, so the local state still shows.
func (service *Service) FetchProject(projectID string) (BranchSyncView, error) {
	ctx, cancel := context.WithTimeout(service.context(), fetchTimeout)
	defer cancel()
	project, root, err := service.syncProject(ctx, projectID)
	if err != nil {
		return BranchSyncView{}, err
	}
	service.syncMu.Lock()
	defer service.syncMu.Unlock()
	fetchErr := errors.New("this repository has no origin remote to fetch from")
	if worktree.HasOrigin(ctx, root) {
		fetchErr = service.fetchOrigin(ctx, project.ID, root)
	}
	view, err := service.branchSync(ctx, project, root)
	if err != nil {
		return BranchSyncView{}, err
	}
	if fetchErr != nil {
		view.FetchError = fetchFailure(fetchErr, view.Sync.Origin)
	}
	return view, nil
}

// UpdateBranch fast-forwards one of the project's branches to its upstream,
// as worktree.UpdateBranch does, and refuses a branch an active run is
// writing on, since moving it would pull the run's ground from under it.
func (service *Service) UpdateBranch(projectID, branch string) (BranchSyncView, error) {
	ctx, cancel := context.WithTimeout(service.context(), time.Minute)
	defer cancel()
	project, root, err := service.syncProject(ctx, projectID)
	if err != nil {
		return BranchSyncView{}, err
	}
	service.syncMu.Lock()
	defer service.syncMu.Unlock()
	checkout, err := worktree.CheckoutOf(ctx, root, branch)
	if err != nil {
		return BranchSyncView{}, fmt.Errorf("find where %s is checked out: %w", branch, err)
	}
	if runID := service.runOnBranch(project.ID, branch, checkout); runID != "" {
		return BranchSyncView{}, fmt.Errorf("run %s is writing on %s; update it once the run finishes", runID, branch)
	}
	update, err := worktree.UpdateBranch(ctx, root, branch, service.projectSettings(project, root))
	if err != nil {
		return BranchSyncView{}, fmt.Errorf("update %s: %w", branch, err)
	}
	view, err := service.branchSync(ctx, project, root)
	if err != nil {
		return BranchSyncView{}, err
	}
	view.Update = &update
	return view, nil
}

// DeleteBranch deletes one of the project's branches that has no upstream,
// as worktree.DeleteBranch does, once the user has confirmed it at commit.
// It refuses a branch an active run works on.
func (service *Service) DeleteBranch(projectID, branch, commit string) (BranchSyncView, error) {
	ctx, cancel := context.WithTimeout(service.context(), time.Minute)
	defer cancel()
	project, root, err := service.syncProject(ctx, projectID)
	if err != nil {
		return BranchSyncView{}, err
	}
	service.syncMu.Lock()
	defer service.syncMu.Unlock()
	checkout, err := worktree.CheckoutOf(ctx, root, branch)
	if err != nil {
		return BranchSyncView{}, fmt.Errorf("find where %s is checked out: %w", branch, err)
	}
	if runID := service.runOnBranch(project.ID, branch, checkout); runID != "" {
		return BranchSyncView{}, fmt.Errorf("run %s works on %s; delete it once the run finishes", runID, branch)
	}
	deletion, err := worktree.DeleteBranch(ctx, root, syncBase(ctx, root), branch, commit, func(path string) error {
		return service.removeBranchWorktree(ctx, project, root, path)
	})
	if err != nil {
		return BranchSyncView{}, fmt.Errorf("delete %s: %w", branch, err)
	}
	view, err := service.branchSync(ctx, project, root)
	if err != nil {
		return BranchSyncView{}, err
	}
	view.Deleted = &deletion
	return view, nil
}

// removeBranchWorktree removes the linked worktree at path for a branch being
// deleted. A finished run's worktree goes through the run, which records the
// removal as Remove worktree does; any other goes directly. Either way one
// with changes is kept.
func (service *Service) removeBranchWorktree(ctx context.Context, project storage.Project, root, path string) error {
	executions, err := service.store.ExecutionsWithWorktrees(ctx, project.ID)
	if err != nil {
		return fmt.Errorf("list run worktrees: %w", err)
	}
	for _, execution := range executions {
		if live, err := service.execution(ctx, execution.ID); err == nil {
			execution = live
		}
		if !samePath(execution.Worktree, path) || !removable(project, execution) {
			continue
		}
		if !terminalRunState(execution.State) {
			return fmt.Errorf("run %s still works there", execution.ID)
		}
		return service.removeWorktree(ctx, project, execution)
	}
	return worktree.Remove(ctx, root, path)
}

// ProjectSync summarizes how a project's repository stands against origin.
// With fetchIfStale it first fetches origin when neither a fetch nor an
// attempt has happened in the last syncFreshness, so a card that refreshes
// with every dashboard update does not fetch every time.
func (service *Service) ProjectSync(projectID string, fetchIfStale bool) (ProjectSyncView, error) {
	ctx, cancel := context.WithTimeout(service.context(), fetchTimeout)
	defer cancel()
	project, root, err := service.syncProject(ctx, projectID)
	if err != nil {
		return ProjectSyncView{}, err
	}
	base := syncBase(ctx, root)
	summary, err := worktree.Summarize(ctx, root, base)
	if err != nil {
		return ProjectSyncView{}, fmt.Errorf("summarize branches of %s: %w", project.Name, err)
	}
	view := ProjectSyncView{ProjectID: project.ID, Summary: summary}
	if !fetchIfStale || !summary.HasOrigin {
		return view, nil
	}
	service.syncMu.Lock()
	defer service.syncMu.Unlock()
	// Another call may have fetched while this one waited for the lock.
	if summary, err = worktree.Summarize(ctx, root, base); err != nil {
		return ProjectSyncView{}, fmt.Errorf("summarize branches of %s: %w", project.Name, err)
	}
	view.Summary = summary
	if service.fetchedRecently(project.ID, summary.FetchedAt) {
		return view, nil
	}
	if err := service.fetchOrigin(ctx, project.ID, root); err != nil {
		view.FetchError = fetchFailure(err, summary.Origin)
		return view, nil
	}
	if view.Summary, err = worktree.Summarize(ctx, root, base); err != nil {
		return ProjectSyncView{}, fmt.Errorf("summarize branches of %s: %w", project.Name, err)
	}
	view.Fetched = true
	return view, nil
}

// fetchOrigin fetches the project's origin and remembers the attempt, since
// a failed fetch leaves no FETCH_HEAD to date it. Callers hold syncMu.
func (service *Service) fetchOrigin(ctx context.Context, projectID, root string) error {
	if service.syncAttempts == nil {
		service.syncAttempts = make(map[string]time.Time)
	}
	service.syncAttempts[projectID] = time.Now()
	return worktree.FetchOrigin(ctx, root)
}

// fetchedRecently reports whether origin was fetched, or a fetch attempted,
// within syncFreshness. Callers hold syncMu.
func (service *Service) fetchedRecently(projectID, fetchedAt string) bool {
	last := service.syncAttempts[projectID]
	if at, err := time.Parse(time.RFC3339, fetchedAt); err == nil && at.After(last) {
		last = at
	}
	return time.Since(last) < syncFreshness
}

// syncProject loads a project and finds its repository's main checkout,
// since a project may point at a directory inside it.
func (service *Service) syncProject(ctx context.Context, projectID string) (storage.Project, string, error) {
	if err := service.ready(); err != nil {
		return storage.Project{}, "", err
	}
	project, err := service.workableProject(ctx, projectID)
	if err != nil {
		return storage.Project{}, "", err
	}
	root, err := worktree.MainRoot(ctx, project.Repository)
	if err != nil {
		return storage.Project{}, "", fmt.Errorf("find the repository of %s: %w", project.Name, err)
	}
	return project, root, nil
}

// branchSync measures the project's branches against the base its branching
// configuration names, else origin's default branch, and withholds the
// update from any branch an active run is writing on.
func (service *Service) branchSync(ctx context.Context, project storage.Project, root string) (BranchSyncView, error) {
	status, err := worktree.Branches(ctx, root, syncBase(ctx, root), service.projectSettings(project, root))
	if err != nil {
		return BranchSyncView{}, fmt.Errorf("read branches of %s: %w", project.Name, err)
	}
	settings, err := service.projectSettingsStatus(ctx, project, root, status)
	if err != nil {
		// The branches are worth showing without the settings; the view
		// says why those cannot be shared.
		settings = &worktree.SettingsStatus{Changes: []worktree.SettingsChange{}, Reason: "The settings changes could not be read: " + err.Error()}
	}
	for index := range status.Branches {
		branch := &status.Branches[index]
		runID := service.runOnBranch(project.ID, branch.Name, branch.CheckedOut)
		if runID == "" {
			continue
		}
		if branch.Updatable {
			branch.Updatable = false
			branch.Reason = "Run " + runID + " is writing on this branch; update it once the run finishes."
		}
		if branch.Deletable {
			branch.Deletable = false
			branch.DeleteReason = "Run " + runID + " works on this branch; delete it once the run finishes."
		}
	}
	return BranchSyncView{ProjectID: project.ID, Sync: status, Settings: settings}, nil
}

// syncBase is the base branches are measured against: the one the branching
// configuration names, else origin's default branch.
func syncBase(ctx context.Context, root string) string {
	if loaded, err := branching.Load(root); err == nil && loaded.Config.Branch.Base != "" {
		return loaded.Config.Branch.Base
	}
	return worktree.DefaultBranch(ctx, root)
}

// samePath compares two paths after resolving symbolic links, since git
// records a worktree's resolved path and a run the one it was given.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	resolve := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return filepath.Clean(path)
	}
	return resolve(a) == resolve(b)
}

// runOnBranch names an active run of the project that works on the branch
// or in the checkout that has it checked out, or returns "".
func (service *Service) runOnBranch(projectID, branch, checkout string) string {
	service.runsMu.RLock()
	defer service.runsMu.RUnlock()
	for id, run := range service.runs {
		view := run.view
		if view.ProjectID != projectID || terminalRunState(view.State) {
			continue
		}
		if view.Branch == branch || samePath(view.Worktree, checkout) {
			return id
		}
	}
	return ""
}

// fetchFailure says why a fetch failed and, when origin wanted credentials,
// how to give git some.
func fetchFailure(err error, origin string) string {
	if !errors.Is(err, worktree.ErrNotSignedIn) {
		return "Could not fetch from origin: " + err.Error()
	}
	host, _, _ := strings.Cut(origin, "/")
	if host == "github.com" {
		return "Git could not sign in to GitHub. Connect GitHub under Connections and run `gh auth setup-git` once so git uses that login over HTTPS, or add an SSH key for this machine, then fetch again. " + err.Error()
	}
	if host == "" {
		host = "origin"
	}
	return "Git could not sign in to " + host + ". Set up git credentials for it, then fetch again. " + err.Error()
}
