package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/inkuna-ai/inkuna/internal/clock"
)

// ViewedFile is a file marked viewed in a run's changes, at the blob it had
// when it was marked.
type ViewedFile struct {
	Path string `json:"path"`
	Blob string `json:"blob"`
}

// ViewedFiles lists a run's viewed marks.
func (store *Store) ViewedFiles(ctx context.Context, executionID string) ([]ViewedFile, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT path,blob FROM viewed_files WHERE execution_id=? ORDER BY path, blob`, executionID)
	if err != nil {
		return nil, fmt.Errorf("query viewed files of run %s: %w", executionID, err)
	}
	defer func() { _ = rows.Close() }()
	files := make([]ViewedFile, 0)
	for rows.Next() {
		var file ViewedFile
		if err := rows.Scan(&file.Path, &file.Blob); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

// SetViewed marks a file viewed at a blob, or clears that mark. Marks on the
// file's other blobs are left alone.
func (store *Store) SetViewed(ctx context.Context, executionID string, file ViewedFile, viewed bool) error {
	if executionID == "" || file.Path == "" || file.Blob == "" {
		return errors.New("viewed file run, path, and blob are required")
	}
	if !viewed {
		_, err := store.db.ExecContext(ctx, `DELETE FROM viewed_files WHERE execution_id=? AND path=? AND blob=?`, executionID, file.Path, file.Blob)
		return err
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO viewed_files(execution_id,path,blob,viewed_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`,
		executionID, file.Path, file.Blob, timestamp(clock.Now()))
	return err
}
