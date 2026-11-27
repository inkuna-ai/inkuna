package workflow

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestExecuteBoundsConcurrencyAndRequiresApproval(t *testing.T) {
	spec := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeCodex, MaxConcurrency: 8, Steps: []domain.WorkflowStep{
		{ID: "a", Title: "A", Kind: domain.StepScript, Permission: domain.PermissionReadOnly},
		{ID: "b", Title: "B", Kind: domain.StepScript, Permission: domain.PermissionReadOnly},
		{ID: "c", Title: "C", Kind: domain.StepScript, Permission: domain.PermissionReadOnly},
		{ID: "approval", Title: "Approval", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, DependsOn: []string{"a", "b", "c"}},
	}}
	var active atomic.Int32
	var maximum atomic.Int32
	executor := func(_ context.Context, step domain.WorkflowStep) (domain.HandoffResult, error) {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return domain.HandoffResult{Status: "completed", Summary: step.Title}, nil
	}
	results, err := Execute(context.Background(), spec, nil, executor)
	if !errors.Is(err, ErrApprovalRequired) || len(results) != 3 {
		t.Fatalf("expected approval boundary after workers: results=%v err=%v", results, err)
	}
	if maximum.Load() > 2 {
		t.Fatalf("concurrency exceeded default cap: %d", maximum.Load())
	}
	results, err = Execute(context.Background(), spec, map[string]bool{"approval": true}, executor)
	if err != nil || len(results) != 4 {
		t.Fatalf("approved workflow failed: %v %v", results, err)
	}
}

// roundSpec has two writable stages side by side, an evaluation after both,
// and an approval at the end.
func roundSpec() domain.WorkflowSpec {
	return domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeCodex, MaxConcurrency: 2, Steps: []domain.WorkflowStep{
		{ID: "plan", Title: "Plan", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly},
		{ID: "api", Title: "API", Kind: domain.StepAgent, Permission: domain.PermissionWorktree, AllowedPaths: []string{"."}, DependsOn: []string{"plan"}},
		{ID: "docs", Title: "Docs", Kind: domain.StepAgent, Permission: domain.PermissionWorktree, AllowedPaths: []string{"."}, DependsOn: []string{"plan"}},
		{ID: "verify", Title: "Verify", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, DependsOn: []string{"api", "docs"}},
		{ID: "approval", Title: "Approval", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, DependsOn: []string{"verify"}},
	}}
}

func TestRestartSetIsTheTargetsTheUnfinishedAndWhatFollows(t *testing.T) {
	spec := roundSpec()
	done := map[string]bool{"plan": true, "api": true, "docs": true, "verify": true, "approval": true}
	if got := RestartSet(spec, []string{"api"}, done); !equalSet(got, "api", "verify", "approval") {
		t.Errorf("feedback for api re-runs api and what follows it, not its sibling: %v", got)
	}
	if got := RestartSet(spec, []string{"api", "docs"}, done); !equalSet(got, "api", "docs", "verify", "approval") {
		t.Errorf("several targets re-run together: %v", got)
	}
	// A run that failed at verify left verify and approval without a result.
	failed := map[string]bool{"plan": true, "api": true, "docs": true}
	if got := RestartSet(spec, []string{"docs"}, failed); !equalSet(got, "docs", "verify", "approval") {
		t.Errorf("steps without a result run again whatever the target: %v", got)
	}
}

func equalSet(got map[string]bool, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, id := range want {
		if !got[id] {
			return false
		}
	}
	return true
}

func TestExecuteRoundRunsOnlyTheRestartSetOnPriorResults(t *testing.T) {
	spec := roundSpec()
	prior := map[string]domain.HandoffResult{
		"plan": {Status: "completed", Summary: "plan from round 1"},
		"docs": {Status: "completed", Summary: "docs from round 1"},
	}
	var ran []string
	var mutex sync.Mutex
	executor := func(_ context.Context, step domain.WorkflowStep) (domain.HandoffResult, error) {
		mutex.Lock()
		ran = append(ran, step.ID)
		mutex.Unlock()
		return domain.HandoffResult{Status: "completed", Summary: step.ID + " from round 2"}, nil
	}
	round := Round{Prior: prior, Restart: RestartSet(spec, []string{"api"}, map[string]bool{"plan": true, "api": true, "docs": true, "verify": true, "approval": true})}
	results, err := ExecuteRound(context.Background(), spec, map[string]bool{"approval": true}, round, executor)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []string{"api", "verify", "approval"}) {
		t.Fatalf("ran %v", ran)
	}
	if results["docs"].Summary != "docs from round 1" || results["api"].Summary != "api from round 2" || len(results) != 5 {
		t.Fatalf("results %v", results)
	}
	delete(prior, "docs")
	if _, err := ExecuteRound(context.Background(), spec, map[string]bool{"approval": true}, round, executor); err == nil || !strings.Contains(err.Error(), "step docs has no result to reuse") {
		t.Fatalf("a step outside the restart set needs a prior result: %v", err)
	}
}

func TestAnUnapprovedGateHoldsOnlyTheStepsAfterIt(t *testing.T) {
	// a -> gate -> after, with b beside them; and a second gate after b.
	spec := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeCodex, MaxConcurrency: 2, Steps: []domain.WorkflowStep{
		{ID: "a", Title: "A", Kind: domain.StepScript, Permission: domain.PermissionReadOnly},
		{ID: "b", Title: "B", Kind: domain.StepScript, Permission: domain.PermissionReadOnly},
		{ID: "slow", Title: "Slow", Kind: domain.StepScript, Permission: domain.PermissionReadOnly, DependsOn: []string{"b"}},
		{ID: "gate", Title: "Gate", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, Review: true, DependsOn: []string{"a"}},
		{ID: "after", Title: "After", Kind: domain.StepScript, Permission: domain.PermissionReadOnly, DependsOn: []string{"gate"}},
		{ID: "second", Title: "Second", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, Review: true, DependsOn: []string{"slow"}},
		{ID: "approval", Title: "Approval", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, DependsOn: []string{"after", "second"}},
	}}
	var mutex sync.Mutex
	var ran []string
	executor := func(_ context.Context, step domain.WorkflowStep) (domain.HandoffResult, error) {
		mutex.Lock()
		ran = append(ran, step.ID)
		mutex.Unlock()
		return domain.HandoffResult{Status: "completed", Summary: step.Title}, nil
	}
	results, err := Execute(context.Background(), spec, map[string]bool{"approval": true}, executor)
	var paused *PausedError
	if !errors.As(err, &paused) || !errors.Is(err, ErrApprovalRequired) || !slices.Equal(paused.Gates, []string{"gate", "second"}) {
		t.Fatalf("the round pauses at both gates once nothing else can run: %v", err)
	}
	slices.Sort(ran)
	if !slices.Equal(ran, []string{"a", "b", "slow"}) || len(results) != 3 {
		t.Fatalf("work beside the first gate ran before the pause: ran %v", ran)
	}

	// Approved, both gates let the rest run.
	ran = nil
	if _, err := Execute(context.Background(), spec, map[string]bool{"gate": true, "second": true, "approval": true}, executor); err != nil {
		t.Fatal(err)
	}
	if len(ran) != len(spec.Steps) {
		t.Fatalf("ran %v", ran)
	}
}

func TestAFailureBesideAHeldGateFailsTheRound(t *testing.T) {
	spec := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeCodex, MaxConcurrency: 2, Steps: []domain.WorkflowStep{
		{ID: "a", Title: "A", Kind: domain.StepScript, Permission: domain.PermissionReadOnly},
		{ID: "b", Title: "B", Kind: domain.StepScript, Permission: domain.PermissionReadOnly},
		{ID: "gate", Title: "Gate", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, Review: true, DependsOn: []string{"a"}},
		{ID: "approval", Title: "Approval", Kind: domain.StepApproval, Permission: domain.PermissionReadOnly, DependsOn: []string{"gate", "b"}},
	}}
	executor := func(_ context.Context, step domain.WorkflowStep) (domain.HandoffResult, error) {
		if step.ID == "b" {
			return domain.HandoffResult{}, errors.New("b broke")
		}
		return domain.HandoffResult{Status: "completed"}, nil
	}
	_, err := Execute(context.Background(), spec, map[string]bool{"approval": true}, executor)
	var paused *PausedError
	if err == nil || errors.As(err, &paused) || !strings.Contains(err.Error(), "b broke") {
		t.Fatalf("a failure is reported, not a pause: %v", err)
	}
}

func TestAFailedRoundReportsEveryStepThatFailedBesideTheFirst(t *testing.T) {
	spec := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeCodex, MaxConcurrency: 2, Steps: []domain.WorkflowStep{
		{ID: "lint", Title: "Lint", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly},
		{ID: "test", Title: "Test", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly},
		{ID: "later", Title: "Later", Kind: domain.StepScript, Permission: domain.PermissionReadOnly, DependsOn: []string{"lint", "test"}},
	}}
	broken := errors.New("exit status 1")
	executor := func(_ context.Context, step domain.WorkflowStep) (domain.HandoffResult, error) {
		if step.ID == "later" {
			t.Error("a step after a failure never starts")
		}
		// test fails after lint, so the order they failed in is kept.
		if step.ID == "test" {
			time.Sleep(20 * time.Millisecond)
		}
		return domain.HandoffResult{}, broken
	}
	_, err := Execute(context.Background(), spec, nil, executor)
	var failed *FailedError
	if !errors.As(err, &failed) || !slices.Equal(failed.Steps, []string{"lint", "test"}) {
		t.Fatalf("both failed steps are reported in order: %v", err)
	}
	if !errors.Is(err, broken) || err.Error() != "step lint: exit status 1; step test: exit status 1" {
		t.Fatalf("each failure is wrapped with its step: %v", err)
	}
}
