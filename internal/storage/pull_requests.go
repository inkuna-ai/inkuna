package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/inkuna-ai/inkuna/internal/clock"
)

// PullRequestLink is the pull request a project's branch was opened in, as
// Inkuna last found it on GitHub.
type PullRequestLink struct {
	ProjectID string
	Branch    string
	Number    int
	Title     string
	URL       string
	// State is OPEN, CLOSED, or MERGED, as GitHub says it.
	State string
	// MergedAt is zero unless the pull request merged.
	MergedAt  time.Time
	CheckedAt time.Time
}

// Merged reports whether the pull request merged.
func (link PullRequestLink) Merged() bool { return link.State == "MERGED" }

// SavePullRequests records the pull requests a check found for the
// project's branches, replacing what an earlier check found for the same
// branch, and forgets the links of branches no run of the project is on
// any more. A branch the check found nothing for keeps its link.
func (store *Store) SavePullRequests(ctx context.Context, projectID string, links []PullRequestLink) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save pull requests: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, link := range links {
		if link.Branch == "" || link.Number == 0 {
			return errors.New("save pull requests: a link needs a branch and a number")
		}
		if link.CheckedAt.IsZero() {
			link.CheckedAt = clock.Now()
		}
		var merged any
		if !link.MergedAt.IsZero() {
			merged = timestamp(link.MergedAt)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO pull_requests(project_id,branch,number,title,url,state,merged_at,checked_at) VALUES(?,?,?,?,?,?,?,?)`,
			projectID, link.Branch, link.Number, link.Title, link.URL, link.State, merged, timestamp(link.CheckedAt)); err != nil {
			return fmt.Errorf("save the pull request of %s: %w", link.Branch, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pull_requests WHERE project_id=? AND branch NOT IN (SELECT branch FROM executions WHERE project_id=?)`, projectID, projectID); err != nil {
		return fmt.Errorf("forget the pull requests of unused branches: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save pull requests: %w", err)
	}
	return nil
}

// PullRequests lists every pull request link, of every project.
func (store *Store) PullRequests(ctx context.Context) ([]PullRequestLink, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT project_id,branch,number,title,url,state,merged_at,checked_at FROM pull_requests ORDER BY project_id, branch`)
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	links := make([]PullRequestLink, 0)
	for rows.Next() {
		var link PullRequestLink
		var merged sql.NullString
		var checked string
		if err := rows.Scan(&link.ProjectID, &link.Branch, &link.Number, &link.Title, &link.URL, &link.State, &merged, &checked); err != nil {
			return nil, fmt.Errorf("read a pull request: %w", err)
		}
		if merged.Valid {
			link.MergedAt, _ = time.Parse(time.RFC3339Nano, merged.String)
		}
		link.CheckedAt, _ = time.Parse(time.RFC3339Nano, checked)
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	return links, nil
}

// RunBranches lists the branches the project's runs are on, each once.
func (store *Store) RunBranches(ctx context.Context, projectID string) ([]string, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT DISTINCT branch FROM executions WHERE project_id=? AND COALESCE(branch,'')<>'' ORDER BY branch`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list run branches: %w", err)
	}
	defer func() { _ = rows.Close() }()
	branches := make([]string, 0)
	for rows.Next() {
		var branch string
		if err := rows.Scan(&branch); err != nil {
			return nil, fmt.Errorf("read a run branch: %w", err)
		}
		branches = append(branches, branch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list run branches: %w", err)
	}
	return branches, nil
}
