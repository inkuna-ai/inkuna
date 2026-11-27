package report

import (
	"context"
	"fmt"
	"strconv"

	"github.com/inkuna-ai/inkuna/internal/diff"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// Thread is a comment thread as the page shows it, read only: where its
// lines are, how it stands, and each comment that was sent, with the
// target's reply. A thread away from its lines has the Excerpt of the diff
// its latest comment was sent with.
type Thread struct {
	Place    string
	Resolved string
	Outdated string
	Excerpt  string
	Comments []ThreadComment
}

// ThreadComment is one sent comment and the target's reply on it.
type ThreadComment struct {
	Item   string
	Round  int
	Target string
	Body   string
	// Status is the reply's status, fixed, declined, question, or no-reply,
	// or empty while the target has not replied; Reply says it in words.
	Status string
	Reply  string
	Note   string
}

var replyWords = map[string]string{
	domain.AddressedFixed:    "fixed it",
	domain.AddressedDeclined: "declined",
	domain.AddressedQuestion: "asked",
	domain.FeedbackNoReply:   "did not reply",
}

// addComments puts the run's sent comments on the page. On the branch's
// changes a thread sits under its last line, where its lines still are;
// the rest, outdated ones and every thread on another scope, are listed on
// their own with the diff they were sent with. Drafts stay in the app.
func (page *Page) addComments(ctx context.Context, store *storage.Store, repository, runID string, scope diff.ScopeKind, between diff.Range) error {
	stored, err := store.CommentThreads(ctx, runID)
	if err != nil {
		return fmt.Errorf("load the run's comments: %w", err)
	}
	var threads []storage.CommentThread
	var shown []Thread
	for _, thread := range stored {
		if view, ok := threadView(thread); ok {
			threads, shown = append(threads, thread), append(shown, view)
		}
	}
	if len(threads) == 0 {
		return nil
	}
	placements := make([]diff.Placement, len(threads))
	if scope == diff.ScopeBranch {
		anchors := make([]diff.Anchor, 0, len(threads))
		for _, thread := range threads {
			anchors = append(anchors, diff.Anchor{Path: thread.Path, Old: thread.Side == domain.SideOld, Line: thread.Line, EndLine: thread.EndLine, Base: thread.Base, Commit: thread.Commit})
		}
		if placements, err = diff.Place(ctx, repository, between.From, between.To, anchors); err != nil {
			return err
		}
	}
	files := make(map[string]*File, len(page.Files))
	for index := range page.Files {
		files[page.Files[index].Path] = &page.Files[index]
	}
	for index, thread := range threads {
		view, placed := shown[index], placements[index]
		if placed.Changed && view.Outdated == "" {
			view.Outdated = "outdated"
		}
		inline := scope == diff.ScopeBranch && !thread.Outdated() && !placed.Changed && !placed.Unknown
		if file := files[thread.Path]; inline && file != nil {
			// A thread under its lines names them where they are now, which
			// the branch may have moved since the thread was last followed.
			moved := thread
			moved.Line, moved.EndLine = placed.Line, placed.EndLine
			shownAt := view
			shownAt.Place = thread.Path + " " + linesLabel(moved)
			if file.place(shownAt, thread.Side == domain.SideOld, placed.EndLine) {
				continue
			}
		}
		view.Excerpt = latestExcerpt(thread)
		page.Comments = append(page.Comments, view)
	}
	return nil
}

// threadView is a thread's sent comments and how it stands; a thread with
// nothing sent is not shown.
func threadView(thread storage.CommentThread) (Thread, bool) {
	view := Thread{Place: thread.Path + " " + linesLabel(thread)}
	target := ""
	for _, comment := range thread.Comments {
		if comment.State != storage.CommentSent {
			continue
		}
		target = comment.Target
		view.Comments = append(view.Comments, ThreadComment{Item: comment.Item, Round: comment.Round, Target: comment.Target, Body: comment.Body,
			Status: comment.Status, Reply: replyWords[comment.Status], Note: comment.Note})
	}
	if len(view.Comments) == 0 {
		return Thread{}, false
	}
	if thread.State == storage.ThreadResolved {
		view.Resolved = "resolved by " + target
		if thread.ResolvedBy == storage.ResolvedByHuman {
			view.Resolved = "resolved by you"
		}
	}
	if thread.Outdated() {
		view.Outdated = fmt.Sprintf("outdated after round %d", thread.OutdatedRound)
	}
	return view, true
}

func linesLabel(thread storage.CommentThread) string {
	lines := fmt.Sprintf("line %d", thread.Line)
	if thread.EndLine > thread.Line {
		lines = fmt.Sprintf("lines %d–%d", thread.Line, thread.EndLine)
	}
	if thread.Side == domain.SideOld {
		return "removed " + lines
	}
	return lines
}

func latestExcerpt(thread storage.CommentThread) string {
	for index := len(thread.Comments) - 1; index >= 0; index-- {
		if excerpt := thread.Comments[index].Excerpt; excerpt != "" {
			return excerpt
		}
	}
	return ""
}

// place puts a thread under the row of its last line in both layouts, and
// reports false when the file's diff shows no such line.
func (file *File) place(thread Thread, old bool, line int) bool {
	number := strconv.Itoa(line)
	placed := false
	for index := range file.Unified {
		row := &file.Unified[index]
		if row.Hunk == "" && (row.Kind == string(diff.LineDeleted)) == old && (old && row.Old == number || !old && row.New == number) {
			row.Threads, placed = append(row.Threads, thread), true
			break
		}
	}
	if !placed {
		return false
	}
	for index := range file.Split {
		row := &file.Split[index]
		side := row.Right
		if old {
			side = row.Left
		}
		if row.Hunk == "" && side != nil && (side.Kind == string(diff.LineDeleted)) == old && side.Number == number {
			row.Threads = append(row.Threads, thread)
			break
		}
	}
	return true
}
