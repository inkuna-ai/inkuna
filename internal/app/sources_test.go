package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/skill"
	"github.com/inkuna-ai/inkuna/internal/sources"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// fakeSource answers with its snapshot, or its error, or not at all while
// hang is set, and records what it was asked.
type fakeSource struct {
	mu       sync.Mutex
	snapshot extension.Snapshot
	err      error
	hang     bool
	asked    []extension.Project
	who      []extension.Principal
}

func (source *fakeSource) Snapshot(ctx context.Context, principal extension.Principal, project extension.Project) (extension.Snapshot, error) {
	source.mu.Lock()
	source.asked = append(source.asked, project)
	source.who = append(source.who, principal)
	snapshot, err, hang := source.snapshot, source.err, source.hang
	source.mu.Unlock()
	if hang {
		<-ctx.Done()
		return extension.Snapshot{}, ctx.Err()
	}
	return snapshot, err
}

func (source *fakeSource) set(snapshot extension.Snapshot, err error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.snapshot, source.err = snapshot, err
}

func (source *fakeSource) setHang(hang bool) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.hang = hang
}

const (
	sharedAgent = "---\nname: api-reviewer\ndescription: Reviews APIs.\npermission: read-only\nskills: [api-style]\n---\n\nReview the API.\n"
	sharedSkill = "---\nname: api-style\ndescription: How this team writes APIs.\n---\n\nUse plural nouns.\n"
)

func definition(kind extension.DefinitionKind, name string, mode extension.DefinitionMode, files map[string][]byte) extension.Definition {
	return extension.Definition{Kind: kind, Name: name, Mode: mode, Scope: "team:platform", Version: name + "-1", Files: files}
}

// catalogSnapshot offers an agent and the skill it names.
func catalogSnapshot(version string) extension.Snapshot {
	return extension.Snapshot{Version: version, Definitions: []extension.Definition{
		definition(extension.KindAgent, "api-reviewer", extension.ModeCatalog, map[string][]byte{"api-reviewer.md": []byte(sharedAgent)}),
		definition(extension.KindSkill, "api-style", extension.ModeCatalog, map[string][]byte{"SKILL.md": []byte(sharedSkill)}),
	}}
}

// withSource registers source, as acme's only definition source, on a
// service built without New, with an identity provider naming ada.
func withSource(t *testing.T, service *Service, source extension.DefinitionSource) {
	t.Helper()
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{
		name: "acme", identities: []extension.IdentityProvider{&fakeIdentity{principal: ada}}, sources: []extension.DefinitionSource{source},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service.extensions = registry
}

func sourceRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	for _, args := range [][]string{{"init"}, {"commit", "--allow-empty", "-m", "initial"}, {"remote", "add", "origin", "https://TOKEN@github.com/acme/api.git"}} {
		if err := runGit(repository, args...); err != nil {
			t.Fatal(err)
		}
	}
	return repository
}

func TestANilDefinitionSourceStopsStartup(t *testing.T) {
	_, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", sources: []extension.DefinitionSource{nil}}})
	if err == nil || err.Error() != `register extension "acme": its definition source is nil` {
		t.Fatalf("registerExtensions = %v", err)
	}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", sources: []extension.DefinitionSource{&fakeSource{}, &fakeSource{}}}})
	if err != nil || len(registry.sources) != 2 || registry.sources[0].name != "acme/source" || registry.sources[1].name != "acme/source-2" {
		t.Fatalf("registry = %+v, %v", registry, err)
	}
}

func TestACatalogSourcesDefinitionsAreOfferedAndAddedWithWhatTheyBring(t *testing.T) {
	repository := sourceRepository(t)
	service := workflowTestService(t, repository, &workflowRuntime{})
	source := &fakeSource{snapshot: catalogSnapshot("v1")}
	withSource(t, service, source)

	listing, err := service.CatalogItems("project")
	if err != nil {
		t.Fatal(err)
	}
	var offered []string
	for _, entry := range listing.Items {
		if entry.Item.Source != "" {
			offered = append(offered, entry.Item.Source+" "+entry.Item.Name+" "+entry.Item.Scope)
		}
	}
	if want := []string{"acme/source api-reviewer team:platform", "acme/source api-style team:platform"}; !slices.Equal(offered, want) {
		t.Fatalf("offered %v, want %v", offered, want)
	}
	if len(listing.Sources) != 1 || listing.Sources[0].Version != "v1" || listing.Sources[0].Definitions[0].Status != sources.StatusOffered {
		t.Fatalf("listing.Sources = %+v", listing.Sources)
	}
	// The source is asked for the person, about the project by its origin,
	// without the token the URL held.
	if source.asked[0] != (extension.Project{Name: "Project", Remote: "https://github.com/acme/api.git"}) || source.who[0].ID != ada.ID {
		t.Fatalf("the source was asked %+v for %+v", source.asked, source.who)
	}

	if _, err := service.AddCatalogItem("project", "api-reviewer", "acme/source", true); err == nil || !strings.Contains(err.Error(), "shares it for this project") {
		t.Fatalf("a global add of a source's agent: %v", err)
	}
	if _, err := service.AddCatalogItem("project", "api-reviewer", "acme/other", false); err == nil || !strings.Contains(err.Error(), `"acme/other" offers nothing`) {
		t.Fatalf("an add from a source the build lacks: %v", err)
	}
	result, err := service.AddCatalogItem("project", "api-reviewer", "acme/source", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 2 || result.Added[1].Path != filepath.Join(repository, ".inkuna", "skills", "api-style") {
		t.Fatalf("AddCatalogItem = %+v", result)
	}
	packages, err := skill.Discover(filepath.Join(repository, ".inkuna", "skills"))
	if err != nil || len(packages) != 1 || packages[0].ID != "api-style" {
		t.Fatalf("the repository's skills = %+v, %v", packages, err)
	}
	registry, _, err := service.agentRegistry(repository)
	if err != nil {
		t.Fatal(err)
	}
	if reviewer, ok := registry.Get("api-reviewer"); !ok || !slices.Equal(reviewer.Skills, []string{"api-style"}) {
		t.Fatalf("the repository's agent = %+v, %v", reviewer, ok)
	}
}

func TestEachModeIsHandledSoNothingWeakerHappens(t *testing.T) {
	repository := sourceRepository(t)
	service := workflowTestService(t, repository, &workflowRuntime{})
	source := &fakeSource{}
	withSource(t, service, source)
	rule := map[string][]byte{"release.yaml": []byte("version: 2\nrules: []\n")}

	// A shared definition is listed as not applied and ignored: the plan
	// goes ahead and says so.
	source.set(extension.Snapshot{Version: "v2", Definitions: []extension.Definition{definition(extension.KindRule, "release", extension.ModeShared, rule)}}, nil)
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository", Kind: domain.TaskResearch})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(plan.Notes, func(note string) bool {
		return strings.Contains(note, `"acme/source" shares rule "release", which this build does not apply`)
	}) {
		t.Fatalf("the plan's notes = %q", plan.Notes)
	}
	if len(plan.Workflow.Sources) != 1 || plan.Workflow.Sources[0].Source != "acme/source" || plan.Workflow.Sources[0].Version != "v2" || plan.Workflow.Sources[0].Cached {
		t.Fatalf("the plan records %+v", plan.Workflow.Sources)
	}
	stored, err := service.store.Workflow(context.Background(), plan.Workflow.ID)
	if err != nil || len(stored.Sources) != 1 || stored.Sources[0].Version != "v2" {
		t.Fatalf("the stored plan records %+v, %v", stored.Sources, err)
	}
	listing, err := service.CatalogItems("project")
	if err != nil || listing.Sources[0].Definitions[0].Status != sources.StatusNotApplied {
		t.Fatalf("listing.Sources = %+v, %v", listing.Sources, err)
	}

	// Enforced, or a mode or kind Go does not know, refuses every plan in
	// the project, naming the definition.
	for _, tc := range []struct {
		name       string
		definition extension.Definition
		want       string
	}{
		{"enforced", definition(extension.KindRule, "release", extension.ModeEnforced, rule), `definition source "acme/source" enforces rule "release" for team:platform, which this build does not apply`},
		{"an unknown mode", definition(extension.KindRule, "release", "pinned", rule), `shares rule "release" for team:platform in the mode "pinned", which this build does not know`},
		{"an unknown kind", definition("gate", "security", extension.ModeShared, nil), `shares "security", a kind of definition this build does not know`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source.set(extension.Snapshot{Version: "v3", Definitions: []extension.Definition{tc.definition}}, nil)
			_, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository", Kind: domain.TaskResearch})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "so plans in this project are refused") {
				t.Fatalf("PlanWorkflow = %v, want a refusal saying %q", err, tc.want)
			}
			listing, err := service.CatalogItems("project")
			if err != nil || listing.Sources[0].Definitions[0].Status != sources.StatusRefuses {
				t.Fatalf("listing.Sources = %+v, %v", listing.Sources, err)
			}
		})
	}

	// A plan made before the source came to enforce something is refused
	// at approval too.
	source.set(extension.Snapshot{Version: "v4"}, nil)
	plan, err = service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository", Kind: domain.TaskResearch})
	if err != nil {
		t.Fatal(err)
	}
	source.set(extension.Snapshot{Version: "v5", Definitions: []extension.Definition{definition(extension.KindRule, "release", extension.ModeEnforced, rule)}}, nil)
	encoded, err := json.Marshal(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), `enforces rule "release"`) {
		t.Fatalf("StartWorkflow = %v, want the enforced rule's refusal", err)
	}
	if state, err := service.store.WorkflowState(context.Background(), plan.Workflow.ID); err != nil || state != "planned" {
		t.Fatalf("a refused approval left the plan %q, %v", state, err)
	}
}

func TestACatalogDefinitionThatFailsItsChecksIsReportedAndNotOffered(t *testing.T) {
	service := workflowTestService(t, sourceRepository(t), &workflowRuntime{})
	snapshot := catalogSnapshot("v1")
	// The agent's file names another agent.
	snapshot.Definitions[0].Files = map[string][]byte{"api-reviewer.md": []byte(strings.Replace(sharedAgent, "name: api-reviewer", "name: someone-else", 1))}
	withSource(t, service, &fakeSource{snapshot: snapshot})
	listing, err := service.CatalogItems("project")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range listing.Items {
		if entry.Item.Name == "api-reviewer" {
			t.Fatalf("a definition that fails its checks is offered: %+v", entry)
		}
	}
	failed := listing.Sources[0].Definitions[0]
	if failed.Status != sources.StatusFails || !strings.Contains(failed.Reason, `must match file name "api-reviewer"`) {
		t.Fatalf("the failing definition is reported as %+v", failed)
	}
	if offered := listing.Sources[0].Definitions[1]; offered.Status != sources.StatusOffered {
		t.Fatalf("the skill beside it = %+v", offered)
	}
	// A catalog definition is only offered, so one that fails refuses no
	// plan.
	if _, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository", Kind: domain.TaskResearch}); err != nil {
		t.Fatal(err)
	}
}

func TestAnUnreachableSourceIsReadFromItsLastSnapshot(t *testing.T) {
	previous := sourceTimeout
	sourceTimeout = 50 * time.Millisecond
	t.Cleanup(func() { sourceTimeout = previous })

	t.Run("without one", func(t *testing.T) {
		service := workflowTestService(t, sourceRepository(t), &workflowRuntime{})
		withSource(t, service, &fakeSource{err: errors.New("the definitions server is down")})
		plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository", Kind: domain.TaskResearch})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(plan.Notes, func(note string) bool {
			return strings.Contains(note, `"acme/source" could not be reached (the definitions server is down) and has no snapshot of this project kept, so it adds nothing`)
		}) || len(plan.Workflow.Sources) != 0 {
			t.Fatalf("the plan's notes = %q, sources %+v", plan.Notes, plan.Workflow.Sources)
		}
		listing, err := service.CatalogItems("project")
		if err != nil || listing.Sources[0].Unreachable == "" || len(listing.Sources[0].Definitions) != 0 {
			t.Fatalf("listing.Sources = %+v, %v", listing.Sources, err)
		}
	})

	t.Run("with one", func(t *testing.T) {
		repository := sourceRepository(t)
		service := workflowTestService(t, repository, &workflowRuntime{})
		source := &fakeSource{snapshot: catalogSnapshot("v1")}
		withSource(t, service, source)
		if _, err := service.CatalogItems("project"); err != nil {
			t.Fatal(err)
		}
		if _, found, err := sources.Load(service.dataDir, "acme/source", repository); err != nil || !found {
			t.Fatalf("the snapshot was not kept: %v %v", found, err)
		}
		source.setHang(true)
		plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository", Kind: domain.TaskResearch})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(plan.Notes, func(note string) bool {
			return strings.Contains(note, `"acme/source" could not be reached (it did not answer within 50ms), so the plan used its snapshot from`)
		}) {
			t.Fatalf("the plan's notes = %q", plan.Notes)
		}
		if len(plan.Workflow.Sources) != 1 || plan.Workflow.Sources[0].Version != "v1" || !plan.Workflow.Sources[0].Cached {
			t.Fatalf("the plan records %+v", plan.Workflow.Sources)
		}
		// What the last snapshot offered can still be added.
		if _, err := service.AddCatalogItem("project", "api-style", "acme/source", false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(repository, ".inkuna", "skills", "api-style", "SKILL.md")); err != nil {
			t.Fatal(err)
		}

		// An error that says nothing is still an error: the kept snapshot
		// is used, not replaced by an empty one.
		source.setHang(false)
		source.set(extension.Snapshot{}, errors.New(""))
		plan, err = service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository", Kind: domain.TaskResearch})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Workflow.Sources) != 1 || plan.Workflow.Sources[0].Version != "v1" || !plan.Workflow.Sources[0].Cached || !slices.ContainsFunc(plan.Notes, func(note string) bool {
			return strings.Contains(note, "could not be reached (it failed without saying why)")
		}) {
			t.Fatalf("an empty error: the plan records %+v, notes %q", plan.Workflow.Sources, plan.Notes)
		}

		// An enforced definition in the last snapshot keeps refusing while
		// the source cannot be reached.
		source.set(extension.Snapshot{Version: "v2", Definitions: []extension.Definition{
			definition(extension.KindRule, "release", extension.ModeEnforced, map[string][]byte{"release.yaml": nil}),
		}}, nil)
		if _, err := service.CatalogItems("project"); err != nil {
			t.Fatal(err)
		}
		source.set(extension.Snapshot{}, errors.New("the definitions server is down"))
		if _, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository", Kind: domain.TaskResearch}); err == nil || !strings.Contains(err.Error(), `enforces rule "release"`) {
			t.Fatalf("PlanWorkflow = %v, want the cached enforced rule's refusal", err)
		}
	})
}

func TestABuildWithoutSourcesAsksNoOne(t *testing.T) {
	service := workflowTestService(t, sourceRepository(t), &workflowRuntime{})
	identity := &fakeIdentity{principal: ada}
	withIdentity(t, service, identity)
	if reading := service.readSources(context.Background(), mustProject(t, service)); len(reading.Reports) != 0 {
		t.Fatalf("readSources = %+v", reading)
	}
	listing, err := service.CatalogItems("project")
	if err != nil || listing.Sources != nil {
		t.Fatalf("listing = %+v, %v", listing, err)
	}
	if _, err := os.Stat(sources.Directory(service.dataDir)); !os.IsNotExist(err) {
		t.Fatalf("a build without sources kept snapshots: %v", err)
	}
}

func mustProject(t *testing.T, service *Service) storage.Project {
	t.Helper()
	project, err := service.store.Project(context.Background(), "project")
	if err != nil {
		t.Fatal(err)
	}
	return project
}
