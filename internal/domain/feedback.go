package domain

import (
	"fmt"
	"slices"
	"strings"
)

// RoundFeedback is the feedback one target was sent in a round: its items,
// and what the run's branch has changed so far.
type RoundFeedback struct {
	Round   int            `json:"round"`
	Items   []FeedbackItem `json:"items"`
	Changes *BranchChanges `json:"changes,omitempty"`
}

// Feedback authors.
const (
	FeedbackFromHuman = "human"
	FeedbackFromCheck = "check"
)

// FeedbackItem is one thing to fix, named so the target can answer it: the
// person's text (f<n>), a comment on a line of the branch's changes (c<n>),
// or a check that failed (k<n>). Text is what to fix: the person's words or
// the comment's body; for a check it can add to what Check says.
type FeedbackItem struct {
	ID      string       `json:"id"`
	From    string       `json:"from"`
	Text    string       `json:"text"`
	Comment *LineComment `json:"comment,omitempty"`
	Check   *FailedCheck `json:"check,omitempty"`
}

// Sides of a diff a line comment can be on: the line as the branch has it,
// or a line the branch removed.
const (
	SideNew = "new"
	SideOld = "old"
)

// LineComment places a feedback item on lines of the branch's changes,
// from Line to EndLine when it covers more than one. Excerpt is the diff
// around them, with ">" before the commented lines, and Thread what was
// said on them in earlier rounds, a line for each comment and its reply.
type LineComment struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine,omitempty"`
	Side    string `json:"side"`
	Excerpt string `json:"excerpt,omitempty"`
	Thread  string `json:"thread,omitempty"`
}

// FailedCheck is the evaluation command a feedback item reports, how it
// ended, and the end of its kept log. The full log stays in its check
// artifact. A command that exited has its code; one that could not start,
// or was stopped by a signal, has none, and Error says what happened.
type FailedCheck struct {
	Command    []string `json:"command"`
	ExitCode   *int     `json:"exitCode,omitempty"`
	Error      string   `json:"error,omitempty"`
	OutputTail string   `json:"outputTail,omitempty"`
}

// Ended says how a failed check ended, as a target and the inbox read it.
func (check FailedCheck) Ended() string {
	switch {
	case check.ExitCode != nil:
		return fmt.Sprintf("exited with code %d", *check.ExitCode)
	case check.Error != "":
		return "could not run: " + check.Error
	}
	return "failed"
}

// System reports whether a failed check is a system failure for a step
// that names codes as such: one that exited with a listed code, or that
// has no exit code because it could not start or a signal stopped it.
// Neither is a failure the code's writer can fix. With no codes, the step
// sends every failure back, so none is a system failure.
func (check FailedCheck) System(codes []int) bool {
	if len(codes) == 0 {
		return false
	}
	return check.ExitCode == nil || slices.Contains(codes, *check.ExitCode)
}

// BranchChanges is what a run's branch has changed so far: the commit its
// workspace started from, its tip, and each changed path with its line
// counts, at most MaxDigestChanges of them; MoreFiles counts the rest. Inkuna
// lists them itself, so a worker that may not run git still sees them.
type BranchChanges struct {
	Base      string        `json:"base"`
	Tip       string        `json:"tip"`
	Files     []ChangedFile `json:"files,omitempty"`
	MoreFiles int           `json:"moreFiles,omitempty"`
}

// ChangedFile is one path a branch changed. A path is held to
// MaxDescriptionBytes, since git reports it and nothing else bounds it.
type ChangedFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
}

// NewBranchChanges bounds a branch's changed files for an envelope.
func NewBranchChanges(base, tip string, files []ChangedFile) *BranchChanges {
	changes := &BranchChanges{Base: base, Tip: tip}
	for _, file := range files {
		if file.Path = boundPath(file.Path); file.Path != "" {
			changes.Files = append(changes.Files, file)
		}
	}
	if len(changes.Files) > MaxDigestChanges {
		changes.Files, changes.MoreFiles = changes.Files[:MaxDigestChanges], len(changes.Files)-MaxDigestChanges
	}
	return changes
}

// Statuses a target can give a feedback item in its handoff, and those of
// an item it has not answered: open while its step runs, no-reply once the
// step ended without an answer.
const (
	AddressedFixed    = "fixed"
	AddressedDeclined = "declined"
	AddressedQuestion = "question"
	FeedbackOpen      = "open"
	FeedbackNoReply   = "no-reply"
)

// Addressed is a target's reply on one feedback item: fixed or declined,
// with a note saying what changed or why not, or a question for the user.
type Addressed struct {
	Item   string `json:"item"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// SettleAddressed checks a target's replies against the items it was sent.
// An entry names its item by id; one that starts with an id, as "[f1]" or
// "f1: Handle an empty name" does, names that item. An entry naming an item
// it was not sent, or with a status other than fixed, declined, or
// question, is dropped, and each dropped entry is explained in the reasons
// returned. A later entry for an item replaces an earlier one, and the
// entries come back in the items' order.
func SettleAddressed(items []FeedbackItem, replies []Addressed) ([]Addressed, []string) {
	latest := make(map[string]Addressed, len(items))
	known := make(map[string]bool, len(items))
	for _, item := range items {
		known[item.ID] = true
	}
	var reasons []string
	for _, reply := range replies {
		reply.Item = strings.TrimSpace(reply.Item)
		if id := leadingID(reply.Item); known[id] {
			reply.Item = id
		}
		reply.Status = strings.ToLower(strings.TrimSpace(reply.Status))
		reply.Note = strings.TrimSpace(reply.Note)
		switch {
		case !known[reply.Item]:
			reasons = append(reasons, fmt.Sprintf("%q is not a feedback item of this round", prefix(reply.Item, 64)))
		case reply.Status != AddressedFixed && reply.Status != AddressedDeclined && reply.Status != AddressedQuestion:
			reasons = append(reasons, fmt.Sprintf("%s has status %q, not fixed, declined, or question", reply.Item, prefix(reply.Status, 64)))
		default:
			latest[reply.Item] = reply
		}
	}
	settled := make([]Addressed, 0, len(latest))
	for _, item := range items {
		if reply, ok := latest[item.ID]; ok {
			settled = append(settled, reply)
		}
	}
	return settled, reasons
}

// leadingID is the id a reply's item starts with: the letters and digits
// after any opening bracket, lower-cased. Models often copy the item's
// "[f1] …" line, or write "f1: …", where the schema asks for "f1".
func leadingID(item string) string {
	item = strings.TrimLeft(item, "[( ")
	end := 0
	for end < len(item) && (item[end] >= '0' && item[end] <= '9' || item[end] >= 'a' && item[end] <= 'z' || item[end] >= 'A' && item[end] <= 'Z') {
		end++
	}
	return strings.ToLower(item[:end])
}

// MaxEarlierRounds is how many runs of a step before its last a RoundDigest
// lists, the latest of them, one line each.
const MaxEarlierRounds = 10

// RoundDigest is a target's own last run of its step, as a round that does
// not resume that run's session needs it: the round it ran in, its summary,
// the paths it changed, which are committed on the branch, and a line for
// each feedback item that run was sent, with its reply, at most
// MaxDigestChanges of them. Earlier holds a line for each run of the step
// before that, oldest first; MoreEarlier counts those left out. Each line is
// held to MaxDescriptionBytes.
type RoundDigest struct {
	Round       int      `json:"round"`
	Summary     string   `json:"summary,omitempty"`
	Changes     []string `json:"changes,omitempty"`
	MoreChanges int      `json:"moreChanges,omitempty"`
	Replies     []string `json:"replies,omitempty"`
	Earlier     []string `json:"earlier,omitempty"`
	MoreEarlier int      `json:"moreEarlier,omitempty"`
}

// NewRoundDigest takes the digest of a step's last run from its handoff,
// with a line for each item that run was sent and each run before it.
func NewRoundDigest(round int, handoff HandoffResult, replies, earlier []string) *RoundDigest {
	digest := &RoundDigest{Round: round, Summary: strings.TrimSpace(handoff.Summary)}
	digest.Changes, digest.MoreChanges = digestChanges(handoff)
	digest.Replies = digestLines(replies)
	if len(digest.Replies) > MaxDigestChanges {
		digest.Replies = digest.Replies[:MaxDigestChanges]
	}
	digest.Earlier = digestLines(earlier)
	if len(digest.Earlier) > MaxEarlierRounds {
		digest.MoreEarlier = len(digest.Earlier) - MaxEarlierRounds
		digest.Earlier = digest.Earlier[digest.MoreEarlier:]
	}
	return digest
}

// digestLines puts each non-blank line on one line, held to
// MaxDescriptionBytes.
func digestLines(lines []string) []string {
	var kept []string
	for _, line := range lines {
		if line = strings.TrimSpace(strings.ReplaceAll(line, "\n", " ")); line != "" {
			if len(line) > MaxDescriptionBytes {
				line = prefix(line, MaxDescriptionBytes-len("…")) + "…"
			}
			kept = append(kept, line)
		}
	}
	return kept
}

// digestChanges lists the paths a handoff reports it changed, each held to
// MaxDescriptionBytes, at most MaxDigestChanges of them, and counts the rest.
func digestChanges(handoff HandoffResult) ([]string, int) {
	var paths []string
	for _, change := range handoff.Changes {
		if path := boundPath(change.Path); path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) > MaxDigestChanges {
		return paths[:MaxDigestChanges], len(paths) - MaxDigestChanges
	}
	return paths, 0
}

func boundPath(path string) string {
	path = strings.TrimSpace(path)
	if len(path) > MaxDescriptionBytes {
		path = prefix(path, MaxDescriptionBytes-len("…")) + "…"
	}
	return path
}

// How a step's last attempt stopped, as a ResumeNote says it.
const (
	StoppedFailed      = "failed"
	StoppedCancelled   = "cancelled"
	StoppedInterrupted = "interrupted"
)

// ResumeNote is what a step that runs again because its run was resumed is
// told of its own last attempt, which never finished: the round it was in,
// how it stopped, and, for a failure, why. Kept lists the uncommitted paths
// it left in the working directory that the person chose to keep, at most
// MaxDigestChanges of them, MoreKept counting the rest; Discarded says the
// person threw such changes away. A step that never started before its run
// stopped gets no note, as nothing of it was left.
type ResumeNote struct {
	Round     int      `json:"round"`
	Stopped   string   `json:"stopped"`
	Reason    string   `json:"reason,omitempty"`
	Kept      []string `json:"kept,omitempty"`
	MoreKept  int      `json:"moreKept,omitempty"`
	Discarded bool     `json:"discarded,omitempty"`
}

// NewResumeNote takes a stopped attempt's note, holding its reason and each
// kept path to MaxDescriptionBytes, since the error and the paths come from
// outside Inkuna and nothing else bounds them.
func NewResumeNote(round int, stopped, reason string, kept []string, discarded bool) *ResumeNote {
	note := &ResumeNote{Round: round, Stopped: stopped, Discarded: discarded}
	if lines := digestLines([]string{reason}); len(lines) > 0 {
		note.Reason = lines[0]
	}
	for _, path := range kept {
		if path = boundPath(path); path != "" {
			note.Kept = append(note.Kept, path)
		}
	}
	if len(note.Kept) > MaxDigestChanges {
		note.Kept, note.MoreKept = note.Kept[:MaxDigestChanges], len(note.Kept)-MaxDigestChanges
	}
	return note
}
