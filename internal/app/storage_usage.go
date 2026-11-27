package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// StorageUsage is the disk space Inkuna's history takes, with the worktrees
// deletes kept, which Settings lists so they can be removed later.
type StorageUsage struct {
	// Artifacts and ArtifactBytes count the blobs in the artifact store.
	Artifacts     int   `json:"artifacts"`
	ArtifactBytes int64 `json:"artifactBytes"`
	// Worktrees and WorktreeBytes count the worktrees Inkuna made that are
	// still on disk: those a run records, and those a delete kept.
	Worktrees     int   `json:"worktrees"`
	WorktreeBytes int64 `json:"worktreeBytes"`
	// Kept are the worktrees a delete left on disk, the most recent first.
	Kept []KeptWorktreeView `json:"kept"`
	// Problems are what could not be read; the rest is still counted.
	Problems []string `json:"problems,omitempty"`
}

// KeptWorktreeView is a worktree a delete kept, as Settings lists it.
type KeptWorktreeView struct {
	Path        string `json:"path"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName,omitempty"`
	Branch      string `json:"branch,omitempty"`
	// TaskID, RunID, and Title name the deleted run that made it.
	TaskID string `json:"taskId"`
	RunID  string `json:"runId"`
	Title  string `json:"title"`
	// Reason is why the delete kept it.
	Reason string `json:"reason"`
	KeptAt string `json:"keptAt"`
	Bytes  int64  `json:"bytes"`
}

// StorageUsage measures the artifact store and every worktree Inkuna made
// that is still on disk, and lists the worktrees deletes kept. A kept
// worktree that is gone, or that a run records as its own again, is no
// longer one a delete kept, so its record is dropped here.
func (service *Service) StorageUsage() (StorageUsage, error) {
	if err := service.ready(); err != nil {
		return StorageUsage{}, err
	}
	ctx := service.context()
	usage := StorageUsage{Kept: []KeptWorktreeView{}}
	if service.artifacts != nil {
		count, size, err := service.artifacts.Usage()
		if err != nil {
			usage.Problems = append(usage.Problems, err.Error())
		}
		usage.Artifacts, usage.ArtifactBytes = count, size
	}
	// Removed projects keep their history and its worktrees, so name them too.
	projects, err := service.store.AllProjects(ctx)
	if err != nil {
		return StorageUsage{}, fmt.Errorf("measure storage: %w", err)
	}
	names := make(map[string]string, len(projects))
	// A worktree two runs record, or a run and a delete, is counted once.
	type tree struct {
		path string
		size int64
	}
	measured := make([]tree, 0)
	measure := func(path string) int64 {
		for _, seen := range measured {
			if samePath(seen.path, path) {
				return seen.size
			}
		}
		size := diskUsage(path)
		measured = append(measured, tree{path: path, size: size})
		usage.Worktrees++
		usage.WorktreeBytes += size
		return size
	}
	repositories := newDeleteRepositories(service.store)
	recorded := make([]storage.Execution, 0)
	for _, project := range projects {
		names[project.ID] = project.Name
		runs, err := service.store.ExecutionsWithWorktrees(ctx, project.ID)
		if err != nil {
			usage.Problems = append(usage.Problems, fmt.Sprintf("Could not list the worktrees of %s: %v", project.Name, err))
			continue
		}
		recorded = append(recorded, runs...)
		repository := repositories.of(ctx, project.ID)
		for _, run := range runs {
			if path := worktreeLeft(repository, run); path != "" {
				measure(path)
			}
		}
	}
	kept, err := service.store.KeptWorktrees(ctx)
	if err != nil {
		return StorageUsage{}, fmt.Errorf("measure storage: %w", err)
	}
	for _, record := range kept {
		_, statErr := os.Stat(record.Path)
		if errors.Is(statErr, os.ErrNotExist) || recordedBy(recorded, record.Path) != nil {
			if err := service.store.ForgetKeptWorktree(ctx, record.Path); err != nil {
				usage.Problems = append(usage.Problems, err.Error())
			}
			continue
		}
		usage.Kept = append(usage.Kept, KeptWorktreeView{
			Path: record.Path, ProjectID: record.ProjectID, ProjectName: names[record.ProjectID], Branch: record.Branch,
			TaskID: record.TaskID, RunID: record.RunID, Title: record.Title, Reason: record.Reason,
			KeptAt: record.KeptAt.Format(time.RFC3339Nano), Bytes: measure(record.Path),
		})
	}
	return usage, nil
}

// RemoveKeptWorktree removes a worktree a delete kept, once the person
// confirmed it, as Remove worktree removes a run's: only a clean one goes,
// and its branch and commits stay. It refuses a path Inkuna has no record
// of keeping, and one a run works in again.
func (service *Service) RemoveKeptWorktree(path string) error {
	if err := service.ready(); err != nil {
		return err
	}
	ctx := service.context()
	record, err := service.store.KeptWorktreeAt(ctx, path)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("no delete kept a worktree at %s", path)
	}
	if err != nil {
		return err
	}
	// workspaceMu keeps a run from being sent back into the directory while
	// this checks no run records it and removes it.
	service.workspaceMu.Lock()
	defer service.workspaceMu.Unlock()
	runs, err := service.store.ExecutionsWithWorktrees(ctx, record.ProjectID)
	if err != nil {
		return fmt.Errorf("check which runs use %s: %w", record.Path, err)
	}
	if run := recordedBy(runs, record.Path); run != nil {
		if err := service.store.ForgetKeptWorktree(ctx, record.Path); err != nil {
			return err
		}
		return fmt.Errorf("run %s of task %s works in %s, so it stays", run.ID, run.TaskID, record.Path)
	}
	repository := newDeleteRepositories(service.store).of(ctx, record.ProjectID)
	if repository.err != nil {
		return fmt.Errorf("open the repository of %s: %w", record.Path, repository.err)
	}
	if samePath(record.Path, repository.root) || samePath(record.Path, repository.project.Repository) {
		return fmt.Errorf("%s is the project checkout, which Inkuna never removes", record.Path)
	}
	if err := worktree.Remove(ctx, repository.root, record.Path); err != nil {
		if errors.Is(err, worktree.ErrDirty) {
			return fmt.Errorf("%s has uncommitted changes; commit or discard them first", record.Path)
		}
		return fmt.Errorf("remove worktree %s: %w", record.Path, err)
	}
	return service.store.ForgetKeptWorktree(ctx, record.Path)
}

// recordedBy returns the run that records path as its worktree, or nil.
func recordedBy(runs []storage.Execution, path string) *storage.Execution {
	for index := range runs {
		if samePath(runs[index].Worktree, path) {
			return &runs[index]
		}
	}
	return nil
}

// diskUsage is the size of the files under path, without following links.
// What cannot be read is left out, so a figure is still shown for the rest.
func diskUsage(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
