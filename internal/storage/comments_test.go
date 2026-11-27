package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestADraftIsSentWithItsRoundAndItsReplySettlesItsThread(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	start := func(id, comment string, line int) {
		t.Helper()
		thread := CommentThread{ExecutionID: "run-1", ID: id, Path: "name.go", Side: domain.SideNew, Line: line, EndLine: line, Base: "base", Commit: "tip1", Round: 1}
		if err := store.StartThread(ctx, thread, Comment{ID: comment, Body: "Fix line " + id, Target: "implement"}); err != nil {
			t.Fatal(err)
		}
	}
	start("thread-1", "comment-1", 3)
	start("thread-2", "comment-2", 7)
	if err := store.AddDraft(ctx, "run-1", Comment{ID: "comment-3", ThreadID: "thread-1", Body: "Another", Target: "implement"}); !errors.Is(err, ErrDraftExists) {
		t.Fatalf("a second draft in a thread: %v", err)
	}
	items := []FeedbackItem{
		{ID: "c1", Target: "implement", From: FeedbackFromHuman, Text: "Fix line thread-1", CommentID: "comment-1", Path: "name.go", Side: domain.SideNew, Line: 4, EndLine: 4, Commit: "tip2", Excerpt: "> 4 + x"},
		{ID: "c2", Target: "implement", From: FeedbackFromHuman, Text: "Fix line thread-2", CommentID: "comment-2", Path: "name.go", Side: domain.SideNew, Line: 8, EndLine: 8, Commit: "tip2", Excerpt: "> 8 + y"},
	}
	if started, err := store.StartRound(ctx, "run-1", 1, items); err != nil || !started {
		t.Fatalf("start round 2: %v %v", started, err)
	}
	threads, err := store.CommentThreads(ctx, "run-1")
	if err != nil || len(threads) != 2 {
		t.Fatalf("threads %+v %v", threads, err)
	}
	// Sending moved each thread to where its item put it.
	sent := threads[0]
	if sent.Line != 4 || sent.Commit != "tip2" || sent.Comments[0].State != CommentSent || sent.Comments[0].Round != 2 || sent.Comments[0].Item != "c1" ||
		sent.Comments[0].Status != FeedbackOpen || sent.Comments[0].Excerpt != "> 4 + x" {
		t.Fatalf("the sent thread %+v", sent)
	}
	if _, ok := sent.Draft(); ok {
		t.Fatal("a sent comment is still a draft")
	}
	feedback, err := store.Feedback(ctx, "run-1", 2)
	if err != nil || feedback[1].CommentID != "comment-2" || feedback[1].Path != "name.go" || feedback[1].Line != 8 || feedback[1].Commit != "tip2" || feedback[1].Excerpt != "> 8 + y" {
		t.Fatalf("the items keep where the comments were %+v %v", feedback, err)
	}
	// A draft is sent once: the same round cannot start twice, and a draft
	// already sent cannot go again in another.
	if _, err := store.DeleteDraft(ctx, "run-1", "comment-1"); err != nil {
		t.Fatal(err)
	}
	if deleted, _ := store.DeleteDraft(ctx, "run-1", "comment-1"); deleted {
		t.Fatal("a sent comment was deleted")
	}

	settled, err := store.SettleFeedback(ctx, "run-1", 2, "implement", []domain.Addressed{{Item: "c1", Status: domain.AddressedFixed, Note: "Done."}, {Item: "c2", Status: domain.AddressedDeclined, Note: "No."}})
	if err != nil || len(settled) != 2 {
		t.Fatalf("settle %+v %v", settled, err)
	}
	threads, _ = store.CommentThreads(ctx, "run-1")
	if threads[0].State != ThreadResolved || threads[0].ResolvedBy != ResolvedByAgent || threads[0].Comments[0].Note != "Done." {
		t.Fatalf("a fixed comment resolves its thread %+v", threads[0])
	}
	if threads[1].State != ThreadOpen || threads[1].Comments[0].Status != domain.AddressedDeclined || threads[1].Comments[0].Note != "No." {
		t.Fatalf("a declined comment keeps its thread open %+v", threads[1])
	}

	// The person reopens a thread and replies; sending the reply leaves
	// the thread open again even if it was resolved.
	if changed, err := store.ResolveThread(ctx, "run-1", "thread-2", true); err != nil || !changed {
		t.Fatalf("resolve %v %v", changed, err)
	}
	if err := store.AddDraft(ctx, "run-1", Comment{ID: "comment-4", ThreadID: "thread-2", Body: "Please.", Target: "implement"}); err != nil {
		t.Fatal(err)
	}
	if edited, err := store.EditDraft(ctx, "run-1", "comment-4", "Please do.", "implement"); err != nil || !edited {
		t.Fatalf("edit %v %v", edited, err)
	}
	reply := []FeedbackItem{{ID: "c1", Target: "implement", From: FeedbackFromHuman, Text: "Please do.", CommentID: "comment-4", Path: "name.go", Side: domain.SideNew, Line: 9, EndLine: 9, Commit: "tip3"}}
	if started, err := store.StartRound(ctx, "run-1", 2, nil); err != nil || started {
		t.Fatalf("round 2 is not ended, so it cannot start round 3 yet: %v %v", started, err)
	}
	execution, _ := store.Execution(ctx, "run-1")
	execution.State = "completed"
	if err := store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if started, err := store.StartRound(ctx, "run-1", 2, reply); err != nil || !started {
		t.Fatalf("start round 3: %v %v", started, err)
	}
	threads, _ = store.CommentThreads(ctx, "run-1")
	if threads[1].State != ThreadOpen || threads[1].Line != 9 || len(threads[1].Comments) != 2 || threads[1].Comments[1].Round != 3 || threads[1].Comments[0].Round != 2 {
		t.Fatalf("the replied thread %+v", threads[1])
	}
	if edited, _ := store.EditDraft(ctx, "run-1", "comment-4", "Too late.", "implement"); edited {
		t.Fatal("a sent comment was edited")
	}
}

func TestARoundFailsToStartWhenADraftItSendsIsGone(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	if err := store.StartThread(ctx, CommentThread{ExecutionID: "run-1", ID: "thread-1", Path: "a.go", Side: domain.SideNew, Line: 1, EndLine: 1, Commit: "tip", Round: 1}, Comment{ID: "comment-1", Body: "x", Target: "implement"}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteDraft(ctx, "run-1", "comment-1"); err != nil || !deleted {
		t.Fatalf("delete %v %v", deleted, err)
	}
	if threads, _ := store.CommentThreads(ctx, "run-1"); len(threads) != 0 {
		t.Fatalf("a thread without comments is left: %+v", threads)
	}
	items := []FeedbackItem{{ID: "c1", Target: "implement", From: FeedbackFromHuman, Text: "x", CommentID: "comment-1"}}
	if started, err := store.StartRound(ctx, "run-1", 1, items); err == nil || started || !strings.Contains(err.Error(), "deleted or sent") {
		t.Fatalf("a round sent a deleted draft: %v %v", started, err)
	}
	if execution, _ := store.Execution(ctx, "run-1"); execution.Round != 1 {
		t.Fatalf("the failed start moved the run to round %d", execution.Round)
	}
}

func TestAThreadIsFollowedOnlyFromWhereItWas(t *testing.T) {
	store, _ := roundsStore(t)
	ctx := context.Background()
	if err := store.StartThread(ctx, CommentThread{ExecutionID: "run-1", ID: "thread-1", Path: "a.go", Side: domain.SideNew, Line: 1, EndLine: 2, Commit: "tip1", Round: 1}, Comment{ID: "comment-1", Body: "x", Target: "implement"}); err != nil {
		t.Fatal(err)
	}
	if moved, err := store.MoveThread(ctx, "run-1", "thread-1", "elsewhere", 5, 6, "tip2"); err != nil || moved {
		t.Fatalf("moved from where it was not: %v %v", moved, err)
	}
	if moved, err := store.MoveThread(ctx, "run-1", "thread-1", "tip1", 5, 6, "tip2"); err != nil || !moved {
		t.Fatalf("move %v %v", moved, err)
	}
	if outdated, err := store.OutdateThread(ctx, "run-1", "thread-1", "tip1", 2); err != nil || outdated {
		t.Fatalf("outdated from where it was not: %v %v", outdated, err)
	}
	if outdated, err := store.OutdateThread(ctx, "run-1", "thread-1", "tip2", 2); err != nil || !outdated {
		t.Fatalf("outdate %v %v", outdated, err)
	}
	thread, err := store.CommentThread(ctx, "run-1", "thread-1")
	if err != nil || !thread.Outdated() || thread.OutdatedRound != 2 || thread.Line != 5 || thread.Commit != "tip2" {
		t.Fatalf("the thread %+v %v", thread, err)
	}
	if moved, _ := store.MoveThread(ctx, "run-1", "thread-1", "tip2", 7, 8, "tip3"); moved {
		t.Fatal("an outdated thread moved")
	}
	if _, err := store.CommentThread(ctx, "run-1", "thread-9"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a missing thread: %v", err)
	}
}
