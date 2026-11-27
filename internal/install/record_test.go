package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordRoundTrip(t *testing.T) {
	path := RecordPath(t.TempDir())
	if _, exists, err := ReadRecord(path); exists || err != nil {
		t.Fatalf("a missing record: exists %v, err %v", exists, err)
	}
	want := Record{Source: "/src/inkuna", Version: "abc1234", Commit: "abc1234def", App: "/Applications/Inkuna.app", CLI: "/home/ada/.local/bin/inkunactl"}
	if err := WriteRecord(path, want); err != nil {
		t.Fatal(err)
	}
	got, exists, err := ReadRecord(path)
	if err != nil || !exists || got != want {
		t.Fatalf("read %+v, %v, %v; want %+v", got, exists, err, want)
	}
}

func TestPreviousRecordIsBesideTheRecord(t *testing.T) {
	if got, want := PreviousRecordPath("/data"), filepath.Join("/data", "install.previous.json"); got != want {
		t.Fatalf("PreviousRecordPath = %s, want %s", got, want)
	}
}

func TestReadRecordIsStrict(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"an unknown key": `{"source": "/src", "channel": "beta"}`,
		"two objects":    `{"source": "/src"} {"source": "/other"}`,
		"not JSON":       `source: /src`,
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadRecord(path); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
