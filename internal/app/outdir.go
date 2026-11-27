package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// OutArchiveArtifact is the artifact kind of what a completed run left in
// .inkuna/out, packed as one .tar.gz.
const OutArchiveArtifact = "archive"

// cleanOut does what the repository's branching configuration says with
// .inkuna/out of every directory a completed run's steps worked in: its
// worktree, and the checkout when a step ran there, as a read-only stage
// before the worktree exists does. It runs before the run reports
// completed and before the checkout is free for another run.
func (service *Service) cleanOut(runID string, project storage.Project) {
	service.runsMu.RLock()
	run := service.runs[runID]
	var taskID string
	if run != nil {
		taskID = run.task.ID
	}
	service.runsMu.RUnlock()
	if run == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// Nothing is removed on a guess.
	loaded, err := branching.Load(project.Repository)
	if err != nil {
		service.noteWorkspace(runID, "Kept "+worktree.OutDir+": "+err.Error())
		return
	}
	directories, err := service.store.RunDirectories(ctx, runID)
	if err != nil {
		service.noteWorkspace(runID, "Kept "+worktree.OutDir+": "+err.Error())
		return
	}
	for _, directory := range directories {
		label := ""
		if len(directories) > 1 {
			label = "worktree"
			if filepath.Clean(directory) == filepath.Clean(project.Repository) {
				label = "checkout"
			}
		}
		note, err := service.emptyOut(ctx, taskID, runID, directory, label, loaded.Config.Out.Cleanup)
		switch {
		case err != nil:
			service.noteWorkspace(runID, "Kept "+worktree.OutDir+" in "+directory+": "+err.Error())
		case note != "":
			service.noteWorkspace(runID, note)
		}
	}
}

// emptyOut applies an out cleanup policy to one working directory and says
// what it did, or nothing when there was nothing to do. An archive that
// cannot be stored leaves every file where it was.
func (service *Service) emptyOut(ctx context.Context, taskID, runID, directory, label string, policy branching.OutCleanup) (string, error) {
	if policy == branching.OutKeep {
		return "", nil
	}
	root, err := os.OpenRoot(directory)
	if errors.Is(err, fs.ErrNotExist) {
		// A worktree removed by hand has nothing left to clean.
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(worktree.OutDir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", worktree.OutDir)
	}
	where := worktree.OutDir
	if label != "" {
		where += " of the " + label
	}
	note := "Emptied " + where
	if policy == branching.OutArchive {
		name := "inkuna-out-" + runID + ".tar.gz"
		if label != "" {
			name = "inkuna-out-" + runID + "-" + label + ".tar.gz"
		}
		files, err := service.archiveOut(ctx, root, taskID, runID, name)
		if err != nil {
			return "", err
		}
		note = "Emptied " + where + "; the run's artifacts already held everything in it"
		if files > 0 {
			note = fmt.Sprintf("Archived %d file(s) left in %s as %s, then emptied it", files, where, name)
		}
	}
	if err := root.RemoveAll(worktree.OutDir); err != nil {
		return "", err
	}
	return note, nil
}

// archiveOut packs the regular files under .inkuna/out that none of the
// task's artifacts holds into one .tar.gz artifact of the run, under name.
// Symbolic links are not followed or packed. It returns how many files the
// archive holds, none when the artifacts held them all and none was stored.
func (service *Service) archiveOut(ctx context.Context, root *os.Root, taskID, runID, name string) (int, error) {
	records, err := service.store.TaskArtifacts(ctx, taskID)
	if err != nil {
		return 0, fmt.Errorf("list the run's artifacts: %w", err)
	}
	held := make(map[string]bool, len(records))
	for _, record := range records {
		held[record.Digest] = true
	}
	var packed bytes.Buffer
	compressed := gzip.NewWriter(&packed)
	archive := tar.NewWriter(compressed)
	files, total := 0, 0
	walkErr := fs.WalkDir(root.FS(), worktree.OutDir, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative := file[len(worktree.OutDir)+1:]
		// Sized before it is read, so a huge file is refused without being held
		// in memory. No stored output or document is over the limit, so such a
		// file is never one the artifacts already hold.
		if info.Size() > maxOutputArtifact {
			return fmt.Errorf("%s in it is over %d bytes, more than one archive holds", path.Clean(relative), maxOutputArtifact)
		}
		data, err := root.ReadFile(file)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if held[hex.EncodeToString(sum[:])] {
			return nil
		}
		total += len(data)
		if total > maxOutputArtifact {
			return fmt.Errorf("what is left in it is over %d bytes, more than one archive holds", maxOutputArtifact)
		}
		header := &tar.Header{Name: path.Clean(relative), Mode: 0o644, Size: int64(len(data)), ModTime: info.ModTime(), Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if _, err := archive.Write(data); err != nil {
			return err
		}
		files++
		return nil
	})
	if walkErr != nil {
		return 0, walkErr
	}
	if err := archive.Close(); err != nil {
		return 0, err
	}
	if err := compressed.Close(); err != nil {
		return 0, err
	}
	if files == 0 {
		return 0, nil
	}
	if _, err := service.storeArtifact(ctx, taskID, runID, "", "", OutArchiveArtifact, name, "application/gzip", &packed); err != nil {
		return 0, fmt.Errorf("store the archive: %w", err)
	}
	return files, nil
}
