package diff

import (
	"context"
	"strings"
	"testing"
)

// branchRepo has a base on main, two commits on feature, a later commit on
// main, and a commit on another branch.
type branchRepo struct {
	repo
	base, first, second, onMain, elsewhere string
}

func newBranchRepo(t *testing.T) branchRepo {
	result := branchRepo{repo: newRepo(t)}
	result.base = result.commit("base", map[string]string{"a.txt": "a\n"})
	result.git("switch", "-q", "-c", "feature")
	result.first = result.commit("first", map[string]string{"a.txt": "a\nb\n"})
	result.second = result.commit("second", map[string]string{"c.txt": "c\n"})
	result.git("switch", "-q", "main")
	result.onMain = result.commit("main moves", map[string]string{"m.txt": "m\n"})
	result.git("switch", "-q", "-c", "other", result.base)
	result.elsewhere = result.commit("other", map[string]string{"o.txt": "o\n"})
	return result
}

func (repo branchRepo) run() Run {
	return Run{Base: repo.base, Branch: "feature", Commits: []string{repo.first, repo.second}}
}

func TestTheBranchScopeEndsAtTheBranchTipAsItIsNow(t *testing.T) {
	repo := newBranchRepo(t)
	ctx := context.Background()
	got, err := Resolve(ctx, repo.path, repo.run(), Scope{Kind: ScopeBranch})
	if err != nil || got != (Range{From: repo.base, To: repo.second}) {
		t.Fatalf("branch scope %+v, %v", got, err)
	}
	repo.git("switch", "-q", "feature")
	third := repo.commit("third", map[string]string{"d.txt": "d\n"})
	if got, err := Resolve(ctx, repo.path, repo.run(), Scope{Kind: ScopeBranch}); err != nil || got.To != third {
		t.Fatalf("after the branch moved on: %+v, %v", got, err)
	}
}

func TestTheCommitScopeRefusesACommitThatIsNotOnTheBranch(t *testing.T) {
	repo := newBranchRepo(t)
	ctx := context.Background()
	got, err := Resolve(ctx, repo.path, repo.run(), Scope{Kind: ScopeCommit, Commit: repo.first[:10]})
	if err != nil || got != (Range{From: repo.base, To: repo.first}) {
		t.Fatalf("commit on the branch: %+v, %v", got, err)
	}
	for name, commit := range map[string]string{
		"a later commit on main":       repo.onMain,
		"a commit on another branch":   repo.elsewhere,
		"the base itself":              repo.base,
		"a ref rather than a hash":     "HEAD",
		"an option rather than a hash": "--output=x",
		"a hash no commit has":         "deadbeefdeadbeef",
	} {
		if got, err := Resolve(ctx, repo.path, repo.run(), Scope{Kind: ScopeCommit, Commit: commit}); err == nil {
			t.Errorf("%s: resolved to %+v", name, got)
		}
	}
}

func TestTheRangeScopeTakesTwoCommitsInOrderOnTheBranch(t *testing.T) {
	repo := newBranchRepo(t)
	ctx := context.Background()
	if got, err := Resolve(ctx, repo.path, repo.run(), Scope{Kind: ScopeRange, From: repo.first, To: repo.second}); err != nil || got != (Range{From: repo.first, To: repo.second}) {
		t.Fatalf("range: %+v, %v", got, err)
	}
	if _, err := Resolve(ctx, repo.path, repo.run(), Scope{Kind: ScopeRange, From: repo.base, To: repo.second}); err != nil {
		t.Fatalf("a range from the base: %v", err)
	}
	for name, scope := range map[string]Scope{
		"backwards":         {Kind: ScopeRange, From: repo.second, To: repo.first},
		"off the branch":    {Kind: ScopeRange, From: repo.first, To: repo.onMain},
		"from another line": {Kind: ScopeRange, From: repo.elsewhere, To: repo.second},
	} {
		if got, err := Resolve(ctx, repo.path, repo.run(), scope); err == nil {
			t.Errorf("%s: resolved to %+v", name, got)
		}
	}
}

func TestARebasedBranchsScopesAllMeasureFromWhereItLeavesItsBase(t *testing.T) {
	repo := newRepo(t)
	root := repo.commit("root", map[string]string{"a.txt": "a\n"})
	base := repo.commit("base", map[string]string{"b.txt": "b\n"})
	repo.git("switch", "-q", "-c", "feature")
	first := repo.commit("first", map[string]string{"c.txt": "c\n"})
	second := repo.commit("second", map[string]string{"d.txt": "d\n"})
	// The run's base is dropped: feature is replayed onto another line from
	// root, so it no longer contains base.
	repo.git("switch", "-q", "-c", "line", root)
	onto := repo.commit("another line", map[string]string{"l.txt": "l\n"})
	repo.git("rebase", "-q", "--onto", onto, base, "feature")
	tip := repo.git("rev-parse", "feature")
	rebasedFirst := repo.git("rev-parse", "feature~1")
	ctx := context.Background()
	run := Run{Base: base, Branch: "feature", Commits: []string{first, second}}

	branch, err := Resolve(ctx, repo.path, run, Scope{Kind: ScopeBranch})
	if err != nil || branch != (Range{From: root, To: tip}) {
		t.Fatalf("branch scope after the rebase: %+v, %v", branch, err)
	}
	// A view loads each file's patch with the branch scope's two commits as
	// a range, so that range must resolve to the same two.
	if got, err := Resolve(ctx, repo.path, run, Scope{Kind: ScopeRange, From: branch.From, To: branch.To}); err != nil || got != branch {
		t.Fatalf("range from the branch scope: %+v, %v", got, err)
	}
	if got, err := Resolve(ctx, repo.path, run, Scope{Kind: ScopeCommit, Commit: rebasedFirst}); err != nil || got != (Range{From: onto, To: rebasedFirst}) {
		t.Fatalf("a rebased commit: %+v, %v", got, err)
	}
	for name, scope := range map[string]Scope{
		"the commit it now leaves its base at": {Kind: ScopeCommit, Commit: root},
		"a commit the rebase left behind":      {Kind: ScopeCommit, Commit: first},
		"a range from the old base":            {Kind: ScopeRange, From: base, To: tip},
	} {
		if got, err := Resolve(ctx, repo.path, run, scope); err == nil {
			t.Errorf("%s: resolved to %+v", name, got)
		}
	}
}

func TestADeletedBranchStillOpensTheRunsOwnCommits(t *testing.T) {
	repo := newBranchRepo(t)
	repo.git("branch", "-q", "-D", "feature")
	ctx := context.Background()
	run := Run{Base: repo.base, Branch: "feature", Commits: []string{repo.first}}
	if _, err := Resolve(ctx, repo.path, run, Scope{Kind: ScopeBranch}); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("branch scope on a deleted branch: %v", err)
	}
	if got, err := Resolve(ctx, repo.path, run, Scope{Kind: ScopeCommit, Commit: repo.first}); err != nil || got.To != repo.first {
		t.Fatalf("the run's own commit: %+v, %v", got, err)
	}
	if _, err := Resolve(ctx, repo.path, run, Scope{Kind: ScopeCommit, Commit: repo.second}); err == nil {
		t.Fatal("a commit the run did not record must be refused once the branch is gone")
	}
}

func TestARunWithoutARecordedBaseStartsFromItsFirstCommitsParent(t *testing.T) {
	repo := newBranchRepo(t)
	run := repo.run()
	run.Base = ""
	if got, err := Resolve(context.Background(), repo.path, run, Scope{Kind: ScopeBranch}); err != nil || got.From != repo.base {
		t.Fatalf("fallback base: %+v, %v", got, err)
	}
	run.Commits = nil
	if _, err := Resolve(context.Background(), repo.path, run, Scope{Kind: ScopeBranch}); err == nil {
		t.Fatal("a run with neither a base nor commits has nothing to show")
	}
}

func TestParseScopeReadsWhatStringWrites(t *testing.T) {
	for _, scope := range []Scope{{Kind: ScopeBranch}, {Kind: ScopeCommit, Commit: "abc123"}, {Kind: ScopeRange, From: "abc1", To: "def2"}} {
		if got, err := ParseScope(scope.String()); err != nil || got != scope {
			t.Errorf("%q: %+v, %v", scope.String(), got, err)
		}
	}
	for _, text := range []string{"", "branch extra", "commit", "range abc", "range ..def", "tip"} {
		if got, err := ParseScope(text); err == nil {
			t.Errorf("%q: parsed as %+v", text, got)
		}
	}
}
