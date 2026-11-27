package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/diff"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// CommentsView is a run's comment threads, placed on its branch's changes
// as of commit To, and what a new comment may be written for now. Refusal is
// Go's reason the run cannot be sent back, which is also why no comment can
// be written: a comment is only ever sent with a request for changes.
type CommentsView struct {
	Threads []CommentThreadView `json:"threads"`
	Targets []ReviewTarget      `json:"targets"`
	Refusal string              `json:"refusal,omitempty"`
	To      string              `json:"to,omitempty"`
}

// CommentThreadView is a thread as the Changes view shows it: where its
// lines are in the version it was placed on, or Outdated when they changed
// since it was written (OutdatedRound says after which round, once a round
// has recorded it), or Unplaced when that version is older than the thread.
type CommentThreadView struct {
	ID            string            `json:"id"`
	Path          string            `json:"path"`
	Side          string            `json:"side"`
	Line          int               `json:"line"`
	EndLine       int               `json:"endLine"`
	Round         int               `json:"round"`
	State         string            `json:"state"`
	ResolvedBy    string            `json:"resolvedBy,omitempty"`
	Outdated      bool              `json:"outdated,omitempty"`
	OutdatedRound int               `json:"outdatedRound,omitempty"`
	Unplaced      bool              `json:"unplaced,omitempty"`
	Comments      []storage.Comment `json:"comments"`
}

// CommentDraft is a comment a person writes. ID names a draft to edit, and
// ThreadID a thread to reply in; with neither, it starts a thread on lines
// Line to EndLine of one side of Path, as the branch's changes were at
// Commit, the tip the view read.
type CommentDraft struct {
	ID       string `json:"id,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
	Path     string `json:"path,omitempty"`
	Side     string `json:"side,omitempty"`
	Line     int    `json:"line,omitempty"`
	EndLine  int    `json:"endLine,omitempty"`
	Commit   string `json:"commit,omitempty"`
	Body     string `json:"body"`
	Target   string `json:"target"`
}

// commitPattern is a full commit hash, SHA-1 or SHA-256.
var commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// RunComments lists a run's comment threads, each placed on the branch's
// changes as of to, a commit on the branch, or its tip when to is empty, and
// says whether comments can be written now and for which stages. A thread
// whose lines changed since it was written is outdated and is not placed.
func (service *Service) RunComments(runID, to string) (CommentsView, error) {
	if err := service.ready(); err != nil {
		return CommentsView{}, err
	}
	ctx := service.context()
	if _, err := service.execution(ctx, runID); err != nil {
		return CommentsView{}, fmt.Errorf("load run %s: %w", runID, err)
	}
	if to != "" && !commitPattern.MatchString(to) {
		return CommentsView{}, fmt.Errorf("%q is not a commit hash", to)
	}
	review, err := service.RunReview(runID)
	if err != nil {
		return CommentsView{}, err
	}
	view := CommentsView{Threads: []CommentThreadView{}, Targets: review.Targets, Refusal: review.Refusal}
	if view.Refusal == "" && len(view.Targets) == 0 {
		view.Refusal = "no stage of this run writes, so there is nothing to send comments to"
	}
	threads, err := service.store.CommentThreads(ctx, runID)
	if err != nil {
		return CommentsView{}, err
	}
	diffCtx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	// Threads read without the branch, once it is deleted, are still listed,
	// though nothing can place them.
	placements, tip, placeErr := service.placeThreads(diffCtx, runID, threads, to)
	view.To = firstNonEmpty(to, tip)
	for index, thread := range threads {
		shown := CommentThreadView{ID: thread.ID, Path: thread.Path, Side: thread.Side, Line: thread.Line, EndLine: thread.EndLine, Round: thread.Round,
			State: thread.State, ResolvedBy: thread.ResolvedBy, Outdated: thread.Outdated(), OutdatedRound: thread.OutdatedRound, Comments: thread.Comments}
		switch placed := placements[index]; {
		case shown.Outdated:
		case placeErr != nil || placed.Unknown:
			shown.Unplaced = true
		case placed.Changed:
			shown.Outdated = true
		default:
			shown.Line, shown.EndLine = placed.Line, placed.EndLine
		}
		view.Threads = append(view.Threads, shown)
	}
	return view, nil
}

// placeThreads places a run's threads that are not outdated on its branch's
// changes as of to, or the tip, which it returns. An outdated thread's
// placement is left empty.
func (service *Service) placeThreads(ctx context.Context, runID string, threads []storage.CommentThread, to string) ([]diff.Placement, string, error) {
	placements := make([]diff.Placement, len(threads))
	repository, between, _, err := service.runDiff(ctx, runID, diff.Scope{Kind: diff.ScopeBranch})
	if err != nil {
		return placements, "", err
	}
	to = firstNonEmpty(to, between.To)
	anchors := make([]diff.Anchor, 0, len(threads))
	at := make([]int, 0, len(threads))
	for index, thread := range threads {
		if !thread.Outdated() {
			anchors = append(anchors, anchorOf(thread))
			at = append(at, index)
		}
	}
	placed, err := diff.Place(ctx, repository, between.From, to, anchors)
	if err != nil {
		return placements, between.To, fmt.Errorf("place the run's comments: %w", err)
	}
	for index, placement := range placed {
		placements[at[index]] = placement
	}
	return placements, between.To, nil
}

func anchorOf(thread storage.CommentThread) diff.Anchor {
	return diff.Anchor{Path: thread.Path, Old: thread.Side == domain.SideOld, Line: thread.Line, EndLine: thread.EndLine, Base: thread.Base, Commit: thread.Commit}
}

// SaveComment writes a draft: a new thread's first comment, a reply in a
// thread, or a change to a draft. Drafts are written only while the run
// can be sent back, for a stage it can be sent back to, and a new thread
// only on lines of the branch's changes as they are now.
func (service *Service) SaveComment(runID string, draft CommentDraft) error {
	if err := service.ready(); err != nil {
		return err
	}
	ctx := service.context()
	execution, err := service.execution(ctx, runID)
	if err != nil {
		return fmt.Errorf("load run %s: %w", runID, err)
	}
	draft.Body = strings.TrimSpace(draft.Body)
	switch {
	case draft.Body == "":
		return errors.New("write what to change")
	case len(draft.Body) > maxFeedbackItemBytes:
		return fmt.Errorf("the comment is %d bytes; at most %d", len(draft.Body), maxFeedbackItemBytes)
	}
	review, err := service.RunReview(runID)
	if err != nil {
		return err
	}
	if review.Refusal != "" {
		return fmt.Errorf("comments are written while a run can be sent back: %s", review.Refusal)
	}
	if !offers(review.Targets, draft.Target) {
		return fmt.Errorf("%q is not a stage this run can send changes to", draft.Target)
	}
	switch {
	case draft.ID != "":
		edited, err := service.store.EditDraft(ctx, runID, draft.ID, draft.Body, draft.Target)
		if err != nil {
			return fmt.Errorf("save the comment: %w", err)
		}
		if !edited {
			return errors.New("this comment was sent or deleted; reload the changes")
		}
		return nil
	case draft.ThreadID != "":
		return service.replyInThread(ctx, runID, draft)
	}
	return service.startThread(ctx, execution, draft)
}

// startThread checks a new thread's lines against the branch's changes as
// they are now, and records it.
func (service *Service) startThread(ctx context.Context, execution storage.Execution, draft CommentDraft) error {
	if draft.Side != domain.SideNew && draft.Side != domain.SideOld {
		return fmt.Errorf("%q is not a side of the diff", draft.Side)
	}
	if draft.EndLine == 0 {
		draft.EndLine = draft.Line
	}
	ctx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	repository, between, review, err := service.runDiff(ctx, execution.ID, diff.Scope{Kind: diff.ScopeBranch})
	if err != nil {
		return err
	}
	if draft.Commit != between.To {
		return errors.New("the branch moved since these changes were read; refresh them to comment")
	}
	patch, err := diff.Patch(ctx, repository, between, review, draft.Path, true)
	if err != nil {
		return fmt.Errorf("read %s: %w", draft.Path, err)
	}
	if _, err := diff.CommentExcerpt(patch, draft.Side == domain.SideOld, draft.Line, draft.EndLine); err != nil {
		return err
	}
	thread := storage.CommentThread{ExecutionID: execution.ID, ID: newID("thread"), Path: draft.Path, Side: draft.Side, Line: draft.Line, EndLine: draft.EndLine,
		Base: between.From, Commit: between.To, Round: max(execution.Round, 1)}
	if err := service.store.StartThread(ctx, thread, storage.Comment{ID: newID("comment"), Body: draft.Body, Target: draft.Target}); err != nil {
		return fmt.Errorf("save the comment: %w", err)
	}
	return nil
}

// replyInThread adds a draft reply to an open thread whose lines are still
// as it was written on.
func (service *Service) replyInThread(ctx context.Context, runID string, draft CommentDraft) error {
	thread, err := service.store.CommentThread(ctx, runID, draft.ThreadID)
	if errors.Is(err, domain.ErrNotFound) {
		return errors.New("this thread was deleted; reload the changes")
	}
	if err != nil {
		return err
	}
	if thread.State == storage.ThreadResolved {
		return errors.New("this thread is resolved; reopen it to reply")
	}
	if outdated, err := service.threadOutdated(ctx, runID, thread); err != nil {
		return err
	} else if outdated {
		return errors.New("the lines of this thread changed since it was written; comment on them as they are now")
	}
	err = service.store.AddDraft(ctx, runID, storage.Comment{ID: newID("comment"), ThreadID: thread.ID, Body: draft.Body, Target: draft.Target})
	if err != nil && !errors.Is(err, storage.ErrDraftExists) {
		return fmt.Errorf("save the comment: %w", err)
	}
	return err
}

// threadOutdated reports whether a thread is outdated, or its lines have
// changed on the branch since it was last placed.
func (service *Service) threadOutdated(ctx context.Context, runID string, thread storage.CommentThread) (bool, error) {
	if thread.Outdated() {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	placed, _, err := service.placeThreads(ctx, runID, []storage.CommentThread{thread}, "")
	if err != nil {
		return false, err
	}
	return placed[0].Changed || placed[0].Unknown, nil
}

// DeleteComment deletes a draft, and the thread it started when it was the
// thread's only comment. A sent comment stays: it is part of a round.
func (service *Service) DeleteComment(runID, id string) error {
	if err := service.ready(); err != nil {
		return err
	}
	deleted, err := service.store.DeleteDraft(service.context(), runID, id)
	if err != nil {
		return fmt.Errorf("delete the comment: %w", err)
	}
	if !deleted {
		return errors.New("this comment was sent or deleted; reload the changes")
	}
	return nil
}

// ResolveThread resolves a thread for the person, or reopens it to reply.
func (service *Service) ResolveThread(runID, id string, resolved bool) error {
	if err := service.ready(); err != nil {
		return err
	}
	changed, err := service.store.ResolveThread(service.context(), runID, id, resolved)
	if err != nil {
		return fmt.Errorf("resolve the thread: %w", err)
	}
	if !changed {
		return errors.New("this thread was deleted; reload the changes")
	}
	return nil
}

// CommentTarget is the stage a new comment on a line goes to unless the
// person picks another: the stage whose commit last changed the line, when
// it is one changes can be sent to, else the stage the review bar starts on
// (docs/feedback-loop.md).
func (service *Service) CommentTarget(runID, path, side string, line int) (string, error) {
	if err := service.ready(); err != nil {
		return "", err
	}
	review, err := service.RunReview(runID)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(service.context(), diffTimeout)
	defer cancel()
	repository, between, _, err := service.runDiff(ctx, runID, diff.Scope{Kind: diff.ScopeBranch})
	if err != nil {
		return "", err
	}
	// A line git cannot place falls back to the bar's stage; the target is
	// a default the person can change, not a check.
	hash, err := diff.LastChange(ctx, repository, between, path, side == domain.SideOld, line)
	if err != nil || hash == "" {
		return review.Target, nil
	}
	commits, err := service.store.Commits(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("load the run's commits: %w", err)
	}
	for _, commit := range commits {
		if commit.Hash == hash && offers(review.Targets, commit.StepID) {
			return commit.StepID, nil
		}
	}
	return review.Target, nil
}

func offers(targets []ReviewTarget, id string) bool {
	return slices.ContainsFunc(targets, func(target ReviewTarget) bool { return target.ID == id })
}

// commentItems makes the round's items from the run's draft comments,
// c1, c2, … in the order their threads were started, each placed on the
// branch's changes as they are now, with the excerpt of the diff there. A
// draft whose lines changed since it was written is refused with the
// reason, as is one for a stage that does not write; used counts the bytes
// the person's other items already take toward the round's limit.
func (service *Service) commentItems(ctx context.Context, runID string, plan domain.WorkflowSpec, used int) ([]storage.FeedbackItem, []string, error) {
	threads, err := service.store.CommentThreads(ctx, runID)
	if err != nil {
		return nil, nil, fmt.Errorf("load the run's comments: %w", err)
	}
	type draft struct {
		thread  storage.CommentThread
		comment storage.Comment
	}
	var drafts []draft
	for _, thread := range threads {
		if comment, ok := thread.Draft(); ok {
			drafts = append(drafts, draft{thread, comment})
		}
	}
	if len(drafts) == 0 {
		return nil, nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	repository, between, review, err := service.runDiff(ctx, runID, diff.Scope{Kind: diff.ScopeBranch})
	if err != nil {
		return nil, nil, fmt.Errorf("read the branch's changes for the comments: %w", err)
	}
	pending := make([]storage.CommentThread, 0, len(drafts))
	for _, current := range drafts {
		pending = append(pending, current.thread)
	}
	placements, _, err := service.placeThreads(ctx, runID, pending, "")
	if err != nil {
		return nil, nil, err
	}
	steps := make(map[string]domain.WorkflowStep, len(plan.Steps))
	for _, step := range plan.Steps {
		steps[step.ID] = step
	}
	patches := map[string]diff.FilePatch{}
	items := make([]storage.FeedbackItem, 0, len(drafts))
	var targets []string
	for index, current := range drafts {
		thread, comment, placed := current.thread, current.comment, placements[index]
		where := codeSpan(thread.Path) + " " + lineLabel(thread.Line, thread.EndLine)
		step, ok := steps[comment.Target]
		switch {
		case thread.Outdated() || placed.Changed || placed.Unknown:
			return nil, nil, fmt.Errorf("the draft comment on %s was written before its lines changed; delete it and comment on the lines as they are now", where)
		case !ok || step.Kind != domain.StepAgent || step.Permission != domain.PermissionWorktree:
			return nil, nil, fmt.Errorf("the comment on %s is for %s, which does not write, so it cannot take feedback", where, comment.Target)
		}
		if used += len(comment.Body); used > maxFeedbackBytes {
			return nil, nil, fmt.Errorf("the feedback and comments are over %d bytes in all", maxFeedbackBytes)
		}
		patch, ok := patches[thread.Path]
		if !ok {
			if patch, err = diff.Patch(ctx, repository, between, review, thread.Path, true); err != nil {
				return nil, nil, fmt.Errorf("read %s for its comment: %w", thread.Path, err)
			}
			patches[thread.Path] = patch
		}
		excerpt, err := diff.CommentExcerpt(patch, thread.Side == domain.SideOld, placed.Line, placed.EndLine)
		if err != nil {
			return nil, nil, fmt.Errorf("the comment on %s: %w", where, err)
		}
		items = append(items, storage.FeedbackItem{ID: fmt.Sprintf("c%d", index+1), Target: step.ID, From: storage.FeedbackFromHuman, Text: comment.Body,
			CommentID: comment.ID, Path: thread.Path, Side: thread.Side, Line: placed.Line, EndLine: placed.EndLine, Commit: between.To, Excerpt: excerpt})
		if !slices.Contains(targets, step.ID) {
			targets = append(targets, step.ID)
		}
	}
	return items, targets, nil
}

func lineLabel(line, end int) string {
	if end > line {
		return fmt.Sprintf("lines %d–%d", line, end)
	}
	return fmt.Sprintf("line %d", line)
}

// commentThread is what was said in an item's thread before the round it
// was sent in, a line for each comment and its reply, for a reply to read
// in its place.
func commentThread(threads []storage.CommentThread, item storage.FeedbackItem) string {
	for _, thread := range threads {
		var lines []string
		mine := false
		for _, comment := range thread.Comments {
			if comment.ID == item.CommentID {
				mine = true
				continue
			}
			if comment.State != storage.CommentSent || comment.Round >= item.Round {
				continue
			}
			lines = append(lines, replyLine(storage.FeedbackItem{ID: comment.Item, Text: comment.Body, Status: comment.Status, Note: comment.Note}, 300, 300)+fmt.Sprintf(" (round %d)", comment.Round))
		}
		if mine {
			return strings.Join(lines, "\n")
		}
	}
	return ""
}

// outdateComments follows a run's threads to its branch's tip once a round
// has stopped, waiting at a gate or ended: a thread whose lines changed is
// outdated after that round, and one whose lines only moved moves with them.
// It is best effort; a thread it misses is still shown as outdated, since
// the view places threads afresh.
func (service *Service) outdateComments(runID string, round int) {
	if service.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(service.context(), diffTimeout)
	defer cancel()
	threads, err := service.store.CommentThreads(ctx, runID)
	if err != nil || len(threads) == 0 {
		return
	}
	placements, tip, err := service.placeThreads(ctx, runID, threads, "")
	if err != nil {
		_ = service.appendRunEvent(runID, syntheticEvent(runID, 0, domain.EventWarning, "The line comments were not followed to the round's changes: "+err.Error()))
		return
	}
	for index, thread := range threads {
		placed := placements[index]
		switch {
		case thread.Outdated() || placed.Unknown:
		case placed.Changed:
			_, err = service.store.OutdateThread(ctx, runID, thread.ID, thread.Commit, round)
		case thread.Commit != tip:
			_, err = service.store.MoveThread(ctx, runID, thread.ID, thread.Commit, placed.Line, placed.EndLine, tip)
		}
		if err != nil {
			_ = service.appendRunEvent(runID, syntheticEvent(runID, 0, domain.EventWarning, fmt.Sprintf("The comment on %s was not followed to the round's changes: %v", thread.Path, err)))
			err = nil
		}
	}
}
