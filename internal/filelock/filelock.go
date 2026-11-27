// Package filelock takes the operating system's exclusive lock on a file.
// The lock belongs to the open file, so the system releases it when the
// process ends, and a process that crashes or is killed never leaves it held.
package filelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrLocked is Lock's error when another open file holds the lock, in this
// process or another.
var ErrLocked = errors.New("the file is locked")

// Lock creates the file at path if it is missing, with its directory, and
// takes its lock. It fails at once with ErrLocked rather than waiting.
// unlock releases it.
func Lock(path string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrLocked) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		_ = unlockFile(file)
		_ = file.Close()
	}, nil
}
