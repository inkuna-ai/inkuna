package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// RunFailureView is why a run that stopped did not finish, step by step, as
// its records say, and the changes to its plan Go suggests from them
// (docs/rules.md). A run that did not stop has neither.
type RunFailureView struct {
	Round int `json:"round"`
	// Stopped says the run failed, was cancelled, or was interrupted, so it
	// can be rerun, even when no step recorded why: one stopped between
	// steps, or before its first.
	Stopped bool `json:"stopped"`
	// Error is the run's own error, when it recorded one.
	Error string `json:"error,omitempty"`
	// Steps are the steps of the round the run stopped in that did not
	// finish, or finished refused something, in the plan's order.
	Steps       []FailedStepView `json:"steps"`
	Suggestions []PlanSuggestion `json:"suggestions"`
}

// FailedStepView is how one step of a stopped run's last round went wrong:
// how it ended, its error, the tool calls its last turn was refused, and the
// commands of its checks that failed.
type FailedStepView struct {
	StepID  string            `json:"stepId"`
	Title   string            `json:"title"`
	State   string            `json:"state"`
	Error   string            `json:"error,omitempty"`
	Denials []DenialView      `json:"denials"`
	Checks  []FailedCheckView `json:"checks"`
}

// DenialView is a tool call a step was refused. Write says it would have
// changed files on a read-only step, which only the step's permission
// decides. Grantable says the person could have allowed it for the run from
// the inbox and did not, or the run stopped before they answered; any other
// denial could not be allowed for one run at all.
type DenialView struct {
	domain.PermissionDenial
	Write     bool `json:"write,omitempty"`
	Grantable bool `json:"grantable,omitempty"`
}

// FailedCheckView is an evaluation command that failed: its argv, and its
// exit code or why it could not run.
type FailedCheckView struct {
	CheckID  string   `json:"checkId"`
	Argv     []string `json:"argv"`
	ExitCode *int     `json:"exitCode,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// PlanSuggestion is a change to a step's access that Go suggests from why
// the run stopped: one of the edits the approval screen can make, which it
// applies only when the person accepts it, for them to review before they
// rerun. Command is a command to add to the step's commands, and Permission
// a permission to give it; one of the two is set.
type PlanSuggestion struct {
	ID         string            `json:"id"`
	StepID     string            `json:"stepId"`
	Text       string            `json:"text"`
	Command    []string          `json:"command,omitempty"`
	Permission domain.Permission `json:"permission,omitempty"`
}

// RunFailure says why a run that failed, was cancelled, or was interrupted
// stopped, and suggests changes to its plan from that.
func (service *Service) RunFailure(runID string) (RunFailureView, error) {
	if err := service.ready(); err != nil {
		return RunFailureView{}, err
	}
	// A run shows as ended a moment before it has stored how its steps
	// ended; a running one has not failed.
	if err := service.settle(runID); errors.Is(err, errRunBusy) {
		return RunFailureView{Steps: []FailedStepView{}, Suggestions: []PlanSuggestion{}}, nil
	} else if err != nil {
		return RunFailureView{}, err
	}
	ctx := service.context()
	execution, err := service.store.Execution(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return RunFailureView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return RunFailureView{}, fmt.Errorf("load run %s: %w", runID, err)
	}
	view := RunFailureView{Round: max(execution.Round, 1), Steps: []FailedStepView{}, Suggestions: []PlanSuggestion{}}
	if !slices.Contains(storage.ResumableStates, execution.State) {
		return view, nil
	}
	view.Stopped, view.Error = true, execution.Error
	plan, err := service.store.ExecutionPlan(ctx, runID)
	if err != nil {
		return RunFailureView{}, fmt.Errorf("load the plan of run %s: %w", runID, err)
	}
	runs, err := service.store.StepRuns(ctx, runID)
	if err != nil {
		return RunFailureView{}, fmt.Errorf("load the step runs of run %s: %w", runID, err)
	}
	checks, err := service.store.Checks(ctx, runID)
	if err != nil {
		return RunFailureView{}, err
	}
	// Read-only agents, by name, so a suggestion to let a step write says
	// when its agent cannot. Without the registry the suggestion still
	// stands, and Go refuses it with the reason when it is applied.
	readOnly := map[string]bool{}
	if task, err := service.store.Task(ctx, execution.TaskID); err == nil {
		if registry, _, err := service.agentRegistry(task.Repository); err == nil {
			for _, definition := range registry.Workers() {
				readOnly[definition.Name] = definition.Permission != domain.PermissionWorktree
			}
		}
	}
	stopped := stoppedRuns(execution, runs)
	for _, step := range plan.Steps {
		run, ok := stopped[step.ID]
		if !ok {
			continue
		}
		state := stoppedState(run, execution)
		failed := FailedStepView{StepID: step.ID, Title: step.Title, State: state, Denials: []DenialView{}, Checks: []FailedCheckView{}}
		if state == domain.StoppedFailed {
			failed.Error = run.Summary
		}
		for _, denial := range run.Denials {
			_, grantable := agentruntime.GrantFor(denial)
			failed.Denials = append(failed.Denials, DenialView{
				PermissionDenial: denial, Grantable: grantable,
				Write: agentruntime.EditTool(denial.Tool) && step.Permission != domain.PermissionWorktree,
			})
		}
		for _, check := range checks {
			if check.StepRunID == run.ID && check.Status == storage.CheckFailed {
				failed.Checks = append(failed.Checks, FailedCheckView{CheckID: check.ID, Argv: check.Argv, ExitCode: check.ExitCode, Error: check.Error})
			}
		}
		if state == domain.HandoffCompleted && len(failed.Denials) == 0 {
			continue
		}
		view.Steps = append(view.Steps, failed)
		view.Suggestions = append(view.Suggestions, suggestFixes(step, failed.Denials, readOnly[step.Agent])...)
	}
	return view, nil
}

// suggestFixes derives a step's suggested plan changes from what it was
// refused: each shell command line that a command can allow exactly, once,
// unless the step already lists it, and write access for a read-only step
// refused a write.
func suggestFixes(step domain.WorkflowStep, denials []DenialView, agentReadOnly bool) []PlanSuggestion {
	var suggestions []PlanSuggestion
	seen := map[string]bool{}
	for _, denial := range denials {
		switch {
		case denial.Write:
			id := "permission:" + step.ID
			if seen[id] {
				continue
			}
			seen[id] = true
			text := fmt.Sprintf("Make step %s worktree-write", step.ID)
			if agentReadOnly {
				text += fmt.Sprintf("; its agent %s is read-only, so give it a writable agent too", step.Agent)
			}
			suggestions = append(suggestions, PlanSuggestion{ID: id, StepID: step.ID, Text: text, Permission: domain.PermissionWorktree})
		case denial.Tool == "Bash" && denial.Command != "":
			argv, ok := agentruntime.AllowedCommandFor(denial.Command)
			line := strings.Join(argv, " ")
			id := "command:" + step.ID + ":" + line
			if !ok || seen[id] || slices.ContainsFunc(step.Commands, func(listed []string) bool { return slices.Equal(listed, argv) }) {
				continue
			}
			seen[id] = true
			suggestions = append(suggestions, PlanSuggestion{ID: id, StepID: step.ID, Text: fmt.Sprintf("Allow %s on step %s", line, step.ID), Command: argv})
		}
	}
	return suggestions
}
