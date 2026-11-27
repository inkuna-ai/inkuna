//go:build !linux && !darwin && !windows

package install

import "errors"

// processExecutables is not available where Inkuna does not install.
func processExecutables() ([]string, error) {
	return nil, errors.New("only macOS, Linux, and Windows are supported")
}
