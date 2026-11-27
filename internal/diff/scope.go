package diff

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ScopeKind is what part of a run's changes a Scope covers.
type ScopeKind string

const (
	// ScopeBranch is everything on the run's branch: its base to the
	// branch's tip as it is now.
	ScopeBranch ScopeKind = "branch"
	// ScopeCommit is one commit on the branch.
	ScopeCommit ScopeKind = "commit"
	// ScopeRange is the changes between two commits on the branch, such as
	// those since a review round.
	ScopeRange ScopeKind = "range"
)

// Scope selects which of a run's changes to show.
type Scope struct {
	Kind ScopeKind `json:"kind"`
	// Commit is the commit of a commit scope.
	Commit string `json:"commit,omitempty"`
	// From and To are the ends of a range scope.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// ParseScope reads a scope as inkunactl takes it: "branch", "commit <sha>",
// or "range <from>..<to>".
func ParseScope(text string) (Scope, error) {
	fields := strings.Fields(text)
	switch {
	case len(fields) == 1 && fields[0] == string(ScopeBranch):
		return Scope{Kind: ScopeBranch}, nil
	case len(fields) == 2 && fields[0] == string(ScopeCommit):
		return Scope{Kind: ScopeCommit, Commit: fields[1]}, nil
	case len(fields) == 2 && fields[0] == string(ScopeRange):
		from, to, ok := strings.Cut(fields[1], "..")
		if ok && from != "" && to != "" {
			return Scope{Kind: ScopeRange, From: from, To: to}, nil
		}
	}
	return Scope{}, fmt.Errorf("scope %q is not branch, commit <sha>, or range <from>..<to>", text)
}

// String writes the scope the way ParseScope reads it.
func (scope Scope) String() string {
	switch scope.Kind {
	case ScopeCommit:
		return string(ScopeCommit) + " " + scope.Commit
	case ScopeRange:
		return string(ScopeRange) + " " + scope.From + ".." + scope.To
	default:
		return string(scope.Kind)
	}
}

// Run is what resolving a scope needs to know about a run.
type Run struct {
	// Base is the commit the run's workspace started from. Runs from before
	// it was recorded leave it empty, and the parent of their first commit
	// stands in.
	Base string
	// Branch is the branch the run committed to.
	Branch string
	// Commits are the run's commits, oldest first.
	Commits []string
}

// Range is the two commits a scope compares.
type Range struct {
	From string `json:"from"`
	To   string `json:"to"`
}

var hashPattern = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// Resolve turns a scope into the two commits it compares, refusing a commit
// that is not on the run's branch. Commits are named only by hash, so no
// argument from a view can be read as an option or a ref.
func Resolve(ctx context.Context, repository string, run Run, scope Scope) (Range, error) {
	base, err := baseOf(ctx, repository, run)
	if err != nil {
		return Range{}, err
	}
	tip, err := branchTip(ctx, repository, run.Branch)
	if err != nil {
		return Range{}, err
	}
	floor, err := floorOf(ctx, repository, run.Branch, base, tip)
	if err != nil {
		return Range{}, err
	}
	switch scope.Kind {
	case ScopeBranch:
		if tip == "" {
			return Range{}, fmt.Errorf("branch %s no longer exists", run.Branch)
		}
		return Range{From: floor, To: tip}, nil
	case ScopeCommit:
		commit, err := commitHash(ctx, repository, scope.Commit)
		if err != nil {
			return Range{}, err
		}
		if commit == floor {
			return Range{}, fmt.Errorf("commit %s is the run's base, not one of its commits", scope.Commit)
		}
		if err := onBranch(ctx, repository, run, floor, tip, commit); err != nil {
			return Range{}, err
		}
		parent, err := git(ctx, repository, nil, listLimit, "rev-parse", "--verify", commit+"^1")
		if err != nil {
			return Range{}, fmt.Errorf("find the parent of %s: %w", scope.Commit, err)
		}
		return Range{From: strings.TrimSpace(string(parent)), To: commit}, nil
	case ScopeRange:
		if tip == "" {
			return Range{}, fmt.Errorf("branch %s no longer exists", run.Branch)
		}
		from, err := commitHash(ctx, repository, scope.From)
		if err != nil {
			return Range{}, err
		}
		to, err := commitHash(ctx, repository, scope.To)
		if err != nil {
			return Range{}, err
		}
		for _, commit := range []string{from, to} {
			if err := onBranch(ctx, repository, run, floor, tip, commit); err != nil {
				return Range{}, err
			}
		}
		if ordered, err := gitStatus(ctx, repository, "merge-base", "--is-ancestor", from, to); err != nil || !ordered {
			return Range{}, errors.Join(fmt.Errorf("range %s..%s does not go forward along the branch", scope.From, scope.To), err)
		}
		return Range{From: from, To: to}, nil
	default:
		return Range{}, fmt.Errorf("unknown scope %q", scope.Kind)
	}
}

// baseOf is the commit the run started from: the recorded one, or for an
// older run the parent of its first commit.
func baseOf(ctx context.Context, repository string, run Run) (string, error) {
	if run.Base != "" {
		return commitHash(ctx, repository, run.Base)
	}
	if len(run.Commits) == 0 {
		return "", errors.New("the run recorded no base commit and made no commits, so it has no changes to show")
	}
	first, err := commitHash(ctx, repository, run.Commits[0])
	if err != nil {
		return "", err
	}
	parent, err := git(ctx, repository, nil, listLimit, "rev-parse", "--verify", first+"^1")
	if err != nil {
		return "", fmt.Errorf("find the parent of the run's first commit: %w", err)
	}
	return strings.TrimSpace(string(parent)), nil
}

// floorOf is where the branch's own commits start: the base, unless the
// branch was rebased and no longer contains it, in which case it is where
// the branch now leaves the base's history. Every scope measures from it,
// so a range built from the branch scope's result still resolves. Once the
// branch is gone, the base stands.
func floorOf(ctx context.Context, repository, branch, base, tip string) (string, error) {
	if tip == "" {
		return base, nil
	}
	output, err := git(ctx, repository, nil, listLimit, "merge-base", base, tip)
	if err != nil {
		return "", fmt.Errorf("find where %s leaves its base: %w", branch, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// branchTip is the commit the branch points at now, or empty when the
// branch is gone.
func branchTip(ctx context.Context, repository, branch string) (string, error) {
	if branch == "" {
		return "", nil
	}
	exists, err := gitStatus(ctx, repository, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil || !exists {
		return "", err
	}
	output, err := git(ctx, repository, nil, listLimit, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("read branch %s: %w", branch, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// commitHash expands a hash, full or abbreviated, to the commit it names.
func commitHash(ctx context.Context, repository, hash string) (string, error) {
	if !hashPattern.MatchString(hash) {
		return "", fmt.Errorf("%q is not a commit hash", hash)
	}
	output, err := git(ctx, repository, nil, listLimit, "rev-parse", "--verify", "--quiet", hash+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("no commit %s in this repository", hash)
	}
	return strings.TrimSpace(string(output)), nil
}

// onBranch refuses a commit that is not between the branch's floor and its
// tip. Once the branch is gone, only the run's own recorded commits are
// accepted.
func onBranch(ctx context.Context, repository string, run Run, floor, tip, commit string) error {
	if tip == "" {
		for _, recorded := range run.Commits {
			if full, err := commitHash(ctx, repository, recorded); err == nil && full == commit {
				return nil
			}
		}
		return fmt.Errorf("commit %s is not one of this run's commits, and branch %s no longer exists", short(commit), run.Branch)
	}
	for _, check := range [][2]string{{floor, commit}, {commit, tip}} {
		ancestor, err := gitStatus(ctx, repository, "merge-base", "--is-ancestor", check[0], check[1])
		if err != nil {
			return err
		}
		if !ancestor {
			return fmt.Errorf("commit %s is not on branch %s", short(commit), run.Branch)
		}
	}
	return nil
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
