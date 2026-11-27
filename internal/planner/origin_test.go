package planner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// writeTestFile writes content to path, creating its directory.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Each planned step says which shared stage or group it came from, in which
// scope and file, and what the flow overrode; the plan keeps that through
// being saved and loaded again, whatever the library says afterwards. A
// repository definition replacing a library one is noted.
func TestPlannedStepsKeepWhereTheyCameFromThroughPersistence(t *testing.T) {
	directory := t.TempDir()
	library := filepath.Join(directory, "stages", "checks.yaml")
	writeTestFile(t, library, "version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [task, ci]\n")
	rulesPath := filepath.Join(directory, "repository", ".inkuna", "rules.yaml")
	writeTestFile(t, rulesPath, `version: 2
stages:
  approval:
    kind: approval
    review: true
rules: []
default:
  stages:
    - id: worker
      kind: agent
      agent: auto
    - use: verify
      dependsOn: [worker]
      commands:
        - [go, test, ./...]
    - use: verify-and-approve
      id: gate
      dependsOn: [verify]
`)
	document, _, err := rules.LoadPathWith(rulesPath, rules.LoadLibrary([]string{filepath.Dir(library)}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: document, Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]*domain.StepOrigin{
		"worker":      nil,
		"verify":      {Stage: "verify", Scope: rules.ScopeGlobal, File: library, Overrides: []string{"commands"}},
		"gate.verify": {Stage: "verify", Groups: []string{"verify-and-approve"}, Scope: rules.ScopeGlobal, File: library},
		"approval":    {Stage: "approval", Groups: []string{"verify-and-approve"}, Scope: rules.ScopeRepository, File: rulesPath},
	}
	check := func(label string, workflow domain.WorkflowSpec) {
		t.Helper()
		if len(workflow.Steps) != len(want) {
			t.Fatalf("%s: steps = %+v", label, workflow.Steps)
		}
		for _, step := range workflow.Steps {
			expected, ok := want[step.ID]
			if !ok || !reflect.DeepEqual(step.From, expected) {
				t.Errorf("%s: step %q came from %+v, want %+v", label, step.ID, step.From, expected)
			}
		}
	}
	check("planned", result.Workflow)
	notes := strings.Join(result.Notes, "\n")
	if !strings.Contains(notes, `Shared stage "approval" is this repository's own, which replaces the built-in one.`) {
		t.Fatalf("the repository's override is not noted: %v", result.Notes)
	}
	if strings.Contains(notes, `"verify" is this repository's own`) {
		t.Fatalf("a global definition is not the repository's own: %v", result.Notes)
	}

	store, err := storage.Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.UpsertProject(ctx, storage.Project{ID: "project", Name: "Project", Repository: filepath.Dir(filepath.Dir(rulesPath)), DefaultRuntime: domain.RuntimeClaude}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTask(ctx, "project", "planned", testTask(domain.TaskImplementation)); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkflow(ctx, "planned", result.Workflow); err != nil {
		t.Fatal(err)
	}
	// The library changes after planning; the plan still says what it
	// was planned from.
	writeTestFile(t, library, "version: 2\nstages: {}\n")
	reloaded, err := store.Workflow(ctx, result.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	check("reloaded", reloaded)
	if !slices.Equal(reloaded.Steps[1].From.Overrides, []string{"commands"}) {
		t.Fatalf("overrides lost: %+v", reloaded.Steps[1].From)
	}
}
