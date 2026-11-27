package sources

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/pkg/extension"
)

func TestASnapshotIsKeptPerSourceAndRepository(t *testing.T) {
	dataDir := t.TempDir()
	// Paths as the platform writes them: the cache keeps each cleaned, which
	// on Windows turns slashes into backslashes.
	apiRepository, webRepository := filepath.FromSlash("/work/api"), filepath.FromSlash("/work/web")
	if entry, found, err := Load(dataDir, "acme/source", apiRepository); err != nil || found {
		t.Fatalf("Load before any save = %+v, %v, %v", entry, found, err)
	}
	fetched := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	api := Entry{Source: "acme/source", Repository: apiRepository + string(filepath.Separator), FetchedAt: fetched, Snapshot: extension.Snapshot{Version: "v1", Definitions: []extension.Definition{
		{Kind: extension.KindSkill, Name: "api-style", Mode: extension.ModeCatalog, Files: map[string][]byte{"SKILL.md": []byte("body")}},
	}}}
	for _, entry := range []Entry{
		api,
		{Source: "acme/source-2", Repository: apiRepository, FetchedAt: fetched, Snapshot: extension.Snapshot{Version: "w1"}},
		{Source: "acme/source", Repository: webRepository, FetchedAt: fetched, Snapshot: extension.Snapshot{Version: "x1"}},
	} {
		if err := Save(dataDir, entry); err != nil {
			t.Fatal(err)
		}
	}
	// A later snapshot replaces the one before it.
	api.Snapshot.Version = "v2"
	if err := Save(dataDir, api); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := Load(dataDir, "acme/source", apiRepository)
	api.Repository = apiRepository
	if err != nil || !found || !reflect.DeepEqual(loaded, api) {
		t.Fatalf("Load = %+v, %v, %v; want %+v", loaded, found, err, api)
	}
	entries, err := ForRepository(dataDir, apiRepository)
	if err != nil || len(entries) != 2 || entries[0].Source != "acme/source" || entries[0].Snapshot.Version != "v2" || entries[1].Source != "acme/source-2" {
		t.Fatalf("ForRepository = %+v, %v", entries, err)
	}
	files, err := os.ReadDir(Directory(dataDir))
	if err != nil || len(files) != 3 {
		t.Fatalf("the directory holds %v, %v; want one file per source and repository, and no temporary file", files, err)
	}
	if entries, err := ForRepository(filepath.Join(dataDir, "none"), apiRepository); err != nil || entries != nil {
		t.Fatalf("ForRepository without a directory = %+v, %v", entries, err)
	}
}

func TestNotesSayHowOldAKeptSnapshotIs(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 30, 0, 0, time.UTC)
	reading := Read([]State{
		{Source: "acme/source", Held: true, Cached: true, Unreachable: "it is down", FetchedAt: now.Add(-3*time.Hour - 5*time.Minute)},
		{Source: "acme/source-2", Unreachable: "it is down"},
	})
	notes := reading.Notes(now)
	want := []string{
		`Definition source "acme/source" could not be reached (it is down), so the plan used its snapshot from 2026-11-20 09:25 UTC, 3h 5min old.`,
		`Definition source "acme/source-2" could not be reached (it is down) and has no snapshot of this project kept, so it adds nothing.`,
	}
	if !reflect.DeepEqual(notes, want) {
		t.Fatalf("Notes = %q, want %q", notes, want)
	}
	if snapshots := reading.Snapshots(); len(snapshots) != 1 || !snapshots[0].Cached {
		t.Fatalf("Snapshots = %+v", snapshots)
	}
	if reading.Refusal() != nil {
		t.Fatalf("Refusal = %v", reading.Refusal())
	}
}

func TestOfTwoCatalogDefinitionsWithOneNameTheOneLeftOutIsReported(t *testing.T) {
	skill := func(body string) extension.Definition {
		return extension.Definition{Kind: extension.KindSkill, Name: "api-style", Mode: extension.ModeCatalog, Files: map[string][]byte{
			"SKILL.md": []byte("---\nname: api-style\ndescription: How this team writes APIs.\n---\n\n" + body + "\n"),
		}}
	}
	broken := extension.Definition{Kind: extension.KindSkill, Name: "api-style", Mode: extension.ModeCatalog, Files: map[string][]byte{"SKILL.md": []byte("no frontmatter")}}
	for _, tc := range []struct {
		name        string
		definitions []extension.Definition
		want        []string
	}{
		{"both load", []extension.Definition{skill("First."), skill("Second.")}, []string{StatusOffered, StatusFails}},
		// The second is offered, since the first did not take the name.
		{"the first fails", []extension.Definition{broken, skill("Second.")}, []string{StatusFails, StatusOffered}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reading := Read([]State{{Source: "acme/source", Held: true, Snapshot: extension.Snapshot{Definitions: tc.definitions}}})
			var got []string
			for _, definition := range reading.Reports[0].Definitions {
				got = append(got, definition.Status)
				if definition.Status == StatusFails && definition.Reason == "" {
					t.Fatalf("a definition left out gives no reason: %+v", definition)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("statuses = %v, want %v", got, tc.want)
			}
		})
	}
}
