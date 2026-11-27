package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PreviousSuffix marks the build an install replaced. It stays until the
// next install, so the earlier build is there to go back to.
const PreviousSuffix = ".previous"

// stagingPattern names the hidden directory a new build is copied into
// beside the installed one. Each call gets its own, so two copies never
// share one.
func stagingPattern(destination string) string {
	return "." + filepath.Base(destination) + ".new-*"
}

// Place puts the file or directory at source at destination, and reports
// whether it replaced one already there.
//
// The new build is copied next to the old one first, into a staging
// directory of its own, then swapped in with renames, so a copy that fails
// part way leaves the installed build as it was, and no step leaves nothing
// at destination for longer than one rename. The replaced build is kept as
// destination.previous, replacing the one an earlier install kept. Renames
// rather than overwrites also let a running app or inkunactl be replaced,
// which Windows refuses to delete or write.
//
// Callers hold the install lock (Lock): Place removes staging directories
// an interrupted install left, and the swap's renames are not atomic
// together.
func Place(source, destination string) (replaced bool, err error) {
	if _, err := os.Lstat(source); err != nil {
		return false, fmt.Errorf("find the build: %w", err)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", parent, err)
	}
	if err := removeStaging(destination); err != nil {
		return false, fmt.Errorf("remove a staged copy left by an interrupted install: %w", err)
	}
	staging, err := os.MkdirTemp(parent, stagingPattern(destination))
	if err != nil {
		return false, fmt.Errorf("create a staging directory in %s: %w", parent, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	copied := filepath.Join(staging, filepath.Base(destination))
	if err := copyTree(source, copied); err != nil {
		return false, fmt.Errorf("copy %s to %s: %w", source, staging, err)
	}
	previous := destination + PreviousSuffix
	if _, err := os.Lstat(destination); err == nil {
		if err := os.RemoveAll(previous); err != nil {
			return false, fmt.Errorf("remove the build kept by the last install: %w", err)
		}
		if err := rename(destination, previous); err != nil {
			return false, fmt.Errorf("move the installed build aside: %w", err)
		}
		replaced = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("check %s: %w", destination, err)
	}
	if err := rename(copied, destination); err != nil {
		if replaced {
			// Put the old build back rather than leave no build at all.
			_ = rename(previous, destination)
		}
		return false, fmt.Errorf("move the new build into place: %w", err)
	}
	return replaced, nil
}

// removeStaging removes the staging directories of destination that an
// interrupted install left. It lists the folder and compares names rather
// than globbing, since a path can hold characters a glob would read as a
// pattern, such as "[".
func removeStaging(destination string) error {
	parent := filepath.Dir(destination)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	prefix := strings.TrimSuffix(stagingPattern(destination), "*")
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(parent, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies a file, or a directory such as an app bundle, keeping
// permissions and symbolic links, which a macOS bundle's frameworks use.
func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case entry.Type().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		}
		return fmt.Errorf("copy %s: not a file, directory, or symbolic link", path)
	})
}

func copyFile(source, destination string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// writeAtomically replaces path with data by writing a temporary file in
// the same directory and renaming it over path.
func writeAtomically(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		_ = os.Remove(name)
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// SwapPrevious swaps the installed build at destination with the one kept
// as destination.previous, so the build an install or update replaced is
// installed again and the one it replaces is kept in its place. Swapping
// twice returns to where it started.
//
// Like Place it only renames, so a running inkunactl can swap itself out,
// which Windows allows for a running program though it refuses to delete
// or overwrite one. The installed build is first moved into a hidden
// directory beside it (.<name>.swap-*), so at no point do both builds share
// a name. A rename that fails puts back what was moved; if even that fails,
// the error names where the build is.
//
// Callers hold the install lock (Lock).
func SwapPrevious(destination string) error {
	previous := destination + PreviousSuffix
	if _, err := os.Lstat(previous); err != nil {
		return fmt.Errorf("find the previous build: %w", err)
	}
	if _, err := os.Lstat(destination); errors.Is(err, fs.ErrNotExist) {
		if err := rename(previous, destination); err != nil {
			return fmt.Errorf("move the previous build into place: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("check %s: %w", destination, err)
	}
	parent := filepath.Dir(destination)
	// The directory is not removed on failure: it may hold the only copy
	// of a build.
	aside, err := os.MkdirTemp(parent, "."+filepath.Base(destination)+".swap-*")
	if err != nil {
		return fmt.Errorf("create a directory in %s to swap through: %w", parent, err)
	}
	held := filepath.Join(aside, filepath.Base(destination))
	if err := rename(destination, held); err != nil {
		_ = os.Remove(aside)
		return fmt.Errorf("move the installed build aside: %w", err)
	}
	if err := rename(previous, destination); err != nil {
		if undo := rename(held, destination); undo != nil {
			return fmt.Errorf("move the previous build into place: %w; the installed build is in %s", err, held)
		}
		_ = os.Remove(aside)
		return fmt.Errorf("move the previous build into place: %w", err)
	}
	if err := rename(held, previous); err != nil {
		return fmt.Errorf("keep the replaced build as %s: %w; it is in %s", previous, err, held)
	}
	_ = os.Remove(aside)
	return nil
}
