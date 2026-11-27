package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLayoutForEachPlatform(t *testing.T) {
	home := filepath.Join("home", "ada")
	applications := filepath.Join("system", "Applications")
	local := filepath.Join(home, "AppData", "Local")
	roaming := filepath.Join(home, "AppData", "Roaming")
	writable := func(dir string) bool { return dir == applications }
	cases := []struct {
		os, app, cli, bin, extra, program string
	}{
		{"darwin", filepath.Join(applications, "Inkuna.app"), filepath.Join(home, ".local", "bin", "inkunactl"), filepath.Join(home, ".local", "bin"), "", filepath.Join(applications, "Inkuna.app", "Contents", "MacOS", "Inkuna")},
		{"linux", filepath.Join(home, ".local", "bin", "inkuna"), filepath.Join(home, ".local", "bin", "inkunactl"), filepath.Join(home, ".local", "bin"), filepath.Join(home, ".local", "share", "applications", "inkuna.desktop"), filepath.Join(home, ".local", "bin", "inkuna")},
		{"windows", filepath.Join(local, "Programs", "Inkuna", "Inkuna.exe"), filepath.Join(local, "Programs", "Inkuna", "inkunactl.exe"), filepath.Join(local, "Programs", "Inkuna"), filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", "Inkuna.lnk"), filepath.Join(local, "Programs", "Inkuna", "Inkuna.exe")},
	}
	for _, test := range cases {
		t.Run(test.os, func(t *testing.T) {
			layout, err := LayoutFor(Platform{OS: test.os, Home: home, LocalAppData: local, AppData: roaming, Applications: applications, Writable: writable})
			if err != nil {
				t.Fatal(err)
			}
			if layout.App.Installed != test.app || layout.CLI.Installed != test.cli || layout.BinDir != test.bin {
				t.Fatalf("app %s, cli %s, bin %s", layout.App.Installed, layout.CLI.Installed, layout.BinDir)
			}
			if extra := layout.DesktopEntry + layout.Shortcut; extra != test.extra {
				t.Fatalf("desktop entry or shortcut = %q, want %q", extra, test.extra)
			}
			if program := layout.AppProgram(); program != test.program {
				t.Fatalf("AppProgram = %s, want %s", program, test.program)
			}
		})
	}
}

func TestLayoutUsesHomeApplicationsWhenTheSharedFolderIsNotWritable(t *testing.T) {
	layout, err := LayoutFor(Platform{OS: "darwin", Home: "/Users/ada", Applications: "/Applications", Writable: func(string) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/Users/ada", "Applications", "Inkuna.app"); layout.App.Installed != want {
		t.Fatalf("app = %s, want %s", layout.App.Installed, want)
	}
}

func TestLayoutRefusesWhatItCannotPlace(t *testing.T) {
	for _, platform := range []Platform{
		{OS: "windows", Home: `C:\Users\ada`},
		{OS: "linux"},
		{OS: "plan9", Home: "/usr/ada"},
	} {
		if _, err := LayoutFor(platform); err == nil {
			t.Errorf("%+v: no error", platform)
		}
	}
}

func TestWritable(t *testing.T) {
	dir := t.TempDir()
	if !Writable(dir) {
		t.Fatal("a temporary directory is not writable")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the probe was left behind: %v", entries)
	}
	if Writable(filepath.Join(dir, "missing")) {
		t.Fatal("a missing directory is writable")
	}
}
