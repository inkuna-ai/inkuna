package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestStaffingDecisionsAreKeptByKeyAndReplaced(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if _, _, found, err := store.StaffingDecision(ctx, "key"); err != nil || found {
		t.Fatalf("nothing is kept yet: found=%v %v", found, err)
	}
	first := time.Date(2026, 10, 18, 14, 2, 0, 0, time.UTC)
	if err := store.SaveStaffingDecision(ctx, "key", json.RawMessage(`{"assignments":[]}`), first); err != nil {
		t.Fatal(err)
	}
	decision, decidedAt, found, err := store.StaffingDecision(ctx, "key")
	if err != nil || !found || string(decision) != `{"assignments":[]}` || !decidedAt.Equal(first) {
		t.Fatalf("kept decision = %s at %v, found=%v, %v", decision, decidedAt, found, err)
	}
	// Re-staffing replaces the decision under the same key.
	later := first.Add(time.Hour)
	if err := store.SaveStaffingDecision(ctx, "key", json.RawMessage(`{"assignments":[],"notes":"again"}`), later); err != nil {
		t.Fatal(err)
	}
	if decision, decidedAt, _, _ := store.StaffingDecision(ctx, "key"); string(decision) != `{"assignments":[],"notes":"again"}` || !decidedAt.Equal(later) {
		t.Fatalf("replaced decision = %s at %v", decision, decidedAt)
	}
	if err := store.SaveStaffingDecision(ctx, "", json.RawMessage(`{}`), later); err == nil {
		t.Fatal("a decision without a key must be refused")
	}
	if err := store.SaveStaffingDecision(ctx, "other", json.RawMessage(`{`), later); err == nil {
		t.Fatal("a decision that is not JSON must be refused")
	}
}
