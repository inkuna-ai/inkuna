package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestUsageByRunStepAndPlanning(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	project := Project{ID: "project", Name: "Project", Repository: t.TempDir(), DefaultRuntime: domain.RuntimeClaude}
	if err := store.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	task := domain.TaskSpec{Version: 1, ID: "task", Kind: domain.TaskImplementation, Objective: "Test", Repository: project.Repository, Budget: domain.Budget{WallTimeSeconds: 60}}
	if err := store.SaveTask(ctx, project.ID, "approved", task); err != nil {
		t.Fatal(err)
	}
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: time.Now(), Steps: []domain.WorkflowStep{{ID: "worker", Title: "Work", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
	if err := store.SaveWorkflow(ctx, "approved", workflow); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveExecution(ctx, Execution{ID: "run", WorkflowID: workflow.ID, TaskID: task.ID, ProjectID: project.ID, State: "running", Title: "Run"}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	call := func(execution, step, kind string, turn int, usage domain.Usage, sections ...domain.PromptSection) domain.UsageRecord {
		return domain.UsageRecord{
			TaskID: task.ID, ExecutionID: execution, StepID: step, CallID: step + "-call", Turn: turn, Kind: kind, Agent: "implementer", Runtime: domain.RuntimeClaude,
			Model: "haiku", Usage: usage, Sections: sections, Outcome: domain.OutcomeCompleted, StartedAt: start, FinishedAt: start.Add(time.Minute),
		}
	}
	planning := call("", "staffing", domain.CallOrchestrator, 1, domain.Usage{InputTokens: 9, CacheWriteTokens: 900, OutputTokens: 80, ReportedCents: 2, Turns: 1}, domain.PromptSection{Section: domain.SectionSchema, Bytes: 1200})
	planning.Cost = domain.Cost{Cents: 2, Source: domain.CostReported}
	first := call("run", "worker", domain.CallWorker, 1, domain.Usage{InputTokens: 10, CacheWriteTokens: 2000, LongCacheWriteTokens: 1500, CacheReadTokens: 100, OutputTokens: 300, ReportedCents: 7.0625, Turns: 3},
		domain.PromptSection{Section: domain.SectionSchema, Bytes: 1500}, domain.PromptSection{Section: domain.SectionSkill, Name: "conventions", Bytes: 4000})
	first.PromptDigest = "5b06af3f927f348c9b694d7ec5613edc5b40098e0129f119e339654a53f060f8"
	first.ModelUsed, first.AuthMode = "claude-haiku-5-5", domain.AuthSubscription
	first.Cost = domain.Cost{Cents: 7.0625, Source: domain.CostReported, EstimatedCents: 6.5, PricingVersion: 1}
	peer := call("run", "worker", domain.CallPeer, 1, domain.Usage{InputTokens: 4, OutputTokens: 40, PremiumRequests: 0.33, Turns: 1})
	peer.Runtime, peer.Outcome = domain.RuntimeCopilot, domain.OutcomeFailed
	peer.Cost = domain.Cost{Cents: 1.32, Source: domain.CostEstimated, EstimatedCents: 1.32, PricingVersion: 1}
	other := call("run", "review", domain.CallWorker, 1, domain.Usage{OutputTokens: 1})
	recorded := []domain.UsageRecord{planning, first, peer, other}
	for index := range recorded {
		id, err := store.RecordUsage(ctx, recorded[index])
		if err != nil {
			t.Fatal(err)
		}
		recorded[index].ID = id
	}
	recorded[0].PromptBytes, recorded[1].PromptBytes = 1200, 5500
	// Calls without sections come back without them, and a call nothing
	// priced comes back unknown.
	recorded[2].Sections, recorded[3].Sections = []domain.PromptSection{}, []domain.PromptSection{}
	recorded[3].Cost = domain.Cost{Source: domain.CostUnknown}

	run, err := store.RunUsage(ctx, "run")
	if err != nil || !reflect.DeepEqual(run, recorded) {
		t.Fatalf("run usage = %#v, %v", run, err)
	}
	step, err := store.StepUsage(ctx, "run", "worker")
	if err != nil || !reflect.DeepEqual(step, recorded[1:3]) {
		t.Fatalf("step usage = %#v, %v", step, err)
	}
	plan, err := store.PlanningUsage(ctx, task.ID)
	if err != nil || !reflect.DeepEqual(plan, recorded[:1]) {
		t.Fatalf("planning usage = %#v, %v", plan, err)
	}
	if _, err := store.RunUsage(ctx, "missing"); err == nil {
		t.Fatal("usage of a run that does not exist must fail")
	}
	running := call("run", "worker", domain.CallWorker, 2, domain.Usage{})
	running.Outcome = domain.OutcomeRunning
	if _, err := store.RecordUsage(ctx, running); err == nil {
		t.Fatal("a call still running must be refused: only one that ended is stored")
	}
	if _, err := store.RecordUsage(ctx, call("", "worker", domain.CallWorker, 1, domain.Usage{})); err == nil {
		t.Fatal("a worker call without its run must be refused")
	}
	if _, err := store.RecordUsage(ctx, call("missing", "worker", domain.CallWorker, 1, domain.Usage{})); err == nil {
		t.Fatal("a call of a run that does not exist must be refused")
	}
	unknown := call("run", "worker", "advisor", 1, domain.Usage{})
	if _, err := store.RecordUsage(ctx, unknown); err == nil {
		t.Fatal("an unknown kind of call must be refused")
	}
}

func TestCostMigrationKeepsWhatEachRuntimeMeasured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	// Put the usage table back as it was before the cost migration, with a
	// planning call from each runtime as #38 recorded them.
	legacy := `
DROP TABLE usage;
DELETE FROM schema_migrations WHERE version = '011_usage_cost.sql';
CREATE TABLE usage (
  id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, execution_id TEXT REFERENCES executions(id), step_id TEXT NOT NULL, call_id TEXT NOT NULL,
  turn INTEGER NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('worker', 'peer', 'orchestrator')), agent TEXT NOT NULL, runtime TEXT NOT NULL, model TEXT NOT NULL,
  input_tokens INTEGER NOT NULL, cache_write_tokens INTEGER NOT NULL, cache_read_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL, cost_cents INTEGER NOT NULL,
  turns INTEGER NOT NULL, prompt_bytes INTEGER NOT NULL, prompt_sections_json TEXT NOT NULL DEFAULT '[]', outcome TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT NOT NULL,
  prompt_digest TEXT NOT NULL DEFAULT '');
INSERT INTO usage(task_id, step_id, call_id, turn, kind, agent, runtime, model, input_tokens, cache_write_tokens, cache_read_tokens, output_tokens, cost_cents, turns, prompt_bytes, outcome, started_at, finished_at) VALUES
  ('task', 'staffing', 'claude-call', 1, 'orchestrator', 'orchestrator', 'claude', 'haiku', 8, 28325, 27913, 192, 5, 3, 900, 'completed', '2026-10-18T09:00:00Z', '2026-10-18T09:01:00Z'),
  ('task', 'staffing', 'codex-call', 1, 'orchestrator', 'orchestrator', 'codex', 'gpt-5.6-luna', 315, 0, 24448, 122, 0, 0, 900, 'completed', '2026-10-18T09:00:00Z', '2026-10-18T09:01:00Z'),
  ('task', 'staffing', 'copilot-call', 1, 'orchestrator', 'orchestrator', 'copilot', '', 0, 0, 0, 67, 33, 1, 900, 'completed', '2026-10-18T09:00:00Z', '2026-10-18T09:01:00Z'),
  ('task', 'staffing', 'copilot-work-call', 1, 'orchestrator', 'orchestrator', 'copilot-work', '', 0, 0, 0, 67, 33, 1, 900, 'completed', '2026-10-18T09:00:00Z', '2026-10-18T09:01:00Z');`
	if _, err := store.DB().ExecContext(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	calls, err := store.PlanningUsage(ctx, "task")
	if err != nil || len(calls) != 4 {
		t.Fatalf("planning usage = %+v, %v", calls, err)
	}
	for _, want := range []struct {
		usage domain.Usage
		cost  domain.Cost
	}{
		// Claude Code's cost stays reported.
		{domain.Usage{InputTokens: 8, CacheWriteTokens: 28325, CacheReadTokens: 27913, OutputTokens: 192, ReportedCents: 5, Turns: 3}, domain.Cost{Cents: 5, Source: domain.CostReported}},
		// Codex reported nothing, and SQL cannot price its tokens.
		{domain.Usage{InputTokens: 315, CacheReadTokens: 24448, OutputTokens: 122}, domain.Cost{Source: domain.CostUnknown}},
		// Copilot reports no cost, so a figure stored for it is not one.
		{domain.Usage{OutputTokens: 67, Turns: 1}, domain.Cost{Source: domain.CostUnknown}},
		// A runtime added in inkuna.yaml may have written Copilot's stream,
		// so its figure is not taken for money either.
		{domain.Usage{OutputTokens: 67, Turns: 1}, domain.Cost{Source: domain.CostUnknown}},
	} {
		call := calls[0]
		calls = calls[1:]
		if call.Usage != want.usage || call.Cost != want.cost {
			t.Errorf("%s after the migration: %+v %+v; want %+v %+v", call.CallID, call.Usage, call.Cost, want.usage, want.cost)
		}
	}
	if _, err := store.RecordUsage(ctx, domain.UsageRecord{TaskID: "task", StepID: "staffing", CallID: "new", Kind: domain.CallOrchestrator, Outcome: domain.OutcomeCompleted, Cost: domain.Cost{Source: "guessed"}}); err == nil {
		t.Fatal("a cost source other than reported, estimated, or unknown must be refused")
	}
}
