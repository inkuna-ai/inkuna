package main

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

func TestUsagePrintsEveryCallOfARunAndItsTotal(t *testing.T) {
	data := t.TempDir()
	t.Setenv("INKUNA_DATA_DIR", data)
	t.Setenv("INKUNA_CONFIG", filepath.Join(data, "inkuna.yaml"))
	var output bytes.Buffer
	if err := run([]string{"usage", "run-1"}, nil, &output); err == nil || !strings.Contains(err.Error(), "no Inkuna database") {
		t.Fatalf("a data directory without a database must say so: %v", err)
	}
	store, err := storage.Open(filepath.Join(data, "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repository := t.TempDir()
	task := domain.TaskSpec{Version: 1, ID: "task-1", Kind: domain.TaskImplementation, Objective: "Test", Repository: repository, Budget: domain.Budget{WallTimeSeconds: 60}}
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow-1", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
	for _, err := range []error{
		store.UpsertProject(ctx, storage.Project{ID: "project", Name: "Project", Repository: repository, DefaultRuntime: domain.RuntimeClaude}),
		store.SaveTask(ctx, "project", "approved", task),
		store.SaveWorkflow(ctx, "approved", workflow),
		store.SaveExecution(ctx, storage.Execution{ID: "run-1", WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: "project", State: "completed", Title: "Run"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range []domain.UsageRecord{
		{TaskID: task.ID, StepID: "staffing", CallID: "orchestrator-task-1", Turn: 1, Kind: domain.CallOrchestrator, Agent: "orchestrator", Runtime: domain.RuntimeClaude, Model: "haiku", ModelUsed: "claude-haiku-5-5", AuthMode: domain.AuthSubscription,
			Outcome: domain.OutcomeCompleted, Usage: domain.Usage{InputTokens: 6, CacheWriteTokens: 12000, OutputTokens: 400, ReportedCents: 2}, Cost: domain.Cost{Cents: 2, Source: domain.CostReported, EstimatedCents: 1.9, PricingVersion: 1},
			Sections: []domain.PromptSection{{Section: domain.SectionSchema, Bytes: 1400}, {Section: "request", Bytes: 600}}},
		{TaskID: task.ID, ExecutionID: "run-1", StepID: "implement", CallID: "run-1-implement", Turn: 1, Kind: domain.CallWorker, Agent: "implementer", Runtime: domain.RuntimeCodex, Model: "gpt-5.6-terra", Outcome: domain.OutcomeTimedOut,
			Usage: domain.Usage{InputTokens: 10, CacheWriteTokens: 0, CacheReadTokens: 36000, OutputTokens: 1600}, Cost: domain.Cost{Cents: 98.5, Source: domain.CostEstimated, EstimatedCents: 98.5, PricingVersion: 1},
			Sections: []domain.PromptSection{{Section: domain.SectionSkill, Name: "conventions", Bytes: 4000}}},
		{TaskID: task.ID, ExecutionID: "run-1", StepID: "implement", CallID: "run-1-review", Turn: 1, Kind: domain.CallPeer, Agent: "reviewer", Runtime: domain.RuntimeCopilot, Outcome: domain.OutcomeFailed,
			Usage: domain.Usage{OutputTokens: 52}},
	} {
		if _, err := store.RecordUsage(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	_ = store.Close()

	output.Reset()
	if err := run([]string{"usage", "--sections", "run-1"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	// Cells are at least two spaces apart; a heading may hold one.
	cells := regexp.MustCompile(`\s{2,}`)
	lines := strings.Split(output.String(), "\n")
	for index, want := range [][]string{
		{"STEP", "TURN", "KIND", "AGENT", "RUNTIME", "MODEL", "OUTCOME", "INPUT", "CACHE WRITE", "CACHE READ", "OUTPUT", "COST", "PROMPT BYTES"},
		{"staffing", "1", "orchestrator", "orchestrator", "claude", "claude-haiku-5-5", "completed", "6", "12000", "0", "400", "$0.0200", "2000"},
		{"implement", "1", "worker", "implementer", "codex", "gpt-5.6-terra", "timed-out", "10", "0", "36000", "1600", "≈$0.9850", "4000"},
		{"implement", "1", "peer", "reviewer", "copilot", "default", "failed", "0", "0", "0", "52", "?", "0"},
		{"TOTAL", "3 calls", "16", "12000", "36000", "2052", "≈$1.0050 +1?", "6000"},
	} {
		if got := cells.Split(strings.TrimSpace(lines[index]), -1); !reflect.DeepEqual(got, want) {
			t.Fatalf("line %d = %q, want %q\n%s", index, got, want, output.String())
		}
	}
	for _, want := range []string{
		"48016 input tokens (75% read from cache), 2052 output tokens, $0.0200 reported, ≈$0.9850 estimated, 1 call without a price",
		"Reported costs from a subscription login are what the calls would cost through the API",
		"staffing turn 1 orchestrator orchestrator: schema 1400, request 600", "implement turn 1 worker implementer: skill:conventions 4000",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, output.String())
		}
	}
	if err := run([]string{"usage", "run-missing"}, nil, &output); err == nil || !strings.Contains(err.Error(), `run "run-missing" was not found`) {
		t.Fatalf("an unknown run must be named: %v", err)
	}
	if err := run([]string{"usage"}, nil, &output); err == nil || !strings.Contains(err.Error(), usageUsage) {
		t.Fatalf("a missing run id must print the usage: %v", err)
	}
}

func TestCostTotalsShowOnlyWhatIsKnown(t *testing.T) {
	for _, test := range []struct {
		total          domain.CostTotal
		label, summary string
	}{
		{domain.CostTotal{ReportedCents: 4}, "$0.0400", "$0.0400 reported"},
		{domain.CostTotal{EstimatedCents: 1.32, UnknownCalls: 2}, "≈$0.0132 +2?", "≈$0.0132 estimated, 2 calls without a price"},
		// Nothing priced is unknown, never zero.
		{domain.CostTotal{UnknownCalls: 1}, "?", "1 call without a price"},
		{domain.CostTotal{}, "$0.0000", "no cost"},
		// A fraction of a hundredth of a cent is not free.
		{domain.CostTotal{EstimatedCents: 0.004}, "≈<$0.0001", "≈<$0.0001 estimated"},
	} {
		if label, summary := totalLabel(test.total), costSummary(test.total); label != test.label || summary != test.summary {
			t.Errorf("%+v: %q, %q; want %q, %q", test.total, label, summary, test.label, test.summary)
		}
	}
}
