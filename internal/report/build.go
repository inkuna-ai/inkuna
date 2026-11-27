// Package report renders a run's changes as one self-contained HTML page,
// like a pull request's: its files and their diffs, its commits, and its
// checks. It reads the run from Inkuna's database and artifact store and the
// changes from git, through the same diff model as the app, and writes a
// page that needs nothing else to open.
package report

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/artifact"
	"github.com/inkuna-ai/inkuna/internal/diff"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// Options choose what the page shows.
type Options struct {
	// Scope is the run's changes to show; the zero scope is the branch.
	Scope diff.Scope
	// Split opens the page with the two versions side by side.
	Split bool
	// IncludeLarge loads patches over the soft caps, up to the hard ones.
	IncludeLarge bool
	// Generated, when set, is shown as when the page was made. Left zero,
	// the page holds no time, so the same run renders the same bytes.
	Generated time.Time
}

// Page is everything the template shows.
type Page struct {
	RunID      string
	Title      string
	Branch     string
	Base       string
	ScopeLabel string
	// Commit is the commit a commit scope shows.
	Commit    *Commit
	From, To  string
	Files     []File
	Tree      []Node
	Additions int
	Deletions int
	Commits   []Commit
	Checks    []Check
	// Comments are the sent comment threads not shown at their lines.
	Comments []Thread
	// Passed and Total count the checks of each evaluation step's latest
	// attempt.
	Passed, Failed, Total int
	Split                 bool
	Generated             string
}

// File is one changed file, with both layouts of its diff.
type File struct {
	Anchor    string
	Path      string
	OldPath   string
	Status    string
	Letter    string
	Additions int
	Deletions int
	Blob      string
	Generated bool
	// Note says why the file has no rows: binary, too large, renamed
	// without changes, and so on.
	Note    string
	Unified []Row
	Split   []SplitRow
}

// Commit is one commit on the run's branch.
type Commit struct {
	Hash      string
	ShortHash string
	Subject   string
	Body      string
	Author    string
	Date      string
	StepID    string
	Agent     string
	External  bool
	Merge     bool
	Files     int
	Additions int
	Deletions int
}

// Check is one command of an evaluation step.
type Check struct {
	Step     string
	Command  string
	Status   string
	ExitCode string
	Duration string
	Error    string
	// Log is a failed check's kept log, as styled lines.
	Log    []LogLine
	LogCut bool
}

// Build reads a run and its changes into a page.
func Build(ctx context.Context, store *storage.Store, artifacts *artifact.Store, runID string, options Options) (Page, error) {
	execution, err := store.Execution(ctx, runID)
	if err != nil {
		return Page{}, fmt.Errorf("load run %s: %w", runID, err)
	}
	if execution.Worktree == "" && execution.Branch == "" {
		return Page{}, errors.New("this run has not started writing, so it has no changes yet")
	}
	project, err := store.Project(ctx, execution.ProjectID)
	if err != nil {
		return Page{}, fmt.Errorf("load project: %w", err)
	}
	records, err := store.Commits(ctx, runID)
	if err != nil {
		return Page{}, fmt.Errorf("load the run's commits: %w", err)
	}
	run := diff.Run{Base: execution.BaseCommit, Branch: execution.Branch}
	for _, record := range records {
		run.Commits = append(run.Commits, record.Hash)
	}
	scope := options.Scope
	if scope.Kind == "" {
		scope = diff.Scope{Kind: diff.ScopeBranch}
	}
	repository := project.Repository
	between, err := diff.Resolve(ctx, repository, run, scope)
	if err != nil {
		return Page{}, err
	}
	review, err := diff.LoadReview(repository)
	if err != nil {
		return Page{}, err
	}
	change, err := diff.Changes(ctx, repository, between, review)
	if err != nil {
		return Page{}, fmt.Errorf("list the run's changes: %w", err)
	}
	page := Page{
		RunID: runID, Title: execution.Title, Branch: execution.Branch, Base: short(between.From),
		From: between.From, To: between.To, Additions: change.Additions, Deletions: change.Deletions,
		Split: options.Split, ScopeLabel: "All changes",
	}
	if !options.Generated.IsZero() {
		page.Generated = options.Generated.UTC().Format(time.RFC3339)
	}

	page.Commits, err = commits(ctx, store, repository, run, records, execution.WorkflowID)
	if err != nil {
		return Page{}, err
	}
	if scope.Kind == diff.ScopeCommit {
		for index := range page.Commits {
			if page.Commits[index].Hash == between.To {
				page.Commit = &page.Commits[index]
				page.ScopeLabel = "Commit " + page.Commit.ShortHash
			}
		}
	}

	page.Tree, page.Files = tree(change.Files)
	var paths []string
	var pending []*File
	for index := range page.Files {
		if file := &page.Files[index]; file.Note == "" {
			paths, pending = append(paths, file.Path), append(pending, file)
		}
	}
	if len(paths) > 0 {
		patches, err := diff.Patches(ctx, repository, between, review, paths, options.IncludeLarge)
		if err != nil {
			return Page{}, err
		}
		for index, file := range pending {
			fill(file, patches[index])
		}
	}

	if err := page.addComments(ctx, store, repository, runID, scope.Kind, between); err != nil {
		return Page{}, err
	}
	if err := page.addChecks(ctx, store, artifacts, runID, execution.WorkflowID); err != nil {
		return Page{}, err
	}
	return page, nil
}

// fill lays a file's patch out both ways, or says why there is nothing to
// lay out.
func fill(file *File, patch diff.FilePatch) {
	switch {
	case patch.TooLarge && patch.Loadable:
		file.Note = patch.Reason + " Run inkunactl diff again with --include-large to include it."
	case patch.TooLarge:
		file.Note = patch.Reason
	case patch.File.Binary:
		file.Note = "Binary file not shown."
	case len(patch.Hunks) == 0:
		file.Note = noLines(file)
	default:
		file.Unified, file.Split = layout(patch.Hunks)
	}
}

// commits lists the branch's commits with the step and agent each was made
// by, as the app's Commits view does: the run's records name them, and a
// record from before agents were kept takes its step's agent from the
// workflow.
func commits(ctx context.Context, store *storage.Store, repository string, run diff.Run, records []storage.CommitRecord, workflowID string) ([]Commit, error) {
	listed, err := diff.Commits(ctx, repository, run)
	if err != nil {
		return nil, fmt.Errorf("list the run's commits: %w", err)
	}
	byHash := make(map[string]storage.CommitRecord, len(records))
	for _, record := range records {
		byHash[record.Hash] = record
	}
	planned := map[string]string{}
	if workflow, err := store.Workflow(ctx, workflowID); err == nil {
		for _, step := range workflow.Steps {
			planned[step.ID] = step.Agent
		}
	}
	views := make([]Commit, 0, len(listed))
	for _, commit := range listed {
		view := Commit{Hash: commit.Hash, ShortHash: commit.ShortHash, Subject: commit.Subject, Body: commit.Body, Author: authorName(commit.Author), Date: commitDate(commit.Date), Merge: commit.Merge, Files: commit.Files, Additions: commit.Additions, Deletions: commit.Deletions}
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

// addChecks lists the run's checks, each failed one with its kept log, and
// counts those of each evaluation step's latest attempt.
func (page *Page) addChecks(ctx context.Context, store *storage.Store, artifacts *artifact.Store, runID, workflowID string) error {
	records, err := store.Checks(ctx, runID)
	if err != nil {
		return err
	}
	titles := map[string]string{}
	if workflow, err := store.Workflow(ctx, workflowID); err == nil {
		for _, step := range workflow.Steps {
			titles[step.ID] = step.Title
		}
	}
	latest := make(map[string]string)
	for _, record := range records {
		latest[record.StepID] = record.StepRunID
	}
	for _, record := range records {
		if latest[record.StepID] != record.StepRunID {
			continue
		}
		page.Total++
		check := Check{Step: firstNonEmpty(titles[record.StepID], record.StepID), Command: strings.Join(record.Argv, " "), Status: record.Status, Error: record.Error, LogCut: record.LogCut}
		if record.ExitCode != nil {
			check.ExitCode = fmt.Sprint(*record.ExitCode)
		}
		if !record.StartedAt.IsZero() && !record.FinishedAt.IsZero() {
			check.Duration = duration(record.FinishedAt.Sub(record.StartedAt))
		}
		switch record.Status {
		case storage.CheckPassed:
			page.Passed++
		case storage.CheckFailed:
			page.Failed++
			// A log that cannot be read, pruned or damaged, costs the page
			// only that log: the check still shows how it ended.
			if record.LogDigest != "" && artifacts != nil {
				if text, err := readLog(artifacts, record.LogDigest); err != nil {
					check.Error = strings.TrimPrefix(check.Error+"; its log could not be read", "; ")
				} else {
					check.Log = logLines(text)
				}
			}
		}
		page.Checks = append(page.Checks, check)
	}
	return nil
}

func readLog(artifacts *artifact.Store, digest string) (string, error) {
	file, err := artifacts.Open(digest)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	return string(data), err
}

func noLines(file *File) string {
	switch file.Status {
	case string(diff.StatusRenamed):
		return "File renamed without changes."
	case string(diff.StatusCopied):
		return "File copied without changes."
	case string(diff.StatusAdded):
		return "Empty file added."
	case string(diff.StatusDeleted):
		return "Empty file deleted."
	}
	return "No lines changed."
}

func duration(value time.Duration) string {
	switch {
	case value < time.Second:
		return fmt.Sprintf("%d ms", value.Milliseconds())
	case value < time.Minute:
		return fmt.Sprintf("%.1f s", value.Seconds())
	default:
		return fmt.Sprintf("%d min %02d s", int(value.Minutes()), int(value.Seconds())%60)
	}
}

// commitDate shows a commit's date in UTC, so the page reads the same
// wherever it is made.
func commitDate(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}

// authorName drops the address from "Name <address>", which the page has
// no need to spread.
func authorName(author string) string {
	if open := strings.LastIndex(author, " <"); open > 0 && strings.HasSuffix(author, ">") {
		return author[:open]
	}
	return author
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
