package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Profile struct {
	Root      string   `json:"root"`
	Branch    string   `json:"branch"`
	Head      string   `json:"head"`
	Dirty     bool     `json:"dirty"`
	Languages []string `json:"languages"`
	Manifests []string `json:"manifests"`
}

func Inspect(ctx context.Context, directory string) (Profile, error) {
	root, err := git(ctx, directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return Profile{}, err
	}
	// git prints the path with forward slashes on Windows too; the root is
	// compared with paths Go builds, so it takes the platform's form.
	root = filepath.Clean(root)
	branch, _ := git(ctx, root, "branch", "--show-current")
	head, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return Profile{}, err
	}
	status, err := git(ctx, root, "status", "--porcelain=v1")
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{Root: root, Branch: branch, Head: head, Dirty: status != ""}
	manifestSet := map[string]bool{}
	languageSet := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		switch entry.Name() {
		case "go.mod", "package.json", "pyproject.toml", "Cargo.toml", "Taskfile.yml", "Makefile":
			manifestSet[filepath.ToSlash(relative)] = true
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".go":
			languageSet["Go"] = true
		case ".ts", ".tsx", ".js", ".jsx":
			languageSet["TypeScript/JavaScript"] = true
		case ".py":
			languageSet["Python"] = true
		case ".rs":
			languageSet["Rust"] = true
		}
		return nil
	})
	if err != nil {
		return Profile{}, err
	}
	for value := range manifestSet {
		profile.Manifests = append(profile.Manifests, value)
	}
	for value := range languageSet {
		profile.Languages = append(profile.Languages, value)
	}
	sort.Strings(profile.Manifests)
	sort.Strings(profile.Languages)
	return profile, nil
}

func (profile Profile) Compact() ([]byte, error) { return json.Marshal(profile) }

func git(ctx context.Context, directory string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output)), nil
}
