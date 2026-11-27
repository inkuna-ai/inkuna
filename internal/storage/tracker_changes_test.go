package storage

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

func trackerChange(id, step string, round, position int, state string) TrackerChange {
	return TrackerChange{
		ExecutionID: "run-1", ID: id, Round: round, StepID: step, StepRunID: "run-1-" + step, Position: position,
		Kind: domain.TrackerComment, Issue: "12", Change: json.RawMessage(`{"change":{"kind":"comment"}}`), State: state,
	}
}

func changeStates(t *testing.T, store *Store) map[string]string {
	t.Helper()
	changes, err := store.TrackerChanges(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	states := make(map[string]string, len(changes))
	for _, change := range changes {
		states[change.ID] = change.State
	}
	return states
}

func TestARunKeepsItsTrackerChangesInTheOrderTheyWereAsked(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	if err := store.AddTrackerChanges(ctx, []TrackerChange{
		trackerChange("c2", "implement", 1, 1, TrackerChangeRefused), trackerChange("c1", "implement", 1, 0, TrackerChangePending),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTrackerChanges(ctx, []TrackerChange{trackerChange("c3", "implement", 2, 0, TrackerChangePending)}); err != nil {
		t.Fatal(err)
	}
	changes, err := store.TrackerChanges(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(changes))
	for _, change := range changes {
		ids = append(ids, change.ID)
	}
	if !reflect.DeepEqual(ids, []string{"c1", "c2", "c3"}) {
		t.Fatalf("order = %v", ids)
	}
	if string(changes[0].Change) != `{"change":{"kind":"comment"}}` || changes[0].Kind != domain.TrackerComment || changes[0].StepRunID != "run-1-implement" {
		t.Fatalf("change = %+v", changes[0])
	}
	if _, err := store.TrackerChange(ctx, "run-1", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a missing change = %v", err)
	}
}

func TestATrackerChangeMovesOnlyFromTheStateItIsIn(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	if err := store.AddTrackerChanges(ctx, []TrackerChange{
		trackerChange("pending", "implement", 1, 0, TrackerChangePending), trackerChange("refused", "implement", 1, 1, TrackerChangeRefused),
		trackerChange("other", "review", 1, 0, TrackerChangePending),
	}); err != nil {
		t.Fatal(err)
	}
	// Approving claims the pending change once; a second claim gets nothing.
	moved, err := store.MoveTrackerChanges(ctx, "run-1", []string{"pending", "refused"}, TrackerChangePending, TrackerChangeApplying, "")
	if err != nil || !reflect.DeepEqual(moved, []string{"pending"}) {
		t.Fatalf("moved = %v, %v", moved, err)
	}
	if moved, err := store.MoveTrackerChanges(ctx, "run-1", []string{"pending"}, TrackerChangePending, TrackerChangeApplying, ""); err != nil || len(moved) != 0 {
		t.Fatalf("a second claim = %v, %v", moved, err)
	}
	// Finishing takes only a change being applied.
	if err := store.FinishTrackerChange(ctx, "run-1", "pending", TrackerChangeApplied, "", "https://tracker.test/c/1"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishTrackerChange(ctx, "run-1", "refused", TrackerChangeApplied, "", ""); err != nil {
		t.Fatal(err)
	}
	applied, err := store.TrackerChange(ctx, "run-1", "pending")
	if err != nil || applied.State != TrackerChangeApplied || applied.URL != "https://tracker.test/c/1" {
		t.Fatalf("applied = %+v, %v", applied, err)
	}
	// Discarding takes only the named steps' pending changes.
	if err := store.AddTrackerChanges(ctx, []TrackerChange{trackerChange("again", "implement", 1, 2, TrackerChangePending)}); err != nil {
		t.Fatal(err)
	}
	if discarded, err := store.DiscardTrackerChanges(ctx, "run-1", []string{"implement"}, "it runs again"); err != nil || discarded != 1 {
		t.Fatalf("discarded = %d, %v", discarded, err)
	}
	want := map[string]string{"pending": TrackerChangeApplied, "refused": TrackerChangeRefused, "other": TrackerChangePending, "again": TrackerChangeDiscarded}
	if got := changeStates(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v", got)
	}
}

func TestAChangeBeingAppliedWhenInkunaStoppedCanBeRetried(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	if err := store.AddTrackerChanges(ctx, []TrackerChange{
		trackerChange("applying", "implement", 1, 0, TrackerChangeApplying), trackerChange("pending", "implement", 1, 1, TrackerChangePending),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInterruptedExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	stopped, err := store.TrackerChange(ctx, "run-1", "applying")
	if err != nil || stopped.State != TrackerChangeFailed || stopped.Reason == "" {
		t.Fatalf("stopped = %+v, %v", stopped, err)
	}
	if got := changeStates(t, store)["pending"]; got != TrackerChangePending {
		t.Fatalf("a pending change waits for its review across a restart: %s", got)
	}
}

func TestANoteJoinsAFinishedThread(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	asked := time.Now().UTC().Add(-time.Hour)
	task := a2a.Task{ID: "a2a-1", ContextID: "task-1", Agent: "implementer", Status: a2a.TaskStatus{State: a2a.StateCompleted, Timestamp: asked},
		History: []a2a.Message{{MessageID: "m1", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.TextPart("Do it")}, Timestamp: asked}}}
	if err := store.SaveA2ATask(ctx, task); err != nil {
		t.Fatal(err)
	}
	note := a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.TextPart("A tracker change failed")}}
	if err := store.AddA2AMessage(ctx, "a2a-1", note); err != nil {
		t.Fatal(err)
	}
	messages, err := store.A2AMessages(ctx, "a2a-1")
	if err != nil || len(messages) != 2 || messages[1].Text() != "A tracker change failed" || messages[1].TaskID != "a2a-1" || messages[1].ContextID != "task-1" {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
	threads, err := store.A2AThreads(ctx, 0)
	if err != nil || len(threads) != 1 || threads[0].MessageCount != 2 || threads[0].LastMessage.Text() != "A tracker change failed" || !threads[0].UpdatedAt.After(asked) {
		t.Fatalf("threads = %+v, %v", threads, err)
	}
	if stored, err := store.A2ATask(ctx, "a2a-1"); err != nil || stored.Status.State != a2a.StateCompleted {
		t.Fatalf("the task's state changed: %+v, %v", stored.Status, err)
	}
	if err := store.AddA2AMessage(ctx, "a2a-missing", note); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a note to no thread = %v", err)
	}
}
