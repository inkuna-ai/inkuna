package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/notify"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

func handoffJSON(t *testing.T, handoff domain.HandoffResult) json.RawMessage {
	t.Helper()
	handoff.Version = domain.ContractVersion
	data, err := json.Marshal(handoff)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func startSpec(t *testing.T, service *Service, spec domain.WorkflowSpec) WorkflowRunView {
	t.Helper()
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func waitForInbox(t *testing.T, service *Service, count int) []InputView {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	for {
		inbox, err := service.Inbox()
		if err != nil {
			t.Fatal(err)
		}
		if len(inbox) == count {
			return inbox
		}
		if time.Now().After(deadline) {
			t.Fatalf("inbox never reached %d items: %#v", count, inbox)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkerHumanInputPausesRunAndResumesWithAnswer(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if !strings.Contains(string(request.Prompt), "# Human responses") {
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffInputRequired, Summary: "Need a decision", HumanInput: &domain.HumanInputRequest{Question: "Which database?", Options: []string{"sqlite", "postgres"}}})}, nil
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Used the chosen database", Decisions: []string{"sqlite"}})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	run := startSpec(t, service, spec)
	inbox := waitForInbox(t, service, 1)
	waiting := waitForWorkflowRun(t, service, run.ID, "waiting")
	if waiting.PendingInputs != 1 || waiting.Steps[0].State != "waiting" {
		t.Fatalf("run did not pause: %#v", waiting)
	}
	question := inbox[0]
	if question.Question != "Which database?" || question.AskedBy != "researcher" || question.Agent != "researcher" || len(question.Options) != 2 || question.RunTitle == "" {
		t.Fatalf("unexpected inbox item %#v", question)
	}
	recorder := service.notifier.(*notify.Recorder)
	deadline := time.Now().Add(settleTimeout)
	for len(recorder.Sent()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if sent := recorder.Sent(); len(sent) != 1 || sent[0].Kind != "input-required" || sent[0].Reference != question.ID {
		t.Fatalf("desktop notification missing: %#v", sent)
	}
	if _, err := service.AnswerInput(question.ID, "   "); err == nil {
		t.Fatal("empty answers must be rejected")
	}
	answered, err := service.AnswerInput(question.ID, "sqlite")
	if err != nil || answered.State != storage.InputAnswered {
		t.Fatalf("answer failed: %#v %v", answered, err)
	}
	if _, err := service.AnswerInput(question.ID, "again"); err == nil {
		t.Fatal("answering twice must fail")
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.PendingInputs != 0 || completed.Steps[0].Summary != "Used the chosen database" {
		t.Fatalf("run did not resume cleanly: %#v", completed)
	}
	requests := started(runtime)
	if len(requests) != 2 || !strings.Contains(string(requests[1].Prompt), "- Q: Which database?\n  A: sqlite\n") {
		t.Fatalf("second turn did not carry the human response: %d requests, prompt=%s", len(requests), requests[len(requests)-1].Prompt)
	}
	messages, err := service.RunMessages(run.ID)
	if err != nil || len(messages) != 1 || len(messages[0].Task.History) != 3 {
		t.Fatalf("A2A history should hold question, pause message, and answer: %#v err=%v", messages, err)
	}
	inbox, err = service.Inbox()
	if err != nil || len(inbox) != 0 {
		t.Fatalf("inbox should be empty: %#v %v", inbox, err)
	}
}

func TestWorkerPeerMessagesAreRelayedOverTheBus(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		prompt := string(request.Prompt)
		switch {
		case strings.HasPrefix(prompt, "# Agent: reviewer"):
			if request.Permission != domain.PermissionReadOnly {
				t.Errorf("peer must run read-only, got %s", request.Permission)
			}
			if !strings.Contains(prompt, "This step is read-only") || strings.Contains(prompt, "List any files you produced") {
				t.Errorf("a peer must be told it cannot write files: %s", prompt)
			}
			// The request, under its subject, is the peer's objective, sent
			// once, and the peer is told who asked.
			subject, question := "Design check", "Is the interface acceptable?"
			if strings.Contains(prompt, "Is the name acceptable?") {
				subject, question = "Naming", "Is the name acceptable?"
			}
			if strings.Count(prompt, question) != 1 || !strings.Contains(prompt, "# Objective\n"+subject+"\n\n"+question+"\n") || strings.Contains(prompt, "# Peer messages") || !strings.Contains(prompt, "Answer researcher's request") {
				t.Errorf("a peer must get the request once, as its objective: %s", prompt)
			}
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "The approach is sound", Decisions: []string{"keep the interface"}, Risks: []string{"the cache may go stale"}})}, nil
		case strings.Contains(prompt, "# Peer messages") && strings.Contains(prompt, "The approach is sound"):
			if !strings.Contains(prompt, "- From reviewer (Re: Design check):\n  The approach is sound\n  Decisions: keep the interface\n  Risks: the cache may go stale\n") {
				t.Errorf("the peer's reply must carry its decisions and risks: %s", prompt)
			}
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Finished after review"})}, nil
		default:
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: HandoffWaitingPeers, Summary: "Need a review", Messages: []domain.PeerMessage{
				{To: "reviewer", Subject: "Design check", Content: "Is the interface acceptable?"},
				{To: "reviewer", Subject: "Naming", Content: "Is the name acceptable?"},
			}})}, nil
		}
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	run := startSpec(t, service, spec)
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.Steps[0].Summary != "Finished after review" {
		t.Fatalf("worker did not finish after the peer reply: %#v", completed)
	}
	requests := started(runtime)
	if len(requests) != 4 {
		t.Fatalf("expected worker, two peer requests to the same agent, worker turns, got %d", len(requests))
	}
	messages, err := service.RunMessages(run.ID)
	if err != nil || len(messages) != 3 {
		t.Fatalf("expected three A2A tasks in the context, got %#v err=%v", messages, err)
	}
	var peer, worker int
	for _, message := range messages {
		task := message.Task
		switch task.Agent {
		case "reviewer":
			peer++
			if len(task.History) == 0 || task.History[0].Role != "agent" || len(task.History[0].ReferenceTaskIDs) != 1 {
				t.Fatalf("peer task should reference its parent: %#v", task.History)
			}
		case "researcher":
			worker++
		}
	}
	if peer != 2 || worker != 1 {
		t.Fatalf("unexpected task agents: %#v", messages)
	}
	var sawQuestion, sawReply bool
	for _, event := range completed.Events {
		sawQuestion = sawQuestion || strings.Contains(event.Summary, "researcher asked reviewer")
		sawReply = sawReply || strings.Contains(event.Summary, "reviewer replied to researcher")
	}
	if !sawQuestion || !sawReply {
		t.Fatalf("peer exchange was not logged: %#v", completed.Events)
	}
}

func TestRuleHumanInputStepFeedsLaterSteps(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if !strings.Contains(string(request.Prompt), "Human input (Ship to staging first?): yes") {
			t.Errorf("worker did not receive the human decision: %s", request.Prompt)
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Done"})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	worker := spec.Steps[0]
	worker.DependsOn = []string{"confirm"}
	spec.RuleName = "custom"
	spec.Steps = []domain.WorkflowStep{
		{ID: "confirm", Title: "Confirm", Kind: domain.StepHumanInput, Prompt: "Ship to staging first?", Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 60}},
		worker,
	}
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)
	inbox := waitForInbox(t, service, 1)
	if inbox[0].Agent != agent.Human || inbox[0].AskedBy != "rule:custom" || inbox[0].Question != "Ship to staging first?" {
		t.Fatalf("unexpected inbox item %#v", inbox[0])
	}
	if _, err := service.AnswerInput(inbox[0].ID, "yes"); err != nil {
		t.Fatal(err)
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.Steps[0].State != "completed" || !strings.Contains(completed.Steps[0].Summary, "Human answered: yes") {
		t.Fatalf("human step did not complete: %#v", completed.Steps)
	}
	if len(started(runtime)) != 1 {
		t.Fatalf("worker should run once after the answer, got %d", len(started(runtime)))
	}
}

func TestWritableStepIsCommittedAndDiffIsViewable(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if err := os.WriteFile(filepath.Join(request.WorkingDir, "feature.go"), []byte("package feature\n"), 0o644); err != nil {
			return agentruntime.RunResult{}, err
		}
		if err := os.MkdirAll(filepath.Join(request.WorkingDir, ".inkuna", "out"), 0o755); err != nil {
			return agentruntime.RunResult{}, err
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDir, ".inkuna", "out", "notes.md"), []byte("# Notes\n"), 0o644); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Added feature package", Outputs: []domain.ArtifactOutput{{Path: ".inkuna/out/notes.md", Description: "Design notes"}, {Path: "../escape.txt"}}})}, nil
	}
	service := workflowTestService(t, repositoryPath, runtime)
	spec := workflowTestSpec(t, service)
	spec.Steps[0].Agent = "implementer"
	spec.Steps[0].Permission = domain.PermissionWorktree
	spec.Steps[0].AllowedPaths = []string{"."}
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.LastCommit == nil || !strings.HasPrefix(completed.LastCommit.Subject, "inkuna(worker): Added feature package") || completed.Branch == "" {
		t.Fatalf("writable step was not committed: %#v", completed)
	}
	if !strings.Contains(completed.LastCommit.Stat, "feature.go") {
		t.Fatalf("commit view missing its stat: %#v", completed.LastCommit)
	}
	listed, err := service.RunCommits(run.ID)
	if err != nil || len(listed) != 1 || listed[0].Hash != completed.LastCommit.Hash || listed[0].StepID != "worker" {
		t.Fatalf("the step's commit should be listed with its step: %#v err=%v", listed, err)
	}
	artifacts, err := service.RunArtifacts(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	var notesDigest string
	for _, artifact := range artifacts {
		kinds[artifact.Kind]++
		if artifact.Kind == "output" {
			notesDigest = artifact.Digest
			if artifact.Description != "Design notes" || artifact.Agent != "implementer" {
				t.Fatalf("output metadata missing: %#v", artifact)
			}
		}
	}
	if kinds["context"] != 1 || kinds["prompt"] != 1 || kinds["handoff"] != 1 || kinds["diff"] != 1 || kinds["output"] != 1 {
		t.Fatalf("unexpected artifact kinds %v", kinds)
	}
	notes, err := service.ReadArtifact(notesDigest)
	if err != nil || notes.Content != "# Notes\n" || notes.Binary {
		t.Fatalf("artifact preview failed: %#v err=%v", notes, err)
	}
	stepContext, err := service.RunContext(run.ID, "worker")
	if err != nil || stepContext.Agent != "implementer" || stepContext.Handoff == nil {
		t.Fatalf("step context missing: %#v err=%v", stepContext, err)
	}
	if prompt := lastPrompt(t, service, stepContext); stepContext.Envelope.Metadata[domain.MetadataPermission] != string(domain.PermissionWorktree) || !strings.HasPrefix(prompt, "# Agent: implementer") || !strings.Contains(prompt, "List any files you produced") {
		t.Fatalf("a writable step must be told how to declare outputs: %v\n%s", stepContext.Envelope.Metadata, prompt)
	}
	commits, err := service.store.Commits(context.Background(), run.ID)
	if err != nil || len(commits) != 1 {
		t.Fatalf("commit was not persisted: %#v err=%v", commits, err)
	}
	var warned bool
	for _, event := range completed.Events {
		warned = warned || strings.Contains(event.Summary, "escape.txt")
	}
	if !warned {
		t.Fatal("unsafe output path should be reported")
	}
}

func TestTaskLimitQueuesAdditionalRuns(t *testing.T) {
	runtime := &workflowRuntime{}
	release := make(chan struct{})
	firstStarted := make(chan struct{})
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if strings.Contains(string(request.Prompt), "first objective") {
			close(firstStarted)
			select {
			case <-release:
			case <-ctx.Done():
				return agentruntime.RunResult{}, ctx.Err()
			}
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "ok"})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	service.config.MaxTasks = 1
	service.taskSlots = make(chan struct{}, 1)
	first := namedSpec(t, service, "first", "first objective")
	second := namedSpec(t, service, "second", "second objective")
	firstRun := startSpec(t, service, first)
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not acquire the task slot")
	}
	secondRun := startSpec(t, service, second)
	queued, err := service.WorkflowRun(secondRun.ID)
	if err != nil || queued.State != "queued" {
		t.Fatalf("second run should wait for a slot: %#v err=%v", queued, err)
	}
	dashboard, err := service.Dashboard()
	if err != nil || dashboard.ActiveRuns != 2 {
		t.Fatalf("dashboard should count both runs: %#v err=%v", dashboard.ActiveRuns, err)
	}
	close(release)
	waitForWorkflowRun(t, service, firstRun.ID, "completed")
	waitForWorkflowRun(t, service, secondRun.ID, "completed")
}

func namedSpec(t *testing.T, service *Service, id, objective string) domain.WorkflowSpec {
	t.Helper()
	task := domain.TaskSpec{
		Version: domain.ContractVersion, ID: "task-" + id, Kind: domain.TaskResearch, Objective: objective,
		Source: domain.TaskSource{Kind: "request"}, Repository: ".", Priority: 1, Budget: domain.Budget{WallTimeSeconds: 60},
	}
	if err := service.store.SaveTask(context.Background(), "project", "planned", task); err != nil {
		t.Fatal(err)
	}
	workflow := domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow-" + id, TaskID: task.ID, DefaultRuntime: domain.RuntimeCodex,
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

func TestStartWorkflowRejectsUnstaffedOrUnknownAgents(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	spec.Steps[0].Agent = "auto"
	if err := service.store.SaveWorkflow(context.Background(), "planned", spec); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(spec)
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "staffed") {
		t.Fatalf("expected unstaffed error, got %v", err)
	}
	spec.Steps[0].Agent = "ghost"
	if err := service.store.SaveWorkflow(context.Background(), "planned", spec); err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(spec)
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected unknown agent error, got %v", err)
	}
}

func TestSetupSavesConfigurationAndSecretsWithoutLeakingValues(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	service.config.ConfigPath = filepath.Join(service.dataDir, "inkuna.yaml")
	repo := t.TempDir()
	file := config.DefaultFile()
	file.Orchestrator.Runtime = domain.RuntimeCodex
	file.Concurrency.MaxTasks = 4
	file.Repositories = []config.RepositoryConfig{{Name: "demo", Path: repo, DefaultRuntime: domain.RuntimeClaude}}
	encoded, _ := json.Marshal(file)
	view, err := service.SaveConfiguration(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if !view.Exists || view.File.Orchestrator.Runtime != domain.RuntimeCodex || service.config.MaxTasks != 4 {
		t.Fatalf("configuration was not applied: %#v", view)
	}
	if _, err := os.Stat(view.Path); err != nil {
		t.Fatal(err)
	}
	projects, err := service.store.Projects(context.Background())
	if err != nil || len(projects) != 2 {
		t.Fatalf("declared repository was not synced: %#v err=%v", projects, err)
	}
	view, err = service.SaveSecret("ANTHROPIC_API_KEY", "sk-ant-test")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, status := range view.Secrets {
		if status.Name == "ANTHROPIC_API_KEY" {
			found = status.Available && status.Source == "keychain"
		}
	}
	if !found {
		t.Fatalf("secret status missing: %#v", view.Secrets)
	}
	raw, _ := os.ReadFile(view.Path)
	if strings.Contains(string(raw), "sk-ant-test") || !strings.Contains(string(raw), "ANTHROPIC_API_KEY") {
		t.Fatalf("inkuna.yaml must declare the name but never the value: %s", raw)
	}
	if _, err := service.SaveSecret("bad name", "x"); err == nil {
		t.Fatal("invalid names must be rejected")
	}
	if _, err := service.DeleteSecret("ANTHROPIC_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveConfiguration(`{"version":1,"apiKeys":{"x":"y"}}`); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
}

func TestAgentsAndRulesBindings(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".inkuna", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "---\nname: security-reviewer\ndescription: Reviews for security issues\npermission: read-only\ntaskKinds: [review]\n---\nCheck for injection.\n"
	if err := os.WriteFile(filepath.Join(repo, ".inkuna", "agents", "security-reviewer.md"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	agents, err := service.Agents("project")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]agent.Source{}
	for _, view := range agents {
		names[view.Definition.Name] = view.Definition.Source
		if view.Card.Name != view.Definition.Name {
			t.Fatalf("card name mismatch %#v", view)
		}
	}
	if names["security-reviewer"] != agent.SourceRepository || names["implementer"] != agent.SourceBuiltIn || names[agent.Human] != agent.SourceBuiltIn {
		t.Fatalf("unexpected agents %#v", names)
	}
	rulesView, err := service.Rules("project")
	if err != nil || rulesView.Exists || len(rulesView.Document.Rules) == 0 || !strings.HasSuffix(rulesView.Path, filepath.FromSlash(".inkuna/rules.yaml")) {
		t.Fatalf("unexpected rules view %#v err=%v", rulesView, err)
	}
}

func TestSecretEnvironmentOnlyExportsDeclaredNames(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	_ = service.secretStore.Set("ANTHROPIC_API_KEY", "anthropic-value")
	_ = service.secretStore.Set("SNEAKY_TOKEN", "value-b")
	service.config.File.Secrets = []config.SecretRef{{Name: "ANTHROPIC_API_KEY"}}
	service.config.RuntimeSecrets = map[domain.Runtime][]string{domain.RuntimeClaude: {"ANTHROPIC_API_KEY"}}
	definition := agent.Definition{Name: "x", Secrets: []string{"SNEAKY_TOKEN", "ANTHROPIC_API_KEY"}}
	environment, err := secretEnvironment(service.snapshotDependencies(), domain.RuntimeClaude, definition)
	if err != nil || len(environment) != 1 || environment[0] != "ANTHROPIC_API_KEY=anthropic-value" {
		t.Fatalf("unexpected environment %v %v", environment, err)
	}
	environment, err = secretEnvironment(service.snapshotDependencies(), domain.RuntimeCodex, agent.Definition{Name: "y"})
	if err != nil || len(environment) != 0 {
		t.Fatalf("undeclared runtimes must export nothing: %v %v", environment, err)
	}
}

func TestASecretTooShortToHideIsNotExported(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	_ = service.secretStore.Set("GITHUB_TOKEN", "tiny123")
	service.config.File.Secrets = []config.SecretRef{{Name: "GITHUB_TOKEN"}}
	definition := agent.Definition{Name: "x", Secrets: []string{"GITHUB_TOKEN"}}
	environment, err := secretEnvironment(service.snapshotDependencies(), domain.RuntimeClaude, definition)
	if err == nil || environment != nil || !strings.Contains(err.Error(), "GITHUB_TOKEN is shorter than 8 bytes") || strings.Contains(err.Error(), "tiny123") {
		t.Fatalf("a secret the runtime could not hide must stop the step, without its value: %v %v", environment, err)
	}
}

func TestParseHandoffNormalizesStatuses(t *testing.T) {
	cases := map[string]string{
		`{"status":"success","summary":"ok"}`:        domain.HandoffCompleted,
		`{"status":"error","summary":"bad"}`:         domain.HandoffFailed,
		`{"status":"needs-input","summary":"?"}`:     domain.HandoffInputRequired,
		`{"status":"waiting-peers","summary":"ask"}`: HandoffWaitingPeers,
		`{"status":"whatever","summary":"x"}`:        domain.HandoffCompleted,
		`not json`:                                   domain.HandoffCompleted,
		``:                                           domain.HandoffCompleted,
		`{"status":"input-required","summary":"","version":1}`: domain.HandoffInputRequired,
	}
	for input, expected := range cases {
		handoff := parseHandoff(json.RawMessage(input))
		if handoff.Status != expected || handoff.Summary == "" || handoff.Version != domain.ContractVersion {
			t.Fatalf("%s: got %+v", input, handoff)
		}
	}
}

func TestAReadOnlyStepWithoutAUsableHandoffFails(t *testing.T) {
	for _, output := range []string{``, `not json`} {
		handoff := parseStepHandoff(json.RawMessage(output), true)
		if handoff.Status != domain.HandoffFailed || !strings.Contains(handoff.Summary, "read-only step's handoff is its whole result") {
			t.Fatalf("%q: a read-only step must fail and say why: %+v", output, handoff)
		}
		if writer := parseStepHandoff(json.RawMessage(output), false); writer.Status != domain.HandoffCompleted {
			t.Fatalf("%q: a step that writes still completes: %+v", output, writer)
		}
	}
	if handoff := parseStepHandoff(json.RawMessage(`{"status":"completed","summary":"Looks right"}`), true); handoff.Status != domain.HandoffCompleted || handoff.Summary != "Looks right" {
		t.Fatalf("a read-only step's handoff is kept: %+v", handoff)
	}
}

func TestRepositoryRulesOverrideFromConfiguration(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	custom := "version: 1\nrules:\n  - name: only-docs\n    when: {}\n    then:\n      stages:\n        - id: write\n          kind: agent\n          agent: documenter\n        - id: approval\n          kind: approval\n          dependsOn: [write]\n"
	if err := os.WriteFile(filepath.Join(repo, "flows.yaml"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	service.config.File.Repositories = []config.RepositoryConfig{{Name: "repo", Path: repo, Rules: "flows.yaml"}}
	view, err := service.Rules("project")
	if err != nil || !view.Exists || len(view.Document.Rules) != 1 || view.Document.Rules[0].Name != "only-docs" || view.Path != filepath.Join(repo, "flows.yaml") {
		t.Fatalf("override was not honoured: %#v err=%v", view, err)
	}
	service.config.File.Repositories[0].Rules = "missing.yaml"
	if _, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "x"}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing override must fail loudly, got %v", err)
	}
}

func TestWorkerMayPauseAsOftenAsItNeeds(t *testing.T) {
	// Six question rounds before finishing: more than any fixed cap the
	// harness used to apply per step.
	const rounds = 6
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		answered := strings.Count(string(request.Prompt), "\n  A: yes\n")
		if answered < rounds {
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffInputRequired, Summary: "Need a decision", HumanInput: &domain.HumanInputRequest{Question: fmt.Sprintf("Question %d?", answered+1)}})}, nil
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Finished after every answer"})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	for round := 1; round <= rounds; round++ {
		inbox := waitForInbox(t, service, 1)
		if inbox[0].Question != fmt.Sprintf("Question %d?", round) {
			t.Fatalf("round %d asked %q", round, inbox[0].Question)
		}
		if _, err := service.AnswerInput(inbox[0].ID, "yes"); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.Steps[0].Summary != "Finished after every answer" || len(started(runtime)) != rounds+1 {
		t.Fatalf("worker did not get every turn: %#v, %d invocations", completed.Steps[0], len(started(runtime)))
	}
}
