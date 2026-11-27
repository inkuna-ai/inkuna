package domain

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrInvalid  = errors.New("invalid contract")
	ErrNotFound = errors.New("not found")
)

func ValidateTask(task TaskSpec) error {
	if task.Version != ContractVersion {
		return fmt.Errorf("%w: unsupported task version %d", ErrInvalid, task.Version)
	}
	if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.Objective) == "" {
		return fmt.Errorf("%w: task id and objective are required", ErrInvalid)
	}
	if !validTaskKind(task.Kind) {
		return fmt.Errorf("%w: unknown task kind %q", ErrInvalid, task.Kind)
	}
	if task.Title != "" {
		if clean, err := CleanTitle(task.Title); err != nil {
			return err
		} else if clean != task.Title {
			return fmt.Errorf("%w: the title must be one trimmed line", ErrInvalid)
		}
	}
	if task.Budget.WallTimeSeconds <= 0 {
		return fmt.Errorf("%w: positive wall time is required", ErrInvalid)
	}
	return nil
}

func ValidateWorkflow(workflow WorkflowSpec) error {
	if workflow.Version != ContractVersion || workflow.ID == "" || workflow.TaskID == "" {
		return fmt.Errorf("%w: workflow version, id, and task id are required", ErrInvalid)
	}
	if !validRuntime(workflow.DefaultRuntime) {
		return fmt.Errorf("%w: invalid default runtime %q", ErrInvalid, workflow.DefaultRuntime)
	}
	if workflow.MaxConcurrency < 1 {
		return fmt.Errorf("%w: max concurrency must be positive", ErrInvalid)
	}
	steps := make(map[string]WorkflowStep, len(workflow.Steps))
	for _, step := range workflow.Steps {
		if step.ID == "" || step.Title == "" {
			return fmt.Errorf("%w: every step needs id and title", ErrInvalid)
		}
		if _, exists := steps[step.ID]; exists {
			return fmt.Errorf("%w: duplicate step %q", ErrInvalid, step.ID)
		}
		if !validStepKind(step.Kind) || !validPermission(step.Permission) {
			return fmt.Errorf("%w: invalid kind or permission on step %q", ErrInvalid, step.ID)
		}
		if step.Runtime != "" && !validRuntime(step.Runtime) {
			return fmt.Errorf("%w: invalid runtime on step %q", ErrInvalid, step.ID)
		}
		if step.Complexity != nil {
			if step.Complexity.Score < 1 || step.Complexity.Score > 10 || strings.TrimSpace(step.Complexity.Rationale) == "" {
				return fmt.Errorf("%w: step %q complexity needs a 1-10 score and rationale", ErrInvalid, step.ID)
			}
		}
		if step.ModelSelection != nil {
			if strings.TrimSpace(step.ModelSelection.Source) == "" {
				return fmt.Errorf("%w: step %q model selection needs a source", ErrInvalid, step.ID)
			}
			// The policy may leave the model to the CLI when the runtime has no
			// bands, and a remote agent's model is its own business; an
			// override exists only to name one.
			if strings.TrimSpace(step.ModelSelection.Model) == "" && step.ModelSelection.Source != "policy" && step.ModelSelection.Source != ModelSourceRemote {
				return fmt.Errorf("%w: step %q model selection needs a model", ErrInvalid, step.ID)
			}
		}
		if step.Runtime == RuntimeRemote && (step.Kind != StepAgent || step.Permission != PermissionReadOnly) {
			return fmt.Errorf("%w: step %q uses the remote runtime, which only staffs read-only agent steps", ErrInvalid, step.ID)
		}
		if step.Kind == StepHumanInput && strings.TrimSpace(step.Prompt) == "" {
			return fmt.Errorf("%w: human-input step %q needs a prompt", ErrInvalid, step.ID)
		}
		if step.Permission == PermissionWorktree && len(step.AllowedPaths) == 0 {
			return fmt.Errorf("%w: writable step %q needs allowed paths", ErrInvalid, step.ID)
		}
		for _, allowed := range step.AllowedPaths {
			if err := CheckAllowedPath(allowed); err != nil {
				return fmt.Errorf("%w: step %q: %w", ErrInvalid, step.ID, err)
			}
		}
		steps[step.ID] = step
	}
	for _, step := range workflow.Steps {
		for _, dependency := range step.DependsOn {
			if _, ok := steps[dependency]; !ok {
				return fmt.Errorf("%w: step %q depends on missing step %q", ErrInvalid, step.ID, dependency)
			}
		}
	}
	if cycle := findCycle(steps); len(cycle) > 0 {
		return fmt.Errorf("%w: workflow cycle: %s", ErrInvalid, strings.Join(cycle, " -> "))
	}
	for _, step := range workflow.Steps {
		if err := validateFeedback(step, steps); err != nil {
			return err
		}
	}
	return nil
}

// CheckAllowedPath refuses an allowed path that does not stay inside the
// repository: an empty one, an absolute one or one on a drive, or one that
// climbs out with "..". Paths come from rules and from the approval screen,
// and may be written with either separator, so both are read as one.
func CheckAllowedPath(allowed string) error {
	if strings.TrimSpace(allowed) == "" {
		return errors.New("an allowed path is empty; write . for the whole repository")
	}
	slashed := strings.ReplaceAll(allowed, `\`, "/")
	cleaned := path.Clean(slashed)
	// C:x is relative to another directory on Windows, so it is as far out
	// as C:\x.
	drive := len(allowed) >= 2 && allowed[1] == ':'
	if filepath.IsAbs(allowed) || drive || strings.HasPrefix(slashed, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("allowed path %q is outside the repository; paths are relative to its root", allowed)
	}
	return nil
}

// validateFeedback checks a step's review gate and check loop against the
// plan itself, since a plan arrives from the view and the store as well as
// from the rules, and feedback must only ever reach a writable agent step
// upstream.
func validateFeedback(step WorkflowStep, steps map[string]WorkflowStep) error {
	if step.Review && step.Kind != StepApproval {
		return fmt.Errorf("%w: step %q pauses for review, which only an approval step does", ErrInvalid, step.ID)
	}
	if step.OnFailure != "" && step.Kind != StepEvaluation {
		return fmt.Errorf("%w: step %q sets onFailure, which only an evaluation step has", ErrInvalid, step.ID)
	}
	switch step.OnFailure {
	case "", FailureFail:
		if step.MaxRounds != 0 {
			return fmt.Errorf("%w: step %q caps feedback rounds but does not send its failures back", ErrInvalid, step.ID)
		}
		if step.Kind == StepEvaluation && step.FeedbackTarget != "" {
			return fmt.Errorf("%w: step %q names a feedback target but does not send its failures back", ErrInvalid, step.ID)
		}
		if len(step.SystemExitCodes) > 0 {
			return fmt.Errorf("%w: step %q names system exit codes but does not send its failures back", ErrInvalid, step.ID)
		}
	case FailureFeedback:
		if step.FeedbackTarget == "" {
			return fmt.Errorf("%w: step %q sends its failures back but names no target", ErrInvalid, step.ID)
		}
		if step.MaxRounds < 1 || step.MaxRounds > MaxFeedbackRounds {
			return fmt.Errorf("%w: step %q allows %d feedback rounds; it must be from 1 to %d", ErrInvalid, step.ID, step.MaxRounds, MaxFeedbackRounds)
		}
		if err := ValidateSystemExitCodes(step.SystemExitCodes); err != nil {
			return fmt.Errorf("%w: step %q: %w", ErrInvalid, step.ID, err)
		}
	default:
		return fmt.Errorf("%w: step %q has invalid onFailure %q", ErrInvalid, step.ID, step.OnFailure)
	}
	if step.FeedbackTarget == "" {
		return nil
	}
	if step.Kind != StepApproval && step.Kind != StepEvaluation {
		return fmt.Errorf("%w: step %q names a feedback target, which only approval and evaluation steps have", ErrInvalid, step.ID)
	}
	target, ok := steps[step.FeedbackTarget]
	switch {
	case !ok:
		return fmt.Errorf("%w: step %q sends feedback to missing step %q", ErrInvalid, step.ID, step.FeedbackTarget)
	case !upstream(steps, step.ID, target.ID):
		return fmt.Errorf("%w: step %q sends feedback to %q, which is not upstream of it", ErrInvalid, step.ID, target.ID)
	case target.Kind != StepAgent || target.Permission != PermissionWorktree:
		return fmt.Errorf("%w: step %q sends feedback to %q, which is not a writable agent step", ErrInvalid, step.ID, target.ID)
	}
	return nil
}

// upstream reports whether target is among the steps from depends on,
// directly or through others. The plan has no cycles by now.
func upstream(steps map[string]WorkflowStep, from, target string) bool {
	seen := make(map[string]bool)
	pending := append([]string(nil), steps[from].DependsOn...)
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if id == target {
			return true
		}
		if !seen[id] {
			seen[id] = true
			pending = append(pending, steps[id].DependsOn...)
		}
	}
	return false
}

// ValidateRoutedWorkflow applies to an approved execution plan. Drafts can be
// topology-valid before scoring, but every agent step must be scored and have
// an explicit runtime/model before execution.
func ValidateRoutedWorkflow(workflow WorkflowSpec) error {
	if err := ValidateWorkflow(workflow); err != nil {
		return err
	}
	if workflow.RoutingPolicyVersion < 1 {
		return fmt.Errorf("%w: routed workflow needs a policy version", ErrInvalid)
	}
	for _, step := range workflow.Steps {
		if step.Kind != StepAgent {
			continue
		}
		if step.Complexity == nil {
			return fmt.Errorf("%w: agent step %q needs a complexity assessment", ErrInvalid, step.ID)
		}
		if step.Runtime == "" || step.ModelSelection == nil {
			return fmt.Errorf("%w: agent step %q needs an explicit runtime and model", ErrInvalid, step.ID)
		}
	}
	return nil
}

func findCycle(steps map[string]WorkflowStep) []string {
	state := make(map[string]uint8, len(steps))
	path := make([]string, 0, len(steps))
	var visit func(string) []string
	visit = func(id string) []string {
		if state[id] == 1 {
			for index, entry := range path {
				if entry == id {
					return append(append([]string{}, path[index:]...), id)
				}
			}
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		path = append(path, id)
		for _, dependency := range steps[id].DependsOn {
			if cycle := visit(dependency); len(cycle) > 0 {
				return cycle
			}
		}
		path = path[:len(path)-1]
		state[id] = 2
		return nil
	}
	for id := range steps {
		if cycle := visit(id); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}

func validTaskKind(kind TaskKind) bool {
	switch kind {
	case TaskResearch, TaskBugfix, TaskImplementation, TaskReview, TaskDocumentation, TaskSkillAuthoring:
		return true
	default:
		return false
	}
}

func validStepKind(kind StepKind) bool {
	switch kind {
	case StepAgent, StepScript, StepEvaluation, StepApproval, StepHumanInput:
		return true
	default:
		return false
	}
}

func validPermission(permission Permission) bool {
	return permission == PermissionReadOnly || permission == PermissionWorktree
}

// runtimeName keeps runtime names safe as map keys, YAML keys, and labels.
var runtimeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidRuntimeName reports whether a runtime name is well formed. Runtimes are
// declared in configuration, so whether one with that name exists is checked
// where the declarations are known: planning, saving, and starting a run.
func ValidRuntimeName(runtime Runtime) bool { return runtimeName.MatchString(string(runtime)) }

func validRuntime(runtime Runtime) bool { return ValidRuntimeName(runtime) }

// ValidateSystemExitCodes checks the exit codes an evaluation step names as
// system failures: each from 1 to MaxExitCode, since 0 is a pass, and none
// twice.
func ValidateSystemExitCodes(codes []int) error {
	seen := make(map[int]bool, len(codes))
	for _, code := range codes {
		if code < 1 || code > MaxExitCode {
			return fmt.Errorf("system exit code %d must be from 1 to %d", code, MaxExitCode)
		}
		if seen[code] {
			return fmt.Errorf("system exit code %d is listed twice", code)
		}
		seen[code] = true
	}
	return nil
}
