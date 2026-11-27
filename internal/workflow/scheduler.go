package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

var ErrApprovalRequired = errors.New("workflow approval required")

// PausedError reports a round that stopped at approval steps not yet
// approved, once nothing else could run: Gates are those steps. It matches
// ErrApprovalRequired.
type PausedError struct {
	Gates []string
}

func (err *PausedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrApprovalRequired, strings.Join(err.Gates, ", "))
}

func (err *PausedError) Unwrap() error { return ErrApprovalRequired }

// FailedError reports the steps of a round that failed. A round stops after
// the batch its first failure was in, and the other steps of that batch run
// to their end first, so every step that failed is here, in the order they
// failed, each wrapped as "step <id>: ...". A check loop reads them all, since
// one round can send back what several failed checks reported.
type FailedError struct {
	Steps []string
	Errs  []error
}

func (err *FailedError) Error() string {
	messages := make([]string, 0, len(err.Errs))
	for _, failure := range err.Errs {
		messages = append(messages, failure.Error())
	}
	return strings.Join(messages, "; ")
}

func (err *FailedError) Unwrap() []error { return err.Errs }

type Executor func(context.Context, domain.WorkflowStep) (domain.HandoffResult, error)

// Round is what a feedback round runs and what it reuses: the steps in
// Restart run again, and every other step keeps its result from Prior
// (docs/feedback-loop.md).
type Round struct {
	Prior   map[string]domain.HandoffResult
	Restart map[string]bool
}

// Execute runs dependency-ready steps with bounded concurrency. Approval steps
// stop at the trust boundary unless their IDs are explicitly approved: steps
// that do not depend on one still run, and the round then ends with a
// *PausedError naming every approval step it is held at.
func Execute(ctx context.Context, spec domain.WorkflowSpec, approved map[string]bool, executor Executor) (map[string]domain.HandoffResult, error) {
	all := make(map[string]bool, len(spec.Steps))
	for _, step := range spec.Steps {
		all[step.ID] = true
	}
	return ExecuteRound(ctx, spec, approved, Round{Restart: all}, executor)
}

// ExecuteRound runs the steps in round.Restart as Execute runs a whole
// workflow, each once its dependencies have a result, from this round or
// reused from round.Prior. Every step outside the restart set must have a
// prior result, since a step after it may need it.
func ExecuteRound(ctx context.Context, spec domain.WorkflowSpec, approved map[string]bool, round Round, executor Executor) (map[string]domain.HandoffResult, error) {
	if err := domain.ValidateWorkflow(spec); err != nil {
		return nil, err
	}
	if executor == nil {
		return nil, errors.New("executor is required")
	}
	limit := spec.MaxConcurrency
	if limit > 2 {
		limit = 2
	}
	results := make(map[string]domain.HandoffResult, len(spec.Steps))
	pending := make(map[string]domain.WorkflowStep, len(spec.Steps))
	for _, step := range spec.Steps {
		if round.Restart[step.ID] {
			pending[step.ID] = step
			continue
		}
		prior, ok := round.Prior[step.ID]
		if !ok {
			return nil, fmt.Errorf("step %s has no result to reuse and is not run again", step.ID)
		}
		results[step.ID] = prior
	}
	held := make([]string, 0)
	for len(pending) > 0 {
		ready := make([]domain.WorkflowStep, 0)
		for _, step := range pending {
			if slices.Contains(held, step.ID) {
				continue
			}
			dependenciesReady := true
			for _, dependency := range step.DependsOn {
				if _, ok := results[dependency]; !ok {
					dependenciesReady = false
					break
				}
			}
			if !dependenciesReady {
				continue
			}
			// An approval no one has given yet holds the steps after it;
			// work that does not depend on it carries on first, so the
			// person reviews everything that could be done.
			if step.Kind == domain.StepApproval && !approved[step.ID] {
				held = append(held, step.ID)
				continue
			}
			ready = append(ready, step)
		}
		if len(ready) == 0 {
			if len(held) > 0 {
				sort.Strings(held)
				return results, &PausedError{Gates: held}
			}
			return results, errors.New("workflow made no progress")
		}
		sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
		for start := 0; start < len(ready); start += limit {
			end := min(start+limit, len(ready))
			batch := ready[start:end]
			var group sync.WaitGroup
			var mutex sync.Mutex
			failed := &FailedError{}
			for _, step := range batch {
				group.Add(1)
				go func(current domain.WorkflowStep) {
					defer group.Done()
					result, err := executor(ctx, current)
					mutex.Lock()
					defer mutex.Unlock()
					if err != nil {
						failed.Steps = append(failed.Steps, current.ID)
						failed.Errs = append(failed.Errs, fmt.Errorf("step %s: %w", current.ID, err))
						return
					}
					results[current.ID] = result
				}(step)
			}
			group.Wait()
			if len(failed.Steps) > 0 {
				return results, failed
			}
			for _, step := range batch {
				delete(pending, step.ID)
			}
		}
	}
	return results, nil
}

// RestartSet is the steps a round runs: the targets, every step without a
// completed result, and every step downstream of those.
func RestartSet(spec domain.WorkflowSpec, targets []string, completed map[string]bool) map[string]bool {
	restart := make(map[string]bool, len(spec.Steps))
	for _, target := range targets {
		restart[target] = true
	}
	for _, step := range spec.Steps {
		if !completed[step.ID] {
			restart[step.ID] = true
		}
	}
	// A plan validates as acyclic, so passes until nothing changes end.
	for changed := true; changed; {
		changed = false
		for _, step := range spec.Steps {
			if restart[step.ID] {
				continue
			}
			for _, dependency := range step.DependsOn {
				if restart[dependency] {
					restart[step.ID], changed = true, true
					break
				}
			}
		}
	}
	return restart
}
