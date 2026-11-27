package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/proc"
)

// Commit describes one git commit inside a worktree.
type Commit struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"shortHash"`
	Author    string `json:"author"`
	Date      string `json:"date"`
	Subject   string `json:"subject"`
	Body      string `json:"body,omitempty"`
	Stat      string `json:"stat"`
	Diff      string `json:"diff,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// MaxDiffBytes bounds the diff returned to the UI.
const MaxDiffBytes = 256 << 10

// OutDir is where agents and Inkuna leave files for the steps after them,
// relative to the working directory. Inkuna's commits never include it, so
// a repository that does not ignore it still keeps it out of a run's branch.
const OutDir = ".inkuna/out"

// withoutOut is the pathspec for the whole working directory except OutDir.
var withoutOut = []string{"--", ".", ":(exclude)" + OutDir}

// CommitAll stages every change in the worktree outside OutDir and commits
// it on the worktree's branch. It returns ok=false when there was nothing to
// commit. The commit is local only; Inkuna never pushes.
func CommitAll(ctx context.Context, path, message string) (Commit, bool, error) {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(message) == "" {
		return Commit{}, false, errors.New("worktree path and commit message are required")
	}
	if _, err := git(ctx, path, "add", "-A", "--", "."); err != nil {
		return Commit{}, false, err
	}
	// OutDir is unstaged after the fact: an exclude pathspec on git add
	// fails when a .gitignore ignores the directory, since it names an
	// ignored path. Reset puts a tracked file there back as HEAD has it.
	if _, err := git(ctx, path, "reset", "-q", "--", OutDir); err != nil {
		return Commit{}, false, err
	}
	status, err := git(ctx, path, append([]string{"status", "--porcelain=v1"}, withoutOut...)...)
	if err != nil {
		return Commit{}, false, err
	}
	if strings.TrimSpace(status) == "" {
		return Commit{}, false, nil
	}
	if _, err := git(ctx, path, "-c", "user.name=Inkuna", "-c", "user.email=inkuna@localhost", "-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-m", message); err != nil {
		return Commit{}, false, err
	}
	commit, err := LastCommit(ctx, path, true)
	return commit, true, err
}

// LastCommit describes HEAD. includeDiff attaches the bounded patch.
func LastCommit(ctx context.Context, path string, includeDiff bool) (Commit, error) {
	return ShowCommit(ctx, path, "HEAD", includeDiff)
}

// ShowCommit describes one commit of the repository at path. Worktrees share
// their repository's objects, so a run's commit stays readable from the main
// checkout after its worktree is removed.
func ShowCommit(ctx context.Context, path, revision string, includeDiff bool) (Commit, error) {
	if strings.TrimSpace(path) == "" {
		return Commit{}, errors.New("worktree path is required")
	}
	if strings.HasPrefix(revision, "-") || strings.TrimSpace(revision) == "" {
		return Commit{}, fmt.Errorf("invalid revision %q", revision)
	}
	format := "%H%x1f%h%x1f%an <%ae>%x1f%aI%x1f%s%x1f%b"
	raw, err := git(ctx, path, "log", "-1", "--format="+format, revision, "--")
	if err != nil {
		return Commit{}, err
	}
	fields := strings.SplitN(raw, "\x1f", 6)
	if len(fields) < 5 {
		return Commit{}, fmt.Errorf("unexpected git log output %q", raw)
	}
	commit := Commit{Hash: fields[0], ShortHash: fields[1], Author: fields[2], Date: fields[3], Subject: fields[4]}
	if len(fields) == 6 {
		commit.Body = strings.TrimSpace(fields[5])
	}
	stat, err := git(ctx, path, "show", "--stat", "--format=", commit.Hash)
	if err != nil {
		return Commit{}, err
	}
	commit.Stat = strings.TrimSpace(stat)
	if includeDiff {
		diff, err := git(ctx, path, "show", "--format=", "--no-color", commit.Hash)
		if err != nil {
			return Commit{}, err
		}
		if len(diff) > MaxDiffBytes {
			diff = diff[:MaxDiffBytes]
			commit.Truncated = true
		}
		commit.Diff = diff
	}
	return commit, nil
}

// DiffSince returns the patch between a base commit and the worktree HEAD.
func DiffSince(ctx context.Context, path, base string) (string, bool, error) {
	diff, err := git(ctx, path, "diff", "--no-color", base, "HEAD")
	if err != nil {
		return "", false, err
	}
	if len(diff) > MaxDiffBytes {
		return diff[:MaxDiffBytes], true, nil
	}
	return diff, false, nil
}

// Head returns the current commit hash.
func Head(ctx context.Context, path string) (string, error) {
	return git(ctx, path, "rev-parse", "HEAD")
}

// git runs a command and returns stdout only, so diagnostics such as
// GIT_TRACE output on stderr can never contaminate parsed results.
func git(ctx context.Context, directory string, args ...string) (string, error) {
	return gitEnv(ctx, directory, nil, args...)
}

// gitEnv is git with variables added to Inkuna's own environment.
func gitEnv(ctx context.Context, directory string, env []string, args ...string) (string, error) {
	return runCommand(gitCommand(ctx, directory, env, args...), args)
}

// gitCommand prepares git in directory with variables added to Inkuna's own
// environment, for a caller that tunes the command before running it.
func gitCommand(ctx context.Context, directory string, env []string, args ...string) *exec.Cmd {
	command := proc.Command(ctx, "git", args...)
	command.Dir = directory
	if len(env) > 0 {
		command.Env = append(os.Environ(), env...)
	}
	return command
}

// runCommand runs a prepared git command and returns its stdout.
func runCommand(command *exec.Cmd, args []string) (string, error) {
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}
