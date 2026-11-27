package planner

import (
	"context"
	"slices"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	"github.com/inkuna-ai/inkuna/internal/rules"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// staffingRemote is an orchestrator that staffs every stage it is asked
// about: writable stages with test-writer, read-only ones with researcher, so
// Go's validation of an orchestrated plan is exercised for every built-in
// flow.
type staffingRemote struct{ called bool }

func (remote *staffingRemote) Decide(_ context.Context, request orchestrator.Request, _ orchestrator.Options, _ agentruntime.EventSink) (orchestrator.Decision, orchestrator.Call, error) {
	remote.called = true
	decision := orchestrator.Decision{Notes: "staffed by the test"}
	for _, stage := range request.Stages {
		name := stage.Agent
		switch {
		case name != "":
		case stage.Permission == domain.PermissionWorktree:
			name = "test-writer"
		default:
			name = "researcher"
		}
		decision.Assignments = append(decision.Assignments, orchestrator.Assignment{
			StageID: stage.ID, Agent: name, Complexity: domain.ComplexityAssessment{Score: 5, Rationale: "test"}, Rationale: "test",
		})
	}
	return decision, orchestrator.Call{}, nil
}

// Repositories without a rules.yaml plan from rules.Default(). Every built-in
// flow must plan for each kind that reaches it, with and without an
// orchestrator, and fallback staffing must leave every writable auto stage
// with implementer.
func TestBuiltInRulesPlanForEveryKindTheyMatch(t *testing.T) {
	document := rules.Default()
	kinds := map[string][]domain.TaskKind{rules.DefaultName: {domain.TaskImplementation, domain.TaskSkillAuthoring}}
	names := []string{rules.DefaultName}
	for _, rule := range document.Rules {
		kinds[rule.Name] = rule.When.Kinds
		names = append(names, rule.Name)
	}
	for _, name := range names {
		flow, _ := document.Named(name)
		for _, kind := range kinds[name] {
			input := Input{
				Task: testTask(kind), Skills: packages(), Rules: document, Registry: agent.NewRegistry(agent.BuiltIn()),
				DefaultRuntime: domain.RuntimeClaude, Disabled: true, RuleName: name,
			}
			result, err := Build(context.Background(), input)
			if err != nil {
				t.Errorf("rule %s (%s) does not plan without an orchestrator: %v", name, kind, err)
				continue
			}
			for _, step := range result.Workflow.Steps {
				stage := slices.IndexFunc(flow.Stages, func(stage rules.Stage) bool { return stage.ID == step.ID })
				if stage < 0 {
					t.Errorf("rule %s (%s): step %q has no stage", name, kind, step.ID)
					continue
				}
				if step.Kind == domain.StepAgent && flow.Stages[stage].Agent == agent.Auto && step.Permission == domain.PermissionWorktree && step.Agent != "implementer" {
					t.Errorf("rule %s (%s): fallback staffs writable stage %q with %s, want implementer", name, kind, step.ID, step.Agent)
				}
			}

			remote := &staffingRemote{}
			input.Disabled, input.Orchestrator = false, remote
			result, err = Build(context.Background(), input)
			if err != nil {
				t.Errorf("rule %s (%s) does not plan with an orchestrator: %v", name, kind, err)
				continue
			}
			// A flow that names every agent, such as documentation, has
			// nothing for the orchestrator to staff, but every agent stage
			// is still scored for its own work.
			if !remote.called || !result.Orchestrated {
				t.Errorf("rule %s (%s): orchestrator called %v, orchestrated %v; want both", name, kind, remote.called, result.Orchestrated)
			}
		}
	}
}

// Every implementation task scores 6 on the deterministic initial score, so
// the feature rule, which starts at 7, takes only larger work: here, four
// acceptance criteria.
func TestFeatureRuleTakesOnlyLargerImplementationTasks(t *testing.T) {
	plan := func(task domain.TaskSpec) Result {
		t.Helper()
		result, err := Build(context.Background(), Input{
			Task: task, Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()),
			DefaultRuntime: domain.RuntimeClaude, Disabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := plan(testTask(domain.TaskImplementation)); result.RuleName != rules.DefaultName {
		t.Fatalf("a small implementation task planned with %s, want the default flow", result.RuleName)
	}
	large := testTask(domain.TaskImplementation)
	large.AcceptanceCriteria = []string{"one", "two", "three", "four"}
	result := plan(large)
	if result.RuleName != "feature" {
		t.Fatalf("a larger implementation task planned with %s, want feature", result.RuleName)
	}
	steps := map[string]domain.WorkflowStep{}
	order := make([]string, 0, len(result.Workflow.Steps))
	for _, step := range result.Workflow.Steps {
		steps[step.ID] = step
		order = append(order, step.ID)
	}
	if want := []string{"profile", "plan", "confirm-plan", "implement", "verify", "review", "approval"}; !slices.Equal(order, want) {
		t.Fatalf("feature steps = %v, want %v", order, want)
	}
	if plan := steps["plan"]; plan.Agent != "planner" || plan.Permission != domain.PermissionReadOnly {
		t.Fatalf("plan = %s, %s; want planner, read-only", plan.Agent, plan.Permission)
	}
	if confirm := steps["confirm-plan"]; confirm.Kind != domain.StepHumanInput {
		t.Fatalf("confirm-plan is a %s stage, want human-input", confirm.Kind)
	}
	// implement reads the plan's handoff and the person's answer, since a
	// step sees only the stages it depends on.
	if implement := steps["implement"]; !slices.Equal(implement.DependsOn, []string{"plan", "confirm-plan"}) || implement.Agent != "implementer" {
		t.Fatalf("implement depends on %v with %s", implement.DependsOn, implement.Agent)
	}
	if review := steps["review"]; review.Agent != "reviewer" || review.Permission != domain.PermissionReadOnly || !slices.Contains(review.DependsOn, "implement") {
		t.Fatalf("review = %s, %s, depends on %v", review.Agent, review.Permission, review.DependsOn)
	}
	if approval := steps["approval"]; !approval.Review || approval.FeedbackTarget != "implement" {
		t.Fatalf("approval review %v, feedback to %q; want a review gate sending feedback to implement", approval.Review, approval.FeedbackTarget)
	}

	docs := plan(testTask(domain.TaskDocumentation))
	if docs.RuleName != "documentation" || !slices.ContainsFunc(docs.Workflow.Steps, func(step domain.WorkflowStep) bool { return step.ID == "write" && step.Agent == "documenter" }) {
		t.Fatalf("documentation planned with %s: %+v", docs.RuleName, docs.Workflow.Steps)
	}
	if skills := plan(testTask(domain.TaskSkillAuthoring)); skills.RuleName != rules.DefaultName {
		t.Fatalf("skill authoring planned with %s, want the default flow", skills.RuleName)
	}
}
