package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// EnsureSearchPath widens PATH with the directories developer tools are
// usually installed in. A desktop app launched from Finder or a launcher
// inherits launchd's minimal PATH, which hides Homebrew, npm, and native
// installs of gh, claude, codex, copilot, and node. Only directories that
// exist are added, existing entries keep their order, and nothing is
// prepended, so an explicit PATH always wins. The updated PATH is returned.
// On Windows PATH is left alone: an app started from the Start menu gets
// the user's whole PATH from the registry, and every candidate is a Unix
// directory.
func EnsureSearchPath() string {
	current := os.Getenv("PATH")
	if runtime.GOOS == "windows" {
		return current
	}
	home, _ := os.UserHomeDir()
	updated := widenSearchPath(current, searchPathCandidates(home, os.Getenv), isDirectory)
	if updated != current {
		_ = os.Setenv("PATH", updated)
	}
	return updated
}

func widenSearchPath(current string, candidates []string, exists func(string) bool) string {
	entries := filepath.SplitList(current)
	seen := make(map[string]bool, len(entries)+len(candidates))
	for _, entry := range entries {
		seen[filepath.Clean(entry)] = true
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if candidate == "." || seen[candidate] || !exists(candidate) {
			continue
		}
		seen[candidate] = true
		entries = append(entries, candidate)
	}
	return strings.Join(entries, string(filepath.ListSeparator))
}

// searchPathCandidates lists the directories worth probing, most specific
// first. Version managers that keep one directory per tool version (nvm,
// fnm) contribute the directory of their default version so the worker
// sees the same node the user's shell does. They come before Homebrew and
// /usr/local because a shell that sets one up puts it ahead of them too:
// probed after, a Homebrew node that the shell never runs would shadow the
// managed one, and a project's gates would run on a version it does not pin.
func searchPathCandidates(home string, environ func(string) string) []string {
	var candidates []string
	// A shell runs an nvm version only when nvm's default alias names an
	// installed one; otherwise ("system", no default, or a version that is not
	// installed) it runs the node outside nvm, so nvm's newest version is
	// only a last resort, after every other candidate.
	var nvmLastResort string
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".local", "share", "mise", "shims"),
			filepath.Join(home, ".local", "share", "fnm", "aliases", "default", "bin"),
			filepath.Join(home, "Library", "Application Support", "fnm", "aliases", "default", "bin"),
		)
		nvmDir := strings.TrimSpace(environ("NVM_DIR"))
		if nvmDir == "" {
			nvmDir = filepath.Join(home, ".nvm")
		}
		if node, resolved := nvmDefaultBin(nvmDir); resolved {
			candidates = append(candidates, node)
		} else {
			nvmLastResort = node
		}
	}
	candidates = append(candidates,
		"/opt/homebrew/bin", "/opt/homebrew/sbin",
		"/usr/local/bin", "/usr/local/sbin",
		"/usr/local/go/bin",
		"/home/linuxbrew/.linuxbrew/bin",
	)
	// GOPATH is a list; go install writes to the first entry's bin.
	if entries := filepath.SplitList(strings.TrimSpace(environ("GOPATH"))); len(entries) > 0 && entries[0] != "" {
		candidates = append(candidates, filepath.Join(entries[0], "bin"))
	}
	if home == "" {
		return candidates
	}
	candidates = append(candidates,
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".claude", "local"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, "bin"),
	)
	if nvmLastResort != "" {
		candidates = append(candidates, nvmLastResort)
	}
	return candidates
}

// nvmDefaultBin resolves nvm's default alias to an installed version's bin
// directory. An alias is a version prefix ("24", "v24.4.1"), the newest
// installed version ("node", "stable"), or the name of another alias file
// under $NVM_DIR/alias, such as a custom alias or "lts/*" -> "lts/jod" ->
// "v22.12.0". A default that names no installed version ("system", the node
// outside nvm, a missing alias, or a version that is not installed) falls
// back to the newest installed version, and resolved is false so the caller
// can rank that fallback behind the system's own node.
func nvmDefaultBin(nvmDir string) (bin string, resolved bool) {
	versionsDir := filepath.Join(nvmDir, "versions", "node")
	entries, err := os.ReadDir(versionsDir)
	if err != nil || len(entries) == 0 {
		return "", false
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "v") {
			versions = append(versions, entry.Name())
		}
	}
	if len(versions) == 0 {
		return "", false
	}
	sort.Slice(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) > 0 })
	version := resolveNvmAlias(filepath.Join(nvmDir, "alias"), "default", versions)
	if version == "" {
		return filepath.Join(versionsDir, versions[0], "bin"), false
	}
	return filepath.Join(versionsDir, version, "bin"), true
}

// maxNvmAliasDepth bounds alias chains so a cycle cannot loop forever.
const maxNvmAliasDepth = 8

// resolveNvmAlias follows the alias file chain starting at name and returns
// the matching installed version, or "" when the chain does not resolve.
// versions must be sorted newest first.
func resolveNvmAlias(aliasDir, name string, versions []string) string {
	for depth := 0; depth < maxNvmAliasDepth; depth++ {
		path := filepath.Join(aliasDir, filepath.FromSlash(name))
		if relative, err := filepath.Rel(aliasDir, path); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ""
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		target := strings.TrimSpace(string(raw))
		switch target {
		case "", "system":
			// "system" is the node outside nvm, not an installed version.
			return ""
		case "node", "stable":
			return versions[0]
		}
		if prefix := strings.TrimPrefix(target, "v"); prefix != "" && prefix[0] >= '0' && prefix[0] <= '9' {
			for _, version := range versions {
				if trimmed := strings.TrimPrefix(version, "v"); trimmed == prefix || strings.HasPrefix(trimmed, prefix+".") {
					return version
				}
			}
			return ""
		}
		name = target
	}
	return ""
}

// compareVersions orders "vMAJOR.MINOR.PATCH" strings numerically.
func compareVersions(a, b string) int {
	left := strings.Split(strings.TrimPrefix(a, "v"), ".")
	right := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for index := 0; index < len(left) || index < len(right); index++ {
		var l, r int
		if index < len(left) {
			l = atoi(left[index])
		}
		if index < len(right) {
			r = atoi(right[index])
		}
		if l != r {
			if l < r {
				return -1
			}
			return 1
		}
	}
	return 0
}

func atoi(value string) int {
	total := 0
	for _, character := range value {
		if character < '0' || character > '9' {
			break
		}
		total = total*10 + int(character-'0')
	}
	return total
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
