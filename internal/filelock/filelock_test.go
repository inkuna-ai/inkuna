package filelock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLockIsHeldByOneOpenFileAtATime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "app.lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("a second lock: %v, want ErrLocked", err)
	}
	unlock()
	again, err := Lock(path)
	if err != nil {
		t.Fatalf("locking after unlock: %v", err)
	}
	again()
}
