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

// costFixture stores two tasks: one planned once and run twice, the second
// run failing, and an older one run once.
func costFixture(t *testing.T) time.Time {
	t.Helper()
	data := t.TempDir()
	t.Setenv("INKUNA_DATA_DIR", data)
	t.Setenv("INKUNA_CONFIG", filepath.Join(data, "inkuna.yaml"))
	store, err := storage.Open(filepath.Join(data, "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	repository := t.TempDir()
	start := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	older := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	steps := []domain.WorkflowStep{
		{ID: "research", Title: "Research", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}},
		{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}},
	}
	if err := store.UpsertProject(ctx, storage.Project{ID: "project", Name: "Inkuna", Repository: repository, DefaultRuntime: domain.RuntimeClaude}); err != nil {
		t.Fatal(err)
	}
	for _, task := range []struct{ id, objective string }{{"task-1", "Add a cost view"}, {"task-2", "Fix the tasks list"}} {
		spec := domain.TaskSpec{Version: 1, ID: task.id, Kind: domain.TaskImplementation, Objective: task.objective, Repository: repository, Budget: domain.Budget{WallTimeSeconds: 60}}
		workflow := domain.WorkflowSpec{Version: 1, ID: task.id + "-workflow", TaskID: task.id, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: start, Steps: steps}
		if err := store.SaveTask(ctx, "project", "approved", spec); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range []storage.Execution{
		{ID: "run-1", WorkflowID: "task-1-workflow", TaskID: "task-1", ProjectID: "project", State: "completed", Title: "Run", StartedAt: start.Add(5 * time.Minute)},
		{ID: "run-2", WorkflowID: "task-1-workflow", TaskID: "task-1", ProjectID: "project", State: "failed", Title: "Run", StartedAt: start.Add(30 * time.Minute)},
		{ID: "run-3", WorkflowID: "task-2-workflow", TaskID: "task-2", ProjectID: "project", State: "completed", Title: "Run", StartedAt: older},
	} {
		if err := store.SaveExecution(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	reported := func(cents float64) domain.Cost {
		return domain.Cost{Cents: cents, Source: domain.CostReported, EstimatedCents: cents, PricingVersion: 1}
	}
	estimated := func(cents float64) domain.Cost {
		return domain.Cost{Cents: cents, Source: domain.CostEstimated, EstimatedCents: cents, PricingVersion: 1}
	}
	call := func(task, run, step, kind, agent string, runtime domain.Runtime, ended time.Time, seconds int, usage domain.Usage, cost domain.Cost) domain.UsageRecord {
		return domain.UsageRecord{TaskID: task, ExecutionID: run, StepID: step, CallID: run + "-" + step + "-" + agent, Turn: 1, Kind: kind, Agent: agent, Runtime: runtime, Model: "haiku",
			Usage: usage, Cost: cost, Outcome: domain.OutcomeCompleted, StartedAt: ended.Add(-time.Duration(seconds) * time.Second), FinishedAt: ended}
	}
	staffing := call("task-1", "", "staffing", domain.CallOrchestrator, "orchestrator", domain.RuntimeClaude, start.Add(time.Minute), 4, domain.Usage{InputTokens: 6, CacheWriteTokens: 12000, OutputTokens: 400}, reported(2))
	staffing.AuthMode = domain.AuthSubscription
	research := call("task-1", "run-1", "research", domain.CallWorker, "researcher", domain.RuntimeClaude, start.Add(7*time.Minute), 60, domain.Usage{InputTokens: 10, CacheReadTokens: 1000, OutputTokens: 100}, reported(4))
	research.AuthMode = domain.AuthSubscription
	retried := call("task-1", "run-2", "research", domain.CallWorker, "researcher", domain.RuntimeClaude, start.Add(40*time.Minute), 45, domain.Usage{InputTokens: 15, OutputTokens: 50}, reported(1.5))
	retried.AuthMode, retried.Outcome = domain.AuthAPIKey, domain.OutcomeFailed
	for _, record := range []domain.UsageRecord{
		staffing, research,
		call("task-1", "run-1", "implement", domain.CallWorker, "implementer", domain.RuntimeCodex, start.Add(9*time.Minute), 30, domain.Usage{InputTokens: 300, OutputTokens: 30}, estimated(2.5)),
		call("task-1", "run-1", "implement", domain.CallPeer, "reviewer", domain.RuntimeCopilot, start.Add(10*time.Minute), 10, domain.Usage{OutputTokens: 40}, domain.Cost{Source: domain.CostUnknown}),
		retried,
		call("task-2", "run-3", "research", domain.CallWorker, "researcher", domain.RuntimeClaude, older.Add(time.Minute), 5, domain.Usage{InputTokens: 5, OutputTokens: 5}, estimated(0.25)),
	} {
		if _, err := store.RecordUsage(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	return start
}

// Cells are at least two spaces apart; a heading may hold one.
var costCells = regexp.MustCompile(`\s{2,}`)

func checkLines(t *testing.T, output string, want [][]string) {
	t.Helper()
	lines := strings.Split(output, "\n")
	for index, cells := range want {
		if index >= len(lines) {
			t.Fatalf("output has %d lines, want %d:\n%s", len(lines), len(want), output)
		}
		if got := costCells.Split(strings.TrimSpace(lines[index]), -1); !reflect.DeepEqual(got, cells) {
			t.Fatalf("line %d = %q, want %q\n%s", index, got, cells, output)
		}
	}
}

func TestCostListsTasksMostExpensiveFirst(t *testing.T) {
	start := costFixture(t)
	var output bytes.Buffer
	if err := run([]string{"cost"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	checkLines(t, output.String(), [][]string{
		{"TASK", "PROJECT", "STATE", "LAST CALL", "CALLS", "INPUT", "CACHE WRITE", "CACHE READ", "OUTPUT", "TIME", "COST", "OBJECTIVE"},
		{"task-1", "Inkuna", "failed", clock(start.Add(40 * time.Minute)), "5", "331", "12000", "1000", "620", "2m29s", "≈$0.1000 +1?", "Add a cost view"},
		{"task-2", "Inkuna", "completed", clock(time.Date(2026, 10, 1, 9, 1, 0, 0, time.UTC)), "1", "5", "0", "0", "5", "5s", "≈$0.0025", "Fix the tasks list"},
		{"TOTAL", "6", "336", "12000", "1000", "625", "2m34s", "≈$0.1025 +1?"},
		{""},
		{"2 tasks: $0.0750 reported, ≈$0.0275 estimated, 1 call without a price"},
		{"$0.0600 of the reported cost came from a subscription login: what those calls would cost through the API, not what the subscription bills."},
	})

	output.Reset()
	if err := run([]string{"cost", "--since", "2026-10-10"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1 task since 2026-10-10: $0.0750 reported, ≈$0.0250 estimated, 1 call without a price") || strings.Contains(output.String(), "task-2") {
		t.Fatalf("since a date, only the calls that ended on or after it count:\n%s", output.String())
	}

	output.Reset()
	if err := run([]string{"cost", "--csv"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"task,project,objective,kind,state,last_call,calls,input_tokens,cache_write_tokens,cache_read_tokens,output_tokens,duration_ms,reported_usd,estimated_usd,unknown_calls,best_usd,subscription_usd",
		"task-1,Inkuna,Add a cost view,implementation,failed,2026-10-19T09:40:00Z,5,331,12000,1000,620,149000,0.075000,0.025000,1,0.100000,0.060000",
		"task-2,Inkuna,Fix the tasks list,implementation,completed,2026-10-01T09:01:00Z,1,5,0,0,5,5000,0.000000,0.002500,0,0.002500,0.000000",
		"",
	}, "\n")
	if output.String() != want {
		t.Fatalf("csv =\n%s\nwant\n%s", output.String(), want)
	}
}

func TestCostBreaksATaskDownByRunAndStep(t *testing.T) {
	start := costFixture(t)
	var output bytes.Buffer
	// The task may come before its flags.
	if err := run([]string{"cost", "task-1"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	checkLines(t, output.String(), [][]string{
		{"Task task-1: Add a cost view"},
		{"5 calls over 2 runs: 13331 input tokens (8% read from cache), 620 output tokens, $0.0750 reported, ≈$0.0250 estimated, 1 call without a price"},
		{"Planning: 1 call, $0.0200"},
		{""},
		{"RUN", "STATE", "STARTED", "CALLS", "INPUT", "CACHE WRITE", "CACHE READ", "OUTPUT", "TIME", "COST"},
		{"run-1", "completed", clock(start.Add(5 * time.Minute)), "3", "310", "0", "1000", "170", "1m40s", "≈$0.0650 +1?"},
		{"research", "1", "10", "0", "1000", "100", "1m0s", "$0.0400"},
		{"implement", "2", "300", "0", "0", "70", "40s", "≈$0.0250 +1?"},
		{"run-2", "failed", clock(start.Add(30 * time.Minute)), "1", "15", "0", "0", "50", "45s", "$0.0150"},
		{"research", "1", "15", "0", "0", "50", "45s", "$0.0150"},
		{""},
		{"STEP", "CALLS", "INPUT", "CACHE WRITE", "CACHE READ", "OUTPUT", "TIME", "COST"},
		{"research", "2", "25", "0", "1000", "150", "1m45s", "$0.0550"},
		{"implement", "2", "300", "0", "0", "70", "40s", "≈$0.0250 +1?"},
		{"(planning)", "1", "6", "12000", "0", "400", "4s", "$0.0200"},
		{"TOTAL", "5", "331", "12000", "1000", "620", "2m29s", "≈$0.1000 +1?"},
		{""},
		{"$0.0600 of the reported cost came from a subscription login: what those calls would cost through the API, not what the subscription bills."},
	})

	output.Reset()
	if err := run([]string{"cost", "--by", "agent", "--csv", "task-1"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"agent,calls,input_tokens,cache_write_tokens,cache_read_tokens,output_tokens,duration_ms,reported_usd,estimated_usd,unknown_calls,best_usd,subscription_usd",
		"researcher,2,25,0,1000,150,105000,0.055000,0.000000,0,0.055000,0.040000",
		"implementer,1,300,0,0,30,30000,0.000000,0.025000,0,0.025000,0.000000",
		"orchestrator,1,6,12000,0,400,4000,0.020000,0.000000,0,0.020000,0.020000",
		"reviewer,1,0,0,0,40,10000,0.000000,0.000000,1,0.000000,0.000000",
		"",
	}, "\n")
	if output.String() != want {
		t.Fatalf("csv =\n%s\nwant\n%s", output.String(), want)
	}

	for _, bad := range []struct {
		args []string
		want string
	}{
		{[]string{"cost", "task-1", "--since", "2026-10-01"}, "--since narrows the task list"},
		{[]string{"cost", "--by", "agent"}, "--by groups one task's calls"},
		{[]string{"cost", "task-1", "--by", "phase"}, `--by "phase" must be step, agent, runtime, or model`},
		{[]string{"cost", "--since", "yesterday"}, `--since "yesterday" must be a date`},
		{[]string{"cost", "task-1", "task-2"}, costUsage},
		{[]string{"cost", "missing"}, `task "missing" was not found`},
	} {
		if err := run(bad.args, nil, &output); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Fatalf("%v = %v, want %q", bad.args, err, bad.want)
		}
	}
}

func TestCostCSVKeepsFormulaLookingTextAsText(t *testing.T) {
	var output bytes.Buffer
	cost := domain.PeriodCost{Tasks: []domain.TaskCostSummary{{
		TaskID: "task-1", ProjectID: "project", Objective: `=HYPERLINK("https://example.com")`, Kind: domain.TaskResearch, State: "completed",
		LastCallAt: time.Date(2026, 10, 19, 9, 40, 0, 0, time.UTC), Total: domain.CallTotals{Calls: 1, BestCents: 1, Cost: domain.CostTotal{ReportedCents: 1}},
	}}}
	if err := writeTaskListCSV(&output, cost, map[string]string{"project": "@team"}); err != nil {
		t.Fatal(err)
	}
	// Numbers are left alone; only text a spreadsheet would run is marked.
	if want := `task-1,'@team,"'=HYPERLINK(""https://example.com"")",research,completed,2026-10-19T09:40:00Z,1,0,0,0,0,0,0.010000,0.000000,0,0.010000,0.000000`; strings.Split(output.String(), "\n")[1] != want {
		t.Fatalf("csv row = %q, want %q", strings.Split(output.String(), "\n")[1], want)
	}
	for _, text := range []string{"=1+1", "+1", "-1", "@SUM(A1)", "\tcmd", "\rcmd", "\n=1+1", "＝1+1"} {
		if got := spreadsheetText(text); got != "'"+text {
			t.Fatalf("spreadsheetText(%q) = %q", text, got)
		}
	}
	for _, text := range []string{"research", "", "gpt-5.5", "(planning)", "Ｒesearch"} {
		if got := spreadsheetText(text); got != text {
			t.Fatalf("spreadsheetText(%q) = %q, want it unchanged", text, got)
		}
	}
}
