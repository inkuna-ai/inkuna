package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// RunEnded reports whether a run in this state has ended, so that its task
// can be deleted. It names the ended states rather than the active ones, so
// a state added later keeps its task until someone decides otherwise.
func RunEnded(state string) bool {
	switch state {
	case "completed", "failed", "cancelled", "interrupted":
		return true
	}
	return false
}

// DeleteRefusedError says why a delete was refused. Nothing was deleted.
type DeleteRefusedError struct {
	TaskID string
	RunID  string
	// Reason completes "run <RunID> ...", or "task <TaskID> ..." when the
	// refusal is not about one run.
	Reason string
}

func (err *DeleteRefusedError) Error() string {
	if err.RunID == "" {
		return fmt.Sprintf("task %s cannot be deleted: it %s", err.TaskID, err.Reason)
	}
	return fmt.Sprintf("task %s cannot be deleted: run %s %s", err.TaskID, err.RunID, err.Reason)
}

// ActiveReason says what a run in a state that has not ended is doing, and
// what to do before its task can be deleted.
func ActiveReason(state string) string {
	switch state {
	case "queued":
		return "is queued; delete it once the run has ended"
	case "running":
		return "is running; delete it once the run has ended"
	case "waiting":
		return "is waiting for an answer or a permission; answer or cancel it first"
	case "reviewing":
		return "is paused for review; approve, send back, or cancel it first"
	case "cancelling":
		return "is being cancelled; delete it once it has stopped"
	}
	return "is " + state + "; delete it once the run has ended"
}

// HistoryTask is a task as its history lists it, with every run it had.
type HistoryTask struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	// Title is the latest run's title, else the plan's, else the objective.
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	// Runs are the task's runs, oldest first. A task whose plan never ran
	// has none.
	Runs []Execution `json:"runs"`
}

// Finished reports whether none of the task's runs is active, which is when
// it can be deleted. A plan that never ran counts as finished.
func (task HistoryTask) Finished() bool {
	for _, run := range task.Runs {
		if !RunEnded(run.State) {
			return false
		}
	}
	return true
}

// HistoryTasks lists tasks with their runs: the named ones in the order
// named, or, with none named, every task, the newest first. A named task
// that does not exist is an error wrapping domain.ErrNotFound.
func (store *Store) HistoryTasks(ctx context.Context, ids []string) ([]HistoryTask, error) {
	// Specs are stored as bytes, which SQLite's JSON functions would read as
	// its binary JSON format.
	const columns = `id, project_id, created_at, COALESCE(json_extract(CAST(spec_json AS TEXT), '$.title'), ''), COALESCE(json_extract(CAST(spec_json AS TEXT), '$.objective'), '')`
	byID := make(map[string]*HistoryTask)
	order := make([]string, 0, len(ids))
	scanTasks := func(rows *sql.Rows) error {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var task HistoryTask
			var created, title, objective string
			if err := rows.Scan(&task.ID, &task.ProjectID, &created, &title, &objective); err != nil {
				return err
			}
			task.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
			task.Title = title
			if task.Title == "" {
				task.Title = objective
			}
			task.Runs = make([]Execution, 0, 1)
			byID[task.ID] = &task
			if len(ids) == 0 {
				order = append(order, task.ID)
			}
		}
		return rows.Err()
	}
	scanRuns := func(rows *sql.Rows) error {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			execution, err := scanExecution(rows)
			if err != nil {
				return err
			}
			if task := byID[execution.TaskID]; task != nil {
				task.Runs = append(task.Runs, execution)
			}
		}
		return rows.Err()
	}
	if len(ids) == 0 {
		rows, err := store.db.QueryContext(ctx, `SELECT `+columns+` FROM tasks ORDER BY created_at DESC`)
		if err != nil {
			return nil, fmt.Errorf("list tasks: %w", err)
		}
		if err := scanTasks(rows); err != nil {
			return nil, fmt.Errorf("list tasks: %w", err)
		}
		rows, err = store.db.QueryContext(ctx, `SELECT `+executionColumns+` FROM executions ORDER BY started_at`)
		if err != nil {
			return nil, fmt.Errorf("list runs: %w", err)
		}
		if err := scanRuns(rows); err != nil {
			return nil, fmt.Errorf("list runs: %w", err)
		}
	} else {
		ids = uniqueIDs(ids)
		order = ids
		for start := 0; start < len(ids); start += queryChunk {
			chunk := ids[start:min(start+queryChunk, len(ids))]
			args := make([]any, len(chunk))
			for index, id := range chunk {
				args[index] = id
			}
			in := `(?` + strings.Repeat(",?", len(chunk)-1) + `)`
			rows, err := store.db.QueryContext(ctx, `SELECT `+columns+` FROM tasks WHERE id IN `+in, args...)
			if err != nil {
				return nil, fmt.Errorf("load tasks: %w", err)
			}
			if err := scanTasks(rows); err != nil {
				return nil, fmt.Errorf("load tasks: %w", err)
			}
			rows, err = store.db.QueryContext(ctx, `SELECT `+executionColumns+` FROM executions WHERE task_id IN `+in+` ORDER BY started_at`, args...)
			if err != nil {
				return nil, fmt.Errorf("load runs: %w", err)
			}
			if err := scanRuns(rows); err != nil {
				return nil, fmt.Errorf("load runs: %w", err)
			}
		}
	}
	tasks := make([]HistoryTask, 0, len(order))
	for _, id := range order {
		task := byID[id]
		if task == nil {
			return nil, fmt.Errorf("%w: task %s", domain.ErrNotFound, id)
		}
		if last := len(task.Runs) - 1; last >= 0 && task.Runs[last].Title != "" {
			task.Title = task.Runs[last].Title
		}
		tasks = append(tasks, *task)
	}
	return tasks, nil
}

// TableRows is how many rows a delete removed from one table.
type TableRows struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// RemovedRun is a run a delete removed, with what it pointed to outside
// SQLite, which the caller may still have to clean up or report.
type RemovedRun struct {
	Execution
	// Story is the run's story as SaveExecutionStory kept it, or nil.
	Story []byte `json:"-"`
}

// GitHubLink is a task's link to a GitHub issue. Deleting the task drops
// the link here and changes nothing on GitHub.
type GitHubLink struct {
	Repository string `json:"repository"`
	Issue      int    `json:"issue"`
	URL        string `json:"url"`
}

// TaskRemoval is what a delete removed for one task, and what it left for
// the caller outside SQLite.
type TaskRemoval struct {
	TaskID string `json:"taskId"`
	// Rows counts the rows removed from each table, in the order removed. A
	// table that had none is left out.
	Rows []TableRows `json:"rows"`
	// Runs are the runs removed, oldest first.
	Runs []RemovedRun `json:"runs"`
	// Artifacts are the digests of the blobs the removed rows indexed, once
	// each. Another task may still index one; ArtifactReferenced says.
	Artifacts   []string     `json:"artifacts"`
	GitHubLinks []GitHubLink `json:"githubLinks"`
}

// deletion removes the rows of one table that belong to what is being
// deleted. ?1 is the run, or for a whole task the task; ?2 is a run's task.
type deletion struct{ table, where string }

const (
	taskWorkflows  = `SELECT id FROM workflows WHERE task_id=?1`
	taskExecutions = `SELECT id FROM executions WHERE task_id=?1`
)

// taskDeletions remove everything a task owns, each child before the parent
// its foreign key names: a step run's events before it, a comment before its
// thread, a run's rows before the run, and the run before its workflow, the
// workflow before the task. A step run belongs to the task through its
// workflow or, for one recorded with its run, through that.
var taskDeletions = []deletion{
	{"events", `run_id IN (SELECT id FROM runs WHERE workflow_id IN (` + taskWorkflows + `) OR execution_id IN (` + taskExecutions + `))`},
	{"runs", `workflow_id IN (` + taskWorkflows + `) OR execution_id IN (` + taskExecutions + `)`},
	{"comments", `execution_id IN (` + taskExecutions + `)`},
	{"comment_threads", `execution_id IN (` + taskExecutions + `)`},
	{"feedback", `execution_id IN (` + taskExecutions + `)`},
	{"commits", `execution_id IN (` + taskExecutions + `)`},
	{"checks", `execution_id IN (` + taskExecutions + `)`},
	{"viewed_files", `execution_id IN (` + taskExecutions + `)`},
	{"resumes", `execution_id IN (` + taskExecutions + `)`},
	{"tracker_changes", `execution_id IN (` + taskExecutions + `)`},
	{"inputs", `execution_id IN (` + taskExecutions + `)`},
	{"doc_reads", `task_id=?1 OR run_id IN (` + taskExecutions + `)`},
	{"usage", `task_id=?1 OR execution_id IN (` + taskExecutions + `)`},
	{"executions", `task_id=?1`},
	{"steps", `workflow_id IN (` + taskWorkflows + `)`},
	{"workflows", `task_id=?1`},
	{"a2a_messages", `context_id=?1 OR task_id IN (SELECT id FROM a2a_tasks WHERE context_id=?1)`},
	{"a2a_tasks", `context_id=?1`},
	{"task_artifacts", `task_id=?1`},
	{"approvals", `task_id=?1`},
	{"github_links", `task_id=?1`},
	{"tasks", `id=?1`},
}

// runA2ATasks are a run's A2A tasks: every one Inkuna sends for a run names
// it in its metadata, and a question's task is also on its input.
const runA2ATasks = `SELECT id FROM a2a_tasks WHERE context_id=?2 AND (json_extract(CAST(task_json AS TEXT), '$.metadata.executionId')=?1 OR id IN (SELECT a2a_task_id FROM inputs WHERE execution_id=?1))`

// runDeletions remove one run and what it owns, but for its artifact index
// (runIndexDeletions), and leave its task, the plan, and the task's other
// runs. Its A2A tasks go first, while its inputs still name theirs.
var runDeletions = []deletion{
	{"a2a_messages", `task_id IN (` + runA2ATasks + `)`},
	{"a2a_tasks", `id IN (` + runA2ATasks + `)`},
	{"events", `run_id IN (SELECT id FROM runs WHERE execution_id=?1)`},
	{"runs", `execution_id=?1`},
	{"comments", `execution_id=?1`},
	{"comment_threads", `execution_id=?1`},
	{"feedback", `execution_id=?1`},
	{"commits", `execution_id=?1`},
	{"checks", `execution_id=?1`},
	{"viewed_files", `execution_id=?1`},
	{"resumes", `execution_id=?1`},
	{"tracker_changes", `execution_id=?1`},
	{"inputs", `execution_id=?1`},
	{"doc_reads", `run_id=?1`},
	{"usage", `execution_id=?1`},
}

// runIndexDeletions remove a run's artifact index, once the rows another
// run of the task still refers to have moved to it, and then the run.
var runIndexDeletions = []deletion{
	{"task_artifacts", `task_id=?2 AND execution_id=?1`},
	{"executions", `id=?1`},
}

// survivingUse names the run of the task, other than ?1, whose rows still
// refer to the blob a task_artifacts row indexes, or is NULL when none does.
// Storing the same content for the same step twice keeps one row, under the
// run that stored it first, so a later run's check log, prompt, handoff, or
// message may depend on a row that names the run being deleted. It runs
// after that run's own rows are gone, so whatever still refers to the blob
// belongs to another run.
const survivingUse = `COALESCE(
	(SELECT execution_id FROM checks WHERE log_digest=task_artifacts.digest AND execution_id IN (SELECT id FROM executions WHERE task_id=?2 AND id<>?1) LIMIT 1),
	(SELECT execution_id FROM usage WHERE prompt_digest=task_artifacts.digest AND task_id=?2 AND COALESCE(execution_id, '') NOT IN ('', ?1) LIMIT 1),
	(SELECT execution_id FROM runs WHERE execution_id IN (SELECT id FROM executions WHERE task_id=?2 AND id<>?1) AND instr(CAST(handoff_json AS TEXT), task_artifacts.digest) > 0 LIMIT 1),
	(SELECT json_extract(CAST(task_json AS TEXT), '$.metadata.executionId') FROM a2a_tasks WHERE context_id=?2 AND instr(CAST(task_json AS TEXT), task_artifacts.digest) > 0
		AND COALESCE(json_extract(CAST(task_json AS TEXT), '$.metadata.executionId'), '') NOT IN ('', ?1) LIMIT 1))`

// DeleteTasks removes every row of each named task in one transaction, or
// nothing: a task that does not exist, or one with a run that has not ended
// or is still making its tracker changes, refuses the whole call with a
// DeleteRefusedError or an error wrapping domain.ErrNotFound. Blobs,
// worktrees, and branches are the caller's.
func (store *Store) DeleteTasks(ctx context.Context, ids []string) ([]TaskRemoval, error) {
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return nil, errors.New("name at least one task to delete")
	}
	removals := make([]TaskRemoval, 0, len(ids))
	err := store.immediately(ctx, func(connection *sql.Conn) error {
		for _, id := range ids {
			var found int
			if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE id=?`, id).Scan(&found); err != nil {
				return fmt.Errorf("load task %s: %w", id, err)
			}
			if found == 0 {
				return fmt.Errorf("%w: task %s", domain.ErrNotFound, id)
			}
			runs, err := removedRuns(ctx, connection, `task_id=?`, id)
			if err != nil {
				return fmt.Errorf("load the runs of task %s: %w", id, err)
			}
			if err := deletable(ctx, connection, id, runs); err != nil {
				return err
			}
			removal := TaskRemoval{TaskID: id, Runs: runs}
			if removal.Artifacts, err = digests(ctx, connection, `task_id=?`, id); err != nil {
				return fmt.Errorf("list the artifacts of task %s: %w", id, err)
			}
			if removal.GitHubLinks, err = githubLinks(ctx, connection, id); err != nil {
				return fmt.Errorf("list the GitHub links of task %s: %w", id, err)
			}
			if removal.Rows, err = deleteRows(ctx, connection, taskDeletions, id); err != nil {
				return fmt.Errorf("delete task %s: %w", id, err)
			}
			removals = append(removals, removal)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removals, nil
}

// DeleteRun removes one run of a task that has others, and every row the
// run owns, in one transaction. The task, its plan, and its other runs stay.
// It refuses, deleting nothing, a run that has not ended, one still making
// its tracker changes, and a task's only run, which goes with its task.
func (store *Store) DeleteRun(ctx context.Context, executionID string) (TaskRemoval, error) {
	var removal TaskRemoval
	err := store.immediately(ctx, func(connection *sql.Conn) error {
		runs, err := removedRuns(ctx, connection, `id=?`, executionID)
		if err != nil {
			return fmt.Errorf("load run %s: %w", executionID, err)
		}
		if len(runs) == 0 {
			return fmt.Errorf("%w: run %s", domain.ErrNotFound, executionID)
		}
		run := runs[0]
		if err := deletable(ctx, connection, run.TaskID, runs); err != nil {
			return err
		}
		var siblings int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM executions WHERE task_id=? AND id<>?`, run.TaskID, run.ID).Scan(&siblings); err != nil {
			return fmt.Errorf("count the runs of task %s: %w", run.TaskID, err)
		}
		if siblings == 0 {
			return &DeleteRefusedError{TaskID: run.TaskID, RunID: run.ID, Reason: "is the task's only run; delete the task instead"}
		}
		removal = TaskRemoval{TaskID: run.TaskID, Runs: runs, GitHubLinks: []GitHubLink{}}
		if removal.Rows, err = deleteRows(ctx, connection, runDeletions, run.ID, run.TaskID); err != nil {
			return fmt.Errorf("delete run %s: %w", run.ID, err)
		}
		if _, err := connection.ExecContext(ctx, `UPDATE task_artifacts SET execution_id=`+survivingUse+` WHERE task_id=?2 AND execution_id=?1 AND `+survivingUse+` IS NOT NULL`, run.ID, run.TaskID); err != nil {
			return fmt.Errorf("keep the artifacts another run of task %s uses: %w", run.TaskID, err)
		}
		if removal.Artifacts, err = digests(ctx, connection, `task_id=? AND execution_id=?`, run.TaskID, run.ID); err != nil {
			return fmt.Errorf("list the artifacts of run %s: %w", run.ID, err)
		}
		rows, err := deleteRows(ctx, connection, runIndexDeletions, run.ID, run.TaskID)
		if err != nil {
			return fmt.Errorf("delete run %s: %w", run.ID, err)
		}
		removal.Rows = append(removal.Rows, rows...)
		return nil
	})
	if err != nil {
		return TaskRemoval{}, err
	}
	return removal, nil
}

// ArtifactReferenced reports whether any task still indexes the blob, which
// must then stay in the artifact store.
func (store *Store) ArtifactReferenced(ctx context.Context, digest string) (bool, error) {
	var found int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM task_artifacts WHERE digest=? LIMIT 1)`, digest).Scan(&found); err != nil {
		return false, fmt.Errorf("check artifact %s: %w", digest, err)
	}
	return found > 0, nil
}

// ExecutionsOnBranch lists a project's runs that committed to the branch,
// oldest first.
func (store *Store) ExecutionsOnBranch(ctx context.Context, projectID, branch string) ([]Execution, error) {
	return store.queryExecutions(ctx, `SELECT `+executionColumns+` FROM executions WHERE project_id=? AND branch=? ORDER BY started_at`, projectID, branch)
}

// deletable refuses the delete when one of the runs has not ended, or is
// still making the tracker changes its approval released: deleting their
// rows then would lose how they went.
func deletable(ctx context.Context, connection *sql.Conn, taskID string, runs []RemovedRun) error {
	for _, run := range runs {
		if refused := runRefusal(taskID, run.ID, run.State, 0); refused != nil {
			return refused
		}
		var applying int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM tracker_changes WHERE execution_id=? AND state=?`, run.ID, TrackerChangeApplying).Scan(&applying); err != nil {
			return fmt.Errorf("check the tracker changes of run %s: %w", run.ID, err)
		}
		if refused := runRefusal(taskID, run.ID, run.State, applying); refused != nil {
			return refused
		}
	}
	return nil
}

// runRefusal is why a run stops its task, or itself, from being deleted, or
// nil when nothing does: it has not ended, or it is still making applying
// tracker changes.
func runRefusal(taskID, runID, state string, applying int) *DeleteRefusedError {
	if !RunEnded(state) {
		return &DeleteRefusedError{TaskID: taskID, RunID: runID, Reason: ActiveReason(state)}
	}
	if applying > 0 {
		return &DeleteRefusedError{TaskID: taskID, RunID: runID, Reason: "is still making its tracker changes; delete it once they are made"}
	}
	return nil
}

// TaskStanding is how a task stands for a delete, as a list shows it before
// anyone asks.
type TaskStanding struct {
	// Runs is how many runs the task has.
	Runs int
	// Refused says why a delete would refuse the task now, judged on the
	// stored runs alone; empty when it would not.
	Refused string
}

// TaskStandings says, for each named task that has runs, how many it has
// and whether a delete would refuse it, by the same rules a delete applies
// to the stored runs. A task without runs, or one that does not exist, is
// left out. It reads without the write lock, so the delete still decides.
func (store *Store) TaskStandings(ctx context.Context, ids []string) (map[string]TaskStanding, error) {
	ids = uniqueIDs(ids)
	standings := make(map[string]TaskStanding, len(ids))
	for start := 0; start < len(ids); start += queryChunk {
		chunk := ids[start:min(start+queryChunk, len(ids))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, TrackerChangeApplying)
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := store.db.QueryContext(ctx, `SELECT task_id, id, state, (SELECT COUNT(*) FROM tracker_changes WHERE execution_id=executions.id AND state=?)
			FROM executions WHERE task_id IN (?`+strings.Repeat(",?", len(chunk)-1)+`) ORDER BY started_at`, args...)
		if err != nil {
			return nil, fmt.Errorf("load the runs of tasks: %w", err)
		}
		err = func() error {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var taskID, runID, state string
				var applying int
				if err := rows.Scan(&taskID, &runID, &state, &applying); err != nil {
					return err
				}
				standing := standings[taskID]
				standing.Runs++
				if refused := runRefusal(taskID, runID, state, applying); refused != nil && standing.Refused == "" {
					standing.Refused = refused.Error()
				}
				standings[taskID] = standing
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, fmt.Errorf("load the runs of tasks: %w", err)
		}
	}
	return standings, nil
}

func removedRuns(ctx context.Context, connection *sql.Conn, where string, args ...any) ([]RemovedRun, error) {
	rows, err := connection.QueryContext(ctx, `SELECT `+executionColumns+`, story_json FROM executions WHERE `+where+` ORDER BY started_at`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	runs := make([]RemovedRun, 0, 1)
	for rows.Next() {
		var story string
		execution, err := scanExecution(withStory{rows, &story})
		if err != nil {
			return nil, err
		}
		run := RemovedRun{Execution: execution}
		if story != "" {
			run.Story = []byte(story)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// withStory scans an execution row followed by its story_json.
type withStory struct {
	rows  *sql.Rows
	story *string
}

func (row withStory) Scan(destinations ...any) error {
	return row.rows.Scan(append(destinations, row.story)...)
}

func digests(ctx context.Context, connection *sql.Conn, where string, args ...any) ([]string, error) {
	rows, err := connection.QueryContext(ctx, `SELECT DISTINCT digest FROM task_artifacts WHERE `+where+` ORDER BY digest`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	found := make([]string, 0)
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, err
		}
		found = append(found, digest)
	}
	return found, rows.Err()
}

func githubLinks(ctx context.Context, connection *sql.Conn, taskID string) ([]GitHubLink, error) {
	rows, err := connection.QueryContext(ctx, `SELECT repository, issue_number, issue_url FROM github_links WHERE task_id=?`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	links := make([]GitHubLink, 0)
	for rows.Next() {
		var link GitHubLink
		if err := rows.Scan(&link.Repository, &link.Issue, &link.URL); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

func deleteRows(ctx context.Context, connection *sql.Conn, deletions []deletion, args ...any) ([]TableRows, error) {
	removed := make([]TableRows, 0, len(deletions))
	for _, step := range deletions {
		result, err := connection.ExecContext(ctx, `DELETE FROM `+step.table+` WHERE `+step.where, args...)
		if err != nil {
			return nil, fmt.Errorf("delete from %s: %w", step.table, err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("count what was deleted from %s: %w", step.table, err)
		}
		if count > 0 {
			removed = append(removed, TableRows{Table: step.table, Rows: count})
		}
	}
	return removed, nil
}

// immediately runs work in a transaction that takes SQLite's write lock
// before its first statement. A deferred transaction that reads and then
// writes fails if another connection wrote in between, which happens all
// the time while runs are active; this one waits for the lock (busy_timeout)
// and then reads what nobody else can change until it commits, including
// inkunactl beside the app.
func (store *Store) immediately(ctx context.Context, work func(*sql.Conn) error) error {
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	err = work(connection)
	if err == nil {
		if _, err = connection.ExecContext(ctx, `COMMIT`); err == nil {
			return nil
		}
		err = fmt.Errorf("commit: %w", err)
	}
	if _, rollbackErr := connection.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`); rollbackErr != nil {
		// A connection that may still be inside the transaction must not go
		// back to the pool.
		_ = connection.Raw(func(any) error { return driver.ErrBadConn })
	}
	return err
}

func uniqueIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return unique
}
