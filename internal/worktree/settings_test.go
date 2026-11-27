package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// settingsFixture is a clone whose main checkout keeps Inkuna settings, and
// a teammate's clone (seed) that moves origin.
type settingsFixture struct {
	clone, seed string
}

func newSettingsFixture(t *testing.T) settingsFixture {
	t.Helper()
	parent := t.TempDir()
	origin := filepath.Join(parent, "origin.git")
	fixture := settingsFixture{seed: filepath.Join(parent, "seed"), clone: filepath.Join(parent, "bakery")}
	runGit(t, parent, "init", "-q", "--bare", "-b", "main", origin)
	runGit(t, parent, "clone", "-q", origin, fixture.seed)
	configure(t, fixture.seed)
	writeFile(t, fixture.seed, ".inkuna/rules.yaml", "version: 1\n")
	writeFile(t, fixture.seed, ".inkuna/review.yaml", "version: 1\n")
	commitFile(t, fixture.seed, "main.go", "package main\n")
	runGit(t, fixture.seed, "add", "-A")
	runGit(t, fixture.seed, "commit", "-qm", "settings")
	runGit(t, fixture.seed, "push", "-q", "origin", "HEAD:main")
	runGit(t, parent, "clone", "-q", origin, fixture.clone)
	configure(t, fixture.clone)
	return fixture
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// push commits the seed's working files and pushes them to origin's main.
func (fixture settingsFixture) push(t *testing.T, files map[string]string) {
	t.Helper()
	for name, content := range files {
		writeFile(t, fixture.seed, name, content)
	}
	runGit(t, fixture.seed, "add", "-A")
	runGit(t, fixture.seed, "commit", "-qm", "teammate")
	runGit(t, fixture.seed, "push", "-q", "origin", "HEAD:main")
}

func mainOf(t *testing.T, repository string) BranchSync {
	t.Helper()
	status, err := Branches(context.Background(), repository, "main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	return byName(t, status)["main"]
}

func TestSettingsHoldsInkunaFilesButNotOut(t *testing.T) {
	settings := SettingsAt("/src/bakery", "/src/bakery/app", "/src/bakery/config/rules.yaml", "/elsewhere/rules.yaml", "/src/bakery/app/.inkuna/rules.yaml")
	want := Settings{Dir: "app/.inkuna", Files: []string{"config/rules.yaml"}}
	if !reflect.DeepEqual(settings, want) {
		t.Fatalf("SettingsAt = %+v, want %+v", settings, want)
	}
	for file, held := range map[string]bool{
		"app/.inkuna/rules.yaml":         true,
		"app/.inkuna/agents/reviewer.md": true,
		"app/.inkuna/out/plan.md":        false,
		"app/.inkuna/out":                false,
		"app/.inkuna":                    false,
		".inkuna/rules.yaml":             false,
		"config/rules.yaml":              true,
		"main.go":                        false,
	} {
		if settings.Holds(file) != held {
			t.Errorf("Holds(%s) = %v", file, !held)
		}
	}
	if (Settings{}).Holds(".inkuna/rules.yaml") {
		t.Error("the zero Settings holds nothing")
	}
}

func TestUpdateCarriesSettingsEditsAndUntrackedFiles(t *testing.T) {
	ctx := context.Background()
	fixture := newSettingsFixture(t)
	fixture.push(t, map[string]string{"main.go": "package main // theirs\n"})
	if err := FetchOrigin(ctx, fixture.clone); err != nil {
		t.Fatal(err)
	}
	writeFile(t, fixture.clone, ".inkuna/rules.yaml", "version: 1 # mine\n")
	writeFile(t, fixture.clone, ".inkuna/agents/reviewer.md", "mine")
	writeFile(t, fixture.clone, "debug.log", "local only")

	if main := mainOf(t, fixture.clone); !main.Updatable || !main.Dirty || main.Reason != "" {
		t.Fatalf("settings edits and untracked files leave main updatable: %+v", main)
	}
	update, err := UpdateBranch(ctx, fixture.clone, "main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if update.Kept != 3 || len(update.Settled) != 0 || update.CheckedOut == "" {
		t.Fatalf("update = %+v", update)
	}
	if readFile(t, fixture.clone, "main.go") != "package main // theirs\n" || readFile(t, fixture.clone, ".inkuna/rules.yaml") != "version: 1 # mine\n" {
		t.Fatal("the checkout must move and keep the settings edit")
	}
	if readFile(t, fixture.clone, "debug.log") != "local only" || readFile(t, fixture.clone, ".inkuna/agents/reviewer.md") != "mine" {
		t.Fatal("untracked files must stay")
	}

	// An edit to tracked code still holds the branch.
	fixture.push(t, map[string]string{"README.md": "theirs"})
	if err := FetchOrigin(ctx, fixture.clone); err != nil {
		t.Fatal(err)
	}
	writeFile(t, fixture.clone, "main.go", "package main // half done\n")
	main := mainOf(t, fixture.clone)
	if main.Updatable || !strings.Contains(main.Reason, "uncommitted changes to main.go") {
		t.Fatalf("a code edit keeps main from updating: %+v", main)
	}
	head := gitOutput(t, fixture.clone, "rev-parse", "HEAD")
	if _, err := UpdateBranch(ctx, fixture.clone, "main", DefaultSettings); !errors.Is(err, ErrDirty) || !strings.Contains(err.Error(), "main.go") {
		t.Fatalf("UpdateBranch with a code edit = %v", err)
	}
	if gitOutput(t, fixture.clone, "rev-parse", "HEAD") != head {
		t.Fatal("a refused update moved main")
	}
}

func TestUpdateRefusesToOverwriteAndSettlesSharedEdits(t *testing.T) {
	ctx := context.Background()
	fixture := newSettingsFixture(t)
	fixture.push(t, map[string]string{
		".inkuna/rules.yaml":         "version: 1 # theirs\n",
		".inkuna/agents/reviewer.md": "theirs",
		"notes.log":                  "theirs",
	})
	runGit(t, fixture.seed, "rm", "-q", ".inkuna/review.yaml")
	runGit(t, fixture.seed, "commit", "-qm", "drop review")
	runGit(t, fixture.seed, "push", "-q", "origin", "HEAD:main")
	if err := FetchOrigin(ctx, fixture.clone); err != nil {
		t.Fatal(err)
	}
	writeFile(t, fixture.clone, ".inkuna/rules.yaml", "version: 1 # mine\n")
	writeFile(t, fixture.clone, ".inkuna/agents/reviewer.md", "theirs")
	writeFile(t, fixture.clone, "notes.log", "mine")
	if err := os.Remove(filepath.Join(fixture.clone, ".inkuna/review.yaml")); err != nil {
		t.Fatal(err)
	}

	main := mainOf(t, fixture.clone)
	if main.Updatable || !strings.Contains(main.Reason, ".inkuna/rules.yaml") || !strings.Contains(main.Reason, "notes.log") || strings.Contains(main.Reason, "reviewer.md") {
		t.Fatalf("edits the upstream would overwrite are named: %+v", main)
	}
	index := gitOutput(t, fixture.clone, "ls-files", "--stage")
	head := gitOutput(t, fixture.clone, "rev-parse", "HEAD")
	if _, err := UpdateBranch(ctx, fixture.clone, "main", DefaultSettings); err == nil || !strings.Contains(err.Error(), "move them aside") {
		t.Fatalf("UpdateBranch over differing edits = %v", err)
	}
	if gitOutput(t, fixture.clone, "rev-parse", "HEAD") != head || gitOutput(t, fixture.clone, "ls-files", "--stage") != index {
		t.Fatal("a refused update changed the checkout")
	}

	// Once the edits match what origin has, the update takes them as merged.
	writeFile(t, fixture.clone, ".inkuna/rules.yaml", "version: 1 # theirs\n")
	if err := os.Remove(filepath.Join(fixture.clone, "notes.log")); err != nil {
		t.Fatal(err)
	}
	if main := mainOf(t, fixture.clone); !main.Updatable {
		t.Fatalf("edits origin already has do not hold main: %+v", main)
	}
	update, err := UpdateBranch(ctx, fixture.clone, "main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".inkuna/agents/reviewer.md", ".inkuna/review.yaml", ".inkuna/rules.yaml"}
	if !reflect.DeepEqual(update.Settled, want) || update.Kept != 0 {
		t.Fatalf("update = %+v, want settled %v", update, want)
	}
	if status := gitOutput(t, fixture.clone, "status", "--porcelain"); status != "" {
		t.Fatalf("the checkout is clean after settling: %q", status)
	}
}

func TestReadSettingsAndShareThemWithoutTouchingTheCheckout(t *testing.T) {
	ctx := context.Background()
	fixture := newSettingsFixture(t)
	clone := fixture.clone
	writeFile(t, clone, ".inkuna/rules.yaml", "version: 1 # mine\n")
	writeFile(t, clone, ".inkuna/agents/reviewer.md", "mine")
	writeFile(t, clone, ".inkuna/out/plan.md", "scratch")
	writeFile(t, clone, "debug.log", "local only")
	if err := os.Remove(filepath.Join(clone, ".inkuna/review.yaml")); err != nil {
		t.Fatal(err)
	}

	status, err := ReadSettings(ctx, clone, "origin/main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, change := range status.Changes {
		kinds[change.Path] = change.Kind
		if change.OnBase || change.Conflict {
			t.Errorf("%s is neither on origin nor in conflict: %+v", change.Path, change)
		}
	}
	wantKinds := map[string]string{".inkuna/agents/reviewer.md": ChangeAdded, ".inkuna/review.yaml": ChangeDeleted, ".inkuna/rules.yaml": ChangeModified}
	if !reflect.DeepEqual(kinds, wantKinds) || !status.Shareable || status.Digest == "" || status.SharedOn != "" {
		t.Fatalf("status = %+v", status)
	}
	if again, _ := ReadSettings(ctx, clone, "origin/main", DefaultSettings); again.Digest != status.Digest {
		t.Fatal("the digest depends only on the edits")
	}

	index := gitOutput(t, clone, "ls-files", "--stage")
	head := gitOutput(t, clone, "rev-parse", "HEAD")
	before := gitOutput(t, clone, "status", "--porcelain")
	commit, err := CommitSettings(ctx, clone, status, "Update Inkuna settings", "Three files.")
	if err != nil {
		t.Fatal(err)
	}
	if gitOutput(t, clone, "rev-parse", "HEAD") != head || gitOutput(t, clone, "ls-files", "--stage") != index || gitOutput(t, clone, "status", "--porcelain") != before {
		t.Fatal("committing settings must not touch the checkout")
	}
	if parent := gitOutput(t, clone, "rev-parse", commit+"^"); parent != gitOutput(t, clone, "rev-parse", "origin/main") {
		t.Fatalf("the share commits on origin/main, not %s", parent)
	}
	files := gitOutput(t, clone, "show", "--name-status", "--format=", commit)
	if files != "A\t.inkuna/agents/reviewer.md\nD\t.inkuna/review.yaml\nM\t.inkuna/rules.yaml" {
		t.Fatalf("the share commits exactly the settings edits:\n%s", files)
	}
	if trailer := gitOutput(t, clone, "log", "-1", "--format=%(trailers:key=Inkuna-Settings,valueonly)", commit); trailer != status.Digest {
		t.Fatalf("trailer = %q, want %q", trailer, status.Digest)
	}

	if err := PublishBranch(ctx, clone, "chore/inkuna-settings", commit); err != nil {
		t.Fatal(err)
	}
	if err := PublishBranch(ctx, clone, "chore/inkuna-settings", commit); err == nil {
		t.Fatal("publishing never overwrites a branch")
	}
	if upstream := gitOutput(t, clone, "rev-parse", "--abbrev-ref", "chore/inkuna-settings@{upstream}"); upstream != "origin/chore/inkuna-settings" {
		t.Fatalf("upstream = %s", upstream)
	}
	if shared, _ := ReadSettings(ctx, clone, "origin/main", DefaultSettings); shared.SharedOn != "chore/inkuna-settings" {
		t.Fatalf("the shared branch is found again: %+v", shared)
	}

	// Pushed straight to main, the edits are on the base and the update
	// leaves only what is not a setting.
	if err := PushCommit(ctx, clone, commit, "main"); err != nil {
		t.Fatal(err)
	}
	onBase, err := ReadSettings(ctx, clone, "origin/main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if onBase.Shareable || !strings.Contains(onBase.Reason, "already has these edits") || len(onBase.Pending()) != 0 || onBase.Digest != "" {
		t.Fatalf("on base = %+v", onBase)
	}
	update, err := UpdateBranch(ctx, clone, "main", DefaultSettings)
	if err != nil || len(update.Settled) != 3 {
		t.Fatalf("update = %+v, %v", update, err)
	}
	if left := gitOutput(t, clone, "status", "--porcelain", "--untracked-files=all"); left != "?? .inkuna/out/plan.md\n?? debug.log" && left != "?? debug.log\n?? .inkuna/out/plan.md" {
		t.Fatalf("left = %q", left)
	}
}

func TestShareRefusesConflictsAndLateEdits(t *testing.T) {
	ctx := context.Background()
	fixture := newSettingsFixture(t)
	writeFile(t, fixture.clone, ".inkuna/rules.yaml", "version: 1 # mine\n")
	status, err := ReadSettings(ctx, fixture.clone, "origin/main", DefaultSettings)
	if err != nil || !status.Shareable {
		t.Fatalf("status = %+v, %v", status, err)
	}
	writeFile(t, fixture.clone, ".inkuna/rules.yaml", "version: 1 # edited after looking\n")
	if _, err := CommitSettings(ctx, fixture.clone, status, "Update", ""); err == nil || !strings.Contains(err.Error(), "changed while it was being shared") {
		t.Fatalf("CommitSettings after a late edit = %v", err)
	}

	fixture.push(t, map[string]string{".inkuna/rules.yaml": "version: 1 # theirs\n"})
	if err := FetchOrigin(ctx, fixture.clone); err != nil {
		t.Fatal(err)
	}
	conflicted, err := ReadSettings(ctx, fixture.clone, "origin/main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	if conflicted.Shareable || len(conflicted.Changes) != 1 || !conflicted.Changes[0].Conflict || !strings.Contains(conflicted.Reason, "would undo that change") {
		t.Fatalf("a file origin changed since the checkout's commit is not shared: %+v", conflicted)
	}

	none, err := ReadSettings(ctx, fixture.clone, "", DefaultSettings)
	if err != nil || none.Shareable || !strings.Contains(none.Reason, "no base branch") || len(none.Changes) != 1 {
		t.Fatalf("without a base = %+v, %v", none, err)
	}
	runGit(t, fixture.clone, "checkout", "--", ".inkuna/rules.yaml")
	clean, err := ReadSettings(ctx, fixture.clone, "origin/main", DefaultSettings)
	if err != nil || len(clean.Changes) != 0 || clean.Shareable {
		t.Fatalf("a clean checkout = %+v, %v", clean, err)
	}
}

func TestPushCommitRefusesWhatIsNotAFastForward(t *testing.T) {
	ctx := context.Background()
	fixture := newSettingsFixture(t)
	writeFile(t, fixture.clone, ".inkuna/rules.yaml", "version: 1 # mine\n")
	status, err := ReadSettings(ctx, fixture.clone, "origin/main", DefaultSettings)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := CommitSettings(ctx, fixture.clone, status, "Update", "")
	if err != nil {
		t.Fatal(err)
	}
	fixture.push(t, map[string]string{"main.go": "package main // moved\n"})
	if err := PushCommit(ctx, fixture.clone, commit, "main"); err == nil {
		t.Fatal("a push over a moved main must be refused")
	}
	if err := PushCommit(ctx, fixture.clone, commit, "--force"); err == nil || !strings.Contains(err.Error(), "invalid branch name") {
		t.Fatalf("PushCommit(--force) = %v", err)
	}
}

func TestShareCommitsOnTheMeasuredBaseAndSwapsAFileForADirectory(t *testing.T) {
	ctx := context.Background()
	fixture := newSettingsFixture(t)
	clone := fixture.clone
	if err := os.Remove(filepath.Join(clone, ".inkuna/review.yaml")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, clone, ".inkuna/review.yaml/notes.md", "now a directory")
	status, err := ReadSettings(ctx, clone, "origin/main", DefaultSettings)
	if err != nil || !status.Shareable {
		t.Fatalf("status = %+v, %v", status, err)
	}
	measured := gitOutput(t, clone, "rev-parse", "origin/main")

	// A fetch after the user looked moves origin/main; the share still
	// commits on what was checked for conflicts, and origin refuses it.
	fixture.push(t, map[string]string{"main.go": "package main // moved\n"})
	if err := FetchOrigin(ctx, clone); err != nil {
		t.Fatal(err)
	}
	commit, err := CommitSettings(ctx, clone, status, "Update", "")
	if err != nil {
		t.Fatal(err)
	}
	if parent := gitOutput(t, clone, "rev-parse", commit+"^"); parent != measured {
		t.Fatalf("parent = %s, want the measured %s", parent, measured)
	}
	if files := gitOutput(t, clone, "show", "--name-status", "--format=", commit); files != "D\t.inkuna/review.yaml\nA\t.inkuna/review.yaml/notes.md" {
		t.Fatalf("files = %q", files)
	}
	if err := PushCommit(ctx, clone, commit, "main"); err == nil {
		t.Fatal("a share on a base that has since moved must be refused")
	}
}

func TestADirectoryReplacedByAFileListsWhatItHeldAsDeleted(t *testing.T) {
	ctx := context.Background()
	fixture := newSettingsFixture(t)
	fixture.push(t, map[string]string{".inkuna/agents/reviewer.md": "theirs"})
	clone := fixture.clone
	runGit(t, clone, "pull", "-q", "--ff-only")
	if err := os.RemoveAll(filepath.Join(clone, ".inkuna/agents")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, clone, ".inkuna/agents", "now a file")
	status, err := ReadSettings(ctx, clone, "origin/main", DefaultSettings)
	if err != nil {
		t.Fatalf("a file under a path that is now a file must read as deleted: %v", err)
	}
	kinds := map[string]string{}
	for _, change := range status.Changes {
		kinds[change.Path] = change.Kind
	}
	if want := map[string]string{".inkuna/agents": ChangeAdded, ".inkuna/agents/reviewer.md": ChangeDeleted}; !reflect.DeepEqual(kinds, want) || !status.Shareable {
		t.Fatalf("status = %+v", status)
	}
}

func TestShareKeepsTheModeWhereGitIgnoresTheExecuteBit(t *testing.T) {
	ctx := context.Background()
	fixture := newSettingsFixture(t)
	writeFile(t, fixture.seed, ".inkuna/skills/release/run.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(fixture.seed, ".inkuna/skills/release/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Windows has no execute bit to set, so the index is told directly; git
	// keeps an index's mode where it ignores the file's.
	runGit(t, fixture.seed, "add", ".inkuna/skills/release/run.sh")
	runGit(t, fixture.seed, "update-index", "--chmod=+x", ".inkuna/skills/release/run.sh")
	fixture.push(t, nil)
	clone := fixture.clone
	runGit(t, clone, "pull", "-q", "--ff-only", "origin", "main")
	runGit(t, clone, "config", "core.fileMode", "false")
	script := filepath.Join(clone, ".inkuna/skills/release/run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := ReadSettings(ctx, clone, "origin/main", DefaultSettings)
	if err != nil || !status.Shareable {
		t.Fatalf("status = %+v, %v", status, err)
	}
	commit, err := CommitSettings(ctx, clone, status, "Update", "")
	if err != nil {
		t.Fatal(err)
	}
	if entry := gitOutput(t, clone, "ls-tree", commit, "--", ".inkuna/skills/release/run.sh"); !strings.HasPrefix(entry, "100755 ") {
		t.Fatalf("the share keeps the script executable: %q", entry)
	}
}
