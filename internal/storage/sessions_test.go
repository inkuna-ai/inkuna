package storage

import (
	"context"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestALaterRoundFindsTheSessionItsStepLastLeft(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	start := func(id, step string, round int) {
		t.Helper()
		if err := store.StartRun(ctx, id, "run-1", spec.ID, step, round, domain.RuntimeClaude, "/worktree"); err != nil {
			t.Fatal(err)
		}
	}
	if _, found, err := store.LastSession(ctx, "run-1", "implement", 2); err != nil || found {
		t.Fatalf("a step that never ran left no session: %v %v", found, err)
	}
	start("run-1-implement", "implement", 1)
	if _, found, err := store.LastSession(ctx, "run-1", "implement", 2); err != nil || found {
		t.Fatalf("a run that kept no session leaves none: %v %v", found, err)
	}
	cached := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	usage := domain.Usage{InputTokens: 40, CacheReadTokens: 900, OutputTokens: 30, Turns: 3}
	if err := store.SaveSession(ctx, "run-1-implement", StepSession{Session: "session-1", Runtime: domain.RuntimeClaude, CacheUntil: cached, Usage: usage}); err != nil {
		t.Fatal(err)
	}
	// A peer's row shares its step's id but is never the step's session.
	start("run-1-implement-peer1-reviewer", "implement", 1)
	if err := store.SaveSession(ctx, "run-1-implement-peer1-reviewer", StepSession{Session: "peer", Runtime: domain.RuntimeClaude}); err != nil {
		t.Fatal(err)
	}
	session, found, err := store.LastSession(ctx, "run-1", "implement", 2)
	if err != nil || !found {
		t.Fatalf("round 2 finds round 1's session: %v %v", found, err)
	}
	want := StepSession{Session: "session-1", Runtime: domain.RuntimeClaude, WorkingDir: "/worktree", CacheUntil: cached, Usage: usage, Round: 1}
	if session != want {
		t.Fatalf("session %+v, want %+v", session, want)
	}
	// A round finds the latest earlier round's session, never its own.
	start("run-1.r2.implement", "implement", 2)
	if err := store.SaveSession(ctx, "run-1.r2.implement", StepSession{Session: "session-2", Runtime: domain.RuntimeCodex}); err != nil {
		t.Fatal(err)
	}
	if session, _, _ := store.LastSession(ctx, "run-1", "implement", 2); session.Session != "session-1" {
		t.Fatalf("round 2 reads round 1's session, not its own: %+v", session)
	}
	if session, _, _ := store.LastSession(ctx, "run-1", "implement", 3); session.Session != "session-2" || !session.CacheUntil.IsZero() || session.Round != 2 {
		t.Fatalf("round 3 reads round 2's session, which never expires: %+v", session)
	}
}

func TestStepRoundsListEachRoundsOwnEndedRun(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	finish := func(id string, round int, state, summary string) {
		t.Helper()
		if err := store.StartRun(ctx, id, "run-1", spec.ID, "implement", round, domain.RuntimeClaude, ""); err != nil {
			t.Fatal(err)
		}
		if state != "" {
			if err := store.FinishRun(ctx, id, state, domain.HandoffResult{Version: 1, Status: state, Summary: summary}); err != nil {
				t.Fatal(err)
			}
		}
	}
	finish("run-1-implement", 1, "completed", "round 1")
	finish("run-1-implement-peer1-reviewer", 1, "completed", "a peer's reply")
	finish("run-1.r2.implement", 2, "failed", "round 2 failed")
	finish("run-1.r3.implement", 3, "", "")
	rounds, err := store.StepRounds(ctx, "run-1", "implement")
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 2 || rounds[0].Round != 1 || rounds[0].Handoff.Summary != "round 1" || rounds[1].State != "failed" || rounds[1].Round != 2 {
		t.Fatalf("rounds %+v", rounds)
	}
}

func TestSettlingARoundRecordsRepliesAndMarksTheRestNoReply(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	items := []FeedbackItem{
		{ID: "f1", Target: "implement", From: FeedbackFromHuman, Text: "Handle an empty name."},
		{ID: "f2", Target: "implement", From: FeedbackFromHuman, Text: "Cache the lookup."},
		{ID: "f3", Target: "implement", From: FeedbackFromHuman, Text: "Rename the flag."},
		{ID: "f4", Target: "docs", From: FeedbackFromHuman, Text: "Say so in the guide."},
	}
	if started, err := store.StartRound(ctx, "run-1", 1, items); err != nil || !started {
		t.Fatalf("start round 2: %v %v", started, err)
	}
	settled, err := store.SettleFeedback(ctx, "run-1", 2, "implement", []domain.Addressed{
		{Item: "f1", Status: domain.AddressedFixed, Note: "Empty names now return an error."},
		{Item: "f2", Status: domain.AddressedDeclined, Note: "The cache is per request."},
		// Another target's item is not this target's to answer.
		{Item: "f4", Status: domain.AddressedFixed, Note: "Not mine."},
	})
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, item := range settled {
		statuses[item.ID] = item.Status + ":" + item.Note
	}
	want := map[string]string{"f1": "fixed:Empty names now return an error.", "f2": "declined:The cache is per request.", "f3": "no-reply:"}
	if len(statuses) != len(want) {
		t.Fatalf("settled %+v", settled)
	}
	for id, status := range want {
		if statuses[id] != status {
			t.Errorf("%s is %q, want %q", id, statuses[id], status)
		}
	}
	all, err := store.TargetFeedback(ctx, "run-1", "docs")
	if err != nil || len(all) != 1 || all[0].Status != FeedbackOpen {
		t.Fatalf("another target's items stay open: %+v %v", all, err)
	}
}
