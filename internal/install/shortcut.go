package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteShortcut writes the Start menu shortcut to the installed app. It
// writes a temporary shortcut beside the real one and renames it into
// place, so a shortcut that fails part way never replaces a working one.
func WriteShortcut(layout Layout) error {
	dir := filepath.Dir(layout.Shortcut)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	temporary := filepath.Join(dir, "."+filepath.Base(layout.Shortcut)+".new")
	if err := saveShortcut(temporary, layout.App.Installed); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("write %s: %w", layout.Shortcut, err)
	}
	if err := rename(temporary, layout.Shortcut); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("write %s: %w", layout.Shortcut, err)
	}
	return nil
}
