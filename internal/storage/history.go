package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// StepRun is how one round's run of a step went, as its row records it.
// FinishedAt is zero while it has not ended, and Summary is its handoff's
// summary once it has. Denials are the tool calls its latest turn was
// refused (SetStepDenials).
type StepRun struct {
	ID         string                    `json:"id"`
	StepID     string                    `json:"stepId"`
	Round      int                       `json:"round"`
	Runtime    domain.Runtime            `json:"runtime"`
	State      string                    `json:"state"`
	Summary    string                    `json:"summary,omitempty"`
	StartedAt  time.Time                 `json:"startedAt"`
	FinishedAt time.Time                 `json:"finishedAt,omitempty"`
	Denials    []domain.PermissionDenial `json:"denials,omitempty"`
}

// StepRuns lists every step run of a run, its steps' own and never their
// peers', in the order they started.
func (store *Store) StepRuns(ctx context.Context, executionID string) ([]StepRun, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, step_id, round, runtime, state, started_at, COALESCE(finished_at, ''), handoff_json, denials_json FROM runs
WHERE execution_id=? AND `+ownStepRun+`
ORDER BY round, started_at, rowid`, executionID)
	if err != nil {
		return nil, fmt.Errorf("list the step runs of %s: %w", executionID, err)
	}
	defer func() { _ = rows.Close() }()
	runs := make([]StepRun, 0)
	for rows.Next() {
		var run StepRun
		var started, finished string
		var handoff []byte
		var denials string
		if err := rows.Scan(&run.ID, &run.StepID, &run.Round, &run.Runtime, &run.State, &started, &finished, &handoff, &denials); err != nil {
			return nil, fmt.Errorf("read a step run of %s: %w", executionID, err)
		}
		run.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		run.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
		if handoff != nil {
			var result domain.HandoffResult
			if err := json.Unmarshal(handoff, &result); err != nil {
				return nil, fmt.Errorf("decode the result of %s: %w", run.ID, err)
			}
			run.Summary = result.Summary
		}
		if denials != "" {
			if err := json.Unmarshal([]byte(denials), &run.Denials); err != nil {
				return nil, fmt.Errorf("decode the denials of %s: %w", run.ID, err)
			}
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list the step runs of %s: %w", executionID, err)
	}
	return runs, nil
}

// SetStepDenials records the tool calls a step run's latest turn was
// refused, replacing those of the turn before, so a denial the person
// granted is gone once the step retries without it.
func (store *Store) SetStepDenials(ctx context.Context, stepRunID string, denials []domain.PermissionDenial) error {
	data := ""
	if len(denials) > 0 {
		encoded, err := json.Marshal(denials)
		if err != nil {
			return err
		}
		data = string(encoded)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE runs SET denials_json=? WHERE id=?`, data, stepRunID); err != nil {
		return fmt.Errorf("record the denials of %s: %w", stepRunID, err)
	}
	return nil
}

// RunDirectories lists the directories a run's step runs worked in,
// peers' included, in the order the run first used each.
func (store *Store) RunDirectories(ctx context.Context, executionID string) ([]string, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT worktree FROM runs WHERE execution_id=? AND COALESCE(worktree, '') != ''
GROUP BY worktree ORDER BY MIN(rowid)`, executionID)
	if err != nil {
		return nil, fmt.Errorf("list the directories of %s: %w", executionID, err)
	}
	defer func() { _ = rows.Close() }()
	directories := make([]string, 0, 2)
	for rows.Next() {
		var directory string
		if err := rows.Scan(&directory); err != nil {
			return nil, fmt.Errorf("list the directories of %s: %w", executionID, err)
		}
		directories = append(directories, directory)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list the directories of %s: %w", executionID, err)
	}
	return directories, nil
}

// RoundEvents returns the last limit events of one round of a run, its
// steps' and their peers', oldest first, as they were stored, and how many
// earlier ones it left out.
func (store *Store) RoundEvents(ctx context.Context, executionID string, round, limit int) ([]domain.RunEvent, int, error) {
	var total int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM events JOIN runs ON runs.id = events.run_id
WHERE runs.execution_id=? AND runs.round=?`, executionID, round).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count the events of round %d: %w", round, err)
	}
	// Rows are numbered as they are stored, which is the order the live
	// timeline showed them in.
	rows, err := store.db.QueryContext(ctx, `SELECT event_json FROM (
  SELECT events.rowid AS position, events.event_json FROM events JOIN runs ON runs.id = events.run_id
  WHERE runs.execution_id=? AND runs.round=? ORDER BY events.rowid DESC LIMIT ?
) ORDER BY position`, executionID, round, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list the events of round %d: %w", round, err)
	}
	defer func() { _ = rows.Close() }()
	events := make([]domain.RunEvent, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, 0, fmt.Errorf("read an event of round %d: %w", round, err)
		}
		var event domain.RunEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, 0, fmt.Errorf("decode an event of round %d: %w", round, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list the events of round %d: %w", round, err)
	}
	return events, total - len(events), nil
}

// PreviousAnswer is what the user last answered when the same step of the
// same run asked the same question before, as a human-input stage asks
// again in each round that re-runs it, or empty when it never did. Reviews
// are left out: a review's reply is feedback, not an answer.
func (store *Store) PreviousAnswer(ctx context.Context, input InputRequest) (string, error) {
	var answer string
	err := store.db.QueryRowContext(ctx, `SELECT COALESCE(answer, '') FROM inputs
WHERE execution_id=? AND step_id=? AND question=? AND state=? AND review=0 AND id<>?
ORDER BY rowid DESC LIMIT 1`,
		input.ExecutionID, input.StepID, input.Question, InputAnswered, input.ID).Scan(&answer)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find the previous answer to input %s: %w", input.ID, err)
	}
	return answer, nil
}
