package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Check statuses. A check is queued until its command starts, then running,
// and ends passed, failed, or cancelled; one whose command never ran is
// skipped.
const (
	CheckQueued    = "queued"
	CheckRunning   = "running"
	CheckPassed    = "passed"
	CheckFailed    = "failed"
	CheckCancelled = "cancelled"
	CheckSkipped   = "skipped"
)

// CheckRecord is one command of an evaluation step.
type CheckRecord struct {
	ID          string `json:"id"`
	ExecutionID string `json:"executionId"`
	StepID      string `json:"stepId"`
	StepRunID   string `json:"stepRunId"`
	// Position is the command's place among its step's commands.
	Position int      `json:"position"`
	Argv     []string `json:"argv"`
	Status   string   `json:"status"`
	// ExitCode is set once the command has exited; a command that could
	// not start, or was stopped, has none.
	ExitCode   *int      `json:"exitCode,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	// LogDigest names the `check` artifact holding the kept log. LogBytes is
	// how much the command wrote, and LogCut says the kept log left part of
	// it out.
	LogDigest string `json:"logDigest,omitempty"`
	LogBytes  int64  `json:"logBytes"`
	LogCut    bool   `json:"logCut,omitempty"`
	// Error says why a command could not start.
	Error string `json:"error,omitempty"`
}

// SaveCheck writes a check, replacing any earlier record of it.
func (store *Store) SaveCheck(ctx context.Context, check CheckRecord) error {
	if check.ID == "" || check.ExecutionID == "" || check.StepID == "" || check.StepRunID == "" || check.Status == "" {
		return errors.New("check id, execution, step, step run, and status are required")
	}
	argv, err := json.Marshal(check.Argv)
	if err != nil {
		return fmt.Errorf("encode check argv: %w", err)
	}
	exit := sql.NullInt64{}
	if check.ExitCode != nil {
		exit = sql.NullInt64{Int64: int64(*check.ExitCode), Valid: true}
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO checks(id,execution_id,step_id,step_run_id,position,argv,status,exit_code,started_at,finished_at,log_digest,log_bytes,log_cut,error) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET status=excluded.status, exit_code=excluded.exit_code, started_at=excluded.started_at, finished_at=excluded.finished_at, log_digest=excluded.log_digest, log_bytes=excluded.log_bytes, log_cut=excluded.log_cut, error=excluded.error`,
		check.ID, check.ExecutionID, check.StepID, check.StepRunID, check.Position, string(argv), check.Status, exit,
		formatTime(check.StartedAt), formatTime(check.FinishedAt), check.LogDigest, check.LogBytes, check.LogCut, check.Error)
	return err
}

// Checks lists a run's checks in the order its steps recorded them.
func (store *Store) Checks(ctx context.Context, executionID string) ([]CheckRecord, error) {
	return store.checks(ctx, `WHERE execution_id=? ORDER BY rowid`, executionID)
}

// StepRunChecks lists the checks of one evaluation step run, in order.
func (store *Store) StepRunChecks(ctx context.Context, stepRunID string) ([]CheckRecord, error) {
	return store.checks(ctx, `WHERE step_run_id=? ORDER BY position`, stepRunID)
}

// Check reads one check.
func (store *Store) Check(ctx context.Context, id string) (CheckRecord, error) {
	checks, err := store.checks(ctx, `WHERE id=?`, id)
	if err != nil {
		return CheckRecord{}, err
	}
	if len(checks) == 0 {
		return CheckRecord{}, fmt.Errorf("check %s not found", id)
	}
	return checks[0], nil
}

// ChecksFor lists the checks of several runs, by run, for the run lists.
func (store *Store) ChecksFor(ctx context.Context, executionIDs []string) (map[string][]CheckRecord, error) {
	byRun := make(map[string][]CheckRecord, len(executionIDs))
	if len(executionIDs) == 0 {
		return byRun, nil
	}
	args := make([]any, len(executionIDs))
	for index, id := range executionIDs {
		args[index] = id
	}
	checks, err := store.checks(ctx, `WHERE execution_id IN (?`+strings.Repeat(",?", len(executionIDs)-1)+`) ORDER BY rowid`, args...)
	if err != nil {
		return nil, err
	}
	for _, check := range checks {
		byRun[check.ExecutionID] = append(byRun[check.ExecutionID], check)
	}
	return byRun, nil
}

func (store *Store) checks(ctx context.Context, where string, args ...any) ([]CheckRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,execution_id,step_id,step_run_id,position,argv,status,exit_code,started_at,finished_at,log_digest,log_bytes,log_cut,error FROM checks `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query checks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	checks := make([]CheckRecord, 0)
	for rows.Next() {
		var check CheckRecord
		var argv, started, finished string
		var exit sql.NullInt64
		if err := rows.Scan(&check.ID, &check.ExecutionID, &check.StepID, &check.StepRunID, &check.Position, &argv, &check.Status, &exit, &started, &finished, &check.LogDigest, &check.LogBytes, &check.LogCut, &check.Error); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(argv), &check.Argv); err != nil {
			return nil, fmt.Errorf("decode the argv of check %s: %w", check.ID, err)
		}
		if exit.Valid {
			code := int(exit.Int64)
			check.ExitCode = &code
		}
		check.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		check.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
		checks = append(checks, check)
	}
	return checks, rows.Err()
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return timestamp(value)
}
