package storage

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/clock"
	"github.com/inkuna-ai/inkuna/internal/domain"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is Inkuna's SQLite database. The times it stamps come from
// clock.Now, which never repeats, so lists ordered by them keep the order
// the writes were made in even where the wall clock is coarse, as on
// Windows.
type Store struct {
	db     *sql.DB
	outbox outbox
}

type Project struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Repository     string         `json:"repository"`
	DefaultRuntime domain.Runtime `json:"defaultRuntime"`
	CreatedAt      time.Time      `json:"createdAt"`
	// RemovedAt is when the project was removed; zero while it is
	// connected. A removed project keeps its history and takes no new work.
	RemovedAt time.Time `json:"removedAt,omitempty"`
	// Listed is set once the project has been in inkuna.yaml's repositories,
	// so a later absence there means its entry was removed.
	Listed bool `json:"listed,omitempty"`
}

// Removed reports whether the project was removed.
func (project Project) Removed() bool { return !project.RemovedAt.IsZero() }

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + url.PathEscape(path)
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	// Transactions begin IMMEDIATE, taking the write lock up front. A
	// deferred one that reads and then writes, after another connection
	// committed in between, fails at once with SQLITE_BUSY: under WAL its
	// snapshot is stale, and the busy timeout cannot wait that out.
	// Under WAL, synchronous NORMAL syncs at checkpoints rather than at
	// every commit: the database stays consistent, and a power loss can lose
	// only the last commits. A sync per commit made every write slow where
	// syncing is, on Windows above all.
	dsn += separator + "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(8)
	store := &Store{db: database}
	if _, err := database.Exec("PRAGMA journal_mode=WAL"); err != nil && path != ":memory:" {
		_ = database.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error { return store.db.Close() }

// Migrations lists the migrations this build knows, oldest first. Their
// names sort in the order they apply.
func Migrations() []string {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		// The directory is embedded at build time, so this cannot happen in
		// a build that compiled.
		panic(fmt.Sprintf("read the embedded migrations: %v", err))
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// LatestMigration is the newest migration this build knows, which inkunactl
// version reports so a rollback can tell whether a build would open the
// database.
func LatestMigration() string {
	names := Migrations()
	return names[len(names)-1]
}

// NewerDatabaseError is a database a newer build migrated: it has
// migrations this build does not know, so this build's code may misread or
// damage what that build wrote.
type NewerDatabaseError struct {
	// Unknown are the applied migrations this build does not have, oldest
	// first.
	Unknown []string
}

func (err NewerDatabaseError) Error() string {
	return fmt.Sprintf("the database was migrated by a newer build of Inkuna: it has %s, which this build does not know; update Inkuna (inkunactl update) to open it", strings.Join(err.Unknown, ", "))
}

func (store *Store) Migrate(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	names := Migrations()
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	applied, err := appliedMigrations(ctx, store.db)
	if err != nil {
		return err
	}
	var unknown []string
	for _, name := range applied {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return NewerDatabaseError{Unknown: unknown}
	}
	done := make(map[string]bool, len(applied))
	for _, name := range applied {
		done[name] = true
	}
	for _, name := range names {
		if done[name] {
			continue
		}
		contents, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := store.applyMigration(ctx, name, string(contents)); err != nil {
			return err
		}
	}
	return nil
}

// appliedMigrations lists the migrations the database records, oldest
// first.
func appliedMigrations(ctx context.Context, database *sql.DB) ([]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read the applied migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read the applied migrations: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the applied migrations: %w", err)
	}
	return names, nil
}

// AppliedMigrations lists the migrations the database at path records,
// oldest first, opening it read-only and migrating nothing, so inkunactl can
// ask before a rollback whether the build it restores would open it. A
// database that does not exist yet, or has no migrations table, has none.
func AppliedMigrations(path string) ([]string, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	database, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = database.Close() }()
	var tables int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&tables); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if tables == 0 {
		return nil, nil
	}
	names, err := appliedMigrations(context.Background(), database)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return names, nil
}

func (store *Store) applyMigration(ctx context.Context, name, contents string) error {
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	// Both rebuild projects, which tasks reference.
	rebuildsReferencedTable := name == "002_copilot_runtime.sql" || name == "005_declared_runtimes.sql"
	if rebuildsReferencedTable {
		if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
			return fmt.Errorf("disable foreign keys for migration %s: %w", name, err)
		}
		defer func() { _, _ = connection.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`) }()
	}
	// The transaction begins IMMEDIATE (Open's _txlock), so it holds the
	// write lock: another process opening the database, as inkunactl does
	// beside the app, may have applied this migration since Migrate looked,
	// and is the only one to.
	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var applied int
	if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, name).Scan(&applied); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if applied > 0 {
		return transaction.Rollback()
	}
	if _, err = transaction.ExecContext(ctx, contents); err == nil && rebuildsReferencedTable {
		var table string
		err = transaction.QueryRowContext(ctx, `SELECT "table" FROM pragma_foreign_key_check LIMIT 1`).Scan(&table)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		} else if err == nil {
			err = fmt.Errorf("foreign key check failed for table %s", table)
		}
	}
	if err == nil {
		_, err = transaction.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, name, timestamp(clock.Now()))
	}
	if err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	if rebuildsReferencedTable {
		if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
			return fmt.Errorf("restore foreign keys after migration %s: %w", name, err)
		}
	}
	return nil
}

func (store *Store) UpsertProject(ctx context.Context, project Project) error {
	if project.ID == "" || project.Name == "" || project.Repository == "" {
		return errors.New("project id, name, and repository are required")
	}
	if project.CreatedAt.IsZero() {
		project.CreatedAt = clock.Now()
	}
	removed := ""
	if project.Removed() {
		removed = timestamp(project.RemovedAt)
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO projects(id,name,repository,default_runtime,created_at,removed_at,listed) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, repository=excluded.repository, default_runtime=excluded.default_runtime, removed_at=excluded.removed_at, listed=excluded.listed`,
		project.ID, project.Name, project.Repository, project.DefaultRuntime, timestamp(project.CreatedAt), removed, project.Listed)
	return err
}

const projectColumns = `p.id,p.name,p.repository,p.default_runtime,p.created_at,p.removed_at,p.listed`

// scanProject reads one row of projectColumns.
func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var project Project
	var created, removed string
	if err := row.Scan(&project.ID, &project.Name, &project.Repository, &project.DefaultRuntime, &created, &removed, &project.Listed); err != nil {
		return Project{}, err
	}
	project.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if removed != "" {
		project.RemovedAt, _ = time.Parse(time.RFC3339Nano, removed)
	}
	return project, nil
}

// Projects lists the connected projects, newest first. A removed project
// is left out; AllProjects includes it.
func (store *Store) Projects(ctx context.Context) ([]Project, error) {
	return store.projects(ctx, `WHERE p.removed_at=''`)
}

// AllProjects lists every project, removed ones too, newest first, for
// reads that name a task's project, such as its history and costs.
func (store *Store) AllProjects(ctx context.Context) ([]Project, error) {
	return store.projects(ctx, "")
}

func (store *Store) projects(ctx context.Context, where string) ([]Project, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects p `+where+` ORDER BY p.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	projects := make([]Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

// Project reads one project, removed or not.
func (store *Store) Project(ctx context.Context, id string) (Project, error) {
	return scanProject(store.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects p WHERE p.id=?`, id))
}

func (store *Store) ProjectForTask(ctx context.Context, taskID string) (Project, error) {
	return scanProject(store.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects p JOIN tasks t ON t.project_id=p.id WHERE t.id=?`, taskID))
}

// SaveTask saves a task's spec. The state it records is the task's state
// when it was planned and is never updated; TaskStates says where a task
// stands now. A task it first saves has no creator; CreateTask records one.
func (store *Store) SaveTask(ctx context.Context, projectID, state string, task domain.TaskSpec) error {
	return store.saveTask(ctx, projectID, state, "", task)
}

// CreateTask saves a new task as SaveTask does, recording createdBy, the
// principal ID of the person who created it. The creator is kept apart from
// the spec, which prompts are built from, and a later save keeps it.
func (store *Store) CreateTask(ctx context.Context, projectID, state, createdBy string, task domain.TaskSpec) error {
	return store.saveTask(ctx, projectID, state, createdBy, task)
}

func (store *Store) saveTask(ctx context.Context, projectID, state, createdBy string, task domain.TaskSpec) error {
	if err := domain.ValidateTask(task); err != nil {
		return err
	}
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	now := timestamp(clock.Now())
	_, err = store.db.ExecContext(ctx, `INSERT INTO tasks(id,project_id,state,kind,spec_json,created_at,updated_at,created_by) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET state=excluded.state,spec_json=excluded.spec_json,updated_at=excluded.updated_at`, task.ID, projectID, state, task.Kind, data, now, now, createdBy)
	return err
}

// TaskCreator returns the principal ID of the person who created a task,
// empty for a task created before creators were recorded.
func (store *Store) TaskCreator(ctx context.Context, id string) (string, error) {
	var createdBy string
	if err := store.db.QueryRowContext(ctx, `SELECT created_by FROM tasks WHERE id=?`, id).Scan(&createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: task %s", domain.ErrNotFound, id)
		}
		return "", fmt.Errorf("read the creator of task %s: %w", id, err)
	}
	return createdBy, nil
}

func (store *Store) Task(ctx context.Context, id string) (domain.TaskSpec, error) {
	var data []byte
	if err := store.db.QueryRowContext(ctx, `SELECT spec_json FROM tasks WHERE id=?`, id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.TaskSpec{}, fmt.Errorf("%w: task %s", domain.ErrNotFound, id)
		}
		return domain.TaskSpec{}, err
	}
	var task domain.TaskSpec
	if err := json.Unmarshal(data, &task); err != nil {
		return domain.TaskSpec{}, fmt.Errorf("decode task %s: %w", id, err)
	}
	return task, nil
}

// taskStateSQL is the state of the task aliased t, derived rather than read
// from tasks.state, which keeps the state the task was planned in: its
// latest run's state, or, for a task that never ran, approved once one of
// its workflows was approved and planned before that. Runs are inserted as
// they start, so the latest is the last inserted; a resume runs again in
// the same row, and starting over inserts a new one.
const taskStateSQL = `COALESCE((SELECT latest.state FROM executions latest WHERE latest.task_id=t.id ORDER BY latest.rowid DESC LIMIT 1),
CASE WHEN EXISTS(SELECT 1 FROM workflows w WHERE w.task_id=t.id AND w.state='approved') THEN 'approved' ELSE 'planned' END)`

// TaskStates returns where each of the tasks stands, by id, as taskStateSQL
// derives it. A task not stored is left out. The ids go as one JSON array,
// which no count of tasks can push past SQLite's limit on bound parameters.
func (store *Store) TaskStates(ctx context.Context, ids []string) (map[string]string, error) {
	states := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return states, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("encode task ids: %w", err)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT t.id,`+taskStateSQL+` FROM json_each(?) j CROSS JOIN tasks t ON t.id=j.value`, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("query task states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return nil, fmt.Errorf("read task state: %w", err)
		}
		states[id] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read task states: %w", err)
	}
	return states, nil
}

// TaskState returns where one task stands, as TaskStates derives it.
func (store *Store) TaskState(ctx context.Context, id string) (string, error) {
	states, err := store.TaskStates(ctx, []string{id})
	if err != nil {
		return "", err
	}
	state, ok := states[id]
	if !ok {
		return "", fmt.Errorf("%w: task %s", domain.ErrNotFound, id)
	}
	return state, nil
}

func (store *Store) SaveWorkflow(ctx context.Context, state string, workflow domain.WorkflowSpec) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	if err := saveWorkflow(ctx, transaction, state, workflow); err != nil {
		return err
	}
	return transaction.Commit()
}

func saveWorkflow(ctx context.Context, transaction *sql.Tx, state string, workflow domain.WorkflowSpec) error {
	if err := domain.ValidateWorkflow(workflow); err != nil {
		return err
	}
	data, err := json.Marshal(workflow)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO workflows(id,task_id,state,spec_json,created_at) VALUES(?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET state=excluded.state,spec_json=excluded.spec_json`, workflow.ID, workflow.TaskID, state, data, timestamp(workflow.CreatedAt))
	if err != nil {
		return err
	}
	for _, step := range workflow.Steps {
		stepData, marshalErr := json.Marshal(step)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO steps(workflow_id,id,state,spec_json) VALUES(?,?,?,?)
ON CONFLICT(workflow_id,id) DO UPDATE SET spec_json=excluded.spec_json`, workflow.ID, step.ID, "pending", stepData); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) Workflow(ctx context.Context, id string) (domain.WorkflowSpec, error) {
	var data []byte
	if err := store.db.QueryRowContext(ctx, `SELECT spec_json FROM workflows WHERE id=?`, id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.WorkflowSpec{}, fmt.Errorf("%w: workflow %s", domain.ErrNotFound, id)
		}
		return domain.WorkflowSpec{}, err
	}
	var workflow domain.WorkflowSpec
	if err := json.Unmarshal(data, &workflow); err != nil {
		return domain.WorkflowSpec{}, fmt.Errorf("decode workflow %s: %w", id, err)
	}
	return workflow, nil
}

// WorkflowState is a stored workflow's state: planned until it is approved.
func (store *Store) WorkflowState(ctx context.Context, id string) (string, error) {
	var state string
	if err := store.db.QueryRowContext(ctx, `SELECT state FROM workflows WHERE id=?`, id).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: workflow %s", domain.ErrNotFound, id)
		}
		return "", err
	}
	return state, nil
}

func (store *Store) ApproveWorkflow(ctx context.Context, workflow domain.WorkflowSpec) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	if err := saveWorkflow(ctx, transaction, "approved", workflow); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE workflows SET approved_at=? WHERE id=?`, timestamp(clock.Now()), workflow.ID); err != nil {
		return err
	}
	return transaction.Commit()
}

// StartRun records a step run, or a peer's, in its run's round.
func (store *Store) StartRun(ctx context.Context, runID, executionID, workflowID, stepID string, round int, runtime domain.Runtime, worktree string) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO runs(id,execution_id,workflow_id,step_id,round,runtime,state,worktree,started_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		runID, executionID, workflowID, stepID, max(round, 1), runtime, "running", worktree, timestamp(clock.Now()))
	return err
}

func (store *Store) FinishRun(ctx context.Context, runID, state string, handoff domain.HandoffResult) error {
	data, err := json.Marshal(handoff)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `UPDATE runs SET state=?, finished_at=?, handoff_json=? WHERE id=?`, state, timestamp(clock.Now()), data, runID)
	return err
}

// LastEventSequence returns the sequence of a step run's last stored event,
// or 0 when it has none.
func (store *Store) LastEventSequence(ctx context.Context, runID string) (int64, error) {
	var last int64
	err := store.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM events WHERE run_id=?`, runID).Scan(&last)
	return last, err
}

func (store *Store) AppendEvent(ctx context.Context, event domain.RunEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	const insert = `INSERT INTO events(run_id,sequence,event_json,created_at) VALUES(?,?,?,?)`
	if !store.outbox.audit.Load() || !auditedEvent(event.Kind) {
		_, err = store.db.ExecContext(ctx, insert, event.RunID, event.Sequence, data, timestamp(event.Timestamp))
		return err
	}
	// The audit event commits with the run event, so a crash cannot keep
	// one without the other.
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, insert, event.RunID, event.Sequence, data, timestamp(event.Timestamp)); err != nil {
		return err
	}
	record, err := runEventAudit(ctx, transaction, event)
	if err != nil {
		return err
	}
	if err := insertOutbox(ctx, transaction, OutboxAudit, 0, record); err != nil {
		return fmt.Errorf("queue the audit event of step run %s: %w", event.RunID, err)
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	store.notifyQueued(OutboxAudit)
	return nil
}

func (store *Store) EventCount(ctx context.Context, runID string) (int, error) {
	var count int
	err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE run_id=?`, runID).Scan(&count)
	return count, err
}

func (store *Store) DB() *sql.DB { return store.db }

// ProjectTaskIDs lists the ids of every task planned for a project.
func (store *Store) ProjectTaskIDs(ctx context.Context, projectID string) ([]string, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id FROM tasks WHERE project_id=? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list the tasks of project %s: %w", projectID, err)
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list the tasks of project %s: %w", projectID, err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ProjectActiveRun returns the first of a project's runs that has not
// ended, by id and state, or "" when every run has. A run paused for review
// survives a restart, so this reads what is stored rather than only the
// runs in memory.
func (store *Store) ProjectActiveRun(ctx context.Context, projectID string) (id, state string, err error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, state FROM executions WHERE project_id=? ORDER BY started_at`, projectID)
	if err != nil {
		return "", "", fmt.Errorf("list the runs of project %s: %w", projectID, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := rows.Scan(&id, &state); err != nil {
			return "", "", fmt.Errorf("list the runs of project %s: %w", projectID, err)
		}
		if !RunEnded(state) {
			return id, state, nil
		}
	}
	return "", "", rows.Err()
}
