package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ignoreLine is what IgnoreOut adds to a repository's .gitignore.
const ignoreLine = OutDir + "/"

// IgnoreOut makes the repository's own .gitignore ignore OutDir, so the
// files Inkuna and its agents leave there stay out of every clone's git,
// not only out of Inkuna's commits. It reports whether it changed the file:
// nothing changes when a .gitignore in the repository already ignores it.
// An ignore that lives outside the repository, in a global excludes file
// or .git/info/exclude, does not count, since a clone does not get it.
func IgnoreOut(ctx context.Context, path string) (string, bool, error) {
	root, err := git(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false, fmt.Errorf("find the repository: %w", err)
	}
	// git prints forward slashes on Windows too.
	root = filepath.Clean(root)
	file := filepath.Join(root, ".gitignore")
	if source, err := ignoredBy(ctx, root); err != nil {
		return file, false, err
	} else if source != "" && !filepath.IsAbs(source) && !strings.HasPrefix(filepath.ToSlash(source), ".git/") {
		return filepath.Join(root, source), false, nil
	}
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return file, false, fmt.Errorf("read %s: %w", file, err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(file); err == nil {
		mode = info.Mode().Perm()
	}
	text := string(data)
	if text != "" {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n"
	}
	text += "# Files Inkuna and its agents leave for later steps (inkunactl init).\n" + ignoreLine + "\n"
	if err := replaceFile(file, []byte(text), mode); err != nil {
		return file, false, err
	}
	return file, true, nil
}

// ignoredBy names the ignore file whose pattern ignores OutDir, relative to
// the repository root when it is inside it, or empty when nothing does.
func ignoredBy(ctx context.Context, root string) (string, error) {
	ignored, err := OutIgnored(ctx, root)
	if err != nil || !ignored {
		return "", err
	}
	// -v prints <source>:<line>:<pattern><TAB><path>.
	output, err := git(ctx, root, "check-ignore", "-v", "--no-index", OutDir+"/inkuna")
	if err != nil {
		return "", fmt.Errorf("git check-ignore %s: %w", OutDir, err)
	}
	match, _, _ := strings.Cut(output, "\t")
	// The source can hold colons (a Windows path); the line and pattern
	// are the last two fields.
	parts := strings.Split(match, ":")
	if len(parts) < 3 {
		return "", fmt.Errorf("git check-ignore printed %q", output)
	}
	return strings.Join(parts[:len(parts)-2], ":"), nil
}

// replaceFile writes data to a temporary file beside path and renames it
// over path, so a reader never sees half of it.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
