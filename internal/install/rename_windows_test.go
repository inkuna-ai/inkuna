//go:build windows

package install

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A scanner holds a build it is scanning open, as os.Open does here: Go
// opens files without sharing delete, so Windows refuses to rename them.
func TestRenameWaitsForAFileAnotherProcessHoldsOpen(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "build.exe"), filepath.Join(dir, "installed.exe")
	writeFile(t, from, "new")
	held, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); !heldOpen(err) {
		_ = held.Close()
		t.Fatalf("os.Rename of a file held open = %v, want a sharing violation", err)
	}
	released := time.AfterFunc(200*time.Millisecond, func() { _ = held.Close() })
	defer released.Stop()
	if err := rename(from, to); err != nil {
		t.Fatalf("rename once the file is let go: %v", err)
	}
	if got := readFile(t, to); got != "new" {
		t.Fatalf("renamed = %q", got)
	}
}

func TestRenameFailsAtOnceOnAMissingFile(t *testing.T) {
	dir := t.TempDir()
	began := time.Now()
	err := rename(filepath.Join(dir, "missing"), filepath.Join(dir, "to"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("rename of a missing file = %v, want not exist", err)
	}
	if elapsed := time.Since(began); elapsed >= renameRetry {
		t.Fatalf("rename retried a missing file for %s", elapsed)
	}
}
