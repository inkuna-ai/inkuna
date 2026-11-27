package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/diff"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// diffTimeout bounds one read of a run's changes; git reads committed
// objects only, so this is generous.
const diffTimeout = time.Minute

// RunChanges lists the files a run changed in scope, with their line
// counts, but no patches; RunFilePatch loads those one file at a time. The
// result names the two commits it compared, which a view passes back as a
// range scope so each file's patch matches this list.
func (service *Service) RunChanges(runID string, scope diff.Scope) (diff.Change, error) {
	ctx, cancel := context.WithTimeout(service.context(), diffTimeout)
	defer cancel()
	repository, between, review, err := service.runDiff(ctx, runID, scope)
	if err != nil {
		return diff.Change{}, err
	}
	change, err := diff.Changes(ctx, repository, between, review)
	if err != nil {
		return diff.Change{}, fmt.Errorf("list the run's changes: %w", err)
	}
	return change, nil
}

// RunFilePatch loads one file of a run's change in scope. A patch over 1 MB
// or 20,000 lines comes back tooLarge unless loadAnyway is set, which
// loads it up to 10 MB or 200,000 lines; past that it is never loaded, and
// never cut short.
func (service *Service) RunFilePatch(runID string, scope diff.Scope, path string, loadAnyway bool) (diff.FilePatch, error) {
	ctx, cancel := context.WithTimeout(service.context(), diffTimeout)
	defer cancel()
	repository, between, review, err := service.runDiff(ctx, runID, scope)
	if err != nil {
		return diff.FilePatch{}, err
	}
	patch, err := diff.Patch(ctx, repository, between, review, path, loadAnyway)
	if err != nil {
		return diff.FilePatch{}, fmt.Errorf("load %s: %w", path, err)
	}
	return patch, nil
}

// RunCommits lists the commits on a run's branch, oldest first, each with
// what it changed and the step and agent that made it. Git decides which
// commits there are; the run's records name the step and agent, so a
// commit Inkuna has no record of, such as one made by hand in the
// worktree, is external. Each opens in the Changes view as a commit scope.
func (service *Service) RunCommits(runID string) ([]BranchCommit, error) {
	ctx, cancel := context.WithTimeout(service.context(), diffTimeout)
	defer cancel()
	repository, run, records, err := service.runSource(ctx, runID)
	if err != nil {
		return nil, err
	}
	commits, err := diff.Commits(ctx, repository, run)
	if err != nil {
		return nil, fmt.Errorf("list the run's commits: %w", err)
	}
	byHash := make(map[string]storage.CommitRecord, len(records))
	unnamed := false
	for _, record := range records {
		byHash[record.Hash] = record
		unnamed = unnamed || record.Agent == ""
	}
	// Commits from before the agent was recorded take the agent the run's
	// workflow gave their step.
	planned := map[string]string{}
	if unnamed {
		planned, err = service.stepAgents(ctx, runID)
		if err != nil {
			return nil, err
		}
	}
	views := make([]BranchCommit, 0, len(commits))
	for _, commit := range commits {
		view := BranchCommit{Hash: commit.Hash, ShortHash: commit.ShortHash, Subject: commit.Subject, Body: commit.Body, Author: commit.Author, Date: commit.Date, Merge: commit.Merge, Files: commit.Files, Additions: commit.Additions, Deletions: commit.Deletions}
		if record, ok := byHash[commit.Hash]; ok {
			view.StepID, view.Agent = record.StepID, record.Agent
			if view.Agent == "" {
				view.Agent = planned[record.StepID]
			}
		} else {
			view.External = true
		}
		views = append(views, view)
	}
	return views, nil
}

// stepAgents maps each step of a run's workflow to its agent.
func (service *Service) stepAgents(ctx context.Context, runID string) (map[string]string, error) {
	execution, err := service.execution(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}
	workflow, err := service.store.Workflow(ctx, execution.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("load the run's workflow: %w", err)
	}
	agents := make(map[string]string, len(workflow.Steps))
	for _, step := range workflow.Steps {
		agents[step.ID] = step.Agent
	}
	return agents, nil
}

// RunFileExcerpt reads unchanged lines of a file in a run's change, to
// expand the context around its hunks: count lines from newStart in the
// newer version, numbered from oldStart in the older one.
func (service *Service) RunFileExcerpt(runID string, scope diff.Scope, path string, oldStart, newStart, count int) (diff.FileExcerpt, error) {
	ctx, cancel := context.WithTimeout(service.context(), diffTimeout)
	defer cancel()
	repository, between, _, err := service.runDiff(ctx, runID, scope)
	if err != nil {
		return diff.FileExcerpt{}, err
	}
	excerpt, err := diff.Excerpt(ctx, repository, between, path, oldStart, newStart, count)
	if err != nil {
		return diff.FileExcerpt{}, fmt.Errorf("expand %s: %w", path, err)
	}
	return excerpt, nil
}

// blobPattern is a git object hash, SHA-1 or SHA-256.
var blobPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// RunViewed lists the files marked viewed in a run's changes, each at the
// blob it was marked on. A file whose blob has changed since is not viewed.
func (service *Service) RunViewed(runID string) ([]storage.ViewedFile, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	if _, err := service.execution(service.context(), runID); err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}
	return service.store.ViewedFiles(service.context(), runID)
}

// SetFileViewed marks a file of a run's changes viewed at its blob, or
// clears that mark.
func (service *Service) SetFileViewed(runID, path, blob string, viewed bool) error {
	if err := service.ready(); err != nil {
		return err
	}
	if path == "" || len(path) > 4096 || strings.ContainsRune(path, 0) {
		return fmt.Errorf("%q is not a file path", path)
	}
	if !blobPattern.MatchString(blob) {
		return fmt.Errorf("%q is not a blob hash", blob)
	}
	if _, err := service.execution(service.context(), runID); err != nil {
		return fmt.Errorf("load run %s: %w", runID, err)
	}
	if err := service.store.SetViewed(service.context(), runID, storage.ViewedFile{Path: path, Blob: blob}, viewed); err != nil {
		return fmt.Errorf("mark %s viewed: %w", path, err)
	}
	return nil
}

// BranchCommit is one commit on a run's branch. StepID and Agent name the
// step that made it and its agent; an External commit is one Inkuna has no
// record of making, so it has neither.
type BranchCommit struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"shortHash"`
	Subject   string `json:"subject"`
	Body      string `json:"body,omitempty"`
	Author    string `json:"author"`
	Date      string `json:"date"`
	StepID    string `json:"stepId,omitempty"`
	Agent     string `json:"agent,omitempty"`
	External  bool   `json:"external,omitempty"`
	// Merge marks a commit with more than one parent; its counts are
	// against its first parent, as its commit scope is.
	Merge     bool `json:"merge,omitempty"`
	Files     int  `json:"files"`
	Additions int  `json:"additions"`
	Deletions int  `json:"deletions"`
}

// runDiff finds what a run's changes are read from: the project
// repository, which shares the objects of every worktree it had, so the
// changes stay readable after the worktree is removed; the two commits the
// scope compares; and the repository's review config, read from its
// checkout like the rest of .inkuna/.
func (service *Service) runDiff(ctx context.Context, runID string, scope diff.Scope) (string, diff.Range, diff.Review, error) {
	repository, run, _, err := service.runSource(ctx, runID)
	if err != nil {
		return "", diff.Range{}, diff.Review{}, err
	}
	between, err := diff.Resolve(ctx, repository, run, scope)
	if err != nil {
		return "", diff.Range{}, diff.Review{}, err
	}
	review, err := diff.LoadReview(repository)
	if err != nil {
		return "", diff.Range{}, diff.Review{}, err
	}
	return repository, between, review, nil
}

// runSource finds the repository a run's changes are read from and what
// the diff model needs to know about the run, with its commit records.
func (service *Service) runSource(ctx context.Context, runID string) (string, diff.Run, []storage.CommitRecord, error) {
	if err := service.ready(); err != nil {
		return "", diff.Run{}, nil, err
	}
	execution, err := service.execution(ctx, runID)
	if err != nil {
		return "", diff.Run{}, nil, fmt.Errorf("load run %s: %w", runID, err)
	}
	if execution.Worktree == "" && execution.Branch == "" {
		return "", diff.Run{}, nil, errors.New("this run has not started writing, so it has no changes yet")
	}
	project, err := service.store.Project(ctx, execution.ProjectID)
	if err != nil {
		return "", diff.Run{}, nil, fmt.Errorf("load project: %w", err)
	}
	records, err := service.store.Commits(ctx, runID)
	if err != nil {
		return "", diff.Run{}, nil, fmt.Errorf("load the run's commits: %w", err)
	}
	run := diff.Run{Base: execution.BaseCommit, Branch: execution.Branch, Commits: make([]string, 0, len(records))}
	for _, record := range records {
		run.Commits = append(run.Commits, record.Hash)
	}
	return project.Repository, run, records, nil
}
