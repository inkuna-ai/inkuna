package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	"github.com/inkuna-ai/inkuna/internal/pricing"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// EventUsage tells the view that a run's usage gained a row. Its payload is
// the run id. A call can end after its run does, because a cancelled run
// finishes without waiting for its CLI to stop, so the view cannot rely on
// the run's own updates to know when to read usage again.
const EventUsage = "inkuna:usage"

// RunUsageView is what a run's model calls consumed: the orchestrator calls
// that planned its task, then each worker turn and peer answer, in the order
// they finished, and their sum, with costs summed by where they came from.
type RunUsageView struct {
	Calls []domain.UsageRecord `json:"calls"`
	Total domain.Usage         `json:"total"`
	Cost  domain.CostTotal     `json:"cost"`
}

// RunUsage lists every model call a run paid for and what they used in all.
func (service *Service) RunUsage(runID string) (RunUsageView, error) {
	if err := service.ready(); err != nil {
		return RunUsageView{}, err
	}
	calls, err := service.store.RunUsage(service.context(), runID)
	if errors.Is(err, domain.ErrNotFound) {
		return RunUsageView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return RunUsageView{}, fmt.Errorf("load run usage: %w", err)
	}
	return RunUsageView{Calls: calls, Total: domain.TotalUsage(calls), Cost: domain.TotalCost(calls)}, nil
}

// recordUsage stores one worker or peer call and announces it. Accounting is
// a record of the run, not part of it: a call that cannot be stored is
// reported on the step's timeline and the step goes on. It is stored even
// when the run was cancelled, since the call was paid for either way.
func (service *Service) recordUsage(executionID, stepRunID string, call domain.UsageRecord) {
	call.FinishedAt = time.Now().UTC()
	call.Cost = service.costOf(call)
	_, err := service.store.RecordUsage(context.Background(), call)
	// Forgotten only once stored: the run view reads running calls before
	// stored ones, so a call that ends in between shows in both, never in
	// neither.
	service.runsMu.Lock()
	if run := service.runs[executionID]; run != nil {
		delete(run.calls, call.CallID)
	}
	service.runsMu.Unlock()
	if err != nil {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, "Usage of "+call.CallID+" was not recorded: "+err.Error()))
		return
	}
	service.emit(EventUsage, executionID)
}

// startCall remembers a worker or peer call until recordUsage stores it, so
// the run view can show a call's prompt while the call is still running.
func (service *Service) startCall(executionID string, call domain.UsageRecord) {
	call.Outcome = domain.OutcomeRunning
	for _, section := range call.Sections {
		call.PromptBytes += section.Bytes
	}
	service.runsMu.Lock()
	if run := service.runs[executionID]; run != nil {
		run.calls[call.CallID] = call
	}
	service.runsMu.Unlock()
}

// runningCalls lists a step's calls that have started and are not stored
// yet, oldest first.
func (service *Service) runningCalls(executionID, stepID string) []domain.UsageRecord {
	service.runsMu.RLock()
	defer service.runsMu.RUnlock()
	run := service.runs[executionID]
	if run == nil {
		return nil
	}
	calls := make([]domain.UsageRecord, 0)
	for _, call := range run.calls {
		if call.StepID == stepID {
			calls = append(calls, call)
		}
	}
	sort.Slice(calls, func(a, b int) bool { return calls[a].StartedAt.Before(calls[b].StartedAt) })
	return calls
}

// recordPlanningCall stores an orchestrator call made while planning a task,
// named by what it planned; a nil call was not asked of a model. It stores
// even past the planning deadline, and even for a task whose planning then
// fails and is never saved, since the call was paid for either way. The plan
// stands without it, so a call that cannot be stored becomes a plan note.
// origin names the project and the person planning, since the task is not
// saved yet.
func (service *Service) recordPlanningCall(ctx context.Context, origin storage.UsageOrigin, taskID string, options orchestrator.Options, stepID string, call *orchestrator.Call) []string {
	if call == nil {
		return nil
	}
	record := domain.UsageRecord{
		TaskID: taskID, StepID: stepID, CallID: call.RunID, Turn: 1, Kind: domain.CallOrchestrator, Agent: agent.Orchestrator,
		Runtime: options.Runtime, Model: options.Model, ModelUsed: call.ModelUsed, AuthMode: call.AuthMode, Usage: call.Usage, Sections: call.Sections, Outcome: call.Outcome,
		StartedAt: call.StartedAt, FinishedAt: call.FinishedAt,
	}
	record.Cost = service.costOf(record)
	if _, err := service.store.RecordUsageFor(context.WithoutCancel(ctx), record, origin); err != nil {
		return []string{"The orchestrator's " + stepID + " usage was not recorded: " + err.Error()}
	}
	return nil
}

// costOf prices a call by the stream its runtime writes, which decides what
// the call can report, and by the model its stream named, or else the one
// Inkuna asked for.
func (service *Service) costOf(call domain.UsageRecord) domain.Cost {
	return service.pricing().Cost(service.streamFormat(call.Runtime), call.ModelName(), call.Usage)
}

// pricing is the configured pricing table; a configuration that was not
// built by config.FromFile carries none and gets the built-in one.
func (service *Service) pricing() pricing.Table {
	if service.config.Pricing.Version == 0 {
		return pricing.Default()
	}
	return service.config.Pricing
}

// streamFormat is the built-in CLI whose stream a declared runtime writes,
// empty for a plain-text template.
func (service *Service) streamFormat(name domain.Runtime) domain.Runtime {
	for _, spec := range service.config.Runtimes {
		if spec.Name == name {
			return spec.Format()
		}
	}
	return agentruntime.Spec{Name: name}.Format()
}
