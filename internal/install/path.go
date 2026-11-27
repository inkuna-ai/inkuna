package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// OnPath reports whether dir is one of the entries in a PATH value. It
// takes the platform's OS rather than the running one, so every platform's
// rules are testable anywhere: Windows separates entries with ";" and
// compares them without regard to case.
func OnPath(value, dir, goos string) bool {
	separator := ":"
	if goos == "windows" {
		separator = ";"
	}
	want := trimDir(dir)
	for _, entry := range strings.Split(value, separator) {
		entry = trimDir(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == want || (goos == "windows" && strings.EqualFold(entry, want)) {
			return true
		}
	}
	return false
}

func trimDir(dir string) string {
	trimmed := strings.TrimRight(dir, `/\`)
	if trimmed == "" {
		return dir
	}
	return trimmed
}

// Profile is a shell startup file and the line that puts a directory on
// PATH in it.
type Profile struct {
	File string
	Line string
}

// Shell is the user's shell and what decides where it reads its startup
// files.
type Shell struct {
	// OS is a GOOS value.
	OS   string
	Home string
	// Program is $SHELL.
	Program string
	// ZDotDir is $ZDOTDIR, where zsh reads its startup files when set.
	ZDotDir string
	// ConfigHome is $XDG_CONFIG_HOME, where fish reads its configuration
	// when set.
	ConfigHome string
}

// bashLoginFiles are the startup files a bash login shell reads, in its
// order. It reads only the first that exists.
var bashLoginFiles = []string{".bash_profile", ".bash_login", ".profile"}

// ShellProfile says which startup file of the user's shell puts dir on PATH
// for new terminals, and the line to add:
//
//   - zsh: .zshrc in $ZDOTDIR, or in the home directory.
//   - bash on Linux: .bashrc.
//   - bash on macOS, where Terminal starts login shells: the first of
//     .bash_profile, .bash_login, and .profile that exists, since bash reads
//     only that one, so a new .bash_profile would hide the user's .profile.
//     .bash_profile when there is none.
//   - fish: a conf.d file under $XDG_CONFIG_HOME, or ~/.config.
//
// ok is false for another shell, or a directory whose name the shell would
// expand, so the wizard says how instead of guessing.
func ShellProfile(shell Shell, dir string) (profile Profile, ok bool) {
	if strings.ContainsAny(dir, "\"$`\\\n") {
		return Profile{}, false
	}
	home := shell.Home
	// $HOME keeps the line right if the home directory moves.
	named := dir
	if rest, under := strings.CutPrefix(dir, home+"/"); under && home != "" {
		named = "$HOME/" + rest
	}
	export := `export PATH="` + named + `:$PATH"`
	switch filepath.Base(shell.Program) {
	case "zsh":
		return Profile{File: filepath.Join(orHome(shell.ZDotDir, home), ".zshrc"), Line: export}, true
	case "bash":
		if shell.OS != "darwin" {
			return Profile{File: filepath.Join(home, ".bashrc"), Line: export}, true
		}
		file := filepath.Join(home, bashLoginFiles[0])
		for _, name := range bashLoginFiles {
			if _, err := os.Stat(filepath.Join(home, name)); err == nil {
				file = filepath.Join(home, name)
				break
			}
		}
		return Profile{File: file, Line: export}, true
	case "fish":
		config := orHome(shell.ConfigHome, filepath.Join(home, ".config"))
		return Profile{File: filepath.Join(config, "fish", "conf.d", "inkuna.fish"), Line: `fish_add_path --global "` + named + `"`}, true
	}
	return Profile{}, false
}

// orHome is dir when the environment set it, else fallback.
func orHome(dir, fallback string) string {
	if strings.TrimSpace(dir) == "" {
		return fallback
	}
	return dir
}

// profileComment goes above the line so a reader of the startup file knows
// where it came from.
const profileComment = "# Added by inkunactl install"

// HasLine reports whether the profile's file already holds its line.
func (profile Profile) HasLine() (bool, error) {
	data, err := os.ReadFile(profile.File)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", profile.File, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == profile.Line {
			return true, nil
		}
	}
	return false, nil
}

// Add appends the line to the profile's file, creating it if needed, and
// reports whether it did; a file that already holds the line is left alone.
// It appends rather than replacing the file through a rename, because a
// startup file is often a symbolic link into a dotfiles repository, which a
// rename would replace with a plain file.
func (profile Profile) Add() (bool, error) {
	present, err := profile.HasLine()
	if err != nil || present {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(profile.File), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", filepath.Dir(profile.File), err)
	}
	// A blank line sets the addition apart from what is already there.
	prefix := ""
	if data, err := os.ReadFile(profile.File); err == nil && len(data) > 0 {
		prefix = "\n"
		if !strings.HasSuffix(string(data), "\n") {
			prefix = "\n\n"
		}
	}
	file, err := os.OpenFile(profile.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", profile.File, err)
	}
	if _, err := file.WriteString(prefix + profileComment + "\n" + profile.Line + "\n"); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("write %s: %w", profile.File, err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("write %s: %w", profile.File, err)
	}
	return true, nil
}

// UserPath is the Windows user PATH that new terminals start with, the
// Path value under HKEY_CURRENT_USER\Environment.
type UserPath interface {
	Get() (string, error)
	Set(value string) error
}

// AddToUserPath appends dir to the user PATH and reports whether it did; a
// PATH that already has it is left alone.
func AddToUserPath(path UserPath, dir string) (bool, error) {
	current, err := path.Get()
	if err != nil {
		return false, fmt.Errorf("read the user PATH: %w", err)
	}
	if OnPath(current, dir, "windows") {
		return false, nil
	}
	next := dir
	if trimmed := strings.TrimRight(current, ";"); trimmed != "" {
		next = trimmed + ";" + dir
	}
	if err := path.Set(next); err != nil {
		return false, fmt.Errorf("write the user PATH: %w", err)
	}
	return true, nil
}
