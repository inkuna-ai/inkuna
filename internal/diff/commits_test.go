package diff

import (
	"context"
	"strings"
	"testing"
)

func TestCommitsListTheBranchOldestFirst(t *testing.T) {
	repo := newBranchRepo(t)
	ctx := context.Background()
	repo.git("switch", "-q", "feature")
	byHand := repo.commit("by hand\n\nMade in the worktree,\nnot by a step.", map[string]string{"h.txt": "h\n"})
	commits, err := Commits(ctx, repo.path, repo.run())
	if err != nil {
		t.Fatal(err)
	}
	var hashes, subjects []string
	for _, commit := range commits {
		hashes = append(hashes, commit.Hash)
		subjects = append(subjects, commit.Subject)
	}
	if strings.Join(hashes, " ") != strings.Join([]string{repo.first, repo.second, byHand}, " ") {
		t.Fatalf("commits %v, subjects %v", hashes, subjects)
	}
	last := commits[2]
	if last.Subject != "by hand" || last.Body != "Made in the worktree,\nnot by a step." || last.Author != "test <test@example.com>" || !strings.HasPrefix(last.Hash, last.ShortHash) || last.Date == "" {
		t.Fatalf("last commit %+v", last)
	}
}

func TestOnceTheBranchIsGoneTheRunsOwnCommitsStandIn(t *testing.T) {
	repo := newBranchRepo(t)
	repo.git("branch", "-D", "feature")
	commits, err := Commits(context.Background(), repo.path, repo.run())
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].Hash != repo.first || commits[1].Hash != repo.second {
		t.Fatalf("commits %+v", commits)
	}
}

func TestCommitsCountWhatEachChangedAsItsCommitScopeShows(t *testing.T) {
	repo := newBranchRepo(t)
	ctx := context.Background()
	repo.git("switch", "-q", "feature")
	edit := repo.commit("edit", map[string]string{"a.txt": "a\nB\n", "c.txt": ""})
	repo.git("merge", "-q", "--no-ff", "-m", "merge main", "main")
	merge := repo.git("rev-parse", "HEAD")
	commits, err := Commits(ctx, repo.path, repo.run())
	if err != nil {
		t.Fatal(err)
	}
	byHash := make(map[string]Commit, len(commits))
	for _, commit := range commits {
		byHash[commit.Hash] = commit
	}
	for hash, want := range map[string][3]int{repo.first: {1, 1, 0}, edit: {2, 1, 2}, merge: {1, 1, 0}} {
		got := byHash[hash]
		if [3]int{got.Files, got.Additions, got.Deletions} != want {
			t.Errorf("%s: files, additions, deletions %d %d %d, want %v", got.Subject, got.Files, got.Additions, got.Deletions, want)
		}
	}
	if !byHash[merge].Merge || byHash[edit].Merge {
		t.Fatalf("only the merge is a merge: %+v %+v", byHash[merge], byHash[edit])
	}
	// A merge is measured against its first parent, as its commit scope is.
	between, err := Resolve(ctx, repo.path, repo.run(), Scope{Kind: ScopeCommit, Commit: merge})
	if err != nil {
		t.Fatal(err)
	}
	change, err := Changes(ctx, repo.path, between, Review{})
	if err != nil {
		t.Fatal(err)
	}
	if got := byHash[merge]; len(change.Files) != got.Files || change.Additions != got.Additions || change.Deletions != got.Deletions {
		t.Fatalf("merge counts %+v, its commit scope %+v", got, change)
	}
}
