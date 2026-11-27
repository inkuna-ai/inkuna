package catalog

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	"github.com/inkuna-ai/inkuna/internal/planner"
	"github.com/inkuna-ai/inkuna/internal/rules"
)

// The catalog that ships must load, which checks every item's form, names,
// and needs, and everything it adds must plan. Loading fails the build on
// its own; this adds the planning.
func TestShippedCatalogPlans(t *testing.T) {
	catalog, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range planProblems(t, catalog) {
		t.Error(problem)
	}
}

func TestPlanProblemsPassAWorkingCatalog(t *testing.T) {
	for _, problem := range planProblems(t, load(t)) {
		t.Error(problem)
	}
}

// A writable agent for implementation named before implementer would take
// every writable auto stage when the orchestrator is unavailable, in the
// built-in flows as much as in a catalog rule's.
func TestPlanProblemsFindAnAgentThatTakesWritableStagesFromImplementer(t *testing.T) {
	fsys := fixture()
	fsys["agents/builder.md"] = &fstest.MapFile{Data: []byte("---\nname: builder\ndescription: Builds.\npermission: worktree-write\ntaskKinds: [implementation]\n---\n\nBuild it.\n")}
	catalog, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	problems := planProblems(t, catalog)
	if !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, "with builder, want implementer")
	}) {
		t.Fatalf("problems = %q, want fallback staffing with builder reported", problems)
	}
}

// A read-only research agent named before researcher would take every
// research task's investigation when the orchestrator is unavailable,
// although no writable stage changes hands.
func TestPlanProblemsFindAnAgentThatTakesReadOnlyStagesFromItsBuiltIn(t *testing.T) {
	fsys := fixture()
	fsys["agents/analyst.md"] = &fstest.MapFile{Data: []byte("---\nname: analyst\ndescription: Analyses.\npermission: read-only\ntaskKinds: [research]\n---\n\nAnalyse it.\n")}
	catalog, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	problems := planProblems(t, catalog)
	if !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, "(research)") && strings.Contains(problem, "with analyst, want researcher")
	}) {
		t.Fatalf("problems = %q, want fallback staffing with analyst reported", problems)
	}
}

// A human-input stage may name the user as its agent: the user is no agent
// to add, so the rule needs nothing for it and plans. An agent stage naming
// the user does not plan, which the planning check reports.
func TestARuleMayNameTheUser(t *testing.T) {
	fsys := fixture()
	fsys["rules/ask-first.yaml"] = &fstest.MapFile{Data: []byte(`version: 2
rules:
  - name: ask-first
    description: Asks the person before fixing.
    when: {kind: [bugfix]}
    then:
      stages:
        - {id: ask, kind: human-input, agent: human, prompt: "What should the fix keep?"}
        - {id: fix, kind: agent, agent: auto, dependsOn: [ask]}
        - {use: approval, dependsOn: [fix]}
`)}
	catalog, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if rule, _ := catalog.Get("ask-first"); len(rule.Needs.Agents) != 0 {
		t.Fatalf("ask-first needs agents %v, want none", rule.Needs.Agents)
	}
	for _, problem := range planProblems(t, catalog) {
		t.Error(problem)
	}

	fsys["rules/ask-first.yaml"] = &fstest.MapFile{Data: []byte(`version: 2
rules:
  - name: ask-first
    description: Gives the work to the person.
    when: {kind: [bugfix]}
    then:
      stages:
        - {id: ask, kind: agent, agent: human}
        - {use: approval, dependsOn: [ask]}
`)}
	if catalog, err = Load(fsys); err != nil {
		t.Fatal(err)
	}
	if problems := planProblems(t, catalog); !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, "ask-first") && strings.Contains(problem, "does not plan")
	}) {
		t.Fatalf("problems = %q, want an agent stage naming the user reported", problems)
	}
}

func TestPlanProblemsFindARuleThatDoesNotPlan(t *testing.T) {
	fsys := fixture()
	// A stage skill must be one the repository has, and a catalog rule
	// cannot know that it does.
	fsys["rules/needs-skill.yaml"] = &fstest.MapFile{Data: []byte(`version: 2
rules:
  - name: needs-skill
    description: Asks for a skill.
    when: {kind: [bugfix]}
    then:
      stages:
        - {id: fix, kind: agent, agent: auto, skills: [go]}
        - {use: approval, dependsOn: [fix]}
`)}
	catalog, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if problems := planProblems(t, catalog); !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, "needs-skill") && strings.Contains(problem, "does not plan")
	}) {
		t.Fatalf("problems = %q, want needs-skill reported", problems)
	}
}

// planProblems adds each item, alone, to an empty repository, then plans
// every flow of the repository's rules, the built-in ones included, for
// each kind of task that reaches it: once with the agents the add brought
// and once with every catalog agent, as a repository that added them all
// would have. Fallback staffing must give every auto stage the agent the
// built-in agents alone would give it.
func planProblems(t *testing.T, catalog Catalog) []string {
	t.Helper()
	var problems []string
	everyAgent := agent.NewRegistry(agent.BuiltIn(), catalog.Agents())
	for _, item := range catalog.Items() {
		root := t.TempDir()
		target := Target{
			RulesPath:      filepath.Join(root, filepath.FromSlash(rules.RelativePath)),
			AgentDirectory: filepath.Join(root, ".inkuna", "agents"),
		}
		if _, err := catalog.Add(item.Name, target); err != nil {
			problems = append(problems, fmt.Sprintf("%s: add: %v", item.Name, err))
			continue
		}
		document, _, err := rules.Load(root)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: rules after the add: %v", item.Name, err))
			continue
		}
		added, err := agent.Build(root, nil)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: agents after the add: %v", item.Name, err))
			continue
		}
		for _, registry := range []agent.Registry{added, everyAgent} {
			problems = append(problems, planFlows(item.Name, document, registry)...)
		}
	}
	return problems
}

// planFlows plans each flow of document for every kind of task that
// reaches it, without an orchestrator. Fallback staffing must give each
// auto stage the agent it would give it with only the built-in agents: a
// catalog agent takes a stage only when a rule names it or the
// orchestrator picks it. Fallback takes the first specialist in name
// order, so one named before a built-in of its kind, such as a writable
// implementation agent before implementer, would take its stages.
func planFlows(item string, document rules.Document, registry agent.Registry) []string {
	builtins := agent.NewRegistry(agent.BuiltIn())
	every := []domain.TaskKind{domain.TaskResearch, domain.TaskBugfix, domain.TaskImplementation, domain.TaskReview, domain.TaskDocumentation, domain.TaskSkillAuthoring}
	names := []string{rules.DefaultName}
	kinds := map[string][]domain.TaskKind{rules.DefaultName: every}
	for _, rule := range document.Rules {
		names = append(names, rule.Name)
		kinds[rule.Name] = rule.When.Kinds
		if len(kinds[rule.Name]) == 0 {
			kinds[rule.Name] = every
		}
	}
	var problems []string
	for _, name := range names {
		flow, _ := document.Named(name)
		for _, kind := range kinds[name] {
			task := domain.TaskSpec{
				Version: 1, ID: "task", Kind: kind, Objective: "Change it", Repository: ".", Budget: domain.Budget{WallTimeSeconds: 1800},
				AcceptanceCriteria: []string{"tests pass"}, Source: domain.TaskSource{Kind: "request"}, Priority: 1,
			}
			result, err := planner.Build(context.Background(), planner.Input{
				Task: task, Rules: document, Registry: registry, DefaultRuntime: domain.RuntimeClaude, Disabled: true, RuleName: name, Now: time.Unix(0, 0),
			})
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: rule %s (%s) does not plan: %v", item, name, kind, err))
				continue
			}
			for _, step := range result.Workflow.Steps {
				at := slices.IndexFunc(flow.Stages, func(stage rules.Stage) bool { return stage.ID == step.ID })
				if step.Kind != domain.StepAgent || at < 0 || flow.Stages[at].Agent != agent.Auto {
					continue
				}
				// A stage no built-in agent can serve has no staffing to keep.
				expected, err := orchestrator.Fallback(task, []orchestrator.StageBrief{{ID: step.ID, Permission: step.Permission}}, builtins, nil)
				if err != nil {
					continue
				}
				if want := expected.Assignments[0].Agent; step.Agent != want {
					problems = append(problems, fmt.Sprintf("%s: rule %s (%s): fallback staffs %s stage %q with %s, want %s, as with only the built-in agents", item, name, kind, step.Permission, step.ID, step.Agent, want))
				}
			}
		}
	}
	return problems
}
