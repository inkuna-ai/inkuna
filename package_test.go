package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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
