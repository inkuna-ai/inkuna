package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/install"
	"github.com/inkuna-ai/inkuna/internal/prereq"
)

// readyMachine has everything Inkuna needs on goos, Claude Code as its
// provider.
func readyMachine(goos string) *machine {
	fake := readyMac()
	switch goos {
	case "linux":
		fake.programs["xcode-select"], fake.programs["brew"] = false, false
		fake.add("apt-get")
		fake.add("cc", "cc --version", "cc (GCC) 13.2.0")
		fake.add("pkg-config", "pkg-config --modversion gtk+-3.0", "3.24.41", "pkg-config --modversion webkit2gtk-4.1", "2.44.0")
	case "windows":
		fake.programs["xcode-select"], fake.programs["brew"] = false, false
		fake.add("winget")
		fake.add("reg", `reg query HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5} /v pv`, "    pv    REG_SZ    131.0.2903.70")
	}
	return fake
}

// fakeUserPath stands in for the Windows user PATH in the registry.
type fakeUserPath struct{ value string }

func (path *fakeUserPath) Get() (string, error) { return path.value, nil }
func (path *fakeUserPath) Set(value string) error {
	path.value = value
	return nil
}

// rig is a machine for the steps after the prerequisites, in a temporary
// directory: a home, a data directory, a clone of Inkuna the wizard runs
// in, and a build that leaves what it makes in the checkout it runs in,
// stamped with the rig's version.
type rig struct {
	t            *testing.T
	goos         string
	root         string
	home         string
	dataDir      string
	clone        string
	workingDir   string
	configExists bool
	shell        string
	zdotdir      string
	// dataDirName, when set, is the data directory as configuration gives
	// it, relative to the working directory.
	dataDirName string
	path        string
	userPath    *fakeUserPath
	version     string
	commit      string
	branch      string
	// failStep is the build step that fails, such as "task package".
	failStep  string
	setupRuns int
	// commits are the commit of each version built, so an installed
	// inkunactl reports the commit it was built from.
	commits map[string]string
	// migrations are the newest database migration each version knows, as
	// its inkunactl version --json reports it; a version without one is
	// from before builds reported it.
	migrations map[string]string
	// running is the program running, as the app the update must not
	// replace.
	running string
	// originState is the checkout's origin, for inkunactl update.
	originState originState
	opened      []string
	// shortcuts are the Start menu shortcuts written, as "path -> target".
	shortcuts []string
}

func newRig(t *testing.T, goos string) *rig {
	t.Helper()
	root := t.TempDir()
	r := &rig{
		t: t, goos: goos, root: root,
		home:         filepath.Join(root, "home"),
		dataDir:      filepath.Join(root, "data"),
		clone:        filepath.Join(root, "src", "inkuna"),
		configExists: true, shell: "/bin/zsh", path: "/usr/bin",
		version: "abc1234", commit: "abc1234def5678", branch: "main",
		commits:    map[string]string{},
		migrations: map[string]string{},
	}
	r.workingDir = filepath.Join(r.clone, "cmd", "inkunactl")
	r.makeCheckout(r.clone)
	for _, dir := range []string{r.workingDir, r.home, r.dataDir, filepath.Join(root, "Applications")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if goos == "windows" {
		r.path = `C:\Windows`
		r.userPath = &fakeUserPath{value: `%USERPROFILE%\go\bin`}
	}
	return r
}

func (r *rig) write(path, content string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) read(path string) string {
	r.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(data)
}

func (r *rig) makeCheckout(dir string) {
	r.write(filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	r.write(filepath.Join(dir, "go.mod"), "module "+inkunaModule+"\n\ngo 1.25.0\n")
	r.write(filepath.Join(dir, "build", "appicon.png"), "png")
}

func (r *rig) platform() install.Platform {
	return install.Platform{
		OS: r.goos, Home: r.home,
		LocalAppData: filepath.Join(r.home, "AppData", "Local"),
		AppData:      filepath.Join(r.home, "AppData", "Roaming"),
		Applications: filepath.Join(r.root, "Applications"),
	}
}

func (r *rig) layout() install.Layout {
	r.t.Helper()
	layout, err := install.LayoutFor(r.platform())
	if err != nil {
		r.t.Fatal(err)
	}
	return layout
}

// app is the file in an app that holds its version: the executable, inside
// the bundle on macOS.
func (r *rig) app(path string) string {
	if r.goos == "darwin" {
		return filepath.Join(path, "Contents", "MacOS", "Inkuna")
	}
	return path
}

// installed puts the rig's version in place from the clone, as an earlier
// install would have, with its directory on PATH.
func (r *rig) installed() *rig {
	r.t.Helper()
	layout := r.layout()
	r.write(r.app(layout.App.Installed), r.version)
	r.write(layout.CLI.Installed, r.version)
	r.commits[r.version] = r.commit
	record := install.Record{Source: r.clone, Version: r.version, Commit: r.commit, App: layout.App.Installed, CLI: layout.CLI.Installed}
	if err := install.WriteRecord(install.RecordPath(r.dataDir), record); err != nil {
		r.t.Fatal(err)
	}
	separator := ":"
	if r.goos == "windows" {
		separator = ";"
	}
	r.path += separator + layout.BinDir
	return r
}

func (r *rig) getenv(name string) string {
	switch name {
	case "SHELL":
		return r.shell
	case "ZDOTDIR":
		return r.zdotdir
	}
	return ""
}

func (r *rig) output(dir string, argv []string) (string, error) {
	if output, ok, err := r.origin(strings.Join(argv, " ")); ok {
		return output, err
	}
	switch line := strings.Join(argv, " "); line {
	case "git rev-parse --abbrev-ref HEAD":
		return r.branch, nil
	case "git describe --tags --always --dirty":
		return r.version, nil
	case "git rev-parse HEAD":
		return r.commit, nil
	case filepath.Join(dir, r.layout().CLI.Built) + " version --json", r.layout().CLI.Installed + " version --json", r.layout().CLI.Installed + install.PreviousSuffix + " version --json":
		if !filepath.IsAbs(argv[0]) {
			// exec resolves a relative program against the directory it
			// runs in, which is the checkout itself.
			return "", fmt.Errorf("%s: no such file or directory", line)
		}
		built, err := os.ReadFile(argv[0])
		if err != nil {
			return "", fmt.Errorf("%s: %w", line, err)
		}
		commit, ok := r.commits[string(built)]
		if !ok {
			commit = r.commit
		}
		report := map[string]string{"version": string(built), "commit": commit}
		if migration, ok := r.migrations[string(built)]; ok {
			report["migration"] = migration
		}
		data, err := json.Marshal(report)
		return string(data), err
	default:
		return "", fmt.Errorf("%s: exit status 1", line)
	}
}

// command does what a command the wizard runs would do to the machine.
func (r *rig) command(dir string, argv []string) error {
	line := strings.Join(argv, " ")
	if line == r.failStep {
		return errors.New("exit status 201")
	}
	layout := r.layout()
	switch {
	case line == "git merge --ff-only origin/main",
		r.originState.release != "" && line == "git checkout --quiet --detach refs/tags/"+r.originState.release:
		r.version, r.commit = r.originState.latestVersion, r.originState.latest
	case line == "git switch --quiet main":
		r.branch = "main"
	case line == "task package":
		r.write(r.app(filepath.Join(dir, layout.App.Built)), r.version)
	case line == "task build":
		r.write(filepath.Join(dir, layout.CLI.Built), r.version)
		r.commits[r.version] = r.commit
	case strings.HasPrefix(line, "git clone "+inkunaCloneURL+" "):
		r.makeCheckout(argv[3])
	}
	return nil
}

func (r *rig) wizard(fake *machine, answers string, effect func([]string), args ...string) wizardRun {
	r.t.Helper()
	return r.run(fake, strings.NewReader(answers), effect, args...)
}

// wizardReading runs the wizard with input that does more than answer.
func (r *rig) wizardReading(fake *machine, input io.Reader, args ...string) wizardRun {
	r.t.Helper()
	return r.run(fake, input, nil, args...)
}

func (r *rig) run(fake *machine, input io.Reader, effect func([]string), args ...string) wizardRun {
	r.t.Helper()
	var output bytes.Buffer
	var result wizardRun
	system := installSystem{
		platform:   r.platform(),
		shellPath:  r.path,
		getenv:     r.getenv,
		workingDir: r.workingDir,
		output:     r.output,
		loadConfig: func() (config.Config, error) {
			dataDir := r.dataDir
			if r.dataDirName != "" {
				dataDir = r.dataDirName
			}
			return config.Config{DataDir: dataDir, ConfigPath: filepath.Join(r.dataDir, "inkuna.yaml"), ConfigExists: r.configExists}, nil
		},
		setup: func(_ io.Reader, stdout io.Writer) error {
			r.setupRuns++
			r.configExists = true
			_, _ = fmt.Fprintln(stdout, "(setup)")
			return nil
		},
		open: func(app string) error {
			r.opened = append(r.opened, app)
			return nil
		},
		shortcut: func(layout install.Layout) error {
			r.shortcuts = append(r.shortcuts, layout.Shortcut+" -> "+layout.App.Installed)
			return nil
		},
		running: func(program string) (bool, error) {
			return r.running != "" && program == r.running, nil
		},
	}
	if r.userPath != nil {
		system.userPath = r.userPath
	}
	wizard := installer{
		probe:   fake,
		options: prereq.Options{OS: r.goos},
		run: func(dir string, argv []string) error {
			result.ran = append(result.ran, argv)
			result.dirs = append(result.dirs, dir)
			if effect != nil {
				effect(argv)
			}
			return r.command(dir, argv)
		},
		in:     bufio.NewReader(input),
		out:    &output,
		system: system,
	}
	if len(args) > 0 && args[0] == "update" {
		result.err = wizard.update(args[1:])
	} else {
		result.err = wizard.main(args)
	}
	result.output = output.String()
	return result
}

func (r *rig) record() (install.Record, bool) {
	r.t.Helper()
	record, exists, err := install.ReadRecord(install.RecordPath(r.dataDir))
	if err != nil {
		r.t.Fatal(err)
	}
	return record, exists
}

// unixPathsOnWindows reports whether a macOS or Linux layout is being tested
// on Windows, where its home has backslashes, which ShellProfile refuses to
// write into a startup file. The startup-file change is tested on macOS and
// Linux, where it runs.
func unixPathsOnWindows(goos string) bool {
	return goos != "windows" && runtime.GOOS == "windows"
}

var builds = [][]string{{"task", "deps"}, {"task", "package"}, {"task", "build"}}

func TestInstallPutsTheBuildInPlaceOnEachPlatform(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			r := newRig(t, goos)
			// Use this clone, build, put in place, change PATH, don't open.
			answers := "y\ny\ny\ny\nn\n"
			if unixPathsOnWindows(goos) {
				// There is no PATH question to answer.
				answers = "y\ny\ny\nn\n"
			}
			got := r.wizard(readyMachine(goos), answers, nil)
			if got.err != nil {
				t.Fatalf("err %v\n%s", got.err, got.output)
			}
			if !reflect.DeepEqual(got.ran, builds) || got.dirs[0] != r.clone {
				t.Fatalf("ran %v in %v", got.ran, got.dirs)
			}
			layout := r.layout()
			want := map[string]string{
				"darwin":  filepath.Join(r.root, "Applications", "Inkuna.app"),
				"linux":   filepath.Join(r.home, ".local", "bin", "inkuna"),
				"windows": filepath.Join(r.home, "AppData", "Local", "Programs", "Inkuna", "Inkuna.exe"),
			}[goos]
			if layout.App.Installed != want || r.read(r.app(want)) != r.version || r.read(layout.CLI.Installed) != r.version {
				t.Fatalf("app %s, want %s", layout.App.Installed, want)
			}
			if record, _ := r.record(); record != (install.Record{Source: r.clone, Version: r.version, Commit: r.commit, App: want, CLI: layout.CLI.Installed}) {
				t.Fatalf("record %+v", record)
			}
			for _, line := range []string{
				"Build from this clone instead of keeping a checkout in " + filepath.Join(r.dataDir, "source") + "? [y/N]",
				"[2/3] task package",
				"The app: " + want + "\n",
				"inkunactl: " + layout.CLI.Installed + "\n",
				"Installed Inkuna abc1234.",
				"Open Inkuna now? [y/N]",
				"To update Inkuna later from " + r.clone + "'s origin, run:\n  inkunactl update\n",
			} {
				if !strings.Contains(got.output, line) {
					t.Errorf("output lacks %q:\n%s", line, got.output)
				}
			}
			switch goos {
			case "windows":
				if r.userPath.value != `%USERPROFILE%\go\bin;`+layout.BinDir {
					t.Errorf("user PATH = %q", r.userPath.value)
				}
				if wantLink := []string{layout.Shortcut + " -> " + want}; !reflect.DeepEqual(r.shortcuts, wantLink) {
					t.Errorf("shortcuts %v, want %v", r.shortcuts, wantLink)
				}
			default:
				if unixPathsOnWindows(goos) {
					break
				}
				if profile := r.read(filepath.Join(r.home, ".zshrc")); !strings.Contains(profile, `export PATH="$HOME/.local/bin:$PATH"`) {
					t.Errorf(".zshrc:\n%s", profile)
				}
			}
			if goos == "linux" {
				if entry := r.read(layout.DesktopEntry); entry != install.DesktopEntry(want, layout.Icon) || r.read(layout.Icon) != "png" {
					t.Errorf("desktop entry:\n%s", entry)
				}
			}
			if len(r.opened) != 0 {
				t.Errorf("opened %v without a yes", r.opened)
			}
		})
	}
}

func TestInstallSwapsANewBuildInAndKeepsThePreviousOne(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			r := newRig(t, goos).installed()
			layout := r.layout()
			for _, next := range []string{"bcd2345", "cde3456"} {
				previous := r.version
				r.version, r.commit = next, next+"0000"
				// Build, put in place, open.
				got := r.wizard(readyMachine(goos), "y\ny\ny\n", nil)
				if got.err != nil || !reflect.DeepEqual(got.ran, builds) {
					t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
				}
				for _, target := range []install.Target{layout.App, layout.CLI} {
					installed := target.Installed
					if target == layout.App {
						if r.read(r.app(installed)) != next || r.read(r.app(installed+install.PreviousSuffix)) != previous {
							t.Fatalf("%s holds %q, previous %q", installed, r.read(r.app(installed)), r.read(r.app(installed+install.PreviousSuffix)))
						}
						continue
					}
					if r.read(installed) != next || r.read(installed+install.PreviousSuffix) != previous {
						t.Fatalf("%s holds %q, previous %q", installed, r.read(installed), r.read(installed+install.PreviousSuffix))
					}
				}
				if !strings.Contains(got.output, "It replaces the one there, which is kept as "+layout.CLI.Installed+install.PreviousSuffix+" until the next install or update, for inkunactl update --rollback.") {
					t.Fatalf("output:\n%s", got.output)
				}
				if record, _ := r.record(); record.Version != next {
					t.Fatalf("record %+v", record)
				}
				// The replaced build's version is kept for update --rollback.
				kept, exists, err := install.ReadRecord(install.PreviousRecordPath(r.dataDir))
				if err != nil || !exists || kept.Version != previous || kept.Commit != r.commits[previous] {
					t.Fatalf("previous record %+v, %v, %v; want %s", kept, exists, err, previous)
				}
			}
			if !reflect.DeepEqual(r.opened, []string{layout.App.Installed, layout.App.Installed}) {
				t.Fatalf("opened %v", r.opened)
			}
		})
	}
}

func TestInstallSecondRunFindsEverythingDone(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			r := newRig(t, goos)
			r.configExists = false
			if got := r.wizard(readyMachine(goos), "y\ny\ny\ny\nn\n", nil); got.err != nil {
				t.Fatalf("first run: %v\n%s", got.err, got.output)
			}
			// The first run's PATH change is in place for new terminals.
			got := r.wizard(readyMachine(goos), "", nil)
			if got.err != nil || len(got.ran) != 0 || strings.Contains(got.output, "[y/N]") {
				t.Fatalf("second run ran %v, err %v\n%s", got.ran, got.err, got.output)
			}
			if r.setupRuns != 1 {
				t.Fatalf("setup ran %d times", r.setupRuns)
			}
			for _, line := range []string{
				"Keeping " + filepath.Join(r.dataDir, "inkuna.yaml") + " as it is.",
				"Building from " + r.clone + ".",
				"Inkuna abc1234 from " + r.clone + " is already installed.",
			} {
				if !strings.Contains(got.output, line) {
					t.Errorf("output lacks %q:\n%s", line, got.output)
				}
			}
			if strings.Contains(got.output, "Put Inkuna in place") {
				t.Errorf("the second run put Inkuna in place again:\n%s", got.output)
			}
			// This terminal's PATH predates the change, so the run says new
			// ones have it rather than offering it again.
			if !unixPathsOnWindows(goos) && !strings.Contains(got.output, "Open a new terminal to use inkunactl.") {
				t.Errorf("output does not say the PATH change is there:\n%s", got.output)
			}
		})
	}
}

func TestInstallRebuildsACheckoutWithUncommittedChanges(t *testing.T) {
	r := newRig(t, "darwin")
	r.version += "-dirty"
	r.installed()
	got := r.wizard(readyMachine("darwin"), "y\ny\nn\n", nil)
	if got.err != nil || !reflect.DeepEqual(got.ran, builds) || !strings.Contains(got.output, "The checkout has uncommitted changes, so Inkuna is built again.") {
		t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
}

func TestInstallRunsSetupOnlyWithoutConfiguration(t *testing.T) {
	r := newRig(t, "linux").installed()
	got := r.wizard(readyMachine("linux"), "", nil)
	if got.err != nil || r.setupRuns != 0 || !strings.Contains(got.output, "Keeping "+filepath.Join(r.dataDir, "inkuna.yaml")+" as it is.") {
		t.Fatalf("setup ran %d times, err %v\n%s", r.setupRuns, got.err, got.output)
	}
	r.configExists = false
	got = r.wizard(readyMachine("linux"), "", nil)
	if got.err != nil || r.setupRuns != 1 || !strings.Contains(got.output, "There is no inkuna.yaml yet, so setup asks") || !strings.Contains(got.output, "(setup)") {
		t.Fatalf("setup ran %d times, err %v\n%s", r.setupRuns, got.err, got.output)
	}
}

func TestInstallReportsABuildThatFailsPartWay(t *testing.T) {
	r := newRig(t, "linux")
	r.failStep = "task package"
	got := r.wizard(readyMachine("linux"), "y\ny\ny\ny\n", nil)
	if exitCode(got.err) != 1 || !reflect.DeepEqual(got.ran, builds[:2]) {
		t.Fatalf("exit %d, ran %v\n%s", exitCode(got.err), got.ran, got.output)
	}
	want := "The build stopped at step 2 of 3, task package: exit status 201.\nIts output above says why. To see it again, run it in " + r.clone + ":\n  task package\nThen run inkunactl install again."
	if !strings.Contains(got.output, want) {
		t.Fatalf("output lacks %q:\n%s", want, got.output)
	}
	layout := r.layout()
	for _, path := range []string{layout.App.Installed, layout.CLI.Installed, install.RecordPath(r.dataDir)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists after a failed build", path)
		}
	}
	// The next run picks up from the build.
	r.failStep = ""
	if got := r.wizard(readyMachine("linux"), "y\ny\ny\nn\n", nil); got.err != nil || !layout.Installed() {
		t.Fatalf("rerun: %v\n%s", got.err, got.output)
	}
}

func TestInstallClonesOverHTTPSOutsideAClone(t *testing.T) {
	r := newRig(t, "darwin")
	r.workingDir = r.home
	managed := filepath.Join(r.dataDir, "source")
	got := r.wizard(readyMachine("darwin"), "y\ny\ny\ny\nn\n", nil)
	wantRan := append([][]string{{"git", "clone", "https://github.com/inkuna-ai/inkuna.git", managed}}, builds...)
	if got.err != nil || !reflect.DeepEqual(got.ran, wantRan) || got.dirs[1] != managed {
		t.Fatalf("ran %v in %v, err %v\n%s", got.ran, got.dirs, got.err, got.output)
	}
	if record, _ := r.record(); record.Source != managed {
		t.Fatalf("record %+v", record)
	}
	// From then on it builds there without asking.
	got = r.wizard(readyMachine("darwin"), "", nil)
	if got.err != nil || len(got.ran) != 0 || !strings.Contains(got.output, "Building from "+managed+".") {
		t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
}

// A new clone starts at the newest release, which the stable channel
// follows; a pre-release above it is passed over.
func TestInstallChecksANewCloneOutAtTheLatestRelease(t *testing.T) {
	r := newRig(t, "linux")
	r.workingDir = r.home
	r.originState = originState{tags: []string{"v0.3.0-rc.1", "v0.2.0", "v0.1.0"}, release: "v0.2.0", latest: "fed4321abcd0000", latestVersion: "v0.2.0"}
	managed := filepath.Join(r.dataDir, "source")
	got := r.wizard(readyMachine("linux"), "y\ny\ny\ny\nn\n", nil)
	wantRan := append([][]string{{"git", "clone", "https://github.com/inkuna-ai/inkuna.git", managed}, checkOut("v0.2.0")}, builds...)
	if got.err != nil || !reflect.DeepEqual(got.ran, wantRan) || got.dirs[1] != managed {
		t.Fatalf("ran %v in %v, err %v\n%s", got.ran, got.dirs, got.err, got.output)
	}
	if record, _ := r.record(); record.Version != "v0.2.0" || !strings.Contains(got.output, "Checked out the latest release, v0.2.0.") {
		t.Fatalf("record %+v\n%s", record, got.output)
	}
}

// A release checkout that fails takes the new clone with it, so the next run
// clones and checks out the release again instead of building main.
func TestInstallRemovesACloneItCouldNotCheckOut(t *testing.T) {
	r := newRig(t, "linux")
	r.workingDir = r.home
	r.originState = originState{tags: []string{"v0.2.0"}, release: "v0.2.0", latest: "fed4321abcd0000", latestVersion: "v0.2.0"}
	r.failStep = strings.Join(checkOut("v0.2.0"), " ")
	managed := filepath.Join(r.dataDir, "source")
	got := r.wizard(readyMachine("linux"), "y\ny\ny\ny\nn\n", nil)
	if exitCode(got.err) != 1 || !strings.Contains(got.output, "Removed the new clone, so the next run clones again.") {
		t.Fatalf("exit %d\n%s", exitCode(got.err), got.output)
	}
	if _, err := os.Stat(managed); err == nil {
		t.Fatal("the clone is still there")
	}
	r.failStep = ""
	got = r.wizard(readyMachine("linux"), "y\ny\ny\ny\nn\n", nil)
	if got.err != nil || len(got.ran) < 2 || got.ran[0][1] != "clone" || !reflect.DeepEqual(got.ran[1], checkOut("v0.2.0")) {
		t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
}

// A channel file that cannot be read stops the install before it clones.
func TestInstallReadsTheChannelBeforeCloning(t *testing.T) {
	r := newRig(t, "linux")
	r.workingDir = r.home
	r.write(install.ChannelPath(r.dataDir), `{"channel":"nightly"}`)
	got := r.wizard(readyMachine("linux"), "y\ny\ny\ny\nn\n", nil)
	if got.err == nil || !strings.Contains(got.err.Error(), install.ChannelFile) || len(got.ran) != 0 {
		t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
}

// On the edge channel, or before the first release, the clone stays on main.
func TestInstallBuildsMainWithoutARelease(t *testing.T) {
	for name, test := range map[string]struct {
		edge bool
		tags []string
		want string
	}{
		"no release yet":   {tags: []string{"v0.1.0-rc.1"}, want: "No release of Inkuna is tagged yet, so it is built from main."},
		"the edge channel": {edge: true, tags: []string{"v0.2.0"}, want: "The edge channel builds main."},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, "linux")
			r.workingDir = r.home
			r.originState.tags = test.tags
			if test.edge {
				if err := install.WriteChannel(install.ChannelPath(r.dataDir), install.ChannelEdge); err != nil {
					t.Fatal(err)
				}
			}
			got := r.wizard(readyMachine("linux"), "y\ny\ny\ny\nn\n", nil)
			if got.err != nil || len(got.ran) != 1+len(builds) || !strings.Contains(got.output, test.want) {
				t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
			}
		})
	}
}

// TestInstallClonesWithoutAGitHubSignIn declines signing in to the GitHub
// CLI: the repository is public, so the clone still runs.
func TestInstallClonesWithoutAGitHubSignIn(t *testing.T) {
	r := newRig(t, "darwin")
	r.workingDir = r.home
	fake := readyMachine("darwin")
	delete(fake.outputs, "gh auth status")
	// Decline the sign-in, agree to the clone, decline the build.
	got := r.wizard(fake, "n\ny\nn\n", nil)
	clone := []string{"git", "clone", "https://github.com/inkuna-ai/inkuna.git", filepath.Join(r.dataDir, "source")}
	if len(got.ran) == 0 || !reflect.DeepEqual(got.ran[0], clone) || strings.Contains(got.output, "private") {
		t.Fatalf("exit %d, ran %v\n%s", exitCode(got.err), got.ran, got.output)
	}
}

func TestInstallSaysWhenTheCloneIsNotOnMain(t *testing.T) {
	r := newRig(t, "darwin")
	r.branch = "feature/42-faster"
	got := r.wizard(readyMachine("darwin"), "n\n", nil)
	if !strings.Contains(got.output, "You are in a clone of inkuna-ai/inkuna at "+r.clone+", on feature/42-faster rather than main.") {
		t.Fatalf("output:\n%s", got.output)
	}
}

func TestInstallChangesNothingWithoutAYes(t *testing.T) {
	for _, answers := range []string{"", "n\n", "y\nn\n", "y\ny\nn\n"} {
		r := newRig(t, "linux")
		got := r.wizard(readyMachine("linux"), answers, nil)
		if exitCode(got.err) != 1 {
			t.Fatalf("answers %q: exit %d\n%s", answers, exitCode(got.err), got.output)
		}
		layout := r.layout()
		for _, path := range []string{layout.App.Installed, layout.CLI.Installed, layout.DesktopEntry, filepath.Join(r.home, ".zshrc")} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("answers %q: %s exists", answers, path)
			}
		}
	}
}

func TestInstallSaysHowToAddToPathWhenItCannot(t *testing.T) {
	r := newRig(t, "linux")
	r.shell = "/bin/tcsh"
	got := r.wizard(readyMachine("linux"), "y\ny\ny\nn\n", nil)
	if got.err != nil || !strings.Contains(got.output, "inkunactl does not know your shell's startup file. Add it to PATH there") {
		t.Fatalf("err %v\n%s", got.err, got.output)
	}
	if unixPathsOnWindows("darwin") {
		return
	}
	r = newRig(t, "darwin")
	got = r.wizard(readyMachine("darwin"), "y\ny\ny\nn\nn\n", nil)
	want := "To use inkunactl from any directory, add this line to " + filepath.Join(r.home, ".zshrc") + ":\n    export PATH=\"$HOME/.local/bin:$PATH\""
	if got.err != nil || !strings.Contains(got.output, want) {
		t.Fatalf("err %v, output lacks %q:\n%s", got.err, want, got.output)
	}
}

// --yes answers the questions about Inkuna's own install, for CI. It does
// not install other software, sign in, or open the app, and Inkuna is
// installed without a provider CLI, which only running tasks needs.
func TestInstallYesAnswersOnlyForInkunasOwnInstall(t *testing.T) {
	r := newRig(t, "windows")
	fake := readyMachine("windows")
	fake.programs["claude"] = false
	delete(fake.outputs, "gh auth status")
	got := r.wizard(fake, "", nil, "--yes")
	if got.err != nil || !reflect.DeepEqual(got.ran, builds) || len(r.opened) != 0 {
		t.Fatalf("ran %v, opened %v, err %v\n%s", got.ran, r.opened, got.err, got.output)
	}
	if !r.layout().Installed() || !strings.Contains(got.output, "Put them in place? [y/N] yes (--yes)") {
		t.Fatalf("output:\n%s", got.output)
	}
	for _, want := range []string{
		"Install Claude Code with npm install -g @anthropic-ai/claude-code? [y/N] \n",
		"Inkuna is installed, but before it can run a task it needs:\n  - GitHub CLI, signed in\n  - a signed-in provider CLI",
	} {
		if !strings.Contains(got.output, want) {
			t.Errorf("output lacks %q:\n%s", want, got.output)
		}
	}
}

func TestInstallReadsZshStartupFilesFromZdotdir(t *testing.T) {
	if unixPathsOnWindows("linux") {
		t.Skip("shell startup files are for macOS and Linux")
	}
	r := newRig(t, "linux")
	r.zdotdir = filepath.Join(r.home, ".config", "zsh")
	if got := r.wizard(readyMachine("linux"), "y\ny\ny\ny\nn\n", nil); got.err != nil {
		t.Fatalf("err %v\n%s", got.err, got.output)
	}
	if profile := r.read(filepath.Join(r.zdotdir, ".zshrc")); !strings.Contains(profile, `export PATH="$HOME/.local/bin:$PATH"`) {
		t.Fatalf("$ZDOTDIR/.zshrc:\n%s", profile)
	}
	if _, err := os.Stat(filepath.Join(r.home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatalf("~/.zshrc, which zsh does not read with ZDOTDIR set, was written: %v", err)
	}
}

// A relative data directory, as INKUNA_DATA_DIR=./data gives, is resolved
// once, so the build runs in the checkout and the built inkunactl is found.
func TestInstallResolvesARelativeDataDirectory(t *testing.T) {
	r := newRig(t, "darwin")
	r.workingDir = r.home
	r.dataDirName = filepath.Base(r.dataDir)
	t.Chdir(r.root)
	got := r.wizard(readyMachine("darwin"), "y\ny\ny\ny\nn\n", nil)
	if got.err != nil {
		t.Fatalf("err %v\n%s", got.err, got.output)
	}
	managed := filepath.Join(r.dataDir, "source")
	if record, _ := r.record(); !sameDir(record.Source, managed) || !filepath.IsAbs(record.Source) {
		t.Fatalf("record source %q, want %s", record.Source, managed)
	}
}

func TestInstallWaitsForAnotherInstall(t *testing.T) {
	r := newRig(t, "linux")
	unlock, err := install.Lock(r.layout().BinDir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	got := r.wizard(readyMachine("linux"), "y\ny\ny\ny\n", nil)
	if exitCode(got.err) != 1 || len(got.ran) != 0 || !strings.Contains(got.output, "Another inkunactl install or update is running.") {
		t.Fatalf("exit %d, ran %v\n%s", exitCode(got.err), got.ran, got.output)
	}
}

// Two data directories share the user's app and CLI. When another install
// replaced them, this one's record is out of date, and it builds again.
func TestInstallRebuildsWhenAnotherInstallReplacedTheBuild(t *testing.T) {
	r := newRig(t, "linux").installed()
	r.write(r.layout().CLI.Installed, "fff9999")
	got := r.wizard(readyMachine("linux"), "y\ny\nn\n", nil)
	if got.err != nil || !reflect.DeepEqual(got.ran, builds) || strings.Contains(got.output, "is already installed") {
		t.Fatalf("ran %v, err %v\n%s", got.ran, got.err, got.output)
	}
	if got := r.read(r.layout().CLI.Installed); got != r.version {
		t.Fatalf("installed %q, want %q", got, r.version)
	}
}

// Loading the configuration widens the process PATH with ~/.local/bin. The
// wizard still judges PATH as the shell gave it, so it offers to add the
// folder when new terminals would not find inkunactl.
func TestNewInstallerKeepsThePathTheShellGave(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("config.Load widens PATH only on macOS and Linux")
	}
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(config.EnvDataDir, filepath.Join(home, "data"))
	t.Setenv("PATH", "/usr/bin:/bin")
	wizard, err := newInstaller(strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !install.OnPath(os.Getenv("PATH"), bin, runtime.GOOS) {
		t.Fatalf("config.Load no longer widens PATH with %s, so this test checks nothing: %s", bin, os.Getenv("PATH"))
	}
	if got := wizard.system.shellPath; got != "/usr/bin:/bin" {
		t.Fatalf("the wizard judges PATH %q, want the shell's", got)
	}
}
