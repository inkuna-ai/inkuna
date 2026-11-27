package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

// fakeCLI writes a script that records its arguments and stdin, then answers
// in prose followed by a JSON object, the way a plain-text CLI would.
func fakeCLI(t *testing.T) (binary, record string) {
	t.Helper()
	directory := t.TempDir()
	binary, record = filepath.Join(directory, "fourth-cli"), filepath.Join(directory, "invocation.txt")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'fourth-cli 1.2.3'; exit 0; fi\n" +
		"for arg in \"$@\"; do printf 'arg=%s\\n' \"$arg\" >> '" + record + "'; done\ncat > /dev/null\n" +
		"printf '%s\\n' 'I looked around.' '{\"summary\":\"inspected\"}'\n"
	binary = fakecli.Write(t, binary, script)
	return binary, record
}

// The acceptance case for declared runtimes: a fourth runtime added purely
// through the bindings staffs a read-only stage, from plan to completed run.
func TestARuntimeAddedThroughTheBindingsStaffsAReadOnlyStage(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	t.Setenv(config.EnvConfigFile, "")
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repositoryPath, &workflowRuntime{})
	binary, record := fakeCLI(t)
	declaration, err := json.Marshal(config.RuntimeConfig{
		Label: "Fourth CLI", Binary: binary, Stdin: "prompt", Output: "text",
		Args: []string{"run", "--model", "{model}"}, ReadOnlyArgs: []string{"--read-only"},
	})
	if err != nil {
		t.Fatal(err)
	}

	runtimes, err := service.SaveRuntime("fourth", string(declaration))
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 4 || runtimes[3].Name != "fourth" || runtimes[3].Label != "Fourth CLI" || runtimes[3].BuiltIn || runtimes[3].CanWrite || runtimes[3].Binary != binary {
		t.Fatalf("Runtimes = %#v", runtimes)
	}
	if runtimes[0].Name != domain.RuntimeClaude || !runtimes[0].BuiltIn || !runtimes[0].CanWrite || runtimes[0].Preset != domain.RuntimeClaude {
		t.Fatalf("built-ins come first and keep their adapters: %#v", runtimes[0])
	}
	if probe, err := service.TestRuntime("fourth"); err != nil || !probe.Available || probe.Version != "fourth-cli 1.2.3" || probe.Path != binary {
		t.Fatalf("TestRuntime = %#v, %v", probe, err)
	}
	if _, err := service.TestRuntime("fifth"); err == nil {
		t.Fatal("testing an undeclared runtime must be an error")
	}

	// The new runtime is a valid project default; an undeclared one is not.
	if _, err := service.AddProject("Undeclared", branchingRepository(t, ""), "fifth"); err == nil || !strings.Contains(err.Error(), "invalid runtime") {
		t.Fatalf("an undeclared runtime must not become a project default: %v", err)
	}
	project, err := service.AddProject("Fourth", branchingRepository(t, ""), "fourth")
	if err != nil || project.DefaultRuntime != "fourth" {
		t.Fatalf("AddProject = %#v, %v", project, err)
	}

	// Run a read-only step on it. Approval may change a step's runtime, which
	// is how the workflow view moves the planned step onto the new runtime.
	spec := workflowTestSpec(t, service)
	spec.Steps[0].Runtime = "fourth"
	spec.Steps[0].ModelSelection = &domain.ModelSelection{Source: "policy", Rationale: "no bands for fourth"}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	if run.Runtime != "fourth" || len(run.Steps) != 1 || run.Steps[0].State != "completed" {
		t.Fatalf("run = %#v", run)
	}
	invocation, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(invocation); got != "arg=run\narg=--read-only\n" {
		t.Fatalf("the CLI was started with:\n%s", got)
	}

	// A writable step is refused for a runtime that declares no write arguments.
	writable := workflowTestSpec(t, service)
	writable.Steps[0].Permission, writable.Steps[0].AllowedPaths, writable.Steps[0].Agent = domain.PermissionWorktree, []string{"."}, "implementer"
	writable = savePlanned(t, service, writable)
	writable.Steps[0].Runtime = "fourth"
	if encoded, err = json.Marshal(writable); err != nil {
		t.Fatal(err)
	}
	if _, err = service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("a write step on a read-only runtime must be refused at start: %v", err)
	}
	writable.Steps[0].Runtime = "fifth"
	if encoded, err = json.Marshal(writable); err != nil {
		t.Fatal(err)
	}
	if _, err = service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("an undeclared runtime must be refused at start: %v", err)
	}

	// A runtime a connected project defaults to stays until the project
	// goes, since inkuna.yaml lists the project with it.
	if _, err := service.DeleteRuntime("fourth"); err == nil || !strings.Contains(err.Error(), "invalid runtime") {
		t.Fatalf("a runtime a project uses must not be removed: %v", err)
	}
	if _, err := service.RemoveProject(project.ID, false); err != nil {
		t.Fatal(err)
	}
	// Removing it takes it out of the registry; a built-in only resets.
	if runtimes, err = service.DeleteRuntime("fourth"); err != nil || len(runtimes) != 3 {
		t.Fatalf("DeleteRuntime = %#v, %v", runtimes, err)
	}
	if _, err := service.DeleteRuntime("fourth"); err == nil {
		t.Fatal("removing a runtime twice must be an error")
	}
	// A built-in is only ever reset: its entry goes back to the default one.
	if runtimes, err = service.SaveRuntime("codex", `{"binary":"/opt/codex"}`); err != nil || !runtimes[1].Customized || runtimes[1].Binary != "/opt/codex" {
		t.Fatalf("customized built-in = %#v, %v", runtimes, err)
	}
	if runtimes, err = service.DeleteRuntime("codex"); err != nil || len(runtimes) != 3 || runtimes[1].Customized || runtimes[1].Binary != "codex" || runtimes[0].Customized {
		t.Fatalf("reset built-in = %#v, %v", runtimes, err)
	}
	if _, err := service.SaveRuntime("bypass", `{"binary":"x","stdin":"prompt","output":"text","args":["--yolo"],"readOnlyArgs":["--plan"]}`); err == nil || !strings.Contains(err.Error(), "permission checks") {
		t.Fatalf("a permission bypass flag must be refused: %v", err)
	}
	if _, err := service.SaveRuntime("extra", `{"binary":"x","shell":true}`); err == nil {
		t.Fatal("unknown fields must be refused")
	}
}
