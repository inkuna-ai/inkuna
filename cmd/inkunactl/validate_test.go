package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// isolateStages is isolate with no stage library directory from the
// environment, so the rules commands read none of the machine running the
// tests; it returns the data directory's default one.
func isolateStages(t *testing.T) string {
	t.Helper()
	isolate(t)
	t.Setenv("INKUNA_STAGE_DIRECTORIES", "")
	return filepath.Join(os.Getenv("INKUNA_DATA_DIR"), "stages")
}

func TestValidateChecksTheMCPServersAnAgentOrStageNames(t *testing.T) {
	isolateStages(t)
	directory := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	agentFile := func(mcp string) string {
		return write("archivist.md", "---\nname: archivist\ndescription: Reads the archive\nmcp: "+mcp+"\n---\n\nRead the archive.\n")
	}
	rulesFile := func(mcp string) string {
		return write("rules.yaml", "version: 1\nrules: []\ndefault:\n  stages:\n    - id: triage\n      kind: agent\n      agent: auto\n      mcp: "+mcp+"\n    - id: approval\n      kind: approval\n      dependsOn: [triage]\n")
	}
	var output bytes.Buffer
	if err := run([]string{"agent", "validate", agentFile("[docs-search]")}, nil, &output); err != nil || !strings.Contains(output.String(), `"mcp": [`) {
		t.Fatalf("a valid agent must pass and show its servers: %v\n%s", err, output.String())
	}
	if err := run([]string{"agent", "validate", agentFile("[Docs]")}, nil, &output); err == nil || !strings.Contains(err.Error(), "not a valid server name") {
		t.Fatalf("an invalid server name must fail: %v", err)
	}
	output.Reset()
	if err := run([]string{"rules", "validate", rulesFile("[linear]")}, nil, &output); err != nil || !strings.Contains(output.String(), `"mcp": [`) {
		t.Fatalf("valid rules must pass and show the stage's servers: %v\n%s", err, output.String())
	}
	if err := run([]string{"rules", "validate", rulesFile("linear")}, nil, &output); err == nil {
		t.Fatal("a stage's mcp must be a list")
	}
}

// rules show prints uses as written and, with --expanded, as the stages
// planning sees; rules validate names a use whose name matches nothing.
func TestRulesShowExpandsSharedStagesAndValidateNamesBadReferences(t *testing.T) {
	isolateStages(t)
	repository := t.TempDir()
	path := filepath.Join(repository, ".inkuna", "rules.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(use string) {
		t.Helper()
		content := "version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [task, ci]\nrules: []\ndefault:\n  stages:\n    - id: implement\n      kind: agent\n      agent: auto\n    - use: " + use + "\n      dependsOn: [implement]\n    - id: approval\n      kind: approval\n      dependsOn: [verify]\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("verify")
	var output bytes.Buffer
	if err := run([]string{"rules", "show", repository}, nil, &output); err != nil || !strings.Contains(output.String(), `"use": "verify"`) {
		t.Fatalf("show prints the rules as written: %v\n%s", err, output.String())
	}
	output.Reset()
	if err := run([]string{"rules", "show", "--expanded", repository}, nil, &output); err != nil {
		t.Fatal(err)
	}
	expanded := output.String()
	if strings.Contains(expanded, `"use"`) || !strings.Contains(expanded, `"id": "verify"`) || !strings.Contains(expanded, `"stage": "verify"`) || !strings.Contains(expanded, `"scope": "repository"`) {
		t.Fatalf("show --expanded prints plain stages with where they came from:\n%s", expanded)
	}
	write("verfy")
	if err := run([]string{"rules", "validate", path}, nil, &output); err == nil || !strings.Contains(err.Error(), `entry 2 (use "verfy"): no shared stage or group is named "verfy"`) {
		t.Fatalf("validate names the bad reference: %v", err)
	}
	if err := run([]string{"rules", "show", "--expanded"}, nil, &output); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("show without a repository is a usage error: %v", err)
	}
}

// stages list shows every definition with its scope and file, marking the
// overridden ones, and the library's problems; rules show and validate
// resolve uses against the global library.
func TestStagesListShowsScopesOverridesAndProblems(t *testing.T) {
	stages := isolateStages(t)
	if err := os.MkdirAll(stages, 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(stages, "checks.yaml")
	if err := os.WriteFile(global, []byte("version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [make, check]\n  lint:\n    kind: evaluation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stages, "broken.yaml"), []byte("version: 2\nrules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := t.TempDir()
	path := filepath.Join(repository, ".inkuna", "rules.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("version: 2\nstages:\n  lint:\n    kind: evaluation\n    commands:\n      - [task, lint]\nrules: []\ndefault:\n  stages:\n    - use: lint\n    - use: verify-and-approve\n      dependsOn: [lint]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"stages", "list", repository}, nil, &output); err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Definitions []struct {
			Name, Scope, File string
			Overridden        bool
		}
		Problems []struct{ File, Message string }
	}
	if err := json.Unmarshal(output.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, definition := range listed.Definitions {
		got[definition.Scope+":"+definition.Name] = fmt.Sprintf("%s overridden=%v", filepath.Base(definition.File), definition.Overridden)
	}
	want := map[string]string{
		"builtin:approval": ". overridden=false", "builtin:profile": ". overridden=false", "builtin:verify-and-approve": ". overridden=false",
		"builtin:verify": ". overridden=true", "global:verify": "checks.yaml overridden=false",
		"global:lint": "checks.yaml overridden=true", "repository:lint": "rules.yaml overridden=false",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stages list = %v, want %v", got, want)
	}
	if len(listed.Problems) != 1 || filepath.Base(listed.Problems[0].File) != "broken.yaml" {
		t.Fatalf("problems = %+v", listed.Problems)
	}
	output.Reset()
	if err := run([]string{"stages", "list"}, nil, &output); err != nil || strings.Contains(output.String(), `"repository"`) {
		t.Fatalf("stages list without a repository: %v\n%s", err, output.String())
	}
	output.Reset()
	// The output is JSON, which escapes a Windows path's backslashes.
	encoded, _ := json.Marshal(global)
	if err := run([]string{"rules", "show", "--expanded", repository}, nil, &output); err != nil || !strings.Contains(output.String(), `"make"`) || !strings.Contains(output.String(), string(encoded)) {
		t.Fatalf("rules show --expanded uses the global verify: %v\n%s", err, output.String())
	}
	// Rules that use a name the library cannot resolve are one more problem,
	// and the library is still listed.
	if err := os.WriteFile(filepath.Join(stages, "duplicate.yaml"), []byte("version: 2\nstages:\n  verify:\n    kind: evaluation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := run([]string{"stages", "list", repository}, nil, &output); err != nil {
		t.Fatalf("rules that do not load must not stop the listing: %v", err)
	}
	listed.Definitions, listed.Problems = nil, nil
	if err := json.Unmarshal(output.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Definitions) == 0 || len(listed.Problems) != 3 || listed.Problems[2].File != path || !strings.Contains(listed.Problems[2].Message, "more than one global stage library file") {
		t.Fatalf("problems with rules that do not load = %+v", listed.Problems)
	}
	if err := run([]string{"stages", "show"}, nil, &output); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("an unknown stages command is a usage error: %v", err)
	}
}
