package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/prereq"
)

const installUsage = "usage: install [--check] [--yes]"

// installer is what the install wizard needs from the machine, so tests can
// fake every lookup and every command.
type installer struct {
	probe   prereq.Probe
	options prereq.Options
	// run starts a command in a directory ("" for the current one) attached
	// to the terminal, since sign-ins and package managers prompt and a
	// build shows its output.
	run func(dir string, argv []string) error
	in  *bufio.Reader
	out io.Writer
	// yes answers yes to the questions about Inkuna's own install (see
	// agree), for a run without a terminal, such as CI's.
	yes    bool
	system installSystem
	// command is the wizard running, install or update, which a step that
	// stops names as the one to run again.
	command string
}

// runInstall checks the prerequisites, offering to install what is missing
// and to sign in where needed, then builds Inkuna and puts the app and CLI
// in place. With --check it only reports the prerequisites.
func runInstall(args []string, stdin io.Reader, stdout io.Writer) error {
	wizard, err := newInstaller(stdin, stdout)
	if err != nil {
		return err
	}
	return wizard.main(args)
}

// newInstaller sets the wizard up on this machine. It keeps PATH as the
// shell gave it before loading the configuration, since config.Load widens
// the process PATH with folders such as ~/.local/bin, so that checks and the
// build find the tools there. Whether new terminals find inkunactl depends
// on the PATH the shell gives them, not the widened one.
func newInstaller(stdin io.Reader, stdout io.Writer) (*installer, error) {
	shellPath := os.Getenv("PATH")
	configuration, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}
	options := prereq.OptionsFor(configuration)
	options.OS = runtime.GOOS
	return &installer{
		probe:   prereq.System{},
		options: options,
		run:     runAttached,
		in:      bufio.NewReader(stdin),
		out:     stdout,
		system:  systemInstall(shellPath),
	}, nil
}

func (wizard *installer) main(args []string) error {
	wizard.command = "install"
	checkOnly := false
	for _, arg := range args {
		switch arg {
		case "--check":
			checkOnly = true
		case "--yes":
			wizard.yes = true
		default:
			return errors.New(installUsage)
		}
	}
	if checkOnly {
		wizard.say("Checking what Inkuna needs on %s.\n", prereq.OSName(wizard.options.OS))
		report := wizard.check()
		if report.Ready() {
			return nil
		}
		wizard.stillMissing(report, false)
		return exitError{code: 1}
	}
	report, err := wizard.prerequisites()
	if err != nil {
		return err
	}
	return wizard.installInkuna(report)
}

// prerequisites checks what Inkuna needs, offers to install what is missing
// and to sign in where needed, and checks again. It stops with exit status 1
// when the build cannot run.
func (wizard *installer) prerequisites() (prereq.Report, error) {
	wizard.say("Checking what Inkuna needs on %s.\n", prereq.OSName(wizard.options.OS))
	report := wizard.check()
	if !report.Ready() || !report.ProviderSignedIn() || signedOut(report) {
		if wizard.offerInstalls(report) {
			wizard.say("\nChecking again.\n")
			report = wizard.check()
		}
		if wizard.offerSignIns(report) {
			wizard.say("\nChecking again.\n")
			report = wizard.check()
		}
	}
	// Building needs the toolchain and the desktop libraries. The GitHub
	// CLI and a provider CLI are needed to run tasks, not to build, so
	// Inkuna is installed without them and the end of the run says they are
	// still missing.
	if !buildReady(report) {
		wizard.stillMissing(report, true)
		return report, exitError{code: 1}
	}
	if report.Ready() {
		wizard.say("\nEverything Inkuna needs is installed.\n")
	}
	return report, nil
}

func (wizard *installer) check() prereq.Report {
	report := prereq.Check(context.Background(), wizard.probe, wizard.options)
	writeReport(wizard.out, report)
	return report
}

func (wizard *installer) say(format string, args ...any) {
	_, _ = fmt.Fprintf(wizard.out, format, args...)
}

// offerInstalls goes through what is missing in report order, so a build
// tool comes before what installs with it. It says what each step does and
// runs only user-level commands the user agrees to; a command that needs
// administrator rights is shown for the user to run. It reports whether it
// ran anything.
func (wizard *installer) offerInstalls(report prereq.Report) bool {
	ran := false
	for _, item := range report.Items {
		if !needsInstall(report, item) {
			continue
		}
		wizard.say("\n%s is %s.\n", item.Name, describeStatus(item))
		for _, step := range item.Install {
			ran = wizard.offerStep(item, step) || ran
		}
	}
	return ran
}

func (wizard *installer) offerStep(item prereq.Item, step prereq.Step) bool {
	line := prereq.CommandLine(step.Command)
	switch {
	case len(step.Command) == 0:
		wizard.say("  %s\n", step.Note)
		return false
	case step.Admin:
		wizard.say("  This needs administrator rights, so run it yourself:\n    %s\n", line)
	case step.Waits != "":
		wizard.say("  It installs with %s, once %s is installed.\n", line, step.Waits)
	default:
		if step.Note != "" {
			wizard.say("  %s\n", step.Note)
		}
		if !wizard.confirm("  Install " + item.Name + " with " + line + "?") {
			return false
		}
		if err := wizard.run("", step.Command); err != nil {
			wizard.say("  %s failed: %v\n", line, err)
		}
		return true
	}
	if step.Note != "" {
		wizard.say("  %s\n", step.Note)
	}
	return false
}

// offerSignIns starts the sign-in of each installed CLI that is signed out,
// when the user agrees. GitHub Copilot CLI cannot report its sign-in, so it
// is offered only when no other provider is known to be signed in.
func (wizard *installer) offerSignIns(report prereq.Report) bool {
	ran := false
	for _, item := range report.Items {
		offer := item.Status == prereq.SignedOut
		prompt := "Sign in to " + item.Name + " with " + prereq.CommandLine(item.SignIn) + "?"
		if item.SignInUnchecked && item.Status == prereq.Present && !report.ProviderSignedIn() {
			offer = true
			prompt = item.Name + " cannot report whether it is signed in. Sign in with " + prereq.CommandLine(item.SignIn) + "?"
		}
		if !offer || len(item.SignIn) == 0 {
			continue
		}
		wizard.say("\n")
		if !wizard.confirm(prompt) {
			continue
		}
		if err := wizard.run("", item.SignIn); err != nil {
			wizard.say("  %s failed: %v\n", prereq.CommandLine(item.SignIn), err)
		}
		ran = true
	}
	return ran
}

// needsInstall reports whether to offer an item's install. Provider CLIs
// are alternatives: once one is usable the others are left alone.
func needsInstall(report prereq.Report, item prereq.Item) bool {
	switch item.Status {
	case prereq.Present, prereq.SignedOut:
		return false
	}
	if item.Group == prereq.GroupProvider {
		return !report.ProviderUsable()
	}
	return item.Required
}

// buildReady reports whether everything the build needs is present: every
// required item but the GitHub CLI and the provider CLIs.
func buildReady(report prereq.Report) bool {
	for _, item := range report.Items {
		switch item.Group {
		case prereq.GroupBuild, prereq.GroupDesktop:
			if item.Required && item.Status != prereq.Present {
				return false
			}
		}
	}
	return true
}

func signedOut(report prereq.Report) bool {
	for _, item := range report.Items {
		if item.Status == prereq.SignedOut {
			return true
		}
	}
	return false
}

// confirm asks a yes-or-no question. Anything but yes, including the end of
// input, is no, so nothing runs without an answer.
func (wizard *installer) confirm(question string) bool {
	wizard.say("%s [y/N] ", question)
	line, err := wizard.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	if errors.Is(err, io.EOF) && line == "" {
		wizard.say("\n")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// again is the command a step that stops says to run again.
func (wizard *installer) again() string {
	return "inkunactl " + orDefault(wizard.command, "install")
}

func (wizard *installer) stillMissing(report prereq.Report, again bool) {
	wizard.say("\nStill missing:\n")
	for _, need := range report.Missing() {
		wizard.say("  - %s\n", need)
	}
	if again {
		wizard.say("\nInstall these, then run %s again. It picks up from here.\n", wizard.again())
	}
}

var groupTitles = []struct {
	group prereq.Group
	title string
}{
	{prereq.GroupBuild, "To build"},
	{prereq.GroupDesktop, "Desktop"},
	{prereq.GroupGitHub, "GitHub"},
	{prereq.GroupProvider, "Provider CLIs (at least one, signed in)"},
}

// writeReport prints each item under its heading: its status, its version
// or what is wrong, and what to do about it.
func writeReport(out io.Writer, report prereq.Report) {
	for _, heading := range groupTitles {
		printed := false
		for _, item := range report.Items {
			if item.Group != heading.group {
				continue
			}
			if !printed {
				_, _ = fmt.Fprintf(out, "\n%s\n", heading.title)
				printed = true
			}
			_, _ = fmt.Fprintf(out, "  %-11s %s", item.Status, item.Name)
			if detail := reportDetail(item); detail != "" {
				_, _ = fmt.Fprintf(out, ": %s", detail)
			}
			_, _ = fmt.Fprintln(out)
			if remedy := item.Remedy(); remedy != "" {
				_, _ = fmt.Fprintf(out, "  %-11s %s\n", "", remedy)
			}
		}
	}
}

func reportDetail(item prereq.Item) string {
	if item.Status == prereq.Missing && item.Minimum != "" {
		return "needs " + item.Minimum + " or later"
	}
	return item.Detail
}

func describeStatus(item prereq.Item) string {
	switch item.Status {
	case prereq.TooOld:
		return "too old (" + item.Detail + ")"
	case prereq.Broken:
		return "not working (" + item.Detail + ")"
	}
	return "missing"
}

// runAttached runs a command directly, never through a shell, with the
// terminal as its input and output, so a sign-in or package manager can
// prompt and a build shows its progress.
func runAttached(dir string, argv []string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("find %s: %w", argv[0], err)
	}
	command := exec.Command(path, argv[1:]...)
	command.Dir = dir
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	return command.Run()
}
