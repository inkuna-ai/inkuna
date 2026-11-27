package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// notStopped is why a run that did not fail, was not cancelled, and was not
// interrupted cannot be rerun.
const notStopped = "only a run that failed, was cancelled, or was interrupted can be rerun"

// RerunView is what the approval screen starts a rerun of a stopped run
// from (docs/feedback-loop.md): the run's plan, with the project, task,
// agents, and servers a first approval shows, and what resuming under that
// plan would do now, or why the run cannot be resumed, which leaves only
// starting over.
type RerunView struct {
	RunID  string           `json:"runId"`
	Plan   WorkflowPlanView `json:"plan"`
	Resume ResumeView       `json:"resume"`
}

// RerunRevision is a rerun's plan as Go rebuilt it for the person's edits,
// the steps they edited, and what resuming under it would do now.
type RerunRevision struct {
	Workflow domain.WorkflowSpec `json:"workflow"`
	Edited   []string            `json:"edited"`
	Resume   ResumeView          `json:"resume"`
}

// RerunRequest is how a person reruns a stopped run: the round they saw it
// stop in, the plan as they approved it, as JSON, and whether to start over.
// A resume also says what to do with what is uncommitted in the run's
// workspace, as ResumeRequest does.
type RerunRequest struct {
	Round     int      `json:"round"`
	Workflow  string   `json:"workflow"`
	StartOver bool     `json:"startOver"`
	Discard   bool     `json:"discard"`
	Changes   []string `json:"changes"`
}

// PlanRerun starts a rerun of a run that failed, was cancelled, or was
// interrupted: the plan it ran under, for the approval screen to edit, and
// what resuming under that plan would do.
func (service *Service) PlanRerun(runID string) (RerunView, error) {
	if err := service.ready(); err != nil {
		return RerunView{}, err
	}
	ctx := service.context()
	execution, resume, err := service.stoppedRun(runID)
	if err != nil {
		return RerunView{}, err
	}
	if !resume.Stopped {
		return RerunView{}, errors.New(notStopped)
	}
	plan, err := service.store.ExecutionPlan(ctx, runID)
	if err != nil {
		return RerunView{}, fmt.Errorf("this run cannot be rerun: %w", err)
	}
	task, err := service.store.Task(ctx, execution.TaskID)
	if err != nil {
		return RerunView{}, fmt.Errorf("load the run's task: %w", err)
	}
	project, err := service.store.ProjectForTask(ctx, execution.TaskID)
	if err != nil {
		return RerunView{}, fmt.Errorf("load the run's project: %w", err)
	}
	if err := refuseRemoved(project); err != nil {
		return RerunView{}, err
	}
	registry, _, err := service.agentRegistry(task.Repository)
	if err != nil {
		return RerunView{}, fmt.Errorf("load agents: %w", err)
	}
	view := RerunView{RunID: runID, Plan: WorkflowPlanView{
		Project: projectView(project), Task: task, Workflow: plan, RuleName: plan.RuleName, Orchestrated: plan.Orchestrated,
		Candidates: agentCandidates(registry), Usage: []domain.UsageRecord{},
	}}
	for _, entry := range service.config.File.MCPServers {
		view.Plan.MCPServers = append(view.Plan.MCPServers, entry.Brief())
	}
	if resume.Refusal == "" {
		if resume, err = service.resumeOutlook(ctx, execution, plan, nil, resume); err != nil {
			return RerunView{}, err
		}
	}
	view.Resume = resume
	return view, nil
}

// ReviseRerun rebuilds a rerun's plan around the approval screen's edits
// exactly as ReviseWorkflow does for a first approval, from the plan the run
// ran under, and says what resuming under it would do: the steps the person
// edited run again with everything after them. Nothing is persisted.
func (service *Service) ReviseRerun(runID, workflowJSON string) (RerunRevision, error) {
	if err := service.ready(); err != nil {
		return RerunRevision{}, err
	}
	var edited domain.WorkflowSpec
	if err := json.Unmarshal([]byte(workflowJSON), &edited); err != nil {
		return RerunRevision{}, fmt.Errorf("decode workflow: %w", err)
	}
	ctx := service.context()
	execution, resume, err := service.stoppedRun(runID)
	if err != nil {
		return RerunRevision{}, err
	}
	if !resume.Stopped {
		return RerunRevision{}, errors.New(notStopped)
	}
	base, err := service.store.ExecutionPlan(ctx, runID)
	if err != nil {
		return RerunRevision{}, fmt.Errorf("this run cannot be rerun: %w", err)
	}
	service.configMu.RLock()
	revision, err := service.reviseFrom(ctx, base, edited)
	service.configMu.RUnlock()
	if err != nil {
		return RerunRevision{}, err
	}
	steps := personEdits(base, edited, revision.spec)
	if resume.Refusal == "" {
		if resume, err = service.resumeOutlook(ctx, execution, revision.spec, steps, resume); err != nil {
			return RerunRevision{}, err
		}
	}
	if steps == nil {
		steps = []string{}
	}
	return RerunRevision{Workflow: revision.spec, Edited: steps, Resume: resume}, nil
}

// RerunWorkflowRun runs a stopped run again under the plan the person
// approved for it, which Go rebuilds and checks as it does a first
// approval. A resume is ResumeWorkflowRun under that plan: a new round of
// the same run, on its worktree, keeping the results of the completed steps
// that neither stopped nor were edited, and recording the plan it replaced.
// Starting over is a new run of the task, from a fresh workspace, that runs
// every step and records the run it started over from; the stopped run
// stays as it was.
func (service *Service) RerunWorkflowRun(runID string, request RerunRequest) (WorkflowRunView, error) {
	if err := service.ready(); err != nil {
		return WorkflowRunView{}, err
	}
	var approved domain.WorkflowSpec
	if err := json.Unmarshal([]byte(request.Workflow), &approved); err != nil {
		return WorkflowRunView{}, fmt.Errorf("decode workflow: %w", err)
	}
	if !request.StartOver {
		return service.resumeRun(runID, ResumeRequest{Round: request.Round, Discard: request.Discard, Changes: request.Changes}, &approved)
	}
	execution, resume, err := service.stoppedRun(runID)
	if err != nil {
		return WorkflowRunView{}, err
	}
	switch {
	case resume.Refusal == resumeBusy:
		return WorkflowRunView{}, errors.New("this run is still running; start it over once it has stopped")
	case !resume.Stopped:
		return WorkflowRunView{}, errors.New(notStopped)
	case request.Round != max(execution.Round, 1):
		return WorkflowRunView{}, fmt.Errorf("this run is in round %d, and it was rerun from round %d; reload it", max(execution.Round, 1), request.Round)
	}
	base, err := service.store.ExecutionPlan(service.context(), runID)
	if err != nil {
		return WorkflowRunView{}, fmt.Errorf("this run cannot be rerun: %w", err)
	}
	return service.startApproved(approved, &base, runID)
}

// approveRerun checks the plan a person approved to resume a run under, as
// StartWorkflow checks a first approval: it must be what Go rebuilds from
// the run's plan for those edits. It returns that plan and the steps the
// person edited.
func (service *Service) approveRerun(ctx context.Context, base, approved domain.WorkflowSpec) (domain.WorkflowSpec, []string, error) {
	if err := domain.ValidateRoutedWorkflow(approved); err != nil {
		return domain.WorkflowSpec{}, nil, err
	}
	// The plan is checked against the configuration, as a first approval
	// is, so no save lands while it is.
	service.configMu.RLock()
	revision, err := service.reviseFrom(ctx, base, approved)
	service.configMu.RUnlock()
	if err != nil {
		return domain.WorkflowSpec{}, nil, err
	}
	if err := checkApprovalCurrent(revision.spec, approved); err != nil {
		return domain.WorkflowSpec{}, nil, err
	}
	return revision.spec, personEdits(base, approved, revision.spec), nil
}

// personEdits names the steps of base whose plan the person changed in an
// approval of it: an agent step's agent, runtime, score, or access, and an
// evaluation step's commands. What Go derives again from those, such as an
// agent's skills or a step's model, is not an edit. Access is compared as Go
// rebuilt it, so a path the person only padded with spaces is no edit.
func personEdits(base, approved, revised domain.WorkflowSpec) []string {
	var edited []string
	for index, step := range base.Steps {
		shown, rebuilt := approved.Steps[index], revised.Steps[index]
		changed := false
		switch step.Kind {
		case domain.StepAgent:
			changed = shown.Agent != step.Agent || shown.Runtime != step.Runtime || scoreOf(shown) != scoreOf(step) ||
				rebuilt.Permission != step.Permission || !sameCommands(rebuilt.Commands, step.Commands) || !sameStrings(rebuilt.AllowedPaths, step.AllowedPaths)
		case domain.StepEvaluation:
			changed = !sameCommands(rebuilt.Commands, step.Commands)
		}
		if changed {
			edited = append(edited, step.ID)
		}
	}
	return edited
}
