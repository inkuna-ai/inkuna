package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/docs"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

func TestRulesAreEditedValidatedAndSavedThroughBindings(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	view, err := service.Rules("project")
	if err != nil || view.Exists {
		t.Fatalf("expected built-in rules: %#v %v", view, err)
	}

	edited := view.Document
	edited.Rules = append([]rules.Rule{{
		Name: "docs-only",
		When: rules.Condition{Kinds: []domain.TaskKind{domain.TaskDocumentation}, Keywords: []string{"readme"}},
		Then: rules.Flow{Stages: []rules.Stage{
			{ID: "write", Kind: domain.StepAgent, Agent: "documenter", Permission: domain.PermissionWorktree},
			{ID: "approve", Kind: domain.StepApproval, DependsOn: []string{"write"}},
		}},
	}}, edited.Rules...)
	encode := func(document rules.Document) string {
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	// Form -> YAML -> form keeps the document intact.
	content, err := service.RulesToYAML("project", encode(edited))
	if err != nil || !strings.Contains(content, "docs-only") {
		t.Fatalf("RulesToYAML = %q, %v", content, err)
	}
	parsed, err := service.RulesFromYAML("project", content)
	if err != nil || len(parsed.Rules) != len(edited.Rules) || parsed.Rules[0].Name != "docs-only" {
		t.Fatalf("RulesFromYAML = %#v, %v", parsed, err)
	}

	saved, err := service.SaveRules("project", encode(edited))
	if err != nil || !saved.Exists || saved.Document.Rules[0].Name != "docs-only" {
		t.Fatalf("SaveRules = %#v, %v", saved, err)
	}
	path := filepath.Join(repo, ".inkuna", "rules.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Go stays the authority: unsafe or unresolvable flows never reach disk.
	noApproval := edited
	noApproval.Rules = append([]rules.Rule(nil), edited.Rules...)
	noApproval.Rules[0].Then = rules.Flow{Stages: edited.Rules[0].Then.Stages[:1]}
	if _, err := service.SaveRules("project", encode(noApproval)); err == nil || !strings.Contains(err.Error(), "docs-only") {
		t.Fatalf("a flow without a final approval must be rejected and name its rule: %v", err)
	}
	unknownAgent := edited
	unknownAgent.Rules = append([]rules.Rule(nil), edited.Rules...)
	stages := append([]rules.Stage(nil), edited.Rules[0].Then.Stages...)
	stages[0].Agent = "nobody"
	unknownAgent.Rules[0].Then = rules.Flow{Stages: stages}
	if _, err := service.SaveRules("project", encode(unknownAgent)); err == nil || !strings.Contains(err.Error(), `unknown agent "nobody"`) {
		t.Fatalf("an unknown agent must be rejected: %v", err)
	}
	if _, err := service.RulesFromYAML("project", "version: 1\nrules: []\nbogus: true\n"); err == nil {
		t.Fatal("unknown YAML fields must be rejected")
	}
	if _, err := service.SaveRules("project", `{"version":1,"rules":[],"surprise":true}`); err == nil {
		t.Fatal("unknown JSON fields must be rejected")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("rejected saves changed the rules file: %v", err)
	}
}

func TestDocIndexFollowsTheDeclaredDocsDirectory(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	doc := "---\ntitle: Guide\nlastUpdated: 2026-10-13\nauthor: Brandon Manning\ntags: [guide]\nstatus: current\n---\n# Guide\n"
	for _, name := range []string{"docs/guide.md", "handbook/intro.md"} {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "todo.md"), []byte("no frontmatter"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	view, err := service.DocIndex("project")
	if err != nil || view.Path != filepath.Join(repo, "docs") || len(view.Docs) != 2 {
		t.Fatalf("default index = %#v, %v", view, err)
	}
	if view.Docs[0].Path != "guide.md" || view.Docs[0].Title != "Guide" || len(view.Docs[1].Problems) != 1 {
		t.Fatalf("index entries = %#v", view.Docs)
	}
	service.config.File.Repositories = []config.RepositoryConfig{{Name: "repo", Path: repo, Docs: "handbook"}}
	view, err = service.DocIndex("project")
	if err != nil || view.Path != filepath.Join(repo, "handbook") || len(view.Docs) != 1 || view.Docs[0].Path != "intro.md" {
		t.Fatalf("override index = %#v, %v", view, err)
	}
	service.config.File.Repositories[0].Docs = "../outside"
	if _, err := service.DocIndex("project"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escaping docs dir must fail, got %v", err)
	}
}

func TestDocsAndDocContentBrowseTheDeclaredDocsRoot(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	doc := "---\ntitle: Guide\nlastUpdated: 2026-10-17\nauthor: Brandon Manning\ntags: [guide]\nstatus: current\n---\n# Guide\n"
	for name, content := range map[string]string{"handbook/intro.md": doc, "handbook/guides/setup.md": "# Setup\n", "docs/other.md": doc, "secret.md": doc} {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	service.config.File.Repositories = []config.RepositoryConfig{{Name: "repo", Path: repo, Docs: "handbook"}}

	view, err := service.Docs("project")
	if err != nil || view.Path != filepath.Join(repo, "handbook") {
		t.Fatalf("Docs = %#v, %v", view, err)
	}
	if len(view.Tree.Docs) != 1 || view.Tree.Docs[0].Path != "intro.md" || len(view.Tree.Folders) != 1 ||
		view.Tree.Folders[0].Path != "guides" || view.Tree.Folders[0].Docs[0].Path != "guides/setup.md" {
		t.Fatalf("tree = %#v", view.Tree)
	}
	content, err := service.DocContent("project", "intro.md")
	if err != nil || content.Doc.Title != "Guide" || content.Markdown != "# Guide\n" {
		t.Fatalf("DocContent = %#v, %v", content, err)
	}
	for _, path := range []string{"../secret.md", "../docs/other.md", "/etc/hosts.md"} {
		if _, err := service.DocContent("project", path); err == nil {
			t.Errorf("DocContent(%q) must be refused", path)
		}
	}
	if _, err := service.DocContent("missing", "intro.md"); err == nil || !strings.Contains(err.Error(), "load project") {
		t.Errorf("an unknown project must be refused, got %v", err)
	}
}

func TestDocGraphLinksRegisteredProjectsThroughTheirOrigin(t *testing.T) {
	doc := "---\ntitle: %s\nlastUpdated: 2026-10-17\nauthor: Brandon Manning\ntags: [guide]\nstatus: current\n---\n%s\n"
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bakery, handbook := t.TempDir(), t.TempDir()
	for _, repo := range []string{bakery, handbook} {
		if err := runGit(repo, "init"); err != nil {
			t.Fatal(err)
		}
	}
	for repo, origin := range map[string]string{bakery: "https://github.com/acme/bakery.git", handbook: "git@github.com:Acme/handbook.git"} {
		if err := runGit(repo, "remote", "add", "origin", origin); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(bakery, "docs", "a.md"), fmt.Sprintf(doc, "A", "[b](b.md), [style](https://github.com/acme/handbook/blob/main/guides/style.md#top)"))
	write(filepath.Join(bakery, "docs", "b.md"), fmt.Sprintf(doc, "B", ""))
	write(filepath.Join(handbook, "guides", "style.md"), fmt.Sprintf(doc, "Style", "[back](https://github.com/acme/bakery/blob/main/docs/a.md)"))
	write(filepath.Join(handbook, "guides", "unrelated.md"), fmt.Sprintf(doc, "Unrelated", ""))

	service := workflowTestService(t, bakery, &workflowRuntime{})
	ctx := context.Background()
	for _, project := range []storage.Project{
		{ID: "handbook", Name: "Handbook", Repository: handbook, DefaultRuntime: domain.RuntimeCodex},
		{ID: "gone", Name: "Gone", Repository: filepath.Join(t.TempDir(), "gone"), DefaultRuntime: domain.RuntimeCodex},
	} {
		if err := service.store.UpsertProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	service.config.File.Repositories = []config.RepositoryConfig{{Name: "handbook", Path: handbook, Docs: "guides"}}

	view, err := service.DocGraph("project", DocGraphProject)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []string{}
	for _, node := range view.Nodes {
		nodes = append(nodes, node.ID)
	}
	if want := []string{"doc:handbook:style.md", "doc:project:a.md", "doc:project:b.md"}; !reflect.DeepEqual(nodes, want) {
		t.Fatalf("project-scope nodes = %v", nodes)
	}
	want := []docs.GraphEdge{
		{From: "doc:handbook:style.md", To: "doc:project:a.md", Count: 1},
		{From: "doc:project:a.md", To: "doc:handbook:style.md", Count: 1},
		{From: "doc:project:a.md", To: "doc:project:b.md", Count: 1},
	}
	if !reflect.DeepEqual(view.Edges, want) {
		t.Fatalf("project-scope edges = %#v", view.Edges)
	}
	if len(view.Warnings) != 1 || !strings.HasPrefix(view.Warnings[0], "Gone: ") {
		t.Fatalf("a project that cannot be indexed is a warning: %v", view.Warnings)
	}

	all, err := service.DocGraph("project", DocGraphAll)
	if err != nil || len(all.Nodes) != 4 || len(all.Edges) != 3 {
		t.Fatalf("all-projects graph = %#v, %v", all, err)
	}
	if _, err := service.DocGraph("project", "everything"); err == nil || !strings.Contains(err.Error(), "must be project or all") {
		t.Errorf("an unknown scope must be refused, got %v", err)
	}
	if _, err := service.DocGraph("gone", DocGraphProject); err == nil || !strings.Contains(err.Error(), "index docs") {
		t.Errorf("the browsed project must index, got %v", err)
	}
}

func TestRepositoryPathSkipsEntriesWithoutTheOverride(t *testing.T) {
	repo := t.TempDir()
	service := workflowTestService(t, repo, &workflowRuntime{})
	service.config.File.Repositories = []config.RepositoryConfig{
		{Name: "first", Path: repo, Docs: "handbook"},
		{Name: "second", Path: repo, Rules: "flows.yaml"},
	}
	project := storage.Project{Repository: repo}
	if path := service.rulesPath(project); path != filepath.Join(repo, "flows.yaml") {
		t.Fatalf("rulesPath = %s, want the later entry's override", path)
	}
	entry, ok := service.repositoryConfig(project)
	if !ok || entry.Name != "first" {
		t.Fatalf("repositoryConfig should keep the first entry: %#v", entry)
	}
}

// Uses and their overrides are saved from the Rules editor as they were
// written, and its checks hold an override, and a shared stage no flow uses,
// to the project's agents as they hold an inline stage.
func TestSharedStagesSurviveTheRulesEditorAndItsChecks(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repo, &workflowRuntime{})
	const content = `version: 2
stages:
  review:
    kind: agent
    agent: reviewer
    permission: read-only
  approval:
    kind: approval
    review: true
rules: []
default:
  stages:
    - id: implement
      kind: agent
      agent: implementer
    - use: review
      dependsOn: [implement]
    - use: approval
      dependsOn: [review]
      review: false
`
	parsed, err := service.RulesFromYAML("project", content)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(document rules.Document) string {
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	saved, err := service.SaveRules("project", encode(parsed))
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(repo, ".inkuna", "rules.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"version: 2\n", "- use: review\n", "- use: approval\n", "review: false\n"} {
		if !strings.Contains(string(written), want) {
			t.Fatalf("saved rules lack %q:\n%s", want, written)
		}
	}
	if again, err := service.RulesToYAML("project", encode(saved.Document)); err != nil || again != string(written) {
		t.Fatalf("the YAML tab shows something other than the file (%v):\n%s", err, again)
	}

	// A use's override is held to the project's agents like an inline stage.
	unknown := strings.Replace(content, "      dependsOn: [implement]\n", "      dependsOn: [implement]\n      agent: nobody\n", 1)
	if _, err := service.RulesFromYAML("project", unknown); err == nil || !strings.Contains(err.Error(), `default flow: stage "review" (from shared stage "review") names unknown agent "nobody"`) {
		t.Fatalf("an override naming an unknown agent must be refused naming the use: %v", err)
	}
	writable := strings.Replace(content, "      dependsOn: [implement]\n", "      dependsOn: [implement]\n      permission: worktree-write\n", 1)
	if _, err := service.RulesFromYAML("project", writable); err == nil || !strings.Contains(err.Error(), `read-only agent "reviewer" cannot do`) {
		t.Fatalf("an override that makes a read-only agent write must be refused: %v", err)
	}
	unused := strings.Replace(content, "stages:\n  review:", "stages:\n  spare:\n    kind: agent\n    agent: nobody\n  review:", 1)
	if _, err := service.RulesFromYAML("project", unused); err == nil || !strings.Contains(err.Error(), `shared stage "spare": stage "spare" names unknown agent "nobody"`) {
		t.Fatalf("a shared stage no flow uses yet is still checked: %v", err)
	}

	// A document without shared stages keeps its version, so builds that
	// read only version 1 still read it, even when it leaves its default
	// flow out and takes the built-in one, which uses shared stages.
	plain, err := rules.Parse("version: 1\nrules: []\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveRules("project", encode(plain)); err != nil {
		t.Fatal(err)
	}
	if written, err := os.ReadFile(filepath.Join(repo, ".inkuna", "rules.yaml")); err != nil || !strings.Contains(string(written), "version: 1\n") {
		t.Fatalf("a document without shared stages must stay version 1 (%v):\n%s", err, written)
	}
}
