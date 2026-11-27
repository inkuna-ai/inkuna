package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// findDefinition is the listed definition with name in scope.
func findDefinition(t *testing.T, view StageLibraryView, name, scope string) LibraryDefinition {
	t.Helper()
	for _, definition := range view.Definitions {
		if definition.Name == name && definition.Scope == scope {
			return definition
		}
	}
	t.Fatalf("no %s definition %q in %+v", scope, name, view.Definitions)
	return LibraryDefinition{}
}

// The library lists every scope with what overrides what, which flows use
// each definition, and what a built-in one cannot do, for the edited rules
// rather than the saved ones.
func TestStageLibraryListsEveryScopeAndWhereEachIsUsed(t *testing.T) {
	service, directory := libraryService(t)
	writeFile(t, filepath.Join(directory, "checks.yaml"), "version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [task, ci]\n")
	document := rules.Default()
	document.Stages = map[string]rules.Stage{"approval": {Kind: domain.StepApproval, Review: true}}
	document.Rules = append(document.Rules, rules.Rule{Name: "broken", Then: rules.Flow{Stages: []rules.Stage{{Use: "missing"}, {Use: "approval"}}}})

	view, err := service.StageLibrary("project", encodeRules(t, document))
	if err != nil {
		t.Fatal(err)
	}
	builtin := findDefinition(t, view, "verify", rules.ScopeBuiltin)
	if !builtin.Overridden || builtin.ReadOnly == "" || len(builtin.UsedBy) > 0 {
		t.Fatalf("built-in verify = %+v", builtin)
	}
	global := findDefinition(t, view, "verify", rules.ScopeGlobal)
	if global.Overridden || global.ReadOnly != "" || !slices.Equal(global.UsedBy, []string{"bugfix", "feature", "documentation", "default"}) {
		t.Fatalf("global verify = %+v", global)
	}
	repository := findDefinition(t, view, "approval", rules.ScopeRepository)
	if repository.Kind != "stage" || !strings.HasSuffix(repository.File, filepath.FromSlash(".inkuna/rules.yaml")) || !slices.Equal(repository.UsedBy, []string{"bugfix", "feature", "documentation", "read-only", "broken", "default"}) {
		t.Fatalf("repository approval = %+v", repository)
	}
	if group := findDefinition(t, view, "verify-and-approve", rules.ScopeBuiltin); group.Kind != "group" || group.Group == nil {
		t.Fatalf("built-in group = %+v", group)
	}
	if !slices.ContainsFunc(view.Overridable, func(field rules.OverridableField) bool { return field.Name == "commands" }) {
		t.Fatalf("overridable fields = %+v", view.Overridable)
	}
}

// A refused save says which flow entry, or which shared stage, it is
// about, whether validation or the agent check refused it.
func TestCheckRulesLocatesTheEntryAMistakeIsIn(t *testing.T) {
	service, _ := libraryService(t)
	flow := func(stages ...rules.Stage) rules.Document {
		document := rules.Default()
		document.Rules = []rules.Rule{{Name: "first", Then: rules.Flow{Stages: []rules.Stage{{Use: "approval"}}}}, {Name: "docs", Then: rules.Flow{Stages: stages}}}
		return document
	}
	write := rules.Stage{ID: "write", Kind: domain.StepAgent, Agent: "auto"}
	cases := map[string]struct {
		document rules.Document
		want     RulesProblem
	}{
		"valid": {flow(write, rules.Stage{Use: "approval", DependsOn: []string{"write"}}), RulesProblem{}},
		"a use of an unknown name": {
			flow(write, rules.Stage{Use: "verfy"}, rules.Stage{Use: "approval"}),
			RulesProblem{Message: `rule "docs": entry 2 (use "verfy"): no shared stage or group is named "verfy"`, Flow: &FlowLocation{Rule: 1, Entry: 1}},
		},
		"a stage inside a used group": {
			flow(write, rules.Stage{Use: "verify-and-approve", DependsOn: []string{"nothing"}}),
			RulesProblem{Message: `rule "docs": stage "verify" (from built-in shared stage "verify" in group "verify-and-approve") depends on "nothing" which must be declared earlier`, Flow: &FlowLocation{Rule: 1, Entry: 1}},
		},
		"an override naming an unknown agent": {
			flow(write, rules.Stage{Use: "approval"}, rules.Stage{ID: "late", Kind: domain.StepAgent, Agent: "nobody"}, rules.Stage{ID: "end", Kind: domain.StepApproval}),
			RulesProblem{Message: `rule "docs": stage "late" names unknown agent "nobody"`, Flow: &FlowLocation{Rule: 1, Entry: 2}},
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			problem, err := service.CheckRules("project", encodeRules(t, test.document))
			if err != nil {
				t.Fatal(err)
			}
			if problem.Message != test.want.Message || (problem.Flow == nil) != (test.want.Flow == nil) || (problem.Flow != nil && *problem.Flow != *test.want.Flow) {
				t.Fatalf("CheckRules = %+v (flow %+v), want %+v (flow %+v)", problem, problem.Flow, test.want, test.want.Flow)
			}
		})
	}

	shared := rules.Default()
	shared.Stages = map[string]rules.Stage{"review": {Kind: domain.StepAgent, Agent: "nobody"}}
	problem, err := service.CheckRules("project", encodeRules(t, shared))
	if err != nil || problem.Definition != "review" || problem.Flow != nil || !strings.Contains(problem.Message, `shared stage "review": stage "review" names unknown agent "nobody"`) {
		t.Fatalf("CheckRules = %+v, %v", problem, err)
	}
}

// The preview is the edited rules expanded as planning would expand them,
// and rules that would not save are refused.
func TestExpandRulesPreviewsTheEditedRules(t *testing.T) {
	service, _ := libraryService(t)
	document := rules.Default()
	expanded, err := service.ExpandRules("project", encodeRules(t, document))
	if err != nil {
		t.Fatal(err)
	}
	stages := expanded.Default.Stages
	last := stages[len(stages)-1]
	if expanded.Stages != nil || last.ID != "approval" || last.From == nil || !slices.Equal(last.From.Groups, []string{"verify-and-approve"}) || last.From.Scope != rules.ScopeBuiltin {
		t.Fatalf("expanded default flow = %+v (last from %+v)", stages, last.From)
	}
	document.Default.Stages = document.Default.Stages[:1]
	if _, err := service.ExpandRules("project", encodeRules(t, document)); err == nil || !strings.Contains(err.Error(), "default flow") {
		t.Fatalf("an invalid document must be refused: %v", err)
	}
}

// Editing a global definition first says which registered repositories
// use it: one whose own definition replaces it does not, and one whose
// rules do not load is listed with why.
func TestGlobalDefinitionUsersListsTheRepositoriesUsingIt(t *testing.T) {
	service, directory := libraryService(t)
	file := filepath.Join(directory, "checks.yaml")
	writeFile(t, file, "version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [task, ci]\n")
	addProject := func(id, name, rulesContent string) {
		t.Helper()
		repository := t.TempDir()
		if rulesContent != "" {
			writeFile(t, filepath.Join(repository, ".inkuna", "rules.yaml"), rulesContent)
		}
		if err := service.store.UpsertProject(context.Background(), storage.Project{ID: id, Name: name, Repository: repository, DefaultRuntime: domain.RuntimeCodex}); err != nil {
			t.Fatal(err)
		}
	}
	addProject("own", "Own verify", "version: 2\nstages:\n  verify:\n    kind: evaluation\nrules: []\ndefault:\n  stages:\n    - use: verify\n    - use: approval\n      dependsOn: [verify]\n")
	addProject("broken", "Broken", "version: 2\nrules: [\n")
	addProject("approval-only", "Approval only", "version: 2\nrules: []\ndefault:\n  stages:\n    - use: approval\n")

	users, err := service.GlobalDefinitionUsers(file, "verify")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, user := range users {
		switch {
		case user.Problem != "":
			got = append(got, user.Project+": problem")
		default:
			got = append(got, user.Project+": "+strings.Join(user.Flows, ","))
		}
	}
	slices.Sort(got)
	if want := []string{"Broken: problem", "Project: bugfix,feature,documentation,default"}; !slices.Equal(got, want) {
		t.Fatalf("users = %v, want %v", got, want)
	}
}

// A global definition is saved to its file and used by the next plan; a
// built-in one is refused.
func TestSaveGlobalDefinitionWritesTheFileAndRefusesABuiltInOne(t *testing.T) {
	service, directory := libraryService(t)
	file := filepath.Join(directory, "checks.yaml")
	writeFile(t, file, "version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [task, ci]\n")
	if err := service.SaveGlobalDefinition(file, "verify", `{"stage":{"id":"","kind":"evaluation","commands":[["task","deps"],["task","ci"]]}}`); err != nil {
		t.Fatal(err)
	}
	definition, err := service.stageLibrary().Resolve("verify")
	if err != nil || len(definition.Stage.Commands) != 2 {
		t.Fatalf("verify after saving = %+v, %v", definition.Stage, err)
	}
	err = service.SaveGlobalDefinition("", "approval", `{"stage":{"id":"","kind":"approval","review":true}}`)
	if err == nil || !strings.Contains(err.Error(), `built-in stage "approval" is read-only; copy it to a repository's rules to change it there`) {
		t.Fatalf("saving a built-in definition must be refused: %v", err)
	}
	if err := service.SaveGlobalDefinition(file, "verify", `{"stage":{"id":"","kind":"evaluation"},"extra":1}`); err == nil || !strings.Contains(err.Error(), "decode definition") {
		t.Fatalf("an unknown field must be refused: %v", err)
	}
}

// writeFile writes content to path, creating its directory.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
