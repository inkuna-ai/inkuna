package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// recordingGitHub answers gh with output and remembers the arguments.
type recordingGitHub struct {
	output []byte
	err    error
	calls  [][]string
}

func (runner *recordingGitHub) Run(_ context.Context, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, args)
	return runner.output, runner.err
}

// settingsClone is a synced clone whose project checkout has an edited rules
// file and an untracked log, and that can commit as someone.
func settingsClone(t *testing.T) (clone, teammate string) {
	t.Helper()
	clone, teammate = syncedClone(t)
	for _, args := range [][]string{
		{"config", "user.name", "Inkuna Test"},
		{"config", "user.email", "inkuna@example.test"},
		{"config", "commit.gpgsign", "false"},
		{"fetch", "-q", "origin"},
		{"merge", "-q", "--ff-only", "origin/main"},
	} {
		if err := runGit(clone, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(clone, ".inkuna"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{".inkuna/rules.yaml": "version: 1\n", "debug.log": "local"} {
		if err := os.WriteFile(filepath.Join(clone, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return clone, teammate
}

func TestShareSettingsPushesToTheBaseAndSettlesTheCheckout(t *testing.T) {
	clone, teammate := settingsClone(t)
	if err := runGit(teammate, "commit", "-q", "--allow-empty", "-m", "moved on"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(teammate, "push", "-q", "origin", "HEAD:main"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, clone, writingRuntime())

	view, err := service.FetchProject("project")
	if err != nil {
		t.Fatal(err)
	}
	if main := branchNamed(t, view, "main"); !main.Updatable || !main.Dirty {
		t.Fatalf("an edited rules file and a log leave main updatable: %+v", main)
	}
	if view.Settings == nil || len(view.Settings.Changes) != 1 || view.Settings.Changes[0].Path != ".inkuna/rules.yaml" || !view.Settings.Shareable {
		t.Fatalf("settings = %+v", view.Settings)
	}

	if _, err := service.ShareSettings("project", SharePush, "stale"); err == nil || !strings.Contains(err.Error(), "changed since they were shown") {
		t.Fatalf("a share of what the user did not see = %v", err)
	}
	if _, err := service.ShareSettings("project", "force", view.Settings.Digest); err == nil {
		t.Fatal("an unknown mode must be refused")
	}
	shared, err := service.ShareSettings("project", SharePush, view.Settings.Digest)
	if err != nil {
		t.Fatal(err)
	}
	share := shared.Shared
	if share == nil || share.Mode != SharePush || share.Base != "main" || !slices.Equal(share.Files, []string{".inkuna/rules.yaml"}) || share.Note != "" {
		t.Fatalf("share = %+v", share)
	}
	if share.Updated == nil || !slices.Equal(share.Updated.Settled, []string{".inkuna/rules.yaml"}) {
		t.Fatalf("the base fast-forwards and takes the edit as merged: %+v", share.Updated)
	}
	if shared.Settings != nil || branchNamed(t, shared, "main").State != worktree.SyncSynced {
		t.Fatalf("after the push the checkout holds no settings edits: %+v", shared)
	}
	if err := runGit(teammate, "pull", "-q", "--ff-only", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(teammate, ".inkuna", "rules.yaml")); err != nil || string(data) != "version: 1\n" {
		t.Fatalf("origin has the rules: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(teammate, "debug.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("only settings are shared")
	}
}

func TestShareSettingsOpensAPullRequestOnce(t *testing.T) {
	clone, _ := settingsClone(t)
	service := workflowTestService(t, clone, writingRuntime())
	view, err := service.ProjectBranches("project")
	if err != nil || view.Settings == nil {
		t.Fatalf("view = %+v, %v", view, err)
	}
	shared, err := service.ShareSettings("project", SharePullRequest, view.Settings.Digest)
	if err != nil {
		t.Fatal(err)
	}
	share := shared.Shared
	// Without a branching configuration the branch is inkuna/settings, and
	// an origin that is not GitHub gets no pull request from gh.
	if share.Branch != "inkuna/settings" || share.PullRequest || share.Note != "Open a pull request for it on origin." {
		t.Fatalf("share = %+v", share)
	}
	if shared.Settings == nil || shared.Settings.SharedOn != "inkuna/settings" {
		t.Fatalf("the checkout keeps the edit, shared on the branch: %+v", shared.Settings)
	}
	if branch := branchNamed(t, shared, "inkuna/settings"); branch.Upstream != "origin/inkuna/settings" || branch.State != worktree.SyncSynced {
		t.Fatalf("the share's branch tracks its push: %+v", branch)
	}
	if _, err := service.ShareSettings("project", SharePullRequest, shared.Settings.Digest); err == nil || !strings.Contains(err.Error(), "already shared on inkuna/settings") {
		t.Fatalf("a second share of the same edits = %v", err)
	}
}

func TestSettingsBranchFollowsTheBranchingConfiguration(t *testing.T) {
	repository := t.TempDir()
	if err := runGit(repository, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	writeBranching(t, repository, "version: 1\ntracker: github\nbranch:\n  base: main\n  pattern: \"{type}/{story}-{slug}\"\n  types: [feature, chore]\n")
	if err := runGit(repository, "commit", "-q", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	name, err := settingsBranch(context.Background(), repository, []string{".inkuna/rules.yaml"})
	if err != nil || name != "chore/inkuna-settings-rules" {
		t.Fatalf("name = %q, %v", name, err)
	}
	if err := runGit(repository, "branch", name); err != nil {
		t.Fatal(err)
	}
	if again, err := settingsBranch(context.Background(), repository, []string{".inkuna/rules.yaml"}); err != nil || again != name+"-2" {
		t.Fatalf("a taken name is numbered: %q, %v", again, err)
	}
	if many, err := settingsBranch(context.Background(), repository, []string{"a", "b"}); err != nil || many != "chore/inkuna-settings" {
		t.Fatalf("several files = %q, %v", many, err)
	}
}

func writeBranching(t *testing.T, repository, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repository, ".inkuna"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".inkuna", "branching.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenPullRequestFallsBackToGitHubsPage(t *testing.T) {
	gh := &recordingGitHub{output: []byte("Creating pull request\nhttps://github.com/acme/bakery/pull/7\n")}
	service := &Service{github: gh}
	url, opened, note := service.openPullRequest(context.Background(), "github.com/acme/bakery", "main", "chore/inkuna-settings", "Update Inkuna settings", "body")
	if url != "https://github.com/acme/bakery/pull/7" || !opened || note != "" {
		t.Fatalf("opened = %q, %v, %q", url, opened, note)
	}
	want := []string{"pr", "create", "--repo", "github.com/acme/bakery", "--base", "main", "--head", "chore/inkuna-settings", "--title", "Update Inkuna settings", "--body", "body"}
	if len(gh.calls) != 1 || !slices.Equal(gh.calls[0], want) {
		t.Fatalf("gh calls = %q", gh.calls)
	}

	service.github = &recordingGitHub{output: []byte("not logged in"), err: errors.New("exit status 4")}
	url, opened, note = service.openPullRequest(context.Background(), "github.com/acme/bakery", "main", "chore/inkuna-settings", "t", "b")
	if url != "https://github.com/acme/bakery/compare/main...chore/inkuna-settings?expand=1" || opened || !strings.Contains(note, "not logged in") {
		t.Fatalf("fallback = %q, %v, %q", url, opened, note)
	}
}

func TestProjectSettingsIncludeARulesOverrideInTheCheckout(t *testing.T) {
	clone, _ := syncedClone(t)
	inside := filepath.Join(clone, "app")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, inside, writingRuntime())
	service.config.File.Repositories = append(service.config.File.Repositories, config.RepositoryConfig{Path: inside, Rules: "../config/rules.yaml"})
	project, root, err := service.syncProject(context.Background(), "project")
	if err != nil {
		t.Fatal(err)
	}
	settings := service.projectSettings(project, root)
	if settings.Dir != "app/.inkuna" || !slices.Equal(settings.Files, []string{"config/rules.yaml"}) {
		t.Fatalf("settings = %+v", settings)
	}
}
