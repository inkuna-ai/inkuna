package worktree

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/branching"
)

// ErrDirty means a worktree has uncommitted or untracked changes, so it is
// kept rather than removed.
var ErrDirty = errors.New("worktree has uncommitted changes")

// HasOrigin reports whether the repository has a remote named origin.
func HasOrigin(ctx context.Context, repository string) bool {
	_, err := git(ctx, repository, "remote", "get-url", "origin")
	return err == nil
}

// FetchTimeout is how long Fetch should wait for origin. A fetch that gives
// up only means starting from the last fetched copy, so a short wait costs
// little, while one that never answered held a run in "running" for as long
// as its step could take.
const FetchTimeout = 30 * time.Second

// fetchWaitDelay is how long Fetch waits, once git has exited or been
// killed, for git's ssh to let go of its output: an ssh waiting on a locked
// agent outlives the git that started it.
const fetchWaitDelay = 2 * time.Second

// ErrFetchTimedOut means origin did not answer a fetch within its timeout.
var ErrFetchTimedOut = errors.New("fetch timed out")

// Fetch updates origin's copy of one branch, giving up after timeout.
// Callers treat a failure as a warning: an offline machine still has the
// refs it fetched before. Nobody is there to answer a prompt, so git's are
// off, and so are ssh's unless the user chose their own SSH command.
func Fetch(ctx context.Context, repository, branch string, timeout time.Duration) error {
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"fetch", "--quiet", "origin", "--", branch}
	command := gitCommand(bounded, repository, fetchEnv(bounded, repository), args...)
	command.WaitDelay = fetchWaitDelay
	_, err := runCommand(command, args)
	if err != nil && ctx.Err() == nil && errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: origin did not answer within %s", ErrFetchTimedOut, timeout)
	}
	return err
}

// fetchEnv turns off git's credential prompts and, when git would run plain
// ssh, ssh's own: BatchMode fails a passphrase or host-key question at once
// instead of waiting on a terminal. GIT_TERMINAL_PROMPT stops only git's
// terminal prompt; git first runs an askpass program (GIT_ASKPASS, then
// core.askPass, then SSH_ASKPASS), which may open a window, and an empty
// GIT_ASKPASS stops it looking for one. An SSH command the user chose
// (GIT_SSH, GIT_SSH_COMMAND, core.sshCommand, or an ssh.variant that may not
// take -o) is left as it is.
func fetchEnv(ctx context.Context, repository string) []string {
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="}
	if os.Getenv("GIT_SSH_COMMAND") != "" || os.Getenv("GIT_SSH") != "" {
		return env
	}
	if _, err := git(ctx, repository, "config", "--get-regexp", `^(core\.sshcommand|ssh\.variant)$`); err == nil {
		return env
	}
	return append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
}

// StartPoint resolves where a new branch starts: origin/<base> when origin
// has it, so a stale local copy never hides merged work, else <base>.
func StartPoint(ctx context.Context, repository, base string) (string, error) {
	if refExists(ctx, repository, "refs/remotes/origin/"+base) {
		return "origin/" + base, nil
	}
	if refExists(ctx, repository, "refs/heads/"+base) {
		return base, nil
	}
	return "", fmt.Errorf("base branch %q exists neither locally nor on origin", base)
}

// BranchTaken reports whether a branch exists locally or on origin.
func BranchTaken(ctx context.Context, repository, name string) bool {
	return refExists(ctx, repository, "refs/heads/"+name) || refExists(ctx, repository, "refs/remotes/origin/"+name)
}

// AddBranch creates branch at start and checks it out in a new worktree at
// path, without touching the main checkout. A branch git created before the
// worktree failed is deleted again so a retry starts clean.
func AddBranch(ctx context.Context, repository, branch, path, start string) (Worktree, error) {
	if _, err := os.Stat(path); err == nil {
		return Worktree{}, fmt.Errorf("worktree path already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Worktree{}, err
	}
	if BranchTaken(ctx, repository, branch) {
		return Worktree{}, fmt.Errorf("branch already exists: %s", branch)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Worktree{}, err
	}
	// --no-track: a run's branch must not adopt origin/<base> as its
	// upstream, or a plain `git push` from the worktree would land on base.
	if _, err := git(ctx, repository, "worktree", "add", "--quiet", "--no-track", "-b", branch, "--", path, start); err != nil {
		if refExists(ctx, repository, "refs/heads/"+branch) {
			_, _ = git(context.Background(), repository, "branch", "-D", "--", branch)
		}
		return Worktree{}, fmt.Errorf("create git worktree: %w", err)
	}
	return Worktree{Branch: branch, Path: path}, nil
}

// Switch creates branch at start and checks it out in the repository
// itself, for repositories that work without worktrees.
func Switch(ctx context.Context, repository, branch, start string) error {
	if BranchTaken(ctx, repository, branch) {
		return fmt.Errorf("branch already exists: %s", branch)
	}
	_, err := git(ctx, repository, "switch", "--quiet", "--no-track", "-c", branch, start)
	return err
}

// Push publishes a branch to origin and sets it as the upstream. Inkuna's
// runs never call it; it serves `inkunactl branch new --push` only.
func Push(ctx context.Context, directory, branch string) error {
	_, err := git(ctx, directory, "push", "--quiet", "-u", "origin", "--", branch)
	return err
}

// Remove deletes a linked worktree's directory and its git bookkeeping. The
// branch and every commit on it stay. A worktree with changes is kept
// (ErrDirty), and the main checkout can never be removed this way.
func Remove(ctx context.Context, repository, path string) error {
	linked, err := linkedWorktrees(ctx, repository)
	if err != nil {
		return err
	}
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && linked[filepath.Clean(resolved)] {
		clean = filepath.Clean(resolved)
	}
	if !linked[clean] {
		if _, statErr := os.Stat(clean); errors.Is(statErr, os.ErrNotExist) {
			// Already gone; forget git's record of it.
			return Prune(ctx, repository)
		}
		return fmt.Errorf("%s is not a linked worktree of %s", clean, repository)
	}
	status, err := Status(ctx, clean)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("%w: %s", ErrDirty, clean)
	}
	_, err = git(ctx, repository, "worktree", "remove", "--", clean)
	return err
}

// Prune forgets worktrees whose directories no longer exist.
func Prune(ctx context.Context, repository string) error {
	_, err := git(ctx, repository, "worktree", "prune")
	return err
}

// CurrentBranch returns the branch checked out at path, or "" when HEAD is
// detached.
func CurrentBranch(ctx context.Context, path string) (string, error) {
	command := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "-q", "HEAD")
	command.Dir = path
	output, err := command.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("git symbolic-ref HEAD: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// Contains reports whether commit is revision or one of its ancestors. A
// commit git does not have is an error, for the caller to explain.
func Contains(ctx context.Context, path, commit, revision string) (bool, error) {
	command := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", commit, revision)
	command.Dir = path
	err := command.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", commit, revision, err)
	}
	return true, nil
}

// Status returns `git status --porcelain` for a checkout: empty when clean.
// Untracked files always count, whatever status.showUntrackedFiles says,
// because removal would delete them and a commit would sweep them in.
func Status(ctx context.Context, path string) (string, error) {
	return git(ctx, path, "status", "--porcelain=v1", "--untracked-files=all")
}

// Changes is Status without OutDir: what a commit by Inkuna would take.
// Files left there do not stop a run from committing or a round from
// starting, since no commit of Inkuna's includes them.
func Changes(ctx context.Context, path string) (string, error) {
	return git(ctx, path, append([]string{"status", "--porcelain=v1", "--untracked-files=all"}, withoutOut...)...)
}

// OutIgnored reports whether git ignores the files Inkuna writes in OutDir
// of the checkout or worktree at path.
func OutIgnored(ctx context.Context, path string) (bool, error) {
	command := exec.CommandContext(ctx, "git", "check-ignore", "-q", "--no-index", OutDir+"/inkuna")
	command.Dir = path
	err := command.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("git check-ignore %s: %w", OutDir, err)
	}
	return true, nil
}

// MainRoot returns the main checkout of the repository containing
// directory, even when directory is inside a linked worktree. git lists the
// main checkout first, which also holds for submodules and separate git
// directories.
func MainRoot(ctx context.Context, directory string) (string, error) {
	output, err := git(ctx, directory, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(output, "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			return filepath.Clean(path), nil
		}
	}
	return "", fmt.Errorf("git lists no worktree for %s", directory)
}

var remoteHost = regexp.MustCompile(`^(?:[a-z+]+://)?(?:[^@/]+@)?([^/:]+)`)

var remoteRepository = regexp.MustCompile(`^(?:[a-z+]+://)?(?:[^@/]+@)?([^/:]+)(?::\d+)?[:/]+([^/]+)/([^/]+?)(?:\.git)?/?$`)

// RemoteRepository names the repository a remote URL points at as
// host/owner/repo, lowercase, for https, ssh, and scp-style remotes. It is
// empty for a URL of any other shape, such as one with a deeper path.
func RemoteRepository(remote string) string {
	match := remoteRepository.FindStringSubmatch(strings.TrimSpace(remote))
	if match == nil {
		return ""
	}
	return strings.ToLower(match[1] + "/" + match[2] + "/" + match[3])
}

// OriginURL is the URL of a repository's origin remote, or "" when it has
// none. An http or https URL loses its user information, which can hold a
// token (https://TOKEN@github.com/...), so the URL can be shown and sent on.
func OriginURL(ctx context.Context, repository string) string {
	remote, err := git(ctx, repository, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	remote = strings.TrimSpace(remote)
	if parsed, err := url.Parse(remote); err == nil && parsed.User != nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		parsed.User = nil
		return parsed.String()
	}
	return remote
}

// OriginRepository names a repository's origin remote as RemoteRepository
// does, or returns "" when it has none.
func OriginRepository(ctx context.Context, repository string) string {
	remote, err := git(ctx, repository, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return RemoteRepository(remote)
}

// Evidence gathers, read-only, how a repository already branches.
func Evidence(ctx context.Context, repository string) (branching.Evidence, error) {
	root, err := filepath.Abs(repository)
	if err != nil {
		return branching.Evidence{}, err
	}
	evidence := branching.Evidence{Repository: filepath.Base(root), Branches: []string{}, Worktrees: []string{}}
	if url, err := git(ctx, root, "remote", "get-url", "origin"); err == nil {
		if match := remoteHost.FindStringSubmatch(strings.TrimSpace(url)); match != nil {
			evidence.RemoteHost = match[1]
		}
	}
	if head, err := git(ctx, root, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); err == nil {
		evidence.DefaultBranch = strings.TrimPrefix(strings.TrimSpace(head), "origin/")
	}
	if current, err := CurrentBranch(ctx, root); err == nil {
		evidence.CurrentBranch = current
	}
	refs, err := git(ctx, root, "for-each-ref", "--sort=-committerdate", "--count=80", "--format=%(refname)", "refs/heads", "refs/remotes/origin")
	if err != nil {
		return branching.Evidence{}, err
	}
	seen := map[string]bool{}
	for _, ref := range strings.Split(refs, "\n") {
		name := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/"), "refs/remotes/origin/")
		if name == "" || name == "HEAD" || seen[name] || len(evidence.Branches) >= 40 {
			continue
		}
		seen[name] = true
		evidence.Branches = append(evidence.Branches, name)
	}
	linked, err := linkedWorktrees(ctx, root)
	if err != nil {
		return branching.Evidence{}, err
	}
	for path := range linked {
		if relative, err := filepath.Rel(root, path); err == nil {
			evidence.Worktrees = append(evidence.Worktrees, relative)
		}
	}
	return evidence, nil
}

// linkedWorktrees lists the repository's worktrees other than the main
// checkout, by cleaned absolute path.
func linkedWorktrees(ctx context.Context, repository string) (map[string]bool, error) {
	output, err := git(ctx, repository, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	linked := map[string]bool{}
	first := true
	for _, line := range strings.Split(output, "\n") {
		path, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
		if first {
			first = false // git lists the main checkout first
			continue
		}
		linked[filepath.Clean(path)] = true
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			linked[filepath.Clean(resolved)] = true
		}
	}
	return linked, nil
}

func refExists(ctx context.Context, repository, ref string) bool {
	_, err := git(ctx, repository, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}
