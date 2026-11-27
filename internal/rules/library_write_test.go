package rules

import (
	"os"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

const checksLibrary = `version: 2
# Checks every repository runs.
stages:
  # The repository's own CI.
  verify:
    kind: evaluation
    commands:
      - [task, ci]
  lint:
    kind: evaluation
    commands:
      - [task, lint]
groups:
  gate:
    stages:
      - use: verify
        continueOnFailure: true
      - id: approval
        kind: approval
        dependsOn: [verify]
`

// Editing a global definition rewrites only that definition, keeps the
// file's other definitions and comments, and is used by the next load.
func TestWriteLibraryDefinitionReplacesOneDefinitionInPlace(t *testing.T) {
	directory := t.TempDir()
	path := writeLibrary(t, directory, "checks.yaml", checksLibrary)
	library := LoadLibrary([]string{directory})
	if len(library.Problems) > 0 {
		t.Fatalf("library problems: %+v", library.Problems)
	}
	stage := Stage{Kind: domain.StepEvaluation, Commands: [][]string{{"task", "deps"}, {"task", "ci"}}, ContinueOnFailure: true}
	written, err := WriteLibraryDefinition(Definition{Name: "verify", Scope: ScopeGlobal, File: path, Stage: &stage}, library)
	if err != nil {
		t.Fatalf("WriteLibraryDefinition: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(data) {
		t.Fatalf("WriteLibraryDefinition returned other content than it wrote:\n%s", written)
	}
	content := string(data)
	for _, want := range []string{"# Checks every repository runs.", "# The repository's own CI.", "- [task, deps]", "- [task, lint]", "continueOnFailure: true"} {
		if !strings.Contains(content, want) {
			t.Fatalf("written file lacks %q:\n%s", want, content)
		}
	}
	reloaded := LoadLibrary([]string{directory})
	if len(reloaded.Problems) > 0 {
		t.Fatalf("the written file has problems: %+v\n%s", reloaded.Problems, content)
	}
	verify, err := reloaded.Resolve("verify")
	if err != nil || len(verify.Stage.Commands) != 2 {
		t.Fatalf("verify after the edit = %+v, %v", verify.Stage, err)
	}
	gate, err := reloaded.Resolve("gate")
	if err != nil || !gate.Group.Stages[0].ContinueOnFailure || gate.Group.Stages[0].Use != "verify" {
		t.Fatalf("the gate group changed: %+v, %v", gate.Group, err)
	}
}

// What cannot be edited in place is refused with the reason, and the file
// is left as it was.
func TestWriteLibraryDefinitionRefusesWhatItCannotChange(t *testing.T) {
	directory := t.TempDir()
	path := writeLibrary(t, directory, "checks.yaml", checksLibrary)
	library := LoadLibrary([]string{directory})
	agentStage := Stage{Kind: domain.StepAgent, Agent: "auto"}
	evaluation := Stage{Kind: domain.StepEvaluation}
	group := Group{Stages: []Stage{{Use: "lint"}}}
	cases := map[string]struct {
		definition Definition
		says       string
	}{
		"a built-in definition": {
			Definition{Name: "approval", Scope: ScopeBuiltin, Stage: &Stage{Kind: domain.StepApproval}},
			`built-in stage "approval" is read-only; copy it to a repository's rules to change it there`,
		},
		"a name the file does not define": {
			Definition{Name: "review", Scope: ScopeGlobal, File: path, Stage: &evaluation},
			`does not define "review"`,
		},
		"a file Inkuna does not read": {
			Definition{Name: "verify", Scope: ScopeGlobal, File: path + ".bak", Stage: &evaluation},
			`does not define "verify" in a stage library directory Inkuna reads`,
		},
		"a stage made a group": {
			Definition{Name: "verify", Scope: ScopeGlobal, File: path, Group: &group},
			`"verify" is a stage in`,
		},
		"a definition that fails its checks": {
			Definition{Name: "lint", Scope: ScopeGlobal, File: path, Stage: &Stage{Kind: domain.StepAgent, Agent: "auto", Permission: "admin"}},
			`invalid permission "admin"`,
		},
		"a change that breaks a group using it": {
			Definition{Name: "verify", Scope: ScopeGlobal, File: path, Stage: &agentStage},
			`the change would break group "gate" in ` + path,
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := WriteLibraryDefinition(test.definition, library)
			if err == nil || !strings.Contains(err.Error(), test.says) {
				t.Fatalf("WriteLibraryDefinition error = %v, want it to say %q", err, test.says)
			}
			data, _ := os.ReadFile(path)
			if string(data) != checksLibrary {
				t.Fatalf("a refused edit changed the file:\n%s", data)
			}
		})
	}
}

// A file someone broke by hand since it loaded is not overwritten.
func TestWriteLibraryDefinitionRefusesAFileThatNoLongerParses(t *testing.T) {
	directory := t.TempDir()
	path := writeLibrary(t, directory, "checks.yaml", checksLibrary)
	library := LoadLibrary([]string{directory})
	writeLibrary(t, directory, "checks.yaml", "version: 2\nstages:\n  verify:\n    kind: evaluation\n    surprise: true\n")
	stage := Stage{Kind: domain.StepEvaluation}
	_, err := WriteLibraryDefinition(Definition{Name: "verify", Scope: ScopeGlobal, File: path, Stage: &stage}, library)
	if err == nil || !strings.Contains(err.Error(), "has a problem to fix first") {
		t.Fatalf("error = %v", err)
	}
}
