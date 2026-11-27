package app

import (
	"errors"
	"fmt"
	"slices"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// GateStep is one agent step before a review gate or a human-input stage
// and what it left for the person to read before approving or answering:
// its document first, then the files it declared, from the round whose
// result stands.
type GateStep struct {
	StepID    string               `json:"stepId"`
	Title     string               `json:"title"`
	Agent     string               `json:"agent"`
	Round     int                  `json:"round"`
	Artifacts []domain.ArtifactRef `json:"artifacts"`
}

// GateArtifacts lists, for a review gate or a human-input stage of a run,
// every agent step it follows, directly or through other steps, that left a
// document or declared outputs, in plan order. Steps that left neither are left out;
// the code a step wrote is in the run's Changes tab.
func (service *Service) GateArtifacts(runID, gateID string) ([]GateStep, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	ctx := service.context()
	plan, err := service.store.ExecutionPlan(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return nil, fmt.Errorf("load the plan of run %s: %w", runID, err)
	}
	upstream := ancestors(plan, gateID)
	if upstream == nil {
		return nil, fmt.Errorf("run %s has no step %q", runID, gateID)
	}
	execution, err := service.store.Execution(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}
	results, err := service.currentResults(ctx, execution, plan)
	if err != nil {
		return nil, err
	}
	runs, err := service.store.StepRuns(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load the step runs of run %s: %w", runID, err)
	}
	// A standing result is the one from the last round its step completed in.
	rounds := make(map[string]int, len(runs))
	for _, run := range runs {
		if run.State == "completed" && run.Round <= max(execution.Round, 1) {
			rounds[run.StepID] = max(rounds[run.StepID], run.Round)
		}
	}
	steps := make([]GateStep, 0)
	for _, step := range plan.Steps {
		result, ok := results[step.ID]
		if !upstream[step.ID] || step.Kind != domain.StepAgent || !ok {
			continue
		}
		var documents, outputs []domain.ArtifactRef
		for _, ref := range result.Artifacts {
			switch ref.Kind {
			case DocumentArtifact:
				documents = append(documents, ref)
			case "output":
				outputs = append(outputs, ref)
			}
		}
		if len(documents)+len(outputs) == 0 {
			continue
		}
		steps = append(steps, GateStep{
			StepID: step.ID, Title: step.Title, Agent: step.Agent, Round: max(rounds[step.ID], 1),
			Artifacts: slices.Concat(documents, outputs),
		})
	}
	return steps, nil
}

// ancestors is every step a step depends on, directly or through others,
// or nil when the plan has no such step.
func ancestors(plan domain.WorkflowSpec, stepID string) map[string]bool {
	steps := make(map[string]domain.WorkflowStep, len(plan.Steps))
	for _, step := range plan.Steps {
		steps[step.ID] = step
	}
	start, ok := steps[stepID]
	if !ok {
		return nil
	}
	found := make(map[string]bool)
	pending := append([]string(nil), start.DependsOn...)
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if found[id] {
			continue
		}
		found[id] = true
		pending = append(pending, steps[id].DependsOn...)
	}
	return found
}
