package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/inkuna-ai/inkuna/internal/catalog"
)

func TestCatalogItemsAreListedAndAddedThroughBindings(t *testing.T) {
	fixture, err := catalog.Load(fstest.MapFS{
		"agents/api-reviewer.md": {Data: []byte("---\nname: api-reviewer\ndescription: Reviews APIs.\n---\n\nReview it.\n")},
		"rules/api-change.yaml": {Data: []byte(`version: 2
rules:
  - name: api-change
    description: API changes get an API review.
    when: {kind: [implementation], keywords: [endpoint]}
    then:
      stages:
        - use: profile
        - {id: implement, kind: agent, agent: auto, dependsOn: [profile]}
        - {id: review, kind: agent, agent: api-reviewer, permission: read-only, dependsOn: [implement]}
        - {use: verify-and-approve, dependsOn: [review]}
`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	previous := loadCatalog
	loadCatalog = func() (catalog.Catalog, error) { return fixture, nil }
	t.Cleanup(func() { loadCatalog = previous })

	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	service.config.AgentDirectories = []string{filepath.Join(t.TempDir(), "agents")}
	service.config.StageDirectories = []string{filepath.Join(t.TempDir(), "stages")}

	listing, err := service.CatalogItems("project")
	if err != nil || len(listing.Items) != 2 || listing.Items[0].Item.Name != "api-change" || listing.Items[0].Item.Content == "" {
		t.Fatalf("CatalogItems = %+v, %v", listing, err)
	}
	if listing.Items[0].Repository != "" || listing.RepositoryProblem != "" || listing.GlobalProblem != "" {
		t.Fatalf("an empty repository's listing = %+v", listing)
	}
	preview, err := service.PreviewCatalogItem("project", "api-change", "", false)
	if err != nil || len(preview.Added) != 2 || len(preview.Notes) != 1 || !strings.Contains(preview.Notes[0], "will be written with the built-in rules") {
		t.Fatalf("PreviewCatalogItem = %+v, %v", preview, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".inkuna")); !os.IsNotExist(err) {
		t.Fatalf("a preview wrote to the repository: %v", err)
	}
	result, err := service.AddCatalogItem("project", "api-change", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 2 || result.Added[1].Path != filepath.Join(repo, ".inkuna", "agents", "api-reviewer.md") {
		t.Fatalf("AddCatalogItem = %+v", result)
	}
	listing, err = service.CatalogItems("project")
	if err != nil || listing.Items[0].Repository != filepath.Join(repo, ".inkuna", "rules.yaml") || listing.Items[1].Repository == "" || listing.Items[1].Global != "" {
		t.Fatalf("CatalogItems after the add = %+v, %v", listing, err)
	}
	// The project's next plan sees the rule and the agent.
	view, err := service.Rules("project")
	if err != nil || len(view.Document.Rules) == 0 || view.Document.Rules[0].Name != "api-change" {
		t.Fatalf("Rules after the add = %+v, %v", view.Document.Rules, err)
	}
	if _, err := service.AddCatalogItem("project", "api-change", "", false); err == nil || !strings.Contains(err.Error(), "already has rule") {
		t.Fatalf("a second add: %v", err)
	}
	if _, err := service.AddCatalogItem("", "api-reviewer", "", false); err == nil || !strings.Contains(err.Error(), "choose a project") {
		t.Fatalf("a repository add without a project: %v", err)
	}
	global, err := service.AddCatalogItem("", "api-reviewer", "", true)
	if err != nil || len(global.Added) != 1 || global.Added[0].Path != filepath.Join(service.config.AgentDirectories[0], "api-reviewer.md") {
		t.Fatalf("a global add = %+v, %v", global, err)
	}
	// Without a project, the listing says only what this machine has.
	listing, err = service.CatalogItems("")
	if err != nil || listing.Items[1].Global != global.Added[0].Path || listing.Items[1].Repository != "" {
		t.Fatalf("CatalogItems without a project = %+v, %v", listing, err)
	}
	// Rules that do not load are said, not hidden: adding to them is refused.
	if err := os.WriteFile(filepath.Join(repo, ".inkuna", "rules.yaml"), []byte("version: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	listing, err = service.CatalogItems("project")
	if err != nil || !strings.Contains(listing.RepositoryProblem, "does not load") || len(listing.Items) != 2 {
		t.Fatalf("CatalogItems with broken rules = %+v, %v", listing, err)
	}
}
