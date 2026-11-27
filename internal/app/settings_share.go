package app

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// How ShareSettings puts the project checkout's settings edits on origin.
const (
	// SharePullRequest pushes them on a new branch and opens a pull request.
	SharePullRequest = "pull-request"
	// SharePush pushes them straight to the base.
	SharePush = "push"
)

// SettingsShare is what ShareSettings did.
type SettingsShare struct {
	Mode   string   `json:"mode"`
	Base   string   `json:"base"`
	Commit string   `json:"commit"`
	Files  []string `json:"files"`
	// Branch is the branch pushed for a pull request.
	Branch string `json:"branch,omitempty"`
	// URL is the pull request, or, when PullRequest is false, the page to
	// open one on.
	URL         string `json:"url,omitempty"`
	PullRequest bool   `json:"pullRequest,omitempty"`
	// Updated is the fast-forward of the base in its checkout after a push.
	Updated *worktree.BranchUpdate `json:"updated,omitempty"`
	// Note says what is left to do, such as why no pull request was opened.
	Note string `json:"note,omitempty"`
}

// ShareSettings puts the settings edits sitting uncommitted in the project
// checkout on origin, so the base can take them and keep syncing. It
// fetches, then commits exactly the files the user confirmed, by digest, on
// origin's base, without touching the checkout, and either pushes that
// commit on a new branch and opens a pull request or pushes it straight to
// the base. The user's click is the approval this push needs; nothing here
// pushes on its own. The checkout keeps the edits, still in effect for
// planning, until an update brings them back merged.
func (service *Service) ShareSettings(projectID, mode, digest string) (BranchSyncView, error) {
	if mode != SharePullRequest && mode != SharePush {
		return BranchSyncView{}, fmt.Errorf("share mode %q must be %s or %s", mode, SharePullRequest, SharePush)
	}
	// The fetch and the pushes get a deadline each, so a slow fetch cannot
	// cut a push off after origin has taken it.
	ctx, cancel := context.WithTimeout(service.context(), fetchTimeout)
	defer cancel()
	project, root, err := service.syncProject(ctx, projectID)
	if err != nil {
		return BranchSyncView{}, err
	}
	service.syncMu.Lock()
	defer service.syncMu.Unlock()
	if !worktree.HasOrigin(ctx, root) {
		return BranchSyncView{}, errors.New("this repository has no origin remote to share settings to")
	}
	base := syncBase(ctx, root)
	if base == "" {
		return BranchSyncView{}, errors.New("there is no base branch to share settings to; set branch.base in .inkuna/branching.yaml")
	}
	origin := worktree.Origin(ctx, root)
	if err := service.fetchOrigin(ctx, project.ID, root); err != nil {
		return BranchSyncView{}, errors.New(fetchFailure(err, origin))
	}
	baseRef := originBase(ctx, root, base)
	if baseRef == "" {
		return BranchSyncView{}, fmt.Errorf("origin has no branch %s to share settings to", base)
	}
	settings := service.projectSettings(project, root)
	status, err := worktree.ReadSettings(ctx, root, baseRef, settings)
	if err != nil {
		return BranchSyncView{}, fmt.Errorf("read settings changes: %w", err)
	}
	switch {
	case !status.Shareable && status.Reason != "":
		return BranchSyncView{}, errors.New(status.Reason)
	case status.Digest == "" || status.Digest != digest:
		return BranchSyncView{}, errors.New("the settings changed since they were shown; look at them again before sharing")
	case mode == SharePullRequest && status.SharedOn != "":
		return BranchSyncView{}, fmt.Errorf("these edits are already shared on %s; once it merges, update %s", status.SharedOn, base)
	}
	share := SettingsShare{Mode: mode, Base: base}
	for _, change := range status.Pending() {
		share.Files = append(share.Files, change.Path)
	}
	title, body := settingsMessage(share.Files)
	ctx, cancelShare := context.WithTimeout(service.context(), fetchTimeout)
	defer cancelShare()
	if share.Commit, err = worktree.CommitSettings(ctx, root, status, title, body); err != nil {
		return BranchSyncView{}, fmt.Errorf("commit settings: %w", err)
	}
	if mode == SharePush {
		if err := worktree.PushCommit(ctx, root, share.Commit, base); err != nil {
			return BranchSyncView{}, fmt.Errorf("push settings to %s: %w; open a pull request instead if %s is protected", base, err, base)
		}
		share.Updated, share.Note = service.settleBase(ctx, project, root, base, settings)
	} else {
		if share.Branch, err = settingsBranch(ctx, root, share.Files); err != nil {
			return BranchSyncView{}, err
		}
		if err := worktree.PublishBranch(ctx, root, share.Branch, share.Commit); err != nil {
			return BranchSyncView{}, fmt.Errorf("push %s: %w", share.Branch, err)
		}
		share.URL, share.PullRequest, share.Note = service.openPullRequest(ctx, origin, base, share.Branch, title, body)
	}
	view, err := service.branchSync(ctx, project, root)
	if err != nil {
		// The share happened; say so rather than leave it looking undone.
		return BranchSyncView{}, fmt.Errorf("shared %s as %s, but reading the branches again failed: %w", strings.Join(share.Files, ", "), share.Commit, err)
	}
	view.Shared = &share
	return view, nil
}

// settleBase fast-forwards the base after a push straight to it, so the
// edits it now has stop being uncommitted in the checkout. It leaves the
// base where it is, saying why, when a run writes on it or it cannot move.
func (service *Service) settleBase(ctx context.Context, project storage.Project, root, base string, settings worktree.Settings) (*worktree.BranchUpdate, string) {
	checkout, err := worktree.CheckoutOf(ctx, root, base)
	if err != nil {
		return nil, "Pushed, but " + base + " was not updated here: " + err.Error()
	}
	if runID := service.runOnBranch(project.ID, base, checkout); runID != "" {
		return nil, fmt.Sprintf("Pushed. Run %s is writing on %s, so update it once the run finishes.", runID, base)
	}
	update, err := worktree.UpdateBranch(ctx, root, base, settings)
	if err != nil {
		return nil, "Pushed, but " + base + " was not updated here: " + err.Error()
	}
	return &update, ""
}

// openPullRequest opens the share's pull request with gh. When gh cannot,
// it returns GitHub's page for opening one instead, saying why, and for an
// origin elsewhere it says to open one there.
func (service *Service) openPullRequest(ctx context.Context, origin, base, branch, title, body string) (string, bool, string) {
	host, repository, _ := strings.Cut(origin, "/")
	if host != "github.com" || repository == "" {
		return "", false, "Open a pull request for it on origin."
	}
	compare := "https://github.com/" + repository + "/compare/" + base + "..." + branch + "?expand=1"
	if service.github == nil {
		return compare, false, "Open the pull request on GitHub."
	}
	output, err := service.github.Run(ctx, "pr", "create", "--repo", origin, "--base", base, "--head", branch, "--title", title, "--body", body)
	if err != nil {
		return compare, false, "gh could not open the pull request, so open it on GitHub: " + strings.TrimSpace(err.Error()+" "+string(output))
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if url := strings.TrimSpace(lines[len(lines)-1]); strings.HasPrefix(url, "https://") {
		return url, true, ""
	}
	return compare, false, "gh did not report the pull request; find it on GitHub."
}

// projectSettings names the project's settings files in its repository's
// main checkout: .inkuna where the project keeps it, and a rules file
// inkuna.yaml declares elsewhere in the checkout.
func (service *Service) projectSettings(project storage.Project, root string) worktree.Settings {
	return worktree.SettingsAt(root, project.Repository, service.rulesPath(project))
}

// projectSettingsStatus reads the checkout's settings edits against origin's
// base. Without origin there is nowhere to share them, which the status
// says, but they are still listed.
func (service *Service) projectSettingsStatus(ctx context.Context, project storage.Project, root string, status worktree.SyncStatus) (*worktree.SettingsStatus, error) {
	baseRef := ""
	if status.HasOrigin && status.Base != "" {
		baseRef = originBase(ctx, root, status.Base)
	}
	settings, err := worktree.ReadSettings(ctx, root, baseRef, service.projectSettings(project, root))
	if err != nil {
		return nil, fmt.Errorf("read settings changes of %s: %w", project.Name, err)
	}
	if len(settings.Changes) == 0 {
		return nil, nil
	}
	return &settings, nil
}

// originBase is origin/<base> when the last fetch brought it in, else "".
func originBase(ctx context.Context, root, base string) string {
	if start, err := worktree.StartPoint(ctx, root, base); err == nil && strings.HasPrefix(start, "origin/") {
		return start
	}
	return ""
}

// settingsBranch names a share's branch by the branching configuration, as a
// chore when it allows one, and numbers it when the name is taken. Without a
// complete configuration it is inkuna/settings.
func settingsBranch(ctx context.Context, root string, files []string) (string, error) {
	description := "inkuna settings"
	if len(files) == 1 {
		description += " " + strings.TrimSuffix(path.Base(files[0]), path.Ext(files[0]))
	}
	name, maxLength := "inkuna/settings", 0
	if loaded, err := branching.Load(root); err == nil && loaded.Complete() {
		task := domain.TaskSpec{Title: description, Labels: []string{"chore"}}
		if named, err := loaded.Config.ForTask(task); err == nil {
			name, maxLength = named, loaded.Config.Branch.MaxLength
		}
	}
	return branching.Unique(name, maxLength, func(candidate string) bool { return worktree.BranchTaken(ctx, root, candidate) })
}

// settingsMessage is the share's commit subject and body, which the pull
// request reuses.
func settingsMessage(files []string) (string, string) {
	title := "Update Inkuna settings"
	if len(files) == 1 {
		title = "Update " + files[0]
	}
	var body strings.Builder
	body.WriteString("Shares the Inkuna settings edited in the project checkout:\n\n")
	for _, file := range files {
		body.WriteString("- `" + file + "`\n")
	}
	body.WriteString("\nShared from Inkuna's Branches view.")
	return title, body.String()
}
