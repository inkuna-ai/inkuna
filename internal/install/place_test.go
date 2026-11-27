package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPlaceSwapsABuildInAndKeepsThePreviousOne(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "bin", "inkunactl")
	for index, build := range []string{"one", "two", "three"} {
		source := filepath.Join(dir, "build-"+build)
		writeFile(t, source, build)
		replaced, err := Place(source, destination)
		if err != nil {
			t.Fatal(err)
		}
		if replaced != (index > 0) {
			t.Fatalf("build %s: replaced = %v", build, replaced)
		}
		if got := readFile(t, destination); got != build {
			t.Fatalf("installed %q, want %q", got, build)
		}
	}
	// Only the build just replaced is kept.
	if got := readFile(t, destination+PreviousSuffix); got != "two" {
		t.Fatalf("previous = %q, want two", got)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "bin"))
	if len(entries) != 2 {
		t.Fatalf("bin holds %v, want the build and the previous one", entries)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(destination); info.Mode().Perm()&0o100 == 0 {
			t.Fatalf("mode %v lost the execute bit", info.Mode())
		}
	}
}

func TestPlaceCopiesAnAppBundle(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "build", "Inkuna.app")
	writeFile(t, filepath.Join(source, "Contents", "MacOS", "Inkuna"), "binary")
	writeFile(t, filepath.Join(source, "Contents", "Info.plist"), "plist")
	link := filepath.Join(source, "Contents", "Current")
	if err := os.Symlink("MacOS", link); err != nil {
		t.Skipf("symbolic links are not available: %v", err)
	}
	destination := filepath.Join(dir, "Applications", "Inkuna.app")
	if _, err := Place(source, destination); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(destination, "Contents", "MacOS", "Inkuna")); got != "binary" {
		t.Fatalf("binary = %q", got)
	}
	if target, err := os.Readlink(filepath.Join(destination, "Contents", "Current")); err != nil || target != "MacOS" {
		t.Fatalf("link = %q, %v; want it kept as a link to MacOS", target, err)
	}
}

func TestPlaceLeavesTheInstalledBuildWhenTheCopyFails(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "Inkuna.app")
	writeFile(t, filepath.Join(destination, "Contents", "MacOS", "Inkuna"), "installed")
	check := func(t *testing.T) {
		t.Helper()
		if got := readFile(t, filepath.Join(destination, "Contents", "MacOS", "Inkuna")); got != "installed" {
			t.Fatalf("installed = %q", got)
		}
		if _, err := os.Stat(destination + PreviousSuffix); !os.IsNotExist(err) {
			t.Fatal("the installed build was moved aside by a failed place")
		}
		if staged, _ := filepath.Glob(filepath.Join(dir, stagingPattern(destination))); len(staged) != 0 {
			t.Fatalf("a failed place left %v", staged)
		}
	}
	t.Run("a missing build", func(t *testing.T) {
		if _, err := Place(filepath.Join(dir, "missing"), destination); err == nil {
			t.Fatal("placing a missing build did not fail")
		}
		check(t)
	})
	t.Run("a file that cannot be read part way through", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs file permissions that refuse the reader")
		}
		source := filepath.Join(dir, "build", "Inkuna.app")
		writeFile(t, filepath.Join(source, "Contents", "Info.plist"), "plist")
		unreadable := filepath.Join(source, "Contents", "MacOS", "Inkuna")
		writeFile(t, unreadable, "new")
		if err := os.Chmod(unreadable, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := Place(source, destination); err == nil {
			t.Fatal("the copy did not fail")
		}
		check(t)
	})
}

func TestPlaceReplacesAStagedCopyLeftByAnInterruptedInstall(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "inkunactl")
	leftover := filepath.Join(dir, ".inkunactl.new-12345")
	writeFile(t, filepath.Join(leftover, "inkunactl"), "half copied")
	source := filepath.Join(dir, "build")
	writeFile(t, source, "new")
	if _, err := Place(source, destination); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, destination); got != "new" {
		t.Fatalf("installed = %q", got)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("the interrupted install's copy is still there: %v", err)
	}
}

// Two copies of one build in flight never share a staging directory, so
// neither can move the other's half-copied build into place.
func TestPlaceStagesEachCopyApart(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "inkunactl")
	first, err := os.MkdirTemp(dir, stagingPattern(destination))
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.MkdirTemp(dir, stagingPattern(destination))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("both copies stage in %s", first)
	}
}

// A folder named like a glob pattern is only a folder: Place works in it,
// and leaves alone a folder the pattern would match. As a glob, "a[b]"
// matches "ab".
func TestPlaceTreatsGlobCharactersAsPlainNames(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "a[b]", "inkunactl")
	other := filepath.Join(root, "ab", ".inkunactl.new-123", "inkunactl")
	writeFile(t, other, "another install's copy")
	source := filepath.Join(root, "build")
	writeFile(t, source, "new")
	if _, err := Place(source, destination); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, destination); got != "new" {
		t.Fatalf("installed = %q", got)
	}
	if got := readFile(t, other); got != "another install's copy" {
		t.Fatalf("the other folder's copy = %q", got)
	}
	if _, err := Place(source, filepath.Join(root, "a[b", "inkunactl")); err != nil {
		t.Fatalf("a folder with an unclosed [: %v", err)
	}
}

func TestSwapPreviousSwapsTheBuildsBothWays(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "Inkuna.app")
	for _, build := range []string{"one", "two"} {
		source := filepath.Join(dir, "build-"+build, "Inkuna.app")
		writeFile(t, filepath.Join(source, "Contents", "MacOS", "Inkuna"), build)
		if _, err := Place(source, destination); err != nil {
			t.Fatal(err)
		}
	}
	binary := func(path string) string {
		return readFile(t, filepath.Join(path, "Contents", "MacOS", "Inkuna"))
	}
	for _, want := range []string{"one", "two", "one"} {
		if err := SwapPrevious(destination); err != nil {
			t.Fatal(err)
		}
		other := map[string]string{"one": "two", "two": "one"}[want]
		if binary(destination) != want || binary(destination+PreviousSuffix) != other {
			t.Fatalf("installed %q, previous %q; want %q, %q", binary(destination), binary(destination+PreviousSuffix), want, other)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 4 {
		t.Fatalf("the folder holds %v, want the two builds and the two sources", entries)
	}
}

func TestSwapPreviousNeedsAPreviousBuild(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "inkunactl")
	writeFile(t, destination, "installed")
	if err := SwapPrevious(destination); err == nil {
		t.Fatal("swapping with no previous build did not fail")
	}
	if got := readFile(t, destination); got != "installed" {
		t.Fatalf("installed = %q", got)
	}
	// With only the previous build, it goes back into place.
	if err := os.Rename(destination, destination+PreviousSuffix); err != nil {
		t.Fatal(err)
	}
	if err := SwapPrevious(destination); err != nil || readFile(t, destination) != "installed" {
		t.Fatalf("err %v", err)
	}
}
