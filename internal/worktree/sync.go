package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A local branch's standing against its upstream, as Branches reports it.
const (
	SyncSynced   = "synced"
	SyncBehind   = "behind"
	SyncAhead    = "ahead"
	SyncDiverged = "diverged"
	// SyncGone means the branch's upstream was deleted on the remote.
	SyncGone = "gone"
	// SyncLocal means the branch tracks no upstream.
	SyncLocal = "local"
)

// MaxSyncBranches bounds how many branches Branches measures, most recently
// committed first, since each one costs a git call.
const MaxSyncBranches = 100

// ErrNotSignedIn means origin refused git's credentials, or git had none to
// offer without prompting.
var ErrNotSignedIn = errors.New("git could not sign in to origin")

// BranchSync is one local branch measured against its upstream and the base.
type BranchSync struct {
	Name string `json:"name"`
	// Upstream is the branch it tracks, such as origin/main; empty when it
	// tracks none.
	Upstream string `json:"upstream,omitempty"`
	State    string `json:"state"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	// BehindBase counts the base's commits the branch does not have, which
	// is how far a run's branch, tracking nothing, has fallen behind.
	BehindBase  int    `json:"behindBase"`
	IsBase      bool   `json:"isBase,omitempty"`
	Commit      string `json:"commit"`
	CommittedAt string `json:"committedAt"`
	// CheckedOut is the checkout that has the branch checked out, if any;
	// Main says it is the repository's main checkout.
	CheckedOut string `json:"checkedOut,omitempty"`
	Main       bool   `json:"main,omitempty"`
	Dirty      bool   `json:"dirty,omitempty"`
	// Updatable says UpdateBranch would fast-forward the branch. Reason says
	// why a branch that is behind cannot be.
	Updatable bool   `json:"updatable"`
	Reason    string `json:"reason,omitempty"`
	// Unique counts, for a branch without an upstream, the commits no other
	// local branch or remote-tracking ref has: what deleting it would drop.
	Unique int `json:"unique"`
	// Deletable says DeleteBranch would delete a branch without an upstream;
	// DeleteReason says why one cannot be.
	Deletable    bool   `json:"deletable"`
	DeleteReason string `json:"deleteReason,omitempty"`
	// upstreamRef is Upstream as a full ref, for measuring an update.
	upstreamRef string
}

// SyncStatus is a repository's local branches measured against what the
// last fetch brought in from origin. Reading it never touches the network.
type SyncStatus struct {
	Repository string `json:"repository"`
	HasOrigin  bool   `json:"hasOrigin"`
	// Origin names origin as host/owner/repo; empty for a URL of another
	// shape.
	Origin string `json:"origin,omitempty"`
	Base   string `json:"base,omitempty"`
	// BaseRef is what BehindBase counts against: origin/<base> when it has
	// been fetched, else the local <base>.
	BaseRef   string       `json:"baseRef,omitempty"`
	FetchedAt string       `json:"fetchedAt,omitempty"`
	Branches  []BranchSync `json:"branches"`
	// Truncated says branches past MaxSyncBranches were left out.
	Truncated bool `json:"truncated,omitempty"`
}

// BranchUpdate is what UpdateBranch did. From equals To when the branch was
// already at its upstream.
type BranchUpdate struct {
	Branch   string `json:"branch"`
	Upstream string `json:"upstream"`
	From     string `json:"from"`
	To       string `json:"to"`
	// CheckedOut is the checkout fast-forwarded along with the branch.
	CheckedOut string `json:"checkedOut,omitempty"`
	// Kept counts the checkout's uncommitted settings edits and untracked
	// files that the fast-forward left as they were.
	Kept int `json:"kept,omitempty"`
	// Settled lists the uncommitted settings edits the upstream already
	// had, which the fast-forward took as merged, so they are no longer
	// uncommitted.
	Settled []string `json:"settled,omitempty"`
}

// Branches measures the repository's local branches against their
// upstreams and against base, which may be empty when there is none.
// Settings names the files whose uncommitted edits an update carries.
func Branches(ctx context.Context, repository, base string, settings Settings) (SyncStatus, error) {
	status := SyncStatus{Repository: repository, Base: base, Branches: []BranchSync{}}
	status.HasOrigin, status.Origin, status.FetchedAt = originOf(ctx, repository)
	if base != "" {
		if start, err := StartPoint(ctx, repository, base); err == nil {
			status.BaseRef = start
		}
	}
	branches, err := localBranches(ctx, repository, base)
	if err != nil {
		return SyncStatus{}, err
	}
	if len(branches) > MaxSyncBranches {
		branches, status.Truncated = branches[:MaxSyncBranches], true
	}
	for _, branch := range branches {
		if status.BaseRef != "" && status.BaseRef != branch.Name {
			if branch.BehindBase, err = countCommits(ctx, repository, "refs/heads/"+branch.Name, status.BaseRef); err != nil {
				return SyncStatus{}, err
			}
		}
		judgeUpdate(ctx, &branch, settings)
		if err := judgeDelete(ctx, repository, base, &branch); err != nil {
			return SyncStatus{}, err
		}
		status.Branches = append(status.Branches, branch)
	}
	return status, nil
}

// SyncSummary is how a repository stands against origin at a glance. It
// costs a few git calls however many branches there are, so every project
// card can show one.
type SyncSummary struct {
	HasOrigin bool   `json:"hasOrigin"`
	Origin    string `json:"origin,omitempty"`
	Base      string `json:"base,omitempty"`
	FetchedAt string `json:"fetchedAt,omitempty"`
	// Checkout is the branch checked out in the main checkout, measured
	// against its upstream only; nil when HEAD is detached.
	Checkout *BranchSync `json:"checkout,omitempty"`
	Branches int         `json:"branches"`
	// OutOfSync counts branches behind, diverged from, or gone from their
	// upstream; NoUpstream counts branches that track nothing.
	OutOfSync  int `json:"outOfSync"`
	NoUpstream int `json:"noUpstream"`
}

// Summarize reads a SyncSummary from what the last fetch brought in.
func Summarize(ctx context.Context, repository, base string) (SyncSummary, error) {
	summary := SyncSummary{Base: base}
	summary.HasOrigin, summary.Origin, summary.FetchedAt = originOf(ctx, repository)
	branches, err := localBranches(ctx, repository, base)
	if err != nil {
		return SyncSummary{}, err
	}
	summary.Branches = len(branches)
	for _, branch := range branches {
		switch branch.State {
		case SyncBehind, SyncDiverged, SyncGone:
			summary.OutOfSync++
		case SyncLocal:
			summary.NoUpstream++
		}
		if branch.Main {
			checkout := branch
			summary.Checkout = &checkout
		}
	}
	return summary, nil
}

// Origin names the repository's origin as host/owner/repo, or returns ""
// when it has none or its URL has another shape.
func Origin(ctx context.Context, repository string) string {
	_, name, _ := originOf(ctx, repository)
	return name
}

// originOf says whether the repository has origin, names it, and says when
// it was last fetched.
func originOf(ctx context.Context, repository string) (has bool, name, fetchedAt string) {
	if remote, err := git(ctx, repository, "remote", "get-url", "origin"); err == nil {
		has, name = true, RemoteRepository(remote)
	}
	if fetched, ok := lastFetch(ctx, repository); ok {
		fetchedAt = fetched.UTC().Format(time.RFC3339)
	}
	return has, name, fetchedAt
}

// localBranches lists every local branch, most recently committed first,
// with its standing against its upstream and where it is checked out: all
// that one for-each-ref and one worktree list can tell.
func localBranches(ctx context.Context, repository, base string) ([]BranchSync, error) {
	checkouts, err := checkedOutBranches(ctx, repository)
	if err != nil {
		return nil, err
	}
	refs, err := git(ctx, repository, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)%1f%(upstream)%1f%(upstream:track,nobracket)%1f%(objectname:short)%1f%(committerdate:iso-strict)", "refs/heads")
	if err != nil {
		return nil, err
	}
	branches := []BranchSync{}
	for _, line := range strings.Split(refs, "\n") {
		fields := strings.Split(line, "\x1f")
		if len(fields) != 5 {
			continue
		}
		branch := BranchSync{
			Name:        strings.TrimPrefix(fields[0], "refs/heads/"),
			Upstream:    shortRef(fields[1]),
			Commit:      fields[3],
			CommittedAt: fields[4],
			upstreamRef: fields[1],
		}
		branch.IsBase = base != "" && branch.Name == base
		ahead, behind, gone := parseTrack(fields[2])
		branch.Ahead, branch.Behind = ahead, behind
		branch.State = syncState(branch.Upstream, gone, ahead, behind)
		if checkout, ok := checkouts.byBranch[branch.Name]; ok {
			branch.CheckedOut, branch.Main = checkout, checkout == checkouts.main
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

// FetchOrigin brings every branch of origin into its remote-tracking refs and
// drops those of branches deleted there, so a gone upstream shows. It moves
// no local branch. Prompts are off: a desktop app has no terminal to answer
// one, so a missing credential fails at once with ErrNotSignedIn.
func FetchOrigin(ctx context.Context, repository string) error {
	_, err := gitEnv(ctx, repository, []string{"GIT_TERMINAL_PROMPT=0"}, "fetch", "--prune", "--quiet", "origin")
	if err != nil && signInFailed(err.Error()) {
		return fmt.Errorf("%w: %w", ErrNotSignedIn, err)
	}
	return err
}

// UpdateBranch fast-forwards a local branch to its upstream and does nothing
// else: it never merges, rebases, pushes, or moves a branch that has commits
// its upstream lacks. A branch nobody has checked out moves only if it still
// points where it was measured, so a concurrent change is never overwritten.
// One that is checked out moves with its checkout, which may hold
// uncommitted edits to settings and untracked files, but no other edits,
// and none of them on a path the fast-forward writes (see planCarry).
func UpdateBranch(ctx context.Context, repository, branch string, settings Settings) (BranchUpdate, error) {
	if strings.TrimSpace(branch) == "" || strings.HasPrefix(branch, "-") {
		return BranchUpdate{}, fmt.Errorf("invalid branch name %q", branch)
	}
	ref := "refs/heads/" + branch
	upstream, err := upstreamOf(ctx, repository, ref)
	if err != nil {
		return BranchUpdate{}, err
	}
	update := BranchUpdate{Branch: branch, Upstream: shortRef(upstream)}
	if upstream == "" {
		return update, fmt.Errorf("%s tracks no remote branch, so there is nothing to update it from", branch)
	}
	if !refExists(ctx, repository, upstream) {
		return update, fmt.Errorf("%s's upstream %s is gone; it was deleted on the remote or has not been fetched", branch, update.Upstream)
	}
	if update.From, err = git(ctx, repository, "rev-parse", "--verify", ref); err != nil {
		return update, err
	}
	if update.To, err = git(ctx, repository, "rev-parse", "--verify", upstream); err != nil {
		return update, err
	}
	if update.From == update.To {
		return update, nil
	}
	behind, err := isAncestor(ctx, repository, update.From, update.To)
	if err != nil {
		return update, err
	}
	if !behind {
		return update, fmt.Errorf("%s has commits %s does not; Inkuna only fast-forwards, so merge or rebase it yourself", branch, update.Upstream)
	}
	checkout, err := CheckoutOf(ctx, repository, branch)
	if err != nil {
		return update, err
	}
	if checkout == "" {
		_, err = git(ctx, repository, "update-ref", "-m", "inkuna: fast-forward to "+update.Upstream, ref, update.To, update.From)
		return update, err
	}
	plan, err := planCarry(ctx, checkout, update.From, update.To, settings)
	if err != nil {
		return update, err
	}
	if len(plan.blocked) > 0 {
		return update, fmt.Errorf("%w to %s in %s, where %s is checked out; commit or stash them, then update", ErrDirty, listPaths(plan.blocked), checkout, branch)
	}
	if len(plan.clobbered) > 0 {
		return update, fmt.Errorf("%s changes %s, which %s has uncommitted or untracked versions of; move them aside, then update", update.Upstream, listPaths(plan.clobbered), checkout)
	}
	update.CheckedOut = checkout
	if len(plan.settled) > 0 {
		if _, err := git(ctx, checkout, append([]string{"update-index", "--add", "--remove", "--"}, plan.settled...)...); err != nil {
			return update, err
		}
	}
	if _, err := git(ctx, checkout, "merge", "--ff-only", "--quiet", update.To); err != nil {
		if len(plan.unstage) > 0 {
			// Put the index back as the user left it; the files never moved.
			if _, resetErr := git(context.Background(), checkout, append([]string{"--literal-pathspecs", "reset", "-q", "--"}, plan.unstage...)...); resetErr != nil {
				err = errors.Join(err, fmt.Errorf("%s is still staged: %w", listPaths(plan.unstage), resetErr))
			}
		}
		return update, err
	}
	update.Kept, update.Settled = plan.kept, plan.settled
	return update, nil
}

// carry is what fast-forwarding a checkout does to its uncommitted changes.
// Git itself never lets a fast-forward overwrite one; carry names the paths
// first, so the view can say which, and decides which edits may stay.
type carry struct {
	// dirty says the checkout has any uncommitted or untracked change.
	dirty bool
	// blocked are edits to tracked files that are not settings. They keep
	// the checkout from moving, as any change once did: a fast-forward
	// under someone's half-made code change is not Inkuna's to make.
	blocked []string
	// clobbered are paths the fast-forward writes that the checkout has
	// its own versions of.
	clobbered []string
	// settled are settings the fast-forward writes that the checkout
	// already has as the upstream does: an edit shared and merged. Staging
	// them lets git take them as merged rather than refuse them.
	settled []string
	// unstage are those of settled whose index entry staging changes, so a
	// failed merge can put it back.
	unstage []string
	// kept counts the changes the fast-forward leaves untouched.
	kept int
}

// planCarry decides what fast-forwarding checkout from one commit to another
// does to its uncommitted changes. Edits to settings and untracked files
// stay where they are, unless the fast-forward writes them; then a setting
// already as the target has it settles, and anything else is clobbered.
func planCarry(ctx context.Context, checkout, from, to string, settings Settings) (carry, error) {
	entries, err := statusEntries(ctx, checkout)
	if err != nil || len(entries) == 0 {
		return carry{}, err
	}
	plan := carry{dirty: true}
	output, err := git(ctx, checkout, "diff", "--name-only", "-z", "--no-renames", from, to, "--")
	if err != nil {
		return carry{}, err
	}
	incoming := strings.Split(strings.TrimRight(output, "\x00"), "\x00")
	touched := []statusEntry{}
	for _, entry := range entries {
		setting := settings.Holds(entry.path)
		switch {
		case !setting && !entry.untracked():
			plan.blocked = append(plan.blocked, entry.path)
		case !overlapsAny(entry.path, incoming):
			plan.kept++
		case setting && (!entry.staged() || entry.work == ' '):
			// A path staged one way and edited another has a version only
			// the index holds, which staging the file would lose.
			touched = append(touched, entry)
		default:
			plan.clobbered = append(plan.clobbered, entry.path)
		}
	}
	if len(touched) == 0 {
		return plan, nil
	}
	paths := make([]string, 0, len(touched))
	for _, entry := range touched {
		paths = append(paths, entry.path)
	}
	target, err := treeEntries(ctx, checkout, to, paths)
	if err != nil {
		return carry{}, err
	}
	head, err := treeEntries(ctx, checkout, "HEAD", paths)
	if err != nil {
		return carry{}, err
	}
	work, _, err := workingEntries(ctx, checkout, paths, false, head)
	if err != nil {
		return carry{}, err
	}
	for _, entry := range touched {
		if work[entry.path] != target[entry.path] {
			plan.clobbered = append(plan.clobbered, entry.path)
			continue
		}
		plan.settled = append(plan.settled, entry.path)
		if !entry.staged() {
			plan.unstage = append(plan.unstage, entry.path)
		}
	}
	sort.Strings(plan.settled)
	return plan, nil
}

// CheckoutOf returns the path of the main checkout or linked worktree that
// has branch checked out, or "" when none has.
func CheckoutOf(ctx context.Context, repository, branch string) (string, error) {
	checkouts, err := checkedOutBranches(ctx, repository)
	if err != nil {
		return "", err
	}
	return checkouts.byBranch[branch], nil
}

// DefaultBranch is origin's default branch as the last clone or fetch
// recorded it (origin/HEAD), or "" when it is not known.
func DefaultBranch(ctx context.Context, repository string) string {
	head, err := git(ctx, repository, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(head), "origin/")
}

// judgeUpdate decides whether UpdateBranch would move a branch, the same way
// UpdateBranch does, so the view offers the update only where it would work.
func judgeUpdate(ctx context.Context, branch *BranchSync, settings Settings) {
	switch branch.State {
	case SyncBehind:
		if branch.CheckedOut == "" {
			branch.Updatable = true
			return
		}
		plan, err := planCarry(ctx, branch.CheckedOut, "refs/heads/"+branch.Name, branch.upstreamRef, settings)
		branch.Dirty = plan.dirty
		switch {
		case err != nil:
			branch.Reason = "Could not read the checkout at " + branch.CheckedOut + ": " + err.Error()
		case len(plan.blocked) > 0:
			branch.Reason = "Checked out in " + branch.CheckedOut + " with uncommitted changes to " + listPaths(plan.blocked) + "; commit or stash them, then update."
		case len(plan.clobbered) > 0:
			branch.Reason = branch.Upstream + " changes " + listPaths(plan.clobbered) + ", which " + branch.CheckedOut + " has uncommitted or untracked versions of; move them aside, then update."
		default:
			branch.Updatable = true
		}
	case SyncDiverged:
		branch.Reason = fmt.Sprintf("Has %d commit(s) %s does not; Inkuna only fast-forwards, so merge or rebase it yourself.", branch.Ahead, branch.Upstream)
	}
}

// errBaseUnknown is why no branch can be deleted while the base is not
// known: the base itself would not be told from the rest, so main could go.
var errBaseUnknown = errors.New("the base branch is not known, so it could not be told from the rest; set branch.base in .inkuna/branching.yaml, or run git remote set-head origin --auto, to delete branches")

// baseUnknown is errBaseUnknown as the view's reason.
var baseUnknown = "T" + errBaseUnknown.Error()[1:] + "."

// judgeDelete decides, the way DeleteBranch does, whether a branch without
// an upstream can be deleted, and counts what deleting it would drop.
func judgeDelete(ctx context.Context, repository, base string, branch *BranchSync) error {
	if branch.State != SyncLocal && branch.State != SyncGone {
		return nil
	}
	unique, err := uniqueCommits(ctx, repository, branch.Name)
	if err != nil {
		return err
	}
	branch.Unique = unique
	switch {
	case base == "":
		branch.DeleteReason = baseUnknown
	case branch.IsBase:
		branch.DeleteReason = "It is the base branch."
	case branch.Main:
		branch.DeleteReason = "Checked out in the project checkout; switch it to another branch first."
	case branch.CheckedOut != "":
		changes, err := Status(ctx, branch.CheckedOut)
		switch {
		case err != nil:
			branch.DeleteReason = "Could not read the worktree at " + branch.CheckedOut + ": " + err.Error()
		case strings.TrimSpace(changes) != "":
			branch.Dirty = true
			branch.DeleteReason = "Checked out in " + branch.CheckedOut + " with uncommitted changes; commit or discard them first."
		default:
			branch.Deletable = true
		}
	default:
		branch.Deletable = true
	}
	return nil
}

// Deletions judges the named local branches as DeleteBranch would, with
// the commits only each has, without measuring the rest. A name that is not
// a local branch is left out of the result.
func Deletions(ctx context.Context, repository, base string, names []string) (map[string]BranchSync, error) {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	branches, err := localBranches(ctx, repository, base)
	if err != nil {
		return nil, err
	}
	judged := make(map[string]BranchSync, len(names))
	for _, branch := range branches {
		if !wanted[branch.Name] {
			continue
		}
		if err := judgeDelete(ctx, repository, base, &branch); err != nil {
			return nil, err
		}
		judged[branch.Name] = branch
	}
	return judged, nil
}

// BranchDeletion is what DeleteBranch removed: enough to restore the branch
// with `git branch <branch> <commit>` until git collects its commits.
type BranchDeletion struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Unique int    `json:"unique"`
	// Worktree is the linked worktree removed along with the branch.
	Worktree string `json:"worktree,omitempty"`
}

// DeleteBranch deletes a local branch that tracks nothing, or whose upstream
// is gone. It refuses the base, a branch checked out in the main checkout,
// and a branch that no longer points at commit, the tip the caller
// confirmed. A branch checked out in a linked worktree goes with it:
// removeWorktree gets that path only after every other check has passed,
// and must refuse a worktree with changes.
func DeleteBranch(ctx context.Context, repository, base, branch, commit string, removeWorktree func(path string) error) (BranchDeletion, error) {
	if strings.TrimSpace(branch) == "" || strings.HasPrefix(branch, "-") {
		return BranchDeletion{}, fmt.Errorf("invalid branch name %q", branch)
	}
	ref := "refs/heads/" + branch
	upstream, err := upstreamOf(ctx, repository, ref)
	if err != nil {
		return BranchDeletion{}, err
	}
	if upstream != "" && refExists(ctx, repository, upstream) {
		return BranchDeletion{}, fmt.Errorf("%s tracks %s, which still exists; Inkuna deletes only branches without an upstream", branch, shortRef(upstream))
	}
	if base == "" {
		return BranchDeletion{}, errBaseUnknown
	}
	if branch == base {
		return BranchDeletion{}, fmt.Errorf("%s is the base branch", branch)
	}
	tip, err := git(ctx, repository, "rev-parse", "--verify", ref)
	if err != nil {
		return BranchDeletion{}, err
	}
	if len(commit) < 7 || !strings.HasPrefix(tip, commit) {
		return BranchDeletion{}, fmt.Errorf("%s now points at %s, not %s; look at it again before deleting it", branch, tip[:7], commit)
	}
	deletion := BranchDeletion{Branch: branch, Commit: tip}
	if deletion.Unique, err = uniqueCommits(ctx, repository, branch); err != nil {
		return BranchDeletion{}, err
	}
	checkouts, err := checkedOutBranches(ctx, repository)
	if err != nil {
		return BranchDeletion{}, err
	}
	if checkout, ok := checkouts.byBranch[branch]; ok {
		if checkout == checkouts.main {
			return BranchDeletion{}, fmt.Errorf("%s is checked out in the project checkout; switch it to another branch first", branch)
		}
		if removeWorktree == nil {
			return BranchDeletion{}, fmt.Errorf("%s is checked out in %s", branch, checkout)
		}
		if err := removeWorktree(checkout); err != nil {
			return BranchDeletion{}, fmt.Errorf("remove the worktree at %s: %w", checkout, err)
		}
		deletion.Worktree = checkout
	}
	if _, err := git(ctx, repository, "branch", "-D", "--", branch); err != nil {
		return deletion, err
	}
	return deletion, nil
}

// uniqueCommits counts the commits only this branch reaches: no other local
// branch, tag, or remote-tracking ref has them, so deleting it drops them.
func uniqueCommits(ctx context.Context, repository, branch string) (int, error) {
	output, err := git(ctx, repository, "rev-list", "--count", "refs/heads/"+branch, "--not", "--exclude="+branch, "--branches", "--tags", "--remotes", "--")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(output))
}

func syncState(upstream string, gone bool, ahead, behind int) string {
	switch {
	case upstream == "":
		return SyncLocal
	case gone:
		return SyncGone
	case ahead > 0 && behind > 0:
		return SyncDiverged
	case behind > 0:
		return SyncBehind
	case ahead > 0:
		return SyncAhead
	default:
		return SyncSynced
	}
}

// parseTrack reads %(upstream:track,nobracket): "ahead 1, behind 2", "ahead
// 1", "behind 2", "gone", or empty when the branch is at its upstream.
func parseTrack(track string) (ahead, behind int, gone bool) {
	if strings.TrimSpace(track) == "gone" {
		return 0, 0, true
	}
	for _, part := range strings.Split(track, ",") {
		word, count, ok := strings.Cut(strings.TrimSpace(part), " ")
		if !ok {
			continue
		}
		value, err := strconv.Atoi(count)
		if err != nil {
			continue
		}
		switch word {
		case "ahead":
			ahead = value
		case "behind":
			behind = value
		}
	}
	return ahead, behind, false
}

// shortRef drops refs/remotes/ or refs/heads/ for display.
func shortRef(ref string) string {
	if short, ok := strings.CutPrefix(ref, "refs/remotes/"); ok {
		return short
	}
	return strings.TrimPrefix(ref, "refs/heads/")
}

// countCommits counts commits reachable from to but not from from.
func countCommits(ctx context.Context, repository, from, to string) (int, error) {
	output, err := git(ctx, repository, "rev-list", "--count", from+".."+to, "--")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(output))
}

// upstreamOf returns the full ref a local branch tracks, or "" when it
// tracks none. It matches the ref exactly, because for-each-ref patterns
// also match the refs below a name.
func upstreamOf(ctx context.Context, repository, ref string) (string, error) {
	output, err := git(ctx, repository, "for-each-ref", "--format=%(refname)%1f%(upstream)", ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(output, "\n") {
		if name, upstream, ok := strings.Cut(line, "\x1f"); ok && name == ref {
			return upstream, nil
		}
	}
	return "", fmt.Errorf("no local branch %s", strings.TrimPrefix(ref, "refs/heads/"))
}

func isAncestor(ctx context.Context, repository, ancestor, descendant string) (bool, error) {
	_, err := git(ctx, repository, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

type checkouts struct {
	main     string
	byBranch map[string]string
}

// checkedOutBranches maps each branch checked out in the main checkout or a
// linked worktree to that checkout's path.
func checkedOutBranches(ctx context.Context, repository string) (checkouts, error) {
	output, err := git(ctx, repository, "worktree", "list", "--porcelain")
	if err != nil {
		return checkouts{}, err
	}
	result := checkouts{byBranch: map[string]string{}}
	path := ""
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(line, "worktree "); ok {
			path = filepath.Clean(value)
			if result.main == "" {
				result.main = path // git lists the main checkout first
			}
		} else if value, ok := strings.CutPrefix(line, "branch "); ok && path != "" {
			result.byBranch[strings.TrimPrefix(value, "refs/heads/")] = path
		}
	}
	return result, nil
}

// lastFetch is when FETCH_HEAD was last written, which every fetch does. A
// failed fetch leaves it empty, so an empty one is no fetch at all rather
// than a fresh one.
func lastFetch(ctx context.Context, repository string) (time.Time, bool) {
	path, err := git(ctx, repository, "rev-parse", "--path-format=absolute", "--git-path", "FETCH_HEAD")
	if err != nil {
		return time.Time{}, false
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// signInFailed recognizes git's and the hosts' wording for a remote that
// wants credentials git could not supply.
func signInFailed(message string) bool {
	message = strings.ToLower(message)
	for _, phrase := range []string{
		"authentication failed",
		"could not read username",
		"could not read password",
		"terminal prompts disabled",
		"permission denied (publickey",
		"invalid username or password",
		"repository not found",
	} {
		if strings.Contains(message, phrase) {
			return true
		}
	}
	return false
}
