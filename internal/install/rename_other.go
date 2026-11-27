//go:build !windows

package install

import "os"

// rename is os.Rename: only Windows refuses to rename a file another
// process has open (see rename_windows.go).
func rename(from, to string) error {
	return os.Rename(from, to)
}
