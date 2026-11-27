package install

import (
	"errors"
	"testing"
)

func TestLockAllowsOneInstallAtATime(t *testing.T) {
	dir := t.TempDir()
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("a second lock: %v, want ErrLocked", err)
	}
	unlock()
	again, err := Lock(dir)
	if err != nil {
		t.Fatalf("locking after unlock: %v", err)
	}
	again()
}
