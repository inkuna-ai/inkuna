package storage

import (
	"context"
	"testing"
	"time"
)

func TestAPullRequestLinkIsReplacedKeptAndForgottenWithItsBranch(t *testing.T) {
	store := historyStore(t)
	ctx := context.Background()
	fillTask(t, store, "task-a", []string{"task-a-run"}, digestOf("shared"))
	fillTask(t, store, "task-b", []string{"task-b-run"}, digestOf("shared"))
	if branches, err := store.RunBranches(ctx, "project"); err != nil || len(branches) != 2 || branches[0] != "feature/task-a-run" || branches[1] != "feature/task-b-run" {
		t.Fatalf("branches = %v, %v", branches, err)
	}
	open := PullRequestLink{Branch: "feature/task-a-run", Number: 12, Title: "Fix login", URL: "https://github.com/acme/app/pull/12", State: "OPEN"}
	closed := PullRequestLink{Branch: "feature/task-b-run", Number: 13, Title: "Fix logout", URL: "https://github.com/acme/app/pull/13", State: "CLOSED"}
	if err := store.SavePullRequests(ctx, "project", []PullRequestLink{open, closed}); err != nil {
		t.Fatal(err)
	}

	// A later check replaces a branch's link and keeps one it found nothing
	// for.
	mergedAt := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	merged := open
	merged.State, merged.MergedAt = "MERGED", mergedAt
	if err := store.SavePullRequests(ctx, "project", []PullRequestLink{merged}); err != nil {
		t.Fatal(err)
	}
	links, err := store.PullRequests(ctx)
	if err != nil || len(links) != 2 {
		t.Fatalf("links = %+v, %v", links, err)
	}
	if got := links[0]; got.ProjectID != "project" || got.Number != 12 || !got.Merged() || !got.MergedAt.Equal(mergedAt) || got.CheckedAt.IsZero() {
		t.Fatalf("the merged link = %+v", got)
	}
	if got := links[1]; got.Number != 13 || got.State != "CLOSED" || !got.MergedAt.IsZero() {
		t.Fatalf("the link the check found nothing for = %+v", got)
	}

	// Once no run is on a branch, the next check forgets its link.
	if _, err := store.DeleteTasks(ctx, []string{"task-b"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePullRequests(ctx, "project", nil); err != nil {
		t.Fatal(err)
	}
	if links, err := store.PullRequests(ctx); err != nil || len(links) != 1 || links[0].Number != 12 {
		t.Fatalf("links = %+v, %v", links, err)
	}
	if err := store.SavePullRequests(ctx, "project", []PullRequestLink{{Branch: "feature/task-a-run"}}); err == nil {
		t.Fatal("a link without a number is refused")
	}
}
