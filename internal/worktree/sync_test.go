package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

// syncFixture is a clone whose branches, once it fetches, stand in every
// sync state against origin. A teammate's clone (seed) moves origin.
type syncFixture struct {
	clone, seed, dirty string
}

func newSyncFixture(t *testing.T) syncFixture {
	t.Helper()
	parent := t.TempDir()
	origin := filepath.Join(parent, "origin.git")
	fixture := syncFixture{seed: filepath.Join(parent, "seed"), clone: filepath.Join(parent, "bakery")}
	runGit(t, parent, "init", "-q", "--bare", "-b", "main", origin)
	runGit(t, parent, "clone", "-q", origin, fixture.seed)
	configure(t, fixture.seed)
	commitFile(t, fixture.seed, "README.md", "one")
	runGit(t, fixture.seed, "push", "-q", "origin", "HEAD:main")
	runGit(t, parent, "clone", "-q", origin, fixture.clone)
	configure(t, fixture.clone)

	clone := fixture.clone
	for _, name := range []string{"feature/synced", "feature/ahead", "feature/diverged", "feature/gone", "feature/behind", "feature/dirty"} {
		runGit(t, clone, "branch", "-q", name, "main")
		runGit(t, clone, "push", "-q", "-u", "origin", name)
	}
	// A run's branch: local only, never pushed.
	runGit(t, clone, "branch", "-q", "feature/local", "main")
	runGit(t, clone, "switch", "-q", "feature/ahead")
	commitFile(t, clone, "AHEAD.md", "mine")
	runGit(t, clone, "switch", "-q", "feature/diverged")
	commitFile(t, clone, "MINE.md", "mine")
	runGit(t, clone, "switch", "-q", "main")
	fixture.dirty = filepath.Join(parent, "bakery-worktrees", "dirty")
	runGit(t, clone, "worktree", "add", "-q", fixture.dirty, "feature/dirty")
	// An edit to tracked code holds a checkout's branch where it is.
	if err := os.WriteFile(filepath.Join(fixture.dirty, "README.md"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}

	seed := fixture.seed
	runGit(t, seed, "fetch", "-q", "origin")
	commitFile(t, seed, "MERGED.md", "two")
	runGit(t, seed, "push", "-q", "origin", "HEAD:main")
	for _, name := range []string{"feature/diverged", "feature/behind", "feature/dirty"} {
		runGit(t, seed, "switch", "-q", "-c", name, "origin/"+name)
		commitFile(t, seed, "THEIRS-"+filepath.Base(name)+".md", "theirs")
		runGit(t, seed, "push", "-q", "origin", name)
	}
	runGit(t, seed, "push", "-q", "origin", "--delete", "feature/gone")
	return fixture
}

func byName(t *testing.T, status SyncStatus) map[string]BranchSync {
	t.Helper()
	branches := map[string]BranchSync{}
	for _, branch := range status.Branches {
		branches[branch.Name] = branch
	}
	return branches
}

func TestBranchesReportsEachSyncStateAfterAFetch(t *testing.T) {
	ctx := context.Background()
	fixture := newSyncFixture(t)

	before, err := Branches(ctx, fixture.clone, "main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if !before.HasOrigin || before.BaseRef != "origin/main" {
		t.Fatalf("status = %+v", before)
	}
	if main := byName(t, before)["main"]; main.State != SyncSynced || !main.IsBase || !main.Main || main.CheckedOut == "" {
		t.Fatalf("before a fetch the clone cannot know origin moved: %+v", main)
	}

	if err := FetchOrigin(ctx, fixture.clone); err != nil {
		t.Fatal(err)
	}
	status, err := Branches(ctx, fixture.clone, "main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if status.FetchedAt == "" {
		t.Fatal("a fetch must be reported")
	}
	branches := byName(t, status)
	want := map[string]struct {
		state         string
		ahead, behind int
		updatable     bool
	}{
		"main":             {SyncBehind, 0, 1, true},
		"feature/synced":   {SyncSynced, 0, 0, false},
		"feature/ahead":    {SyncAhead, 1, 0, false},
		"feature/diverged": {SyncDiverged, 1, 1, false},
		"feature/gone":     {SyncGone, 0, 0, false},
		"feature/behind":   {SyncBehind, 0, 1, true},
		"feature/dirty":    {SyncBehind, 0, 1, false},
		"feature/local":    {SyncLocal, 0, 0, false},
	}
	if len(branches) != len(want) {
		t.Fatalf("branches = %+v", status.Branches)
	}
	for name, expected := range want {
		branch := branches[name]
		if branch.State != expected.state || branch.Ahead != expected.ahead || branch.Behind != expected.behind || branch.Updatable != expected.updatable {
			t.Errorf("%s = %+v, want %+v", name, branch, expected)
		}
	}
	if local := branches["feature/local"]; local.BehindBase != 1 || local.Upstream != "" {
		t.Errorf("a run's branch counts how far it is behind origin/main: %+v", local)
	}
	if main := branches["main"]; main.Upstream != "origin/main" || main.BehindBase != 1 {
		t.Errorf("main = %+v", main)
	}
	dirty := branches["feature/dirty"]
	if !dirty.Dirty || dirty.CheckedOut == "" || dirty.Main || !strings.Contains(dirty.Reason, "uncommitted changes") {
		t.Errorf("a dirty checkout keeps its branch from updating: %+v", dirty)
	}
	if diverged := branches["feature/diverged"]; !strings.Contains(diverged.Reason, "only fast-forwards") {
		t.Errorf("diverged = %+v", diverged)
	}
}

func TestUpdateBranchFastForwardsOnly(t *testing.T) {
	ctx := context.Background()
	fixture := newSyncFixture(t)
	if err := FetchOrigin(ctx, fixture.clone); err != nil {
		t.Fatal(err)
	}

	// Checked out in the clean main checkout: the files arrive too.
	update, err := UpdateBranch(ctx, fixture.clone, "main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if update.From == update.To || update.CheckedOut == "" || update.Upstream != "origin/main" {
		t.Fatalf("update = %+v", update)
	}
	if _, err := os.Stat(filepath.Join(fixture.clone, "MERGED.md")); err != nil {
		t.Fatal("the checkout must move with its branch")
	}

	// Not checked out anywhere: only the ref moves.
	update, err = UpdateBranch(ctx, fixture.clone, "feature/behind", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if update.CheckedOut != "" || gitOutput(t, fixture.clone, "rev-parse", "feature/behind") != gitOutput(t, fixture.clone, "rev-parse", "origin/feature/behind") {
		t.Fatalf("feature/behind did not reach its upstream: %+v", update)
	}

	// Already there: nothing to do and no error.
	if again, err := UpdateBranch(ctx, fixture.clone, "feature/behind", DefaultSettings); err != nil || again.From != again.To {
		t.Fatalf("again = %+v, %v", again, err)
	}

	refusals := map[string]string{
		"feature/diverged": "only fast-forwards",
		"feature/ahead":    "only fast-forwards",
		"feature/local":    "tracks no remote branch",
		"feature/gone":     "is gone",
		"feature/missing":  "no local branch",
		"--force":          "invalid branch name",
	}
	for name, reason := range refusals {
		before, _ := exec.Command("git", "-C", fixture.clone, "rev-parse", "--verify", "-q", "refs/heads/"+name).Output()
		if _, err := UpdateBranch(ctx, fixture.clone, name, DefaultSettings); err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("UpdateBranch(%s) = %v, want %q", name, err, reason)
		}
		after, _ := exec.Command("git", "-C", fixture.clone, "rev-parse", "--verify", "-q", "refs/heads/"+name).Output()
		if string(before) != string(after) {
			t.Errorf("a refused update moved %s", name)
		}
	}

	head := gitOutput(t, fixture.dirty, "rev-parse", "HEAD")
	if _, err := UpdateBranch(ctx, fixture.clone, "feature/dirty", DefaultSettings); !errors.Is(err, ErrDirty) {
		t.Fatalf("a dirty checkout must refuse: %v", err)
	}
	if gitOutput(t, fixture.dirty, "rev-parse", "HEAD") != head {
		t.Fatal("a refused update moved the dirty checkout")
	}
	runGit(t, fixture.dirty, "checkout", "--", "README.md")
	if update, err := UpdateBranch(ctx, fixture.clone, "feature/dirty", DefaultSettings); err != nil || update.CheckedOut == "" {
		t.Fatalf("a clean linked worktree moves with its branch: %+v, %v", update, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.dirty, "THEIRS-dirty.md")); err != nil {
		t.Fatal("the linked worktree must have the upstream's files")
	}
}

func TestBranchesWithoutOriginOrBase(t *testing.T) {
	ctx := context.Background()
	repository := t.TempDir()
	runGit(t, repository, "init", "-q", "-b", "trunk")
	configure(t, repository)
	commitFile(t, repository, "README.md", "one")
	if err := FetchOrigin(ctx, repository); err == nil {
		t.Fatal("fetching without origin must fail")
	}
	status, err := Branches(ctx, repository, "", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if status.HasOrigin || status.BaseRef != "" || status.FetchedAt != "" || len(status.Branches) != 1 {
		t.Fatalf("status = %+v", status)
	}
	if trunk := status.Branches[0]; trunk.Name != "trunk" || trunk.State != SyncLocal || trunk.Updatable {
		t.Fatalf("trunk = %+v", trunk)
	}
}

// Without origin or branch.base, the base is not known, so no branch is
// the base: main, not checked out and without an upstream, would be
// deletable. Deleting is refused until the base is known.
func TestNoBranchIsDeletableWhileTheBaseIsNotKnown(t *testing.T) {
	ctx := context.Background()
	repository := t.TempDir()
	runGit(t, repository, "init", "-q", "-b", "main")
	configure(t, repository)
	commitFile(t, repository, "README.md", "one")
	runGit(t, repository, "switch", "-q", "-c", "feature")
	status, err := Branches(ctx, repository, "", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	main := byName(t, status)["main"]
	if main.Deletable || main.DeleteReason != baseUnknown {
		t.Fatalf("main must not be deletable while the base is not known: %+v", main)
	}
	judged, err := Deletions(ctx, repository, "", []string{"main"})
	if err != nil || judged["main"].Deletable || judged["main"].DeleteReason != baseUnknown {
		t.Fatalf("Deletions judges it the same: %+v %v", judged, err)
	}
	if _, err := DeleteBranch(ctx, repository, "", "main", main.Commit, nil); !errors.Is(err, errBaseUnknown) {
		t.Fatalf("DeleteBranch refuses it: %v", err)
	}
	if gitOutput(t, repository, "rev-parse", "--verify", "refs/heads/main") == "" {
		t.Fatal("main is still there")
	}
	// With the base known, a branch beside it can go.
	runGit(t, repository, "switch", "-q", "main")
	status, err = Branches(ctx, repository, "main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if feature := byName(t, status)["feature"]; !feature.Deletable {
		t.Fatalf("feature is deletable once the base is known: %+v", feature)
	}
}

func TestParseTrackAndSignInFailures(t *testing.T) {
	for track, want := range map[string][3]int{
		"":                   {0, 0, 0},
		"ahead 2":            {2, 0, 0},
		"behind 3":           {0, 3, 0},
		"ahead 1, behind 12": {1, 12, 0},
		"gone":               {0, 0, 1},
	} {
		ahead, behind, gone := parseTrack(track)
		if ahead != want[0] || behind != want[1] || gone != (want[2] == 1) {
			t.Errorf("parseTrack(%q) = %d, %d, %v", track, ahead, behind, gone)
		}
	}
	for message, want := range map[string]bool{
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled": true,
		"git@github.com: Permission denied (publickey).":                                     true,
		"remote: Repository not found.":                                                      true,
		"fatal: unable to access 'https://github.com/x/y.git/': Could not resolve host":      false,
	} {
		if signInFailed(message) != want {
			t.Errorf("signInFailed(%q) != %v", message, want)
		}
	}
}

func TestDeleteBranchOnlyWithoutAnUpstream(t *testing.T) {
	ctx := context.Background()
	fixture := newSyncFixture(t)
	if err := FetchOrigin(ctx, fixture.clone); err != nil {
		t.Fatal(err)
	}
	clone := fixture.clone
	// A run's branch with work no other ref has.
	runGit(t, clone, "switch", "-q", "-c", "feature/run")
	commitFile(t, clone, "RUN-1.md", "one")
	commitFile(t, clone, "RUN-2.md", "two")
	runGit(t, clone, "switch", "-q", "main")
	// Run branches still checked out in their worktrees, one clean, one not.
	tree := filepath.Join(filepath.Dir(clone), "bakery-worktrees", "in-tree")
	runGit(t, clone, "worktree", "add", "-q", "-b", "feature/in-tree", tree, "main")
	commitFile(t, tree, "TREE.md", "tree")
	untidy := filepath.Join(filepath.Dir(clone), "bakery-worktrees", "in-dirty")
	runGit(t, clone, "worktree", "add", "-q", "-b", "feature/in-dirty", untidy, "main")
	if err := os.WriteFile(filepath.Join(untidy, "SCRATCH.md"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, err := Branches(ctx, clone, "main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	branches := byName(t, status)
	for name, want := range map[string]struct {
		deletable bool
		unique    int
	}{
		"feature/run":      {true, 2},
		"feature/gone":     {true, 0},
		"feature/local":    {true, 0},
		"feature/in-tree":  {true, 1},
		"feature/in-dirty": {false, 0},
		"main":             {false, 0},
		"feature/synced":   {false, 0},
	} {
		if branch := branches[name]; branch.Deletable != want.deletable || branch.Unique != want.unique {
			t.Errorf("%s = %+v, want deletable %v, unique %d", name, branch, want.deletable, want.unique)
		}
	}
	if dirty := branches["feature/in-dirty"]; !dirty.Dirty || !strings.Contains(dirty.DeleteReason, "uncommitted changes") {
		t.Errorf("a dirty worktree keeps its branch: %+v", dirty)
	}
	if main := branches["main"]; main.DeleteReason != "" {
		t.Errorf("a branch with an upstream is not offered for deletion at all: %+v", main)
	}
	asBase, err := Branches(ctx, clone, "feature/local", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if local := byName(t, asBase)["feature/local"]; local.Deletable || !strings.Contains(local.DeleteReason, "base branch") {
		t.Errorf("the base is never deletable: %+v", local)
	}

	remove := func(path string) error { return Remove(ctx, clone, path) }
	tip := gitOutput(t, clone, "rev-parse", "feature/run")
	refusals := map[string][2]string{
		"feature/synced": {gitOutput(t, clone, "rev-parse", "--short", "feature/synced"), "which still exists"},
		"feature/run":    {"0000000", "now points at"},
		"feature/local":  {gitOutput(t, clone, "rev-parse", "--short", "feature/local"), "is the base branch"},
		"feature/nope":   {"0000000", "no local branch"},
		"--force":        {"0000000", "invalid branch name"},
	}
	for name, refusal := range refusals {
		base := "main"
		if name == "feature/local" {
			base = "feature/local"
		}
		if _, err := DeleteBranch(ctx, clone, base, name, refusal[0], remove); err == nil || !strings.Contains(err.Error(), refusal[1]) {
			t.Errorf("DeleteBranch(%s) = %v, want %q", name, err, refusal[1])
		}
	}
	if _, err := DeleteBranch(ctx, clone, "main", "feature/in-dirty", gitOutput(t, untidy, "rev-parse", "HEAD"), remove); !errors.Is(err, ErrDirty) {
		t.Fatalf("a dirty worktree refuses the delete: %v", err)
	}
	if _, err := os.Stat(untidy); err != nil || !BranchTaken(ctx, clone, "feature/in-dirty") {
		t.Fatal("a refused delete must leave the worktree and the branch")
	}
	for name := range refusals {
		if name != "feature/nope" && name != "--force" && !BranchTaken(ctx, clone, name) {
			t.Errorf("a refused delete removed %s", name)
		}
	}

	deleted, err := DeleteBranch(ctx, clone, "main", "feature/run", tip[:7], remove)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Commit != tip || deleted.Unique != 2 || deleted.Worktree != "" || refExists(ctx, clone, "refs/heads/feature/run") {
		t.Fatalf("deleted = %+v", deleted)
	}
	// What the view tells the user to run brings it back.
	runGit(t, clone, "branch", "feature/run", deleted.Commit)

	if _, err := DeleteBranch(ctx, clone, "main", "feature/gone", gitOutput(t, clone, "rev-parse", "feature/gone"), remove); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command("git", "-C", clone, "config", "--get", "branch.feature/gone.remote").Output(); len(out) != 0 {
		t.Fatal("deleting a gone branch drops its tracking configuration")
	}

	deleted, err = DeleteBranch(ctx, clone, "main", "feature/in-tree", gitOutput(t, tree, "rev-parse", "HEAD"), remove)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(tree); deleted.Worktree == "" || !errors.Is(statErr, os.ErrNotExist) || refExists(ctx, clone, "refs/heads/feature/in-tree") {
		t.Fatalf("a branch in a clean worktree goes with the worktree: %+v, %v", deleted, statErr)
	}

	runGit(t, clone, "switch", "-q", "feature/local")
	status, err = Branches(ctx, clone, "main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if local := byName(t, status)["feature/local"]; local.Deletable || !strings.Contains(local.DeleteReason, "project checkout") {
		t.Errorf("the checked-out branch is never deletable: %+v", local)
	}
	if _, err := DeleteBranch(ctx, clone, "main", "feature/local", gitOutput(t, clone, "rev-parse", "HEAD"), remove); err == nil || !strings.Contains(err.Error(), "project checkout") {
		t.Fatalf("DeleteBranch of the checked-out branch = %v", err)
	}
}

func TestSummarizeCountsWhatIsOutOfSync(t *testing.T) {
	ctx := context.Background()
	fixture := newSyncFixture(t)
	before, err := Summarize(ctx, fixture.clone, "main")
	if err != nil {
		t.Fatal(err)
	}
	if before.OutOfSync != 0 || before.Checkout == nil || before.Checkout.State != SyncSynced {
		t.Fatalf("before a fetch nothing looks out of sync: %+v", before)
	}
	if err := FetchOrigin(ctx, fixture.clone); err != nil {
		t.Fatal(err)
	}
	summary, err := Summarize(ctx, fixture.clone, "main")
	if err != nil {
		t.Fatal(err)
	}
	// main, feature/behind, and feature/dirty are behind; feature/diverged
	// and feature/gone count too; feature/local tracks nothing.
	if !summary.HasOrigin || summary.FetchedAt == "" || summary.Branches != 8 || summary.OutOfSync != 5 || summary.NoUpstream != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if checkout := summary.Checkout; checkout.Name != "main" || checkout.State != SyncBehind || checkout.Behind != 1 || checkout.Upstream != "origin/main" {
		t.Fatalf("checkout = %+v", checkout)
	}
	runGit(t, fixture.clone, "switch", "-q", "--detach")
	if detached, err := Summarize(ctx, fixture.clone, "main"); err != nil || detached.Checkout != nil {
		t.Fatalf("a detached HEAD has no checked-out branch: %+v, %v", detached, err)
	}
}

func TestDeletionsJudgesOnlyTheNamedBranches(t *testing.T) {
	ctx := context.Background()
	fixture := newSyncFixture(t)
	clone := fixture.clone
	runGit(t, clone, "switch", "-q", "-c", "feature/run")
	commitFile(t, clone, "RUN.md", "one")
	runGit(t, clone, "switch", "-q", "main")

	judged, err := Deletions(ctx, clone, "main", []string{"feature/run", "feature/synced", "main", "feature/nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(judged) != 3 {
		t.Fatalf("judged %v; a missing branch is left out and an unnamed one is not measured", judged)
	}
	if run := judged["feature/run"]; !run.Deletable || run.Unique != 1 || run.Commit != gitOutput(t, clone, "rev-parse", "--short", "feature/run") {
		t.Errorf("a run's branch with one commit of its own = %+v", run)
	}
	if synced := judged["feature/synced"]; synced.Deletable || synced.Upstream != "origin/feature/synced" {
		t.Errorf("a branch with an upstream is not deletable = %+v", synced)
	}
	if main := judged["main"]; main.Deletable {
		t.Errorf("the base is not deletable = %+v", main)
	}
}
