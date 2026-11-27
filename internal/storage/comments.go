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

// States of a comment thread and of a comment in it (docs/feedback-loop.md).
// A thread is open until its target fixes what its latest comment asked, or
// a person resolves it. A comment is a draft until it is sent.
const (
	ThreadOpen     = "open"
	ThreadResolved = "resolved"
	CommentDraft   = "draft"
	CommentSent    = "sent"
)

// Who resolved a thread: the person, or the target, by fixing it.
const (
	ResolvedByHuman = "human"
	ResolvedByAgent = "agent"
)

// ErrDraftExists refuses a second draft in a thread that has one; the person
// edits that draft instead.
var ErrDraftExists = errors.New("this thread already has a draft; edit it instead")

// CommentThread is a place on a run's branch changes that a person commented
// on, with what was said there. Lines are on one side: SideNew numbers them
// in Commit, the branch's version, and SideOld in Base, for lines the branch
// removed. A thread follows its lines as the branch moves, and is outdated
// once they change.
type CommentThread struct {
	ExecutionID   string    `json:"executionId"`
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	Side          string    `json:"side"`
	Line          int       `json:"line"`
	EndLine       int       `json:"endLine"`
	Base          string    `json:"base"`
	Commit        string    `json:"commit"`
	Round         int       `json:"round"`
	State         string    `json:"state"`
	ResolvedBy    string    `json:"resolvedBy,omitempty"`
	OutdatedRound int       `json:"outdatedRound,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	Comments      []Comment `json:"comments"`
}

// Comment is what a person wrote in a thread, for the target it names. Once
// sent, Round and Item name the feedback item it went as, and Status and
// Note are the target's reply on that item, with the Excerpt of the diff it
// was shown.
type Comment struct {
	ID        string    `json:"id"`
	ThreadID  string    `json:"threadId"`
	Body      string    `json:"body"`
	Target    string    `json:"target"`
	State     string    `json:"state"`
	Round     int       `json:"round,omitempty"`
	Item      string    `json:"item,omitempty"`
	Status    string    `json:"status,omitempty"`
	Note      string    `json:"note,omitempty"`
	Excerpt   string    `json:"excerpt,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Outdated reports whether the thread's lines changed after it was written.
func (thread CommentThread) Outdated() bool { return thread.OutdatedRound > 0 }

// Draft is the thread's unsent comment, if it has one.
func (thread CommentThread) Draft() (Comment, bool) {
	for _, comment := range thread.Comments {
		if comment.State == CommentDraft {
			return comment, true
		}
	}
	return Comment{}, false
}

// StartThread records a new thread with its first comment, a draft.
func (store *Store) StartThread(ctx context.Context, thread CommentThread, comment Comment) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	now := timestamp(clock.Now())
	if _, err := transaction.ExecContext(ctx, `INSERT INTO comment_threads(execution_id,id,path,side,line,end_line,base,commit_hash,round,state,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		thread.ExecutionID, thread.ID, thread.Path, thread.Side, thread.Line, thread.EndLine, thread.Base, thread.Commit, thread.Round, ThreadOpen, now); err != nil {
		return fmt.Errorf("record thread: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO comments(execution_id,id,thread_id,body,target,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		thread.ExecutionID, comment.ID, thread.ID, comment.Body, comment.Target, CommentDraft, now, now); err != nil {
		return fmt.Errorf("record comment: %w", err)
	}
	return transaction.Commit()
}

// AddDraft records a draft reply in a thread, refusing with ErrDraftExists
// when the thread has one, so each thread sends at most one comment a round.
func (store *Store) AddDraft(ctx context.Context, executionID string, comment Comment) error {
	now := timestamp(clock.Now())
	added, err := store.changed(store.db.ExecContext(ctx, `INSERT INTO comments(execution_id,id,thread_id,body,target,state,created_at,updated_at)
SELECT ?,?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM comments WHERE execution_id=? AND thread_id=? AND state=?)`,
		executionID, comment.ID, comment.ThreadID, comment.Body, comment.Target, CommentDraft, now, now, executionID, comment.ThreadID, CommentDraft))
	if err != nil {
		return fmt.Errorf("record comment: %w", err)
	}
	if !added {
		return ErrDraftExists
	}
	return nil
}

// EditDraft changes a draft's text and target, and reports false, changing
// nothing, when it is no longer a draft.
func (store *Store) EditDraft(ctx context.Context, executionID, id, body, target string) (bool, error) {
	return store.changed(store.db.ExecContext(ctx, `UPDATE comments SET body=?, target=?, updated_at=? WHERE execution_id=? AND id=? AND state=?`,
		body, target, timestamp(clock.Now()), executionID, id, CommentDraft))
}

// DeleteDraft removes a draft, and the thread it started when it was the
// thread's only comment. It reports false when there is no such draft.
func (store *Store) DeleteDraft(ctx context.Context, executionID, id string) (bool, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = transaction.Rollback() }()
	var thread string
	err = transaction.QueryRowContext(ctx, `SELECT thread_id FROM comments WHERE execution_id=? AND id=? AND state=?`, executionID, id, CommentDraft).Scan(&thread)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM comments WHERE execution_id=? AND id=?`, executionID, id); err != nil {
		return false, fmt.Errorf("delete comment: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM comment_threads WHERE execution_id=? AND id=? AND NOT EXISTS (SELECT 1 FROM comments WHERE execution_id=? AND thread_id=?)`,
		executionID, thread, executionID, thread); err != nil {
		return false, fmt.Errorf("delete its thread: %w", err)
	}
	return true, transaction.Commit()
}

// ResolveThread resolves a thread for the person, or reopens it, and
// reports false when the run has no such thread.
func (store *Store) ResolveThread(ctx context.Context, executionID, id string, resolved bool) (bool, error) {
	state, by := ThreadOpen, ""
	if resolved {
		state, by = ThreadResolved, ResolvedByHuman
	}
	return store.changed(store.db.ExecContext(ctx, `UPDATE comment_threads SET state=?, resolved_by=? WHERE execution_id=? AND id=?`, state, by, executionID, id))
}

// MoveThread carries a thread to the same lines in a later commit, and
// reports false, changing nothing, when the thread is no longer where it was
// or is outdated, as when a send moved it first.
func (store *Store) MoveThread(ctx context.Context, executionID, id, from string, line, end int, commit string) (bool, error) {
	return store.changed(store.db.ExecContext(ctx, `UPDATE comment_threads SET line=?, end_line=?, commit_hash=? WHERE execution_id=? AND id=? AND commit_hash=? AND outdated_round=0`,
		line, end, commit, executionID, id, from))
}

// OutdateThread marks a thread outdated after round, when its lines changed
// since commit, and reports false, changing nothing, when the thread has
// moved on from commit or is outdated already.
func (store *Store) OutdateThread(ctx context.Context, executionID, id, commit string, round int) (bool, error) {
	return store.changed(store.db.ExecContext(ctx, `UPDATE comment_threads SET outdated_round=? WHERE execution_id=? AND id=? AND commit_hash=? AND outdated_round=0`,
		round, executionID, id, commit))
}

// CommentThreads lists a run's threads, oldest first, each with its
// comments in the order they were written and the replies on those sent.
func (store *Store) CommentThreads(ctx context.Context, executionID string) ([]CommentThread, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,path,side,line,end_line,base,commit_hash,round,state,resolved_by,outdated_round,created_at FROM comment_threads WHERE execution_id=? ORDER BY rowid`, executionID)
	if err != nil {
		return nil, fmt.Errorf("query the threads of run %s: %w", executionID, err)
	}
	threads := make([]CommentThread, 0)
	at := make(map[string]int)
	for rows.Next() {
		thread := CommentThread{ExecutionID: executionID, Comments: make([]Comment, 0, 1)}
		var created string
		if err := rows.Scan(&thread.ID, &thread.Path, &thread.Side, &thread.Line, &thread.EndLine, &thread.Base, &thread.Commit, &thread.Round, &thread.State, &thread.ResolvedBy, &thread.OutdatedRound, &created); err != nil {
			_ = rows.Close()
			return nil, err
		}
		thread.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		at[thread.ID] = len(threads)
		threads = append(threads, thread)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	rows, err = store.db.QueryContext(ctx, `SELECT comments.id,comments.thread_id,comments.body,comments.target,comments.state,comments.round,comments.item,comments.created_at,comments.updated_at,
COALESCE(feedback.status,''),COALESCE(feedback.note,''),COALESCE(feedback.excerpt,'')
FROM comments LEFT JOIN feedback ON feedback.execution_id=comments.execution_id AND feedback.round=comments.round AND feedback.id=comments.item AND comments.item<>''
WHERE comments.execution_id=? ORDER BY comments.rowid`, executionID)
	if err != nil {
		return nil, fmt.Errorf("query the comments of run %s: %w", executionID, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var comment Comment
		var created, updated string
		if err := rows.Scan(&comment.ID, &comment.ThreadID, &comment.Body, &comment.Target, &comment.State, &comment.Round, &comment.Item, &created, &updated, &comment.Status, &comment.Note, &comment.Excerpt); err != nil {
			return nil, err
		}
		comment.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		comment.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		if index, ok := at[comment.ThreadID]; ok {
			threads[index].Comments = append(threads[index].Comments, comment)
		}
	}
	return threads, rows.Err()
}

// CommentThread is one of a run's threads.
func (store *Store) CommentThread(ctx context.Context, executionID, id string) (CommentThread, error) {
	threads, err := store.CommentThreads(ctx, executionID)
	if err != nil {
		return CommentThread{}, err
	}
	for _, thread := range threads {
		if thread.ID == id {
			return thread, nil
		}
	}
	return CommentThread{}, domain.ErrNotFound
}

// sendComment marks a draft sent as a round's item, as part of the
// transaction that records the item, and moves its thread to where the item
// placed it, open again. It fails when the draft is gone, so a comment
// deleted while it was being sent is never recorded as sent.
func sendComment(ctx context.Context, transaction *sql.Tx, executionID string, round int, item FeedbackItem) error {
	now := timestamp(clock.Now())
	var thread string
	if err := transaction.QueryRowContext(ctx, `SELECT thread_id FROM comments WHERE execution_id=? AND id=? AND state=?`, executionID, item.CommentID, CommentDraft).Scan(&thread); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("the draft comment for %s was deleted or sent while sending; send again", item.ID)
		}
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE comments SET state=?, round=?, item=?, updated_at=? WHERE execution_id=? AND id=?`,
		CommentSent, round, item.ID, now, executionID, item.CommentID); err != nil {
		return fmt.Errorf("record comment %s as sent: %w", item.ID, err)
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE comment_threads SET line=?, end_line=?, commit_hash=?, state=?, resolved_by='' WHERE execution_id=? AND id=?`,
		item.Line, item.EndLine, item.Commit, ThreadOpen, executionID, thread); err != nil {
		return fmt.Errorf("move the thread of %s: %w", item.ID, err)
	}
	return nil
}

// resolveFixed resolves, for the target, each thread whose comment a target
// fixed in a round.
func resolveFixed(ctx context.Context, transaction *sql.Tx, executionID string, round int, target string) error {
	_, err := transaction.ExecContext(ctx, `UPDATE comment_threads SET state=?, resolved_by=? WHERE execution_id=? AND id IN (
SELECT comments.thread_id FROM comments JOIN feedback ON feedback.execution_id=comments.execution_id AND feedback.round=comments.round AND feedback.id=comments.item
WHERE comments.execution_id=? AND feedback.round=? AND feedback.target=? AND feedback.status=?)`,
		ThreadResolved, ResolvedByAgent, executionID, executionID, round, target, domain.AddressedFixed)
	if err != nil {
		return fmt.Errorf("resolve the fixed threads: %w", err)
	}
	return nil
}
