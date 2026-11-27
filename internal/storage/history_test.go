package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestStepRunsListEveryRoundsOwnRunsInOrder(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	start := func(id, step string, round int) {
		t.Helper()
		if err := store.StartRun(ctx, id, "run-1", spec.ID, step, round, domain.RuntimeClaude, ""); err != nil {
			t.Fatal(err)
		}
	}
	start("run-1-implement", "implement", 1)
	if err := store.FinishRun(ctx, "run-1-implement", "completed", domain.HandoffResult{Version: 1, Status: "completed", Summary: "Added the flag"}); err != nil {
		t.Fatal(err)
	}
	// A peer's row shares its step's id but is never one of its runs.
	start("run-1-implement-peer1-reviewer", "implement", 1)
	start("run-1-approval", "approval", 1)
	if err := store.FinishRun(ctx, "run-1-approval", "changes-requested", domain.HandoffResult{Version: 1, Status: "changes-requested", Summary: "Changes requested for round 2"}); err != nil {
		t.Fatal(err)
	}
	start("run-1.r2.implement", "implement", 2)
	runs, err := store.StepRuns(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(runs))
	for _, run := range runs {
		got = append(got, fmt.Sprintf("%d %s %s %q %v", run.Round, run.StepID, run.State, run.Summary, run.FinishedAt.IsZero()))
	}
	want := []string{
		`1 implement completed "Added the flag" false`,
		`1 approval changes-requested "Changes requested for round 2" false`,
		`2 implement running "" true`,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("step runs\n%v\nwant\n%v", got, want)
	}
	if runs[2].ID != "run-1.r2.implement" || runs[2].StartedAt.IsZero() {
		t.Fatalf("a run keeps its id and start: %+v", runs[2])
	}
}

func TestRoundEventsKeepEachRoundsLatestInStoredOrder(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	for _, run := range []struct {
		id    string
		round int
	}{{"run-1-implement", 1}, {"run-1-implement-peer1-reviewer", 1}, {"run-1.r2.implement", 2}} {
		if err := store.StartRun(ctx, run.id, "run-1", spec.ID, "implement", run.round, domain.RuntimeClaude, ""); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	add := func(runID string, sequence int64, summary string) {
		t.Helper()
		// Timestamps go backwards so only the stored order can put them right.
		at = at.Add(-time.Second)
		if err := store.AppendEvent(ctx, domain.RunEvent{Version: 1, RunID: runID, Sequence: sequence, Timestamp: at, Kind: domain.EventLifecycle, Summary: summary}); err != nil {
			t.Fatal(err)
		}
	}
	add("run-1-implement", 1, "one")
	add("run-1-implement-peer1-reviewer", 1, "peer")
	add("run-1-implement", 2, "two")
	add("run-1.r2.implement", 1, "round two")
	summaries := func(round, limit int) (string, int) {
		t.Helper()
		events, hidden, err := store.RoundEvents(ctx, "run-1", round, limit)
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(events))
		for _, event := range events {
			names = append(names, event.Summary)
		}
		return fmt.Sprint(names), hidden
	}
	if got, hidden := summaries(1, 10); got != "[one peer two]" || hidden != 0 {
		t.Fatalf("round 1 has its steps' and peers' events in stored order: %s, %d hidden", got, hidden)
	}
	if got, hidden := summaries(1, 2); got != "[peer two]" || hidden != 1 {
		t.Fatalf("a limit keeps the latest events: %s, %d hidden", got, hidden)
	}
	if got, hidden := summaries(2, 10); got != "[round two]" || hidden != 0 {
		t.Fatalf("round 2 has only its own: %s, %d hidden", got, hidden)
	}
	if got, _ := summaries(3, 10); got != "[]" {
		t.Fatalf("a round with no runs has no events: %s", got)
	}
}

func TestPreviousAnswerIsTheLastAnswerToTheSameStepsQuestion(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	save := func(input InputRequest) {
		t.Helper()
		input.ExecutionID, input.Agent = "run-1", "human"
		if input.CreatedAt.IsZero() {
			input.CreatedAt = time.Now().UTC()
		}
		if err := store.SaveInput(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	asked := InputRequest{ID: "input-3", ExecutionID: "run-1", StepID: "choose", Question: "Which format?", State: InputPending}
	save(asked)
	if answer, err := store.PreviousAnswer(ctx, asked); err != nil || answer != "" {
		t.Fatalf("a first question has no previous answer: %q %v", answer, err)
	}
	save(InputRequest{ID: "input-1", StepID: "choose", Question: "Which format?", State: InputAnswered, Answer: "JSON"})
	save(InputRequest{ID: "input-2", StepID: "choose", Question: "Which format?", State: InputAnswered, Answer: "YAML"})
	// Another step's answer, another question's, and a review's reply never count.
	save(InputRequest{ID: "input-4", StepID: "other", Question: "Which format?", State: InputAnswered, Answer: "TOML"})
	save(InputRequest{ID: "input-5", StepID: "choose", Question: "Which name?", State: InputAnswered, Answer: "inkuna"})
	save(InputRequest{ID: "input-6", StepID: "choose", Question: "Which format?", State: InputAnswered, Answer: "Fix the flag", Review: true, ReviewTarget: "implement"})
	answer, err := store.PreviousAnswer(ctx, asked)
	if err != nil || answer != "YAML" {
		t.Fatalf("previous answer %q, %v; want the latest, YAML", answer, err)
	}
}

func TestACallIsInTheRoundOfTheStepRunThatMadeIt(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	for _, run := range []struct {
		id    string
		round int
	}{{"run-1-implement", 1}, {"run-1.r2.implement", 2}} {
		if err := store.StartRun(ctx, run.id, "run-1", spec.ID, "implement", run.round, domain.RuntimeClaude, ""); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	record := func(execution, callID string, turn int) {
		t.Helper()
		call := domain.UsageRecord{TaskID: "task-1", ExecutionID: execution, StepID: "implement", CallID: callID, Turn: turn, Kind: domain.CallWorker, Agent: "implementer",
			Runtime: domain.RuntimeClaude, Model: "haiku", Outcome: domain.OutcomeCompleted, StartedAt: at, FinishedAt: at}
		if execution == "" {
			call.Kind, call.StepID = domain.CallOrchestrator, "staffing"
		}
		if _, err := store.RecordUsage(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	record("run-1", "run-1-implement", 1)
	record("run-1", "run-1-implement-turn2", 2)
	record("run-1", "run-1.r2.implement", 1)
	// A later turn's call is named after its step run with -turn<N> added.
	record("run-1", "run-1.r2.implement-turn2", 2)
	record("", "planning-1", 1)
	// A run's usage starts with its task's planning, which no round made.
	calls, err := store.RunUsage(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(calls))
	for _, call := range calls {
		got = append(got, fmt.Sprintf("%s=%d", call.CallID, call.Round))
	}
	want := "[planning-1=0 run-1-implement=1 run-1-implement-turn2=1 run-1.r2.implement=2 run-1.r2.implement-turn2=2]"
	if fmt.Sprint(got) != want {
		t.Fatalf("rounds %v, want %s", got, want)
	}
}

func TestRunDirectoriesListsWhereARunsStepsWorkedOnce(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	for _, run := range []struct {
		id, step, directory string
		round               int
	}{
		{"run-1-implement", "implement", "/repository", 1},
		{"run-1-implement-peer1-reviewer", "implement", "/repository", 1},
		{"run-1-approval", "approval", "", 1},
		{"run-1.r2.implement", "implement", "/worktrees/run-1", 2},
	} {
		if err := store.StartRun(ctx, run.id, "run-1", spec.ID, run.step, run.round, domain.RuntimeClaude, run.directory); err != nil {
			t.Fatal(err)
		}
	}
	directories, err := store.RunDirectories(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(directories) != "[/repository /worktrees/run-1]" {
		t.Fatalf("directories %v", directories)
	}
}
