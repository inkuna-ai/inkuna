package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/clock"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// States of a tracker change (docs/mcp-and-trackers.md). A change Go's
// check refuses is refused and never sent; one it accepts is pending until
// a person approves the run, which makes it applying and then applied or
// failed, or requests changes that run its step again, which discards it. A
// failed change can be retried, which makes it applying again.
const (
	TrackerChangeRefused   = "refused"
	TrackerChangePending   = "pending"
	TrackerChangeDiscarded = "discarded"
	TrackerChangeApplying  = "applying"
	TrackerChangeApplied   = "applied"
	TrackerChangeFailed    = "failed"
)

// TrackerChange is one change an agent asked for in its handoff, as its run
// keeps it. Change is the change as Go checked it, as JSON the application
// reads back; storage does not read it. A2ATaskID is the asking step's
// conversation.
type TrackerChange struct {
	ExecutionID string             `json:"executionId"`
	ID          string             `json:"id"`
	Round       int                `json:"round"`
	StepID      string             `json:"stepId"`
	StepRunID   string             `json:"stepRunId"`
	A2ATaskID   string             `json:"a2aTaskId,omitempty"`
	Position    int                `json:"position"`
	Kind        domain.TrackerKind `json:"kind"`
	Issue       string             `json:"issue,omitempty"`
	Change      json.RawMessage    `json:"change"`
	State       string             `json:"state"`
	Reason      string             `json:"reason,omitempty"`
	URL         string             `json:"url,omitempty"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
}

// AddTrackerChanges keeps the changes a step asked for, all or none.
func (store *Store) AddTrackerChanges(ctx context.Context, changes []TrackerChange) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	now := timestamp(clock.Now())
	for _, change := range changes {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO tracker_changes(execution_id,id,round,step_id,step_run_id,a2a_task_id,position,kind,issue,change_json,state,reason,url,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			change.ExecutionID, change.ID, change.Round, change.StepID, change.StepRunID, change.A2ATaskID, change.Position, string(change.Kind), change.Issue,
			string(change.Change), change.State, change.Reason, change.URL, now, now); err != nil {
			return fmt.Errorf("keep tracker change %s of run %s: %w", change.ID, change.ExecutionID, err)
		}
	}
	return transaction.Commit()
}

const trackerChangeColumns = `execution_id,id,round,step_id,step_run_id,a2a_task_id,position,kind,issue,change_json,state,reason,url,created_at,updated_at`

// TrackerChanges lists a run's tracker changes in the order its steps asked
// for them: by round, then as each step ended, then as the step listed them.
func (store *Store) TrackerChanges(ctx context.Context, executionID string) ([]TrackerChange, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT `+trackerChangeColumns+` FROM tracker_changes WHERE execution_id=? ORDER BY round, created_at, position, rowid`, executionID)
	if err != nil {
		return nil, fmt.Errorf("list the tracker changes of run %s: %w", executionID, err)
	}
	defer func() { _ = rows.Close() }()
	changes := make([]TrackerChange, 0)
	for rows.Next() {
		change, err := scanTrackerChange(rows)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

// TrackerChange loads one of a run's tracker changes.
func (store *Store) TrackerChange(ctx context.Context, executionID, id string) (TrackerChange, error) {
	change, err := scanTrackerChange(store.db.QueryRowContext(ctx, `SELECT `+trackerChangeColumns+` FROM tracker_changes WHERE execution_id=? AND id=?`, executionID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return TrackerChange{}, fmt.Errorf("%w: tracker change %s of run %s", domain.ErrNotFound, id, executionID)
	}
	return change, err
}

func scanTrackerChange(row rowScanner) (TrackerChange, error) {
	var change TrackerChange
	var kind, data, created, updated string
	if err := row.Scan(&change.ExecutionID, &change.ID, &change.Round, &change.StepID, &change.StepRunID, &change.A2ATaskID, &change.Position, &kind, &change.Issue,
		&data, &change.State, &change.Reason, &change.URL, &created, &updated); err != nil {
		return TrackerChange{}, err
	}
	change.Kind, change.Change = domain.TrackerKind(kind), json.RawMessage(data)
	change.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	change.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return change, nil
}

// MoveTrackerChanges moves those of the named changes that are in state from
// to state to, with reason, and returns the ids it moved. A change in any
// other state is left as it is, so two requests to apply the same change
// cannot both claim it.
func (store *Store) MoveTrackerChanges(ctx context.Context, executionID string, ids []string, from, to, reason string) ([]string, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Rollback() }()
	now := timestamp(clock.Now())
	moved := make([]string, 0, len(ids))
	for _, id := range ids {
		result, err := transaction.ExecContext(ctx, `UPDATE tracker_changes SET state=?, reason=?, updated_at=? WHERE execution_id=? AND id=? AND state=?`, to, reason, now, executionID, id, from)
		if err != nil {
			return nil, fmt.Errorf("move tracker change %s of run %s to %s: %w", id, executionID, to, err)
		}
		if rows, err := result.RowsAffected(); err == nil && rows == 1 {
			moved = append(moved, id)
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return moved, nil
}

// DiscardTrackerChanges discards the pending changes of the named steps of
// a run, whose steps are about to run again and may ask again, and returns
// how many it discarded.
func (store *Store) DiscardTrackerChanges(ctx context.Context, executionID string, stepIDs []string, reason string) (int, error) {
	if len(stepIDs) == 0 {
		return 0, nil
	}
	args := []any{TrackerChangeDiscarded, reason, timestamp(clock.Now()), executionID, TrackerChangePending}
	for _, id := range stepIDs {
		args = append(args, id)
	}
	result, err := store.db.ExecContext(ctx, `UPDATE tracker_changes SET state=?, reason=?, updated_at=? WHERE execution_id=? AND state=? AND step_id IN (?`+strings.Repeat(",?", len(stepIDs)-1)+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("discard the tracker changes of run %s: %w", executionID, err)
	}
	discarded, err := result.RowsAffected()
	return int(discarded), err
}

// FinishTrackerChange records how applying a change went: applied, with
// what it made, or failed, with the tracker's reason. Only a change being
// applied is finished.
func (store *Store) FinishTrackerChange(ctx context.Context, executionID, id, state, reason, url string) error {
	_, err := store.db.ExecContext(ctx, `UPDATE tracker_changes SET state=?, reason=?, url=?, updated_at=? WHERE execution_id=? AND id=? AND state=?`,
		state, reason, url, timestamp(clock.Now()), executionID, id, TrackerChangeApplying)
	if err != nil {
		return fmt.Errorf("record tracker change %s of run %s: %w", id, executionID, err)
	}
	return nil
}

// AddA2AMessage adds a message to a conversation that may have ended, as
// the last of its messages, and moves the conversation to the top of the
// Messages list. It leaves the task's state alone: Inkuna uses it to tell a
// finished step's thread what became of something it asked for.
func (store *Store) AddA2AMessage(ctx context.Context, taskID string, message a2a.Message) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	var contextID, agent string
	err = transaction.QueryRowContext(ctx, `SELECT context_id, agent FROM a2a_tasks WHERE id=?`, taskID).Scan(&contextID, &agent)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: a2a task %s", domain.ErrNotFound, taskID)
	}
	if err != nil {
		return err
	}
	if message.MessageID == "" {
		message.MessageID = a2a.NewID("msg")
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = clock.Now()
	}
	message.TaskID, message.ContextID = taskID, contextID
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO a2a_messages(message_id,task_id,context_id,agent,role,sequence,message_json,created_at)
VALUES(?,?,?,?,?,(SELECT COALESCE(MAX(sequence), -1) + 1 FROM a2a_messages WHERE task_id=?),?,?)`,
		message.MessageID, taskID, contextID, agent, string(message.Role), taskID, encoded, timestamp(message.Timestamp)); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE a2a_tasks SET updated_at=? WHERE id=?`, timestamp(clock.Now()), taskID); err != nil {
		return err
	}
	return transaction.Commit()
}
