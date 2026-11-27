package planner

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
)

// trackerRules is a flow whose one agent stage, staffed by agentName, asks
// for the given tracker kinds, followed by a review gate when gated.
func trackerRules(t *testing.T, agentName, kinds string, gated bool) rules.Document {
	t.Helper()
	stage := "    - id: research\n      kind: agent\n      agent: " + agentName + "\n      permission: read-only\n"
	if kinds != "" {
		stage += "      tracker: [" + kinds + "]\n"
	}
	gate := "    - id: approval\n      kind: approval\n      dependsOn: [research]\n"
	if gated {
		// The gate need not follow the stage directly.
		gate = "    - id: summarize\n      kind: agent\n      agent: researcher\n      permission: read-only\n      dependsOn: [research]\n" +
			"    - id: approval\n      kind: approval\n      review: true\n      dependsOn: [summarize]\n"
	}
	document, err := rules.Parse("version: 1\nrules: []\ndefault:\n  stages:\n" + stage + gate)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func trackerAgent(kinds ...domain.TrackerKind) agent.Registry {
	return agent.NewRegistry(agent.BuiltIn(), []agent.Definition{{
		Name: "triager", Description: "Triages", Permission: domain.PermissionReadOnly, Tracker: kinds, Instructions: "Triage.", Source: agent.SourceRepository,
	}})
}

func TestAStepIsGrantedTheKindsItAsksForThatTheProjectAllows(t *testing.T) {
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskResearch), Rules: trackerRules(t, "triager", "comment, status", true), Registry: trackerAgent(domain.TrackerCreate, domain.TrackerEdit),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, TrackerKinds: []domain.TrackerKind{domain.TrackerComment, domain.TrackerCreate},
	})
	if err != nil {
		t.Fatal(err)
	}
	research := result.Workflow.Steps[0]
	if !slices.Equal(research.TrackerKinds, []domain.TrackerKind{domain.TrackerComment, domain.TrackerCreate}) {
		t.Fatalf("kinds = %v", research.TrackerKinds)
	}
	if !slices.ContainsFunc(result.Notes, func(note string) bool {
		return strings.Contains(note, `Step "research" asks for status, edit tracker changes, which the repository's tracker settings do not allow here`)
	}) {
		t.Fatalf("notes = %v", result.Notes)
	}
	if !slices.Equal(result.Workflow.TrackerKinds, []domain.TrackerKind{domain.TrackerComment, domain.TrackerCreate}) {
		t.Fatalf("the plan keeps what the project allowed: %v", result.Workflow.TrackerKinds)
	}
	if summarize := result.Workflow.Steps[1]; len(summarize.TrackerKinds) != 0 {
		t.Fatalf("a step that asks for none has none: %v", summarize.TrackerKinds)
	}
}

func TestAStepThatMayAskForChangesNeedsAReviewGateAfterIt(t *testing.T) {
	input := Input{
		Task: testTask(domain.TaskResearch), Rules: trackerRules(t, "researcher", "comment", false), Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, TrackerKinds: []domain.TrackerKind{domain.TrackerComment},
	}
	if _, err := Build(context.Background(), input); err == nil || !strings.Contains(err.Error(), `step "research" may ask for comment tracker changes, but no review gate follows it`) {
		t.Fatalf("a step with kinds and no gate after it must not plan: %v", err)
	}
	// A project that allows nothing gives the step no kinds, and so needs
	// no gate.
	input.TrackerKinds = nil
	if _, err := Build(context.Background(), input); err != nil {
		t.Fatalf("a step granted nothing needs no gate: %v", err)
	}
}

func TestARemoteAdvisorAsksForNoTrackerChanges(t *testing.T) {
	_, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskResearch), Rules: trackerRules(t, "security-advisor", "comment", true), Registry: remoteRegistry(false),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, TrackerKinds: domain.TrackerKinds(),
	})
	if err == nil || !strings.Contains(err.Error(), `cannot be given stage "research", which asks for tracker changes`) {
		t.Fatalf("a remote advisor must not staff a stage that asks for changes: %v", err)
	}
	registry, _ := agent.NewRegistry(agent.BuiltIn()).WithRemote([]agent.Definition{{Name: "remote-reader", Description: "Reads", Tracker: []domain.TrackerKind{domain.TrackerComment}}})
	if definition, _ := registry.Get("remote-reader"); len(definition.Tracker) != 0 {
		t.Fatalf("a remote agent keeps no tracker kinds: %v", definition.Tracker)
	}
}

func TestRestaffSwapsTheAgentsKindsWithinWhatThePlanAllowed(t *testing.T) {
	registry := agent.NewRegistry(agent.BuiltIn(), []agent.Definition{
		{Name: "triager", Description: "Triages", Permission: domain.PermissionReadOnly, Tracker: []domain.TrackerKind{domain.TrackerEdit}, Instructions: "Triage.", Source: agent.SourceRepository},
		{Name: "filer", Description: "Files issues", Permission: domain.PermissionReadOnly, Tracker: []domain.TrackerKind{domain.TrackerCreate, domain.TrackerStatus}, Instructions: "File.", Source: agent.SourceRepository},
	})
	allowed := []domain.TrackerKind{domain.TrackerComment, domain.TrackerEdit, domain.TrackerCreate}
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskResearch), Rules: trackerRules(t, "triager", "comment", true), Registry: registry,
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, TrackerKinds: allowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	step := result.Workflow.Steps[0]
	if !slices.Equal(step.TrackerKinds, []domain.TrackerKind{domain.TrackerComment, domain.TrackerEdit}) {
		t.Fatalf("planned kinds = %v", step.TrackerKinds)
	}
	if err := Restaff(&step, Edit{Agent: "filer", Runtime: domain.RuntimeClaude, Score: step.Complexity.Score}, registry, nil, nil, result.Workflow.TrackerKinds); err != nil {
		t.Fatal(err)
	}
	// The stage's comment stays, triager's edit leaves, and filer's status
	// is not allowed.
	if !slices.Equal(step.TrackerKinds, []domain.TrackerKind{domain.TrackerComment, domain.TrackerCreate}) {
		t.Fatalf("restaffed kinds = %v", step.TrackerKinds)
	}
}

func TestCheckTrackerGatesLooksPastTheNextStep(t *testing.T) {
	workflow := domain.WorkflowSpec{Steps: []domain.WorkflowStep{
		{ID: "a", Kind: domain.StepAgent, TrackerKinds: []domain.TrackerKind{domain.TrackerComment}},
		{ID: "b", Kind: domain.StepAgent, DependsOn: []string{"a"}},
		{ID: "plain", Kind: domain.StepApproval, DependsOn: []string{"b"}},
		{ID: "gate", Kind: domain.StepApproval, Review: true, DependsOn: []string{"plain"}},
	}}
	if err := CheckTrackerGates(workflow); err != nil {
		t.Fatal(err)
	}
	// A gate beside the step, not after it, does not count.
	workflow.Steps[3].DependsOn = nil
	if err := CheckTrackerGates(workflow); err == nil {
		t.Fatal("a gate that does not follow the step was accepted")
	}
}
