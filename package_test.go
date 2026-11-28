package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/install"
	"github.com/inkuna-ai/inkuna/internal/secrets"
)

// TestThePackageIsInkunas keeps Wails' placeholders out of the packaged
// app. macOS keys an app's preferences, WebKit storage and permission grants
// on its bundle ID, so the ID cannot change once the app is released; it is
// the service Inkuna's keychain items use, and a wails dev build gets one of
// its own so it is not taken for the installed app.
func TestThePackageIsInkunas(t *testing.T) {
	bundleIDs := map[string]string{
		"Info.plist":     secrets.DefaultService,
		"Info.dev.plist": secrets.DefaultService + ".dev",
	}
	identifier := regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>([^<]*)</string>`)
	for file, want := range bundleIDs {
		data, err := os.ReadFile(filepath.Join("build", "darwin", file))
		if err != nil {
			t.Fatal(err)
		}
		plist := string(data)
		got := ""
		if match := identifier.FindStringSubmatch(plist); match != nil {
			got = match[1]
		}
		if got != want {
			t.Errorf("%s: the bundle ID is %q, want %q", file, got, want)
		}
		if strings.Contains(plist, "com.wails") {
			t.Errorf("%s still names com.wails", file)
		}
	}

	var project struct {
		Info struct {
			CompanyName, ProductName, ProductVersion, Copyright, Comments string
		}
	}
	readJSON(t, "wails.json", &project)
	info := project.Info
	// CFBundleShortVersionString and the Windows version resource both take
	// numbers only.
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(info.ProductVersion) {
		t.Errorf("wails.json: productVersion %q is not x.y.z; Wails would ship 1.0.0", info.ProductVersion)
	}
	var frontend struct{ Version string }
	readJSON(t, filepath.Join("frontend", "package.json"), &frontend)
	if info.ProductVersion != frontend.Version {
		t.Errorf("wails.json: productVersion %q is not the frontend's %q; release both together", info.ProductVersion, frontend.Version)
	}
	// Empty, Wails fills these in with its own name and placeholders.
	for field, value := range map[string]string{"companyName": info.CompanyName, "productName": info.ProductName, "copyright": info.Copyright, "comments": info.Comments} {
		if value == "" || strings.Contains(strings.ToLower(value), "wails") {
			t.Errorf("wails.json: info.%s is %q", field, value)
		}
	}
}

// TestTheWindowsInstallerMatchesTheLayout keeps the installer a release
// ships putting the app, inkunactl, and the Start menu shortcut where
// inkunactl install does, for the current user. An install from either one
// then replaces the other, and inkunactl update finds what the installer put
// in place.
func TestTheWindowsInstallerMatchesTheLayout(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("build", "windows", "installer", "project.nsi"))
	if err != nil {
		t.Fatal(err)
	}
	var project struct {
		Name string
		Info struct{ ProductName string }
	}
	readJSON(t, "wails.json", &project)
	// project.nsi names the app through wails_tools.nsh's defines, which
	// wails build fills in from wails.json. A checkout with Git's autocrlf
	// has CRLF line endings.
	nsi := strings.NewReplacer(
		"\r\n", "\n",
		"${INFO_PRODUCTNAME}", project.Info.ProductName,
		"${PRODUCT_EXECUTABLE}", project.Name+".exe",
	).Replace(string(data))

	// Admin rights would install for every user, under Program Files.
	for _, want := range []string{`!define REQUEST_EXECUTION_LEVEL "user"`, `!define WAILS_INSTALL_SCOPE "user"`} {
		if !strings.Contains(nsi, want+"\n") {
			t.Errorf("project.nsi lacks %s", want)
		}
	}

	local, roaming := `C:\Users\ada\AppData\Local`, `C:\Users\ada\AppData\Roaming`
	layout, err := install.LayoutFor(install.Platform{OS: "windows", LocalAppData: local, AppData: roaming})
	if err != nil {
		t.Fatal(err)
	}
	// NSIS paths use backslashes; LayoutFor joins with this platform's
	// separator.
	under := func(root, nsisPath string) string {
		return filepath.Join(append([]string{root}, strings.Split(nsisPath, `\`)...)...)
	}
	match := regexp.MustCompile(`(?m)^InstallDir "\$LOCALAPPDATA\\([^"]+)"$`).FindStringSubmatch(nsi)
	if match == nil {
		t.Fatal(`project.nsi has no InstallDir "$LOCALAPPDATA\..."`)
	}
	dir := under(local, match[1])
	if got := filepath.Join(dir, project.Name+".exe"); got != layout.App.Installed {
		t.Errorf("the installer puts the app at %s, inkunactl install at %s", got, layout.App.Installed)
	}
	if got := filepath.Join(dir, "inkunactl.exe"); got != layout.CLI.Installed {
		t.Errorf("the installer puts inkunactl at %s, inkunactl install at %s", got, layout.CLI.Installed)
	}
	if !strings.Contains(nsi, `File "/oname=inkunactl.exe"`) {
		t.Error("the installer does not install inkunactl.exe")
	}

	shortcut := regexp.MustCompile(`(?m)^\s*CreateShortcut "\$SMPROGRAMS\\([^"]+)"`).FindAllStringSubmatch(nsi, -1)
	if len(shortcut) != 1 {
		t.Fatalf("project.nsi makes %d Start menu shortcuts, want 1", len(shortcut))
	}
	if got := under(filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs"), shortcut[0][1]); got != layout.Shortcut {
		t.Errorf("the installer's shortcut is %s, inkunactl install's %s", got, layout.Shortcut)
	}
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
