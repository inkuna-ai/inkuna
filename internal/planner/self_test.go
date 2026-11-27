package planner

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/skill"
)

// conventionsSkill carries Inkuna's conventions to every worker. It has to be
// a stage skill because Copilot workers never read AGENTS.md or CLAUDE.md.
const conventionsSkill = "conventions"

// Inkuna plans its own work from its .inkuna directory as it would for any
// repository. Every flow there must plan with deterministic staffing, so a
// broken rule, agent, or skill fails the build instead of a later plan.
func TestInkunaPlansFromItsOwnDirectory(t *testing.T) {
	const root = "../.."
	document, exists, err := rules.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("expected Inkuna's rules at %s", rules.RelativePath)
	}
	registry, err := agent.Build(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	packages, err := skill.Discover(filepath.Join(root, ".inkuna", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	available := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		available[pkg.ID] = true
	}

	// The planner skips an agent's declared skill when the repository lacks
	// it; Inkuna's own agents must not rely on that.
	for _, definition := range registry.All() {
		for _, id := range definition.Skills {
			if definition.Source == agent.SourceRepository && !available[id] {
				t.Errorf("agent %s declares skill %q, which .inkuna/skills does not have", definition.Name, id)
			}
		}
	}

	// Fallback staffing depends on the task kind, so each rule is planned for
	// every kind it matches. Stages are looked up in the flows as planning
	// sees them, with any shared stages and groups expanded.
	flows := map[string]rules.Flow{}
	kinds := map[string][]domain.TaskKind{rules.DefaultName: {domain.TaskImplementation}}
	flows[rules.DefaultName], _ = document.Named(rules.DefaultName)
	for _, rule := range document.Rules {
		flows[rule.Name], _ = document.Named(rule.Name)
		kinds[rule.Name] = rule.When.Kinds
		if len(kinds[rule.Name]) == 0 {
			kinds[rule.Name] = []domain.TaskKind{domain.TaskImplementation}
		}
	}
	for name, flow := range flows {
		for _, kind := range kinds[name] {
			result, err := Build(context.Background(), Input{
				Task: testTask(kind), Skills: packages, Rules: document, Registry: registry,
				DefaultRuntime: domain.RuntimeClaude, Disabled: true, RuleName: name,
			})
			if err != nil {
				t.Errorf("rule %s (%s) does not plan: %v", name, kind, err)
				continue
			}
			for _, step := range result.Workflow.Steps {
				if step.Kind != domain.StepAgent {
					continue
				}
				if !slices.Contains(step.SelectedSkills, conventionsSkill) {
					t.Errorf("rule %s (%s): stage %q does not receive the %s skill", name, kind, step.ID, conventionsSkill)
				}
				stage := slices.IndexFunc(flow.Stages, func(stage rules.Stage) bool { return stage.ID == step.ID })
				if stage < 0 {
					t.Errorf("rule %s (%s): step %q has no stage", name, kind, step.ID)
					continue
				}
				// Fallback staffing takes the first specialist in name order. A
				// repository specialist named before "implementer" would quietly
				// take every writable stage whenever the orchestrator is down.
				if flow.Stages[stage].Agent == agent.Auto && step.Permission == domain.PermissionWorktree && step.Agent != "implementer" {
					t.Errorf("rule %s (%s): fallback staffs writable stage %q with %s, want implementer", name, kind, step.ID, step.Agent)
				}
			}
		}
	}
}
