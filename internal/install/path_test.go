package install

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOnPath(t *testing.T) {
	cases := []struct {
		value, dir, goos string
		want             bool
	}{
		{"/usr/bin:/home/ada/.local/bin", "/home/ada/.local/bin", "linux", true},
		{"/usr/bin:/home/ada/.local/bin/", "/home/ada/.local/bin", "darwin", true},
		{"/usr/bin:/home/ada/.local/bin/x", "/home/ada/.local/bin", "linux", false},
		{"/usr/bin:/home/Ada/.local/bin", "/home/ada/.local/bin", "linux", false},
		{`C:\Windows;C:\Users\Ada\AppData\Local\Programs\Inkuna\`, `C:\Users\ada\AppData\Local\Programs\Inkuna`, "windows", true},
		{`C:\Windows;C:\Users\ada\AppData\Local\Programs`, `C:\Users\ada\AppData\Local\Programs\Inkuna`, "windows", false},
		{"", "/home/ada/.local/bin", "linux", false},
	}
	for _, test := range cases {
		if got := OnPath(test.value, test.dir, test.goos); got != test.want {
			t.Errorf("OnPath(%q, %q, %s) = %v, want %v", test.value, test.dir, test.goos, got, test.want)
		}
	}
}

func TestShellProfile(t *testing.T) {
	home := "/home/ada"
	dir := home + "/.local/bin"
	cases := []struct {
		goos, shell, file, line string
	}{
		{"darwin", "/bin/zsh", ".zshrc", `export PATH="$HOME/.local/bin:$PATH"`},
		{"linux", "/usr/bin/bash", ".bashrc", `export PATH="$HOME/.local/bin:$PATH"`},
		{"darwin", "/bin/bash", ".bash_profile", `export PATH="$HOME/.local/bin:$PATH"`},
		{"linux", "/usr/bin/fish", filepath.Join(".config", "fish", "conf.d", "inkuna.fish"), `fish_add_path --global "$HOME/.local/bin"`},
	}
	for _, test := range cases {
		profile, ok := ShellProfile(Shell{OS: test.goos, Home: home, Program: test.shell}, dir)
		if !ok || profile.File != filepath.Join(home, test.file) || profile.Line != test.line {
			t.Errorf("%s %s: %+v, %v", test.goos, test.shell, profile, ok)
		}
	}
	if profile, ok := ShellProfile(Shell{OS: "linux", Home: home, Program: "/bin/zsh"}, "/opt/inkuna/bin"); !ok || profile.Line != `export PATH="/opt/inkuna/bin:$PATH"` {
		t.Errorf("a directory outside home: %+v", profile)
	}
	for _, refused := range []struct{ shell, dir string }{
		{"/bin/tcsh", dir},
		{"", dir},
		{"/bin/zsh", home + "/$bin"},
		{"/bin/zsh", home + `/"bin"`},
	} {
		if profile, ok := ShellProfile(Shell{OS: "linux", Home: home, Program: refused.shell}, refused.dir); ok {
			t.Errorf("%+v gave %+v", refused, profile)
		}
	}
}

func TestShellProfileFollowsWhereTheShellReads(t *testing.T) {
	home := "/home/ada"
	dir := home + "/.local/bin"
	if profile, _ := ShellProfile(Shell{OS: "linux", Home: home, Program: "/bin/zsh", ZDotDir: "/home/ada/.config/zsh"}, dir); profile.File != filepath.Join("/home/ada/.config/zsh", ".zshrc") {
		t.Errorf("with ZDOTDIR: %s", profile.File)
	}
	if profile, _ := ShellProfile(Shell{OS: "linux", Home: home, Program: "/usr/bin/fish", ConfigHome: "/srv/ada/config"}, dir); profile.File != filepath.Join("/srv/ada/config", "fish", "conf.d", "inkuna.fish") {
		t.Errorf("with XDG_CONFIG_HOME: %s", profile.File)
	}
}

// A bash login shell reads only the first of .bash_profile, .bash_login,
// and .profile, so the line goes in the one the user has.
func TestShellProfileUsesTheBashLoginFileThatExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell startup files are for macOS and Linux")
	}
	for _, existing := range [][]string{nil, {".profile"}, {".bash_login", ".profile"}, {".bash_profile", ".profile"}} {
		home := t.TempDir()
		for _, name := range existing {
			writeFile(t, filepath.Join(home, name), "# "+name+"\n")
		}
		want := ".bash_profile"
		if len(existing) > 0 {
			want = existing[0]
		}
		profile, ok := ShellProfile(Shell{OS: "darwin", Home: home, Program: "/bin/bash"}, filepath.Join(home, ".local", "bin"))
		if !ok || profile.File != filepath.Join(home, want) {
			t.Errorf("with %v: %s, want %s", existing, profile.File, want)
		}
	}
}

func TestProfileAddAppendsOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell startup files are for macOS and Linux")
	}
	home := t.TempDir()
	profile, _ := ShellProfile(Shell{OS: "linux", Home: home, Program: "/bin/zsh"}, filepath.Join(home, ".local", "bin"))
	if err := os.WriteFile(profile.File, []byte("alias ll='ls -l'"), 0o644); err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		added, err := profile.Add()
		if err != nil || added != (index == 0) {
			t.Fatalf("add %d: added %v, err %v", index, added, err)
		}
	}
	want := "alias ll='ls -l'\n\n" + profileComment + "\n" + profile.Line + "\n"
	if got := readFile(t, profile.File); got != want {
		t.Fatalf("profile:\n%s\nwant:\n%s", got, want)
	}
}

func TestProfileAddCreatesTheFileAndItsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell startup files are for macOS and Linux")
	}
	home := t.TempDir()
	profile, _ := ShellProfile(Shell{OS: "linux", Home: home, Program: "/usr/bin/fish"}, filepath.Join(home, ".local", "bin"))
	if added, err := profile.Add(); err != nil || !added {
		t.Fatalf("added %v, err %v", added, err)
	}
	if got, want := readFile(t, profile.File), profileComment+"\n"+profile.Line+"\n"; got != want {
		t.Fatalf("profile = %q, want %q", got, want)
	}
}

// A startup file linked from a dotfiles repository stays a link.
func TestProfileAddKeepsALinkedStartupFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell startup files are for macOS and Linux")
	}
	home := t.TempDir()
	dotfiles := filepath.Join(home, "dotfiles", "zshrc")
	writeFile(t, dotfiles, "setopt autocd\n")
	profile, _ := ShellProfile(Shell{OS: "darwin", Home: home, Program: "/bin/zsh"}, filepath.Join(home, ".local", "bin"))
	if err := os.Symlink(dotfiles, profile.File); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Add(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(profile.File); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the startup file is no longer a link: %v", err)
	}
	if got := readFile(t, dotfiles); !strings.HasSuffix(got, profile.Line+"\n") {
		t.Fatalf("the linked file lacks the line:\n%s", got)
	}
}

type fakeUserPath struct {
	value string
	err   error
	sets  int
}

func (path *fakeUserPath) Get() (string, error) { return path.value, path.err }
func (path *fakeUserPath) Set(value string) error {
	path.value = value
	path.sets++
	return nil
}

func TestAddToUserPath(t *testing.T) {
	dir := `C:\Users\ada\AppData\Local\Programs\Inkuna`
	path := &fakeUserPath{value: `%USERPROFILE%\go\bin;`}
	for index := range 2 {
		added, err := AddToUserPath(path, dir)
		if err != nil || added != (index == 0) {
			t.Fatalf("add %d: added %v, err %v", index, added, err)
		}
	}
	if want := `%USERPROFILE%\go\bin;` + dir; path.value != want || path.sets != 1 {
		t.Fatalf("PATH = %q after %d sets, want %q once", path.value, path.sets, want)
	}
	empty := &fakeUserPath{}
	if _, err := AddToUserPath(empty, dir); err != nil || empty.value != dir {
		t.Fatalf("an empty PATH became %q, %v", empty.value, err)
	}
	if _, err := AddToUserPath(&fakeUserPath{err: errors.New("access denied")}, dir); err == nil {
		t.Fatal("a PATH that cannot be read was changed")
	}
}
