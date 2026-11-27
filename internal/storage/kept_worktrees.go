package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/inkuna-ai/inkuna/internal/clock"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// KeptWorktree is a worktree a delete left on disk, recorded because the
// run that made it is gone and nothing else says the directory is Inkuna's.
type KeptWorktree struct {
	Path      string `json:"path"`
	ProjectID string `json:"projectId"`
	Branch    string `json:"branch,omitempty"`
	// TaskID and RunID name the deleted run, for the person to recognise;
	// neither row exists any more.
	TaskID string `json:"taskId"`
	RunID  string `json:"runId"`
	Title  string `json:"title"`
	// Reason is why the delete kept it, as its report said.
	Reason string    `json:"reason"`
	KeptAt time.Time `json:"keptAt"`
}

// KeepWorktree records a worktree a delete kept, replacing an earlier record
// of the same path.
func (store *Store) KeepWorktree(ctx context.Context, kept KeptWorktree) error {
	if kept.Path == "" {
		return errors.New("record a kept worktree: the path is required")
	}
	if kept.KeptAt.IsZero() {
		kept.KeptAt = clock.Now()
	}
	_, err := store.db.ExecContext(ctx, `INSERT OR REPLACE INTO kept_worktrees(path,project_id,branch,task_id,run_id,title,reason,kept_at) VALUES(?,?,?,?,?,?,?,?)`,
		kept.Path, kept.ProjectID, kept.Branch, kept.TaskID, kept.RunID, kept.Title, kept.Reason, timestamp(kept.KeptAt))
	if err != nil {
		return fmt.Errorf("record kept worktree %s: %w", kept.Path, err)
	}
	return nil
}

// KeptWorktrees lists the worktrees deletes kept, the most recent first.
func (store *Store) KeptWorktrees(ctx context.Context) ([]KeptWorktree, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT path,project_id,branch,task_id,run_id,title,reason,kept_at FROM kept_worktrees ORDER BY kept_at DESC, path`)
	if err != nil {
		return nil, fmt.Errorf("list kept worktrees: %w", err)
	}
	defer func() { _ = rows.Close() }()
	kept := make([]KeptWorktree, 0)
	for rows.Next() {
		record, err := scanKeptWorktree(rows)
		if err != nil {
			return nil, fmt.Errorf("list kept worktrees: %w", err)
		}
		kept = append(kept, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list kept worktrees: %w", err)
	}
	return kept, nil
}

// KeptWorktreeAt returns the record of the worktree a delete kept at path,
// or an error wrapping domain.ErrNotFound when there is none.
func (store *Store) KeptWorktreeAt(ctx context.Context, path string) (KeptWorktree, error) {
	record, err := scanKeptWorktree(store.db.QueryRowContext(ctx, `SELECT path,project_id,branch,task_id,run_id,title,reason,kept_at FROM kept_worktrees WHERE path=?`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return KeptWorktree{}, fmt.Errorf("%w: kept worktree %s", domain.ErrNotFound, path)
	}
	if err != nil {
		return KeptWorktree{}, fmt.Errorf("load kept worktree %s: %w", path, err)
	}
	return record, nil
}

// ForgetKeptWorktree drops the record of a kept worktree once it is gone or
// a run works in it again. Forgetting one with no record is not an error.
func (store *Store) ForgetKeptWorktree(ctx context.Context, path string) error {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM kept_worktrees WHERE path=?`, path); err != nil {
		return fmt.Errorf("forget kept worktree %s: %w", path, err)
	}
	return nil
}

func scanKeptWorktree(row interface{ Scan(...any) error }) (KeptWorktree, error) {
	var record KeptWorktree
	var keptAt string
	if err := row.Scan(&record.Path, &record.ProjectID, &record.Branch, &record.TaskID, &record.RunID, &record.Title, &record.Reason, &keptAt); err != nil {
		return KeptWorktree{}, err
	}
	record.KeptAt, _ = time.Parse(time.RFC3339Nano, keptAt)
	return record, nil
}
