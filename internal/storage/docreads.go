package storage

import (
	"context"
	"errors"
	"time"

	"github.com/inkuna-ai/inkuna/internal/clock"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// RecordDocRead stores that an agent opened a doc, or searched a directory
// of docs, during a task. Reads keep the order they are recorded in.
func (store *Store) RecordDocRead(ctx context.Context, read domain.DocRead) error {
	if read.TaskID == "" || read.RunID == "" || read.ProjectID == "" || read.DocsDir == "" || read.DocPath == "" {
		return errors.New("doc read task, run, project, docs directory, and path are required")
	}
	if read.OpenedAt.IsZero() {
		read.OpenedAt = clock.Now()
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO doc_reads(task_id,step_id,run_id,project_id,agent,runtime,docs_dir,doc_path,tool,opened_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		read.TaskID, read.StepID, read.RunID, read.ProjectID, read.Agent, string(read.Runtime), read.DocsDir, read.DocPath, read.Tool, timestamp(read.OpenedAt))
	return err
}

// DocReadsForTask lists a task's doc reads in the order they happened.
func (store *Store) DocReadsForTask(ctx context.Context, taskID string) ([]domain.DocRead, error) {
	return store.docReads(ctx, `WHERE task_id=? ORDER BY id ASC`, taskID)
}

// DocReadsForDoc lists the latest reads of one doc in a project, newest
// first, at most limit of them. The doc is its path under docsDir, the docs
// root relative to the repository: a read made while the root was
// elsewhere was of another file.
func (store *Store) DocReadsForDoc(ctx context.Context, projectID, docsDir, docPath string, limit int) ([]domain.DocRead, error) {
	if limit <= 0 {
		limit = 100
	}
	return store.docReads(ctx, `WHERE project_id=? AND docs_dir=? AND doc_path=? ORDER BY id DESC LIMIT ?`, projectID, docsDir, docPath, limit)
}

func (store *Store) docReads(ctx context.Context, where string, args ...any) ([]domain.DocRead, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT task_id,step_id,run_id,project_id,agent,runtime,docs_dir,doc_path,tool,opened_at FROM doc_reads `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	reads := make([]domain.DocRead, 0)
	for rows.Next() {
		var read domain.DocRead
		var runtime, opened string
		if err := rows.Scan(&read.TaskID, &read.StepID, &read.RunID, &read.ProjectID, &read.Agent, &runtime, &read.DocsDir, &read.DocPath, &read.Tool, &opened); err != nil {
			return nil, err
		}
		read.Runtime = domain.Runtime(runtime)
		read.OpenedAt, _ = time.Parse(time.RFC3339Nano, opened)
		reads = append(reads, read)
	}
	return reads, rows.Err()
}
