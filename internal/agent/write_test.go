package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestWriteRoundTripsADefinition(t *testing.T) {
	directory := filepath.Join(t.TempDir(), ".inkuna", "agents")
	want := Definition{
		Name: "security-reviewer", Description: "Reviews changes for security issues: injection, secrets, authz.",
		Runtime: domain.RuntimeClaude, Model: "sonnet", ReasoningEffort: "high", Permission: domain.PermissionReadOnly,
		Skills: []string{"threat-modeling"}, TaskKinds: []domain.TaskKind{domain.TaskReview}, Tags: []string{"security"},
		MaxTurns: 3, Secrets: []string{"SNYK_TOKEN"},
		Instructions: "Check for injection.\n\n---\n\nA horizontal rule in the body must survive.",
	}
	path, err := Write(directory, want)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "security-reviewer.md" {
		t.Fatalf("file name must equal the agent name, got %s", path)
	}
	got, err := Load(path, SourceRepository)
	if err != nil {
		t.Fatal(err)
	}
	want.Source, want.Path = SourceRepository, path
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the definition:\n got %#v\nwant %#v", got, want)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(directory, temporaryPattern)); len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
	// Windows reports 0666 for any writable file, so the mode is only checked elsewhere.
	if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o644) {
		t.Fatalf("definition should be an ordinary 0644 source file: %v %v", info, err)
	}
}

func TestWriteRefusesInvalidAndReservedDefinitions(t *testing.T) {
	directory := t.TempDir()
	valid := Definition{Name: "helper", Description: "Helps", Permission: domain.PermissionReadOnly, Instructions: "Help."}
	for name, broken := range map[string]Definition{
		"path traversal": {Name: "../escape", Description: "x", Permission: domain.PermissionReadOnly, Instructions: "x"},
		"reserved human": {Name: Human, Description: "x", Permission: domain.PermissionReadOnly, Instructions: "x"},
		"reserved auto":  {Name: Auto, Description: "x", Permission: domain.PermissionReadOnly, Instructions: "x"},
		"no body":        {Name: "helper", Description: "x", Permission: domain.PermissionReadOnly},
		"bad permission": {Name: "helper", Description: "x", Permission: "root", Instructions: "x"},
	} {
		if _, err := Write(directory, broken); err == nil {
			t.Fatalf("%s: an invalid definition was written", name)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected writes left files behind: %v %v", entries, err)
	}
	if _, err := Write(directory, valid); err != nil {
		t.Fatal(err)
	}
	if err := Remove(directory, "../helper"); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("Remove must reject unsafe names: %v", err)
	}
	if err := Remove(directory, "helper"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(directory, "helper"); err == nil {
		t.Fatal("removing a missing definition must be an error")
	}
}
