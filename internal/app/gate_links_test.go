package app

import (
	"context"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// TestAHumanInputStageLinksTheDocumentsItFollows checks that a human-input
// stage's question is marked as one, so its thread lists what it is about,
// and that the listing a review gate uses gives it the plan before it.
func TestAHumanInputStageLinksTheDocumentsItFollows(t *testing.T) {
	runtime := &workflowRuntime{handle: func(_ context.Context, _ agentruntime.RunRequest) (agentruntime.RunResult, error) {
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Planned the change", Document: planDocument})}, nil
	}}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	plan := spec.Steps[0]
	plan.ID, plan.Title, plan.Permission = "plan", "Plan the work", domain.PermissionReadOnly
	spec.Steps = []domain.WorkflowStep{
		plan,
		{ID: "confirm", Title: "Approve the plan", Kind: domain.StepHumanInput, Prompt: "Reply approved to start.", Permission: domain.PermissionReadOnly, DependsOn: []string{"plan"}, Budget: domain.Budget{WallTimeSeconds: 60}},
	}
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)

	input := waitForInbox(t, service, 1)[0]
	if !input.Stage || input.StepID != "confirm" || len(input.Permissions) != 0 || len(input.InputRequest.Permissions) != 0 {
		t.Fatalf("the stage's question is marked as a stage's: %+v", input)
	}
	steps, err := service.GateArtifacts(run.ID, "confirm")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].StepID != "plan" || steps[0].Title != "Plan the work" || len(steps[0].Artifacts) != 1 || steps[0].Artifacts[0].Kind != DocumentArtifact {
		t.Fatalf("the stage links the plan it asks about: %+v", steps)
	}
	if _, err := service.AnswerInput(input.ID, "approved"); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
}
