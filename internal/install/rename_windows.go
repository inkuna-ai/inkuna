//go:build windows

package install

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// renameRetry is how long rename keeps trying a file another process has
// open. A virus scanner, Windows Defender among them, opens a build that
// was just written to scan it, and Windows refuses to rename a file while
// another process holds it without sharing delete. The scan ends within a
// second or so, so a short retry turns a failed install into a slow one.
const renameRetry = 2 * time.Second

// rename is os.Rename, retried while another process holds the file.
func rename(from, to string) error {
	deadline := time.Now().Add(renameRetry)
	for {
		err := os.Rename(from, to)
		if err == nil || !heldOpen(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// heldOpen reports whether err is Windows refusing a rename because a
// process has the file open: a sharing violation, or access denied, which
// Windows also gives for a file whose deletion is pending in a handle
// still open.
func heldOpen(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
