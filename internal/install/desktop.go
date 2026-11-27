package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DesktopEntry is the Linux desktop entry that lists the app in the
// desktop's application menu. Icon is a full path, since icon themes have
// no size for the 1024-pixel app icon and would not find it by name.
func DesktopEntry(executable, icon string) string {
	return strings.Join([]string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=Inkuna",
		"Comment=Run several coding agents on one repository",
		"Exec=" + desktopQuote(executable),
		"Icon=" + icon,
		"Terminal=false",
		"Categories=Development;",
		"StartupWMClass=Inkuna",
		"",
	}, "\n")
}

// desktopQuote quotes an Exec program as the Desktop Entry Specification
// asks: inside double quotes, a backslash, double quote, backquote, or
// dollar sign is escaped with a backslash. Then the value's own escaping
// doubles every backslash.
func desktopQuote(path string) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	for _, character := range path {
		switch character {
		case '\\', '"', '`', '$':
			quoted.WriteByte('\\')
		}
		quoted.WriteRune(character)
	}
	quoted.WriteByte('"')
	return strings.ReplaceAll(quoted.String(), `\`, `\\`)
}

// WriteDesktopEntry writes the app's desktop entry and copies its icon.
func WriteDesktopEntry(layout Layout, checkout string) error {
	icon, err := os.ReadFile(filepath.Join(checkout, layout.IconSource))
	if err != nil {
		return fmt.Errorf("read the app icon: %w", err)
	}
	if err := writeAtomically(layout.Icon, icon, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", layout.Icon, err)
	}
	if err := writeAtomically(layout.DesktopEntry, []byte(DesktopEntry(layout.App.Installed, layout.Icon)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", layout.DesktopEntry, err)
	}
	return nil
}
