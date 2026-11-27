// Package install puts a build of Inkuna in place: where the app and
// inkunactl go on each platform, swapping a new build in beside the old one,
// putting the CLI's directory on PATH, the Linux desktop entry and the
// Windows Start menu shortcut, and the record of what was installed. The
// inkunactl install wizard drives it; every function takes the platform and
// paths it works on, so tests run each platform's layout in a temporary home.
package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Platform is what decides where Inkuna goes on one machine.
type Platform struct {
	// OS is a GOOS value: darwin, linux, or windows.
	OS string
	// Home is the user's home directory.
	Home string
	// LocalAppData and AppData are %LOCALAPPDATA% and %APPDATA% on Windows.
	LocalAppData string
	AppData      string
	// Applications is macOS's shared applications folder, /Applications.
	Applications string
	// Writable reports whether files can be created in a directory. Nil
	// means Writable.
	Writable func(dir string) bool
}

// Target is one thing the build makes and where it is installed.
type Target struct {
	// Name is how the wizard refers to it.
	Name string
	// Built is where the build leaves it, relative to the checkout.
	Built string
	// Installed is where it goes.
	Installed string
}

// Layout is where everything goes on one platform.
type Layout struct {
	OS  string
	App Target
	CLI Target
	// BinDir holds inkunactl, and on Linux the app. It goes on PATH.
	BinDir string
	// DesktopEntry and Icon are the Linux desktop entry and its icon.
	DesktopEntry string
	Icon         string
	// IconSource is the icon in the checkout, relative to it.
	IconSource string
	// Shortcut is the Windows Start menu shortcut.
	Shortcut string
}

// LayoutFor says where the app and CLI go:
//
//   - macOS: Inkuna.app in /Applications, or ~/Applications when
//     /Applications is not writable; inkunactl in ~/.local/bin.
//   - Linux: the app and inkunactl in ~/.local/bin, with a desktop entry
//     and icon under ~/.local/share.
//   - Windows: the app and inkunactl in %LOCALAPPDATA%\Programs\Inkuna,
//     with a Start menu shortcut.
//
// Every location belongs to the user, so nothing needs administrator
// rights, except /Applications, which is used only when it is writable.
func LayoutFor(platform Platform) (Layout, error) {
	if strings.TrimSpace(platform.Home) == "" && platform.OS != "windows" {
		return Layout{}, errors.New("find the home directory: it is not set")
	}
	writable := platform.Writable
	if writable == nil {
		writable = Writable
	}
	local := filepath.Join(platform.Home, ".local")
	switch platform.OS {
	case "darwin":
		applications := filepath.Join(platform.Home, "Applications")
		if platform.Applications != "" && writable(platform.Applications) {
			applications = platform.Applications
		}
		bin := filepath.Join(local, "bin")
		return Layout{
			OS:     platform.OS,
			App:    Target{Name: "The app", Built: filepath.Join("build", "bin", "Inkuna.app"), Installed: filepath.Join(applications, "Inkuna.app")},
			CLI:    Target{Name: "inkunactl", Built: filepath.Join("bin", "inkunactl"), Installed: filepath.Join(bin, "inkunactl")},
			BinDir: bin,
		}, nil
	case "linux":
		bin := filepath.Join(local, "bin")
		share := filepath.Join(local, "share")
		return Layout{
			OS:           platform.OS,
			App:          Target{Name: "The app", Built: filepath.Join("build", "bin", "Inkuna"), Installed: filepath.Join(bin, "inkuna")},
			CLI:          Target{Name: "inkunactl", Built: filepath.Join("bin", "inkunactl"), Installed: filepath.Join(bin, "inkunactl")},
			BinDir:       bin,
			DesktopEntry: filepath.Join(share, "applications", "inkuna.desktop"),
			Icon:         filepath.Join(share, "icons", "inkuna.png"),
			IconSource:   filepath.Join("build", "appicon.png"),
		}, nil
	case "windows":
		if strings.TrimSpace(platform.LocalAppData) == "" || strings.TrimSpace(platform.AppData) == "" {
			return Layout{}, errors.New("find the install folder: LOCALAPPDATA and APPDATA must be set")
		}
		programs := filepath.Join(platform.LocalAppData, "Programs", "Inkuna")
		return Layout{
			OS:       platform.OS,
			App:      Target{Name: "The app", Built: filepath.Join("build", "bin", "Inkuna.exe"), Installed: filepath.Join(programs, "Inkuna.exe")},
			CLI:      Target{Name: "inkunactl", Built: filepath.Join("bin", "inkunactl.exe"), Installed: filepath.Join(programs, "inkunactl.exe")},
			BinDir:   programs,
			Shortcut: filepath.Join(platform.AppData, "Microsoft", "Windows", "Start Menu", "Programs", "Inkuna.lnk"),
		}, nil
	}
	return Layout{}, fmt.Errorf("install on %s: only macOS, Linux, and Windows are supported", platform.OS)
}

// AppProgram is the file that runs when the app does: the executable inside
// the bundle on macOS, the app itself elsewhere. Running looks for it.
func (layout Layout) AppProgram() string {
	if layout.OS == "darwin" {
		return filepath.Join(layout.App.Installed, "Contents", "MacOS", "Inkuna")
	}
	return layout.App.Installed
}

// Installed reports whether the app and CLI are both where the layout puts
// them.
func (layout Layout) Installed() bool {
	for _, path := range []string{layout.App.Installed, layout.CLI.Installed} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

// Writable reports whether a file can be created in dir, by creating one.
// Permission bits alone do not say, since an access control list or a
// read-only volume can refuse what they allow.
func Writable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".inkuna-write-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}
