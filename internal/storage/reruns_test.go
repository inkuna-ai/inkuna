package storage

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestAStepRunKeepsOnlyItsLatestTurnsDenials(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	if err := store.StartRun(ctx, "run-1-implement", "run-1", spec.ID, "implement", 1, domain.RuntimeClaude, ""); err != nil {
		t.Fatal(err)
	}
	denied := []domain.PermissionDenial{{Tool: "Bash", Command: "go test ./...", Detail: "go test ./..."}, {Tool: "Edit", Detail: "main.go"}}
	if err := store.SetStepDenials(ctx, "run-1-implement", denied); err != nil {
		t.Fatal(err)
	}
	runs, err := store.StepRuns(ctx, "run-1")
	if err != nil || len(runs) != 1 || !slices.Equal(runs[0].Denials, denied) {
		t.Fatalf("the step run lists its denials: %+v %v", runs, err)
	}
	// The next turn was granted what it asked for and was refused nothing.
	if err := store.SetStepDenials(ctx, "run-1-implement", nil); err != nil {
		t.Fatal(err)
	}
	if runs, err = store.StepRuns(ctx, "run-1"); err != nil || runs[0].Denials != nil {
		t.Fatalf("a later turn replaces the denials: %+v %v", runs, err)
	}
}

func TestARerunRecordsTheRunItStartedOverFrom(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	rerun := Execution{ID: "run-2", WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: "running", Title: "Run", RerunOf: "run-1"}
	if err := store.SaveExecution(ctx, rerun); err != nil {
		t.Fatal(err)
	}
	// Saving the run again as it moves on never changes where it came from.
	rerun.State, rerun.RerunOf = "completed", ""
	if err := store.SaveExecution(ctx, rerun); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Execution(ctx, "run-2")
	if err != nil || saved.RerunOf != "run-1" || saved.State != "completed" {
		t.Fatalf("the rerun names the run it started over from: %+v %v", saved, err)
	}
	if original, err := store.Execution(ctx, "run-1"); err != nil || original.RerunOf != "" {
		t.Fatalf("the original run names none: %+v %v", original, err)
	}
}

func TestAnEditedResumeReplacesThePlanAndKeepsTheOneItReplaced(t *testing.T) {
	store, approved := roundsStore(t)
	ctx := context.Background()
	if err := store.SaveExecutionPlan(ctx, "run-1", approved); err != nil {
		t.Fatal(err)
	}
	execution, err := store.Execution(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	execution.State, execution.FinishedAt = "failed", time.Now().UTC()
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	edited := approved
	edited.Steps = slices.Clone(approved.Steps)
	edited.Steps[0].Commands = [][]string{{"go", "test", "./..."}}
	if _, err := store.StartResumeRound(ctx, "run-1", 1, nil, false, &PlanEdit{Replaced: approved, Plan: edited}); err == nil {
		t.Fatal("an edited resume must name the steps the person edited")
	}
	if started, err := store.StartResumeRound(ctx, "run-1", 1, nil, false, &PlanEdit{Replaced: approved, Plan: edited, Steps: []string{"implement"}}); err != nil || !started {
		t.Fatalf("resume under the edited plan: %v %v", started, err)
	}
	current, err := store.ExecutionPlan(ctx, "run-1")
	if err != nil || len(current.Steps[0].Commands) != 1 {
		t.Fatalf("the run now runs the edited plan: %+v %v", current.Steps[0], err)
	}
	for round, want := range map[int]int{1: 0, 2: 1} {
		plan, err := store.RoundPlan(ctx, "run-1", round)
		if err != nil || len(plan.Steps[0].Commands) != want {
			t.Fatalf("round %d ran under the plan of its own time: %+v %v", round, plan.Steps[0], err)
		}
	}
	resumes, err := store.ResumedRounds(ctx, "run-1")
	if err != nil || !slices.Equal(resumes[2].Edited, []string{"implement"}) {
		t.Fatalf("the resume names the steps the person edited: %+v %v", resumes, err)
	}
	if edited := EditedRounds(resumes); len(edited) != 1 || !slices.Equal(edited[2], []string{"implement"}) {
		t.Fatalf("edited rounds = %v", edited)
	}
}
