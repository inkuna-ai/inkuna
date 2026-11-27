package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/install"
	"github.com/inkuna-ai/inkuna/internal/prereq"
	"github.com/inkuna-ai/inkuna/internal/proc"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/internal/version"
)

const (
	// inkunaRepository is the repository the wizard builds from.
	inkunaRepository = "inkuna-ai/inkuna"
	// inkunaCloneURL is public, so the clone needs no GitHub sign-in.
	inkunaCloneURL = "https://github.com/" + inkunaRepository + ".git"
	// inkunaModule identifies a clone of it, whatever its remote is called.
	inkunaModule = "github.com/inkuna-ai/inkuna"
	// managedCheckout is the checkout Inkuna keeps in the data directory.
	managedCheckout = "source"
)

// buildSteps make the app and inkunactl, with the version stamped
// (Taskfile.yml's LDFLAGS).
var buildSteps = [][]string{{"task", "deps"}, {"task", "package"}, {"task", "build"}}

// installSystem is what the steps after the prerequisites need from the
// machine, so tests can run every platform's install in a temporary home.
type installSystem struct {
	platform install.Platform
	// shellPath is PATH as the shell started inkunactl with, which is what
	// says whether new terminals find inkunactl.
	shellPath  string
	getenv     func(string) string
	workingDir string
	// output runs a command in a directory and returns what it printed.
	output     func(dir string, argv []string) (string, error)
	loadConfig func() (config.Config, error)
	// setup is the setup wizard, for a machine without inkuna.yaml.
	setup func(stdin io.Reader, stdout io.Writer) error
	// userPath is the Windows user PATH; nil elsewhere.
	userPath install.UserPath
	// open starts the installed app.
	open func(app string) error
	// shortcut writes the Windows Start menu shortcut, which only Windows
	// can make.
	shortcut func(layout install.Layout) error
	// running reports whether a program is running (install.Running).
	running func(program string) (bool, error)
}

// envApplicationsDir replaces /Applications on macOS, so an install can go
// elsewhere, such as CI's temporary home.
const envApplicationsDir = "INKUNA_APPLICATIONS_DIR"

func systemInstall(shellPath string) installSystem {
	home, _ := os.UserHomeDir()
	workingDir, _ := os.Getwd()
	return installSystem{
		platform: install.Platform{
			OS:           runtime.GOOS,
			Home:         home,
			LocalAppData: os.Getenv("LOCALAPPDATA"),
			AppData:      os.Getenv("APPDATA"),
			Applications: orDefault(os.Getenv(envApplicationsDir), "/Applications"),
		},
		shellPath:  shellPath,
		getenv:     os.Getenv,
		workingDir: workingDir,
		output:     commandOutput,
		loadConfig: config.Load,
		setup: func(stdin io.Reader, stdout io.Writer) error {
			return runSetup(stdin, stdout, secrets.Keyring{})
		},
		userPath: install.SystemUserPath(),
		open:     openApp,
		shortcut: install.WriteShortcut,
		running:  install.Running,
	}
}

// installInkuna runs the steps after the prerequisites: the configuration,
// the source checkout, the build, putting the app and CLI in place, PATH,
// and the end. Each step checks what is already done, so a second run, or
// one after a failure, picks up where the last one stopped.
func (wizard *installer) installInkuna(report prereq.Report) error {
	system := wizard.system
	configuration, err := wizard.configure()
	if err != nil {
		return err
	}
	// The checkout's path is both the build's working directory and the
	// prefix of the built inkunactl, so a relative one would resolve twice.
	dataDir, err := filepath.Abs(configuration.DataDir)
	if err != nil {
		return fmt.Errorf("resolve the data directory: %w", err)
	}
	layout, err := install.LayoutFor(system.platform)
	if err != nil {
		return err
	}
	unlock, err := wizard.lock(layout)
	if err != nil {
		return err
	}
	defer unlock()
	record, _, err := install.ReadRecord(install.RecordPath(dataDir))
	if err != nil {
		return err
	}
	source, err := wizard.chooseSource(dataDir, record)
	if err != nil {
		return err
	}
	if source, err = filepath.Abs(source); err != nil {
		return fmt.Errorf("resolve the checkout: %w", err)
	}
	current, err := wizard.describe(source)
	if err != nil {
		return err
	}
	placed := false
	if wizard.upToDate(record, source, current, layout) {
		wizard.say("\nInkuna %s from %s is already installed.\n", current.Version, source)
	} else {
		if sameDir(record.Source, source) && record.Commit == current.Commit && strings.HasSuffix(current.Version, "-dirty") {
			wizard.say("\nThe checkout has uncommitted changes, so Inkuna is built again.\n")
		}
		built, err := wizard.build(source, layout)
		if err != nil {
			return err
		}
		if err := wizard.place(layout); err != nil {
			return err
		}
		if err := wizard.swapIn(layout, source, built, dataDir, record); err != nil {
			return err
		}
		wizard.say("  Installed Inkuna %s.\n", built.Version)
		placed = true
	}
	wizard.putOnPath(layout)
	wizard.finish(report, layout, source, placed)
	return nil
}

// lock takes the install lock, or stops when another install or update
// holds it.
func (wizard *installer) lock(layout install.Layout) (func(), error) {
	unlock, err := install.Lock(layout.BinDir)
	if errors.Is(err, install.ErrLocked) {
		wizard.say("\nAnother inkunactl install or update is running. Run this one again once it finishes.\n")
		return nil, exitError{code: 1}
	}
	return unlock, err
}

// configure runs setup when there is no inkuna.yaml, and leaves one that
// exists as it is. It comes before the checkout because setup chooses the
// data directory the checkout goes in.
func (wizard *installer) configure() (config.Config, error) {
	configuration, err := wizard.system.loadConfig()
	if err != nil {
		return config.Config{}, fmt.Errorf("load configuration: %w", err)
	}
	wizard.say("\nConfiguration\n")
	if configuration.ConfigExists {
		wizard.say("  Keeping %s as it is.\n", configuration.ConfigPath)
		return configuration, nil
	}
	wizard.say("  There is no inkuna.yaml yet, so setup asks for the data directory, the orchestrator, repositories, and secrets for the keychain. Enter keeps the default in brackets.\n\n")
	if err := wizard.system.setup(wizard.in, wizard.out); err != nil {
		return config.Config{}, fmt.Errorf("set up configuration: %w", err)
	}
	if configuration, err = wizard.system.loadConfig(); err != nil {
		return config.Config{}, fmt.Errorf("load the new configuration: %w", err)
	}
	return configuration, nil
}

// chooseSource picks the checkout to build from: the clone the wizard runs
// in when the user agrees, else the one the last install used, else the
// checkout Inkuna keeps in the data directory, cloned over HTTPS when it is
// not there yet.
func (wizard *installer) chooseSource(dataDir string, record install.Record) (string, error) {
	wizard.say("\nSource\n")
	managed := filepath.Join(dataDir, managedCheckout)
	current := ""
	switch {
	case record.Source != "" && isInkunaCheckout(record.Source):
		current = record.Source
	case isInkunaCheckout(managed):
		current = managed
	}
	if clone := inkunaCheckoutAbove(wizard.system.workingDir); clone != "" && !sameDir(clone, current) {
		instead := "keeping a checkout in " + managed
		if current != "" {
			instead = "building from " + current
		}
		wizard.say("  You are in a clone of %s at %s%s.\n", inkunaRepository, clone, wizard.branchNote(clone))
		if wizard.agree("  Build from this clone instead of " + instead + "?") {
			return clone, nil
		}
	}
	if current != "" {
		wizard.say("  Building from %s.\n", current)
		return current, nil
	}
	// The channel decides where the new clone starts, so a channel file
	// that cannot be read stops the install before anything is cloned.
	channel, err := install.ReadChannel(install.ChannelPath(dataDir))
	if err != nil {
		return "", err
	}
	clone := []string{wizard.git(), "clone", inkunaCloneURL, managed}
	wizard.say("  Inkuna keeps a checkout of %s to build from, in %s.\n", inkunaRepository, managed)
	if !wizard.agree("  Clone it with " + prereq.CommandLine(clone) + "?") {
		wizard.say("  Nothing was cloned. Run inkunactl install again to clone and build.\n")
		return "", exitError{code: 1}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dataDir, err)
	}
	if err := wizard.run("", clone); err != nil {
		wizard.say("  The clone failed: %v. Its output above says why.\n", err)
		return "", exitError{code: 1}
	}
	if err := wizard.checkOutRelease(channel, managed); err != nil {
		// A clone left on main would be built as it is by the next run,
		// which finds it already there, so it goes: the next run clones and
		// checks out the release again.
		if removeErr := os.RemoveAll(managed); removeErr != nil {
			wizard.say("  Could not remove the new clone %s: %v. Remove it, then run inkunactl install again.\n", managed, removeErr)
		} else {
			wizard.say("  Removed the new clone, so the next run clones again.\n")
		}
		return "", err
	}
	return managed, nil
}

// checkOutRelease puts a new clone at the newest release, which the stable
// channel follows, so the install starts where inkunactl update would take
// it. The edge channel, and stable before the first release, build main.
func (wizard *installer) checkOutRelease(channel install.Channel, clone string) error {
	if channel == install.ChannelEdge {
		wizard.say("  The edge channel builds main.\n")
		return nil
	}
	tag, err := wizard.latestRelease(clone)
	if err != nil {
		return err
	}
	if tag == "" {
		wizard.say("  No release of Inkuna is tagged yet, so it is built from main.\n")
		return nil
	}
	target := releaseTarget(tag)
	if err := wizard.run(clone, []string{wizard.git(), "checkout", "--quiet", "--detach", target.ref}); err != nil {
		wizard.say("  Checking out the release %s failed: %v. Its output above says why.\n", tag, err)
		return exitError{code: 1}
	}
	wizard.say("  Checked out the latest release, %s.\n", tag)
	return nil
}

// branchNote says when a clone is not on main, since the build then
// reports that branch's version.
func (wizard *installer) branchNote(clone string) string {
	branch, err := wizard.system.output(clone, []string{wizard.git(), "rev-parse", "--abbrev-ref", "HEAD"})
	switch {
	case err != nil || branch == "main":
		return ""
	case branch == "HEAD":
		return ", at a commit rather than main"
	}
	return ", on " + branch + " rather than main"
}

// describe is the checkout's version and commit, as the build would stamp
// them.
func (wizard *installer) describe(source string) (version.Info, error) {
	described, err := wizard.system.output(source, []string{wizard.git(), "describe", "--tags", "--always", "--dirty"})
	if err != nil {
		return version.Info{}, fmt.Errorf("read the checkout's version: %w", err)
	}
	commit, err := wizard.system.output(source, []string{wizard.git(), "rev-parse", "HEAD"})
	if err != nil {
		return version.Info{}, fmt.Errorf("read the checkout's commit: %w", err)
	}
	return version.Info{Version: described, Commit: commit}, nil
}

// upToDate reports whether the last install built this commit of this
// checkout and put it where the layout says, so nothing needs building. A
// checkout with uncommitted changes is always rebuilt, since its version
// does not say which changes. The record belongs to one data directory but
// the app and CLI are the user's, so another install may have replaced
// them: the installed inkunactl must also report this build.
func (wizard *installer) upToDate(record install.Record, source string, current version.Info, layout install.Layout) bool {
	recorded := sameDir(record.Source, source) &&
		record.Commit == current.Commit && record.Version == current.Version &&
		!strings.HasSuffix(current.Version, "-dirty") &&
		record.App == layout.App.Installed && record.CLI == layout.CLI.Installed &&
		layout.Installed()
	if !recorded {
		return false
	}
	installed, err := wizard.versionOf(source, layout.CLI.Installed)
	return err == nil && installed == current
}

// versionOf asks an inkunactl which build it is.
func (wizard *installer) versionOf(dir, cli string) (version.Info, error) {
	output, err := wizard.system.output(dir, []string{cli, "version", "--json"})
	if err != nil {
		return version.Info{}, err
	}
	var info version.Info
	if err := json.Unmarshal([]byte(output), &info); err != nil {
		return version.Info{}, fmt.Errorf("read %s's version: %w", cli, err)
	}
	return info, nil
}

// build runs the build steps in the checkout, each with its output on the
// terminal, and returns the version the build stamped.
func (wizard *installer) build(source string, layout install.Layout) (version.Info, error) {
	wizard.say("\nBuild\n  Inkuna builds the app and inkunactl in %s with %s. It takes a few minutes and shows each step's output.\n", source, buildDescription())
	if !wizard.agree("  Build now?") {
		wizard.say("  Nothing was built. Run inkunactl install again to build.\n")
		return version.Info{}, exitError{code: 1}
	}
	return wizard.runBuild(source, layout)
}

// buildDescription names the build steps in order, for a question about
// building.
func buildDescription() string {
	lines := buildLines()
	return lines[0] + ", then " + strings.Join(lines[1:], " and ")
}

func buildLines() []string {
	lines := make([]string, len(buildSteps))
	for index, step := range buildSteps {
		lines[index] = prereq.CommandLine(step)
	}
	return lines
}

// runBuild runs the build steps, which the user has agreed to.
func (wizard *installer) runBuild(source string, layout install.Layout) (version.Info, error) {
	lines := buildLines()
	for index, step := range buildSteps {
		wizard.say("\n[%d/%d] %s\n", index+1, len(buildSteps), lines[index])
		if err := wizard.run(source, step); err != nil {
			wizard.say("\nThe build stopped at step %d of %d, %s: %v.\nIts output above says why. To see it again, run it in %s:\n  %s\nThen run %s again. It picks up from here.\n",
				index+1, len(buildSteps), lines[index], err, source, lines[index], wizard.again())
			return version.Info{}, exitError{code: 1}
		}
	}
	for _, target := range []install.Target{layout.App, layout.CLI} {
		if _, err := os.Stat(filepath.Join(source, target.Built)); err != nil {
			wizard.say("\nThe build finished without making %s.\n", filepath.Join(source, target.Built))
			return version.Info{}, exitError{code: 1}
		}
	}
	// The built inkunactl says what was stamped, which is what the next run
	// compares with the checkout.
	built, err := wizard.versionOf(source, filepath.Join(source, layout.CLI.Built))
	if err != nil {
		return version.Info{}, fmt.Errorf("read the built version: %w", err)
	}
	return built, nil
}

// place says where the app and CLI go and what they replace, and asks. The
// caller puts them there (swapIn).
func (wizard *installer) place(layout install.Layout) error {
	wizard.say("\nPut Inkuna in place\n")
	wizard.describePlace(layout)
	if !wizard.agree("  Put them in place?") {
		wizard.say("  Nothing was put in place. Run inkunactl install again to install the build.\n")
		return exitError{code: 1}
	}
	return nil
}

// describePlace says where the app and CLI go, what they replace, and the
// launcher that opens the app.
func (wizard *installer) describePlace(layout install.Layout) {
	for _, target := range []install.Target{layout.App, layout.CLI} {
		wizard.say("  %s: %s\n", target.Name, target.Installed)
		if _, err := os.Lstat(target.Installed); err == nil {
			wizard.say("    It replaces the one there, which is kept as %s until the next install or update, for inkunactl update --rollback.\n", target.Installed+install.PreviousSuffix)
		}
	}
	if layout.DesktopEntry != "" {
		wizard.say("  A desktop entry, %s, with its icon, %s, lists the app in the application menu.\n", layout.DesktopEntry, layout.Icon)
	}
	if layout.Shortcut != "" {
		wizard.say("  A Start menu shortcut, %s, opens the app.\n", layout.Shortcut)
	}
}

// swapIn puts the build the user agreed to in place and records it in the
// data directory's install.json. The build it replaces is kept as
// *.previous, and its version in install.previous.json, which
// inkunactl update --rollback reads. last is the record before this build.
//
// It refuses while the app is running (appStopped), since the running app
// would carry on as the old build.
func (wizard *installer) swapIn(layout install.Layout, source string, built version.Info, dataDir string, last install.Record) error {
	if err := wizard.appStopped(layout); err != nil {
		return err
	}
	// The installed inkunactl says which build is being replaced. One that
	// cannot, such as a build from before inkunactl version, is still kept,
	// with no version on record.
	replacing, replacingErr := wizard.versionOf("", layout.CLI.Installed)
	if err := wizard.putInPlace(layout, source); err != nil {
		return err
	}
	previousPath := install.PreviousRecordPath(dataDir)
	if replacingErr == nil {
		previous := install.Record{Source: last.Source, Version: replacing.Version, Commit: replacing.Commit, App: layout.App.Installed, CLI: layout.CLI.Installed}
		if err := install.WriteRecord(previousPath, previous); err != nil {
			return err
		}
	} else if err := os.Remove(previousPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the record of an earlier previous build: %w", err)
	}
	record := install.Record{Source: source, Version: built.Version, Commit: built.Commit, App: layout.App.Installed, CLI: layout.CLI.Installed}
	return install.WriteRecord(install.RecordPath(dataDir), record)
}

// appStopped stops with exit status 1 while the app is running, before
// anything replaces it. When the processes cannot be listed it says so and
// goes on, since being unable to tell must not block every update.
func (wizard *installer) appStopped(layout install.Layout) error {
	if wizard.system.running == nil {
		return nil
	}
	running, err := wizard.system.running(layout.AppProgram())
	if err != nil {
		wizard.say("\nCould not tell whether Inkuna is running: %v. Quit it if it is.\n", err)
		return nil
	}
	if running {
		wizard.say("\nInkuna is running from %s. Quit it, then run %s again. Nothing was replaced.\n", layout.App.Installed, wizard.again())
		return exitError{code: 1}
	}
	return nil
}

// putInPlace swaps the app and CLI in, and writes the launcher.
func (wizard *installer) putInPlace(layout install.Layout, source string) error {
	for _, target := range []install.Target{layout.App, layout.CLI} {
		if _, err := install.Place(filepath.Join(source, target.Built), target.Installed); err != nil {
			return fmt.Errorf("install %s: %w", target.Installed, err)
		}
	}
	if layout.DesktopEntry != "" {
		if err := install.WriteDesktopEntry(layout, source); err != nil {
			return err
		}
	}
	if layout.Shortcut != "" {
		if err := wizard.system.shortcut(layout); err != nil {
			return err
		}
	}
	return nil
}

// putOnPath adds the CLI's directory to PATH for new terminals when the
// user agrees, and otherwise, or when it cannot, says how. It never fails
// the install: inkunactl works by its full path either way.
func (wizard *installer) putOnPath(layout install.Layout) {
	system := wizard.system
	dir := layout.BinDir
	if install.OnPath(system.shellPath, dir, layout.OS) {
		return
	}
	wizard.say("\nPATH\n")
	if layout.OS == "windows" {
		yourself := fmt.Sprintf("  To use inkunactl from any terminal, add %s to your user PATH in Settings, under System, About, Advanced system settings, Environment Variables.\n", dir)
		if system.userPath == nil {
			wizard.say("%s", yourself)
			return
		}
		current, err := system.userPath.Get()
		if err != nil {
			wizard.say("  Could not read your user PATH: %v.\n%s", err, yourself)
			return
		}
		if install.OnPath(current, dir, layout.OS) {
			wizard.say("  %s is on your user PATH. Open a new terminal to use inkunactl.\n", dir)
			return
		}
		if !wizard.agree("  Add " + dir + " to your user PATH, so new terminals find inkunactl?") {
			wizard.say("%s", yourself)
			return
		}
		if _, err := install.AddToUserPath(system.userPath, dir); err != nil {
			wizard.say("  %v.\n%s", err, yourself)
			return
		}
		wizard.say("  Added it. Open a new terminal to use inkunactl.\n")
		return
	}
	shell := install.Shell{
		OS:         layout.OS,
		Home:       system.platform.Home,
		Program:    system.getenv("SHELL"),
		ZDotDir:    system.getenv("ZDOTDIR"),
		ConfigHome: system.getenv("XDG_CONFIG_HOME"),
	}
	profile, ok := install.ShellProfile(shell, dir)
	if !ok {
		wizard.say("  %s is not on PATH, and inkunactl does not know your shell's startup file. Add it to PATH there to use inkunactl from any directory.\n", dir)
		return
	}
	yourself := fmt.Sprintf("  To use inkunactl from any directory, add this line to %s:\n    %s\n", profile.File, profile.Line)
	present, err := profile.HasLine()
	if err != nil {
		wizard.say("  %v.\n%s", err, yourself)
		return
	}
	if present {
		wizard.say("  %s puts %s on PATH. Open a new terminal to use inkunactl.\n", profile.File, dir)
		return
	}
	wizard.say("  %s is not on PATH. This line in %s puts it there for new terminals:\n    %s\n", dir, profile.File, profile.Line)
	if !wizard.agree("  Add it?") {
		wizard.say("%s", yourself)
		return
	}
	if _, err := profile.Add(); err != nil {
		wizard.say("  %v.\n%s", err, yourself)
		return
	}
	wizard.say("  Added it. Open a new terminal to use inkunactl.\n")
}

// finish says what Inkuna still needs to run tasks, offers to open the app
// it just installed, and says how to update.
func (wizard *installer) finish(report prereq.Report, layout install.Layout, source string, placed bool) {
	if missing := report.Missing(); len(missing) > 0 {
		wizard.say("\nInkuna is installed, but before it can run a task it needs:\n")
		for _, need := range missing {
			wizard.say("  - %s\n", need)
		}
		wizard.say("Run inkunactl install again to install them. It skips what is done.\n")
	}
	// Opening the app always asks, even with --yes, which is for runs with
	// no one there to see it.
	if placed && wizard.confirm("\nOpen Inkuna now?") {
		if err := wizard.system.open(layout.App.Installed); err != nil {
			wizard.say("  Could not open it: %v\n", err)
		}
	}
	wizard.say("\nTo update Inkuna later from %s's origin, run:\n  inkunactl update\n", source)
}

// agree asks a question about Inkuna's own install: which checkout to
// build from, cloning it, building, putting the build in place, and PATH.
// With --yes it answers yes itself and says so. Installing other software
// and signing in always ask (confirm), since they change more than Inkuna.
func (wizard *installer) agree(question string) bool {
	if wizard.yes {
		wizard.say("%s [y/N] yes (--yes)\n", question)
		return true
	}
	return wizard.confirm(question)
}

func (wizard *installer) git() string {
	return orDefault(wizard.options.Binaries.Git, "git")
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// inkunaCheckoutAbove is the clone of Inkuna that holds dir, or "" when dir
// is not in one. It walks up to the first directory with .git, the top of
// the working tree, and checks its go.mod names Inkuna's module, so a fork
// or a clone with another remote counts.
func inkunaCheckoutAbove(dir string) string {
	if dir == "" {
		return ""
	}
	for current := filepath.Clean(dir); ; {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			if isInkunaCheckout(current) {
				return current
			}
			return ""
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// isInkunaCheckout reports whether dir is the top of a git working tree of
// Inkuna's module. .git is a directory in a clone and a file in a worktree.
func isInkunaCheckout(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	file, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if module, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(module), `"`) == inkunaModule
		}
	}
	return false
}

// sameDir reports whether two paths name the same directory, following
// symbolic links, such as macOS's /var to /private/var.
func sameDir(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	resolve := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return filepath.Clean(path)
	}
	return resolve(left) == resolve(right)
}

// commandOutput runs a command directly, never through a shell, and returns
// its trimmed output.
func commandOutput(dir string, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := proc.Command(ctx, argv[0], argv[1:]...)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if detail, _, _ := strings.Cut(strings.TrimSpace(string(exit.Stderr)), "\n"); detail != "" {
				return "", fmt.Errorf("%s: %w: %s", prereq.CommandLine(argv), err, detail)
			}
		}
		return "", fmt.Errorf("%s: %w", prereq.CommandLine(argv), err)
	}
	return string(bytes.TrimSpace(output)), nil
}

// openApp starts the installed app without waiting for it, in its own
// process group so closing the terminal does not close it. macOS opens an
// app bundle with open.
func openApp(app string) error {
	argv := []string{app}
	if runtime.GOOS == "darwin" {
		argv = []string{"open", app}
	}
	command := exec.Command(argv[0], argv[1:]...)
	proc.Configure(command)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
