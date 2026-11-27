package rules

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// legacyDefault is the built-in rules as they were written before they used
// the built-in library, every stage inline.
func legacyDefault() Document {
	return Document{
		Version: 1,
		Rules: []Rule{
			{
				Name: "bugfix", When: Condition{Kinds: []domain.TaskKind{domain.TaskBugfix}},
				Then: Flow{Stages: []Stage{
					{ID: "profile", Kind: domain.StepScript},
					{ID: "reproduce", Title: "Reproduce and locate the defect", Kind: domain.StepAgent, Agent: "researcher", Permission: domain.PermissionReadOnly, DependsOn: []string{"profile"}},
					{ID: "fix", Title: "Implement the fix", Kind: domain.StepAgent, Agent: "auto", Permission: domain.PermissionWorktree, DependsOn: []string{"reproduce"}},
					{ID: "verify", Kind: domain.StepEvaluation, DependsOn: []string{"fix"}},
					{ID: "approval", Kind: domain.StepApproval, DependsOn: []string{"verify"}},
				}},
			},
			{
				Name: "read-only", When: Condition{Kinds: []domain.TaskKind{domain.TaskResearch, domain.TaskReview}},
				Then: Flow{Stages: []Stage{
					{ID: "profile", Kind: domain.StepScript},
					{ID: "worker", Title: "Investigate", Kind: domain.StepAgent, Agent: "auto", Permission: domain.PermissionReadOnly, DependsOn: []string{"profile"}},
					{ID: "approval", Kind: domain.StepApproval, DependsOn: []string{"worker"}},
				}},
			},
		},
		Default: &Flow{Stages: []Stage{
			{ID: "profile", Kind: domain.StepScript},
			{ID: "worker", Title: "Complete task", Kind: domain.StepAgent, Agent: "auto", DependsOn: []string{"profile"}},
			{ID: "verify", Kind: domain.StepEvaluation, DependsOn: []string{"worker"}},
			{ID: "approval", Kind: domain.StepApproval, DependsOn: []string{"verify"}},
		}},
	}
}

// writeLibrary writes a global library file and returns its path.
func writeLibrary(t *testing.T, directory, name, content string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The built-in rules use the built-in library, and plan every task kind
// exactly as they did when every stage was written inline, whether they are
// used directly or a file that leaves out its default flow takes theirs.
func TestTheBuiltInRulesPlanAsTheyDidBeforeTheyUsedTheLibrary(t *testing.T) {
	legacy, current := legacyDefault(), Default()
	if err := current.Validate(); err != nil {
		t.Fatalf("the built-in rules do not validate: %v", err)
	}
	if current.RequiredVersion() != current.Version {
		t.Fatalf("the built-in rules need version %d, declare %d", current.RequiredVersion(), current.Version)
	}
	for _, flow := range append([]Flow{*current.Default}, current.Rules[0].Then, current.Rules[1].Then) {
		if !slices.ContainsFunc(flow.Stages, func(stage Stage) bool { return stage.Use != "" }) {
			t.Fatalf("a built-in flow uses no shared stage: %+v", flow)
		}
	}
	withoutDefault := mustParse(t, "version: 1\nrules: []\n")
	if withoutDefault.RequiredVersion() != 1 {
		t.Fatalf("a version 1 file without a default flow needs version %d once the built-in one is filled in", withoutDefault.RequiredVersion())
	}
	now := time.Now()
	kinds := []domain.TaskKind{domain.TaskResearch, domain.TaskBugfix, domain.TaskImplementation, domain.TaskReview, domain.TaskDocumentation, domain.TaskSkillAuthoring}
	for _, rule := range []string{"bugfix", "read-only", DefaultName} {
		before, _ := legacy.Named(rule)
		after, _ := current.Named(rule)
		for _, kind := range kinds {
			want, wantErr := before.Workflow(task(kind), rule, domain.RuntimeClaude, now)
			got, gotErr := after.Workflow(task(kind), rule, domain.RuntimeClaude, now)
			if wantErr != nil || gotErr != nil || !reflect.DeepEqual(withoutOrigins(got), want) {
				t.Errorf("rule %s plans a %s task differently: %v / %v\n got %+v\nwant %+v", rule, kind, gotErr, wantErr, got.Steps, want.Steps)
			}
		}
	}
	filled, _ := withoutDefault.Named(DefaultName)
	want, _ := legacy.Default.Workflow(task(domain.TaskImplementation), DefaultName, domain.RuntimeClaude, now)
	got, err := filled.Workflow(task(domain.TaskImplementation), DefaultName, domain.RuntimeClaude, now)
	if err != nil || !reflect.DeepEqual(withoutOrigins(got), want) {
		t.Errorf("a file without a default flow plans the built-in one differently: %v", err)
	}
	// Only where each step came from is new: the shared ones say so.
	origins := make(map[string]*domain.StepOrigin)
	for _, step := range got.Steps {
		origins[step.ID] = step.From
	}
	if origins["worker"] != nil || origins["profile"] == nil || origins["profile"].Stage != "profile" || origins["profile"].Scope != ScopeBuiltin ||
		origins["approval"] == nil || !slices.Equal(origins["approval"].Groups, []string{"verify-and-approve"}) {
		t.Errorf("planned origins = profile %+v, worker %+v, approval %+v", origins["profile"], origins["worker"], origins["approval"])
	}
}

// withoutOrigins is a planned workflow without where its steps came from,
// to compare it with one planned from stages written inline.
func withoutOrigins(workflow domain.WorkflowSpec) domain.WorkflowSpec {
	workflow.Steps = slices.Clone(workflow.Steps)
	for index := range workflow.Steps {
		workflow.Steps[index].From = nil
	}
	return workflow
}

// A global definition overrides a built-in one, and a repository's own
// overrides a global one, by name, including inside a group from a lower
// scope that uses the name.
func TestARepositoryOverridesAGlobalStageWhichOverridesABuiltInOne(t *testing.T) {
	directory := t.TempDir()
	file := writeLibrary(t, directory, "checks.yaml", "version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [make, check]\n")
	library := LoadLibrary([]string{directory, filepath.Join(directory, "missing")})
	if len(library.Problems) > 0 {
		t.Fatalf("problems: %+v", library.Problems)
	}

	// The built-in rules, with no rules file, take the global verify, and
	// the group that uses it does too.
	global := Default().WithLibrary(library)
	verify := stageByID(t, named(t, global, DefaultName), "verify")
	if !reflect.DeepEqual(verify.Commands, [][]string{{"make", "check"}}) {
		t.Fatalf("built-in flow verify = %v, want the global commands", verify.Commands)
	}
	if verify.From == nil || verify.From.Scope != ScopeGlobal || verify.From.File != file || !slices.Equal(verify.From.Groups, []string{"verify-and-approve"}) {
		t.Fatalf("verify provenance = %+v", verify.From)
	}
	approval := stageByID(t, named(t, global, DefaultName), "approval")
	if approval.From == nil || approval.From.Scope != ScopeBuiltin {
		t.Fatalf("approval provenance = %+v, want built-in", approval.From)
	}

	// A repository that defines verify keeps it to its own flows.
	own, err := ParseWith("version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [task, ci]\nrules: []\ndefault:\n  stages:\n    - use: profile\n    - id: worker\n      kind: agent\n      agent: auto\n      dependsOn: [profile]\n    - use: verify-and-approve\n      dependsOn: [worker]\n", library)
	if err != nil {
		t.Fatal(err)
	}
	verify = stageByID(t, named(t, own, DefaultName), "verify")
	if !reflect.DeepEqual(verify.Commands, [][]string{{"task", "ci"}}) || verify.From.Scope != ScopeRepository || verify.From.File != "" {
		t.Fatalf("repository verify = %v from %+v", verify.Commands, verify.From)
	}
	if other := stageByID(t, named(t, global, DefaultName), "verify"); !reflect.DeepEqual(other.Commands, [][]string{{"make", "check"}}) {
		t.Fatalf("another repository's verify changed: %v", other.Commands)
	}

	listed := library.Effective(&own)
	marks := map[string]bool{}
	for _, entry := range listed {
		if entry.Name == "verify" {
			marks[entry.Scope] = entry.Overridden
		}
	}
	if !reflect.DeepEqual(marks, map[string]bool{ScopeBuiltin: true, ScopeGlobal: true, ScopeRepository: false}) {
		t.Fatalf("verify overridden marks = %v", marks)
	}
}

// Two global files defining one name is a problem when the library loads,
// and a use of the name fails naming both files, even though a built-in
// definition has the name too; a repository's own definition still wins.
func TestADuplicateGlobalNameIsAProblemAndCannotBeUsed(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	one := writeLibrary(t, first, "a.yaml", "version: 2\nstages:\n  verify:\n    kind: evaluation\n")
	two := writeLibrary(t, second, "b.yaml", "version: 2\ngroups:\n  verify:\n    stages:\n      - use: approval\n")
	library := LoadLibrary([]string{first, second})
	if len(library.Problems) != 1 || library.Problems[0].File != two || !strings.Contains(library.Problems[0].Message, one) {
		t.Fatalf("problems = %+v", library.Problems)
	}
	_, err := ParseWith("version: 2\nrules: []\ndefault:\n  stages:\n    - use: verify\n    - use: approval\n      dependsOn: [verify]\n", library)
	if err == nil || !strings.Contains(err.Error(), one) || !strings.Contains(err.Error(), two) || !strings.Contains(err.Error(), `entry 1 (use "verify")`) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := LoadPathWith(filepath.Join(t.TempDir(), "rules.yaml"), library); err == nil || !strings.Contains(err.Error(), "more than one global stage library file") {
		t.Fatalf("the built-in rules used a duplicate name: %v", err)
	}
	if _, err := ParseWith("version: 2\nstages:\n  verify:\n    kind: evaluation\nrules: []\ndefault:\n  stages:\n    - use: verify\n    - use: approval\n      dependsOn: [verify]\n", library); err != nil {
		t.Fatalf("a repository definition should settle the name: %v", err)
	}
	for _, entry := range library.Effective(nil) {
		if entry.Scope == ScopeGlobal && !entry.Ambiguous {
			t.Fatalf("%s in %s is not marked ambiguous", entry.Name, entry.File)
		}
	}
}

// A library file that does not parse is reported and adds nothing, while the
// files beside it still load; a mistake in a definition is reported too.
func TestMalformedLibraryFilesAreReported(t *testing.T) {
	directory := t.TempDir()
	cases := map[string]string{
		"bad-yaml.yaml":    "version: 2\nstages: [\n",
		"unknown-key.yaml": "version: 2\nstages:\n  lint:\n    kind: evaluation\n    command: [x]\n",
		"with-rules.yaml":  "version: 2\nrules: []\n",
		"no-version.yaml":  "stages:\n  lint:\n    kind: evaluation\n",
		"version-1.yaml":   "version: 1\nstages:\n  lint:\n    kind: evaluation\n",
		"newer.yaml":       "version: 99\nwhatever: true\n",
		"bad-stage.yaml":   "version: 2\nstages:\n  writer:\n    kind: agent\n",
		"bad-ref.yaml":     "version: 2\ngroups:\n  gate:\n    stages:\n      - use: lint-everything\n",
	}
	for name, content := range cases {
		writeLibrary(t, directory, name, content)
	}
	writeLibrary(t, directory, "good.yaml", "version: 2\nstages:\n  docs:\n    kind: evaluation\n    commands:\n      - [task, docs]\n")
	writeLibrary(t, directory, "notes.md", "not a library")
	library := LoadLibrary([]string{directory})
	reported := map[string]string{}
	for _, problem := range library.Problems {
		reported[filepath.Base(problem.File)] = problem.Message
	}
	want := map[string]string{
		"bad-yaml.yaml":    "parse stage library",
		"unknown-key.yaml": "field command not found",
		"with-rules.yaml":  "field rules not found",
		"no-version.yaml":  "unsupported rules version 0",
		"version-1.yaml":   "needs rules version 2",
		"newer.yaml":       "newer than this build",
		"bad-stage.yaml":   `agent stage "writer" needs an agent`,
		"bad-ref.yaml":     `no shared stage or group is named "lint-everything"`,
	}
	for file, fragment := range want {
		if !strings.Contains(reported[file], fragment) {
			t.Errorf("%s: problem %q, want it to mention %q", file, reported[file], fragment)
		}
	}
	if len(reported) != len(want) {
		t.Errorf("problems = %v", reported)
	}
	if _, err := library.Resolve("docs"); err != nil {
		t.Fatalf("the good file did not load beside the bad ones: %v", err)
	}
}

// Planning with a broken reference fails naming the rule, the reference, and
// the library file it is written in.
func TestABrokenReferenceInAGlobalGroupNamesTheRuleTheReferenceAndTheFile(t *testing.T) {
	directory := t.TempDir()
	file := writeLibrary(t, directory, "gates.yaml", "version: 2\ngroups:\n  gate:\n    stages:\n      - use: lint\n      - use: approval\n        dependsOn: [lint]\n")
	library := LoadLibrary([]string{directory})
	_, err := ParseWith("version: 2\nrules:\n  - name: docs\n    when: {}\n    then:\n      stages:\n        - use: gate\n", library)
	if err == nil {
		t.Fatal("a broken reference parsed")
	}
	for _, fragment := range []string{`rule "docs"`, `group "gate" in ` + file, `entry 1 (use "lint")`, `no shared stage or group is named "lint"`} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not mention %q", err, fragment)
		}
	}
	// A shared stage that fails its checks is refused, not used without what
	// was wrong with it, and the file's other definitions still are.
	ordered := writeLibrary(t, directory, "ordered.yaml", "version: 2\nstages:\n  ordered:\n    kind: evaluation\n    dependsOn: [first]\n  fine:\n    kind: evaluation\n")
	library = LoadLibrary([]string{directory})
	_, err = ParseWith("version: 2\nrules: []\ndefault:\n  stages:\n    - id: first\n      kind: script\n    - use: ordered\n    - use: approval\n      dependsOn: [ordered]\n", library)
	if err == nil || !strings.Contains(err.Error(), `shared stage "ordered" in `+ordered+` cannot be used until it is fixed`) || !strings.Contains(err.Error(), "sets dependsOn") {
		t.Fatalf("a use of a broken shared stage must be refused: %v", err)
	}
	if _, err := library.Resolve("fine"); err != nil {
		t.Fatalf("a good definition beside a broken one must stay usable: %v", err)
	}
	if err := os.Remove(ordered); err != nil {
		t.Fatal(err)
	}
	// An expanded stage that fails its own checks names its file too.
	writeLibrary(t, directory, "agents.yaml", "version: 2\nstages:\n  lint:\n    kind: evaluation\n    commands:\n      - [sh, -c, lint]\n")
	_, err = ParseWith("version: 2\nrules: []\ndefault:\n  stages:\n    - use: gate\n", LoadLibrary([]string{directory}))
	if err == nil || !strings.Contains(err.Error(), filepath.Join(directory, "agents.yaml")) {
		t.Fatalf("err = %v", err)
	}
}

// A global group may use a stage that only some repositories define: the
// library reports it without refusing the group, a repository that defines
// the stage plans with it, and one that does not is refused, naming the rule,
// the reference, and the file.
func TestAGlobalGroupCanUseAStageARepositoryDefines(t *testing.T) {
	directory := t.TempDir()
	file := writeLibrary(t, directory, "gates.yaml", "version: 2\ngroups:\n  gate:\n    stages:\n      - use: deploy\n      - use: approval\n        dependsOn: [deploy]\n")
	library := LoadLibrary([]string{directory})
	if len(library.Problems) != 1 || library.Problems[0].File != file || !strings.Contains(library.Problems[0].Message, `no shared stage or group is named "deploy"`) {
		t.Fatalf("problems = %v, want the unresolved reference reported", library.Problems)
	}
	if _, err := library.Resolve("gate"); err != nil {
		t.Fatalf("a group whose reference a repository may define was refused: %v", err)
	}
	document, err := ParseWith("version: 2\nstages:\n  deploy:\n    kind: script\nrules: []\ndefault:\n  stages:\n    - use: gate\n", library)
	if err != nil {
		t.Fatalf("a repository defining the stage cannot use the group: %v", err)
	}
	deploy := stageByID(t, named(t, document, DefaultName), "deploy")
	if deploy.Kind != domain.StepScript || deploy.From == nil || deploy.From.Scope != ScopeRepository || !slices.Equal(deploy.From.Groups, []string{"gate"}) {
		t.Fatalf("deploy = %+v, want the repository's stage inside the global group", deploy)
	}
	_, err = ParseWith("version: 2\nrules:\n  - name: ship\n    when: {}\n    then:\n      stages:\n        - use: gate\n", library)
	if err == nil {
		t.Fatal("a repository without the stage used the group")
	}
	for _, fragment := range []string{`rule "ship"`, `group "gate" in ` + file, `entry 1 (use "deploy")`, `no shared stage or group is named "deploy"`} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not mention %q", err, fragment)
		}
	}
}

// Uses lists what a rule's flow uses, through its groups, and a definition's
// digest changes with what it says and not with where it is written.
func TestUsesListsEveryDefinitionAFlowExpandsAndDigestsThem(t *testing.T) {
	names := func(definitions []Definition) []string {
		listed := make([]string, len(definitions))
		for index, definition := range definitions {
			listed[index] = definition.Scope + ":" + definition.Name
		}
		return listed
	}
	document := Default()
	for rule, want := range map[string][]string{
		DefaultName: {"builtin:approval", "builtin:profile", "builtin:verify", "builtin:verify-and-approve"},
		"READ-ONLY": {"builtin:approval", "builtin:profile"},
	} {
		used, err := document.Uses(rule)
		if err != nil || !slices.Equal(names(used), want) {
			t.Errorf("Uses(%s) = %v, %v; want %v", rule, names(used), err, want)
		}
	}
	// A file without a default flow takes the built-in one expanded, and
	// still reports what it used.
	if used, err := mustParse(t, "version: 1\nrules: []\n").Uses(DefaultName); err != nil || len(used) != 4 {
		t.Errorf("Uses of a filled-in default = %v, %v", names(used), err)
	}
	if _, err := document.Uses("missing"); err == nil {
		t.Error("Uses of an unknown rule succeeded")
	}

	directory := t.TempDir()
	writeLibrary(t, directory, "a.yaml", "version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [make, check]\n")
	before, _ := LoadLibrary([]string{directory}).Resolve("verify")
	if err := os.Rename(filepath.Join(directory, "a.yaml"), filepath.Join(directory, "b.yaml")); err != nil {
		t.Fatal(err)
	}
	moved, _ := LoadLibrary([]string{directory}).Resolve("verify")
	writeLibrary(t, directory, "b.yaml", "version: 2\nstages:\n  verify:\n    kind: evaluation\n    commands:\n      - [make, check, -k]\n")
	edited, _ := LoadLibrary([]string{directory}).Resolve("verify")
	if before.Digest() != moved.Digest() || before.Digest() == edited.Digest() {
		t.Fatalf("digests: before %s, moved %s, edited %s", before.Digest(), moved.Digest(), edited.Digest())
	}
}

// stageByID is the stage of flow with id, failing the test when there is
// none.
func stageByID(t *testing.T, flow Flow, id string) Stage {
	t.Helper()
	for _, stage := range flow.Stages {
		if stage.ID == id {
			return stage
		}
	}
	t.Fatalf("no stage %q in %v", id, shape(flow))
	return Stage{}
}
