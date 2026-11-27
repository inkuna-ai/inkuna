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

// StepSession is the runtime session a step's worker last ran in: its id,
// the runtime that keeps it, the working directory it ran in, which Claude
// Code finds it by, when its prompt cache expires (zero when resuming costs
// the same at any time), and the totals the runtime reported for it, which
// a resumed call's totals include. Round is the round of the step run that
// left it.
type StepSession struct {
	Session    string         `json:"session"`
	Runtime    domain.Runtime `json:"runtime"`
	WorkingDir string         `json:"workingDir"`
	CacheUntil time.Time      `json:"cacheUntil"`
	Usage      domain.Usage   `json:"usage"`
	Round      int            `json:"round"`
}

// SaveSession records the session a step run's worker last ran in, after
// each of its turns, so a later round can resume it.
func (store *Store) SaveSession(ctx context.Context, runID string, session StepSession) error {
	usage, err := json.Marshal(session.Usage)
	if err != nil {
		return err
	}
	cacheUntil := ""
	if !session.CacheUntil.IsZero() {
		cacheUntil = timestamp(session.CacheUntil)
	}
	_, err = store.db.ExecContext(ctx, `UPDATE runs SET session=?, session_runtime=?, cache_until=?, session_usage=? WHERE id=?`,
		session.Session, session.Runtime, cacheUntil, string(usage), runID)
	return err
}

// LastSession returns the session a step's own run left in the latest round
// before the given one that ran the step, and false when that run kept
// none: its runtime cannot resume, or it stopped before its first turn
// ended.
func (store *Store) LastSession(ctx context.Context, executionID, stepID string, before int) (StepSession, bool, error) {
	var session StepSession
	var worktree sql.NullString
	var cacheUntil, usage string
	err := store.db.QueryRowContext(ctx, `SELECT session, session_runtime, worktree, cache_until, session_usage, round FROM runs
WHERE execution_id=? AND step_id=? AND round<? AND `+ownStepRun+`
ORDER BY round DESC LIMIT 1`, executionID, stepID, before).Scan(&session.Session, &session.Runtime, &worktree, &cacheUntil, &usage, &session.Round)
	if errors.Is(err, sql.ErrNoRows) {
		return StepSession{}, false, nil
	}
	if err != nil {
		return StepSession{}, false, err
	}
	if session.Session == "" {
		return StepSession{}, false, nil
	}
	session.WorkingDir = worktree.String
	if cacheUntil != "" {
		if session.CacheUntil, err = time.Parse(time.RFC3339Nano, cacheUntil); err != nil {
			return StepSession{}, false, fmt.Errorf("read the cache expiry of step %s: %w", stepID, err)
		}
	}
	if usage != "" {
		if err := json.Unmarshal([]byte(usage), &session.Usage); err != nil {
			return StepSession{}, false, fmt.Errorf("read the session usage of step %s: %w", stepID, err)
		}
	}
	return session, true, nil
}

// StepRound is how one round's run of a step ended.
type StepRound struct {
	Round   int                  `json:"round"`
	State   string               `json:"state"`
	Handoff domain.HandoffResult `json:"handoff"`
}

// StepRounds lists the ended runs of one step of a run, its own and never a
// peer's, oldest round first. A run still going, or one that left no
// handoff, is left out.
func (store *Store) StepRounds(ctx context.Context, executionID, stepID string) ([]StepRound, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT round, state, handoff_json FROM runs
WHERE execution_id=? AND step_id=? AND finished_at IS NOT NULL AND handoff_json IS NOT NULL AND `+ownStepRun+`
ORDER BY round`, executionID, stepID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	rounds := make([]StepRound, 0)
	for rows.Next() {
		var round StepRound
		var data []byte
		if err := rows.Scan(&round.Round, &round.State, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &round.Handoff); err != nil {
			return nil, fmt.Errorf("decode round %d of step %s: %w", round.Round, stepID, err)
		}
		rounds = append(rounds, round)
	}
	return rounds, rows.Err()
}
