package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/pricing"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// promptBytes is what a call sent the model: its prompt and output schema.
func promptBytes(request agentruntime.RunRequest) int {
	return len(request.Prompt) + len(request.Schema)
}

func TestStepTurnsAndPeerAnswersAddUpToTheRunTotal(t *testing.T) {
	// Codex reports tokens and no cost.
	first := domain.Usage{InputTokens: 10, CacheWriteTokens: 20000, OutputTokens: 300, Turns: 2}
	peer := domain.Usage{InputTokens: 4, CacheWriteTokens: 8000, OutputTokens: 90, Turns: 1}
	second := domain.Usage{InputTokens: 12, CacheReadTokens: 20000, OutputTokens: 500, Turns: 3}
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		prompt := string(request.Prompt)
		switch {
		case strings.HasPrefix(prompt, "# Agent: reviewer"):
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Sound"}), Usage: peer}, nil
		case strings.Contains(prompt, "# Peer messages"):
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Finished after review"}), Usage: second}, nil
		default:
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: HandoffWaitingPeers, Summary: "Need a review", Messages: []domain.PeerMessage{{To: "reviewer", Content: "Is this sound?"}}}), Usage: first}, nil
		}
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	waitForWorkflowRun(t, service, run.ID, "completed")

	usage, err := service.RunUsage(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	requests := started(runtime)
	if len(usage.Calls) != 3 || len(requests) != 3 {
		t.Fatalf("want a row per call: %d rows for %d calls", len(usage.Calls), len(requests))
	}
	want := []struct {
		kind, agent, callID string
		turn                int
		usage               domain.Usage
	}{
		{domain.CallWorker, "researcher", run.ID + "-worker", 1, first},
		{domain.CallPeer, "reviewer", run.ID + "-worker-peer1-reviewer", 1, peer},
		{domain.CallWorker, "researcher", run.ID + "-worker-turn2", 2, second},
	}
	for index, call := range usage.Calls {
		expected := want[index]
		if call.Kind != expected.kind || call.Agent != expected.agent || call.CallID != expected.callID || call.Turn != expected.turn || call.Usage != expected.usage ||
			call.StepID != "worker" || call.ExecutionID != run.ID || call.Outcome != domain.OutcomeCompleted || call.Runtime != domain.RuntimeCodex || call.Model == "" || call.Model != requests[index].Model {
			t.Fatalf("call %d = %+v, want %+v", index, call, expected)
		}
		if call.PromptBytes != promptBytes(requests[index]) || len(call.Sections) < 4 {
			t.Fatalf("call %d measured %d prompt bytes in %d sections, but sent %d", index, call.PromptBytes, len(call.Sections), promptBytes(requests[index]))
		}
		// Codex names no model, so the routed one prices the call's tokens.
		if want := pricing.Default().Cost(domain.RuntimeCodex, requests[index].Model, expected.usage); call.Cost != want || want.Source != domain.CostEstimated {
			t.Fatalf("call %d cost = %+v, want %+v", index, call.Cost, want)
		}
	}
	if total := first.Add(peer).Add(second); usage.Total != total || domain.TotalUsage(usage.Calls) != total {
		t.Fatalf("run total = %+v, want %+v", usage.Total, total)
	}
	if usage.Cost != domain.TotalCost(usage.Calls) || usage.Cost.EstimatedCents <= 0 || usage.Cost.ReportedCents != 0 || usage.Cost.UnknownCalls != 0 {
		t.Fatalf("run cost = %+v", usage.Cost)
	}
	// The second turn carries the peer's reply, and its prompt shows it.
	if !hasSection(usage.Calls[2].Sections, domain.SectionPeerMessages) || hasSection(usage.Calls[0].Sections, domain.SectionPeerMessages) {
		t.Fatalf("only the second turn carried peer messages: %+v / %+v", usage.Calls[0].Sections, usage.Calls[2].Sections)
	}
	context, err := service.RunContext(run.ID, "worker")
	if err != nil || len(context.Usage) != 3 || context.Usage[2].CallID != want[2].callID {
		t.Fatalf("the step's context lists its calls: %+v, %v", context.Usage, err)
	}
	if _, err := service.RunUsage("run-missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("an unknown run must be refused: %v", err)
	}
}

func hasSection(sections []domain.PromptSection, name string) bool {
	for _, section := range sections {
		if section.Section == name {
			return true
		}
	}
	return false
}

func TestFailedWorkerRunStillRecordsItsUsage(t *testing.T) {
	spent := domain.Usage{InputTokens: 40, CacheWriteTokens: 3000, CacheReadTokens: 90000, OutputTokens: 2100, Turns: 9}
	runtime := &workflowRuntime{}
	runtime.handle = func(context.Context, agentruntime.RunRequest) (agentruntime.RunResult, error) {
		return agentruntime.RunResult{Usage: spent}, errors.New("claude worker failed: too many turns")
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	waitForWorkflowRun(t, service, run.ID, "failed")
	usage, err := service.RunUsage(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.Calls) != 1 || usage.Calls[0].Outcome != domain.OutcomeFailed || usage.Calls[0].Usage != spent || usage.Total != spent || usage.Calls[0].Cost.Source != domain.CostEstimated {
		t.Fatalf("the failed call must keep what it spent, priced: %+v", usage)
	}
}

func TestPlanningRecordsTheOrchestratorsUsage(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	staffing := domain.Usage{InputTokens: 6, CacheWriteTokens: 12000, OutputTokens: 400, ReportedCents: 2.25, Turns: 1}
	// The proposal timed out before Claude Code reported its cost.
	proposing := domain.Usage{InputTokens: 3, CacheReadTokens: 12000, OutputTokens: 50, Turns: 1}
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if strings.HasPrefix(request.RunID, "branching-") {
			// The proposal times out; planning infers the values instead.
			return agentruntime.RunResult{Usage: proposing}, context.DeadlineExceeded
		}
		decision := `{"assignments":[{"stageId":"worker","agent":"implementer","skills":[],"complexity":{"score":6,"rationale":"one package"},"rationale":"writes code"}]}`
		return agentruntime.RunResult{Output: []byte(decision), Usage: staffing, Model: "claude-haiku-5-5", AuthMode: domain.AuthSubscription}, nil
	}
	service := workflowTestService(t, repositoryPath, runtime)
	service.config.Orchestrator.Runtime, service.config.Orchestrator.Model = domain.RuntimeClaude, "haiku"
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Title: "Record usage", Objective: "Record usage", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Orchestrated || len(plan.Usage) != 2 {
		t.Fatalf("staffing and the branching proposal are each a call: orchestrated=%v usage=%+v notes=%v", plan.Orchestrated, plan.Usage, plan.Notes)
	}
	requests := started(runtime)
	for index, want := range []struct {
		step, callID, outcome, modelUsed, authMode string
		usage                                      domain.Usage
		cost                                       domain.Cost
	}{
		// Claude Code reported the staffing call's cost and named its model.
		{"staffing", "orchestrator-" + plan.Task.ID, domain.OutcomeCompleted, "claude-haiku-5-5", domain.AuthSubscription, staffing,
			domain.Cost{Cents: 2.25, Source: domain.CostReported, EstimatedCents: pricing.Default().Models["claude-haiku-5-5"].Cents(staffing), PricingVersion: 1}},
		// The proposal said neither, and haiku, the model asked for, is an
		// alias the table does not price.
		{"branching", "branching-" + plan.Task.ID, domain.OutcomeTimedOut, "", "", proposing, domain.Cost{Source: domain.CostUnknown}},
	} {
		call := plan.Usage[index]
		if call.ModelUsed != want.modelUsed || call.AuthMode != want.authMode || call.Cost != want.cost {
			t.Fatalf("call %d model %q, auth %q, cost %+v; want %q, %q, %+v", index, call.ModelUsed, call.AuthMode, call.Cost, want.modelUsed, want.authMode, want.cost)
		}
		if call.StepID != want.step || call.CallID != want.callID || call.Outcome != want.outcome || call.Usage != want.usage || call.Kind != domain.CallOrchestrator ||
			call.ExecutionID != "" || call.TaskID != plan.Task.ID || call.Runtime != domain.RuntimeClaude || call.Model != "haiku" || call.PromptBytes != promptBytes(requests[index]) {
			t.Fatalf("call %d = %+v, want %+v of %d prompt bytes", index, call, want, promptBytes(requests[index]))
		}
	}
	stored, err := service.store.PlanningUsage(context.Background(), plan.Task.ID)
	if err != nil || len(stored) != 2 {
		t.Fatalf("planning usage must be stored with the task: %+v, %v", stored, err)
	}
}

func TestPlanningThatRunsOutOfTimeStillRecordsTheOrchestratorsUsage(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	spent := domain.Usage{InputTokens: 4, CacheWriteTokens: 15000, OutputTokens: 900}
	runtime := &workflowRuntime{}
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		// The orchestrator streams what it used, then thinks past the
		// planning deadline.
		<-ctx.Done()
		return agentruntime.RunResult{Usage: spent}, ctx.Err()
	}
	service := workflowTestService(t, repositoryPath, runtime)
	service.config.Orchestrator.Runtime = domain.RuntimeClaude
	service.config.PlanningTimeout = time.Second
	if _, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Title: "Record usage", Objective: "Record usage", Kind: domain.TaskImplementation}); err == nil {
		t.Fatal("planning past its deadline must fail")
	}
	requests := started(runtime)
	if len(requests) == 0 || !strings.HasPrefix(requests[0].RunID, "orchestrator-") {
		t.Fatalf("the orchestrator was never called: %+v", requests)
	}
	taskID := strings.TrimPrefix(requests[0].RunID, "orchestrator-")
	recorded, err := service.store.PlanningUsage(context.Background(), taskID)
	if err != nil || len(recorded) != 1 || recorded[0].StepID != "staffing" || recorded[0].Outcome != domain.OutcomeTimedOut || recorded[0].Usage != spent {
		t.Fatalf("the timed-out staffing call must be recorded: %+v, %v", recorded, err)
	}
}

func TestUsageRecordedAfterACancelledRunEndsIsAnnounced(t *testing.T) {
	spent := domain.Usage{InputTokens: 9, CacheWriteTokens: 30000, OutputTokens: 700}
	stopping := make(chan struct{})
	runtime := &workflowRuntime{}
	runtime.handle = func(ctx context.Context, _ agentruntime.RunRequest) (agentruntime.RunResult, error) {
		<-ctx.Done()
		// The CLI is still being stopped and drained when the run ends.
		<-stopping
		return agentruntime.RunResult{Usage: spent}, ctx.Err()
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	announced := make(chan any, 4)
	service.emitMu.Lock()
	service.emitter = func(name string, data any) {
		if name == EventUsage {
			announced <- data
		}
	}
	service.emitMu.Unlock()
	run := startSpec(t, service, workflowTestSpec(t, service))
	deadline := time.Now().Add(5 * time.Second)
	for len(started(runtime)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("runtime did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := service.CancelWorkflowRun(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "cancelled")
	if usage, err := service.RunUsage(run.ID); err != nil || len(usage.Calls) != 0 {
		t.Fatalf("the call is still stopping, so it has no row yet: %+v, %v", usage, err)
	}
	close(stopping)
	select {
	case data := <-announced:
		if data != run.ID {
			t.Fatalf("the usage event must name its run: %v", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a row stored after the run ended was never announced")
	}
	usage, err := service.RunUsage(run.ID)
	if err != nil || len(usage.Calls) != 1 || usage.Calls[0].Outcome != domain.OutcomeCancelled || usage.Calls[0].Usage != spent {
		t.Fatalf("the announced row must be readable: %+v, %v", usage, err)
	}
}

func TestAResumedTurnIsGivenWhatItsSessionHadUsed(t *testing.T) {
	// The CLI reports the session's totals; the runtime takes off what the
	// session had used, which Inkuna hands back from the turn before.
	sessionTotals := domain.Usage{InputTokens: 1000, CacheReadTokens: 900, OutputTokens: 30}
	runtime := &workflowRuntime{resume: true}
	ask := askThenFinish(t)
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		result, err := ask(ctx, request)
		if request.Session == "" {
			result.Usage, result.SessionUsage = sessionTotals, sessionTotals
		} else {
			result.Usage, result.SessionUsage = domain.Usage{InputTokens: 100, OutputTokens: 20}, domain.Usage{InputTokens: 1100, CacheReadTokens: 900, OutputTokens: 50}
		}
		return result, err
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	answerTheQuestion(t, service, run.ID)
	requests := started(runtime)
	if len(requests) != 2 || requests[1].Session != "session-1" || requests[1].SessionUsage != sessionTotals || requests[0].SessionUsage != (domain.Usage{}) {
		t.Fatalf("the resumed turn must be given its session's totals: %+v", requests)
	}
}

func TestTaskCostCoversEachRunOfTheTask(t *testing.T) {
	spent := domain.Usage{InputTokens: 40, CacheWriteTokens: 3000, OutputTokens: 200, Turns: 2}
	var calls atomic.Int32
	runtime := &workflowRuntime{}
	runtime.handle = func(context.Context, agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if calls.Add(1) == 1 {
			return agentruntime.RunResult{Usage: spent}, errors.New("codex worker failed")
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Done"}), Usage: spent}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	failed := startSpec(t, service, spec)
	waitForWorkflowRun(t, service, failed.ID, "failed")
	again := startSpec(t, service, spec)
	waitForWorkflowRun(t, service, again.ID, "completed")

	cost, err := service.TaskCost(spec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cost.Runs) != 2 || cost.Runs[0].ExecutionID != failed.ID || cost.Runs[0].State != "failed" || cost.Runs[1].ExecutionID != again.ID || cost.Runs[1].State != "completed" {
		t.Fatalf("runs = %+v", cost.Runs)
	}
	for _, run := range cost.Runs {
		usage, err := service.RunUsage(run.ExecutionID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Total.Usage != usage.Total || run.Total.Cost != usage.Cost || run.Total.Calls != len(usage.Calls) || len(run.Steps) != 1 || run.Steps[0].StepID != "worker" || run.Steps[0].Agent != "researcher" {
			t.Fatalf("run %s cost = %+v, but its usage is %+v", run.ExecutionID, run, usage)
		}
	}
	if cost.Total.Calls != 2 || cost.Total.Usage != spent.Add(spent) || cost.Total.Cost.EstimatedCents <= 0 || cost.Total.BestCents != cost.Total.Cost.EstimatedCents {
		t.Fatalf("task total = %+v", cost.Total)
	}

	period, err := service.TaskCosts("project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(period.Tasks) != 1 || period.Tasks[0].TaskID != spec.TaskID || period.Tasks[0].Objective != "Inspect the repository" || period.Tasks[0].Total != cost.Total || period.Total != cost.Total {
		t.Fatalf("project costs = %+v, want the task's %+v", period, cost.Total)
	}
	// Every project's tasks, when no project is named.
	if everywhere, err := service.TaskCosts("", "", ""); err != nil || len(everywhere.Tasks) != 1 || everywhere.Tasks[0].ProjectID != "project" || everywhere.Tasks[0].LatestRunID != again.ID {
		t.Fatalf("every project's costs = %+v, %v", everywhere, err)
	}
	// Each run in the list, and a run's own view, carry what its task has
	// cost so far.
	runs, err := service.WorkflowRuns()
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.TaskCost == nil || *run.TaskCost != cost.Total {
			t.Fatalf("run %s task cost = %+v, want %+v", run.ID, run.TaskCost, cost.Total)
		}
	}
	if detail, err := service.WorkflowRun(failed.ID); err != nil || detail.TaskCost == nil || *detail.TaskCost != cost.Total {
		t.Fatalf("run detail task cost = %+v, %v", detail.TaskCost, err)
	}
	later := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if empty, err := service.TaskCosts("project", later, ""); err != nil || len(empty.Tasks) != 0 {
		t.Fatalf("a period after every call = %+v, %v", empty, err)
	}
	for _, bad := range []struct{ project, from, to, want string }{
		{"project", "yesterday", "", "from must be an RFC 3339 time"},
		{"project", "", "2026-13-01T00:00:00Z", "to must be an RFC 3339 time"},
		{"project", "2026-10-02T00:00:00Z", "2026-10-01T00:00:00Z", "must end after it starts"},
		{"project", "2026-10-01T00:00:00Z", "2026-10-01T00:00:00Z", "must end after it starts"},
		{"missing", "", "", "load project"},
	} {
		if _, err := service.TaskCosts(bad.project, bad.from, bad.to); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Fatalf("TaskCosts(%q, %q, %q) = %v, want %q", bad.project, bad.from, bad.to, err, bad.want)
		}
	}
	if _, err := service.TaskCost("missing"); err == nil || !strings.Contains(err.Error(), `task "missing" was not found`) {
		t.Fatalf("missing task = %v", err)
	}
}

func TestTaskCostShowsARunFinishedBeforeItsRecordIsStored(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(context.Context, agentruntime.RunRequest) (agentruntime.RunResult, error) {
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Done"})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	run := waitForWorkflowRun(t, service, startSpec(t, service, spec).ID, "completed")
	// The record is stored by the time the run's goroutine is done, so
	// putting it back as it stood just before then makes certain the gap in
	// which the view has finished and the record has not.
	service.runsWG.Wait()
	stored, err := service.store.Execution(context.Background(), run.ID)
	if err != nil || stored.State != "completed" {
		t.Fatalf("stored run = %+v, %v", stored, err)
	}
	stored.State, stored.FinishedAt = "running", time.Time{}
	if err := service.store.SaveExecution(context.Background(), stored); err != nil {
		t.Fatal(err)
	}

	cost, err := service.TaskCost(spec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	finished, err := time.Parse(time.RFC3339Nano, run.FinishedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(cost.Runs) != 1 || cost.Runs[0].State != "completed" || !cost.Runs[0].FinishedAt.Equal(finished) {
		t.Fatalf("runs = %+v, want the view's state and end %s", cost.Runs, run.FinishedAt)
	}
}
