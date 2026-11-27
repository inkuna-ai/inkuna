package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/branching"
)

func git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "user.name=Inkuna Test", "-c", "user.email=inkuna@example.test", "-c", "commit.gpgsign=false"}, args...)...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// isolate keeps inkunactl away from the user's own configuration.
func isolate(t *testing.T) {
	t.Helper()
	data := t.TempDir()
	t.Setenv("INKUNA_DATA_DIR", data)
	t.Setenv("INKUNA_CONFIG", filepath.Join(data, "inkuna.yaml"))
}

func cloned(t *testing.T, config string) string {
	t.Helper()
	parent := t.TempDir()
	origin := filepath.Join(parent, "origin.git")
	repository := filepath.Join(parent, "bakery")
	git(t, parent, "init", "-q", "--bare", "-b", "main", origin)
	git(t, parent, "clone", "-q", origin, repository)
	if config != "" {
		path := filepath.Join(repository, filepath.FromSlash(branching.RelativePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "add", "-A")
	git(t, repository, "commit", "-qm", "initial")
	git(t, repository, "push", "-q", "origin", "HEAD:main")
	return repository
}

func TestBranchNewCreatesWorktreeAndPushesOnRequest(t *testing.T) {
	isolate(t)
	repository := cloned(t, "version: 1\ntracker: linear\nbranch:\n  base: main\n  pattern: \"{type}/{story}-{slug}\"\n  maxLength: 50\nworktrees:\n  enabled: true\n  location: ../{repo}-worktrees\n")
	t.Chdir(repository)
	var output bytes.Buffer
	if err := run([]string{"branch", "new", "--type", "feature", "--story", "eng-12", "--push", "Add", "user", "login"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	path, _ := result["worktree"].(string)
	if result["branch"] != "feature/ENG-12-add-user-login" || result["startPoint"] != "origin/main" || result["pushed"] != true ||
		!strings.HasSuffix(path, filepath.Join("bakery-worktrees", "feature", "ENG-12-add-user-login")) {
		t.Fatalf("unexpected result %v", result)
	}
	if git(t, repository, "rev-parse", "--abbrev-ref", "HEAD") != "main" {
		t.Fatal("the main checkout must stay where it was")
	}
	if upstream := git(t, path, "rev-parse", "--abbrev-ref", "@{upstream}"); upstream != "origin/feature/ENG-12-add-user-login" {
		t.Fatalf("upstream = %q", upstream)
	}

	t.Chdir(path)
	output.Reset()
	err := run([]string{"branch", "new", "--type", "feature", "--story", "ENG-12", "Add", "user", "login"}, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a second branch of the same name must be refused, even from a worktree: %v", err)
	}
	for _, args := range [][]string{
		{"branch", "new", "--type", "spike", "--story", "ENG-1", "x"},
		{"branch", "new", "--type", "feature", "x"},
		{"branch", "new", "--type", "feature", "--story", "ENG-1"},
		{"branch", "new", "--type", "feature", "--story", "ENG-1", "--base", "--upload-pack=x", "x"},
	} {
		if err := run(args, nil, &output); err == nil {
			t.Errorf("%v should be refused", args)
		}
	}
}

func TestBranchInitProposesAndWritesMissingKeys(t *testing.T) {
	isolate(t)
	repository := cloned(t, "")
	git(t, repository, "branch", "feature/ENG-3-first")
	var output bytes.Buffer
	if err := run([]string{"branch", "init", "--no-orchestrator", "--write", repository}, nil, &output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Proposed map[string]string `json:"proposed"`
		Written  []string          `json:"written"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Proposed["tracker"] != "linear" || result.Proposed["branch.pattern"] != "{type}/{story}-{slug}" || len(result.Written) != 3 {
		t.Fatalf("unexpected proposal %+v", result)
	}
	loaded, err := branching.Load(repository)
	if err != nil || !loaded.Complete() {
		t.Fatalf("init --write should leave a complete configuration: %#v %v", loaded, err)
	}
	output.Reset()
	if err := run([]string{"branch", "show", repository}, nil, &output); err != nil || !strings.Contains(output.String(), `"worktreeRoot"`) {
		t.Fatalf("branch show = %s, %v", output.String(), err)
	}
}
