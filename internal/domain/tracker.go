package domain

import (
	"context"
	"time"
)

// TrackerKind is a sort of change to an issue tracker that a repository's
// .inkuna/tracker.yaml can allow agents to ask for (docs/mcp-and-trackers.md).
// Archiving, deleting, and closing an issue as not planned are not kinds:
// Inkuna never does them.
type TrackerKind string

const (
	// TrackerStatus moves an issue to another status.
	TrackerStatus TrackerKind = "status"
	// TrackerComment comments on an issue.
	TrackerComment TrackerKind = "comment"
	// TrackerEdit changes an issue's fields: title, description, labels,
	// estimate, priority, assignee, cycle, or milestone, as far as the
	// tracker has them.
	TrackerEdit TrackerKind = "edit"
	// TrackerCreate creates an issue or a sub-issue.
	TrackerCreate TrackerKind = "create"
)

// TrackerKinds lists every kind in the order settings show them.
func TrackerKinds() []TrackerKind {
	return []TrackerKind{TrackerStatus, TrackerComment, TrackerEdit, TrackerCreate}
}

// ValidTrackerKind reports whether kind is one of TrackerKinds.
func ValidTrackerKind(kind TrackerKind) bool {
	for _, known := range TrackerKinds() {
		if kind == known {
			return true
		}
	}
	return false
}

// TrackerUpdate is one of the updates Inkuna makes to a run's story on its
// own, each of which a repository turns on separately.
type TrackerUpdate string

const (
	// UpdateStart moves the story to In Progress when its run starts.
	UpdateStart TrackerUpdate = "start"
	// UpdateResult comments the run's result when it ends.
	UpdateResult TrackerUpdate = "result"
	// UpdateDone attaches the merged pull request and moves the story to
	// Done when someone asks from the run.
	UpdateDone TrackerUpdate = "done"
)

// TrackerUpdates lists every update in the order settings show them.
func TrackerUpdates() []TrackerUpdate {
	return []TrackerUpdate{UpdateStart, UpdateResult, UpdateDone}
}

// Tracker reads and writes a repository's issue tracker. Implementations
// hold the project's credential and never hand it to a worker; Go makes
// every write, and only a tracker.Guard decides which writes are allowed.
// An id is the tracker's own: MAD-12 for Linear, 12 for a GitHub issue.
type Tracker interface {
	// Story reads one issue.
	Story(ctx context.Context, id string) (TrackerIssue, error)
	// Statuses lists the statuses an issue can be moved to: those
	// SetStatus would take for it. A tracker that cannot set the issue's
	// status at all refuses, saying why.
	Statuses(ctx context.Context, id string) ([]string, error)
	// SetStatus moves an issue to the status of that name. A status that
	// would close the issue as not planned is refused.
	SetStatus(ctx context.Context, id, status string) error
	// Advance moves an issue to a stage for Inkuna's own updates: to the
	// status of that name, or, when the tracker has none, to its first
	// status of the stage. An issue already in that status, or already
	// done, is left alone. Done also closes, as completed, an issue the
	// tracker keeps open apart from its status.
	Advance(ctx context.Context, id string, stage StoryStage, status string) (StageChange, error)
	// Comment adds a comment and returns its URL. With a marker, a comment
	// made earlier with the same marker is edited instead, so a retry never
	// comments twice.
	Comment(ctx context.Context, id, body, marker string) (string, error)
	// CheckEdit refuses, without asking the tracker, an edit that sets a
	// field the tracker does not have or a value it never takes. Edit
	// refuses the same edits.
	CheckEdit(fields IssueFields) error
	// Edit changes the fields that are set.
	Edit(ctx context.Context, id string, fields IssueFields) error
	// Create makes an issue, or a sub-issue when the draft names a parent.
	// With a marker, an issue made earlier with the same marker is returned
	// instead of a second one.
	Create(ctx context.Context, draft IssueDraft) (TrackerIssue, error)
	// Attach links a URL to an issue, unless it already is, and reports
	// whether it did.
	Attach(ctx context.Context, id, url, title string) (bool, error)
}

// StoryStage is where Inkuna's own updates move a run's story.
type StoryStage string

const (
	// StageStarted is In Progress: a Linear team's started states, or the
	// GitHub Project's option for it.
	StageStarted StoryStage = "started"
	// StageDone is Done: a Linear team's completed states, or the GitHub
	// Project's option for it with the issue closed as completed.
	StageDone StoryStage = "done"
)

// StageChange is what Advance did.
type StageChange struct {
	// Status is the issue's status afterwards.
	Status string
	// Changed is false when the issue already was where it was asked to go.
	Changed bool
	// Note says how the tracker took the move: the status it chose when
	// none had the name asked for, what else it did, or why it changed
	// nothing.
	Note string
}

// TrackerIssue is an issue as a tracker reports it, in no provider's format.
type TrackerIssue struct {
	// ID is the tracker's id as runs name it: MAD-12, or 12 for GitHub.
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
	// Status is the issue's status: Linear's workflow state, or the option
	// the GitHub Project's status field has for it, empty when the issue is
	// not on the project.
	Status string `json:"status,omitempty"`
	// Done is set when the issue is complete: in a completed Linear state,
	// or a GitHub issue closed as completed.
	Done bool `json:"done,omitempty"`
	// Team is the Linear team's key.
	Team string `json:"team,omitempty"`
	// Cycle names the Linear cycle the issue is in, and CurrentCycle says
	// whether that is its team's active one.
	Cycle        string   `json:"cycle,omitempty"`
	CurrentCycle bool     `json:"currentCycle,omitempty"`
	Milestone    string   `json:"milestone,omitempty"`
	Parent       string   `json:"parent,omitempty"`
	Labels       []string `json:"labels,omitempty"`
	Assignees    []string `json:"assignees,omitempty"`
	// Links are the URLs attached to a Linear issue.
	Links []string `json:"links,omitempty"`
}

// IssueFields are the fields an edit sets; an unset field is left alone.
// Each tracker refuses the fields it does not have, saying so.
type IssueFields struct {
	Title        *string  `json:"title,omitempty"`
	Description  *string  `json:"description,omitempty"`
	AddLabels    []string `json:"addLabels,omitempty"`
	RemoveLabels []string `json:"removeLabels,omitempty"`
	Estimate     *int     `json:"estimate,omitempty"`
	// Priority is Linear's: 0 none, 1 urgent, 2 high, 3 medium, 4 low.
	Priority *int `json:"priority,omitempty"`
	// Assignee is a login, a name, or an email address; "me" is the
	// credential's own user.
	Assignee  *string `json:"assignee,omitempty"`
	Cycle     *string `json:"cycle,omitempty"`
	Milestone *string `json:"milestone,omitempty"`
}

// Empty reports whether the edit would change nothing.
func (fields IssueFields) Empty() bool {
	return fields.Title == nil && fields.Description == nil && len(fields.AddLabels) == 0 && len(fields.RemoveLabels) == 0 &&
		fields.Estimate == nil && fields.Priority == nil && fields.Assignee == nil && fields.Cycle == nil && fields.Milestone == nil
}

// IssueDraft is an issue to create.
type IssueDraft struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	// Parent makes the issue a sub-issue of that one.
	Parent string `json:"parent,omitempty"`
	// Team is the Linear team's key; a sub-issue takes its parent's team.
	Team string `json:"team,omitempty"`
	// Marker identifies the draft across retries, so it is created once.
	Marker string `json:"marker,omitempty"`
}

// TrackerRefusal is a write a tracker cannot make as asked, such as a field
// it does not have or a status it does not know. Trying again will not help;
// Reason says why.
type TrackerRefusal struct{ Reason string }

func (refusal TrackerRefusal) Error() string { return refusal.Reason }

// Outcomes of a tracker write. Unchanged is a write that was not needed:
// the issue already was as asked.
const (
	TrackerApplied   = "applied"
	TrackerUnchanged = "unchanged"
	TrackerRefused   = "refused"
	TrackerFailed    = "failed"
)

// TrackerWrite records one write Inkuna made, or would have made, to a
// tracker: what it was for, what it changed, and how it went. Every write is
// recorded on the run that made it, refused ones included.
type TrackerWrite struct {
	Tracker string `json:"tracker"`
	// Action is the write: status, comment, edit, create, or attach.
	Action string `json:"action"`
	// Kind is the kind an agent asked for, or Update the update Inkuna made
	// on its own; exactly one is set.
	Kind   TrackerKind   `json:"kind,omitempty"`
	Update TrackerUpdate `json:"update,omitempty"`
	Issue  string        `json:"issue,omitempty"`
	// Change says what the write changed, or would have.
	Change string `json:"change"`
	// Outcome is applied, unchanged (the issue already was as asked),
	// refused (Inkuna or the tracker would not make it), or failed (the
	// tracker could not be reached or erred).
	Outcome string `json:"outcome"`
	// Reason says why the write was not made, or, when it was not needed,
	// how the issue already was.
	Reason string `json:"reason,omitempty"`
	// URL is what the write made or touched: a comment, or a new issue.
	URL string    `json:"url,omitempty"`
	At  time.Time `json:"at"`
}
