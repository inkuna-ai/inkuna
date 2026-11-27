//go:build !windows

package install

import "errors"

// saveShortcut makes a Start menu shortcut, which only Windows has.
func saveShortcut(path, target string) error {
	return errors.New("only Windows has a Start menu")
}
