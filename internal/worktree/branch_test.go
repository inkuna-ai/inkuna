package worktree

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// cloneWithOrigin returns a clone of a bare origin whose main branch is one
// commit ahead of the clone's local main, as after a teammate's merge.
func cloneWithOrigin(t *testing.T) (clone string) {
	t.Helper()
	parent := t.TempDir()
	origin := filepath.Join(parent, "origin.git")
	seed := filepath.Join(parent, "seed")
	clone = filepath.Join(parent, "bakery")
	runGit(t, parent, "init", "-q", "--bare", "-b", "main", origin)
	runGit(t, parent, "clone", "-q", origin, seed)
	configure(t, seed)
	commitFile(t, seed, "README.md", "one")
	runGit(t, seed, "push", "-q", "origin", "HEAD:main")
	runGit(t, parent, "clone", "-q", origin, clone)
	configure(t, clone)
	commitFile(t, seed, "MERGED.md", "two")
	runGit(t, seed, "push", "-q", "origin", "HEAD:main")
	runGit(t, seed, "push", "-q", "origin", "HEAD:feature/ENG-9-taken")
	return clone
}

func configure(t *testing.T, repository string) {
	t.Helper()
	runGit(t, repository, "config", "user.email", "inkuna@example.invalid")
	runGit(t, repository, "config", "user.name", "Inkuna Test")
	runGit(t, repository, "config", "commit.gpgsign", "false")
}

func commitFile(t *testing.T, repository, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", name)
	runGit(t, repository, "commit", "-qm", "add "+name)
}

func TestAddBranchStartsFromOriginAndRemoveKeepsBranch(t *testing.T) {
	ctx := context.Background()
	clone := cloneWithOrigin(t)
	if !HasOrigin(ctx, clone) {
		t.Fatal("clone should have origin")
	}
	if err := Fetch(ctx, clone, "main", FetchTimeout); err != nil {
		t.Fatal(err)
	}
	start, err := StartPoint(ctx, clone, "main")
	if err != nil || start != "origin/main" {
		t.Fatalf("StartPoint = %q, %v", start, err)
	}
	if _, err := StartPoint(ctx, clone, "release"); err == nil {
		t.Fatal("a base that exists nowhere must be refused")
	}
	if err := Fetch(ctx, clone, "feature/ENG-9-taken", FetchTimeout); err != nil {
		t.Fatal(err)
	}
	if !BranchTaken(ctx, clone, "feature/ENG-9-taken") || BranchTaken(ctx, clone, "feature/ENG-10-new") {
		t.Fatal("BranchTaken must see origin's branches and only those")
	}

	path := filepath.Join(filepath.Dir(clone), "bakery-worktrees", "feature", "ENG-10-new")
	created, err := AddBranch(ctx, clone, "feature/ENG-10-new", path, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(created.Path, "MERGED.md")); err != nil {
		t.Fatal("the worktree must start from origin's main, which has the merged file")
	}
	if branch, err := CurrentBranch(ctx, created.Path); err != nil || branch != "feature/ENG-10-new" {
		t.Fatalf("CurrentBranch = %q, %v", branch, err)
	}
	if upstream, err := git(ctx, clone, "rev-parse", "--abbrev-ref", "feature/ENG-10-new@{upstream}"); err == nil {
		t.Fatalf("a run's branch must not track its base, but tracks %q", upstream)
	}
	if main, err := CurrentBranch(ctx, clone); err != nil || main != "main" {
		t.Fatalf("the main checkout must stay on main, got %q, %v", main, err)
	}
	if root, err := MainRoot(ctx, created.Path); err != nil || filepath.Base(root) != "bakery" {
		t.Fatalf("MainRoot from a worktree = %q, %v", root, err)
	}
	if _, err := AddBranch(ctx, clone, "feature/ENG-10-new", path+"-again", start); err == nil {
		t.Fatal("an existing branch must be refused")
	}

	evidence, err := Evidence(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Repository != "bakery" || evidence.DefaultBranch != "main" || evidence.CurrentBranch != "main" || len(evidence.Worktrees) != 1 {
		t.Fatalf("Evidence = %#v", evidence)
	}

	// Even when status hides untracked files, they count as changes.
	runGit(t, clone, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(created.Path, "WIP.md"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, clone, created.Path); !errors.Is(err, ErrDirty) {
		t.Fatalf("a dirty worktree must be kept, got %v", err)
	}
	if err := os.Remove(filepath.Join(created.Path, "WIP.md")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, clone, created.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the worktree directory should be gone")
	}
	if !BranchTaken(ctx, clone, "feature/ENG-10-new") {
		t.Fatal("removing a worktree must keep its branch")
	}
	if err := Remove(ctx, clone, created.Path); err != nil {
		t.Fatalf("removing an already removed worktree should be a no-op: %v", err)
	}
	if err := Remove(ctx, clone, clone); err == nil {
		t.Fatal("the main checkout must never be removable")
	}
}

func TestSwitchAndShowCommit(t *testing.T) {
	ctx := context.Background()
	clone := cloneWithOrigin(t)
	if err := Switch(ctx, clone, "chore/tidy", "main"); err != nil {
		t.Fatal(err)
	}
	if branch, _ := CurrentBranch(ctx, clone); branch != "chore/tidy" {
		t.Fatalf("Switch left the checkout on %q", branch)
	}
	commitFile(t, clone, "TIDY.md", "tidy")
	head, err := Head(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, clone, "switch", "-q", "main")
	commit, err := ShowCommit(ctx, clone, head, true)
	if err != nil || commit.Subject != "add TIDY.md" || commit.Diff == "" {
		t.Fatalf("ShowCommit = %#v, %v", commit, err)
	}
	if _, err := ShowCommit(ctx, clone, "--output=/tmp/x", false); err == nil {
		t.Fatal("an option must not be accepted as a revision")
	}
	runGit(t, clone, "switch", "-q", "--detach", head)
	if branch, err := CurrentBranch(ctx, clone); err != nil || branch != "" {
		t.Fatalf("detached HEAD should report no branch, got %q, %v", branch, err)
	}
}

func TestRemoteRepositoryNamesHostOwnerAndRepo(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/inkuna-ai/inkuna.git":             "github.com/inkuna-ai/inkuna",
		"https://github.com/Inkuna-AI/Inkuna":                 "github.com/inkuna-ai/inkuna",
		"https://user:token@github.com/inkuna-ai/inkuna.git/": "github.com/inkuna-ai/inkuna",
		"git@github.com:inkuna-ai/inkuna.git":                 "github.com/inkuna-ai/inkuna",
		"ssh://git@ghe.example.com:2222/team/handbook.git":    "ghe.example.com/team/handbook",
		"git://github.com/inkuna-ai/inkuna":                   "github.com/inkuna-ai/inkuna",
		"https://gitlab.example.com/group/sub/repo.git":       "",
		"/srv/git/inkuna.git":                                 "",
		"":                                                    "",
	} {
		if got := RemoteRepository(remote); got != want {
			t.Errorf("RemoteRepository(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestOriginURLLeavesOutAnHTTPSToken(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-q")
	if got := OriginURL(context.Background(), repository); got != "" {
		t.Fatalf("OriginURL without an origin = %q", got)
	}
	runGit(t, repository, "remote", "add", "origin", "https://example.com/placeholder.git")
	for remote, want := range map[string]string{
		"https://TOKEN@github.com/inkuna-ai/inkuna.git":       "https://github.com/inkuna-ai/inkuna.git",
		"https://user:secret@github.com/inkuna-ai/inkuna.git": "https://github.com/inkuna-ai/inkuna.git",
		"git@github.com:inkuna-ai/inkuna.git":                 "git@github.com:inkuna-ai/inkuna.git",
	} {
		runGit(t, repository, "remote", "set-url", "origin", remote)
		if got := OriginURL(context.Background(), repository); got != want {
			t.Errorf("OriginURL for %q = %q, want %q", remote, got, want)
		}
	}
}

func TestFetchGivesUpOnAnOriginThatNeverAnswers(t *testing.T) {
	ctx := context.Background()
	clone := cloneWithOrigin(t)
	unsetSSHEnv(t)
	runGit(t, clone, "remote", "set-url", "origin", "ssh://git@example.invalid/bakery.git")
	// An ssh that waits, as one does on a locked agent, and holds git's
	// stderr after git is killed. Naming the variant skips git's `ssh -G`
	// probe, so the wait is in the connection rather than the probe.
	runGit(t, clone, "config", "core.sshCommand", "sleep 20; :")
	runGit(t, clone, "config", "ssh.variant", "ssh")
	began := time.Now()
	err := Fetch(ctx, clone, "main", 200*time.Millisecond)
	if !errors.Is(err, ErrFetchTimedOut) {
		t.Fatalf("Fetch = %v, want ErrFetchTimedOut", err)
	}
	if elapsed := time.Since(began); elapsed > 200*time.Millisecond+fetchWaitDelay+3*time.Second {
		t.Fatalf("Fetch waited %s for an ssh that never answers", elapsed)
	}
	if start, err := StartPoint(ctx, clone, "main"); err != nil || start != "origin/main" {
		t.Fatalf("the last fetched copy must still be the start point: %q, %v", start, err)
	}
}

func TestFetchEnvTurnsPromptsOffAndKeepsTheUsersSSHCommand(t *testing.T) {
	ctx := context.Background()
	isolateGitConfig(t)
	clone := cloneWithOrigin(t)
	unsetSSHEnv(t)
	noPrompts := []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="}
	if env := fetchEnv(ctx, clone); !slices.Equal(env, append(noPrompts, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")) {
		t.Fatalf("plain ssh should run in batch mode: %v", env)
	}
	for _, setting := range [][2]string{{"core.sshCommand", "ssh -i ~/.ssh/work"}, {"ssh.variant", "plink"}} {
		runGit(t, clone, "config", setting[0], setting[1])
		if env := fetchEnv(ctx, clone); !slices.Equal(env, noPrompts) {
			t.Fatalf("%s must be kept as the user set it: %v", setting[0], env)
		}
		runGit(t, clone, "config", "--unset", setting[0])
	}
	t.Setenv("GIT_SSH_COMMAND", "ssh -i ~/.ssh/work")
	if env := fetchEnv(ctx, clone); !slices.Equal(env, noPrompts) {
		t.Fatalf("GIT_SSH_COMMAND must be kept as the user set it: %v", env)
	}
}

func TestFetchNeverRunsAnAskpassProgram(t *testing.T) {
	// An origin that asks for credentials, so git looks for a way to ask.
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("WWW-Authenticate", `Basic realm="origin"`)
		response.WriteHeader(http.StatusUnauthorized)
	}))
	defer origin.Close()
	for _, source := range []string{"GIT_ASKPASS", "core.askPass", "SSH_ASKPASS"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			isolateGitConfig(t)
			clone := cloneWithOrigin(t)
			runGit(t, clone, "remote", "set-url", "origin", origin.URL+"/bakery.git")
			for _, name := range []string{"GIT_ASKPASS", "SSH_ASKPASS"} {
				t.Setenv(name, "")
				if err := os.Unsetenv(name); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(t.TempDir(), "asked")
			askpass := filepath.Join(t.TempDir(), "askpass")
			if err := os.WriteFile(askpass, []byte("#!/bin/sh\ntouch '"+marker+"'\necho secret\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if source == "core.askPass" {
				runGit(t, clone, "config", source, askpass)
			} else {
				t.Setenv(source, askpass)
			}
			err := Fetch(ctx, clone, "main", FetchTimeout)
			if err == nil || !strings.Contains(err.Error(), "terminal prompts disabled") {
				t.Fatalf("the fetch should fail without asking for credentials: %v", err)
			}
			if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("the fetch ran the %s program", source)
			}
		})
	}
}

// isolateGitConfig keeps the machine's global and system git configuration,
// such as a core.sshCommand or a credential helper, out of the test.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// unsetSSHEnv removes the SSH commands the environment chose for the rest of
// the test. Git runs an empty GIT_SSH as a command, so setting one to "" is
// not the same as unsetting it.
func unsetSSHEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		t.Setenv(name, "") // restores the original value after the test
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}
