//go:build windows

package proc

import "time"

// EndLeftOver finds nothing on Windows, which offers no cheap way to read
// another process's environment. Nothing is left to find: what Start starts
// ends with the Inkuna that started it.
func EndLeftOver(string, time.Duration) (int, error) {
	return 0, nil
}
