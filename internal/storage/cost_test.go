package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// costFixture is a migrated store with one project and helpers to add
// tasks, runs, and calls to it.
type costFixture struct {
	t       *testing.T
	store   *Store
	ctx     context.Context
	project Project
	start   time.Time
}

func newCostFixture(t *testing.T) *costFixture {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixture := &costFixture{t: t, store: store, ctx: context.Background(), start: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)}
	fixture.project = fixture.addProject("project")
	return fixture
}

func (fixture *costFixture) addProject(id string) Project {
	fixture.t.Helper()
	project := Project{ID: id, Name: id, Repository: fixture.t.TempDir(), DefaultRuntime: domain.RuntimeClaude}
	if err := fixture.store.UpsertProject(fixture.ctx, project); err != nil {
		fixture.t.Fatal(err)
	}
	return project
}

// addTask saves a task with a workflow of a research and an implement step
// that run side by side.
func (fixture *costFixture) addTask(project Project, id, objective string) {
	fixture.t.Helper()
	task := domain.TaskSpec{Version: 1, ID: id, Kind: domain.TaskImplementation, Objective: objective, Repository: project.Repository, Budget: domain.Budget{WallTimeSeconds: 60}}
	if err := fixture.store.SaveTask(fixture.ctx, project.ID, "approved", task); err != nil {
		fixture.t.Fatal(err)
	}
	steps := []domain.WorkflowStep{
		{ID: "research", Title: "Research", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}},
		{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}},
	}
	workflow := domain.WorkflowSpec{Version: 1, ID: id + "-workflow", TaskID: id, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: fixture.start, Steps: steps}
	if err := fixture.store.SaveWorkflow(fixture.ctx, "approved", workflow); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *costFixture) addRun(project Project, taskID, id, state string, started time.Time) {
	fixture.t.Helper()
	execution := Execution{ID: id, WorkflowID: taskID + "-workflow", TaskID: taskID, ProjectID: project.ID, State: state, Title: "Run", StartedAt: started, FinishedAt: started.Add(10 * time.Minute)}
	if err := fixture.store.SaveExecution(fixture.ctx, execution); err != nil {
		fixture.t.Fatal(err)
	}
}

// call is a call that ran for seconds and ended at finished. Without a run
// it is the orchestrator's, made while planning; without an outcome it
// completed.
type call struct {
	task, run, step, kind, agent string
	runtime                      domain.Runtime
	model, outcome, auth         string
	turn                         int
	usage                        domain.Usage
	cost                         domain.Cost
	finished                     time.Time
	seconds                      int
}

func (fixture *costFixture) record(calls ...call) []domain.UsageRecord {
	fixture.t.Helper()
	records := make([]domain.UsageRecord, 0, len(calls))
	for index, current := range calls {
		if current.run == "" {
			current.kind, current.agent = domain.CallOrchestrator, "orchestrator"
		}
		if current.outcome == "" {
			current.outcome = domain.OutcomeCompleted
		}
		if current.turn == 0 {
			current.turn = 1
		}
		record := domain.UsageRecord{
			TaskID: current.task, ExecutionID: current.run, StepID: current.step, CallID: fmt.Sprintf("%s-%s-%d", current.step, current.agent, index), Turn: current.turn, Kind: current.kind,
			Agent: current.agent, Runtime: current.runtime, Model: current.model, AuthMode: current.auth, Usage: current.usage, Cost: current.cost, Outcome: current.outcome,
			StartedAt: current.finished.Add(-time.Duration(current.seconds) * time.Second), FinishedAt: current.finished,
		}
		id, err := fixture.store.RecordUsage(fixture.ctx, record)
		if err != nil {
			fixture.t.Fatalf("record call %d: %v", index, err)
		}
		record.ID, record.Sections = id, []domain.PromptSection{}
		records = append(records, record)
	}
	return records
}

func reported(cents float64) domain.Cost {
	return domain.Cost{Cents: cents, Source: domain.CostReported, EstimatedCents: cents, PricingVersion: 1}
}

func estimated(cents float64) domain.Cost {
	return domain.Cost{Cents: cents, Source: domain.CostEstimated, EstimatedCents: cents, PricingVersion: 1}
}

var unknown = domain.Cost{Source: domain.CostUnknown}

// wantTotals is a level's totals written out by hand: calls, reported and
// estimated cents, unknown calls, fresh input and output tokens, and seconds.
// subscription is the reported cents a subscription login reported.
type wantTotals struct {
	calls                             int
	reported, estimated, subscription float64
	unknown                           int
	input, output, secs               int
	cacheWrite, cacheRead             int
}

func checkTotals(t *testing.T, level string, got domain.CallTotals, want wantTotals) {
	t.Helper()
	expected := domain.CallTotals{
		Usage: domain.Usage{InputTokens: want.input, CacheWriteTokens: want.cacheWrite, CacheReadTokens: want.cacheRead, OutputTokens: want.output},
		Cost:  domain.CostTotal{ReportedCents: want.reported, EstimatedCents: want.estimated, UnknownCalls: want.unknown},
		// Best available: each call's reported figure, else its estimate.
		BestCents: want.reported + want.estimated, SubscriptionCents: want.subscription, Calls: want.calls, DurationMs: int64(want.secs) * 1000,
	}
	if got != expected {
		t.Fatalf("%s totals = %+v, want %+v", level, got, expected)
	}
}

func TestTaskCostAddsUpAtEveryLevel(t *testing.T) {
	fixture := newCostFixture(t)
	project, start := fixture.project, fixture.start
	fixture.addTask(project, "task", "Add a cost view")
	at := func(minutes int) time.Time { return start.Add(time.Duration(minutes) * time.Minute) }
	// Runs are saved out of order: the task's runs come in the order they
	// started.
	fixture.addRun(project, "task", "run-failed", "failed", at(30))
	fixture.addRun(project, "task", "run-first", "completed", at(10))
	fixture.addRun(project, "task", "run-again", "completed", at(50))
	fixture.addRun(project, "task", "run-cancelled", "cancelled", at(70))
	calls := fixture.record(
		// The first plan staffed and proposed branching; the re-plan staffed
		// again on Codex, which priced nothing for a model it did not name.
		call{task: "task", step: "staffing", runtime: domain.RuntimeClaude, model: "haiku", usage: domain.Usage{InputTokens: 100, OutputTokens: 10}, cost: reported(2), finished: at(1), seconds: 4},
		call{task: "task", step: "branching", runtime: domain.RuntimeClaude, model: "haiku", usage: domain.Usage{InputTokens: 50, OutputTokens: 5}, cost: estimated(0.5), finished: at(2), seconds: 2},
		call{task: "task", step: "staffing", runtime: domain.RuntimeCodex, usage: domain.Usage{InputTokens: 80, CacheReadTokens: 40, OutputTokens: 8}, cost: unknown, finished: at(5), seconds: 3},
		// The first run: its steps ran side by side, and implement's call
		// ended, so was stored, before research's first turn, which started
		// earlier. A reviewer on Copilot answered research's second turn.
		call{task: "task", run: "run-first", step: "implement", kind: domain.CallWorker, agent: "implementer", runtime: domain.RuntimeCodex, model: "gpt-5.5", usage: domain.Usage{InputTokens: 300, OutputTokens: 30}, cost: estimated(2.5), finished: at(12), seconds: 30},
		call{task: "task", run: "run-first", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, model: "haiku", usage: domain.Usage{InputTokens: 10, CacheWriteTokens: 1000, OutputTokens: 100}, cost: reported(4), finished: at(13), seconds: 120},
		call{task: "task", run: "run-first", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, model: "haiku", turn: 2, usage: domain.Usage{InputTokens: 20, CacheReadTokens: 1000, OutputTokens: 200}, cost: reported(3), finished: at(15), seconds: 60},
		call{task: "task", run: "run-first", step: "research", kind: domain.CallPeer, agent: "reviewer", runtime: domain.RuntimeCopilot, model: "claude-sonnet-5", turn: 2, usage: domain.Usage{OutputTokens: 40}, cost: estimated(1.25), finished: at(16), seconds: 30},
		// The failed run stopped in research, and its call was paid for all
		// the same.
		call{task: "task", run: "run-failed", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, model: "haiku", outcome: domain.OutcomeFailed, usage: domain.Usage{InputTokens: 15, OutputTokens: 50}, cost: reported(1.5), finished: at(32), seconds: 45},
		// The run again left the model to Codex, which did not name it.
		call{task: "task", run: "run-again", step: "implement", kind: domain.CallWorker, agent: "implementer", runtime: domain.RuntimeCodex, usage: domain.Usage{InputTokens: 70, OutputTokens: 7}, cost: unknown, finished: at(52), seconds: 20},
	)
	// Another task's calls are not this task's.
	fixture.addTask(project, "other", "Something else")
	fixture.addRun(project, "other", "other-run", "completed", at(10))
	fixture.record(call{task: "other", run: "other-run", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, usage: domain.Usage{OutputTokens: 1}, cost: reported(99), finished: at(11), seconds: 1})

	cost, err := fixture.store.TaskCost(fixture.ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	if cost.TaskID != "task" {
		t.Fatalf("task id = %q", cost.TaskID)
	}
	checkTotals(t, "task", cost.Total, wantTotals{calls: 9, reported: 10.5, estimated: 4.25, unknown: 2, input: 645, cacheWrite: 1000, cacheRead: 1040, output: 450, secs: 314})

	checkTotals(t, "planning", cost.Planning.Total, wantTotals{calls: 3, reported: 2, estimated: 0.5, unknown: 1, input: 230, cacheRead: 40, output: 23, secs: 9})
	if !reflect.DeepEqual(cost.Planning.Calls, calls[:3]) {
		t.Fatalf("planning calls = %+v", cost.Planning.Calls)
	}

	if len(cost.Runs) != 4 {
		t.Fatalf("runs = %+v", cost.Runs)
	}
	first, failed, again, cancelled := cost.Runs[0], cost.Runs[1], cost.Runs[2], cost.Runs[3]
	for index, want := range []struct{ id, state string }{{"run-first", "completed"}, {"run-failed", "failed"}, {"run-again", "completed"}, {"run-cancelled", "cancelled"}} {
		if run := cost.Runs[index]; run.ExecutionID != want.id || run.State != want.state || run.StartedAt.IsZero() || run.FinishedAt.IsZero() {
			t.Fatalf("run %d = %s %s, want %s %s", index, run.ExecutionID, run.State, want.id, want.state)
		}
	}
	checkTotals(t, "first run", first.Total, wantTotals{calls: 4, reported: 7, estimated: 3.75, input: 330, cacheWrite: 1000, cacheRead: 1000, output: 370, secs: 240})
	checkTotals(t, "failed run", failed.Total, wantTotals{calls: 1, reported: 1.5, input: 15, output: 50, secs: 45})
	checkTotals(t, "run again", again.Total, wantTotals{calls: 1, unknown: 1, input: 70, output: 7, secs: 20})
	checkTotals(t, "cancelled run", cancelled.Total, wantTotals{})
	if len(cancelled.Steps) != 0 {
		t.Fatalf("a run without calls has no steps: %+v", cancelled.Steps)
	}

	// Research started first, so it comes first though implement's call was
	// stored first, and its worker, not the peer that answered it, names the
	// step's agent, runtime, and model.
	if len(first.Steps) != 2 {
		t.Fatalf("first run steps = %+v", first.Steps)
	}
	research, implement := first.Steps[0], first.Steps[1]
	if research.StepID != "research" || research.Agent != "researcher" || research.Runtime != domain.RuntimeClaude || research.Model != "haiku" || !research.StartedAt.Equal(at(11)) {
		t.Fatalf("research step = %s %s %s %s from %s", research.StepID, research.Agent, research.Runtime, research.Model, research.StartedAt)
	}
	checkTotals(t, "first run research", research.Total, wantTotals{calls: 3, reported: 7, estimated: 1.25, input: 30, cacheWrite: 1000, cacheRead: 1000, output: 340, secs: 210})
	if want := calls[4:7]; !reflect.DeepEqual(research.Calls, want) {
		t.Fatalf("research calls = %+v, want its two turns and the peer's answer", research.Calls)
	}
	if implement.StepID != "implement" || implement.Agent != "implementer" || implement.Runtime != domain.RuntimeCodex || implement.Model != "gpt-5.5" {
		t.Fatalf("implement step = %+v", implement)
	}
	checkTotals(t, "first run implement", implement.Total, wantTotals{calls: 1, estimated: 2.5, input: 300, output: 30, secs: 30})
	if len(failed.Steps) != 1 || failed.Steps[0].Calls[0].Outcome != domain.OutcomeFailed {
		t.Fatalf("failed run steps = %+v", failed.Steps)
	}
	if len(again.Steps) != 1 || again.Steps[0].Model != "" {
		t.Fatalf("a step whose runtime chose the model names none: %+v", again.Steps)
	}

	// The groups, most expensive first. Each adds up to the task, except
	// steps, which leave planning out.
	groups := func(list []domain.CostGroup) map[string]float64 {
		best := make(map[string]float64, len(list))
		for _, group := range list {
			best[group.Key] = group.Total.BestCents
		}
		return best
	}
	keys := func(list []domain.CostGroup) []string {
		names := make([]string, 0, len(list))
		for _, group := range list {
			names = append(names, group.Key)
		}
		return names
	}
	if got, want := keys(cost.ByAgent), []string{"researcher", "implementer", "orchestrator", "reviewer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("agents = %v, want %v (%v)", got, want, groups(cost.ByAgent))
	}
	if got, want := groups(cost.ByAgent), map[string]float64{"researcher": 8.5, "orchestrator": 2.5, "implementer": 2.5, "reviewer": 1.25}; !reflect.DeepEqual(got, want) {
		t.Fatalf("agent costs = %v, want %v", got, want)
	}
	if got, want := keys(cost.ByRuntime), []string{"claude", "codex", "copilot"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("runtimes = %v, want %v", got, want)
	}
	checkTotals(t, "codex", cost.ByRuntime[1].Total, wantTotals{calls: 3, estimated: 2.5, unknown: 2, input: 450, cacheRead: 40, output: 45, secs: 53})
	if got, want := groups(cost.ByModel), map[string]float64{"haiku": 11, "gpt-5.5": 2.5, "claude-sonnet-5": 1.25, "": 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("model costs = %v, want %v", got, want)
	}
	if got, want := keys(cost.ByModel), []string{"haiku", "gpt-5.5", "claude-sonnet-5", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	if got, want := keys(cost.ByStep), []string{"research", "implement"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	checkTotals(t, "research across runs", cost.ByStep[0].Total, wantTotals{calls: 4, reported: 8.5, estimated: 1.25, input: 45, cacheWrite: 1000, cacheRead: 1000, output: 390, secs: 255})
	checkTotals(t, "implement across runs", cost.ByStep[1].Total, wantTotals{calls: 2, estimated: 2.5, unknown: 1, input: 370, output: 37, secs: 50})
	sum := func(list []domain.CostGroup) domain.CallTotals {
		var total domain.CallTotals
		for _, group := range list {
			total.Usage, total.Cost = total.Usage.Add(group.Total.Usage), domain.CostTotal{
				ReportedCents: total.Cost.ReportedCents + group.Total.Cost.ReportedCents, EstimatedCents: total.Cost.EstimatedCents + group.Total.Cost.EstimatedCents,
				UnknownCalls: total.Cost.UnknownCalls + group.Total.Cost.UnknownCalls,
			}
			total.BestCents += group.Total.BestCents
			total.Calls += group.Total.Calls
			total.DurationMs += group.Total.DurationMs
		}
		return total
	}
	for name, list := range map[string][]domain.CostGroup{"agents": cost.ByAgent, "runtimes": cost.ByRuntime, "models": cost.ByModel} {
		if got := sum(list); got != cost.Total {
			t.Fatalf("%s add up to %+v, not the task's %+v", name, got, cost.Total)
		}
	}
	checkTotals(t, "steps", sum(cost.ByStep), wantTotals{calls: 6, reported: 8.5, estimated: 3.75, unknown: 1, input: 415, cacheWrite: 1000, cacheRead: 1000, output: 427, secs: 305})
}

func TestTaskCostOfAPlanThatWasNeverSaved(t *testing.T) {
	fixture := newCostFixture(t)
	// Planning failed after its staffing call, so the task was never saved.
	calls := fixture.record(call{task: "unsaved", step: "staffing", runtime: domain.RuntimeClaude, model: "haiku", usage: domain.Usage{InputTokens: 9}, cost: reported(1), finished: fixture.start, seconds: 1})
	cost, err := fixture.store.TaskCost(fixture.ctx, "unsaved")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cost.Planning.Calls, calls) || len(cost.Runs) != 0 || cost.Total.BestCents != 1 {
		t.Fatalf("cost = %+v", cost)
	}
	// A task with nothing recorded costs nothing; an id that is neither is
	// not found.
	fixture.addTask(fixture.project, "idle", "Nothing yet")
	idle, err := fixture.store.TaskCost(fixture.ctx, "idle")
	if err != nil || idle.Total != (domain.CallTotals{}) || len(idle.Runs) != 0 || len(idle.Planning.Calls) != 0 || idle.ByAgent == nil {
		t.Fatalf("idle cost = %+v, %v", idle, err)
	}
	if _, err := fixture.store.TaskCost(fixture.ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing task error = %v", err)
	}
}

func TestPeriodCostSumsEachTasksCallsInThePeriod(t *testing.T) {
	fixture := newCostFixture(t)
	project, start := fixture.project, fixture.start
	fixture.addTask(project, "early", "Early task")
	fixture.addTask(project, "late", "Late task")
	elsewhere := fixture.addProject("elsewhere")
	fixture.addTask(elsewhere, "foreign", "Another project's task")
	fixture.addRun(project, "early", "early-run", "completed", start)
	fixture.addRun(project, "early", "early-rerun", "failed", start.Add(3*time.Hour))
	fixture.addRun(project, "late", "late-run", "running", start)
	fixture.addRun(elsewhere, "foreign", "foreign-run", "completed", start)
	// Fractions of a second around the bounds: the period runs from 10:00:00.25
	// to 11:00:00.5.
	from, to := start.Add(time.Hour+250*time.Millisecond), start.Add(2*time.Hour+500*time.Millisecond)
	fixture.record(
		call{task: "early", step: "staffing", runtime: domain.RuntimeClaude, usage: domain.Usage{InputTokens: 1}, cost: reported(100), finished: start.Add(30 * time.Minute)},
		call{task: "early", run: "early-run", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, usage: domain.Usage{InputTokens: 2}, cost: reported(200), finished: start.Add(time.Hour + 100*time.Millisecond)},
		call{task: "early", run: "early-run", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, turn: 2, usage: domain.Usage{InputTokens: 10}, cost: reported(2), finished: from, seconds: 5},
		call{task: "early", run: "early-run", step: "implement", kind: domain.CallWorker, agent: "implementer", runtime: domain.RuntimeCodex, usage: domain.Usage{InputTokens: 20}, cost: estimated(5.5), finished: start.Add(90 * time.Minute), seconds: 10},
		call{task: "late", step: "staffing", runtime: domain.RuntimeCodex, usage: domain.Usage{InputTokens: 30}, cost: unknown, finished: start.Add(100 * time.Minute), seconds: 1},
		// Claude Code signed in to a subscription reported this one.
		call{task: "late", run: "late-run", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, auth: domain.AuthSubscription, usage: domain.Usage{InputTokens: 40}, cost: reported(4), finished: to.Add(-250 * time.Millisecond), seconds: 2},
		call{task: "late", run: "late-run", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, turn: 2, usage: domain.Usage{InputTokens: 50}, cost: reported(300), finished: to},
		call{task: "late", run: "late-run", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, turn: 3, usage: domain.Usage{InputTokens: 60}, cost: reported(400), finished: to.Add(time.Second)},
		call{task: "foreign", run: "foreign-run", step: "research", kind: domain.CallWorker, agent: "researcher", runtime: domain.RuntimeClaude, usage: domain.Usage{InputTokens: 70}, cost: reported(500), finished: start.Add(90 * time.Minute)},
		// A plan that failed before its task was saved belongs to no project.
		call{task: "unsaved", step: "staffing", runtime: domain.RuntimeClaude, usage: domain.Usage{InputTokens: 80}, cost: reported(600), finished: start.Add(90 * time.Minute)},
	)

	cost, err := fixture.store.PeriodCost(fixture.ctx, project.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(cost.Tasks) != 2 {
		t.Fatalf("tasks = %+v", cost.Tasks)
	}
	// Early cost more in the period, so it comes first though late was
	// active since.
	early, late := cost.Tasks[0], cost.Tasks[1]
	if early.TaskID != "early" || early.ProjectID != project.ID || early.Objective != "Early task" || early.Title != "Early task" || early.Kind != domain.TaskImplementation || early.State != "failed" ||
		early.LatestRunID != "early-rerun" || !early.LastCallAt.Equal(start.Add(90*time.Minute)) {
		t.Fatalf("the most expensive task comes first, with its latest run: %+v", early)
	}
	checkTotals(t, "early", early.Total, wantTotals{calls: 2, reported: 2, estimated: 5.5, input: 30, secs: 15})
	if late.TaskID != "late" || late.Objective != "Late task" || late.LatestRunID != "late-run" || late.State != "running" || !late.LastCallAt.Equal(to.Add(-250*time.Millisecond)) {
		t.Fatalf("late = %+v", late)
	}
	checkTotals(t, "late", late.Total, wantTotals{calls: 2, reported: 4, subscription: 4, unknown: 1, input: 70, secs: 3})
	checkTotals(t, "period", cost.Total, wantTotals{calls: 4, reported: 6, estimated: 5.5, subscription: 4, unknown: 1, input: 100, secs: 18})

	// Open bounds take every call the project's tasks made, and late cost
	// more over all of them.
	all, err := fixture.store.PeriodCost(fixture.ctx, project.ID, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	checkTotals(t, "all time", all.Total, wantTotals{calls: 8, reported: 1006, estimated: 5.5, subscription: 4, unknown: 1, input: 213, secs: 18})
	if len(all.Tasks) != 2 || all.Tasks[0].TaskID != "late" {
		t.Fatalf("all tasks = %+v", all.Tasks)
	}
	none, err := fixture.store.PeriodCost(fixture.ctx, project.ID, start.Add(10*time.Hour), time.Time{})
	if err != nil || len(none.Tasks) != 0 || none.Tasks == nil || none.Total != (domain.CallTotals{}) {
		t.Fatalf("a period with no calls = %+v, %v", none, err)
	}
	// Without a project, every project's tasks count, and still not the
	// unsaved plan's.
	everywhere, err := fixture.store.PeriodCost(fixture.ctx, "", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(everywhere.Tasks) != 3 || everywhere.Tasks[0].TaskID != "late" || everywhere.Tasks[1].TaskID != "foreign" || everywhere.Tasks[1].ProjectID != elsewhere.ID {
		t.Fatalf("every project's tasks = %+v", everywhere.Tasks)
	}
	checkTotals(t, "every project", everywhere.Total, wantTotals{calls: 9, reported: 1506, estimated: 5.5, subscription: 4, unknown: 1, input: 283, secs: 18})

	// A task's totals cover all its calls, whenever they ended; a task with
	// none is left out.
	totals, err := fixture.store.TaskTotals(fixture.ctx, []string{"early", "late", "foreign", "unsaved", "nothing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(totals) != 4 || totals["early"] != all.Tasks[1].Total || totals["late"] != all.Tasks[0].Total || totals["foreign"] != everywhere.Tasks[1].Total || totals["unsaved"].BestCents != 600 {
		t.Fatalf("task totals = %+v", totals)
	}
	if empty, err := fixture.store.TaskTotals(fixture.ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no tasks = %+v, %v", empty, err)
	}

	// A task whose plan was approved and that never ran is approved.
	fixture.addTask(project, "unrun", "Planned only")
	later := start.Add(24 * time.Hour)
	fixture.record(call{task: "unrun", step: "staffing", runtime: domain.RuntimeClaude, cost: reported(1), finished: later})
	unrun, err := fixture.store.PeriodCost(fixture.ctx, project.ID, later, time.Time{})
	if err != nil || len(unrun.Tasks) != 1 || unrun.Tasks[0].State != "approved" || unrun.Tasks[0].LatestRunID != "" {
		t.Fatalf("a task never run = %+v, %v", unrun.Tasks, err)
	}
}
