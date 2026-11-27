package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

// TestStepReadingADocRecordsOneReadWithItsTaskID runs a step through the real
// Codex adapter with a stand-in CLI that prints recorded-shape events: the
// shell reads docs/x.md (reported when it starts and again when it
// completes) and src/app.go, then hands off.
func TestStepReadingADocRecordsOneReadWithItsTaskID(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"docs/x.md": "# X\n", "src/app.go": "package app\n"} {
		path := filepath.Join(repositoryPath, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runGit(repositoryPath, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	handoff, err := json.Marshal(string(handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Read the docs"})))
	if err != nil {
		t.Fatal(err)
	}
	command := func(state, command string) string {
		item := map[string]any{"id": "item", "type": "command_execution", "command": command, "status": state}
		event := "item.started"
		if state == "completed" {
			item["exit_code"] = 0
			event = "item.completed"
		}
		line, err := json.Marshal(map[string]any{"type": event, "item": item})
		if err != nil {
			t.Fatal(err)
		}
		return string(line)
	}
	stream := command("in_progress", "/bin/zsh -lc 'cat docs/x.md'") + "\n" +
		command("completed", "/bin/zsh -lc 'cat docs/x.md'") + "\n" +
		command("completed", "/bin/zsh -lc 'cat src/app.go'") + "\n" +
		`{"type":"item.completed","item":{"id":"reply","type":"agent_message","text":` + string(handoff) + `}}` + "\n"
	tools := t.TempDir()
	events := filepath.Join(tools, "events.jsonl")
	if err := os.WriteFile(events, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(tools, "codex")
	binary = fakecli.Write(t, binary, "#!/bin/sh\ncat >/dev/null\ncat '"+events+"'\n")

	service := workflowTestService(t, repositoryPath, &workflowRuntime{})
	service.runtimes[domain.RuntimeCodex] = agentruntime.NewCodex(binary)
	spec := workflowTestSpec(t, service)
	run := startSpec(t, service, spec)
	waitForWorkflowRun(t, service, run.ID, "completed")

	reads, err := service.store.DocReadsForTask(context.Background(), spec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reads) != 1 {
		t.Fatalf("want exactly one doc read, got %#v", reads)
	}
	read := reads[0]
	if read.TaskID != spec.TaskID || read.StepID != "worker" || read.RunID != run.ID || read.ProjectID != "project" ||
		read.Agent != "researcher" || read.Runtime != domain.RuntimeCodex || read.DocsDir != "docs" || read.DocPath != "x.md" || read.Tool != "shell:cat" || read.OpenedAt.IsZero() {
		t.Fatalf("doc read = %#v", read)
	}
	stepContext, err := service.RunContext(run.ID, "worker")
	// The step was handed docs/x.md in its envelope's index, so the read
	// is marked as a delivered doc.
	if err != nil || len(stepContext.DocReads) != 1 || stepContext.DocReads[0].DocRead != read || !stepContext.DocReads[0].Delivered {
		t.Fatalf("step context doc reads = %#v, %v", stepContext.DocReads, err)
	}
	byDoc, err := service.DocReads("project", "x.md")
	if err != nil || len(byDoc) != 1 || byDoc[0] != read {
		t.Fatalf("reads of x.md = %#v, %v", byDoc, err)
	}

	// Moving the docs root later changes neither what the step read nor
	// whether it was delivered; handbook/x.md is another file, never read.
	service.config.File.Repositories = []config.RepositoryConfig{{Name: "repository", Path: repositoryPath, Docs: "handbook"}}
	stepContext, err = service.RunContext(run.ID, "worker")
	if err != nil || len(stepContext.DocReads) != 1 || stepContext.DocReads[0].DocRead != read || !stepContext.DocReads[0].Delivered {
		t.Fatalf("step context doc reads after the root moved = %#v, %v", stepContext.DocReads, err)
	}
	if moved, err := service.DocReads("project", "x.md"); err != nil || len(moved) != 0 {
		t.Fatalf("reads of handbook/x.md = %#v, %v", moved, err)
	}
}
