package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PendingChange is one path a checkout has changed since its last commit,
// outside OutDir: part of what Inkuna's next commit there would take. Status
// is git's two-letter porcelain code, such as " M", "A ", or "??".
type PendingChange struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// PendingChanges lists what Changes reports, path by path, so a person can
// see what a choice about them would keep or throw away. A renamed or copied
// path is listed by its new name.
func PendingChanges(ctx context.Context, path string) ([]PendingChange, error) {
	output, err := git(ctx, path, append([]string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}, withoutOut...)...)
	if err != nil {
		return nil, err
	}
	changes := make([]PendingChange, 0)
	entries := strings.Split(output, "\x00")
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 {
			continue
		}
		status := entry[:2]
		changes = append(changes, PendingChange{Status: status, Path: entry[3:]})
		// With -z a rename's or copy's original path follows as an entry of
		// its own, whichever column marks it: the index's for a staged one,
		// the worktree's for one git detects against an intent-to-add path.
		if strings.ContainsAny(status, "RC") {
			index++
		}
	}
	return changes, nil
}

// IgnoredInTheWayError is a discard refused before it changed anything: a
// path staged for deletion has an ignored file in the checkout, which
// restoring the last commit would overwrite though nothing listed it.
type IgnoredInTheWayError struct {
	Paths []string
}

func (err *IgnoredInTheWayError) Error() string {
	return fmt.Sprintf("restoring the last commit would overwrite ignored files staged for deletion: %s; move them or unstage their deletion first", strings.Join(err.Paths, ", "))
}

// Discard throws away a checkout's uncommitted changes outside OutDir:
// tracked files, staged or not, go back to the last commit, and untracked
// files and directories are deleted. Ignored files are kept, as no commit of
// Inkuna's would take them; one at a path staged for deletion would be
// overwritten, so Discard then refuses, changing nothing, with an
// IgnoredInTheWayError. It deletes work for good, so only a person's
// explicit choice calls it, after TakeSnapshot when the caller may need to
// PutBack what it threw away.
func Discard(ctx context.Context, path string) error {
	deleted, err := git(ctx, path, append([]string{"diff", "--cached", "--name-only", "-z", "--no-renames", "--diff-filter=D"}, withoutOut...)...)
	if err != nil {
		return fmt.Errorf("list the deletions staged: %w", err)
	}
	if paths := splitNull(deleted); len(paths) > 0 {
		// The paths are names, not patterns.
		ignored, err := git(ctx, path, append([]string{"--literal-pathspecs", "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--"}, paths...)...)
		if err != nil {
			return fmt.Errorf("look for ignored files staged for deletion: %w", err)
		}
		if paths := splitNull(ignored); len(paths) > 0 {
			return &IgnoredInTheWayError{Paths: paths}
		}
	}
	if _, err := git(ctx, path, append([]string{"restore", "--source=HEAD", "--staged", "--worktree"}, withoutOut...)...); err != nil {
		return fmt.Errorf("restore the last commit: %w", err)
	}
	// An exclude pathspec would not keep OutDir: clean takes a wholly
	// untracked parent directory as one entry, OutDir with it. An ignore
	// pattern keeps it, since clean deletes no ignored file.
	if _, err := git(ctx, path, "clean", "-q", "-f", "-d", "-e", "/"+OutDir+"/"); err != nil {
		return fmt.Errorf("delete untracked files: %w", err)
	}
	return nil
}

// Snapshot is what a checkout held beyond its last commit, kept before a
// discard so that PutBack can restore it. Commit, on top of HEAD with no ref
// pointing to it, holds the files outside OutDir, untracked ones included and
// ignored ones not; git keeps it until it prunes it, two weeks by default.
// The index file is kept whole, as git last wrote it, since no tree can hold
// all it may: a version staged apart from the file's own, an unmerged path's
// stages, or an intent-to-add entry.
type Snapshot struct {
	Commit string
	// indexFile is empty for a Snapshot TakeSnapshot did not take, whose
	// PutBack then leaves the index alone; index is nil when the checkout had
	// no index file.
	indexFile string
	index     []byte
	indexMode os.FileMode
}

// TakeSnapshot keeps a checkout's uncommitted changes, leaving its files and
// index as they were: it builds the commit in an index of its own.
func TakeSnapshot(ctx context.Context, path string) (Snapshot, error) {
	var snapshot Snapshot
	file, err := git(ctx, path, "rev-parse", "--git-path", "index")
	if err != nil {
		return Snapshot{}, fmt.Errorf("locate the index: %w", err)
	}
	if snapshot.indexFile = strings.TrimSpace(file); !filepath.IsAbs(snapshot.indexFile) {
		snapshot.indexFile = filepath.Join(path, snapshot.indexFile)
	}
	if info, err := os.Stat(snapshot.indexFile); err == nil {
		snapshot.indexMode = info.Mode().Perm()
		if snapshot.index, err = os.ReadFile(snapshot.indexFile); err != nil {
			return Snapshot{}, fmt.Errorf("keep the index: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Snapshot{}, fmt.Errorf("keep the index: %w", err)
	}
	directory, err := os.MkdirTemp("", "inkuna-snapshot-")
	if err != nil {
		return Snapshot{}, fmt.Errorf("make a scratch index: %w", err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	scratch := []string{"GIT_INDEX_FILE=" + filepath.Join(directory, "index")}
	// OutDir is taken out after the fact, as CommitAll does: an exclude
	// pathspec on git add fails when a .gitignore ignores the directory.
	for _, args := range [][]string{{"read-tree", "HEAD"}, {"add", "-A", "--", "."}, {"reset", "-q", "--", OutDir}} {
		if _, err := gitEnv(ctx, path, scratch, args...); err != nil {
			return Snapshot{}, err
		}
	}
	tree, err := gitEnv(ctx, path, scratch, "write-tree")
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Commit, err = git(ctx, path, "-c", "user.name=Inkuna", "-c", "user.email=inkuna@localhost", "-c", "commit.gpgsign=false",
		"commit-tree", strings.TrimSpace(tree), "-p", "HEAD", "-m", "inkuna: uncommitted changes kept before a discard"); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// PutBack restores what a discard threw away from its Snapshot: the files
// outside OutDir, with the tracked files it had deleted removed again, and
// then the index, so what was staged, and how, is as it was.
func PutBack(ctx context.Context, path string, snapshot Snapshot) error {
	// The files go first, while the index is still the last commit's, which
	// a restore can read without meeting an unmerged path.
	if _, err := git(ctx, path, append([]string{"restore", "--source=" + snapshot.Commit, "--worktree"}, withoutOut...)...); err != nil {
		return fmt.Errorf("put back the files kept in %s: %w", snapshot.Commit, err)
	}
	if snapshot.indexFile == "" {
		return nil
	}
	if snapshot.index == nil {
		if err := os.Remove(snapshot.indexFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("put back what was staged: %w", err)
		}
		return nil
	}
	if err := writeIndex(snapshot.indexFile, snapshot.index, snapshot.indexMode); err != nil {
		return fmt.Errorf("put back what was staged: %w", err)
	}
	return nil
}

// writeIndex replaces an index file as git does: through its lock file,
// created only when no git command holds it, then renamed into place.
func writeIndex(file string, data []byte, mode os.FileMode) error {
	lock := file + ".lock"
	handle, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("lock the index: %w", err)
	}
	_, err = handle.Write(data)
	if closeErr := handle.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(lock, file)
	}
	if err != nil {
		_ = os.Remove(lock)
		return err
	}
	return nil
}

// splitNull splits git's -z output into its entries.
func splitNull(output string) []string {
	var entries []string
	for _, entry := range strings.Split(output, "\x00") {
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}
