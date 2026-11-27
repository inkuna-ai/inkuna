package planner

import (
	"context"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	"github.com/inkuna-ai/inkuna/internal/routing"
	"github.com/inkuna-ai/inkuna/internal/rules"
)

// scoringRules has a stage the rule staffs with a read-only agent, one left
// to the orchestrator, and one whose rule-named agent pins Codex.
func scoringRules(t *testing.T) rules.Document {
	t.Helper()
	document, err := rules.Parse(`version: 1
rules: []
default:
  stages:
    - id: research
      kind: agent
      agent: researcher
      permission: read-only
    - id: implement
      kind: agent
      agent: auto
      permission: worktree-write
      dependsOn: [research]
    - id: notes
      kind: agent
      agent: note-taker
      permission: read-only
      dependsOn: [implement]
    - id: approval
      kind: approval
      dependsOn: [notes]
`)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func scoringRegistry() agent.Registry {
	return agent.NewRegistry(agent.BuiltIn(), []agent.Definition{{
		Name: "note-taker", Description: "Writes a short note of what changed", Permission: domain.PermissionReadOnly, Runtime: domain.RuntimeCodex,
		Instructions: "Write the note.", Source: agent.SourceRepository,
	}})
}

func steps(result Result) map[string]domain.WorkflowStep {
	byID := make(map[string]domain.WorkflowStep, len(result.Workflow.Steps))
	for _, step := range result.Workflow.Steps {
		byID[step.ID] = step
	}
	return byID
}

func TestTheOrchestratorScoresEveryAgentStageForItsOwnWork(t *testing.T) {
	remote := &fakeRemote{decision: orchestrator.Decision{Assignments: []orchestrator.Assignment{
		{StageID: "research", Agent: "researcher", Complexity: domain.ComplexityAssessment{Score: 4, Rationale: "traces one path"}},
		{StageID: "implement", Agent: "implementer", Complexity: domain.ComplexityAssessment{Score: 8, Rationale: "cross-cutting"}, Rationale: "generalist"},
		{StageID: "notes", Agent: "note-taker", Complexity: domain.ComplexityAssessment{Score: 1, Rationale: "a short note"}},
	}}}
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: scoringRules(t), Registry: scoringRegistry(),
		DefaultRuntime: domain.RuntimeClaude, Orchestrator: remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Orchestrated {
		t.Fatalf("the decision must be used: %v", result.Notes)
	}

	// The request names each rule-staffed stage's agent and the runtime it
	// runs on, and the bands of every runtime a stage may run on.
	briefs := make(map[string]orchestrator.StageBrief)
	for _, brief := range remote.request.Stages {
		briefs[brief.ID] = brief
	}
	if len(briefs) != 3 || briefs["research"].Agent != "researcher" || briefs["implement"].Agent != "" || briefs["notes"].Agent != "note-taker" || briefs["notes"].Runtime != domain.RuntimeCodex {
		t.Fatalf("every agent stage must be briefed, with the rule's agent and runtime: %+v", remote.request.Stages)
	}
	defaults := routing.DefaultPolicy().Bands
	if remote.request.Runtime != domain.RuntimeClaude || len(remote.request.Routing) != 2 ||
		len(remote.request.Routing[domain.RuntimeClaude]) != len(defaults[domain.RuntimeClaude]) || len(remote.request.Routing[domain.RuntimeCodex]) != len(defaults[domain.RuntimeCodex]) {
		t.Fatalf("the request must carry the plan's runtime and the bands of each runtime a stage runs on: %s %+v", remote.request.Runtime, remote.request.Routing)
	}

	byID := steps(result)
	for id, want := range map[string]struct {
		score int
		model string
	}{"research": {4, "sonnet"}, "implement": {8, "opus"}, "notes": {1, "gpt-6-luna"}} {
		step := byID[id]
		if step.Complexity == nil || step.Complexity.Score != want.score || step.ModelSelection == nil || step.ModelSelection.Model != want.model {
			t.Errorf("step %s: scored %+v and routed to %+v; want %d on %s", id, step.Complexity, step.ModelSelection, want.score, want.model)
		}
	}
	if research := byID["research"]; research.Agent != "researcher" || research.Metadata["assignment"] != "rule default names researcher" {
		t.Fatalf("a rule-staffed stage keeps the rule's agent and why: %+v", research)
	}
}

func TestADecisionThatChangesTheRulesAgentFallsBack(t *testing.T) {
	remote := &fakeRemote{decision: orchestrator.Decision{Assignments: []orchestrator.Assignment{
		{StageID: "research", Agent: "planner", Complexity: domain.ComplexityAssessment{Score: 4, Rationale: "r"}},
		{StageID: "implement", Agent: "implementer", Complexity: domain.ComplexityAssessment{Score: 8, Rationale: "r"}},
		{StageID: "notes", Agent: "note-taker", Complexity: domain.ComplexityAssessment{Score: 1, Rationale: "r"}},
	}}}
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: scoringRules(t), Registry: scoringRegistry(),
		DefaultRuntime: domain.RuntimeClaude, Orchestrator: remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Orchestrated || !strings.Contains(strings.Join(result.Notes, " "), `stage "research" is staffed by the rule with "researcher"`) {
		t.Fatalf("a decision that swaps the rule's agent must be rejected: %+v", result.Notes)
	}
	if research := steps(result)["research"]; research.Agent != "researcher" {
		t.Fatalf("the fallback keeps the rule's agent: %s", research.Agent)
	}
}

// Without a model, the stages of the built-in feature flow that only read,
// planning and review, score below the one that implements the task.
func TestTheFallbackScoresStagesThatOnlyReadBelowTheOneThatWrites(t *testing.T) {
	task := testTask(domain.TaskImplementation)
	task.AcceptanceCriteria = []string{"one", "two", "three", "four"}
	result, err := Build(context.Background(), Input{
		Task: task, Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()), DefaultRuntime: domain.RuntimeClaude, Disabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RuleName != "feature" {
		t.Fatalf("planned with %s, want feature", result.RuleName)
	}
	byID := steps(result)
	implement, plan, review := byID["implement"].Complexity.Score, byID["plan"].Complexity.Score, byID["review"].Complexity.Score
	if implement != 7 || plan != 5 || review != 5 {
		t.Fatalf("implement %d, plan %d, review %d; want 7, 5, 5", implement, plan, review)
	}
	if model := byID["plan"].ModelSelection; model == nil || model.Model != "sonnet" || model.ReasoningEffort != "medium" {
		t.Fatalf("plan routes by its own score: %+v", model)
	}
}
