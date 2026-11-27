package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/inkuna-ai/inkuna/internal/catalog"
	"github.com/inkuna-ai/inkuna/internal/sources"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// useCatalog makes the catalog commands read a small catalog of their own:
// an agent and a shared stage that names it.
func useCatalog(t *testing.T) {
	t.Helper()
	fixture, err := catalog.Load(fstest.MapFS{
		"agents/api-reviewer.md": {Data: []byte("---\nname: api-reviewer\ndescription: Reviews APIs.\n---\n\nReview it.\n")},
		"stages/api-review.yaml": {Data: []byte("# Reviews the API, read-only.\nversion: 2\nstages:\n  api-review:\n    kind: agent\n    agent: api-reviewer\n    permission: read-only\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	previous := loadCatalog
	loadCatalog = func() (catalog.Catalog, error) { return fixture, nil }
	t.Cleanup(func() { loadCatalog = previous })
}

func runJSON(t *testing.T, args []string, value any) error {
	t.Helper()
	var output bytes.Buffer
	if err := run(args, nil, &output); err != nil {
		return err
	}
	if err := json.Unmarshal(output.Bytes(), value); err != nil {
		t.Fatalf("%v: %s", err, output.String())
	}
	return nil
}

func TestCatalogListsShowsAndAddsItems(t *testing.T) {
	isolateStages(t)
	useCatalog(t)
	var listed []map[string]any
	if err := runJSON(t, []string{"catalog", "list"}, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0]["name"] != "api-review" || listed[0]["kind"] != "stage" || listed[0]["content"] != nil {
		t.Fatalf("list = %v", listed)
	}
	var shown map[string]any
	if err := runJSON(t, []string{"catalog", "show", "api-review"}, &shown); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown["content"].(string), "kind: agent") {
		t.Fatalf("show = %v", shown)
	}

	repository := t.TempDir()
	var result catalog.Result
	if err := runJSON(t, []string{"catalog", "add", "api-review", repository}, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 2 || result.Added[0].Name != "api-review" || result.Added[1].Path != filepath.Join(repository, ".inkuna", "agents", "api-reviewer.md") {
		t.Fatalf("add = %+v", result)
	}
	if err := run([]string{"catalog", "add", "api-review", repository}, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "already has shared stage") {
		t.Fatalf("a second add: %v", err)
	}

	var global catalog.Result
	if err := runJSON(t, []string{"catalog", "add", "--global", "api-reviewer"}, &global); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(os.Getenv("INKUNA_DATA_DIR"), "agents", "api-reviewer.md"); len(global.Added) != 1 || global.Added[0].Path != want {
		t.Fatalf("global add = %+v, want %s", global, want)
	}
}

// An add lands in the rules file planning reads, which inkuna.yaml may move.
func TestCatalogAddWritesTheRulesFileInkunaYAMLNames(t *testing.T) {
	isolateStages(t)
	useCatalog(t)
	repository := t.TempDir()
	config := "version: 1\nrepositories:\n  - name: bakery\n    path: " + repository + "\n    rules: config/rules.yaml\n"
	if err := os.WriteFile(os.Getenv("INKUNA_CONFIG"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	var result catalog.Result
	if err := runJSON(t, []string{"catalog", "add", "api-review", repository}, &result); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repository, "config", "rules.yaml"); result.Added[0].Path != want {
		t.Fatalf("added to %s, want %s", result.Added[0].Path, want)
	}
}

func TestCatalogRefusesMalformedCommands(t *testing.T) {
	isolateStages(t)
	useCatalog(t)
	for _, args := range [][]string{
		{"catalog"}, {"catalog", "list", "one", "two"}, {"catalog", "sources", "one", "two"}, {"catalog", "list", "--source", "acme/source"},
		{"catalog", "show"}, {"catalog", "remove", "x"}, {"catalog", "add", "api-review", "--source"}, {"catalog", "show", "--global", "api-review"},
		{"catalog", "add"}, {"catalog", "add", "--global", "api-review", "."}, {"catalog", "add", "--force", "api-review"},
	} {
		if err := run(args, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage: catalog") {
			t.Errorf("%v: %v", args, err)
		}
	}
	if err := run([]string{"catalog", "show", "nothing"}, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no item") {
		t.Errorf("show of an unknown item: %v", err)
	}
}

// keepSnapshot keeps a snapshot for repository as the app does when a
// definition source answers.
func keepSnapshot(t *testing.T, source, repository string, snapshot extension.Snapshot) {
	t.Helper()
	entry := sources.Entry{Source: source, Repository: repository, FetchedAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC), Snapshot: snapshot}
	if err := sources.Save(os.Getenv("INKUNA_DATA_DIR"), entry); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogOffersWhatTheAppKeptOfARepositorysSources(t *testing.T) {
	isolateStages(t)
	useCatalog(t)
	repository := t.TempDir()
	skillFiles := map[string][]byte{"SKILL.md": []byte("---\nname: api-style\ndescription: How this team writes APIs.\n---\n\nUse plural nouns.\n")}
	keepSnapshot(t, "acme/source", repository, extension.Snapshot{Version: "v1", Definitions: []extension.Definition{
		{Kind: extension.KindSkill, Name: "api-style", Mode: extension.ModeCatalog, Scope: "team:platform", Files: skillFiles},
		{Kind: extension.KindRule, Name: "release", Mode: extension.ModeEnforced, Scope: "team:platform"},
	}})
	var listed []map[string]any
	if err := runJSON(t, []string{"catalog", "list", repository}, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 || listed[2]["name"] != "api-style" || listed[2]["source"] != "acme/source" || listed[2]["scope"] != "team:platform" {
		t.Fatalf("list = %v", listed)
	}
	// Elsewhere only the built-in catalog is offered.
	if err := runJSON(t, []string{"catalog", "list", t.TempDir()}, &listed); err != nil || len(listed) != 2 {
		t.Fatalf("list elsewhere = %v, %v", listed, err)
	}
	var reports []sources.Report
	if err := runJSON(t, []string{"catalog", "sources", repository}, &reports); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !reports[0].Cached || reports[0].FetchedAt != "2026-11-20T09:00:00Z" || reports[0].Definitions[1].Status != sources.StatusRefuses {
		t.Fatalf("sources = %+v", reports)
	}
	var result catalog.Result
	if err := runJSON(t, []string{"catalog", "add", "api-style", repository}, &result); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repository, ".inkuna", "skills", "api-style"); len(result.Added) != 1 || result.Added[0].Path != want {
		t.Fatalf("add = %+v, want %s", result, want)
	}

	// Two sources offering one name are told apart with --source.
	keepSnapshot(t, "acme/source-2", repository, extension.Snapshot{Version: "w1", Definitions: []extension.Definition{
		{Kind: extension.KindSkill, Name: "api-style", Mode: extension.ModeCatalog, Files: skillFiles},
	}})
	if err := run([]string{"catalog", "show", "api-style", repository}, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "choose one with --source") {
		t.Fatalf("show of a name two sources offer: %v", err)
	}
	var shown catalog.Item
	if err := runJSON(t, []string{"catalog", "show", "api-style", "--source", "acme/source-2", repository}, &shown); err != nil || shown.Source != "acme/source-2" {
		t.Fatalf("show --source = %+v, %v", shown, err)
	}
	if err := run([]string{"catalog", "show", "api-style", "--source", "acme/other", repository}, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "offers nothing here") {
		t.Fatalf("show from a source with nothing kept: %v", err)
	}
}
