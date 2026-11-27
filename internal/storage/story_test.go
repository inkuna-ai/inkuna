package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestARunKeepsItsStoryAcrossSaves(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	if story, err := store.ExecutionStory(ctx, "run-1"); err != nil || story != nil {
		t.Fatalf("a run without a story = %q, %v", story, err)
	}
	if err := store.SaveExecutionStory(ctx, "run-1", []byte(`{"id":"12"}`)); err != nil {
		t.Fatal(err)
	}
	// Saving the run again, as every state change does, keeps it.
	if err := store.SaveExecution(ctx, Execution{ID: "run-1", WorkflowID: "workflow-1", TaskID: "task-1", ProjectID: "project", State: "running", Title: "Run", Round: 2}); err != nil {
		t.Fatal(err)
	}
	if story, err := store.ExecutionStory(ctx, "run-1"); err != nil || string(story) != `{"id":"12"}` {
		t.Fatalf("story = %q, %v", story, err)
	}
	if err := store.SaveExecutionStory(ctx, "run-missing", []byte(`{}`)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a story for no run = %v", err)
	}
	if _, err := store.ExecutionStory(ctx, "run-missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the story of no run = %v", err)
	}
}
