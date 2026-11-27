package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/artifact"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/docs"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/notify"
	"github.com/inkuna-ai/inkuna/internal/repository"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

type workflowRuntime struct {
	mu      sync.Mutex
	started []agentruntime.RunRequest
	wait    bool
	// resume reports that the runtime can continue a session.
	resume bool
	// handle, when set, scripts the runtime's behaviour per request.
	handle func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error)
}

func (runtime *workflowRuntime) Probe(context.Context) (agentruntime.Capabilities, error) {
	return runtime.Capabilities(), nil
}

func (runtime *workflowRuntime) Start(ctx context.Context, request agentruntime.RunRequest, sink agentruntime.EventSink) (agentruntime.RunResult, error) {
	runtime.mu.Lock()
	runtime.started = append(runtime.started, request)
	runtime.mu.Unlock()
	if err := sink(syntheticEvent(request.RunID, 0, domain.EventLifecycle, "worker started")); err != nil {
		return agentruntime.RunResult{}, err
	}
	if runtime.handle != nil {
		// A failed run keeps the usage the handler reported, and whether the
		// model acted, as Start does.
		result, err := runtime.handle(ctx, request)
		if err != nil {
			return agentruntime.RunResult{Usage: result.Usage, Acted: result.Acted}, err
		}
		return withReadOnlyHandoff(request, result), sink(syntheticEvent(request.RunID, 1, domain.EventComplete, "worker completed"))
	}
	if runtime.wait {
		<-ctx.Done()
		return agentruntime.RunResult{}, ctx.Err()
	}
	return withReadOnlyHandoff(request, agentruntime.RunResult{}), sink(syntheticEvent(request.RunID, 1, domain.EventComplete, "worker completed"))
}

// withReadOnlyHandoff gives a read-only call that returned nothing the
// handoff a real worker's structured output would hold, since such a step
// fails without one (parseStepHandoff).
func withReadOnlyHandoff(request agentruntime.RunRequest, result agentruntime.RunResult) agentruntime.RunResult {
	if request.Permission == domain.PermissionReadOnly && len(result.Output) == 0 {
		result.Output = json.RawMessage(`{"status":"completed","summary":"Looked; nothing to change"}`)
	}
	return result
}

func (*workflowRuntime) Cancel(string) error { return nil }

func (runtime *workflowRuntime) Capabilities() agentruntime.Capabilities {
	return agentruntime.Capabilities{Runtime: domain.RuntimeCodex, Available: true, ReadOnly: true, WorktreeWrite: true, StructuredOutput: true, MCPDisabled: true, Resume: runtime.resume, MCPServers: true}
}

func TestPlanWorkflowPersistsExecutablePlan(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repositoryPath, &workflowRuntime{})

	plan, err := service.PlanWorkflow(WorkflowRequestView{
		ProjectID: "project", Objective: "Connect the workflow runner", Kind: domain.TaskImplementation,
		AcceptanceCriteria: []string{"The run starts", "  ", "Events are visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Task.Objective != "Connect the workflow runner" || len(plan.Task.AcceptanceCriteria) != 2 {
		t.Fatalf("unexpected planned task: %#v", plan.Task)
	}
	if err := domain.ValidateRoutedWorkflow(plan.Workflow); err != nil {
		t.Fatalf("planned workflow is not executable: %v", err)
	}
	stored, err := service.store.Task(context.Background(), plan.Task.ID)
	if err != nil || stored.Objective != plan.Task.Objective {
		t.Fatalf("planned task was not persisted: %#v %v", stored, err)
	}
	if state, err := service.store.TaskState(context.Background(), plan.Task.ID); err != nil || state != "planned" {
		t.Fatalf("a plan not yet approved leaves its task planned: %q %v", state, err)
	}
}

func TestStartWorkflowRunsAgentAndPublishesEvents(t *testing.T) {
	runtime := &workflowRuntime{}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	if len(run.Events) != 3 || run.Runtime != domain.RuntimeCodex || run.Model == "" || run.Agent != "researcher" {
		t.Fatalf("unexpected completed run: %#v", run)
	}
	if len(run.Steps) != 1 || run.Steps[0].State != "completed" || run.Steps[0].A2ATaskID == "" {
		t.Fatalf("step view was not tracked: %#v", run.Steps)
	}
	messages, err := service.RunMessages(run.ID)
	if err != nil || len(messages) != 1 || messages[0].Task.Agent != "researcher" || messages[0].Task.Status.State != "completed" || len(messages[0].Task.Artifacts) != 1 {
		t.Fatalf("A2A task was not recorded: %#v err=%v", messages, err)
	}
	if strings.Contains(string(started(runtime)[0].Prompt), "# Agent: researcher") == false {
		t.Fatalf("worker prompt lacks agent instructions: %s", started(runtime)[0].Prompt)
	}
	latest, err := service.LatestWorkflowRun()
	if err != nil || latest.ID != run.ID {
		t.Fatalf("latest workflow run: %#v err=%v", latest, err)
	}
	runtime.mu.Lock()
	started := append([]agentruntime.RunRequest(nil), runtime.started...)
	runtime.mu.Unlock()
	if len(started) != 1 || started[0].WorkingDir == "" || len(started[0].Prompt) == 0 {
		t.Fatalf("runtime did not receive the approved task: %#v", started)
	}
	count, err := service.store.EventCount(context.Background(), started[0].RunID)
	if err != nil || count != 3 {
		t.Fatalf("events were not persisted: count=%d err=%v", count, err)
	}
	history, err := service.WorkflowRuns()
	if err != nil || len(history) != 1 || history[0].ID != run.ID || history[0].State != "completed" {
		t.Fatalf("run history missing: %#v err=%v", history, err)
	}
}

func started(runtime *workflowRuntime) []agentruntime.RunRequest {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return append([]agentruntime.RunRequest(nil), runtime.started...)
}

// writeDocs creates files under a repository, keyed by slash-separated path.
func writeDocs(t *testing.T, repositoryPath string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(repositoryPath, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEveryAgentStepReceivesTheDocIndex(t *testing.T) {
	repositoryPath := t.TempDir()
	writeDocs(t, repositoryPath, map[string]string{
		"docs/guide.md":      "---\ntitle: Guide\nlastUpdated: 2026-10-01\nauthor: A. Author\ntags: [guide]\nstatus: current\n---\nGuide body stays in the file.\n",
		"docs/old/legacy.md": "---\ntitle: Legacy\nlastUpdated: 2025-01-01\nauthor: A. Author\ntags: [old]\nstatus: deprecated\n---\nLegacy body stays in the file.\n",
		"docs/notes.md":      "Notes body stays in the file.\n",
	})
	runtime := &workflowRuntime{}
	service := workflowTestService(t, repositoryPath, runtime)
	spec := workflowTestSpec(t, service)
	review := spec.Steps[0]
	review.ID, review.Title, review.Agent, review.DependsOn = "review", "Review", "reviewer", []string{"worker"}
	spec.Steps = append(spec.Steps, review)
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)
	waitForWorkflowRun(t, service, run.ID, "completed")

	if domain.DocStatusDeprecated != string(docs.StatusDeprecated) {
		t.Fatal("the envelope must drop the status the docs index calls deprecated")
	}
	want := []domain.DocBrief{
		{Path: "docs/guide.md", Title: "Guide", Tags: []string{"guide"}, Status: "current", LastUpdated: "2026-10-01", Author: "A. Author"},
		{Path: "docs/notes.md", Tags: []string{}, Problems: []string{"missing frontmatter"}},
		{Path: "docs/old/legacy.md", Title: "Legacy", Tags: []string{"old"}, Status: "deprecated", LastUpdated: "2025-01-01", Author: "A. Author"},
	}
	for _, stepID := range []string{"worker", "review"} {
		stepContext, err := service.RunContext(run.ID, stepID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stepContext.Envelope.Docs, want) {
			t.Fatalf("%s: docs = %#v", stepID, stepContext.Envelope.Docs)
		}
		prompt := lastPrompt(t, service, stepContext)
		if !strings.Contains(prompt, "Docs maps the repository's documentation") || !strings.Contains(prompt, "\n# Docs\n- docs/guide.md: Guide (current, 2026-10-01, A. Author) [guide]\n- docs/notes.md\n  - problem: missing frontmatter\n- docs/old/legacy.md: Legacy (deprecated, 2025-01-01, A. Author) [old]\n") {
			t.Fatalf("%s: prompt does not explain or carry the index: %s", stepID, prompt)
		}
		if strings.Contains(prompt, "body stays in the file") {
			t.Fatalf("%s: a doc body reached the prompt", stepID)
		}
	}
}

func TestStepsInTheWorktreeGetTheWorktreesDocIndex(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	writeDocs(t, repositoryPath, map[string]string{"docs/guide.md": "---\ntitle: Guide\nlastUpdated: 2026-10-01\nauthor: A. Author\ntags: [guide]\nstatus: current\n---\nBody.\n"})
	if err := runGit(repositoryPath, "add", "."); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	// Uncommitted, so the worktree, created from a commit, never has it.
	writeDocs(t, repositoryPath, map[string]string{"docs/draft.md": "---\ntitle: Draft\nlastUpdated: 2026-10-02\nauthor: A. Author\ntags: [draft]\nstatus: draft\n---\nBody.\n"})
	runtime := &workflowRuntime{}
	service := workflowTestService(t, repositoryPath, runtime)
	spec := workflowTestSpec(t, service)
	code := spec.Steps[0]
	code.ID, code.Title, code.Agent, code.Permission, code.AllowedPaths, code.DependsOn = "code", "Code", "implementer", domain.PermissionWorktree, []string{"."}, []string{"worker"}
	spec.Steps = append(spec.Steps, code)
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)
	waitForWorkflowRun(t, service, run.ID, "completed")
	for stepID, want := range map[string][]string{"worker": {"docs/draft.md", "docs/guide.md"}, "code": {"docs/guide.md"}} {
		stepContext, err := service.RunContext(run.ID, stepID)
		if err != nil {
			t.Fatal(err)
		}
		paths := make([]string, 0, len(stepContext.Envelope.Docs))
		for _, doc := range stepContext.Envelope.Docs {
			paths = append(paths, doc.Path)
		}
		if !reflect.DeepEqual(paths, want) {
			t.Fatalf("%s: docs = %v, want the docs in its working directory %v", stepID, paths, want)
		}
	}
}

func TestWorkerPromptSaysWhenTheDocIndexWasCut(t *testing.T) {
	definition := agent.Definition{Name: "researcher", Instructions: "Investigate."}
	for name, tc := range map[string]struct {
		envelope        domain.ContextEnvelope
		explains, warns bool
	}{
		"no docs":     {envelope: domain.ContextEnvelope{}},
		"whole":       {envelope: domain.ContextEnvelope{Docs: []domain.DocBrief{{Path: "docs/a.md"}}}, explains: true},
		"cut":         {envelope: domain.ContextEnvelope{Docs: []domain.DocBrief{{Path: "docs/a.md"}}, Metadata: map[string]string{domain.MetadataDocsTruncated: "true"}}, explains: true, warns: true},
		"cut to none": {envelope: domain.ContextEnvelope{Metadata: map[string]string{domain.MetadataDocsTruncated: "true"}}, explains: true, warns: true},
	} {
		prompt, _ := workerPrompt(definition, tc.envelope)
		if explains := strings.Contains(string(prompt), "Docs maps"); explains != tc.explains {
			t.Errorf("%s: explains the index = %v, want %v", name, explains, tc.explains)
		}
		if warns := strings.Contains(string(prompt), "The doc index was cut to fit"); warns != tc.warns {
			t.Errorf("%s: says it was cut = %v, want %v", name, warns, tc.warns)
		}
	}
}

func TestWorkerPromptTellsAReadOnlyStepItCannotWriteFiles(t *testing.T) {
	definition := agent.Definition{Name: "researcher", Instructions: "Investigate."}
	for permission, readOnly := range map[string]bool{string(domain.PermissionReadOnly): true, string(domain.PermissionWorktree): false, "": false} {
		envelope := domain.ContextEnvelope{}
		if permission != "" {
			envelope.Metadata = map[string]string{domain.MetadataPermission: permission}
		}
		prompt, _ := workerPrompt(definition, envelope)
		if says := strings.Contains(string(prompt), "This step is read-only"); says != readOnly {
			t.Errorf("%q: says the step is read-only = %v, want %v", permission, says, readOnly)
		}
		// A read-only step can produce no files, so it is not asked to list any.
		if lists := strings.Contains(string(prompt), "List any files you produced"); lists == readOnly {
			t.Errorf("%q: asks for outputs = %v, want %v", permission, lists, !readOnly)
		}
	}
}

func TestRunWithAnUnreadableDocsRootGoesAheadAndSaysWhy(t *testing.T) {
	repositoryPath := t.TempDir()
	writeDocs(t, repositoryPath, map[string]string{"docs": "a file where the docs directory should be"})
	runtime := &workflowRuntime{}
	service := workflowTestService(t, repositoryPath, runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	var said bool
	for _, event := range completed.Events {
		said = said || (strings.Contains(event.Summary, "This step runs without the doc index") && strings.Contains(event.Summary, "not a directory"))
	}
	if !said {
		t.Fatalf("the run should say why it has no doc index: %#v", completed.Events)
	}
	stepContext, err := service.RunContext(run.ID, "worker")
	if err != nil || len(stepContext.Envelope.Docs) != 0 || strings.Contains(lastPrompt(t, service, stepContext), "Docs maps") {
		t.Fatalf("expected no doc index: %#v err=%v", stepContext.Envelope.Docs, err)
	}
}

func TestCancelWorkflowRunStopsActiveRuntime(t *testing.T) {
	runtime := &workflowRuntime{wait: true}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(settleTimeout)
	for {
		runtime.mu.Lock()
		started := len(runtime.started) > 0
		runtime.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := service.CancelWorkflowRun(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "cancelled")
}

func TestStartWorkflowRejectsApprovalThatExpandsPermissions(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	spec.Steps[0].Permission = domain.PermissionWorktree
	spec.Steps[0].AllowedPaths = []string{"."}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), `agent "researcher" is read-only`) {
		t.Fatalf("approval must not give a read-only agent's step write access, got %v", err)
	}
}

func TestApprovalAllowsEquivalentEmptyCollections(t *testing.T) {
	created := time.Now().UTC()
	planned := domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeClaude,
		RoutingPolicyVersion: 1, MaxConcurrency: 1, CreatedAt: created,
		Steps: []domain.WorkflowStep{{
			ID: "profile", Title: "Profile repository", Kind: domain.StepScript,
			Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 60},
		}},
	}
	approved := planned
	approved.Steps = append([]domain.WorkflowStep(nil), planned.Steps...)
	approved.Steps[0].DependsOn = []string{}
	approved.Steps[0].SelectedSkills = []string{}
	approved.Steps[0].AllowedPaths = []string{}
	approved.Steps[0].Commands = [][]string{}
	approved.Steps[0].ExpectedOutputs = []string{}
	approved.Steps[0].Metadata = map[string]string{}
	if err := validateApprovalChanges(planned, approved); err != nil {
		t.Fatalf("equivalent empty collections were rejected: %v", err)
	}
}

func TestCopyRunViewOmitsHeavyPayloadWithoutMutatingStoredEvent(t *testing.T) {
	original := WorkflowRunView{Events: []domain.RunEvent{{
		RunID: "run", Sequence: 1, Payload: map[string]any{"message": "large provider event"},
	}}}
	copied := copyRunView(original)
	if copied.Events[0].Payload != nil {
		t.Fatal("live run response retained the provider payload")
	}
	if original.Events[0].Payload == nil {
		t.Fatal("copying the live run mutated the stored event")
	}
}

func TestLatestWorkflowRunUsesStartInstant(t *testing.T) {
	service := &Service{runs: map[string]*liveExecution{
		"older": {view: WorkflowRunView{ID: "older", StartedAt: "9999-12-31T23:59:59Z"}, started: time.Unix(1, 0)},
		"newer": {view: WorkflowRunView{ID: "newer", StartedAt: "0001-01-01T00:00:00Z"}, started: time.Unix(2, 0)},
	}}
	latest, err := service.LatestWorkflowRun()
	if err != nil || latest.ID != "newer" {
		t.Fatalf("latest workflow run: %#v err=%v", latest, err)
	}
}

func TestEvictTerminalRunsPreservesActiveRuns(t *testing.T) {
	service := &Service{runs: make(map[string]*liveExecution)}
	for index := 0; index <= retainedTerminalRunLimit; index++ {
		id := fmt.Sprintf("terminal-%d", index)
		service.runs[id] = &liveExecution{view: WorkflowRunView{ID: id, State: "completed"}, started: time.Unix(int64(index), 0)}
	}
	service.runs["active"] = &liveExecution{view: WorkflowRunView{ID: "active", State: "running"}, started: time.Now()}
	service.evictTerminalRuns()
	if len(service.runs) != retainedTerminalRunLimit+1 || service.runs["active"] == nil || service.runs["terminal-0"] != nil {
		t.Fatalf("unexpected retained runs: %d", len(service.runs))
	}
}

func TestVerificationCommandsSupportNestedManifests(t *testing.T) {
	commands := verificationCommands(repository.Profile{Manifests: []string{"services/api/go.mod", "web/package.json"}})
	want := [][]string{{"go", "-C", "services/api", "test", "./..."}, {"npm", "--prefix", "web", "test", "--", "--run"}}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("verification commands = %v, want %v", commands, want)
	}
}

func TestConcurrentWritableStepsReuseOneWorktree(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	runtime := &workflowRuntime{}
	service := workflowTestService(t, repositoryPath, runtime)
	spec := workflowTestSpec(t, service)
	first := spec.Steps[0]
	first.ID = "worker-a"
	first.Agent = "implementer"
	first.Permission = domain.PermissionWorktree
	first.AllowedPaths = []string{"."}
	second := first
	second.ID = "worker-b"
	spec.Steps = []domain.WorkflowStep{first, second}
	spec.MaxConcurrency = 2
	spec = savePlanned(t, service, spec)
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	runtime.mu.Lock()
	started := append([]agentruntime.RunRequest(nil), runtime.started...)
	runtime.mu.Unlock()
	if len(started) != 2 || started[0].WorkingDir != started[1].WorkingDir || started[0].WorkingDir == repositoryPath || completed.Worktree != started[0].WorkingDir {
		t.Fatalf("writable steps did not reuse one worktree: run=%#v requests=%#v", completed, started)
	}
}

func TestAppendRunEventBoundsMemoryAndPersistsFullHistory(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	if err := service.store.StartRun(context.Background(), "step-run", "", spec.ID, spec.Steps[0].ID, 1, domain.RuntimeCodex, ""); err != nil {
		t.Fatal(err)
	}
	service.runs["execution"] = &liveExecution{view: WorkflowRunView{ID: "execution", State: "completed"}}
	for sequence := 0; sequence <= liveRunEventLimit; sequence++ {
		event := syntheticEvent("step-run", int64(sequence), domain.EventMessage, "event")
		if err := service.appendRunEvent("execution", event); err != nil {
			t.Fatal(err)
		}
	}
	view, err := service.WorkflowRun("execution")
	if err != nil || len(view.Events) != liveRunEventLimit {
		t.Fatalf("bounded live events: count=%d err=%v", len(view.Events), err)
	}
	if view.Events[0].Sequence != 2 {
		t.Fatalf("bounded live events: first=%d", view.Events[0].Sequence)
	}
	count, err := service.store.EventCount(context.Background(), "step-run")
	if err != nil || count != liveRunEventLimit+1 {
		t.Fatalf("durable events: count=%d err=%v", count, err)
	}
}

func TestEvaluationFailurePublishesCombinedOutput(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	step := evaluationRun(t, service)
	step.Commands = [][]string{{os.Args[0], "-test.run=TestVerificationCommandHelper", "--", "--verification-helper"}}
	_, err := service.executeEvaluationStep(context.Background(), "execution", "step-run", t.TempDir(), humanInputsFile{path: filepath.Join(t.TempDir(), "inputs.json"), data: []byte("[]\n")}, step)
	if err == nil || !strings.Contains(err.Error(), "stdout marker") || !strings.Contains(err.Error(), "stderr marker") {
		t.Fatalf("verification failure did not include combined output: %v", err)
	}
	view, runErr := service.WorkflowRun("execution")
	if runErr != nil || len(view.Events) != 2 || view.Events[1].Kind != domain.EventError || !strings.Contains(view.Events[1].Summary, "stderr marker") {
		t.Fatalf("verification failure event: view=%#v err=%v", view, runErr)
	}
}

func TestVerificationCommandHelper(t *testing.T) {
	for _, argument := range os.Args {
		if argument == "--verification-helper" {
			_, _ = fmt.Fprintln(os.Stdout, "stdout marker")
			_, _ = fmt.Fprintln(os.Stderr, "stderr marker")
			os.Exit(3)
		}
	}
}

func workflowTestService(t *testing.T, repositoryPath string, runtime agentruntime.AgentRuntime) *Service {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProject(context.Background(), storage.Project{ID: "project", Name: "Project", Repository: repositoryPath, DefaultRuntime: domain.RuntimeCodex}); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	artifacts, err := artifact.New(filepath.Join(dataDir, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		dataDir: dataDir, store: store, artifacts: artifacts, runtimes: map[domain.Runtime]agentruntime.AgentRuntime{domain.RuntimeCodex: runtime, domain.RuntimeClaude: runtime}, runs: make(map[string]*liveExecution),
		config:      config.Config{MaxConcurrency: 2, MaxTasks: 2, PlanningTimeout: time.Minute, WorkerTimeout: time.Minute, VerifierTimeout: time.Minute, ApprovalTimeout: time.Minute, File: config.DefaultFile()},
		secretStore: secrets.NewMemory(), notifier: &notify.Recorder{},
	}
	service.secrets = secrets.NewResolver(service.secretStore)
	service.ensureBus()
	t.Cleanup(func() { service.Shutdown(context.Background()) })
	return service
}

func workflowTestSpec(t *testing.T, service *Service) domain.WorkflowSpec {
	t.Helper()
	task := domain.TaskSpec{
		Version: domain.ContractVersion, ID: "task", Kind: domain.TaskResearch, Objective: "Inspect the repository",
		Source: domain.TaskSource{Kind: "request"}, Repository: ".", Priority: 1, Budget: domain.Budget{WallTimeSeconds: 60},
	}
	if err := service.store.SaveTask(context.Background(), "project", "planned", task); err != nil {
		t.Fatal(err)
	}
	workflow := domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeCodex,
		RoutingPolicyVersion: service.routingPolicy().Version, MaxConcurrency: 1, CreatedAt: time.Now().UTC(),
		Steps: []domain.WorkflowStep{{
			ID: "worker", Title: "Inspect", Kind: domain.StepAgent, Runtime: domain.RuntimeCodex, Permission: domain.PermissionReadOnly, Agent: "researcher",
			Complexity:     &domain.ComplexityAssessment{Score: 4, Rationale: "read-only inspection"},
			ModelSelection: &domain.ModelSelection{Model: "gpt-5", Source: "policy", Rationale: "test route"},
			Budget:         domain.Budget{WallTimeSeconds: 60, MaxOutputBytes: 1024},
		}},
	}
	return savePlanned(t, service, workflow)
}

// savePlanned persists a hand-built plan and returns it as the approval
// screen would hold it: rebuilt by ReviseWorkflow, so its skills, model, and
// provenance are Go's own derivation rather than the fixture's guesses.
func savePlanned(t *testing.T, service *Service, workflow domain.WorkflowSpec) domain.WorkflowSpec {
	t.Helper()
	if err := service.store.SaveWorkflow(context.Background(), "planned", workflow); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(workflow)
	if err != nil {
		t.Fatal(err)
	}
	revised, err := service.ReviseWorkflow(string(encoded))
	if err != nil {
		t.Fatalf("revise planned workflow: %v", err)
	}
	return revised
}

// settleTimeout is how long a test waits for a run to get somewhere: to a
// state, an inbox question, a runtime call, or a notification. A wait that
// succeeds returns as soon as it does, so the timeout costs only a test
// that would fail anyway, while a short one failed tests on Windows
// runners, where a run takes several times as long to get there.
const settleTimeout = 30 * time.Second

func waitForWorkflowRun(t *testing.T, service *Service, runID, expected string) WorkflowRunView {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	for {
		run, err := service.WorkflowRun(runID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State == expected {
			return run
		}
		if terminalRunState(run.State) {
			t.Fatalf("run finished in state %q, want %q: %s", run.State, expected, run.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("run remained in state %q", run.State)
		}
		time.Sleep(time.Millisecond)
	}
}

func runGit(directory string, args ...string) error {
	// An empty directory, as a run that failed before its worktree existed
	// records, would run git in this package's own checkout.
	if directory == "" {
		return errors.New("runGit needs a directory; an empty one would run git in the repository under test")
	}
	if args[0] == "init" {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	command := exec.Command("git", append([]string{"-c", "user.name=Inkuna Test", "-c", "user.email=inkuna@example.test", "-c", "commit.gpgsign=false"}, args...)...)
	command.Dir = directory
	return command.Run()
}

func TestReviseWorkflowRestaffsAndReroutesAgentSteps(t *testing.T) {
	runtime := &workflowRuntime{}
	service := workflowTestService(t, t.TempDir(), runtime)
	planned := workflowTestSpec(t, service)
	if planned.Steps[0].ModelSelection.Model != "gpt-6.1-terra" || planned.Steps[0].ModelSelection.ReasoningEffort != "low" {
		t.Fatalf("fixture is not routed by the default policy: %+v", planned.Steps[0].ModelSelection)
	}
	edited := planned
	edited.Steps = []domain.WorkflowStep{cloneStep(planned.Steps[0])}
	edited.Steps[0].Agent = "reviewer"
	edited.Steps[0].Runtime = domain.RuntimeClaude
	edited.Steps[0].Complexity.Score = 2
	stale, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	revised, err := service.ReviseWorkflow(string(stale))
	if err != nil {
		t.Fatal(err)
	}
	step := revised.Steps[0]
	if step.Agent != "reviewer" || step.Runtime != domain.RuntimeClaude || step.ModelSelection == nil || step.ModelSelection.Model != "haiku" || step.ModelSelection.ReasoningEffort != "high" || step.Complexity.Score != 2 {
		t.Fatalf("revision did not restaff and reroute: %+v", step)
	}
	if !strings.HasPrefix(step.Complexity.Rationale, "set to 2 at approval; the plan scored 4") || step.Metadata["assignment"] != "chosen at approval; the plan assigned researcher" {
		t.Fatalf("revision provenance missing: %+v %v", step.Complexity, step.Metadata)
	}
	stored, err := service.store.Workflow(context.Background(), planned.ID)
	if err != nil || stored.Steps[0].Agent != "researcher" || stored.Steps[0].Complexity.Score != 4 {
		t.Fatalf("revising must not change the persisted plan: %+v %v", stored.Steps[0], err)
	}

	// An approval that still shows the old model is stale and refused; the
	// revised one runs with the chosen agent.
	if _, err := service.StartWorkflow(string(stale)); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("a stale approval must be refused, got %v", err)
	}
	encoded, err := json.Marshal(revised)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	if run.Agent != "reviewer" || run.Runtime != domain.RuntimeClaude || run.Model != "haiku" {
		t.Fatalf("the run did not use the approved staffing: %+v", run)
	}
	if !strings.Contains(string(started(runtime)[0].Prompt), "# Agent: reviewer") {
		t.Fatalf("worker prompt lacks the chosen agent's instructions: %s", started(runtime)[0].Prompt)
	}

	// Approval edits outside staffing, access, and routing are still
	// refused. Widening access is approval_access_test.go's.
	other := planned
	other.Steps = []domain.WorkflowStep{cloneStep(planned.Steps[0])}
	other.Steps[0].Prompt = "do something else"
	encoded, _ = json.Marshal(other)
	if _, err := service.ReviseWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "outside staffing") {
		t.Fatalf("a prompt change must be refused, got %v", err)
	}
}

func TestPlanWorkflowHonoursChosenRuleLabelsAndListsCandidates(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repositoryPath, &workflowRuntime{})
	chosen, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Explain the runner", Kind: domain.TaskImplementation, RuleName: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.RuleName != "read-only" || chosen.Workflow.RuleName != "read-only" || len(chosen.Workflow.Steps) != 3 {
		t.Fatalf("the chosen rule was not used: %s %d steps", chosen.RuleName, len(chosen.Workflow.Steps))
	}
	if !strings.Contains(strings.Join(chosen.Notes, "\n"), "chosen by the user") {
		t.Fatalf("override note missing: %v", chosen.Notes)
	}
	if len(chosen.Candidates) == 0 {
		t.Fatal("the plan must list agents the approval screen may assign")
	}
	for _, candidate := range chosen.Candidates {
		if candidate.Name == "orchestrator" || candidate.Name == "human" {
			t.Fatalf("%s is not a worker and cannot be a candidate", candidate.Name)
		}
	}
	labelled, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Fix the crash", Kind: domain.TaskImplementation, Labels: []string{"BugFix"}})
	if err != nil {
		t.Fatal(err)
	}
	if labelled.RuleName != "bugfix" || !strings.Contains(strings.Join(labelled.Notes, "\n"), "selected by a task label") {
		t.Fatalf("a bugfix label must select the bugfix flow: %s %v", labelled.RuleName, labelled.Notes)
	}
	if _, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Anything", RuleName: "ghost"}); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("an unknown rule must fail planning, got %v", err)
	}
}
