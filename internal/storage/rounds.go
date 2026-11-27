package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/clock"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// FeedbackItem is one thing a round was sent back to fix
// (docs/feedback-loop.md). Status is open until the target's step ends,
// then the target's reply, fixed, declined, or question, with its note, or
// no-reply when it gave none.
type FeedbackItem struct {
	ExecutionID string    `json:"executionId"`
	Round       int       `json:"round"`
	ID          string    `json:"id"`
	Target      string    `json:"target"`
	From        string    `json:"from"`
	Text        string    `json:"text"`
	Status      string    `json:"status"`
	Note        string    `json:"note,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	// CheckID names the failed check an item from a check reports, and Tail
	// is the end of its log that the target was sent.
	CheckID string `json:"checkId,omitempty"`
	Tail    string `json:"tail,omitempty"`
	// CommentID names the line comment an item was sent as, with where its
	// lines were on the branch's changes (in Commit for the new side, in the
	// branch's base for the old) and the Excerpt of the diff around them
	// that the target was sent.
	CommentID string `json:"commentId,omitempty"`
	Path      string `json:"path,omitempty"`
	Side      string `json:"side,omitempty"`
	Line      int    `json:"line,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Excerpt   string `json:"excerpt,omitempty"`
}

// Feedback authors and the status of an item no reply has settled.
const (
	FeedbackFromHuman = domain.FeedbackFromHuman
	FeedbackFromCheck = domain.FeedbackFromCheck
	FeedbackOpen      = domain.FeedbackOpen
)

// ReopenableStates are the ended states a person can send a run back from.
// A cancelled run stays ended: cancelling was a choice.
var ReopenableStates = []string{"completed", "failed", "interrupted"}

// Reviewing is the state of a run paused at a review gate. Nothing of it
// runs until a person approves it or requests changes, so it holds no task
// slot and survives a restart.
const Reviewing = "reviewing"

// StartRound moves a run from round to round+1, queued, and records the new
// round's feedback, all in one transaction. It reports false, changing
// nothing, when the run is no longer in that round or in a state it can be
// sent back from, ended or reviewing, so two requests for the same round
// cannot both start one.
func (store *Store) StartRound(ctx context.Context, executionID string, round int, items []FeedbackItem) (bool, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = transaction.Rollback() }()
	from := append(slices.Clone(ReopenableStates), Reviewing)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(from)), ",")
	args := []any{executionID, round}
	for _, state := range from {
		args = append(args, state)
	}
	result, err := transaction.ExecContext(ctx, `UPDATE executions SET round=round+1, state='queued', error='', finished_at=NULL WHERE id=? AND round=? AND state IN (`+placeholders+`)`, args...)
	if err != nil {
		return false, fmt.Errorf("start round: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return false, err
	}
	if err := recordFeedback(ctx, transaction, executionID, round+1, items); err != nil {
		return false, err
	}
	return true, transaction.Commit()
}

// ResumableStates are the ended states a person can resume a run from: the
// ones it stopped in before it finished. Resuming a cancelled run is a choice
// as deliberate as cancelling it was.
var ResumableStates = []string{"failed", "cancelled", "interrupted"}

// StartResumeRound moves a run that stopped in round to round+1, queued, and
// records the new round as a resume, all in one transaction. The round runs
// what the run left unfinished. Carry names the steps it runs again that
// round sent back with feedback: their items, which they never finished,
// are sent again in the new round, open, with the line comments they were
// sent as moving with them, so a resume never drops what a person or a
// check asked for. Given an edit, the new round runs under edit.Plan, which
// replaces the run's plan, and the resume keeps edit.Replaced, so the rounds
// before it keep the plan they ran. It reports false, changing nothing, when
// the run is no longer in that round or in a state it can be resumed from,
// so two resumes of the same round cannot both start one.
func (store *Store) StartResumeRound(ctx context.Context, executionID string, round int, carry []string, discarded bool, edit *PlanEdit) (bool, error) {
	replaced, plan, steps := "", "", ""
	if edit != nil {
		if len(edit.Steps) == 0 {
			return false, errors.New("an edited resume names the steps the person edited")
		}
		before, err := json.Marshal(edit.Replaced)
		if err != nil {
			return false, err
		}
		after, err := json.Marshal(edit.Plan)
		if err != nil {
			return false, err
		}
		edited, err := json.Marshal(edit.Steps)
		if err != nil {
			return false, err
		}
		replaced, plan, steps = string(before), string(after), string(edited)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = transaction.Rollback() }()
	args := []any{executionID, round}
	for _, state := range ResumableStates {
		args = append(args, state)
	}
	moved, err := store.changed(transaction.ExecContext(ctx, `UPDATE executions SET round=round+1, state='queued', error='', finished_at=NULL WHERE id=? AND round=? AND state IN (`+placeholders(len(ResumableStates))+`)`, args...))
	if err != nil {
		return false, fmt.Errorf("start round %d: %w", round+1, err)
	}
	if !moved {
		return false, nil
	}
	now := timestamp(clock.Now())
	if _, err := transaction.ExecContext(ctx, `INSERT INTO resumes(execution_id,round,discarded,created_at,replaced_plan_json,edited_steps) VALUES(?,?,?,?,?,?)`, executionID, round+1, discarded, now, replaced, steps); err != nil {
		return false, fmt.Errorf("record the resume: %w", err)
	}
	if plan != "" {
		if _, err := transaction.ExecContext(ctx, `UPDATE executions SET plan_json=? WHERE id=?`, plan, executionID); err != nil {
			return false, fmt.Errorf("record the edited plan: %w", err)
		}
	}
	if len(carry) > 0 {
		args := []any{round + 1, FeedbackOpen, now, executionID, round}
		for _, target := range carry {
			args = append(args, target)
		}
		in := placeholders(len(carry))
		// The comments move first, while their items are still found only in
		// round.
		if _, err := transaction.ExecContext(ctx, `UPDATE comments SET round=? WHERE execution_id=? AND round=? AND item IN (SELECT id FROM feedback WHERE execution_id=? AND round=? AND target IN (`+in+`))`,
			append([]any{round + 1, executionID, round, executionID, round}, args[5:]...)...); err != nil {
			return false, fmt.Errorf("carry the line comments: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO feedback(execution_id,round,id,target,author,text,status,note,created_at,check_id,tail,comment_id,path,side,line,end_line,commit_hash,excerpt)
SELECT execution_id,?,id,target,author,text,?,'',?,check_id,tail,comment_id,path,side,line,end_line,commit_hash,excerpt FROM feedback WHERE execution_id=? AND round=? AND target IN (`+in+`)`, args...); err != nil {
			return false, fmt.Errorf("carry the feedback: %w", err)
		}
	}
	return true, transaction.Commit()
}

// PlanEdit is a resume under a plan the person edited: the plan the round
// runs under, the plan it replaces, and the steps the person edited.
type PlanEdit struct {
	Replaced domain.WorkflowSpec
	Plan     domain.WorkflowSpec
	Steps    []string
}

// ResumedRound is a round a person started by resuming a run: whether they
// discarded what the stopped steps had left uncommitted, and, for a round
// under a plan they edited, the steps they edited.
type ResumedRound struct {
	Discarded bool
	Edited    []string
}

// ResumedRounds lists the rounds of a run a person started by resuming it.
func (store *Store) ResumedRounds(ctx context.Context, executionID string) (map[int]ResumedRound, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT round, discarded, edited_steps FROM resumes WHERE execution_id=?`, executionID)
	if err != nil {
		return nil, fmt.Errorf("list the run's resumes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	rounds := make(map[int]ResumedRound)
	for rows.Next() {
		var round int
		var resumed ResumedRound
		var edited string
		if err := rows.Scan(&round, &resumed.Discarded, &edited); err != nil {
			return nil, err
		}
		if edited != "" {
			if err := json.Unmarshal([]byte(edited), &resumed.Edited); err != nil {
				return nil, fmt.Errorf("decode the steps edited for round %d: %w", round, err)
			}
		}
		rounds[round] = resumed
	}
	return rounds, rows.Err()
}

// EditedRounds is, for each round a resume under an edited plan started,
// the steps the person edited.
func EditedRounds(resumes map[int]ResumedRound) map[int][]string {
	edited := make(map[int][]string)
	for round, resumed := range resumes {
		if len(resumed.Edited) > 0 {
			edited[round] = resumed.Edited
		}
	}
	return edited
}

// RoundPlan returns the plan a run ran round under: the plan the first
// edited resume after round replaced, or else the run's plan as it stands.
func (store *Store) RoundPlan(ctx context.Context, executionID string, round int) (domain.WorkflowSpec, error) {
	var replaced string
	err := store.db.QueryRowContext(ctx, `SELECT replaced_plan_json FROM resumes WHERE execution_id=? AND round>? AND replaced_plan_json<>'' ORDER BY round LIMIT 1`, executionID, round).Scan(&replaced)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ExecutionPlan(ctx, executionID)
	}
	if err != nil {
		return domain.WorkflowSpec{}, fmt.Errorf("load the plan of round %d: %w", round, err)
	}
	var plan domain.WorkflowSpec
	if err := json.Unmarshal([]byte(replaced), &plan); err != nil {
		return domain.WorkflowSpec{}, fmt.Errorf("decode the plan of round %d: %w", round, err)
	}
	return plan, nil
}

// placeholders is n comma-separated SQL placeholders.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// recordFeedback stores a round's items as part of the transaction that
// starts the round, and marks the draft comments they were made from sent.
func recordFeedback(ctx context.Context, transaction *sql.Tx, executionID string, round int, items []FeedbackItem) error {
	now := timestamp(clock.Now())
	for _, item := range items {
		status := item.Status
		if status == "" {
			status = FeedbackOpen
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO feedback(execution_id,round,id,target,author,text,status,note,created_at,check_id,tail,comment_id,path,side,line,end_line,commit_hash,excerpt) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			executionID, round, item.ID, item.Target, item.From, item.Text, status, item.Note, now, item.CheckID, item.Tail,
			item.CommentID, item.Path, item.Side, item.Line, item.EndLine, item.Commit, item.Excerpt); err != nil {
			return fmt.Errorf("record feedback %s: %w", item.ID, err)
		}
		if item.CommentID != "" {
			if err := sendComment(ctx, transaction, executionID, round, item); err != nil {
				return err
			}
		}
	}
	return nil
}

// StageCap is how many automatic rounds an evaluation stage's failed checks
// may start since a person last sent the run back.
type StageCap struct {
	Stage string
	Max   int
}

// CheckRound is what starting a check round did: whether it started, and
// how many automatic rounds each failed stage has used since a person last
// sent the run back, this one included once it started. Over names the
// first stage, in the order given, that one more round would take past its
// cap, and then nothing changed.
type CheckRound struct {
	Started bool
	Over    string
	Used    map[string]int
}

// StartCheckRound starts the next round of a run that is running in round,
// with the items its failed checks send back, unless that would take one of
// the failed stages past its cap. The counts and the round change in one
// transaction, so the decision is made on the counts the round changes. A
// round counts once for each stage whose checks it sends back, and only
// rounds after the latest one a person started count. It reports Started
// false, changing nothing, when the run is no longer running in round.
func (store *Store) StartCheckRound(ctx context.Context, executionID string, round int, items []FeedbackItem, caps []StageCap) (CheckRound, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return CheckRound{}, err
	}
	defer func() { _ = transaction.Rollback() }()
	started := CheckRound{Used: make(map[string]int, len(caps))}
	// The round moves first, so the transaction takes the write lock, and
	// waits for it, before it reads the counts; one that read first could not
	// write once another connection had. Over a cap, it rolls back.
	moved, err := store.changed(transaction.ExecContext(ctx, `UPDATE executions SET round=round+1 WHERE id=? AND round=? AND state='running'`, executionID, round))
	if err != nil {
		return CheckRound{}, fmt.Errorf("start round %d: %w", round+1, err)
	}
	if !moved {
		return started, nil
	}
	// A person starts a round by sending the run back or by resuming it.
	var human int
	if err := transaction.QueryRowContext(ctx, `SELECT MAX(COALESCE((SELECT MAX(round) FROM feedback WHERE execution_id=? AND author=?), 0), COALESCE((SELECT MAX(round) FROM resumes WHERE execution_id=?), 0))`,
		executionID, FeedbackFromHuman, executionID).Scan(&human); err != nil {
		return CheckRound{}, fmt.Errorf("find the latest round a person started: %w", err)
	}
	for _, stage := range caps {
		var used int
		if err := transaction.QueryRowContext(ctx, `SELECT count(DISTINCT feedback.round) FROM feedback JOIN checks ON checks.id = feedback.check_id
WHERE feedback.execution_id=? AND feedback.author=? AND feedback.round>? AND checks.step_id=?`, executionID, FeedbackFromCheck, human, stage.Stage).Scan(&used); err != nil {
			return CheckRound{}, fmt.Errorf("count the automatic rounds of %s: %w", stage.Stage, err)
		}
		started.Used[stage.Stage] = used
		if used+1 > stage.Max && started.Over == "" {
			started.Over = stage.Stage
		}
	}
	if started.Over != "" {
		return started, nil
	}
	if err := recordFeedback(ctx, transaction, executionID, round+1, items); err != nil {
		return CheckRound{}, err
	}
	if err := transaction.Commit(); err != nil {
		return CheckRound{}, err
	}
	for _, stage := range caps {
		started.Used[stage.Stage]++
	}
	started.Started = true
	return started, nil
}

// ResumeReview moves a run paused at a review gate in round back to queued,
// to carry on past the gates it was approved at. It reports false, changing
// nothing, when the run is no longer reviewing in that round, so an Approve
// cannot race a Request changes or a Cancel.
func (store *Store) ResumeReview(ctx context.Context, executionID string, round int) (bool, error) {
	return store.changed(store.db.ExecContext(ctx, `UPDATE executions SET state='queued' WHERE id=? AND round=? AND state=?`, executionID, round, Reviewing))
}

// CancelReview ends a run paused at a review gate as cancelled, and reports
// false, changing nothing, when it is no longer reviewing.
func (store *Store) CancelReview(ctx context.Context, executionID string) (bool, error) {
	return store.changed(store.db.ExecContext(ctx, `UPDATE executions SET state='cancelled', finished_at=? WHERE id=? AND state=?`, timestamp(clock.Now()), executionID, Reviewing))
}

// SetRunState sets a step run's state without finishing it, as a review
// gate's does while it waits for the person.
func (store *Store) SetRunState(ctx context.Context, runID, state string) error {
	_, err := store.db.ExecContext(ctx, `UPDATE runs SET state=? WHERE id=?`, state, runID)
	return err
}

func (store *Store) changed(result sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// Feedback lists a round's feedback items in the order they were given.
func (store *Store) Feedback(ctx context.Context, executionID string, round int) ([]FeedbackItem, error) {
	return store.feedback(ctx, `execution_id=? AND round=?`, executionID, round)
}

// TargetFeedback lists every round's feedback items for one step of a run,
// by round and in the order they were given.
func (store *Store) TargetFeedback(ctx context.Context, executionID, stepID string) ([]FeedbackItem, error) {
	return store.feedback(ctx, `execution_id=? AND target=?`, executionID, stepID)
}

// SettleFeedback records a target's replies on its items in a round, once
// its step has ended: each replied item takes its reply's status and note,
// and every other item still open becomes no-reply. A line comment the
// target fixed resolves its thread. It returns the target's items as they
// now stand.
func (store *Store) SettleFeedback(ctx context.Context, executionID string, round int, target string, replies []domain.Addressed) ([]FeedbackItem, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Rollback() }()
	for _, reply := range replies {
		if _, err := transaction.ExecContext(ctx, `UPDATE feedback SET status=?, note=? WHERE execution_id=? AND round=? AND target=? AND id=?`,
			reply.Status, reply.Note, executionID, round, target, reply.Item); err != nil {
			return nil, fmt.Errorf("record the reply on %s: %w", reply.Item, err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE feedback SET status=? WHERE execution_id=? AND round=? AND target=? AND status=?`,
		domain.FeedbackNoReply, executionID, round, target, FeedbackOpen); err != nil {
		return nil, fmt.Errorf("record the items left without a reply: %w", err)
	}
	if err := resolveFixed(ctx, transaction, executionID, round, target); err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return store.feedback(ctx, `execution_id=? AND round=? AND target=?`, executionID, round, target)
}

// RunFeedback is every item a run was sent back with, oldest round first.
func (store *Store) RunFeedback(ctx context.Context, executionID string) ([]FeedbackItem, error) {
	return store.feedback(ctx, "execution_id=?", executionID)
}

func (store *Store) feedback(ctx context.Context, where string, args ...any) ([]FeedbackItem, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT execution_id,round,id,target,author,text,status,note,created_at,check_id,tail,comment_id,path,side,line,end_line,commit_hash,excerpt FROM feedback WHERE `+where+` ORDER BY round, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]FeedbackItem, 0)
	for rows.Next() {
		var item FeedbackItem
		var created string
		if err := rows.Scan(&item.ExecutionID, &item.Round, &item.ID, &item.Target, &item.From, &item.Text, &item.Status, &item.Note, &created, &item.CheckID, &item.Tail,
			&item.CommentID, &item.Path, &item.Side, &item.Line, &item.EndLine, &item.Commit, &item.Excerpt); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

// ownStepRun matches a step's own run in its round, never a peer's, whose
// row shares its step's id: <run>-<step> in round 1, <run>.r<N>.<step>
// after.
const ownStepRun = `(id = execution_id || '-' || step_id OR id = execution_id || '.r' || round || '.' || step_id)`

// StepResults returns the handoff of each step of a run whose latest
// attempt, in whatever round, completed. A step whose latest attempt
// failed, was cancelled, or never finished has none, even when an earlier
// round's passed, so the next round runs it again rather than trusting a
// result older than the work it checked. Peers' rows share their step's
// id, so only a step's own row counts: <run>-<step> in round 1,
// <run>.r<N>.<step> after.
func (store *Store) StepResults(ctx context.Context, executionID string) (map[string]domain.HandoffResult, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT step_id, state, handoff_json FROM runs
WHERE execution_id=? AND `+ownStepRun+`
ORDER BY round`, executionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	type attempt struct {
		state string
		data  []byte
	}
	latest := make(map[string]attempt)
	for rows.Next() {
		var step string
		var current attempt
		if err := rows.Scan(&step, &current.state, &current.data); err != nil {
			return nil, err
		}
		latest[step] = current
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	results := make(map[string]domain.HandoffResult)
	for step, last := range latest {
		if last.state != "completed" || last.data == nil {
			continue
		}
		var handoff domain.HandoffResult
		if err := json.Unmarshal(last.data, &handoff); err != nil {
			return nil, fmt.Errorf("decode the result of step %s: %w", step, err)
		}
		results[step] = handoff
	}
	return results, nil
}

// SaveExecutionPlan keeps the plan a run was approved with, so a later
// round runs that plan even after its task is planned again.
func (store *Store) SaveExecutionPlan(ctx context.Context, executionID string, plan domain.WorkflowSpec) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `UPDATE executions SET plan_json=? WHERE id=?`, string(data), executionID)
	return err
}

// ExecutionPlan returns the plan a run was approved with. A run from before
// plans were kept falls back to its workflow's stored plan, but only while
// that is still the approved plan it ran: approved no later than the run
// started, and not planned again since.
func (store *Store) ExecutionPlan(ctx context.Context, executionID string) (domain.WorkflowSpec, error) {
	var plan, workflowID, started string
	err := store.db.QueryRowContext(ctx, `SELECT plan_json, workflow_id, started_at FROM executions WHERE id=?`, executionID).Scan(&plan, &workflowID, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowSpec{}, fmt.Errorf("%w: execution %s", domain.ErrNotFound, executionID)
	}
	if err != nil {
		return domain.WorkflowSpec{}, err
	}
	data := []byte(plan)
	if plan == "" {
		var state string
		var approved sql.NullString
		if err := store.db.QueryRowContext(ctx, `SELECT state, approved_at, spec_json FROM workflows WHERE id=?`, workflowID).Scan(&state, &approved, &data); err != nil {
			return domain.WorkflowSpec{}, fmt.Errorf("load the run's plan: %w", err)
		}
		approvedAt, _ := time.Parse(time.RFC3339Nano, approved.String)
		startedAt, _ := time.Parse(time.RFC3339Nano, started)
		if state != "approved" || !approved.Valid || approvedAt.After(startedAt) {
			return domain.WorkflowSpec{}, errors.New("the task was planned again after this run started, and the run did not keep the plan it was approved with")
		}
	}
	var spec domain.WorkflowSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return domain.WorkflowSpec{}, fmt.Errorf("decode the run's plan: %w", err)
	}
	return spec, nil
}
