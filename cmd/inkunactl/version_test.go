package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/storage"
)

// The test binary has no stamps, so it reports what `go run` does.
func TestVersionPrintsDevWithoutStamps(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"version"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "version   dev\ncommit    dev\nmigration "+storage.LatestMigration()+"\n"; got != want {
		t.Fatalf("version printed %q, want %q", got, want)
	}
}

func TestVersionJSON(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"version", "--json"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatalf("version --json printed %q: %v", output.String(), err)
	}
	if len(fields) != 3 || fields["version"] != "dev" || fields["commit"] != "dev" || fields["migration"] != storage.LatestMigration() {
		t.Fatalf("version --json = %v, want exactly version and commit, both dev, and the newest migration", fields)
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	for _, args := range [][]string{{"version", "extra"}, {"version", "--yaml"}} {
		err := run(args, nil, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), versionUsage) {
			t.Fatalf("%v: want the usage, got %v", args, err)
		}
	}
}
