package diff

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The fixtures are one commit made by testdata/make.sh; see there for what
// each file exercises.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseRawReadsEveryKindOfChange(t *testing.T) {
	entries, err := parseRaw(fixture(t, "raw.z"))
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]File, len(entries))
	for _, entry := range entries {
		// A file is reviewed against its newer blob, and a deleted one
		// against the blob it had.
		want := entry.newBlob
		if entry.file.Status == StatusDeleted {
			want = entry.oldBlob
		}
		if entry.file.Blob != want || missingBlob(want) {
			t.Errorf("%s: blob %q, want %q", entry.file.Path, entry.file.Blob, want)
		}
		entry.file.Blob = ""
		got[entry.file.Path] = entry.file
	}
	want := map[string]File{
		"crlf.txt":            {Path: "crlf.txt", Status: StatusModified, OldMode: "100644", NewMode: "100644"},
		"docs/café/naïve.md":  {Path: "docs/café/naïve.md", Status: StatusModified, OldMode: "100644", NewMode: "100644"},
		"docs/remove.txt":     {Path: "docs/remove.txt", Status: StatusDeleted, OldMode: "100644"},
		`docs/say "hi".txt`:   {Path: `docs/say "hi".txt`, Status: StatusAdded, NewMode: "100644"},
		"docs/with space.txt": {Path: "docs/with space.txt", Status: StatusAdded, NewMode: "100644"},
		"logo.bin":            {Path: "logo.bin", Status: StatusModified, OldMode: "100644", NewMode: "100644"},
		"run.sh":              {Path: "run.sh", Status: StatusModified, OldMode: "100644", NewMode: "100755"},
		"src/new_name.go":     {Path: "src/new_name.go", OldPath: "src/old_name.go", Status: StatusRenamed, Similarity: 77, OldMode: "100644", NewMode: "100644"},
		"tail.txt":            {Path: "tail.txt", Status: StatusModified, OldMode: "100644", NewMode: "100644"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("raw entries\n got %+v\nwant %+v", got, want)
	}
	for _, entry := range entries {
		if entry.file.Status == StatusAdded && !missingBlob(entry.oldBlob) || entry.file.Status == StatusDeleted && !missingBlob(entry.newBlob) {
			t.Errorf("%s: blobs %s %s", entry.file.Path, entry.oldBlob, entry.newBlob)
		}
	}
}

func TestParseNumstatCountsLinesAndSpotsBinaryFiles(t *testing.T) {
	got, err := parseNumstat(fixture(t, "numstat.z"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]counts{
		"crlf.txt": {1, 1, false}, "docs/café/naïve.md": {1, 1, false}, "docs/remove.txt": {0, 3, false},
		`docs/say "hi".txt`: {1, 0, false}, "docs/with space.txt": {1, 0, false}, "logo.bin": {0, 0, true},
		"run.sh": {0, 0, false}, "src/new_name.go": {1, 1, false}, "tail.txt": {1, 1, false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("numstat\n got %+v\nwant %+v", got, want)
	}
}

func TestParsePatchReadsEachFilesHeaderAndHunks(t *testing.T) {
	sections, err := parsePatch(fixture(t, "all.patch"))
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]section, len(sections))
	for _, section := range sections {
		byPath[section.path()] = section
	}
	if len(byPath) != 9 {
		t.Fatalf("got %d sections: %v", len(byPath), keys(byPath))
	}

	t.Run("rename with edits", func(t *testing.T) {
		renamed := byPath["src/new_name.go"]
		if renamed.oldPath != "src/old_name.go" || len(renamed.hunks) != 1 {
			t.Fatalf("rename: %+v", renamed)
		}
		hunk := renamed.hunks[0]
		if hunk.OldStart != 1 || hunk.OldLines != 7 || hunk.NewStart != 1 || hunk.NewLines != 7 || len(hunk.Lines) != 8 {
			t.Fatalf("hunk range %+v", hunk)
		}
		changed := linesOf(hunk, LineDeleted, LineAdded)
		want := []Line{
			{Kind: LineDeleted, Old: 4, Text: "\treturn \"Hello, \" + name"},
			{Kind: LineAdded, New: 4, Text: "\treturn \"Hello there, \" + name"},
		}
		if !reflect.DeepEqual(changed, want) {
			t.Fatalf("changed lines\n got %+v\nwant %+v", changed, want)
		}
	})
	t.Run("binary file", func(t *testing.T) {
		if binary := byPath["logo.bin"]; !binary.binary || len(binary.hunks) != 0 {
			t.Fatalf("binary: %+v", binary)
		}
	})
	t.Run("deletion", func(t *testing.T) {
		deleted := byPath["docs/remove.txt"]
		if deleted.newPath != "" || deleted.oldPath != "docs/remove.txt" || len(deleted.hunks) != 1 || len(deleted.hunks[0].Lines) != 3 {
			t.Fatalf("deletion: %+v", deleted)
		}
		if last := deleted.hunks[0].Lines[2]; last.Kind != LineDeleted || last.Old != 3 || last.New != 0 || last.Text != "line three" {
			t.Fatalf("last deleted line %+v", last)
		}
	})
	t.Run("mode-only change", func(t *testing.T) {
		if mode := byPath["run.sh"]; mode.oldPath != "run.sh" || mode.newPath != "run.sh" || len(mode.hunks) != 0 || mode.binary {
			t.Fatalf("mode change: %+v", mode)
		}
	})
	t.Run("no trailing newline", func(t *testing.T) {
		lines := byPath["tail.txt"].hunks[0].Lines
		want := []Line{
			{Kind: LineContext, Old: 1, New: 1, Text: "first"},
			{Kind: LineDeleted, Old: 2, Text: "last without newline", NoNewline: true},
			{Kind: LineAdded, New: 2, Text: "last still without newline", NoNewline: true},
		}
		if !reflect.DeepEqual(lines, want) {
			t.Fatalf("lines\n got %+v\nwant %+v", lines, want)
		}
	})
	t.Run("CRLF line endings", func(t *testing.T) {
		lines := byPath["crlf.txt"].hunks[0].Lines
		want := []Line{
			{Kind: LineContext, Old: 1, New: 1, Text: "alpha", CRLF: true},
			{Kind: LineDeleted, Old: 2, Text: "beta", CRLF: true},
			{Kind: LineAdded, New: 2, Text: "BETA", CRLF: true},
			{Kind: LineContext, Old: 3, New: 3, Text: "gamma", CRLF: true},
		}
		if !reflect.DeepEqual(lines, want) {
			t.Fatalf("lines\n got %+v\nwant %+v", lines, want)
		}
	})
	t.Run("unicode and quoted paths", func(t *testing.T) {
		for _, path := range []string{"docs/café/naïve.md", "docs/with space.txt", `docs/say "hi".txt`} {
			section, ok := byPath[path]
			if !ok || len(section.hunks) != 1 {
				t.Fatalf("%s: %+v (have %v)", path, section, keys(byPath))
			}
		}
		if added := byPath[`docs/say "hi".txt`]; added.oldPath != "" || !reflect.DeepEqual(added.hunks[0].Lines[0], Line{Kind: LineAdded, New: 1, Text: "quoted"}) {
			t.Fatalf("quoted added file: %+v", added)
		}
	})
}

func TestParsePatchRefusesLinesOutsideTheirHunk(t *testing.T) {
	patch := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n+c\n"
	if _, err := parsePatch([]byte(patch)); err == nil || !strings.Contains(err.Error(), "after a hunk's last line") {
		t.Fatalf("got %v, want an error about the extra line", err)
	}
}

func TestParseRawRefusesWhatItCannotRead(t *testing.T) {
	for _, data := range []string{"M\x00path\x00", ":100644 100644 a b X\x00path\x00", ":100644 100644 a b R100\x00old\x00"} {
		if _, err := parseRaw([]byte(data)); err == nil {
			t.Errorf("%q: want an error", data)
		}
	}
}

func linesOf(hunk Hunk, kinds ...LineKind) []Line {
	var lines []Line
	for _, line := range hunk.Lines {
		for _, kind := range kinds {
			if line.Kind == kind {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func keys[V any](values map[string]V) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	return names
}
