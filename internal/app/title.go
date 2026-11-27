package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// titleTimeout bounds the orchestrator's title call.
const titleTimeout = time.Minute

// titleTask gives a task being planned its title: the one its author wrote,
// or, when they left it blank, one the orchestrator proposes from the
// details, or else one derived from the details' first line. Go cleans and
// checks a proposed title as it does an author's, so a refused one falls
// back too. It returns notes for the plan and the call it made, if any.
func titleTask(ctx context.Context, task *domain.TaskSpec, given string, proposer orchestrator.TitleProposer, options orchestrator.Options, sink agentruntime.EventSink) ([]string, *orchestrator.Call, error) {
	if strings.TrimSpace(given) != "" {
		title, err := domain.CleanTitle(given)
		if err != nil {
			return nil, nil, titleError(err)
		}
		task.Title = title
		return nil, nil, nil
	}
	// A derived title is cleaned as any other, and one that does not clean
	// leaves the task untitled, to be shown by DisplayTitle.
	derive := func(why string) []string {
		task.Title = ""
		if title, err := domain.CleanTitle(domain.DeriveTitle(task.Objective)); err == nil {
			task.Title = title
		}
		return []string{fmt.Sprintf("The title was taken from the details' first line (%s); change it before approving if it does not name the task.", why)}
	}
	if proposer == nil {
		return derive("the orchestrator is off"), nil, nil
	}
	// The title has a fallback, so its call gets at most titleTimeout and
	// never more than half of what planning has left, which staffing and
	// branching still need.
	limit := titleTimeout
	if deadline, ok := ctx.Deadline(); ok {
		limit = min(limit, time.Until(deadline)/2)
	}
	if limit < time.Second {
		return derive("planning had no time left to ask the orchestrator"), nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	options.RunID, options.WallTimeSeconds = "title-"+task.ID, int(limit/time.Second)
	proposed, call, err := proposer.ProposeTitle(ctx, orchestrator.TitleRequest{Kind: task.Kind, Details: task.Objective, AcceptanceCriteria: task.AcceptanceCriteria}, options, sink)
	var made *orchestrator.Call
	if call.Outcome != "" {
		made = &call
	}
	if err != nil {
		return derive("the orchestrator could not write one: " + err.Error()), made, nil
	}
	title, err := domain.CleanTitle(proposed)
	if err != nil {
		return derive(fmt.Sprintf("the orchestrator's %q was refused: %v", truncateSummary(proposed, 120), titleError(err))), made, nil
	}
	task.Title = title
	return []string{"The orchestrator wrote the title from the details; change it before approving if it does not fit."}, made, nil
}

// PlanTitleView is a planned task with its new title, and the branch plan
// named again for it.
type PlanTitleView struct {
	Task      domain.TaskSpec    `json:"task"`
	Branching *domain.BranchPlan `json:"branching,omitempty"`
}

// SetPlanTitle changes the title of a planned task before it is approved,
// and names its planned branch again from the new title, so the approval
// screen shows the branch the run will get. Approval reads both back from
// the stored plan.
func (service *Service) SetPlanTitle(workflowID, title string) (PlanTitleView, error) {
	if err := service.ready(); err != nil {
		return PlanTitleView{}, err
	}
	ctx := service.context()
	state, err := service.store.WorkflowState(ctx, workflowID)
	if err != nil {
		return PlanTitleView{}, fmt.Errorf("load plan: %w", err)
	}
	if state != "planned" {
		return PlanTitleView{}, errors.New("this plan was approved already; its title can no longer change")
	}
	cleaned, err := domain.CleanTitle(title)
	if err != nil {
		return PlanTitleView{}, titleError(err)
	}
	workflow, err := service.store.Workflow(ctx, workflowID)
	if err != nil {
		return PlanTitleView{}, fmt.Errorf("load plan: %w", err)
	}
	task, err := service.store.Task(ctx, workflow.TaskID)
	if err != nil {
		return PlanTitleView{}, fmt.Errorf("load the plan's task: %w", err)
	}
	project, err := service.store.ProjectForTask(ctx, task.ID)
	if err != nil {
		return PlanTitleView{}, fmt.Errorf("load the plan's project: %w", err)
	}
	if err := refuseRemoved(project); err != nil {
		return PlanTitleView{}, err
	}
	task.Title = cleaned
	if plan := workflow.Branching; plan != nil && plan.Branch != "" {
		loaded, err := branching.Load(task.Repository)
		if err != nil {
			return PlanTitleView{}, err
		}
		config := loaded.Config.WithValues(plan.Proposed)
		if err := config.Validate(); err != nil {
			return PlanTitleView{}, fmt.Errorf("branching config: %w", err)
		}
		if plan.Branch, err = config.ForTask(task); err != nil {
			return PlanTitleView{}, fmt.Errorf("name the run's branch: %w", err)
		}
	}
	// The title is saved first: a branch that failed to save is named again
	// from it on the next change, and approval reads both back.
	if err := service.store.SaveTask(ctx, project.ID, "planned", task); err != nil {
		return PlanTitleView{}, fmt.Errorf("save task: %w", err)
	}
	if workflow.Branching != nil && workflow.Branching.Branch != "" {
		if err := service.store.SaveWorkflow(ctx, "planned", workflow); err != nil {
			return PlanTitleView{}, fmt.Errorf("save plan: %w", err)
		}
	}
	return PlanTitleView{Task: task, Branching: workflow.Branching}, nil
}

// RunTask is the task a run carries out: its title, and the details the run
// header folds under it.
func (service *Service) RunTask(runID string) (domain.TaskSpec, error) {
	if err := service.ready(); err != nil {
		return domain.TaskSpec{}, err
	}
	execution, err := service.execution(service.context(), runID)
	if err != nil {
		return domain.TaskSpec{}, fmt.Errorf("load run %s: %w", runID, err)
	}
	task, err := service.store.Task(service.context(), execution.TaskID)
	if err != nil {
		return domain.TaskSpec{}, fmt.Errorf("load the run's task: %w", err)
	}
	return task, nil
}

// titleError is why a title was refused, without the contract's prefix, as
// a person reads it.
func titleError(err error) error {
	return errors.New(strings.TrimPrefix(err.Error(), domain.ErrInvalid.Error()+": "))
}
