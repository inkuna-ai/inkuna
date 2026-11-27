package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/install"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// originState is the checkout's origin and working tree as inkunactl update
// sees them, kept on the rig.
type originState struct {
	// latest and latestVersion are the target's commit and its git
	// describe: origin/main's, or the release's when release is set.
	latest, latestVersion string
	// tags are the tags git tag --list --merged origin/main prints, highest
	// first; release is the one the stable channel follows.
	tags    []string
	release string
	// merged are the first-parent titles from the installed commit to the
	// target, newest first.
	merged []string
	// diverged is an installed commit that is not on the target, and newer
	// one built after the release.
	diverged, newer bool
	// changes and local are git status and the commits the target lacks;
	// localMain are main's, for a checkout at a commit.
	changes, local, localMain string
	goMod                     string
}

// ref is how the update names the target to git.
func (state originState) ref() string {
	if state.release != "" {
		return "refs/tags/" + state.release
	}
	return "origin/main"
}

// origin answers the git commands inkunactl update reads the checkout's
// origin and state with. ok is false for any other command.
func (r *rig) origin(line string) (output string, ok bool, err error) {
	state := r.originState
	ref := state.ref()
	switch {
	case line == "git tag --list --merged origin/main --sort=-v:refname v*":
		return strings.Join(state.tags, "\n"), true, nil
	case line == "git rev-parse --verify "+ref+"^{commit}":
		return state.latest, true, nil
	case line == "git describe --tags --always "+ref:
		return state.latestVersion, true, nil
	case strings.HasPrefix(line, "git cat-file -e "):
		return "", true, nil
	case strings.HasPrefix(line, "git log --first-parent --format=%s "):
		return strings.Join(state.merged, "\n"), true, nil
	case line == "git merge-base --is-ancestor "+state.latest+" "+r.commit:
		// Is the install built after the target?
		if state.newer {
			return "", true, nil
		}
		return "", true, errors.New("exit status 1")
	case strings.HasPrefix(line, "git merge-base --is-ancestor "):
		if state.diverged {
			return "", true, errors.New("exit status 1")
		}
		return "", true, nil
	case line == "git status --porcelain --untracked-files=no":
		return state.changes, true, nil
	case line == "git log --format=%h %s "+ref+"..HEAD":
		return state.local, true, nil
	case line == "git log --format=%h %s "+ref+"..main":
		return state.localMain, true, nil
	case line == "git show "+ref+":go.mod":
		if state.goMod == "" {
			return "", true, errors.New("exit status 128")
		}
		return state.goMod, true, nil
	}
	return "", false, nil
}

// behind puts origin/main ahead of the installed build by the titles given,
// newest first.
func (r *rig) behind(titles ...string) *rig {
	r.originState = originState{latest: "def5678abcd0000", latestVersion: "def5678", merged: titles}
	return r
}

// upToDate puts origin/main at the installed build.
func (r *rig) upToDate() *rig {
	r.originState = originState{latest: r.commit, latestVersion: r.version}
	return r
}

// released puts origin/main at a release tagged tag, ahead of the installed
// build by the titles given, newest first, with older releases below it.
func (r *rig) released(tag string, titles ...string) *rig {
	r.originState = originState{latest: "fed4321abcd0000", latestVersion: tag, tags: []string{tag, "v0.1.0-rc.1", "v0.0.9"}, release: tag, merged: titles}
	return r
}

var (
	fetch  = []string{"git", "fetch", "--quiet", "--tags", "origin"}
	merge  = []string{"git", "merge", "--ff-only", "origin/main"}
	toMain = []string{"git", "switch", "--quiet", "main"}
)

// checkOut is the command that detaches the checkout at a release.
func checkOut(tag string) []string {
	return []string{"git", "checkout", "--quiet", "--detach", "refs/tags/" + tag}
}

// installedVersions are what the installed app and CLI hold, and the ones
// kept as previous ("" for none).
func (r *rig) installedVersions() (app, cli, previousApp, previousCLI string) {
	layout := r.layout()
	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return string(data)
	}
	return read(r.app(layout.App.Installed)), read(layout.CLI.Installed),
		read(r.app(layout.App.Installed + install.PreviousSuffix)), read(layout.CLI.Installed + install.PreviousSuffix)
}

func TestUpdateCheckSaysWhenUpToDate(t *testing.T) {
	r := newRig(t, "linux").installed().upToDate()
	got := r.wizard(readyMachine("linux"), "", nil, "update", "--check")
	if got.err != nil || !reflect.DeepEqual(got.ran, [][]string{fetch}) || got.dirs[0] != r.clone {
		t.Fatalf("ran %v in %v, err %v\n%s", got.ran, got.dirs, got.err, got.output)
	}
	for _, want := range []string{"Fetching origin in " + r.clone + ".", "Inkuna abc1234 is up to date with origin/main."} {
		if !strings.Contains(got.output, want) {
			t.Errorf("output lacks %q:\n%s", want, got.output)
		}
	}
}

func TestUpdateCheckListsWhatWasMergedSince(t *testing.T) {
	r := newRig(t, "darwin").installed().behind("Third change (#3)", "Second change (#2)", "First change (#1)")
	got := r.wizard(readyMachine("darwin"), "", nil, "update", "--check")
	if exitCode(got.err) != 1 || !reflect.DeepEqual(got.ran, [][]string{fetch}) {
		t.Fatalf("exit %d, ran %v\n%s", exitCode(got.err), got.ran, got.output)
	}
	want := "Inkuna abc1234 is 3 commits behind origin/main, which is at def5678. Merged since:\n  - Third change (#3)\n  - Second change (#2)\n  - First change (#1)\nRun inkunactl update to update.\n"
	if !strings.Contains(got.output, want) {
		t.Fatalf("output lacks %q:\n%s", want, got.output)
	}
	if app, cli, _, _ := r.installedVersions(); app != "abc1234" || cli != "abc1234" || strings.Contains(got.output, "[y/N]") {
		t.Fatalf("--check changed the install (%s, %s) or asked:\n%s", app, cli, got.output)
	}
}

func TestUpdateCheckListsAtMostTwentyTitles(t *testing.T) {
	titles := make([]string, 25)
	for index := range titles {
		titles[index] = fmt.Sprintf("Change %d", 25-index)
	}
	r := newRig(t, "linux").installed().behind(titles...)
	got := r.wizard(readyMachine("linux"), "", nil, "update", "--check")
	if !strings.Contains(got.output, "is 25 commits behind") || !strings.Contains(got.output, "  - Change 6\n  and 5 more\n") || strings.Contains(got.output, "Change 5\n") {
		t.Fatalf("output:\n%s", got.output)
	}
}

func TestUpdateCheckSaysWhenTheInstallIsNotOnMain(t *testing.T) {
	r := newRig(t, "linux").installed().behind("A change on main (#9)")
	r.originState.diverged = true
	got := r.wizard(readyMachine("linux"), "", nil, "update", "--check")
	want := "Inkuna abc1234 was built from a commit that is not on origin/main, which is at def5678, with 1 commit the install does not have:\n  - A change on main (#9)\n"
	if exitCode(got.err) != 1 || !strings.Contains(got.output, want) {
		t.Fatalf("exit %d, output lacks %q:\n%s", exitCode(got.err), want, got.output)
	}
}

func TestUpdateNeedsAnInstall(t *testing.T) {
	r := newRig(t, "linux").behind()
	got := r.wizard(readyMachine("linux"), "", nil, "update")
	if exitCode(got.err) != 1 || len(got.ran) != 0 || !strings.Contains(got.output, "There is no install to update: "+install.RecordPath(r.dataDir)+" does not exist. Run inkunactl install") {
		t.Fatalf("exit %d, ran %v\n%s", exitCode(got.err), got.ran, got.output)
	}
}

func TestUpdateFastForwardsBuildsAndSwapsTheBuildIn(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			r := newRig(t, goos).installed().behind("Faster startup (#12)")
			// Update, then don't open the app.
			got := r.wizard(readyMachine(goos), "y\nn\n", nil, "update")
			if got.err != nil {
				t.Fatalf("err %v\n%s", got.err, got.output)
			}
			want := append([][]string{fetch, merge}, builds...)
			if !reflect.DeepEqual(got.ran, want) {
				t.Fatalf("ran %v, want %v", got.ran, want)
			}
			for _, dir := range got.dirs {
				if dir != r.clone {
					t.Fatalf("ran in %v, want every command in %s", got.dirs, r.clone)
				}
			}
			if app, cli, previousApp, previousCLI := r.installedVersions(); app != "def5678" || cli != "def5678" || previousApp != "abc1234" || previousCLI != "abc1234" {
				t.Fatalf("installed %s and %s, previous %s and %s", app, cli, previousApp, previousCLI)
			}
			if record, _ := r.record(); record.Version != "def5678" || record.Commit != "def5678abcd0000" || record.Source != r.clone {
				t.Fatalf("record %+v", record)
			}
			if kept, _, _ := install.ReadRecord(install.PreviousRecordPath(r.dataDir)); kept.Version != "abc1234" || kept.Commit != "abc1234def5678" {
				t.Fatalf("previous record %+v", kept)
			}
			for _, line := range []string{
				"Inkuna abc1234 is 1 commit behind origin/main, which is at def5678. Merged since:\n  - Faster startup (#12)\n",
				"inkunactl update fast-forwards " + r.clone + " to origin/main, builds it with task deps, then task package and task build",
				"Configuration, the database, and secrets are left as they are.",
				"Update Inkuna to def5678? [y/N]",
				// The prerequisites are checked again before the build.
				"Checking what Inkuna needs on",
				"[3/3] task build",
				"Updated Inkuna from abc1234 to def5678. inkunactl update --rollback goes back to abc1234.",
			} {
				if !strings.Contains(got.output, line) {
					t.Errorf("output lacks %q:\n%s", line, got.output)
				}
			}
			// Now up to date.
			got = r.wizard(readyMachine(goos), "", nil, "update")
			if got.err != nil || !reflect.DeepEqual(got.ran, [][]string{fetch}) || !strings.Contains(got.output, "Inkuna def5678 is up to date with origin/main.") {
				t.Fatalf("second update ran %v, err %v\n%s", got.ran, got.err, got.output)
			}
		})
	}
}

// --yes agrees to the update, for CI, but never opens the app.
func TestUpdateYesAgreesToTheUpdate(t *testing.T) {
	r := newRig(t, "windows").installed().behind("A change (#1)")
	got := r.wizard(readyMachine("windows"), "", nil, "update", "--yes")
	if got.err != nil || !strings.Contains(got.output, "Update Inkuna to def5678? [y/N] yes (--yes)") || len(r.opened) != 0 {
		t.Fatalf("err %v, opened %v\n%s", got.err, r.opened, got.output)
	}
	if _, cli, _, _ := r.installedVersions(); cli != "def5678" {
		t.Fatalf("installed %s", cli)
	}
}

func TestUpdateRefusesACheckoutItCannotFastForward(t *testing.T) {
	for name, test := range map[string]struct {
		change func(r *rig)
		want   string
	}{
		"uncommitted changes": {
			func(r *rig) { r.originState.changes = "M internal/app/service.go\nA  notes.md" },
			"has uncommitted changes, so it is not fast-forwarded:\n  M internal/app/service.go\n  A  notes.md\nCommit, stash, or discard them, then run inkunactl update again. Nothing was changed.",
		},
		"local commits": {
			func(r *rig) { r.originState.local = "1a2b3c4 Try a faster index" },
			"has commits that are not on origin/main, so it is not fast-forwarded:\n  1a2b3c4 Try a faster index\nMove them to a branch of their own",
		},
		"another branch": {
			func(r *rig) { r.branch = "feature/42-faster" },
			"is on feature/42-faster, not main, so it is not fast-forwarded. Switch it to main",
		},
		"commits on main beside a checkout at a commit": {
			func(r *rig) { r.branch, r.originState.localMain = "HEAD", "9f8e7d6 An experiment on main" },
			"has commits that are not on origin/main, so it is not fast-forwarded:\n  9f8e7d6 An experiment on main\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, "linux").installed().behind("A change (#1)")
			test.change(r)
			got := r.wizard(readyMachine("linux"), "y\ny\ny\n", nil, "update")
			if exitCode(got.err) != 1 || !reflect.DeepEqual(got.ran, [][]string{fetch}) {
				t.Fatalf("exit %d, ran %v\n%s", exitCode(got.err), got.ran, got.output)
			}
			if !strings.Contains(got.output, "The checkout "+r.clone+" "+test.want) || strings.Contains(got.output, "[y/N]") {
				t.Fatalf("output lacks %q, or asked:\n%s", test.want, got.output)
			}
			if app, cli, previousApp, _ := r.installedVersions(); app != "abc1234" || cli != "abc1234" || previousApp != "" {
				t.Fatalf("the install changed: %s, %s, previous %s", app, cli, previousApp)
			}
		})
	}
}

func TestUpdateRefusesWhileTheAppRuns(t *testing.T) {
	r := newRig(t, "darwin").installed().behind("A change (#1)")
	layout := r.layout()
	r.running = layout.AppProgram()
	got := r.wizard(readyMachine("darwin"), "y\ny\n", nil, "update")
	if exitCode(got.err) != 1 || !reflect.DeepEqual(got.ran, [][]string{fetch}) || strings.Contains(got.output, "[y/N]") {
		t.Fatalf("exit %d, ran %v\n%s", exitCode(got.err), got.ran, got.output)
	}
	if want := "Inkuna is running from " + layout.App.Installed + ". Quit it, then run inkunactl update again. Nothing was replaced."; !strings.Contains(got.output, want) {
		t.Fatalf("output lacks %q:\n%s", want, got.output)
	}
	// Opened during the build, it still is not replaced.
	r.running = ""
	got = r.wizard(readyMachine("darwin"), "y\n", func(argv []string) {
		if strings.Join(argv, " ") == "task build" {
			r.running = layout.AppProgram()
		}
	}, "update")
	if exitCode(got.err) != 1 || !strings.Contains(got.output, "Inkuna is running from") {
		t.Fatalf("exit %d\n%s", exitCode(got.err), got.output)
	}
	if app, cli, previousApp, _ := r.installedVersions(); app != "abc1234" || cli != "abc1234" || previousApp != "" {
		t.Fatalf("the install changed: %s, %s, previous %s", app, cli, previousApp)
	}
	// Once it quits, the update finishes from where it stopped.
	r.running = ""
	if got := r.wizard(readyMachine("darwin"), "y\nn\n", nil, "update"); got.err != nil {
		t.Fatalf("err %v\n%s", got.err, got.output)
	}
	if _, cli, _, _ := r.installedVersions(); cli != "def5678" {
		t.Fatalf("installed %s", cli)
	}
}

// The commit an update builds can need a newer Go than the running
// inkunactl knows. Its go.mod says, and the update stops before changing
// anything.
func TestUpdateChecksTheGoTheNewCommitNeeds(t *testing.T) {
	r := newRig(t, "linux").installed().behind("Move to Go 1.26 (#30)")
	r.originState.goMod = "module " + inkunaModule + "\n\ngo 1.26.0\n"
	got := r.wizard(readyMachine("linux"), "y\n", nil, "update")
	if exitCode(got.err) != 1 || !reflect.DeepEqual(got.ran, [][]string{fetch}) {
		t.Fatalf("exit %d, ran %v\n%s", exitCode(got.err), got.ran, got.output)
	}
	if !strings.Contains(got.output, "Go 1.26.0 or later (found 1.25.5)") || !strings.Contains(got.output, "then run inkunactl update again") {
		t.Fatalf("output:\n%s", got.output)
	}
}

func TestUpdateRollbackSwapsThePreviousBuildBack(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			r := newRig(t, goos).installed().behind("A change (#1)")
			if got := r.wizard(readyMachine(goos), "y\nn\n", nil, "update"); got.err != nil {
				t.Fatalf("update: %v\n%s", got.err, got.output)
			}
			layout := r.layout()
			got := r.wizard(readyMachine(goos), "y\n", nil, "update", "--rollback")
			if got.err != nil || len(got.ran) != 0 {
				t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
			}
			if app, cli, previousApp, previousCLI := r.installedVersions(); app != "abc1234" || cli != "abc1234" || previousApp != "def5678" || previousCLI != "def5678" {
				t.Fatalf("installed %s and %s, previous %s and %s", app, cli, previousApp, previousCLI)
			}
			if record, _ := r.record(); record.Version != "abc1234" || record.Commit != "abc1234def5678" || record.Source != r.clone {
				t.Fatalf("record %+v", record)
			}
			if kept, _, _ := install.ReadRecord(install.PreviousRecordPath(r.dataDir)); kept.Version != "def5678" {
				t.Fatalf("previous record %+v", kept)
			}
			for _, line := range []string{
				"The app and inkunactl go back to abc1234, kept as:\n    " + layout.App.Installed + install.PreviousSuffix + "\n    " + layout.CLI.Installed + install.PreviousSuffix + "\n",
				"The build installed now, def5678, is kept as the previous one, so inkunactl update --rollback again returns to it.",
				"The database keeps any migrations the newer build ran.",
				"Roll back to abc1234? [y/N]",
				"Inkuna abc1234 is installed again. inkunactl update brings it up to date; inkunactl update --rollback goes back to def5678.",
			} {
				if !strings.Contains(got.output, line) {
					t.Errorf("output lacks %q:\n%s", line, got.output)
				}
			}
			// A second rollback undoes the first.
			if got := r.wizard(readyMachine(goos), "y\n", nil, "update", "--rollback"); got.err != nil {
				t.Fatalf("second rollback: %v\n%s", got.err, got.output)
			}
			if _, cli, _, previousCLI := r.installedVersions(); cli != "def5678" || previousCLI != "abc1234" {
				t.Fatalf("after two rollbacks, installed %s, previous %s", cli, previousCLI)
			}
		})
	}
}

// A rollback says before it asks when the build it restores would refuse
// the database, because the newer build ran a migration it does not know.
func TestUpdateRollbackWarnsWhenTheOlderBuildWouldRefuseTheDatabase(t *testing.T) {
	r := newRig(t, "linux").installed().behind("A change (#1)")
	if got := r.wizard(readyMachine("linux"), "y\nn\n", nil, "update"); got.err != nil {
		t.Fatalf("update: %v\n%s", got.err, got.output)
	}
	// The newer build migrated the database past what abc1234 knows.
	path := filepath.Join(r.dataDir, "inkuna.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	known := storage.Migrations()
	r.migrations["abc1234"] = known[len(known)-2]
	got := r.wizard(readyMachine("linux"), "n\n", nil, "update", "--rollback")
	want := "Warning: the database has " + known[len(known)-1] + ", which abc1234 does not know, so its app and inkunactl will refuse to open the database until you update again (inkunactl update)."
	if exitCode(got.err) != 1 || !strings.Contains(got.output, want) || strings.Index(got.output, want) > strings.Index(got.output, "Roll back to abc1234?") {
		t.Fatalf("the warning must come before the question:\n%s", got.output)
	}
	// A build that knows every migration rolls back without one.
	r.migrations["abc1234"] = storage.LatestMigration()
	if got := r.wizard(readyMachine("linux"), "n\n", nil, "update", "--rollback"); strings.Contains(got.output, "Warning:") || strings.Contains(got.output, "not known") {
		t.Fatalf("no warning for a build that knows the database:\n%s", got.output)
	}
	// A build from before builds reported their migrations opens any
	// database, as it always did.
	delete(r.migrations, "abc1234")
	if got := r.wizard(readyMachine("linux"), "n\n", nil, "update", "--rollback"); !strings.Contains(got.output, "abc1234 is from before Inkuna checked a database's migrations") {
		t.Fatalf("an older build is named as one that does not check:\n%s", got.output)
	}
}

func TestUpdateRollbackRefuses(t *testing.T) {
	t.Run("without a previous build", func(t *testing.T) {
		r := newRig(t, "linux").installed()
		got := r.wizard(readyMachine("linux"), "y\n", nil, "update", "--rollback")
		if exitCode(got.err) != 1 || !strings.Contains(got.output, "There is no previous build to go back to") {
			t.Fatalf("exit %d\n%s", exitCode(got.err), got.output)
		}
	})
	t.Run("while the app runs", func(t *testing.T) {
		r := newRig(t, "linux").installed().behind("A change (#1)")
		if got := r.wizard(readyMachine("linux"), "y\nn\n", nil, "update"); got.err != nil {
			t.Fatalf("update: %v\n%s", got.err, got.output)
		}
		r.running = r.layout().AppProgram()
		got := r.wizard(readyMachine("linux"), "y\n", nil, "update", "--rollback")
		if exitCode(got.err) != 1 || !strings.Contains(got.output, "Quit it, then run inkunactl update again.") {
			t.Fatalf("exit %d\n%s", exitCode(got.err), got.output)
		}
		if _, cli, _, _ := r.installedVersions(); cli != "def5678" {
			t.Fatalf("installed %s", cli)
		}
	})
	t.Run("without a yes", func(t *testing.T) {
		r := newRig(t, "linux").installed().behind("A change (#1)")
		if got := r.wizard(readyMachine("linux"), "y\nn\n", nil, "update"); got.err != nil {
			t.Fatalf("update: %v\n%s", got.err, got.output)
		}
		got := r.wizard(readyMachine("linux"), "", nil, "update", "--rollback")
		if exitCode(got.err) != 1 || !strings.Contains(got.output, "Nothing was changed.") {
			t.Fatalf("exit %d\n%s", exitCode(got.err), got.output)
		}
		if _, cli, _, _ := r.installedVersions(); cli != "def5678" {
			t.Fatalf("installed %s", cli)
		}
	})
}

func TestUpdateRejectsUnknownArguments(t *testing.T) {
	r := newRig(t, "linux").installed()
	for _, args := range [][]string{{"update", "--force"}, {"update", "--check", "--rollback"}, {"update", "--rollback", "--channel", "edge"}, {"update", "--rollback", "--channel="}, {"update", "--channel"}} {
		if got := r.wizard(readyMachine("linux"), "", nil, args...); got.err == nil || got.err.Error() != updateUsage {
			t.Errorf("%v: err %v", args, got.err)
		}
	}
}

func TestUpdateRejectsAnUnknownChannel(t *testing.T) {
	r := newRig(t, "linux").installed().behind("A change (#1)")
	for _, name := range []string{"nightly", ""} {
		got := r.wizard(readyMachine("linux"), "", nil, "update", "--channel="+name)
		if got.err == nil || !strings.Contains(got.err.Error(), `unknown channel "`+name+`": use stable or edge`) || len(got.ran) != 0 {
			t.Fatalf("%q: ran %v, err %v", name, got.ran, got.err)
		}
	}
	if _, err := os.Stat(install.ChannelPath(r.dataDir)); err == nil {
		t.Fatal("an unknown channel was recorded")
	}
}

// Before the first release, the stable channel follows origin/main, and
// says so.
func TestUpdateFollowsMainUntilTheFirstRelease(t *testing.T) {
	r := newRig(t, "linux").installed().behind("A change (#1)")
	r.originState.tags = []string{"v0.1.0-rc.1"}
	got := r.wizard(readyMachine("linux"), "", nil, "update", "--check")
	if exitCode(got.err) != 1 || !strings.Contains(got.output, "No release of Inkuna is tagged yet, so the stable channel follows origin/main until the first one.") ||
		!strings.Contains(got.output, "is 1 commit behind origin/main") {
		t.Fatalf("exit %d\n%s", exitCode(got.err), got.output)
	}
}

// The stable channel checks the checkout out at the newest release, past a
// pre-release above it, and builds it.
func TestUpdateFollowsTheLatestRelease(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			r := newRig(t, goos).installed().released("v0.2.0", "Second change (#2)", "First change (#1)")
			r.originState.tags = []string{"v0.3.0-rc.1", "v0.2.0", "v0.1.0"}
			got := r.wizard(readyMachine(goos), "y\nn\n", nil, "update")
			if want := append([][]string{fetch, checkOut("v0.2.0")}, builds...); got.err != nil || !reflect.DeepEqual(got.ran, want) {
				t.Fatalf("ran %v, want %v, err %v\n%s", got.ran, want, got.err, got.output)
			}
			for _, line := range []string{
				"Inkuna abc1234 is 2 commits behind v0.2.0. Merged since:\n  - Second change (#2)\n  - First change (#1)\n",
				"inkunactl update checks " + r.clone + " out at the release v0.2.0, builds it with task deps",
				"Update Inkuna to v0.2.0? [y/N]",
				"Check out " + r.clone + " at v0.2.0",
				"Updated Inkuna from abc1234 to v0.2.0.",
			} {
				if !strings.Contains(got.output, line) {
					t.Errorf("output lacks %q:\n%s", line, got.output)
				}
			}
			if record, _ := r.record(); record.Version != "v0.2.0" || record.Commit != "fed4321abcd0000" {
				t.Fatalf("record %+v", record)
			}
			got = r.wizard(readyMachine(goos), "", nil, "update")
			if got.err != nil || !reflect.DeepEqual(got.ran, [][]string{fetch}) || !strings.Contains(got.output, "Inkuna v0.2.0 is up to date with v0.2.0.") {
				t.Fatalf("second update ran %v, err %v\n%s", got.ran, got.err, got.output)
			}
		})
	}
}

// An install built after the latest release, as one from main is, is not
// taken back to it.
func TestUpdateKeepsAnInstallNewerThanTheLatestRelease(t *testing.T) {
	r := newRig(t, "darwin").installed().released("v0.2.0")
	r.originState.newer = true
	for _, args := range [][]string{{"update", "--check"}, {"update"}} {
		got := r.wizard(readyMachine("darwin"), "y\n", nil, args...)
		if got.err != nil || !reflect.DeepEqual(got.ran, [][]string{fetch}) || strings.Contains(got.output, "[y/N]") ||
			!strings.Contains(got.output, "Inkuna abc1234 is newer than the latest release, v0.2.0, so it stays as it is until a newer release. inkunactl update --channel edge follows origin/main instead.") {
			t.Fatalf("%v: ran %v, err %v\n%s", args, got.ran, got.err, got.output)
		}
	}
}

func TestUpdateRefusesToMoveAwayFromCommitsTheReleaseLacks(t *testing.T) {
	r := newRig(t, "linux").installed().released("v0.2.0", "A change (#1)")
	r.branch, r.originState.local = "HEAD", "1a2b3c4 A fix of my own"
	got := r.wizard(readyMachine("linux"), "y\n", nil, "update")
	want := "The checkout " + r.clone + " has commits that are not on v0.2.0, so it is not moved to v0.2.0:\n  1a2b3c4 A fix of my own\n"
	if exitCode(got.err) != 1 || !reflect.DeepEqual(got.ran, [][]string{fetch}) || !strings.Contains(got.output, want) {
		t.Fatalf("exit %d, ran %v\n%s", exitCode(got.err), got.ran, got.output)
	}
}

// --channel edge is remembered: this update and the next follow origin/main
// past the release, switching a checkout at the release back to main.
// --channel stable goes back.
func TestUpdateChannelIsRemembered(t *testing.T) {
	r := newRig(t, "linux").installed().behind("A change after v0.2.0 (#5)")
	r.branch, r.originState.tags = "HEAD", []string{"v0.2.0"}
	got := r.wizard(readyMachine("linux"), "y\nn\n", nil, "update", "--channel", "edge")
	if want := append([][]string{fetch, toMain, merge}, builds...); got.err != nil || !reflect.DeepEqual(got.ran, want) {
		t.Fatalf("ran %v, want %v, err %v\n%s", got.ran, want, got.err, got.output)
	}
	for _, line := range []string{
		"Now following the edge channel: origin/main, every change as it merges.",
		"inkunactl update switches " + r.clone + " to main, fast-forwards it to origin/main, builds it",
	} {
		if !strings.Contains(got.output, line) {
			t.Errorf("output lacks %q:\n%s", line, got.output)
		}
	}
	if channel, err := install.ReadChannel(install.ChannelPath(r.dataDir)); err != nil || channel != install.ChannelEdge {
		t.Fatalf("channel %q, err %v", channel, err)
	}
	got = r.wizard(readyMachine("linux"), "", nil, "update", "--check")
	if got.err != nil || !strings.Contains(got.output, "Inkuna def5678 is up to date with origin/main.") || strings.Contains(got.output, "No release") {
		t.Fatalf("err %v\n%s", got.err, got.output)
	}
	r.released("v0.3.0", "Release 0.3.0 (#9)")
	got = r.wizard(readyMachine("linux"), "", nil, "update", "--channel", "stable", "--check")
	if exitCode(got.err) != 1 || !strings.Contains(got.output, "Now following the stable channel: the newest release.") || !strings.Contains(got.output, "behind v0.3.0") {
		t.Fatalf("exit %d\n%s", exitCode(got.err), got.output)
	}
	if got := r.wizard(readyMachine("linux"), "", nil, "update", "--channel", "stable", "--check"); !strings.Contains(got.output, "Already following the stable channel.") {
		t.Fatalf("output:\n%s", got.output)
	}
}

// Each record names the checkout its build came from: a rollback to a build
// from another checkout makes that checkout the one to update.
func TestUpdateRollbackRecordsEachBuildsCheckout(t *testing.T) {
	r := newRig(t, "linux").installed().behind("A change (#1)")
	if got := r.wizard(readyMachine("linux"), "y\nn\n", nil, "update"); got.err != nil {
		t.Fatalf("update: %v\n%s", got.err, got.output)
	}
	other := filepath.Join(r.root, "src", "other")
	r.makeCheckout(other)
	kept, _, err := install.ReadRecord(install.PreviousRecordPath(r.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	kept.Source = other
	if err := install.WriteRecord(install.PreviousRecordPath(r.dataDir), kept); err != nil {
		t.Fatal(err)
	}
	if got := r.wizard(readyMachine("linux"), "y\n", nil, "update", "--rollback"); got.err != nil {
		t.Fatalf("rollback: %v\n%s", got.err, got.output)
	}
	if record, _ := r.record(); record.Source != other || record.Version != "abc1234" {
		t.Fatalf("record %+v, want abc1234 from %s", record, other)
	}
	if kept, _, _ := install.ReadRecord(install.PreviousRecordPath(r.dataDir)); kept.Source != r.clone || kept.Version != "def5678" {
		t.Fatalf("previous record %+v, want def5678 from %s", kept, r.clone)
	}
}

// On macOS the app goes in /Applications only while it is writable. An
// install made in ~/Applications is updated and rolled back there, where
// the record says it is, even once /Applications is writable.
func TestUpdateReplacesTheAppWhereTheRecordSaysItIs(t *testing.T) {
	r := newRig(t, "darwin").installed().behind("A change (#1)")
	layout := r.layout()
	recorded := filepath.Join(r.home, "Applications", "Inkuna.app")
	if err := os.MkdirAll(filepath.Dir(recorded), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(layout.App.Installed, recorded); err != nil {
		t.Fatal(err)
	}
	record, _ := r.record()
	record.App = recorded
	if err := install.WriteRecord(install.RecordPath(r.dataDir), record); err != nil {
		t.Fatal(err)
	}
	if got := r.wizard(readyMachine("darwin"), "y\nn\n", nil, "update"); got.err != nil || !strings.Contains(got.output, "The app: "+recorded+"\n") {
		t.Fatalf("err %v\n%s", got.err, got.output)
	}
	if got := r.read(r.app(recorded)); got != "def5678" {
		t.Fatalf("%s holds %s", recorded, got)
	}
	if _, err := os.Stat(layout.App.Installed); !os.IsNotExist(err) {
		t.Fatalf("the update put a second app in %s", layout.App.Installed)
	}
	if record, _ := r.record(); record.App != recorded {
		t.Fatalf("record app %s, want %s", record.App, recorded)
	}
	if got := r.wizard(readyMachine("darwin"), "y\n", nil, "update", "--rollback"); got.err != nil || r.read(r.app(recorded)) != "abc1234" {
		t.Fatalf("rollback: err %v\n%s", got.err, got.output)
	}
}

// A build with uncommitted changes is not origin/main, even of its commit:
// the update builds that commit as committed.
func TestUpdateRebuildsADirtyBuildOfTheLatestCommit(t *testing.T) {
	r := newRig(t, "linux")
	r.version = "abc1234-dirty"
	r.installed()
	r.originState = originState{latest: r.commit, latestVersion: "abc1234"}
	got := r.wizard(readyMachine("linux"), "", nil, "update", "--check")
	want := "Inkuna abc1234-dirty was built from origin/main's commit with uncommitted changes. An update builds that commit as committed."
	if exitCode(got.err) != 1 || !strings.Contains(got.output, want) {
		t.Fatalf("exit %d, output lacks %q:\n%s", exitCode(got.err), want, got.output)
	}
	r.version = "abc1234"
	got = r.wizard(readyMachine("linux"), "y\nn\n", nil, "update")
	if got.err != nil || !reflect.DeepEqual(got.ran, append([][]string{fetch, merge}, builds...)) {
		t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
	if _, cli, _, previousCLI := r.installedVersions(); cli != "abc1234" || previousCLI != "abc1234-dirty" {
		t.Fatalf("installed %s, previous %s", cli, previousCLI)
	}
}

// The app can be opened while the rollback question waits for an answer.
func TestUpdateRollbackChecksTheAppAgainAfterTheAnswer(t *testing.T) {
	r := newRig(t, "linux").installed().behind("A change (#1)")
	if got := r.wizard(readyMachine("linux"), "y\nn\n", nil, "update"); got.err != nil {
		t.Fatalf("update: %v\n%s", got.err, got.output)
	}
	opened := &openedWhileAsked{Reader: strings.NewReader("y\n"), open: func() { r.running = r.layout().AppProgram() }}
	got := r.wizardReading(readyMachine("linux"), opened, "update", "--rollback")
	if exitCode(got.err) != 1 || !strings.Contains(got.output, "Inkuna is running from") {
		t.Fatalf("exit %d\n%s", exitCode(got.err), got.output)
	}
	if _, cli, _, _ := r.installedVersions(); cli != "def5678" {
		t.Fatalf("installed %s", cli)
	}
}

// openedWhileAsked is input whose first read, the answer to a question,
// also opens the app.
type openedWhileAsked struct {
	io.Reader
	open func()
}

func (input *openedWhileAsked) Read(buffer []byte) (int, error) {
	if input.open != nil {
		input.open()
		input.open = nil
	}
	return input.Reader.Read(buffer)
}
