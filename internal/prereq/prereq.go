// Package prereq checks what Inkuna needs on this machine: the build
// toolchain, the platform's desktop libraries, a signed-in GitHub CLI, and
// a signed-in provider CLI. For each missing item it names the platform's
// install command. `inkunactl install` and `inkunactl doctor` (and the
// app's dependency health) read the same report, so they cannot disagree
// about what is installed.
package prereq

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	inkunaclaude "github.com/inkuna-ai/inkuna/internal/claude"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/proc"
)

// Status is what a check found for one item.
type Status string

const (
	// Present is installed, new enough, and signed in where that applies.
	Present Status = "present"
	// Missing is not installed, or not found where Inkuna looks.
	Missing Status = "missing"
	// TooOld is installed at a version below the minimum.
	TooOld Status = "too old"
	// SignedOut is installed but not signed in.
	SignedOut Status = "signed out"
	// Broken is installed, but its version could not be read.
	Broken Status = "error"
)

// Group is a heading items are shown under.
type Group string

const (
	GroupBuild    Group = "build"
	GroupDesktop  Group = "desktop"
	GroupGitHub   Group = "github"
	GroupProvider Group = "provider"
)

// DefaultTimeout bounds each command a check runs when Options sets none.
const DefaultTimeout = 15 * time.Second

// Step is one way to install a missing item: a command, a note, or both.
type Step struct {
	// Command is an argv array. It never goes through a shell.
	Command []string `json:"command,omitempty"`
	// Admin marks a command that needs administrator rights, such as
	// `sudo apt-get`. Inkuna shows it for the user to run and never runs it.
	Admin bool `json:"admin,omitempty"`
	// Note is guidance that goes with the command, or stands in for one
	// when the platform has no installer Inkuna can name, such as a
	// download page.
	Note string `json:"note,omitempty"`
	// Waits names what Command needs that is not installed yet, such as
	// "npm (it comes with Node.js)". The step cannot run until it is.
	Waits string `json:"waits,omitempty"`

	// needs is the program Command runs, as a key into waitPhrases.
	needs string
}

// Runnable reports whether Inkuna may offer to run the step: a user-level
// command whose program is installed.
func (step Step) Runnable() bool {
	return len(step.Command) > 0 && !step.Admin && step.Waits == ""
}

// Item is one prerequisite and what the check found for it.
type Item struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  Group  `json:"group"`
	Status Status `json:"status"`
	// Version is the installed version, when one was read.
	Version string `json:"version,omitempty"`
	// Minimum is the oldest version Inkuna accepts, when there is one.
	Minimum string `json:"minimum,omitempty"`
	// Required items must all be present. Provider CLIs are not required
	// one by one: Report.Missing asks for at least one.
	Required bool   `json:"required"`
	Detail   string `json:"detail"`
	// Install is how to install the item on this platform. It is set for
	// every item, so a report can say how even when nothing is missing.
	Install []Step `json:"install,omitempty"`
	// SignIn starts the CLI's own sign-in. Empty for items without one.
	SignIn []string `json:"signIn,omitempty"`
	// SignInUnchecked is set for a CLI that cannot report whether it is
	// signed in (GitHub Copilot CLI): it is present once installed.
	SignInUnchecked bool `json:"signInUnchecked,omitempty"`
}

// Report is every item checked on one platform, in a stable order: build,
// desktop, GitHub, providers.
type Report struct {
	OS    string `json:"os"`
	Items []Item `json:"items"`
}

// Missing says what is still needed, one phrase per gap, in report order.
// It is empty when Inkuna has everything it needs.
func (report Report) Missing() []string {
	var missing []string
	var providers []string
	usable := false
	for _, item := range report.Items {
		if item.Group == GroupProvider {
			providers = append(providers, item.Name)
			usable = usable || item.Status == Present
			continue
		}
		if item.Required && item.Status != Present {
			missing = append(missing, item.need())
		}
	}
	if len(providers) > 0 && !usable {
		missing = append(missing, "a signed-in provider CLI: "+oneOf(providers))
	}
	return missing
}

// Ready reports whether nothing is missing.
func (report Report) Ready() bool { return len(report.Missing()) == 0 }

// ProviderSignedIn reports whether a provider CLI is verified signed in, as
// opposed to one whose sign-in cannot be checked.
func (report Report) ProviderSignedIn() bool {
	for _, item := range report.Items {
		if item.Group == GroupProvider && item.Status == Present && !item.SignInUnchecked {
			return true
		}
	}
	return false
}

// ProviderUsable reports whether any provider CLI is present.
func (report Report) ProviderUsable() bool {
	for _, item := range report.Items {
		if item.Group == GroupProvider && item.Status == Present {
			return true
		}
	}
	return false
}

func (item Item) need() string {
	name := item.Name
	if item.Minimum != "" {
		name += " " + item.Minimum + " or later"
	}
	switch item.Status {
	case TooOld:
		return name + " (found " + item.Version + ")"
	case SignedOut:
		return name + ", signed in"
	case Broken:
		return name + " (" + item.Detail + ")"
	}
	return name
}

// Remedy is the first thing to do about an item that is not present, as one
// line: a sign-in command, an install command, or a note.
func (item Item) Remedy() string {
	if item.Status == SignedOut && len(item.SignIn) > 0 {
		return "sign in with " + CommandLine(item.SignIn)
	}
	if item.Status == Present || len(item.Install) == 0 {
		return ""
	}
	step := item.Install[0]
	switch {
	case len(step.Command) == 0:
		return step.Note
	case step.Admin:
		return "run " + CommandLine(step.Command)
	case step.Waits != "":
		return CommandLine(step.Command) + ", once " + step.Waits + " is installed"
	}
	return "install with " + CommandLine(step.Command)
}

func oneOf(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
}

// CommandLine shows an argv array as it would be typed, quoting arguments
// that contain spaces. It is for display only: commands run as argv.
func CommandLine(argv []string) string {
	shown := make([]string, len(argv))
	for index, arg := range argv {
		if arg == "" || strings.ContainsAny(arg, " \t\"'") {
			arg = strconv.Quote(arg)
		}
		shown[index] = arg
	}
	return strings.Join(shown, " ")
}

// Probe is how a check looks at the machine. System is the real one; tests
// fake it.
type Probe interface {
	// LookPath finds a program the way exec.LookPath does.
	LookPath(name string) (string, error)
	// Output runs a program found by LookPath and returns its combined
	// output. A non-zero exit is an error.
	Output(ctx context.Context, path string, args ...string) ([]byte, error)
}

// System probes this machine.
type System struct{}

// LookPath implements Probe with exec.LookPath.
func (System) LookPath(name string) (string, error) { return exec.LookPath(name) }

// Output implements Probe. The command runs directly, never through a shell.
func (System) Output(ctx context.Context, path string, args ...string) ([]byte, error) {
	return proc.Command(ctx, path, args...).CombinedOutput()
}

// Options are the inputs a check takes besides the machine itself.
type Options struct {
	// OS is a GOOS value; empty means the running one.
	OS string
	// Binaries are the program names or paths configuration chose; an
	// empty one is looked up by its usual name.
	Binaries config.Binaries
	// Timeout bounds each command; zero is DefaultTimeout.
	Timeout time.Duration
	// VersionArgs replace a provider CLI's --version, keyed by its ID
	// (codex, claude, copilot), for a configured binary that is a wrapper
	// with its own version command.
	VersionArgs map[string][]string
	// GoMinimum is the Go the source to be built needs, from the go
	// directive of its go.mod (GoDirective). inkunactl update sets it from
	// the commit it is about to build, which can need a newer Go than this
	// build knew. The check asks for the newer of it and GoMinimum.
	GoMinimum string
}

// goMinimum is the Go to ask for: GoMinimum, or the source's when newer.
func (options Options) goMinimum() string {
	if options.GoMinimum != "" && compareVersions(options.GoMinimum, GoMinimum) > 0 {
		return options.GoMinimum
	}
	return GoMinimum
}

// GoDirective is the version a go.mod's go directive names, or "" when it
// has none.
func GoDirective(goMod []byte) string {
	for _, line := range strings.Split(string(goMod), "\n") {
		// The directive is the keyword and the version, separated by any
		// space, and may be followed by a comment.
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "go" {
			return fields[1]
		}
	}
	return ""
}

// OptionsFor is what configuration decides about a check: the binaries, the
// health timeout, and the version command of each built-in runtime that
// declares one.
func OptionsFor(configuration config.Config) Options {
	options := Options{Binaries: configuration.Binaries, Timeout: configuration.HealthTimeout, VersionArgs: map[string][]string{}}
	for _, spec := range configuration.Runtimes {
		if len(spec.VersionArgs) > 0 && slices.Contains(config.BuiltInRuntimes(), spec.Name) {
			options.VersionArgs[string(spec.Name)] = spec.VersionArgs
		}
	}
	return options
}

// Check looks for every prerequisite of the platform. Items are checked
// concurrently, so the probe must be safe for concurrent use.
func Check(ctx context.Context, probe Probe, options Options) Report {
	if options.OS == "" {
		options.OS = runtime.GOOS
	}
	if options.Timeout <= 0 {
		options.Timeout = DefaultTimeout
	}
	options.Binaries = withDefaults(options.Binaries)
	platform := detectPlatform(probe, options.OS)
	requirements := catalog(platform, options.Binaries, options.VersionArgs, options.goMinimum())
	items := make([]Item, len(requirements))
	var wait sync.WaitGroup
	for index, requirement := range requirements {
		wait.Add(1)
		go func() {
			defer wait.Done()
			items[index] = requirement.check(ctx, probe, options.Timeout)
		}()
	}
	wait.Wait()
	return Report{OS: options.OS, Items: items}
}

func withDefaults(binaries config.Binaries) config.Binaries {
	fill := func(value *string, name string) {
		if strings.TrimSpace(*value) == "" {
			*value = name
		}
	}
	fill(&binaries.Git, "git")
	fill(&binaries.GitHub, "gh")
	fill(&binaries.Go, "go")
	fill(&binaries.Node, "node")
	fill(&binaries.NPM, "npm")
	fill(&binaries.Codex, "codex")
	fill(&binaries.Claude, "claude")
	fill(&binaries.Copilot, "copilot")
	return binaries
}

// detection is what looking for an item found, before its minimum version
// and sign-in are applied.
type detection struct {
	found   bool
	broken  bool
	version string
	detail  string
}

type requirement struct {
	id       string
	name     string
	group    Group
	required bool
	minimum  string
	detect   func(ctx context.Context, probe Probe) detection
	signedIn func(ctx context.Context, probe Probe) (bool, string)
	// signInUnchecked is for a CLI with no way to report its sign-in.
	signInUnchecked bool
	signIn          []string
	install         []Step
}

func (requirement requirement) check(ctx context.Context, probe Probe, timeout time.Duration) Item {
	limited := limitedProbe{Probe: probe, timeout: timeout}
	item := Item{
		ID: requirement.id, Name: requirement.name, Group: requirement.group, Minimum: requirement.minimum,
		Required: requirement.required, SignIn: requirement.signIn, SignInUnchecked: requirement.signInUnchecked,
		Install: resolveWaits(probe, requirement.install),
	}
	found := requirement.detect(ctx, limited)
	item.Version, item.Detail = found.version, found.detail
	switch {
	case !found.found:
		item.Status = Missing
		return item
	case found.broken:
		item.Status = Broken
		return item
	case requirement.minimum != "" && found.version == "":
		item.Status, item.Detail = Broken, "could not read its version: "+found.detail
		return item
	case requirement.minimum != "" && compareVersions(found.version, requirement.minimum) < 0:
		item.Status, item.Detail = TooOld, found.version+" is older than "+requirement.minimum
		return item
	}
	item.Status = Present
	switch {
	case requirement.signedIn != nil:
		signedIn, detail := requirement.signedIn(ctx, limited)
		if !signedIn {
			item.Status, item.Detail = SignedOut, detail
			return item
		}
		item.Detail = joinDetail(found.version, "signed in")
	case requirement.signInUnchecked:
		item.Detail = joinDetail(found.version, "sign-in not checked: the CLI cannot report it")
	}
	return item
}

func joinDetail(parts ...string) string {
	var kept []string
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, ", ")
}

// limitedProbe gives every command its own timeout, so one slow CLI cannot
// use up another's.
type limitedProbe struct {
	Probe
	timeout time.Duration
}

func (probe limitedProbe) Output(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, probe.timeout)
	defer cancel()
	return probe.Probe.Output(ctx, path, args...)
}

// waitPhrases say what a step's program is, for a step that waits on it.
var waitPhrases = map[string]string{
	"brew":   "Homebrew (https://brew.sh)",
	"winget": "winget (App Installer, from the Microsoft Store)",
	"npm":    "npm (it comes with Node.js)",
	"go":     "Go",
}

func resolveWaits(probe Probe, steps []Step) []Step {
	resolved := make([]Step, len(steps))
	for index, step := range steps {
		if len(step.Command) > 0 && !step.Admin {
			if _, err := probe.LookPath(step.Command[0]); err != nil {
				step.Waits = waitPhrases[step.needs]
				if step.Waits == "" {
					step.Waits = step.Command[0]
				}
			}
		}
		resolved[index] = step
	}
	return resolved
}

// tool detects a program by running it with versionArgs.
func tool(binary string, versionArgs ...string) func(context.Context, Probe) detection {
	return func(ctx context.Context, probe Probe) detection {
		path, err := probe.LookPath(binary)
		if err != nil {
			return detection{detail: binary + " was not found in PATH"}
		}
		output, err := probe.Output(ctx, path, versionArgs...)
		if err != nil {
			return detection{found: true, broken: true, detail: CommandLine(append([]string{binary}, versionArgs...)) + " failed: " + firstLine(output, err)}
		}
		version := firstVersion(string(output))
		if version == "" {
			return detection{found: true, detail: firstLine(output, nil)}
		}
		return detection{found: true, version: version, detail: version}
	}
}

// exitsZero detects a program that is installed when a command succeeds and
// reports no version, such as `xcode-select -p`.
func exitsZero(binary, missing string, args ...string) func(context.Context, Probe) detection {
	return func(ctx context.Context, probe Probe) detection {
		path, err := probe.LookPath(binary)
		if err != nil {
			return detection{detail: missing}
		}
		output, err := probe.Output(ctx, path, args...)
		if err != nil {
			return detection{detail: missing + ": " + firstLine(output, err)}
		}
		return detection{found: true, detail: firstLine(output, nil)}
	}
}

// pkgConfig detects a library by its pkg-config module. A module that is not
// installed is missing, not broken.
func pkgConfig(module string) func(context.Context, Probe) detection {
	return func(ctx context.Context, probe Probe) detection {
		path, err := probe.LookPath("pkg-config")
		if err != nil {
			return detection{detail: "pkg-config was not found in PATH, so " + module + " could not be checked"}
		}
		output, err := probe.Output(ctx, path, "--modversion", module)
		if err != nil {
			return detection{detail: "pkg-config does not know " + module}
		}
		version := firstVersion(string(output))
		return detection{found: true, version: version, detail: version}
	}
}

// webView2Keys are where the Evergreen WebView2 runtime records its version:
// per machine on 64-bit and 32-bit Windows, then per user.
var webView2Keys = []string{
	`HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`,
	`HKLM\SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`,
	`HKCU\Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`,
}

var registryVersion = regexp.MustCompile(`(?m)^\s*pv\s+REG_SZ\s+(\S+)\s*$`)

// webView2 reads the runtime's version with reg.exe, as Microsoft documents
// for detecting it, so the check is an argv command like every other.
func webView2(ctx context.Context, probe Probe) detection {
	path, err := probe.LookPath("reg")
	if err != nil {
		return detection{found: true, broken: true, detail: "reg was not found in PATH, so the WebView2 runtime could not be checked"}
	}
	for _, key := range webView2Keys {
		output, err := probe.Output(ctx, path, "query", key, "/v", "pv")
		if err != nil {
			continue
		}
		match := registryVersion.FindStringSubmatch(string(output))
		if match == nil || match[1] == "0.0.0.0" {
			continue
		}
		return detection{found: true, version: match[1], detail: match[1]}
	}
	return detection{detail: "the WebView2 runtime is not installed"}
}

// exitStatus is a sign-in check for a CLI whose status command exits zero
// only when it is signed in.
func exitStatus(binary string, args ...string) func(context.Context, Probe) (bool, string) {
	return func(ctx context.Context, probe Probe) (bool, string) {
		path, err := probe.LookPath(binary)
		if err != nil {
			return false, binary + " was not found in PATH"
		}
		if output, err := probe.Output(ctx, path, args...); err != nil {
			return false, "not signed in: " + firstLine(output, err)
		}
		return true, ""
	}
}

// claudeSignedIn reads `claude auth status` with the parser the app's
// Claude connection uses.
func claudeSignedIn(binary string) func(context.Context, Probe) (bool, string) {
	return func(ctx context.Context, probe Probe) (bool, string) {
		path, err := probe.LookPath(binary)
		if err != nil {
			return false, binary + " was not found in PATH"
		}
		status, err := inkunaclaude.AuthenticationStatus(ctx, probeRunner{probe: probe, path: path})
		if err != nil {
			return false, "could not read the sign-in: " + err.Error()
		}
		if !status.Authenticated {
			return false, "not signed in"
		}
		return true, ""
	}
}

type probeRunner struct {
	probe Probe
	path  string
}

func (runner probeRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	output, err := runner.probe.Output(ctx, runner.path, args...)
	if err != nil {
		return output, fmt.Errorf("%s: %w", CommandLine(args), err)
	}
	return output, nil
}

var versionPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)

// firstVersion is the first dotted number in a version banner:
// "go version go1.25.5 darwin/arm64" is 1.25.5, "v22.11.0" is 22.11.0.
func firstVersion(output string) string {
	return versionPattern.FindString(output)
}

func firstLine(output []byte, err error) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	line = strings.TrimSpace(line)
	if line == "" && err != nil {
		return err.Error()
	}
	return line
}

// compareVersions compares dotted versions numerically; a missing part is
// zero, so 22 equals 22.0.0.
func compareVersions(left, right string) int {
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	for index := 0; index < max(len(leftParts), len(rightParts)); index++ {
		var a, b int
		if index < len(leftParts) {
			a, _ = strconv.Atoi(leftParts[index])
		}
		if index < len(rightParts) {
			b, _ = strconv.Atoi(rightParts[index])
		}
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}
	return 0
}
