package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/install"
	"github.com/inkuna-ai/inkuna/internal/prereq"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/version"
)

const updateUsage = "usage: update [--check | --rollback] [--channel stable|edge] [--yes]"

// mainBranch is what the edge channel follows, and the stable one until the
// first release is tagged.
const mainBranch = "origin/main"

// releaseTag matches the tag of a release. A pre-release such as
// v1.0.0-rc.1 does not, so the stable channel never follows one.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// maxTitles caps the titles an update lists, so an install far behind does
// not fill the terminal.
const maxTitles = 20

// runUpdate brings an install made by inkunactl install up to date with its
// channel, the newest release or origin/main, says what changed, or with
// --rollback swaps the build the last install or update replaced back in.
func runUpdate(args []string, stdin io.Reader, stdout io.Writer) error {
	wizard, err := newInstaller(stdin, stdout)
	if err != nil {
		return err
	}
	return wizard.update(args)
}

// installation is what an update works on: the data directory the install
// is recorded in, where its app and CLI are, and its record.
type installation struct {
	dataDir  string
	layout   install.Layout
	record   install.Record
	recorded bool
}

// updateTarget is what an update moves the checkout to and builds.
type updateTarget struct {
	// ref is what git resolves: origin/main, or refs/tags/<tag> for a
	// release, which no branch of that name can shadow.
	ref string
	// name is what messages call it: origin/main, or the release's tag.
	name string
	// release is set for a release, which the checkout is detached at.
	release bool
}

func mainTarget() updateTarget { return updateTarget{ref: mainBranch, name: mainBranch} }

func releaseTarget(tag string) updateTarget {
	return updateTarget{ref: "refs/tags/" + tag, name: tag, release: true}
}

// updateStatus is how the installed build compares with the channel's
// target.
type updateStatus struct {
	source    string
	target    updateTarget
	installed version.Info
	latest    version.Info
	// newer is set when the install was built from a commit after the
	// release the stable channel follows, as one built from main is.
	newer bool
}

// upToDate reports whether the target's commit is installed as committed,
// or a later one is. A build with uncommitted changes (-dirty) of the
// target's commit is not, since its version does not say which changes it
// has.
func (status updateStatus) upToDate() bool {
	return status.newer || status.installed.Commit == status.latest.Commit && !dirty(status.installed)
}

func dirty(build version.Info) bool {
	return strings.HasSuffix(build.Version, "-dirty")
}

func (wizard *installer) update(args []string) error {
	wizard.command = "update"
	checkOnly, rollback := false, false
	// channelGiven tells --channel= from no --channel, so an empty name is
	// refused rather than read as none.
	channelName, channelGiven := "", false
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; {
		case arg == "--check":
			checkOnly = true
		case arg == "--rollback":
			rollback = true
		case arg == "--yes":
			wizard.yes = true
		case arg == "--channel" && index+1 < len(args):
			index++
			channelName, channelGiven = args[index], true
		case strings.HasPrefix(arg, "--channel="):
			channelName, channelGiven = strings.TrimPrefix(arg, "--channel="), true
		default:
			return errors.New(updateUsage)
		}
	}
	if checkOnly && rollback || rollback && channelGiven {
		return errors.New(updateUsage)
	}
	var channel install.Channel
	if channelGiven {
		parsed, err := install.ParseChannel(channelName)
		if err != nil {
			return fmt.Errorf("%w\n%s", err, updateUsage)
		}
		channel = parsed
	}
	found, err := wizard.findInstall()
	if err != nil {
		return err
	}
	if rollback {
		return wizard.rollback(found)
	}
	if channel != "" && found.recorded {
		if err := wizard.switchChannel(found, channel); err != nil {
			return err
		}
	}
	if checkOnly {
		// Only the remote-tracking refs change, so no lock is needed.
		status, err := wizard.status(found)
		if err != nil {
			return err
		}
		if !status.upToDate() {
			wizard.say("Run inkunactl update to update.\n")
			return exitError{code: 1}
		}
		return nil
	}
	unlock, err := wizard.lock(found.layout)
	if err != nil {
		return err
	}
	defer unlock()
	status, err := wizard.status(found)
	if err != nil || status.upToDate() {
		return err
	}
	// Everything that would stop the update is checked before asking, so a
	// refusal changes nothing.
	branch, err := wizard.checkoutReady(status.source, status.target)
	if err != nil {
		return err
	}
	if err := wizard.appStopped(found.layout); err != nil {
		return err
	}
	wizard.say("\nUpdate\n  inkunactl update %s, builds it with %s, and puts the app and inkunactl in place:\n", describeMove(status.source, status.target, branch), buildDescription())
	wizard.describePlace(found.layout)
	wizard.say("  Configuration, the database, and secrets are left as they are. The app runs its database migrations when it next starts.\n")
	if !wizard.agree("  Update Inkuna to " + status.latest.Version + "?") {
		wizard.say("  Nothing was changed. Run inkunactl update again to update.\n")
		return exitError{code: 1}
	}
	// The commit about to be built may need a newer Go than this inkunactl
	// knows, which its go.mod says.
	if goMod, err := wizard.system.output(status.source, []string{wizard.git(), "show", status.target.ref + ":go.mod"}); err == nil {
		wizard.options.GoMinimum = prereq.GoDirective([]byte(goMod))
	}
	wizard.say("\n")
	if _, err := wizard.prerequisites(); err != nil {
		return err
	}
	if err := wizard.move(status.source, status.target, branch); err != nil {
		return err
	}
	wizard.say("\nBuild\n")
	built, err := wizard.runBuild(status.source, found.layout)
	if err != nil {
		return err
	}
	// The app may have been opened during the build; swapIn checks again.
	if err := wizard.swapIn(found.layout, status.source, built, found.dataDir, found.record); err != nil {
		return err
	}
	wizard.say("\nUpdated Inkuna from %s to %s. inkunactl update --rollback goes back to %s.\n", status.installed.Version, built.Version, status.installed.Version)
	// Opening the app always asks, as at the end of an install.
	if wizard.confirm("\nOpen Inkuna now?") {
		if err := wizard.system.open(found.layout.App.Installed); err != nil {
			wizard.say("  Could not open it: %v\n", err)
		}
	}
	return nil
}

// switchChannel records the channel --channel names, which this and every
// later update follow.
func (wizard *installer) switchChannel(found installation, channel install.Channel) error {
	path := install.ChannelPath(found.dataDir)
	current, err := install.ReadChannel(path)
	if err != nil {
		return err
	}
	if current == channel {
		wizard.say("Already following the %s channel.\n", channel)
		return nil
	}
	if err := install.WriteChannel(path, channel); err != nil {
		return err
	}
	if channel == install.ChannelEdge {
		wizard.say("Now following the edge channel: %s, every change as it merges.\n", mainBranch)
	} else {
		wizard.say("Now following the stable channel: the newest release.\n")
	}
	return nil
}

// findInstall reads the install record of the configured data directory.
// Unlike install, it never runs setup: there is nothing to update without an
// install, and an install has its configuration.
func (wizard *installer) findInstall() (installation, error) {
	configuration, err := wizard.system.loadConfig()
	if err != nil {
		return installation{}, fmt.Errorf("load configuration: %w", err)
	}
	dataDir, err := filepath.Abs(configuration.DataDir)
	if err != nil {
		return installation{}, fmt.Errorf("resolve the data directory: %w", err)
	}
	layout, err := install.LayoutFor(wizard.system.platform)
	if err != nil {
		return installation{}, err
	}
	record, recorded, err := install.ReadRecord(install.RecordPath(dataDir))
	if err != nil {
		return installation{}, err
	}
	return installation{dataDir: dataDir, layout: recordedLayout(layout, record), record: record, recorded: recorded}, nil
}

// recordedLayout puts the app and CLI where the record says the install put
// them. The layout can choose another place since: on macOS the app goes in
// /Applications only while it is writable, else in ~/Applications. An
// update replaces the install where it is, and a rollback finds its
// *.previous builds there.
func recordedLayout(layout install.Layout, record install.Record) install.Layout {
	if record.App != "" {
		layout.App.Installed = record.App
	}
	if record.CLI != "" {
		layout.CLI.Installed = record.CLI
		layout.BinDir = filepath.Dir(record.CLI)
	}
	return layout
}

// latestRelease is the highest release tag on origin/main in the checkout,
// or "" before the first release. A tag off main is never followed.
func (wizard *installer) latestRelease(source string) (string, error) {
	tags, err := wizard.system.output(source, []string{wizard.git(), "tag", "--list", "--merged", mainBranch, "--sort=-v:refname", "v*"})
	if err != nil {
		return "", fmt.Errorf("list the releases in %s: %w", source, err)
	}
	for _, tag := range nonEmptyLines(tags) {
		if tag = strings.TrimSpace(tag); releaseTag.MatchString(tag) {
			return tag, nil
		}
	}
	return "", nil
}

// target is what the channel follows now: origin/main on edge; on stable
// the newest release, or origin/main until the first one.
func (wizard *installer) target(source string, channel install.Channel) (updateTarget, error) {
	if channel == install.ChannelEdge {
		return mainTarget(), nil
	}
	tag, err := wizard.latestRelease(source)
	if err != nil {
		return updateTarget{}, err
	}
	if tag == "" {
		wizard.say("No release of Inkuna is tagged yet, so the stable channel follows %s until the first one.\n", mainBranch)
		return mainTarget(), nil
	}
	return releaseTarget(tag), nil
}

// status fetches the checkout's origin and its tags and says how the
// installed build compares with what its channel follows: up to date, newer
// than the latest release, or how many commits behind with the titles of
// what was merged since. Inkuna's pull requests are squash-merged under
// their titles, so each first-parent commit subject is one.
func (wizard *installer) status(found installation) (updateStatus, error) {
	source := found.record.Source
	switch {
	case !found.recorded:
		wizard.say("There is no install to update: %s does not exist. Run inkunactl install to install Inkuna.\n", install.RecordPath(found.dataDir))
		return updateStatus{}, exitError{code: 1}
	case !isInkunaCheckout(source):
		wizard.say("The checkout Inkuna was built from, %s, is no longer a clone of %s. Run inkunactl install to build from another.\n", source, inkunaRepository)
		return updateStatus{}, exitError{code: 1}
	}
	channel, err := install.ReadChannel(install.ChannelPath(found.dataDir))
	if err != nil {
		return updateStatus{}, err
	}
	installed, err := wizard.versionOf(source, found.layout.CLI.Installed)
	if err == nil && installed.Commit == version.Dev {
		err = errors.New("it was built without a version")
	}
	if err != nil {
		wizard.say("Could not ask the installed inkunactl, %s, which build it is: %v. Run inkunactl install to install it again.\n", found.layout.CLI.Installed, err)
		return updateStatus{}, exitError{code: 1}
	}
	git := wizard.git()
	wizard.say("Fetching origin in %s.\n", source)
	if err := wizard.run(source, []string{git, "fetch", "--quiet", "--tags", "origin"}); err != nil {
		wizard.say("The fetch failed: %v. Its output above says why.\n", err)
		return updateStatus{}, exitError{code: 1}
	}
	target, err := wizard.target(source, channel)
	if err != nil {
		return updateStatus{}, err
	}
	latest, err := wizard.system.output(source, []string{git, "rev-parse", "--verify", target.ref + "^{commit}"})
	if err != nil {
		if target.release {
			wizard.say("The checkout has no commit for the release %s: %v.\n", target.name, err)
		} else {
			wizard.say("The checkout's origin has no main branch: %v.\n", err)
		}
		return updateStatus{}, exitError{code: 1}
	}
	described, err := wizard.system.output(source, []string{git, "describe", "--tags", "--always", target.ref})
	if err != nil {
		described = latest
	}
	status := updateStatus{source: source, target: target, installed: installed, latest: version.Info{Version: described, Commit: latest}}
	if status.upToDate() {
		wizard.say("\nInkuna %s is up to date with %s.\n", installed.Version, target.name)
		return status, nil
	}
	if installed.Commit == latest {
		wizard.say("\nInkuna %s was built from %s's commit with uncommitted changes. An update builds that commit as committed.\n", installed.Version, target.name)
		return status, nil
	}
	if _, err := wizard.system.output(source, []string{git, "cat-file", "-e", installed.Commit + "^{commit}"}); err != nil {
		wizard.say("\nInkuna %s is installed, and %s is at %s. The installed build's commit is not in %s, so what changed since cannot be listed.\n", installed.Version, target.name, described, source)
		return status, nil
	}
	// An install built after the release, as one from main is, stays until
	// a newer release: the stable channel never takes it back.
	if target.release {
		if _, err := wizard.system.output(source, []string{git, "merge-base", "--is-ancestor", latest, installed.Commit}); err == nil {
			wizard.say("\nInkuna %s is newer than the latest release, %s, so it stays as it is until a newer release. inkunactl update --channel edge follows %s instead.\n", installed.Version, target.name, mainBranch)
			status.newer = true
			return status, nil
		}
	}
	log, err := wizard.system.output(source, []string{git, "log", "--first-parent", "--format=%s", installed.Commit + ".." + latest})
	if err != nil {
		return updateStatus{}, fmt.Errorf("list the changes since %s: %w", installed.Version, err)
	}
	titles := nonEmptyLines(log)
	if _, err := wizard.system.output(source, []string{git, "merge-base", "--is-ancestor", installed.Commit, latest}); err == nil {
		where := ", which is at " + described
		if target.release {
			where = ""
		}
		wizard.say("\nInkuna %s is %s behind %s%s. Merged since:\n", installed.Version, commits(len(titles)), target.name, where)
	} else {
		wizard.say("\nInkuna %s was built from a commit that is not on %s, which is at %s", installed.Version, target.name, described)
		if len(titles) == 0 {
			wizard.say(".\n")
			return status, nil
		}
		wizard.say(", with %s the install does not have:\n", commits(len(titles)))
	}
	for index, title := range titles {
		if index == maxTitles {
			wizard.say("  and %d more\n", len(titles)-maxTitles)
			break
		}
		wizard.say("  - %s\n", title)
	}
	return status, nil
}

// checkoutReady stops, having changed nothing, when moving the checkout to
// the target could lose work: it is on a branch other than main, it has
// uncommitted changes, or it has commits the target does not. Untracked
// files do not count, as they do not in the version a build stamps. A
// checkout at a commit rather than on a branch, as one at a release is,
// goes to either target. It returns the branch the checkout is on, "HEAD"
// when it is at a commit.
func (wizard *installer) checkoutReady(source string, target updateTarget) (string, error) {
	git := wizard.git()
	moved := "fast-forwarded"
	if target.release {
		moved = "moved to " + target.name
	}
	branch, err := wizard.system.output(source, []string{git, "rev-parse", "--abbrev-ref", "HEAD"})
	if err != nil {
		return "", fmt.Errorf("read the checkout's branch: %w", err)
	}
	if branch != "main" && branch != "HEAD" {
		wizard.say("\nThe checkout %s is on %s, not main, so it is not %s. Switch it to main, then run inkunactl update again. Nothing was changed.\n", source, branch, moved)
		return "", exitError{code: 1}
	}
	changes, err := wizard.system.output(source, []string{git, "status", "--porcelain", "--untracked-files=no"})
	if err != nil {
		return "", fmt.Errorf("read the checkout's changes: %w", err)
	}
	if changes != "" {
		wizard.say("\nThe checkout %s has uncommitted changes, so it is not %s:\n%sCommit, stash, or discard them, then run inkunactl update again. Nothing was changed.\n", source, moved, indent(changes))
		return "", exitError{code: 1}
	}
	// A checkout at a commit is switched to main before the edge channel
	// fast-forwards it, so main's own commits count too.
	heads := []string{"HEAD"}
	if branch == "HEAD" && !target.release {
		heads = append(heads, "main")
	}
	for _, head := range heads {
		local, err := wizard.system.output(source, []string{git, "log", "--format=%h %s", target.ref + ".." + head})
		if err != nil {
			return "", fmt.Errorf("read the checkout's commits: %w", err)
		}
		if local != "" {
			wizard.say("\nThe checkout %s has commits that are not on %s, so it is not %s:\n%sMove them to a branch of their own, then run inkunactl update again. Nothing was changed.\n", source, target.name, moved, indent(local))
			return "", exitError{code: 1}
		}
	}
	return branch, nil
}

// describeMove says what the update does to the checkout, for the question
// before it.
func describeMove(source string, target updateTarget, branch string) string {
	switch {
	case target.release:
		return "checks " + source + " out at the release " + target.name
	case branch == "HEAD":
		return "switches " + source + " to main, fast-forwards it to " + mainBranch
	}
	return "fast-forwards " + source + " to " + mainBranch
}

// move puts the checkout at the target: detached at a release, or on main
// fast-forwarded to origin/main.
func (wizard *installer) move(source string, target updateTarget, branch string) error {
	git := wizard.git()
	var steps [][]string
	if target.release {
		wizard.say("\nCheck out %s at %s\n", source, target.name)
		steps = [][]string{{git, "checkout", "--quiet", "--detach", target.ref}}
	} else {
		wizard.say("\nFast-forward %s to %s\n", source, mainBranch)
		if branch == "HEAD" {
			steps = append(steps, []string{git, "switch", "--quiet", "main"})
		}
		steps = append(steps, []string{git, "merge", "--ff-only", mainBranch})
	}
	for _, step := range steps {
		if err := wizard.run(source, step); err != nil {
			wizard.say("  %s failed: %v. Its output above says why.\n", prereq.CommandLine(step), err)
			return exitError{code: 1}
		}
	}
	return nil
}

// rollback swaps the app and CLI kept as *.previous back in, keeping the
// ones it replaces in their place, so a second rollback undoes the first.
// The checkout is left where it is: the next update builds what the channel
// follows again.
func (wizard *installer) rollback(found installation) error {
	layout := found.layout
	unlock, err := wizard.lock(layout)
	if err != nil {
		return err
	}
	defer unlock()
	for _, target := range []install.Target{layout.App, layout.CLI} {
		if _, err := os.Lstat(target.Installed + install.PreviousSuffix); err != nil {
			wizard.say("There is no previous build to go back to: %s is not there. inkunactl install and inkunactl update keep the build they replace.\n", target.Installed+install.PreviousSuffix)
			return exitError{code: 1}
		}
	}
	if err := wizard.appStopped(layout); err != nil {
		return err
	}
	current, currentErr := wizard.versionOf("", layout.CLI.Installed)
	previousPath := install.PreviousRecordPath(found.dataDir)
	previous, _, err := install.ReadRecord(previousPath)
	if err != nil {
		return err
	}
	now, named := "the build installed now", ""
	if currentErr == nil {
		now, named = current.Version, ", "+current.Version+","
	}
	wizard.say("Roll back\n  The app and inkunactl go back to %s, kept as:\n    %s\n    %s\n  The build installed now%s is kept as the previous one, so inkunactl update --rollback again returns to it.\n  Configuration and secrets are left as they are. The database keeps any migrations the newer build ran.\n%s",
		describeBuild(previous), layout.App.Installed+install.PreviousSuffix, layout.CLI.Installed+install.PreviousSuffix, named, wizard.databaseWarning(found, describeBuild(previous)))
	if !wizard.agree("  Roll back to " + describeBuild(previous) + "?") {
		wizard.say("  Nothing was changed.\n")
		return exitError{code: 1}
	}
	// The app may have been opened while the question waited.
	if err := wizard.appStopped(layout); err != nil {
		return err
	}
	if err := install.SwapPrevious(layout.App.Installed); err != nil {
		return fmt.Errorf("roll back %s: %w", layout.App.Installed, err)
	}
	if err := install.SwapPrevious(layout.CLI.Installed); err != nil {
		// The app and CLI go back together, or not at all.
		if undo := install.SwapPrevious(layout.App.Installed); undo != nil {
			return fmt.Errorf("roll back %s: %w; putting the app back failed too: %w", layout.CLI.Installed, err, undo)
		}
		return fmt.Errorf("roll back %s: %w", layout.CLI.Installed, err)
	}
	// The CLI now in place says which build it is, whatever the record says.
	restored, err := wizard.versionOf("", layout.CLI.Installed)
	if err != nil {
		restored = version.Info{Version: previous.Version, Commit: previous.Commit}
	}
	// Each record names the checkout its build came from, which the next
	// install or update builds from.
	source := orDefault(previous.Source, found.record.Source)
	record := install.Record{Source: source, Version: restored.Version, Commit: restored.Commit, App: layout.App.Installed, CLI: layout.CLI.Installed}
	if err := install.WriteRecord(install.RecordPath(found.dataDir), record); err != nil {
		return err
	}
	if currentErr == nil {
		if err := install.WriteRecord(previousPath, install.Record{Source: found.record.Source, Version: current.Version, Commit: current.Commit, App: layout.App.Installed, CLI: layout.CLI.Installed}); err != nil {
			return err
		}
	} else if err := os.Remove(previousPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the record of the previous build: %w", err)
	}
	wizard.say("\nInkuna %s is installed again. inkunactl update brings it up to date; inkunactl update --rollback goes back to %s.\n", describeBuild(record), now)
	return nil
}

// databaseWarning says when the build a rollback restores would refuse the
// database, because the newer build ran migrations it does not know
// (storage.NewerDatabaseError), or when that cannot be told. It is empty
// when the build knows every migration the database has.
func (wizard *installer) databaseWarning(found installation, build string) string {
	path := filepath.Join(found.dataDir, "inkuna.db")
	applied, err := storage.AppliedMigrations(path)
	if err != nil {
		return fmt.Sprintf("  Which migrations the database has could not be read (%v), so whether %s opens it is not known.\n", err, build)
	}
	if len(applied) == 0 {
		return ""
	}
	output, err := wizard.system.output("", []string{found.layout.CLI.Installed + install.PreviousSuffix, "version", "--json"})
	if err != nil {
		return fmt.Sprintf("  %s did not say which database migrations it knows (%v), so whether it opens the database is not known.\n", build, err)
	}
	var report buildReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		return fmt.Sprintf("  %s did not say which database migrations it knows (%v), so whether it opens the database is not known.\n", build, err)
	}
	if report.Migration == "" {
		// A build from before the check opens any database, as it always did.
		return fmt.Sprintf("  %s is from before Inkuna checked a database's migrations: it opens the database as it is, though it may not handle what the newer build's migrations changed.\n", build)
	}
	var unknown []string
	for _, name := range applied {
		// Migration names sort in the order they apply, so the ones after
		// the newest a build knows are the ones it does not.
		if name > report.Migration {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	return fmt.Sprintf("  Warning: the database has %s, which %s does not know, so its app and inkunactl will refuse to open the database until you update again (inkunactl update).\n", strings.Join(unknown, ", "), build)
}

// describeBuild names a recorded build by its version, when it has one.
func describeBuild(record install.Record) string {
	if record.Version == "" {
		return "the previous build"
	}
	return record.Version
}

func commits(count int) string {
	if count == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", count)
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimRight(line, "\r"); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// indent lists command output under a message, one line each.
func indent(text string) string {
	var builder strings.Builder
	for _, line := range nonEmptyLines(text) {
		builder.WriteString("  " + strings.TrimSpace(line) + "\n")
	}
	return builder.String()
}
