package install

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopEntryQuotesTheProgram(t *testing.T) {
	entry := DesktopEntry(`/home/ada/my "apps"/$inkuna`, "/home/ada/.local/share/icons/inkuna.png")
	for _, want := range []string{
		"[Desktop Entry]\nType=Application\nName=Inkuna\n",
		`Exec="/home/ada/my \\"apps\\"/\\$inkuna"` + "\n",
		"Icon=/home/ada/.local/share/icons/inkuna.png\n",
		"Terminal=false\n",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("entry lacks %q:\n%s", want, entry)
		}
	}
}

func TestWriteDesktopEntryCopiesTheIcon(t *testing.T) {
	home, checkout := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(checkout, "build", "appicon.png"), "png")
	layout, err := LayoutFor(Platform{OS: "linux", Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteDesktopEntry(layout, checkout); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, layout.Icon); got != "png" {
		t.Fatalf("icon = %q", got)
	}
	if got := readFile(t, layout.DesktopEntry); got != DesktopEntry(layout.App.Installed, layout.Icon) {
		t.Fatalf("entry:\n%s", got)
	}
}
