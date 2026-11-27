package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// SaveExecutionStory keeps a run's story in its repository's tracker and
// the tracker settings the run was approved under, as JSON the application
// reads back with ExecutionStory. Storage does not read it.
func (store *Store) SaveExecutionStory(ctx context.Context, executionID string, story []byte) error {
	result, err := store.db.ExecContext(ctx, `UPDATE executions SET story_json=? WHERE id=?`, string(story), executionID)
	if err != nil {
		return fmt.Errorf("save the story of run %s: %w", executionID, err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return fmt.Errorf("%w: execution %s", domain.ErrNotFound, executionID)
	}
	return nil
}

// ExecutionStory is what SaveExecutionStory kept for a run, or nil when the
// run has no story or is from before stories were kept.
func (store *Store) ExecutionStory(ctx context.Context, executionID string) ([]byte, error) {
	var story string
	err := store.db.QueryRowContext(ctx, `SELECT story_json FROM executions WHERE id=?`, executionID).Scan(&story)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: execution %s", domain.ErrNotFound, executionID)
	}
	if err != nil {
		return nil, fmt.Errorf("load the story of run %s: %w", executionID, err)
	}
	if story == "" {
		return nil, nil
	}
	return []byte(story), nil
}
