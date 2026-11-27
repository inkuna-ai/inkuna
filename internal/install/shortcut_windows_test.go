//go:build windows

package install

import (
	"os"
	"path/filepath"
	"testing"
)

// The shortcut is read back through the Shell's own link object, which is
// how Explorer and the Start menu read it.
func TestWriteShortcutPointsAtTheApp(t *testing.T) {
	home := t.TempDir()
	layout, err := LayoutFor(Platform{OS: "windows", Home: home, LocalAppData: filepath.Join(home, "Local"), AppData: filepath.Join(home, "Roaming")})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, layout.App.Installed, "app")
	for range 2 {
		if err := WriteShortcut(layout); err != nil {
			t.Fatal(err)
		}
	}
	target, err := shortcutTarget(layout.Shortcut)
	if err != nil {
		t.Fatal(err)
	}
	// The Shell gives the long form of a path the temporary directory names
	// in its 8.3 short form (RUNNER~1), so compare the files, not the names.
	opened, err := os.Stat(target)
	if err != nil {
		t.Fatalf("the shortcut opens %q: %v", target, err)
	}
	app, err := os.Stat(layout.App.Installed)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(opened, app) {
		t.Fatalf("the shortcut opens %q, want %q", target, layout.App.Installed)
	}
}
