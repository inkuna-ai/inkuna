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

// Execution is the durable header of one workflow run.
type Execution struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflowId"`
	TaskID     string `json:"taskId"`
	ProjectID  string `json:"projectId"`
	State      string `json:"state"`
	Title      string `json:"title"`
	Worktree   string `json:"worktree,omitempty"`
	// Branch is the branch the run's writable steps committed to.
	Branch string `json:"branch,omitempty"`
	// BaseCommit is the commit the run's workspace started from; empty for
	// runs from before it was recorded.
	BaseCommit string `json:"baseCommit,omitempty"`
	// Cleanup is the plan's worktree cleanup policy (domain.Cleanup*).
	Cleanup string `json:"cleanup,omitempty"`
	// WorktreeRemovedAt is set once Inkuna has removed the run's worktree.
	WorktreeRemovedAt time.Time `json:"worktreeRemovedAt,omitempty"`
	// Round is the run's current feedback round, from 1.
	Round      int       `json:"round"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	// RerunOf is the run this one started over from, empty for a run planned
	// as it is. It is set when the run is first saved and never changes.
	RerunOf string `json:"rerunOf,omitempty"`
	// ApprovedBy is the principal ID of the person who approved the run,
	// empty for runs from before it was recorded. It is set when the run is
	// first saved and never changes, so a resume keeps it.
	ApprovedBy string `json:"approvedBy,omitempty"`
}

// InputRequest is a question waiting for, or answered by, the user.
type InputRequest struct {
	ID          string `json:"id"`
	ExecutionID string `json:"executionId"`
	StepID      string `json:"stepId"`
	A2ATaskID   string `json:"a2aTaskId,omitempty"`
	// Agent is the A2A card that receives the answer.
	Agent string `json:"agent"`
	// AskedBy names who raised the question: a worker agent or the rule.
	AskedBy    string    `json:"askedBy"`
	Question   string    `json:"question"`
	Options    []string  `json:"options,omitempty"`
	Context    string    `json:"context,omitempty"`
	State      string    `json:"state"`
	Answer     string    `json:"answer,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	ResolvedAt time.Time `json:"resolvedAt,omitempty"`
	// Review marks a run paused at a review gate: it is approved, or a
	// reply sends changes to ReviewTarget, which is empty when the gate can
	// only be approved.
	Review       bool   `json:"review,omitempty"`
	ReviewTarget string `json:"reviewTarget,omitempty"`
	// CheckPause marks a run paused because its checks failed in a way the
	// writer cannot fix. It is answered only with one of Options, and is
	// never an answer the run's workers are given.
	CheckPause bool `json:"checkPause,omitempty"`
	// Permissions is what a permission request asks the user to allow, one
	// command line or tool each, so the inbox can show each on its own. The
	// view carries it on app.InputView instead, where Wails finds its type.
	Permissions []PermissionAsk `json:"-"`
	// Stage marks the question a human-input stage asks. Its thread lists
	// the documents of the steps the stage follows, as a review gate's does.
	Stage bool `json:"stage,omitempty"`
}

// PermissionAsk is one thing a permission request asks the user to allow:
// a command line, or a tool with what the worker wanted it for.
type PermissionAsk struct {
	Command string `json:"command,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ArtifactRecord ties a content-addressed blob to the task that produced it.
type ArtifactRecord struct {
	Digest      string    `json:"digest"`
	TaskID      string    `json:"taskId"`
	ExecutionID string    `json:"executionId,omitempty"`
	StepID      string    `json:"stepId,omitempty"`
	Agent       string    `json:"agent,omitempty"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	MediaType   string    `json:"mediaType"`
	Size        int64     `json:"size"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// CommitRecord remembers the commit Inkuna made after a writable step.
type CommitRecord struct {
	ExecutionID string `json:"executionId"`
	StepID      string `json:"stepId"`
	// Round is the feedback round the step committed in, from 1.
	Round int `json:"round"`
	// Agent is the agent that ran the step; empty for commits from before it
	// was recorded.
	Agent     string    `json:"agent,omitempty"`
	Hash      string    `json:"hash"`
	Subject   string    `json:"subject"`
	Stat      string    `json:"stat"`
	CreatedAt time.Time `json:"createdAt"`
}

const (
	InputPending = "pending"
	// InputDelivering marks an answer that has been recorded but whose A2A
	// delivery has not been confirmed. A retry is rejected so the answer is
	// never appended twice.
	InputDelivering = "delivering"
	InputAnswered   = "answered"
	InputCanceled   = "canceled"
)

func (store *Store) SaveExecution(ctx context.Context, execution Execution) error {
	if execution.ID == "" || execution.WorkflowID == "" || execution.TaskID == "" || execution.ProjectID == "" {
		return errors.New("execution id, workflow, task, and project are required")
	}
	if execution.StartedAt.IsZero() {
		execution.StartedAt = clock.Now()
	}
	execution.Round = max(execution.Round, 1)
	_, err := store.db.ExecContext(ctx, `INSERT INTO executions(id,workflow_id,task_id,project_id,state,title,worktree,branch,base_commit,cleanup,worktree_removed_at,round,error,started_at,finished_at,rerun_of,approved_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET state=excluded.state, title=excluded.title, worktree=excluded.worktree, branch=excluded.branch, base_commit=excluded.base_commit, cleanup=excluded.cleanup, worktree_removed_at=excluded.worktree_removed_at, round=excluded.round, error=excluded.error, finished_at=excluded.finished_at`,
		execution.ID, execution.WorkflowID, execution.TaskID, execution.ProjectID, execution.State, execution.Title, execution.Worktree, execution.Branch, execution.BaseCommit, execution.Cleanup,
		nullTime(execution.WorktreeRemovedAt), execution.Round, execution.Error, timestamp(execution.StartedAt), nullTime(execution.FinishedAt), execution.RerunOf, execution.ApprovedBy)
	return err
}

func nullTime(value time.Time) sql.NullString {
	if value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: timestamp(value), Valid: true}
}

const executionColumns = `id,workflow_id,task_id,project_id,state,title,COALESCE(worktree,''),branch,base_commit,cleanup,COALESCE(worktree_removed_at,''),round,COALESCE(error,''),started_at,COALESCE(finished_at,''),rerun_of,approved_by`

type rowScanner interface{ Scan(...any) error }

func scanExecution(row rowScanner) (Execution, error) {
	var execution Execution
	var removed, started, finished string
	if err := row.Scan(&execution.ID, &execution.WorkflowID, &execution.TaskID, &execution.ProjectID, &execution.State, &execution.Title, &execution.Worktree,
		&execution.Branch, &execution.BaseCommit, &execution.Cleanup, &removed, &execution.Round, &execution.Error, &started, &finished, &execution.RerunOf, &execution.ApprovedBy); err != nil {
		return Execution{}, err
	}
	execution.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	if finished != "" {
		execution.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
	}
	if removed != "" {
		execution.WorktreeRemovedAt, _ = time.Parse(time.RFC3339Nano, removed)
	}
	return execution, nil
}

func (store *Store) Executions(ctx context.Context, limit int) ([]Execution, error) {
	if limit <= 0 {
		limit = 100
	}
	return store.queryExecutions(ctx, `SELECT `+executionColumns+` FROM executions ORDER BY started_at DESC LIMIT ?`, limit)
}

// ExecutionsWithWorktrees lists a project's runs whose worktree Inkuna has
// not removed, oldest first, for cleanup.
func (store *Store) ExecutionsWithWorktrees(ctx context.Context, projectID string) ([]Execution, error) {
	return store.queryExecutions(ctx, `SELECT `+executionColumns+` FROM executions WHERE project_id=? AND COALESCE(worktree,'')<>'' AND worktree_removed_at IS NULL ORDER BY started_at`, projectID)
}

func (store *Store) queryExecutions(ctx context.Context, query string, args ...any) ([]Execution, error) {
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	executions := make([]Execution, 0)
	for rows.Next() {
		execution, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		executions = append(executions, execution)
	}
	return executions, rows.Err()
}

// Execution returns one execution header by id.
func (store *Store) Execution(ctx context.Context, id string) (Execution, error) {
	execution, err := scanExecution(store.db.QueryRowContext(ctx, `SELECT `+executionColumns+` FROM executions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Execution{}, fmt.Errorf("%w: execution %s", domain.ErrNotFound, id)
	}
	return execution, err
}

// ClaimInput atomically moves a pending input to delivering and records the
// answer. It reports false when the input was not pending, so concurrent
// answers cannot both proceed to delivery.
func (store *Store) ClaimInput(ctx context.Context, id, answer string) (bool, error) {
	result, err := store.db.ExecContext(ctx, `UPDATE inputs SET state=?, answer=? WHERE id=? AND state=?`, InputDelivering, answer, id, InputPending)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// FinishDelivery moves a delivering input to its final state only if it is
// still delivering, so a cancellation that happened meanwhile is preserved.
// answered=true records the answer as delivered; false restores pending and
// clears the answer. It reports whether a row changed.
func (store *Store) FinishDelivery(ctx context.Context, id string, answered bool) (bool, error) {
	var result sql.Result
	var err error
	if answered {
		// Answers are listed in the order they were given, so two given in a
		// row must not share a time (clock.Now).
		result, err = store.db.ExecContext(ctx, `UPDATE inputs SET state=?, resolved_at=? WHERE id=? AND state=?`, InputAnswered, timestamp(clock.Now()), id, InputDelivering)
	} else {
		result, err = store.db.ExecContext(ctx, `UPDATE inputs SET state=?, answer=NULL WHERE id=? AND state=?`, InputPending, id, InputDelivering)
	}
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// CancelPendingInputs cancels every unanswered question for one execution.
func (store *Store) CancelPendingInputs(ctx context.Context, executionID string) error {
	_, err := store.db.ExecContext(ctx, `UPDATE inputs SET state=?, resolved_at=? WHERE execution_id=? AND state IN (?, ?)`, InputCanceled, timestamp(clock.Now()), executionID, InputPending, InputDelivering)
	return err
}

// MarkInterruptedExecutions flips runs that were active when the process
// died into a terminal state so the UI never shows phantom activity.
func (store *Store) MarkInterruptedExecutions(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, `UPDATE executions SET state='interrupted', error='Inkuna stopped while this run was active', finished_at=? WHERE state IN ('queued','running','waiting','cancelling')`, timestamp(clock.Now()))
	if err != nil {
		return err
	}
	// Runs do not survive a restart, so answers caught mid-delivery cannot be
	// replayed; they are cancelled alongside unanswered questions. A run
	// paused at a review gate is not running, and stays paused for its
	// review.
	_, err = store.db.ExecContext(ctx, `UPDATE inputs SET state=?, resolved_at=? WHERE state IN (?, ?) AND execution_id NOT IN (SELECT id FROM executions WHERE state='reviewing')`, InputCanceled, timestamp(clock.Now()), InputPending, InputDelivering)
	if err != nil {
		return err
	}
	// A command running when Inkuna stopped was stopped with it, and the
	// ones after it never ran.
	_, err = store.db.ExecContext(ctx, `UPDATE checks SET status=CASE status WHEN ? THEN ? ELSE ? END, finished_at=CASE status WHEN ? THEN ? ELSE finished_at END WHERE status IN (?, ?)`,
		CheckRunning, CheckCancelled, CheckSkipped, CheckRunning, timestamp(clock.Now()), CheckRunning, CheckQueued)
	if err != nil {
		return err
	}
	// A tracker change being applied when Inkuna stopped may or may not
	// have reached the tracker; it is failed so a person can retry it, and
	// a retry never makes a comment or an issue twice.
	_, err = store.db.ExecContext(ctx, `UPDATE tracker_changes SET state=?, reason=?, updated_at=? WHERE state=?`,
		TrackerChangeFailed, "Inkuna stopped while applying this change, so it may not have been made; retry it", timestamp(clock.Now()), TrackerChangeApplying)
	return err
}

func (store *Store) SaveInput(ctx context.Context, input InputRequest) error {
	if input.ID == "" || input.ExecutionID == "" || input.StepID == "" || input.Question == "" {
		return errors.New("input id, execution, step, and question are required")
	}
	options, err := json.Marshal(input.Options)
	if err != nil {
		return err
	}
	permissions, err := json.Marshal(input.Permissions)
	if err != nil {
		return err
	}
	if input.Permissions == nil {
		permissions = []byte("[]")
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = clock.Now()
	}
	resolved := sql.NullString{}
	if !input.ResolvedAt.IsZero() {
		resolved = sql.NullString{String: timestamp(input.ResolvedAt), Valid: true}
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO inputs(id,execution_id,step_id,a2a_task_id,agent,asked_by,question,options_json,context,state,answer,created_at,resolved_at,review,review_target,check_pause,permissions_json,stage) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET state=excluded.state, answer=excluded.answer, resolved_at=excluded.resolved_at`,
		input.ID, input.ExecutionID, input.StepID, input.A2ATaskID, input.Agent, input.AskedBy, input.Question, string(options), input.Context, input.State, input.Answer, timestamp(input.CreatedAt), resolved, input.Review, input.ReviewTarget, input.CheckPause, string(permissions), input.Stage)
	return err
}

func (store *Store) Inputs(ctx context.Context, state string, limit int) ([]InputRequest, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id,execution_id,step_id,COALESCE(a2a_task_id,''),agent,asked_by,question,options_json,COALESCE(context,''),state,COALESCE(answer,''),created_at,COALESCE(resolved_at,''),review,review_target,check_pause,permissions_json,stage FROM inputs`
	args := []any{}
	if state != "" {
		query += ` WHERE state=?`
		args = append(args, state)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	inputs := make([]InputRequest, 0)
	for rows.Next() {
		var input InputRequest
		var options, permissions, created, resolved string
		if err := rows.Scan(&input.ID, &input.ExecutionID, &input.StepID, &input.A2ATaskID, &input.Agent, &input.AskedBy, &input.Question, &options, &input.Context, &input.State, &input.Answer, &created, &resolved, &input.Review, &input.ReviewTarget, &input.CheckPause, &permissions, &input.Stage); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(options), &input.Options)
		_ = json.Unmarshal([]byte(permissions), &input.Permissions)
		input.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if resolved != "" {
			input.ResolvedAt, _ = time.Parse(time.RFC3339Nano, resolved)
		}
		inputs = append(inputs, input)
	}
	return inputs, rows.Err()
}

func (store *Store) Input(ctx context.Context, id string) (InputRequest, error) {
	var input InputRequest
	var options, permissions, created, resolved string
	err := store.db.QueryRowContext(ctx, `SELECT id,execution_id,step_id,COALESCE(a2a_task_id,''),agent,asked_by,question,options_json,COALESCE(context,''),state,COALESCE(answer,''),created_at,COALESCE(resolved_at,''),review,review_target,check_pause,permissions_json,stage FROM inputs WHERE id=?`, id).
		Scan(&input.ID, &input.ExecutionID, &input.StepID, &input.A2ATaskID, &input.Agent, &input.AskedBy, &input.Question, &options, &input.Context, &input.State, &input.Answer, &created, &resolved, &input.Review, &input.ReviewTarget, &input.CheckPause, &permissions, &input.Stage)
	if errors.Is(err, sql.ErrNoRows) {
		return InputRequest{}, fmt.Errorf("%w: input %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return InputRequest{}, err
	}
	if err := json.Unmarshal([]byte(options), &input.Options); err != nil {
		return InputRequest{}, fmt.Errorf("decode input %s options: %w", id, err)
	}
	if err := json.Unmarshal([]byte(permissions), &input.Permissions); err != nil {
		return InputRequest{}, fmt.Errorf("decode input %s permissions: %w", id, err)
	}
	input.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if resolved != "" {
		input.ResolvedAt, _ = time.Parse(time.RFC3339Nano, resolved)
	}
	return input, nil
}

// SaveA2ATask satisfies a2a.Persister.
// SaveA2ATask mirrors a task snapshot and records every message in its
// history once. Messages are immutable, so existing rows are left alone.
func (store *Store) SaveA2ATask(ctx context.Context, task a2a.Task) error {
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	now := clock.Now()
	if _, err := transaction.ExecContext(ctx, `INSERT INTO a2a_tasks(id,context_id,agent,state,task_json,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET state=excluded.state, task_json=excluded.task_json, updated_at=excluded.updated_at`,
		task.ID, task.ContextID, task.Agent, string(task.Status.State), data, timestamp(now)); err != nil {
		return err
	}
	for sequence, message := range task.History {
		if message.MessageID == "" {
			continue
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return err
		}
		created := message.Timestamp
		if created.IsZero() {
			created = now
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO a2a_messages(message_id,task_id,context_id,agent,role,sequence,message_json,created_at) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(message_id) DO NOTHING`,
			message.MessageID, task.ID, task.ContextID, task.Agent, string(message.Role), sequence, encoded, timestamp(created)); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

// queryChunk keeps IN (...) lists under SQLite's bound-parameter limit.
const queryChunk = 400

// LatestExecutionsByTask returns the most recent execution of each given
// Inkuna task. It looks the tasks up directly, so how many other executions
// exist does not matter.
func (store *Store) LatestExecutionsByTask(ctx context.Context, taskIDs []string) (map[string]Execution, error) {
	latest := make(map[string]Execution, len(taskIDs))
	for start := 0; start < len(taskIDs); start += queryChunk {
		chunk := taskIDs[start:min(start+queryChunk, len(taskIDs))]
		args := make([]any, len(chunk))
		for index, id := range chunk {
			args[index] = id
		}
		rows, err := store.db.QueryContext(ctx, `SELECT id,workflow_id,task_id,project_id,state,title,COALESCE(worktree,''),COALESCE(error,''),started_at,COALESCE(finished_at,'') FROM executions WHERE task_id IN (?`+strings.Repeat(",?", len(chunk)-1)+`) ORDER BY started_at DESC`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var execution Execution
			var started, finished string
			if err := rows.Scan(&execution.ID, &execution.WorkflowID, &execution.TaskID, &execution.ProjectID, &execution.State, &execution.Title, &execution.Worktree, &execution.Error, &started, &finished); err != nil {
				_ = rows.Close()
				return nil, err
			}
			execution.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
			if finished != "" {
				execution.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
			}
			if _, seen := latest[execution.TaskID]; !seen {
				latest[execution.TaskID] = execution
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return latest, nil
}

// OldestPendingInputs returns, for each given A2A task, the unanswered
// question that has waited longest. It is keyed by task rather than capped,
// so a long backlog cannot hide a thread's question.
func (store *Store) OldestPendingInputs(ctx context.Context, a2aTaskIDs []string) (map[string]InputRequest, error) {
	oldest := make(map[string]InputRequest)
	for start := 0; start < len(a2aTaskIDs); start += queryChunk {
		chunk := a2aTaskIDs[start:min(start+queryChunk, len(a2aTaskIDs))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, InputPending)
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := store.db.QueryContext(ctx, `SELECT id, a2a_task_id FROM inputs WHERE state=? AND a2a_task_id IN (?`+strings.Repeat(",?", len(chunk)-1)+`) ORDER BY created_at ASC`, args...)
		if err != nil {
			return nil, err
		}
		ids := make(map[string]string)
		for rows.Next() {
			var id, taskID string
			if err := rows.Scan(&id, &taskID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if _, seen := ids[taskID]; !seen {
				ids[taskID] = id
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
		for taskID, id := range ids {
			input, err := store.Input(ctx, id)
			if err != nil {
				return nil, err
			}
			oldest[taskID] = input
		}
	}
	return oldest, nil
}

// A2AThread summarizes one agent conversation for the thread list.
type A2AThread struct {
	TaskID       string      `json:"taskId"`
	ContextID    string      `json:"contextId"`
	Agent        string      `json:"agent"`
	State        string      `json:"state"`
	MessageCount int         `json:"messageCount"`
	LastMessage  a2a.Message `json:"lastMessage"`
	UpdatedAt    time.Time   `json:"updatedAt"`
}

// A2AThreads lists conversations, most recently active first.
func (store *Store) A2AThreads(ctx context.Context, limit int) ([]A2AThread, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := store.db.QueryContext(ctx, `SELECT t.id, t.context_id, t.agent, t.state, t.updated_at,
  (SELECT count(*) FROM a2a_messages m WHERE m.task_id=t.id),
  COALESCE((SELECT m.message_json FROM a2a_messages m WHERE m.task_id=t.id ORDER BY m.sequence DESC LIMIT 1), '')
FROM a2a_tasks t ORDER BY t.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	threads := make([]A2AThread, 0)
	for rows.Next() {
		var thread A2AThread
		var updated, last string
		if err := rows.Scan(&thread.TaskID, &thread.ContextID, &thread.Agent, &thread.State, &updated, &thread.MessageCount, &last); err != nil {
			return nil, err
		}
		thread.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		if last != "" {
			if err := json.Unmarshal([]byte(last), &thread.LastMessage); err != nil {
				return nil, err
			}
		}
		threads = append(threads, thread)
	}
	return threads, rows.Err()
}

// A2ATask loads one persisted task snapshot.
func (store *Store) A2ATask(ctx context.Context, id string) (a2a.Task, error) {
	var data []byte
	err := store.db.QueryRowContext(ctx, `SELECT task_json FROM a2a_tasks WHERE id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return a2a.Task{}, fmt.Errorf("%w: a2a task %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return a2a.Task{}, err
	}
	var task a2a.Task
	if err := json.Unmarshal(data, &task); err != nil {
		return a2a.Task{}, err
	}
	return task, nil
}

// A2AMessages returns a conversation's messages in order.
func (store *Store) A2AMessages(ctx context.Context, taskID string) ([]a2a.Message, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT message_json FROM a2a_messages WHERE task_id=? ORDER BY sequence ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	messages := make([]a2a.Message, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var message a2a.Message
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// TaskInputs returns every question raised on an A2A task, oldest first,
// including answered ones so a thread keeps its full record.
func (store *Store) TaskInputs(ctx context.Context, a2aTaskID string) ([]InputRequest, error) {
	return store.inputsWhere(ctx, `SELECT id FROM inputs WHERE a2a_task_id=? ORDER BY created_at ASC`, a2aTaskID)
}

// AnsweredInputs returns the questions the user has answered during a run,
// oldest first. Reviews are left out: what a review sends back reaches only
// its targets, as feedback. So are check pauses, whose answer only says
// what the run does next.
func (store *Store) AnsweredInputs(ctx context.Context, executionID string) ([]InputRequest, error) {
	return store.inputsWhere(ctx, `SELECT id FROM inputs WHERE execution_id=? AND state=? AND review=0 AND check_pause=0 ORDER BY created_at ASC`, executionID, InputAnswered)
}

// PendingReviews returns the review gates a run is paused at, oldest first.
func (store *Store) PendingReviews(ctx context.Context, executionID string) ([]InputRequest, error) {
	return store.inputsWhere(ctx, `SELECT id FROM inputs WHERE execution_id=? AND state=? AND review=1 ORDER BY created_at ASC`, executionID, InputPending)
}

// ResolveReviews closes every review gate a run is paused at, answered with
// what the person decided or cancelled with the run.
func (store *Store) ResolveReviews(ctx context.Context, executionID, state, answer string) error {
	_, err := store.db.ExecContext(ctx, `UPDATE inputs SET state=?, answer=?, resolved_at=? WHERE execution_id=? AND review=1 AND state=?`, state, answer, timestamp(clock.Now()), executionID, InputPending)
	return err
}

func (store *Store) inputsWhere(ctx context.Context, query string, args ...any) ([]InputRequest, error) {
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	inputs := make([]InputRequest, 0, len(ids))
	for _, id := range ids {
		input, err := store.Input(ctx, id)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func (store *Store) A2ATasks(ctx context.Context, contextID string) ([]a2a.Task, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT task_json FROM a2a_tasks WHERE context_id=? ORDER BY updated_at ASC`, contextID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tasks := make([]a2a.Task, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var task a2a.Task
		if err := json.Unmarshal(data, &task); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (store *Store) RecordArtifact(ctx context.Context, record ArtifactRecord) error {
	if record.Digest == "" || record.TaskID == "" || record.Kind == "" || record.Name == "" {
		return errors.New("artifact digest, task, kind, and name are required")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = clock.Now()
	}
	if record.MediaType == "" {
		record.MediaType = "application/octet-stream"
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO task_artifacts(digest,task_id,execution_id,step_id,agent,kind,name,media_type,size,description,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(digest,task_id,step_id,kind) DO UPDATE SET name=excluded.name, description=excluded.description`,
		record.Digest, record.TaskID, record.ExecutionID, record.StepID, record.Agent, record.Kind, record.Name, record.MediaType, record.Size, record.Description, timestamp(record.CreatedAt))
	return err
}

func (store *Store) TaskArtifacts(ctx context.Context, taskID string) ([]ArtifactRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT digest,task_id,COALESCE(execution_id,''),COALESCE(step_id,''),COALESCE(agent,''),kind,name,media_type,size,COALESCE(description,''),created_at FROM task_artifacts WHERE task_id=? ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := make([]ArtifactRecord, 0)
	for rows.Next() {
		var record ArtifactRecord
		var created string
		if err := rows.Scan(&record.Digest, &record.TaskID, &record.ExecutionID, &record.StepID, &record.Agent, &record.Kind, &record.Name, &record.MediaType, &record.Size, &record.Description, &created); err != nil {
			return nil, err
		}
		record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *Store) ArtifactRecord(ctx context.Context, digest string) (ArtifactRecord, error) {
	var record ArtifactRecord
	var created string
	err := store.db.QueryRowContext(ctx, `SELECT digest,task_id,COALESCE(execution_id,''),COALESCE(step_id,''),COALESCE(agent,''),kind,name,media_type,size,COALESCE(description,''),created_at FROM task_artifacts WHERE digest=? ORDER BY created_at DESC LIMIT 1`, digest).
		Scan(&record.Digest, &record.TaskID, &record.ExecutionID, &record.StepID, &record.Agent, &record.Kind, &record.Name, &record.MediaType, &record.Size, &record.Description, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactRecord{}, fmt.Errorf("%w: artifact %s", domain.ErrNotFound, digest)
	}
	if err != nil {
		return ArtifactRecord{}, err
	}
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return record, nil
}

func (store *Store) SaveCommit(ctx context.Context, record CommitRecord) error {
	if record.ExecutionID == "" || record.StepID == "" || record.Hash == "" {
		return errors.New("commit execution, step, and hash are required")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = clock.Now()
	}
	record.Round = max(record.Round, 1)
	_, err := store.db.ExecContext(ctx, `INSERT INTO commits(execution_id,step_id,round,agent,hash,subject,stat,created_at) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(execution_id,step_id,round) DO UPDATE SET agent=excluded.agent, hash=excluded.hash, subject=excluded.subject, stat=excluded.stat, created_at=excluded.created_at`,
		record.ExecutionID, record.StepID, record.Round, record.Agent, record.Hash, record.Subject, record.Stat, timestamp(record.CreatedAt))
	return err
}

func (store *Store) Commits(ctx context.Context, executionID string) ([]CommitRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT execution_id,step_id,round,agent,hash,subject,stat,created_at FROM commits WHERE execution_id=? ORDER BY created_at ASC`, executionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := make([]CommitRecord, 0)
	for rows.Next() {
		var record CommitRecord
		var created string
		if err := rows.Scan(&record.ExecutionID, &record.StepID, &record.Round, &record.Agent, &record.Hash, &record.Subject, &record.Stat, &created); err != nil {
			return nil, err
		}
		record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		records = append(records, record)
	}
	return records, rows.Err()
}
