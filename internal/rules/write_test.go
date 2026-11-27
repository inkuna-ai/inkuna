package rules

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestWriteRoundTripsTheDefaultDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".inkuna", "rules.yaml")
	want := Default().WithLibrary(Builtin())
	if err := Write(path, want); err != nil {
		t.Fatal(err)
	}
	got, exists, err := LoadPath(path)
	if err != nil || !exists {
		t.Fatalf("LoadPath = exists %v, err %v", exists, err)
	}
	got.Source, want.Source = "", ""
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the document:\n got %#v\nwant %#v", got, want)
	}
	assertNoTemporaryFiles(t, filepath.Dir(path))
	// Windows files have no Unix permissions to check.
	if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o644) {
		t.Fatalf("rules file should be an ordinary 0644 source file: %v %v", info, err)
	}
}

func TestWriteRefusesInvalidDocumentsAndKeepsTheOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := Write(path, Default()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A flow that does not end with an approval must never reach disk.
	broken := Default()
	broken.Default = &Flow{Stages: broken.Default.Stages[:len(broken.Default.Stages)-1]}
	if err := Write(path, broken); err == nil {
		t.Fatal("a flow without a final approval was written")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("a rejected write changed the file: %v", err)
	}
	assertNoTemporaryFiles(t, filepath.Dir(path))

	// A rename that cannot succeed (the target is a non-empty directory)
	// must still clean up its temporary file.
	blocked := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.MkdirAll(filepath.Join(blocked, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(blocked, Default()); err == nil {
		t.Fatal("writing over a non-empty directory must fail")
	}
	assertNoTemporaryFiles(t, filepath.Dir(blocked))
}

func TestMarshalKeepsSourceOutOfTheFile(t *testing.T) {
	document := Default()
	document.Source = "/somewhere/rules.yaml"
	data, err := Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/somewhere") {
		t.Fatalf("source leaked into YAML:\n%s", data)
	}
	if _, err := Parse(string(data)); err != nil {
		t.Fatalf("marshalled YAML does not parse: %v", err)
	}
}

func assertNoTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	leftovers, err := filepath.Glob(filepath.Join(directory, temporaryPattern))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}
