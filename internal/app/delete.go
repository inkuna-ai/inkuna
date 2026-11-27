package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/artifact"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// DeleteOptions says what deleting history removes besides Inkuna's own
// records. Neither is done unless asked for.
type DeleteOptions struct {
	// RemoveWorktrees removes each deleted run's worktree that has no
	// uncommitted changes. One that has them is kept.
	RemoveWorktrees bool `json:"removeWorktrees"`
	// DeleteBranches names the deleted runs' branches to delete, by project
	// and then branch, each with the commit the person confirmed it at, once
	// a preview showed them what only that branch has. Two projects can each
	// have a branch of the same name, so a name alone would confirm both. A
	// branch it does not name is kept, and so is one that has moved since or
	// that DeleteBranch would refuse.
	DeleteBranches map[string]map[string]string `json:"deleteBranches,omitempty"`
}

// TaskDeletePreview is what deleting a task would remove and what it would
// leave, so a person can choose before anything is deleted.
type TaskDeletePreview struct {
	TaskID    string `json:"taskId"`
	ProjectID string `json:"projectId"`
	Title     string `json:"title"`
	// Refused says why the task cannot be deleted now; empty when it can.
	Refused string             `json:"refused,omitempty"`
	Runs    []RunDeletePreview `json:"runs"`
}

// RunDeletePreview is one run of a task being deleted, with what it left
// outside Inkuna's history.
type RunDeletePreview struct {
	RunID      string `json:"runId"`
	Title      string `json:"title"`
	State      string `json:"state"`
	FinishedAt string `json:"finishedAt,omitempty"`
	// Worktree is the run's worktree while it is there; a worktree another
	// run of the task shares is listed on the first.
	Worktree *WorktreePreview `json:"worktree,omitempty"`
	// Branch is the branch the run committed to while it exists, listed on
	// the first run of the task that used it.
	Branch *BranchPreview `json:"branch,omitempty"`
}

// WorktreePreview is a run's worktree, the disk space it takes, and whether
// RemoveWorktrees would remove it.
type WorktreePreview struct {
	Path string `json:"path"`
	// Bytes is the size of the files in it, git's own record of it in the
	// repository aside.
	Bytes     int64  `json:"bytes"`
	Removable bool   `json:"removable"`
	Reason    string `json:"reason,omitempty"`
}

// BranchPreview is a run's branch: its tip, which a delete must confirm,
// the commits only it has, which deleting it would drop, and whether it can
// be deleted.
type BranchPreview struct {
	// ProjectID is the project whose repository has the branch; a delete
	// confirms the branch under it.
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Commit    string `json:"commit"`
	Unique    int    `json:"unique"`
	Deletable bool   `json:"deletable"`
	Reason    string `json:"reason,omitempty"`
}

// DeleteReport is what a delete removed, and what it kept and why.
type DeleteReport struct {
	Tasks []TaskDeleteReport `json:"tasks"`
}

// TaskDeleteReport is what a delete did for one task.
type TaskDeleteReport struct {
	TaskID string `json:"taskId"`
	// RunID is set when only this one run of the task was deleted.
	RunID string              `json:"runId,omitempty"`
	Rows  []storage.TableRows `json:"rows"`
	// Blobs and BlobBytes count the artifacts freed: those no remaining task
	// indexes. KeptBlobs counts the task's artifacts another task still
	// indexes, which stay. A blob two deleted tasks shared is counted on the
	// first.
	Blobs     int               `json:"blobs"`
	BlobBytes int64             `json:"blobBytes"`
	KeptBlobs int               `json:"keptBlobs"`
	Worktrees []WorktreeOutcome `json:"worktrees"`
	Branches  []BranchOutcome   `json:"branches"`
	// Links are the tracker issues the task and its runs were linked to.
	// Only Inkuna's link is gone; nothing changed in the tracker.
	Links []DroppedLink `json:"links"`
	// Problems are what went wrong after the task's rows were deleted, such
	// as a blob that could not be removed.
	Problems []string `json:"problems,omitempty"`
}

// WorktreeOutcome is what happened to a deleted run's worktree.
type WorktreeOutcome struct {
	RunID   string `json:"runId"`
	Path    string `json:"path"`
	Removed bool   `json:"removed"`
	// Reason says why a worktree was kept.
	Reason string `json:"reason,omitempty"`
}

// BranchOutcome is what happened to a deleted run's branch.
type BranchOutcome struct {
	RunID   string `json:"runId"`
	Name    string `json:"name"`
	Commit  string `json:"commit,omitempty"`
	Unique  int    `json:"unique"`
	Deleted bool   `json:"deleted"`
	// Reason says why a branch was kept.
	Reason string `json:"reason,omitempty"`
}

// DroppedLink is a tracker issue a deleted task was linked to.
type DroppedLink struct {
	Tracker string `json:"tracker"`
	ID      string `json:"id"`
	URL     string `json:"url,omitempty"`
}

// TaskHistory deletes finished tasks from Inkuna's database and artifact
// store. The app uses it under its own locks, after checking the runs it
// holds in memory; inkunactl uses it on its own, beside the app or without
// it, and relies on the stored states, which a delete reads again under
// SQLite's write lock.
type TaskHistory struct {
	Store     *storage.Store
	Artifacts *artifact.Store
	// service is the app the history belongs to, when it is the app's: its
	// locks are taken so that a step storing the same blob, or a branch being
	// updated or deleted, is not changed underneath.
	service *Service
}

// Preview says, for each task, whether it can be deleted now and what of
// its runs is left outside history.
func (history TaskHistory) Preview(ctx context.Context, taskIDs []string) ([]TaskDeletePreview, error) {
	if len(taskIDs) == 0 {
		return nil, errors.New("name at least one task")
	}
	tasks, err := history.Store.HistoryTasks(ctx, taskIDs)
	if err != nil {
		return nil, err
	}
	deleting := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		deleting[task.ID] = true
	}
	alsoDeleted := func(other storage.Execution) bool { return deleting[other.TaskID] }
	repositories := newDeleteRepositories(history.Store)
	previews := make([]TaskDeletePreview, 0, len(tasks))
	for _, task := range tasks {
		preview := TaskDeletePreview{TaskID: task.ID, ProjectID: task.ProjectID, Title: task.Title, Runs: make([]RunDeletePreview, 0, len(task.Runs))}
		seenPaths, seenBranches := make(map[string]bool), make(map[string]bool)
		for _, run := range task.Runs {
			if !storage.RunEnded(run.State) && preview.Refused == "" {
				preview.Refused = (&storage.DeleteRefusedError{TaskID: task.ID, RunID: run.ID, Reason: storage.ActiveReason(run.State)}).Error()
			}
			preview.Runs = append(preview.Runs, history.previewRun(ctx, repositories.of(ctx, run.ProjectID), run, seenPaths, seenBranches, alsoDeleted))
		}
		previews = append(previews, preview)
	}
	return previews, nil
}

// PreviewRun says whether one run of a task can be deleted on its own now,
// and what of it is left outside history, as Preview does for a task. A
// task's only run is refused: it goes with its task.
func (history TaskHistory) PreviewRun(ctx context.Context, runID string) (TaskDeletePreview, error) {
	run, err := history.Store.Execution(ctx, strings.TrimSpace(runID))
	if err != nil {
		return TaskDeletePreview{}, err
	}
	tasks, err := history.Store.HistoryTasks(ctx, []string{run.TaskID})
	if err != nil {
		return TaskDeletePreview{}, err
	}
	task := tasks[0]
	preview := TaskDeletePreview{TaskID: task.ID, ProjectID: task.ProjectID, Title: task.Title}
	switch {
	case !storage.RunEnded(run.State):
		preview.Refused = (&storage.DeleteRefusedError{TaskID: task.ID, RunID: run.ID, Reason: storage.ActiveReason(run.State)}).Error()
	case len(task.Runs) < 2:
		preview.Refused = (&storage.DeleteRefusedError{TaskID: task.ID, RunID: run.ID, Reason: "is the task's only run; delete the task instead"}).Error()
	}
	repository := newDeleteRepositories(history.Store).of(ctx, run.ProjectID)
	// The task's other runs stay, so a worktree or branch one of them shares
	// is theirs too.
	view := history.previewRun(ctx, repository, run, make(map[string]bool), make(map[string]bool), func(other storage.Execution) bool { return other.ID == run.ID })
	preview.Runs = []RunDeletePreview{view}
	return preview, nil
}

// previewRun is what deleting a run would leave outside history: its
// worktree and branch, each listed on the first run that has it. deleting
// says whether another run is being deleted with it.
func (history TaskHistory) previewRun(ctx context.Context, repository *deleteRepository, run storage.Execution, seenPaths, seenBranches map[string]bool, deleting func(storage.Execution) bool) RunDeletePreview {
	view := RunDeletePreview{RunID: run.ID, Title: run.Title, State: run.State}
	if !run.FinishedAt.IsZero() {
		view.FinishedAt = run.FinishedAt.Format(time.RFC3339Nano)
	}
	if path := worktreeLeft(repository, run); path != "" && !seenPaths[path] {
		seenPaths[path] = true
		view.Worktree = previewWorktree(ctx, path)
		if user := history.worktreeUser(ctx, repository, run, deleting); user != "" {
			view.Worktree.Removable, view.Worktree.Reason = false, user
		}
	}
	if run.Branch != "" && !seenBranches[run.Branch] {
		seenBranches[run.Branch] = true
		view.Branch = history.previewBranch(ctx, repository, run, deleting)
	}
	return view
}

// DeleteTasks deletes the tasks and everything that belongs to them, or
// nothing when one of them cannot be deleted; then it frees the blobs no
// other task indexes and removes what options asks for.
func (history TaskHistory) DeleteTasks(ctx context.Context, taskIDs []string, options DeleteOptions) (DeleteReport, error) {
	removals, err := history.Store.DeleteTasks(ctx, taskIDs)
	if err != nil {
		return DeleteReport{}, err
	}
	return history.cleanUp(ctx, removals, options, false), nil
}

// DeleteRun deletes one run of a task that has others, as DeleteTasks
// deletes a task.
func (history TaskHistory) DeleteRun(ctx context.Context, runID string, options DeleteOptions) (DeleteReport, error) {
	removal, err := history.Store.DeleteRun(ctx, runID)
	if err != nil {
		return DeleteReport{}, err
	}
	return history.cleanUp(ctx, []storage.TaskRemoval{removal}, options, true), nil
}

// cleanUp does what is left once the rows are gone. Nothing can resume or
// send back a run whose rows are gone, so no lock on runs is needed; a
// failure here keeps what it could not remove and says so.
func (history TaskHistory) cleanUp(ctx context.Context, removals []storage.TaskRemoval, options DeleteOptions, oneRun bool) DeleteReport {
	report := DeleteReport{Tasks: make([]TaskDeleteReport, 0, len(removals))}
	repositories := newDeleteRepositories(history.Store)
	freed := make(map[string]bool)
	for _, removal := range removals {
		task := TaskDeleteReport{TaskID: removal.TaskID, Rows: removal.Rows, Worktrees: []WorktreeOutcome{}, Branches: []BranchOutcome{}, Links: droppedLinks(removal)}
		if oneRun && len(removal.Runs) == 1 {
			task.RunID = removal.Runs[0].ID
		}
		history.freeBlobs(ctx, removal.Artifacts, freed, &task)
		history.cleanUpGit(ctx, repositories, removal.Runs, options, &task)
		report.Tasks = append(report.Tasks, task)
	}
	return report
}

// freeBlobs removes each blob no remaining task indexes. In the app it holds
// the artifact lock across the check and the removal, so a step storing the
// same content cannot record it in between and lose its blob.
func (history TaskHistory) freeBlobs(ctx context.Context, digests []string, freed map[string]bool, task *TaskDeleteReport) {
	if history.service != nil {
		history.service.artifactMu.Lock()
		defer history.service.artifactMu.Unlock()
	}
	for _, digest := range digests {
		if freed[digest] {
			continue
		}
		referenced, err := history.Store.ArtifactReferenced(ctx, digest)
		if err != nil {
			task.Problems = append(task.Problems, err.Error())
			continue
		}
		if referenced {
			task.KeptBlobs++
			continue
		}
		freed[digest] = true
		if history.Artifacts == nil {
			continue
		}
		size, err := history.Artifacts.Remove(digest)
		if err != nil {
			task.Problems = append(task.Problems, fmt.Sprintf("Could not remove artifact %s: %v", digest[:12], err))
			continue
		}
		task.Blobs++
		task.BlobBytes += size
	}
}

// cleanUpGit removes the runs' worktrees and deletes their branches as far
// as options asks, and reports every worktree and branch that is left. A
// branch is deleted only as the Branches view deletes one, at the tip the
// person confirmed, and the worktree it is checked out in goes with it only
// when that is one of these runs' worktrees and has no uncommitted changes.
func (history TaskHistory) cleanUpGit(ctx context.Context, repositories *deleteRepositories, runs []storage.RemovedRun, options DeleteOptions, task *TaskDeleteReport) {
	type tree struct {
		run     storage.Execution
		root    string
		outcome *WorktreeOutcome
		// err is why removing the worktree failed, for a branch deletion
		// that needs it gone to say so.
		err error
		// shared is set when another run still works in it, which makes it
		// that run's rather than a kept one.
		shared bool
	}
	trees := make(map[string]*tree)
	order := make([]string, 0, len(runs))
	for _, run := range runs {
		repository := repositories.of(ctx, run.ProjectID)
		path := worktreeLeft(repository, run.Execution)
		if path == "" || trees[path] != nil {
			continue
		}
		trees[path] = &tree{run: run.Execution, root: repository.root}
		order = append(order, path)
		if repository.err != nil {
			trees[path].err = fmt.Errorf("could not open the repository: %w", repository.err)
			trees[path].outcome = &WorktreeOutcome{RunID: run.ID, Path: path, Reason: "Could not open the repository: " + repository.err.Error()}
			continue
		}
		// The deleted rows are gone, so every run left that records the
		// path is another.
		if user := history.worktreeUser(ctx, repository, run.Execution, func(storage.Execution) bool { return false }); user != "" {
			trees[path].err = errors.New(user)
			trees[path].outcome = &WorktreeOutcome{RunID: run.ID, Path: path, Reason: user}
			trees[path].shared = true
		}
	}
	remove := func(path string) error {
		current := trees[path]
		if current == nil {
			for known, candidate := range trees {
				if samePath(known, path) {
					current, path = candidate, known
					break
				}
			}
		}
		if current == nil {
			return fmt.Errorf("it is checked out in %s, which is not a worktree of these runs", path)
		}
		switch {
		case current.outcome != nil && current.outcome.Removed:
			return nil
		case current.err != nil:
			return current.err
		}
		if err := worktree.Remove(ctx, current.root, path); err != nil {
			current.err = err
			current.outcome = &WorktreeOutcome{RunID: current.run.ID, Path: path, Reason: worktreeKept(err)}
			return err
		}
		current.outcome = &WorktreeOutcome{RunID: current.run.ID, Path: path, Removed: true}
		return nil
	}
	if options.RemoveWorktrees {
		for _, path := range order {
			if trees[path].outcome != nil {
				continue
			}
			_ = remove(path)
		}
	}

	seen := make(map[string]bool)
	for _, run := range runs {
		if run.Branch == "" || seen[run.ProjectID+"\x00"+run.Branch] {
			continue
		}
		seen[run.ProjectID+"\x00"+run.Branch] = true
		task.Branches = append(task.Branches, history.cleanUpBranch(ctx, repositories.of(ctx, run.ProjectID), run.Execution, options.DeleteBranches, remove))
	}

	for _, path := range order {
		current := trees[path]
		if current.outcome == nil {
			current.outcome = &WorktreeOutcome{RunID: current.run.ID, Path: path, Reason: "Not asked to remove it."}
		}
		task.Worktrees = append(task.Worktrees, *current.outcome)
		// Nothing else says a kept worktree is Inkuna's once its run's rows
		// are gone, so it is recorded for Settings to list and remove.
		if current.outcome.Removed || current.shared {
			continue
		}
		kept := storage.KeptWorktree{Path: path, ProjectID: current.run.ProjectID, Branch: current.run.Branch, TaskID: current.run.TaskID, RunID: current.run.ID, Title: current.run.Title, Reason: current.outcome.Reason}
		if err := history.Store.KeepWorktree(ctx, kept); err != nil {
			task.Problems = append(task.Problems, err.Error())
		}
	}
}

// cleanUpBranch deletes a deleted run's branch when the person confirmed
// it, and otherwise says what is left of it.
func (history TaskHistory) cleanUpBranch(ctx context.Context, repository *deleteRepository, run storage.Execution, confirmed map[string]map[string]string, remove func(string) error) BranchOutcome {
	outcome := BranchOutcome{RunID: run.ID, Name: run.Branch}
	// The deleted rows are gone, so every run left on the branch is another.
	preview := history.previewBranch(ctx, repository, run, func(storage.Execution) bool { return false })
	if preview == nil {
		outcome.Reason = "It no longer exists."
		return outcome
	}
	outcome.Commit, outcome.Unique = preview.Commit, preview.Unique
	commit, asked := confirmed[run.ProjectID][run.Branch]
	switch {
	case !asked:
		outcome.Reason = "Not asked to delete it."
		return outcome
	case !preview.Deletable:
		outcome.Reason = preview.Reason
		return outcome
	}
	if history.service != nil {
		history.service.syncMu.Lock()
		defer history.service.syncMu.Unlock()
	}
	deletion, err := worktree.DeleteBranch(ctx, repository.root, repository.base, run.Branch, commit, remove)
	if err != nil {
		outcome.Reason = err.Error()
		if errors.Is(err, worktree.ErrDirty) {
			outcome.Reason = "Its worktree has uncommitted changes; commit or discard them first."
		}
		return outcome
	}
	outcome.Deleted, outcome.Commit, outcome.Unique, outcome.Reason = true, deletion.Commit, deletion.Unique, ""
	return outcome
}

// previewBranch says what deleting a run's branch would drop and whether it
// can be deleted, or nil when the branch no longer exists. deleting says
// whether another run on the branch is being deleted too.
func (history TaskHistory) previewBranch(ctx context.Context, repository *deleteRepository, run storage.Execution, deleting func(storage.Execution) bool) *BranchPreview {
	if repository.err != nil {
		return &BranchPreview{ProjectID: run.ProjectID, Name: run.Branch, Reason: "Could not open the repository: " + repository.err.Error()}
	}
	judged, err := repository.branch(ctx, run.Branch)
	if err != nil {
		return &BranchPreview{ProjectID: run.ProjectID, Name: run.Branch, Reason: "Could not read it: " + err.Error()}
	}
	if judged == nil {
		return nil
	}
	preview := &BranchPreview{ProjectID: run.ProjectID, Name: run.Branch, Commit: judged.Commit, Unique: judged.Unique, Deletable: judged.Deletable, Reason: judged.DeleteReason}
	if run.Cleanup == domain.CleanupCheckout {
		preview.Deletable, preview.Reason = false, "The run committed on the branch the project checkout had checked out; Inkuna did not make it."
		return preview
	}
	if judged.Upstream != "" && judged.State != worktree.SyncGone {
		preview.Deletable, preview.Reason = false, "It tracks "+judged.Upstream+"; Inkuna deletes only branches without an upstream."
		return preview
	}
	others, err := history.Store.ExecutionsOnBranch(ctx, run.ProjectID, run.Branch)
	if err != nil {
		preview.Deletable, preview.Reason = false, "Could not check which runs use it: "+err.Error()
		return preview
	}
	for _, other := range others {
		if other.ID != run.ID && !deleting(other) {
			preview.Deletable, preview.Reason = false, fmt.Sprintf("Run %s of task %s works on it too.", other.ID, other.TaskID)
			return preview
		}
	}
	return preview
}

// worktreeLeft is the path of a run's worktree that is still there for a
// delete to remove or report, or "" when there is none: it was never made,
// Inkuna removed it, it is gone, or it is the project checkout, which a
// delete never touches.
func worktreeLeft(repository *deleteRepository, run storage.Execution) string {
	if run.Worktree == "" || !run.WorktreeRemovedAt.IsZero() || run.Cleanup == domain.CleanupCheckout {
		return ""
	}
	if samePath(run.Worktree, repository.project.Repository) || samePath(run.Worktree, repository.root) {
		return ""
	}
	if _, err := os.Stat(run.Worktree); errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return run.Worktree
}

// worktreeUser says which other run still records the run's worktree as
// its own, or "" when none does: a run that worked there after the
// directory was removed outside Inkuna and made again, whose worktree a
// delete must not take. deleting says whether that run is being deleted too.
func (history TaskHistory) worktreeUser(ctx context.Context, repository *deleteRepository, run storage.Execution, deleting func(storage.Execution) bool) string {
	if repository.worktrees == nil {
		runs, err := history.Store.ExecutionsWithWorktrees(ctx, run.ProjectID)
		if err != nil {
			return "Could not check which runs use it: " + err.Error()
		}
		repository.worktrees = runs
	}
	for _, other := range repository.worktrees {
		if other.ID != run.ID && samePath(other.Worktree, run.Worktree) && !deleting(other) {
			return fmt.Sprintf("Run %s of task %s works in it too.", other.ID, other.TaskID)
		}
	}
	return ""
}

func previewWorktree(ctx context.Context, path string) *WorktreePreview {
	preview := &WorktreePreview{Path: path, Bytes: diskUsage(path)}
	status, err := worktree.Status(ctx, path)
	switch {
	case err != nil:
		preview.Reason = "Could not read it: " + err.Error()
	case status != "":
		preview.Reason = "It has uncommitted changes, so it would be kept."
	default:
		preview.Removable = true
	}
	return preview
}

func worktreeKept(err error) string {
	if errors.Is(err, worktree.ErrDirty) {
		return "It has uncommitted changes, so it was kept."
	}
	return "Could not remove it: " + err.Error()
}

// droppedLinks lists the tracker issues a removal's task and runs were for,
// once each.
func droppedLinks(removal storage.TaskRemoval) []DroppedLink {
	links := make([]DroppedLink, 0, len(removal.GitHubLinks)+len(removal.Runs))
	seen := make(map[string]bool)
	add := func(link DroppedLink) {
		if key := link.Tracker + "\x00" + link.ID; link.ID != "" && !seen[key] {
			seen[key] = true
			links = append(links, link)
		}
	}
	for _, link := range removal.GitHubLinks {
		add(DroppedLink{Tracker: "github", ID: strconv.Itoa(link.Issue), URL: link.URL})
	}
	for _, run := range removal.Runs {
		var story runStory
		if len(run.Story) == 0 || json.Unmarshal(run.Story, &story) != nil {
			continue
		}
		add(DroppedLink{Tracker: string(story.Settings.Tracker), ID: story.ID})
	}
	return links
}

// deleteRepository is a project's repository as a delete finds it: its main
// checkout, its base branch, and its branches judged for deletion, read
// once per delete.
type deleteRepository struct {
	project storage.Project
	root    string
	base    string
	err     error
	judged  map[string]*worktree.BranchSync
	// worktrees are the project's runs whose worktree Inkuna has not
	// removed, loaded once.
	worktrees []storage.Execution
}

func (repository *deleteRepository) branch(ctx context.Context, name string) (*worktree.BranchSync, error) {
	if judged, ok := repository.judged[name]; ok {
		return judged, nil
	}
	found, err := worktree.Deletions(ctx, repository.root, repository.base, []string{name})
	if err != nil {
		return nil, err
	}
	var judged *worktree.BranchSync
	if branch, ok := found[name]; ok {
		judged = &branch
	}
	repository.judged[name] = judged
	return judged, nil
}

type deleteRepositories struct {
	store *storage.Store
	byID  map[string]*deleteRepository
}

func newDeleteRepositories(store *storage.Store) *deleteRepositories {
	return &deleteRepositories{store: store, byID: make(map[string]*deleteRepository)}
}

func (all *deleteRepositories) of(ctx context.Context, projectID string) *deleteRepository {
	if found := all.byID[projectID]; found != nil {
		return found
	}
	found := &deleteRepository{judged: make(map[string]*worktree.BranchSync)}
	all.byID[projectID] = found
	project, err := all.store.Project(ctx, projectID)
	if err != nil {
		found.err = fmt.Errorf("load project %s: %w", projectID, err)
		return found
	}
	found.project = project
	if found.root, err = worktree.MainRoot(ctx, project.Repository); err != nil {
		found.err = err
		return found
	}
	found.base = syncBase(ctx, found.root)
	return found
}

// taskHistory is the history of the app's own database, under its locks.
func (service *Service) taskHistory() TaskHistory {
	return TaskHistory{Store: service.store, Artifacts: service.artifacts, service: service}
}

// PreviewDeleteTasks says, for each task, whether it can be deleted now and
// what of its runs is left outside history: each worktree, and each branch
// with its tip and the commits only it has, which DeleteTasks needs
// confirmed before it deletes one.
func (service *Service) PreviewDeleteTasks(taskIDs []string) ([]TaskDeletePreview, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	previews, err := service.taskHistory().Preview(service.context(), taskIDs)
	if err != nil {
		return nil, err
	}
	// The runs in memory are ahead of the store, which can still say queued
	// once a run has started, so their reason wins, as it does for a delete.
	for index := range previews {
		if err := service.refuseLive(map[string]bool{previews[index].TaskID: true}, ""); err != nil {
			previews[index].Refused = err.Error()
		}
	}
	return previews, nil
}

// PreviewDeleteRun says whether one finished run of a task that has others
// can be deleted on its own now, and what of it is left outside history, as
// PreviewDeleteTasks does for tasks.
func (service *Service) PreviewDeleteRun(runID string) (TaskDeletePreview, error) {
	if err := service.ready(); err != nil {
		return TaskDeletePreview{}, err
	}
	preview, err := service.taskHistory().PreviewRun(service.context(), runID)
	if err != nil {
		return TaskDeletePreview{}, err
	}
	// Memory is ahead of the store, so its reason wins, as in PreviewDeleteTasks.
	if err := service.refuseLive(nil, strings.TrimSpace(runID)); err != nil {
		preview.Refused = err.Error()
	}
	return preview, nil
}

// DeleteTasks deletes finished tasks and everything that belongs to them:
// their plans, runs, timelines, questions, messages, commits, usage, and the
// blobs no other task indexes. It refuses the whole call, deleting nothing,
// when a task has a run that is running, paused for input, a permission or
// review, or sent back. Worktrees and branches stay unless options asks.
func (service *Service) DeleteTasks(taskIDs []string, options DeleteOptions) (DeleteReport, error) {
	if err := service.ready(); err != nil {
		return DeleteReport{}, err
	}
	ctx := service.context()
	tasks := make(map[string]bool, len(taskIDs))
	for _, id := range taskIDs {
		tasks[strings.TrimSpace(id)] = true
	}
	removals, err := service.deleteRows(tasks, "", func() ([]storage.TaskRemoval, error) {
		return service.store.DeleteTasks(ctx, taskIDs)
	})
	if err != nil {
		return DeleteReport{}, err
	}
	service.auditDeletion(removals, "")
	report := service.taskHistory().cleanUp(ctx, removals, options, false)
	service.emit("inkuna:runs", "")
	return report, nil
}

// DeleteRun deletes one finished run of a task that has others, as
// DeleteTasks deletes a task, and leaves the task and its other runs.
func (service *Service) DeleteRun(runID string, options DeleteOptions) (DeleteReport, error) {
	if err := service.ready(); err != nil {
		return DeleteReport{}, err
	}
	ctx := service.context()
	removals, err := service.deleteRows(nil, runID, func() ([]storage.TaskRemoval, error) {
		removal, err := service.store.DeleteRun(ctx, runID)
		return []storage.TaskRemoval{removal}, err
	})
	if err != nil {
		return DeleteReport{}, err
	}
	service.auditDeletion(removals, runID)
	report := service.taskHistory().cleanUp(ctx, removals, options, true)
	service.emit("inkuna:runs", runID)
	return report, nil
}

// deleteRows deletes rows while no run can be sent back or resumed, after
// checking the runs in memory, which are ahead of what is stored, and then
// forgets the deleted runs. Once the rows are gone nothing can reopen them,
// so the rest of the clean-up runs without workspaceMu, which a branch
// deletion takes after syncMu.
func (service *Service) deleteRows(tasks map[string]bool, runID string, remove func() ([]storage.TaskRemoval, error)) ([]storage.TaskRemoval, error) {
	service.workspaceMu.Lock()
	defer service.workspaceMu.Unlock()
	if err := service.refuseLive(tasks, runID); err != nil {
		return nil, err
	}
	removals, err := remove()
	if err != nil {
		return nil, err
	}
	service.runsMu.Lock()
	for _, removal := range removals {
		for _, run := range removal.Runs {
			delete(service.runs, run.ID)
		}
	}
	service.runsMu.Unlock()
	return removals, nil
}

// refuseLive refuses a delete of the tasks, or of one run, while a run of
// theirs in memory has not ended, or has ended but its goroutine is still
// storing its end and cleaning up.
func (service *Service) refuseLive(tasks map[string]bool, runID string) error {
	service.runsMu.RLock()
	defer service.runsMu.RUnlock()
	for id, run := range service.runs {
		if id != runID && !tasks[run.task.ID] {
			continue
		}
		if err := liveRefusal(id, run); err != nil {
			return err
		}
	}
	return nil
}

// liveRefusal is why a run in memory stops a delete of it or its task, or
// nil when nothing does. The caller holds runsMu.
func liveRefusal(id string, run *liveExecution) error {
	if !terminalRunState(run.view.State) {
		return &storage.DeleteRefusedError{TaskID: run.task.ID, RunID: id, Reason: storage.ActiveReason(run.view.State)}
	}
	if run.done != nil {
		select {
		case <-run.done:
		default:
			return &storage.DeleteRefusedError{TaskID: run.task.ID, RunID: id, Reason: "is still finishing; try again in a moment"}
		}
	}
	return nil
}

// attachDeleteRefusals says on each listed run why its task cannot be
// deleted now, by the runs in memory, which are ahead of the store, and
// then the stored ones, so the view offers a delete only where Go would
// accept it. The delete decides again when asked.
func (service *Service) attachDeleteRefusals(views []WorkflowRunView) {
	if service.store == nil || len(views) == 0 {
		return
	}
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.TaskID)
	}
	standings, err := service.store.TaskStandings(service.context(), ids)
	if err != nil {
		for index := range views {
			views[index].DeleteRefused = "Could not check whether its task can be deleted: " + err.Error()
		}
		return
	}
	live := make(map[string]string)
	service.runsMu.RLock()
	for id, run := range service.runs {
		if live[run.task.ID] != "" {
			continue
		}
		if err := liveRefusal(id, run); err != nil {
			live[run.task.ID] = err.Error()
		}
	}
	service.runsMu.RUnlock()
	for index := range views {
		task := views[index].TaskID
		views[index].DeleteRefused = live[task]
		if views[index].DeleteRefused == "" {
			views[index].DeleteRefused = standings[task].Refused
		}
	}
}
