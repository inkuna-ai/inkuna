package prereq

import "github.com/inkuna-ai/inkuna/internal/config"

// Minimum versions. GoMinimum follows the go directive in go.mod, which a
// test holds it to.
const (
	GoMinimum   = "1.25"
	NodeMinimum = "22"
	// TaskMinimum is the Taskfile schema version Inkuna's Taskfile uses.
	TaskMinimum = "3"
)

// platform is a GOOS plus, on Linux, the system package manager found on
// PATH, which decides the install commands shown.
type platform struct {
	os string
	// packages is apt-get, dnf, pacman, or empty on Linux.
	packages string
}

// linuxPackageManagers are tried in order. A machine with none gets notes
// that name the packages instead of commands.
var linuxPackageManagers = []string{"apt-get", "dnf", "pacman"}

func detectPlatform(probe Probe, goos string) platform {
	detected := platform{os: goos}
	if goos != "linux" {
		return detected
	}
	for _, manager := range linuxPackageManagers {
		if _, err := probe.LookPath(manager); err == nil {
			detected.packages = manager
			break
		}
	}
	return detected
}

// OSName is how a GOOS value is shown to people.
func OSName(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	}
	return goos
}

// catalog lists the requirements of a platform in the order they are shown.
func catalog(on platform, binaries config.Binaries, versionArgs map[string][]string, goMinimum string) []requirement {
	providerVersion := func(id string) []string {
		if args := versionArgs[id]; len(args) > 0 {
			return args
		}
		return []string{"--version"}
	}
	requirements := []requirement{
		{id: "git", name: "Git", group: GroupBuild, required: true,
			detect: tool(binaries.Git, "--version"), install: gitInstall(on)},
		{id: "go", name: "Go", group: GroupBuild, required: true, minimum: goMinimum,
			detect: tool(binaries.Go, "version"), install: goInstall(on, goMinimum)},
		{id: "node", name: "Node.js", group: GroupBuild, required: true, minimum: NodeMinimum,
			detect: tool(binaries.Node, "--version"), install: nodeInstall(on)},
		// npm ships with Node.js, so it installs the same way.
		{id: "npm", name: "npm", group: GroupBuild, required: true,
			detect: tool(binaries.NPM, "--version"), install: nodeInstall(on)},
		{id: "task", name: "Task", group: GroupBuild, required: true, minimum: TaskMinimum,
			detect: tool("task", "--version"), install: taskInstall(on, binaries)},
	}
	requirements = append(requirements, desktop(on)...)
	requirements = append(requirements,
		requirement{id: "gh", name: "GitHub CLI", group: GroupGitHub, required: true,
			detect: tool(binaries.GitHub, "--version"), install: githubInstall(on),
			signedIn: exitStatus(binaries.GitHub, "auth", "status"), signIn: []string{binaries.GitHub, "auth", "login"}},
		requirement{id: "codex", name: "Codex CLI", group: GroupProvider,
			detect: tool(binaries.Codex, providerVersion("codex")...), install: npmInstall(binaries, "@openai/codex"),
			signedIn: exitStatus(binaries.Codex, "login", "status"), signIn: []string{binaries.Codex, "login"}},
		requirement{id: "claude", name: "Claude Code", group: GroupProvider,
			detect: tool(binaries.Claude, providerVersion("claude")...), install: npmInstall(binaries, "@anthropic-ai/claude-code"),
			signedIn: claudeSignedIn(binaries.Claude), signIn: []string{binaries.Claude, "auth", "login"}},
		// Copilot CLI has no status command; it finds its token in the
		// system credential store or the environment when it starts.
		requirement{id: "copilot", name: "GitHub Copilot CLI", group: GroupProvider,
			detect: tool(binaries.Copilot, providerVersion("copilot")...), install: npmInstall(binaries, "@github/copilot"),
			signInUnchecked: true, signIn: []string{binaries.Copilot, "login"}},
	)
	return requirements
}

// desktop is what the Wails desktop build needs on each platform.
func desktop(on platform) []requirement {
	switch on.os {
	case "darwin":
		return []requirement{{id: "xcode", name: "Xcode Command Line Tools", group: GroupDesktop, required: true,
			detect:  exitsZero("xcode-select", "the Xcode Command Line Tools are not installed", "-p"),
			install: []Step{user("xcode-select", "xcode-select", "--install")}}}
	case "linux":
		return []requirement{
			{id: "cc", name: "C compiler", group: GroupDesktop, required: true,
				detect: tool("cc", "--version"),
				install: linuxPackages(on, "a C compiler",
					[]string{"build-essential"}, []string{"gcc"}, []string{"base-devel"})},
			{id: "gtk", name: "GTK 3", group: GroupDesktop, required: true,
				detect: pkgConfig("gtk+-3.0"),
				install: linuxPackages(on, "the GTK 3 development files and pkg-config",
					[]string{"pkg-config", "libgtk-3-dev"}, []string{"pkgconf-pkg-config", "gtk3-devel"}, []string{"pkgconf", "gtk3"})},
			{id: "webkitgtk", name: "WebKitGTK 4.1", group: GroupDesktop, required: true,
				detect: pkgConfig("webkit2gtk-4.1"),
				install: linuxPackages(on, "the WebKitGTK 4.1 development files",
					[]string{"libwebkit2gtk-4.1-dev"}, []string{"webkit2gtk4.1-devel"}, []string{"webkit2gtk-4.1"})},
		}
	case "windows":
		return []requirement{{id: "webview2", name: "WebView2 runtime", group: GroupDesktop, required: true,
			detect: webView2, install: []Step{winget("Microsoft.EdgeWebView2Runtime")}}}
	}
	return nil
}

func gitInstall(on platform) []Step {
	switch on.os {
	case "darwin":
		return []Step{brew("git")}
	case "windows":
		return []Step{winget("Git.Git")}
	}
	return linuxPackages(on, "Git", []string{"git"}, []string{"git"}, []string{"git"})
}

func goInstall(on platform, minimum string) []Step {
	switch on.os {
	case "darwin":
		return []Step{brew("go")}
	case "windows":
		return []Step{winget("GoLang.Go")}
	}
	// Distribution packages usually lag the Go release Inkuna needs.
	return []Step{{Note: "Install Go " + minimum + " or later from https://go.dev/doc/install; distribution packages are often older."}}
}

func nodeInstall(on platform) []Step {
	switch on.os {
	case "darwin":
		return []Step{brew("node")}
	case "windows":
		return []Step{winget("OpenJS.NodeJS.LTS")}
	}
	return []Step{{Note: "Install Node.js " + NodeMinimum + " or later, with npm, from https://nodejs.org/en/download (nvm or fnm keep it in your home directory); distribution packages are often older."}}
}

func taskInstall(on platform, binaries config.Binaries) []Step {
	switch on.os {
	case "darwin":
		return []Step{brew("go-task")}
	case "windows":
		return []Step{winget("Task.Task")}
	}
	step := user("go", binaries.Go, "install", "github.com/go-task/task/v3/cmd/task@latest")
	step.Note = "This puts task in $(go env GOPATH)/bin, which must be on PATH."
	return []Step{step}
}

func githubInstall(on platform) []Step {
	switch on.os {
	case "darwin":
		return []Step{brew("gh")}
	case "windows":
		return []Step{winget("GitHub.cli")}
	}
	const guide = "https://github.com/cli/cli/blob/trunk/docs/install_linux.md"
	switch on.packages {
	case "dnf":
		return []Step{admin("sudo", "dnf", "install", "-y", "gh")}
	case "pacman":
		return []Step{admin("sudo", "pacman", "-S", "--needed", "github-cli")}
	}
	// Debian and Ubuntu need GitHub's own apt repository for a current gh.
	return []Step{{Note: "Install GitHub CLI by following " + guide + "."}}
}

func npmInstall(binaries config.Binaries, pkg string) []Step {
	return []Step{user("npm", binaries.NPM, "install", "-g", pkg)}
}

// linuxPackages is the install command for the package manager found, or a
// note naming what to install when there is none Inkuna knows.
func linuxPackages(on platform, what string, apt, dnf, pacman []string) []Step {
	switch on.packages {
	case "apt-get":
		return []Step{admin(append([]string{"sudo", "apt-get", "install", "-y"}, apt...)...)}
	case "dnf":
		return []Step{admin(append([]string{"sudo", "dnf", "install", "-y"}, dnf...)...)}
	case "pacman":
		return []Step{admin(append([]string{"sudo", "pacman", "-S", "--needed"}, pacman...)...)}
	}
	return []Step{{Note: "Install " + what + " with your distribution's package manager."}}
}

func brew(formula string) Step { return user("brew", "brew", "install", formula) }

func winget(id string) Step {
	return user("winget", "winget", "install", "--id", id, "--exact", "--source", "winget")
}

// user is a user-level command Inkuna may run once the user agrees. needs
// names its program for the phrase shown while it is not installed.
func user(needs string, argv ...string) Step { return Step{Command: argv, needs: needs} }

// admin is a command that needs administrator rights: shown, never run.
func admin(argv ...string) Step { return Step{Command: argv, Admin: true} }
