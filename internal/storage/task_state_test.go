package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// A task's state follows its plan's approval and then its latest run, though
// tasks.state keeps the state it was planned in.
func TestATasksStateFollowsApprovalAndItsLatestRun(t *testing.T) {
	fixture := newCostFixture(t)
	store, ctx, project := fixture.store, fixture.ctx, fixture.project
	plan := func(id string) domain.WorkflowSpec {
		t.Helper()
		task := domain.TaskSpec{Version: 1, ID: id, Kind: domain.TaskImplementation, Objective: "Derive " + id, Repository: project.Repository, Budget: domain.Budget{WallTimeSeconds: 60}}
		if err := store.SaveTask(ctx, project.ID, "planned", task); err != nil {
			t.Fatal(err)
		}
		workflow := domain.WorkflowSpec{Version: 1, ID: id + "-workflow", TaskID: id, DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, CreatedAt: fixture.start,
			Steps: []domain.WorkflowStep{{ID: "implement", Title: "Implement", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 10}}}}
		if err := store.SaveWorkflow(ctx, "planned", workflow); err != nil {
			t.Fatal(err)
		}
		return workflow
	}
	expect := func(step, want string) {
		t.Helper()
		if got, err := store.TaskState(ctx, "task"); err != nil || got != want {
			t.Fatalf("%s: state = %q, %v; want %q", step, got, err, want)
		}
	}
	run := func(id, state string) {
		t.Helper()
		execution := Execution{ID: id, WorkflowID: "task-workflow", TaskID: "task", ProjectID: project.ID, State: state, Title: "Run", StartedAt: fixture.start}
		if err := store.SaveExecution(ctx, execution); err != nil {
			t.Fatal(err)
		}
	}

	workflow := plan("task")
	expect("planned", "planned")
	if err := store.ApproveWorkflow(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	expect("approved", "approved")
	run("first", "running")
	expect("running", "running")
	run("first", "failed")
	expect("failed", "failed")
	run("second", "queued")
	expect("run again", "queued")
	run("second", "completed")
	expect("completed", "completed")
	// The latest run is the last one started, not the last one written: a
	// change to the earlier run, such as reopening it, leaves the task where
	// its latest run is.
	run("first", "failed")
	expect("an earlier run written again", "completed")

	plan("draft")
	states, err := store.TaskStates(ctx, []string{"task", "draft", "missing"})
	if err != nil || len(states) != 2 || states["task"] != "completed" || states["draft"] != "planned" {
		t.Fatalf("task states = %v, %v", states, err)
	}
	if empty, err := store.TaskStates(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no tasks = %v, %v", empty, err)
	}
	if _, err := store.TaskState(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a task not stored: %v", err)
	}

	// The period's costs show the same state.
	fixture.record(call{task: "task", run: "second", step: "implement", kind: domain.CallWorker, agent: "implementer", runtime: domain.RuntimeClaude, cost: reported(1), finished: fixture.start.Add(time.Minute)})
	fixture.record(call{task: "draft", step: "staffing", runtime: domain.RuntimeClaude, cost: reported(1), finished: fixture.start.Add(time.Minute)})
	period, err := store.PeriodCost(ctx, project.ID, time.Time{}, time.Time{})
	if err != nil || len(period.Tasks) != 2 {
		t.Fatalf("period = %+v, %v", period, err)
	}
	for _, line := range period.Tasks {
		if line.State != states[line.TaskID] {
			t.Fatalf("the costs line for %s shows %q, want %q", line.TaskID, line.State, states[line.TaskID])
		}
	}
}
