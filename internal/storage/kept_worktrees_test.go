package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestAKeptWorktreeIsRecordedListedAndForgotten(t *testing.T) {
	store := historyStore(t)
	ctx := context.Background()
	older := KeptWorktree{Path: "/src/worktrees/feature/1-a", ProjectID: "project", Branch: "feature/1-a", TaskID: "task-a", RunID: "run-a", Title: "A", Reason: "Not asked to remove it.", KeptAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	newer := KeptWorktree{Path: "/src/worktrees/feature/2-b", ProjectID: "project", TaskID: "task-b", RunID: "run-b", Title: "B", Reason: "It has uncommitted changes, so it was kept.", KeptAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	for _, kept := range []KeptWorktree{older, newer} {
		if err := store.KeepWorktree(ctx, kept); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.KeepWorktree(ctx, KeptWorktree{ProjectID: "project"}); err == nil {
		t.Fatal("a record without a path is refused")
	}
	listed, err := store.KeptWorktrees(ctx)
	if err != nil || len(listed) != 2 || listed[0] != newer || listed[1] != older {
		t.Fatalf("kept = %+v, %v; want the newer first", listed, err)
	}

	// Keeping the same path again replaces its record.
	again := older
	again.TaskID, again.RunID, again.KeptAt = "task-c", "run-c", time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	if err := store.KeepWorktree(ctx, again); err != nil {
		t.Fatal(err)
	}
	if found, err := store.KeptWorktreeAt(ctx, older.Path); err != nil || found != again {
		t.Fatalf("found = %+v, %v", found, err)
	}

	if err := store.ForgetKeptWorktree(ctx, older.Path); err != nil {
		t.Fatal(err)
	}
	if err := store.ForgetKeptWorktree(ctx, older.Path); err != nil {
		t.Fatalf("forgetting twice is not an error: %v", err)
	}
	if _, err := store.KeptWorktreeAt(ctx, older.Path); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a forgotten worktree = %v, want not found", err)
	}
	if listed, err := store.KeptWorktrees(ctx); err != nil || len(listed) != 1 || listed[0].Path != newer.Path {
		t.Fatalf("kept = %+v, %v", listed, err)
	}
}
