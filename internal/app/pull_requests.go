package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/branching"
	inkunagithub "github.com/inkuna-ai/inkuna/internal/github"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// pullRequestFreshness is how long a project's pull requests, once read,
// are not read again unless a person asks: the Tasks view checks each time
// it opens, and switching views should not call GitHub each time.
const pullRequestFreshness = 5 * time.Minute

// recentPullRequests is how many of a repository's most recently updated
// pull requests one check reads. One that merged since the last check is
// among them; an older one found before keeps its link.
const recentPullRequests = 100

// pullRequestCheckTimeout bounds one check, every project included.
const pullRequestCheckTimeout = 2 * time.Minute

// PullRequestView is the pull request a run's branch was opened in, as
// Inkuna last found it.
type PullRequestView struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	// State is OPEN, CLOSED, or MERGED.
	State    string `json:"state"`
	MergedAt string `json:"mergedAt,omitempty"`
	// CheckedAt is when a check last found it.
	CheckedAt string `json:"checkedAt"`
}

// PullRequestCheck is what reading the projects' pull requests found and
// did.
type PullRequestCheck struct {
	// Checked counts the projects whose pull requests were read now; one
	// read in the last few minutes is skipped unless a person asked.
	Checked int `json:"checked"`
	// Linked counts the branches the check found a pull request for.
	Linked int `json:"linked"`
	// Removed lists the worktrees removed because their branch's pull
	// request merged, in repositories whose worktrees.cleanup is merged.
	Removed []string `json:"removed"`
	// Problems say what could not be read or removed; the rest still was.
	Problems []string `json:"problems,omitempty"`
}

// CheckPullRequests finds the pull request each run branch was opened in,
// with one gh call per project, and records it for the Tasks view. In a
// repository whose worktrees.cleanup is merged, it then removes the clean
// worktree of each ended run whose branch's pull request merged, and keeps
// the branch. A project read in the last few minutes is skipped unless force
// is set, as Check now sets it. It never deletes history.
func (service *Service) CheckPullRequests(force bool) (PullRequestCheck, error) {
	if err := service.ready(); err != nil {
		return PullRequestCheck{}, err
	}
	check := PullRequestCheck{Removed: []string{}}
	// The Tasks view checks each time it opens, and one opened while a
	// check runs has nothing to add to it; Check now waits its turn.
	if force {
		service.pullsMu.Lock()
	} else if !service.pullsMu.TryLock() {
		return check, nil
	}
	defer service.pullsMu.Unlock()
	ctx, cancel := context.WithTimeout(service.context(), pullRequestCheckTimeout)
	defer cancel()
	projects, err := service.store.Projects(ctx)
	if err != nil {
		return PullRequestCheck{}, fmt.Errorf("check pull requests: %w", err)
	}
	if service.pullChecks == nil {
		service.pullChecks = make(map[string]time.Time)
	}
	for _, project := range projects {
		// A failed read waits as long as a good one, so a missing sign-in
		// is not asked about each time the view opens.
		if !force && time.Since(service.pullChecks[project.ID]) < pullRequestFreshness {
			continue
		}
		service.pullChecks[project.ID] = time.Now()
		read, linked, err := service.linkPullRequests(ctx, project)
		if err != nil {
			check.Problems = append(check.Problems, err.Error())
		}
		if !read {
			continue
		}
		check.Checked++
		check.Linked += linked
		removed, problems := service.removeMergedWorktrees(ctx, project)
		check.Removed = append(check.Removed, removed...)
		check.Problems = append(check.Problems, problems...)
	}
	if check.Checked > 0 {
		service.emit("inkuna:runs", "")
	}
	return check, nil
}

// linkPullRequests reads the project's recent pull requests and records the
// one each of its run branches was opened in. read is false when there was
// nothing to read: no run is on a branch, or the repository has no origin.
func (service *Service) linkPullRequests(ctx context.Context, project storage.Project) (read bool, linked int, err error) {
	branches, err := service.store.RunBranches(ctx, project.ID)
	if err != nil {
		return false, 0, fmt.Errorf("could not list the run branches of %s: %w", project.Name, err)
	}
	if len(branches) == 0 {
		return false, 0, nil
	}
	origin := worktree.OriginRepository(ctx, project.Repository)
	if origin == "" {
		return false, 0, nil
	}
	pulls, err := inkunagithub.RecentPullRequests(ctx, service.github, origin, recentPullRequests)
	if err != nil {
		return false, 0, fmt.Errorf("could not read the pull requests of %s: %w", project.Name, err)
	}
	links := pickPullRequests(pulls, branches, time.Now().UTC())
	if err := service.store.SavePullRequests(ctx, project.ID, links); err != nil {
		return false, 0, fmt.Errorf("could not record the pull requests of %s: %w", project.Name, err)
	}
	return true, len(links), nil
}

// pickPullRequests links each branch to one pull request opened from it, as
// Mark done picks one: the most recently updated that merged, else the open
// one, else the most recently updated. A pull request from a fork is
// skipped, since its branch only shares a name with the run's.
func pickPullRequests(pulls []inkunagithub.PullRequest, branches []string, checked time.Time) []storage.PullRequestLink {
	byBranch := make(map[string][]inkunagithub.PullRequest, len(branches))
	order := make([]string, 0)
	for _, pull := range pulls {
		if pull.IsCrossRepository || !slices.Contains(branches, pull.HeadRefName) {
			continue
		}
		if byBranch[pull.HeadRefName] == nil {
			order = append(order, pull.HeadRefName)
		}
		byBranch[pull.HeadRefName] = append(byBranch[pull.HeadRefName], pull)
	}
	links := make([]storage.PullRequestLink, 0, len(order))
	for _, branch := range order {
		pull, merged := inkunagithub.MergedPull(byBranch[branch])
		if !merged {
			pull, _ = inkunagithub.CurrentPull(byBranch[branch])
		}
		link := storage.PullRequestLink{Branch: branch, Number: pull.Number, Title: pull.Title, URL: pull.URL, State: strings.ToUpper(pull.State), CheckedAt: checked}
		if merged {
			link.MergedAt, _ = time.Parse(time.RFC3339, pull.MergedAt)
		}
		links = append(links, link)
	}
	return links
}

// removeMergedWorktrees removes, when the project's checkout chooses
// worktrees.cleanup merged, the clean worktree of each ended run whose
// branch's pull request merged. The policy is read now rather than from
// each run's plan, as prune applies the starting run's to every finished
// run, so choosing it also clears the runs from before. A branch a run is
// still active on keeps every worktree, and branches always stay.
func (service *Service) removeMergedWorktrees(ctx context.Context, project storage.Project) (removed, problems []string) {
	loaded, err := branching.Load(project.Repository)
	if err != nil {
		return nil, []string{fmt.Sprintf("Could not read the branching configuration of %s: %v", project.Name, err)}
	}
	if !loaded.Exists || loaded.Config.Worktrees.Cleanup != branching.CleanupMerged {
		return nil, nil
	}
	links, err := service.store.PullRequests(ctx)
	if err != nil {
		return nil, []string{err.Error()}
	}
	removed = make([]string, 0)
	for _, link := range links {
		if link.ProjectID != project.ID || !link.Merged() {
			continue
		}
		runs, err := service.store.ExecutionsOnBranch(ctx, project.ID, link.Branch)
		if err != nil {
			problems = append(problems, fmt.Sprintf("Could not list the runs on %s: %v", link.Branch, err))
			continue
		}
		// A live run's state is ahead of what is stored.
		for index, run := range runs {
			if live, err := service.execution(ctx, run.ID); err == nil {
				runs[index] = live
			}
		}
		if slices.ContainsFunc(runs, func(run storage.Execution) bool { return !terminalRunState(run.State) }) {
			continue
		}
		for _, run := range runs {
			if !removable(project, run) || slices.ContainsFunc(removed, func(path string) bool { return samePath(path, run.Worktree) }) {
				continue
			}
			// One removed outside Inkuna has nothing left to remove.
			if _, err := os.Stat(run.Worktree); errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err := service.removeWorktree(ctx, project, run); err != nil {
				problems = append(problems, fmt.Sprintf("Kept %s after pull request #%d merged: %v", run.Worktree, link.Number, err))
				continue
			}
			removed = append(removed, run.Worktree)
			service.noteWorkspace(run.ID, fmt.Sprintf("Removed the worktree: pull request #%d merged; the branch is kept", link.Number))
		}
	}
	if len(removed) > 0 {
		_ = worktree.Prune(ctx, project.Repository)
	}
	return removed, problems
}

// attachPullRequests gives each run the pull request its branch was opened
// in, as the last check found it.
func (service *Service) attachPullRequests(views []WorkflowRunView) {
	if service.store == nil || len(views) == 0 {
		return
	}
	links, err := service.store.PullRequests(service.context())
	if err != nil || len(links) == 0 {
		return
	}
	found := make(map[[2]string]storage.PullRequestLink, len(links))
	for _, link := range links {
		found[[2]string{link.ProjectID, link.Branch}] = link
	}
	for index := range views {
		link, ok := found[[2]string{views[index].ProjectID, views[index].Branch}]
		if !ok || views[index].Branch == "" {
			continue
		}
		view := &PullRequestView{Number: link.Number, Title: link.Title, URL: link.URL, State: link.State, CheckedAt: link.CheckedAt.Format(time.RFC3339Nano)}
		if !link.MergedAt.IsZero() {
			view.MergedAt = link.MergedAt.Format(time.RFC3339Nano)
		}
		views[index].PullRequest = view
	}
}
