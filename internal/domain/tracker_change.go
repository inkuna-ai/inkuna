package domain

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Caps on what one handoff may ask of a tracker (docs/mcp-and-trackers.md).
// A change over a cap is refused, and changes past MaxTrackerChanges are
// refused without being kept one by one.
const (
	// MaxTrackerChanges is how many changes one handoff may ask for.
	MaxTrackerChanges = 20
	// MaxTrackerText caps a comment's body and an issue's description, in
	// characters.
	MaxTrackerText = 10000
	// MaxTrackerTitle caps an issue's title, in characters: GitHub's limit.
	MaxTrackerTitle = 256
)

// StoryIssue is how a tracker change names the run's own story.
const StoryIssue = "story"

// MetadataStageTracker is the step metadata key listing, comma separated,
// the tracker kinds the step's stage asks for, as against those its agent
// asks for: approval rebuilds the step's kinds from it.
const MetadataStageTracker = "stageTracker"

// TrackerChange is one change an agent asks for in its handoff's
// trackerChanges. Inkuna checks it when the step ends and makes it only
// once a person approves the run. It is one flat object for every kind, so
// the handoff's schema stays strict on every model; Problem says which
// fields each kind may carry. Issue is "story" for the run's own story, or
// an id in the tracker's format, and is left out of a create, which names
// the issue it goes under, if any, in Parent.
type TrackerChange struct {
	Kind  TrackerKind `json:"kind"`
	Issue string      `json:"issue,omitempty"`
	// Body is a comment's.
	Body string `json:"body,omitempty"`
	// Status is the status a status change moves the issue to.
	Status string `json:"status,omitempty"`
	// Title and Description are an edit's new values, or a new issue's.
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	// AddLabels are an edit's labels to add, or a new issue's labels.
	AddLabels    []string `json:"addLabels,omitempty"`
	RemoveLabels []string `json:"removeLabels,omitempty"`
	Estimate     *int     `json:"estimate,omitempty"`
	Priority     *int     `json:"priority,omitempty"`
	Assignee     *string  `json:"assignee,omitempty"`
	Cycle        *string  `json:"cycle,omitempty"`
	Milestone    *string  `json:"milestone,omitempty"`
	// Parent makes a new issue a sub-issue of that one.
	Parent string `json:"parent,omitempty"`
}

// Normalized trims every text and reads an empty one, or an empty list, as
// left out, since a model held to a strict schema may fill a field it does
// not use with one. A number another kind than edit carries is read as left
// out when it is zero, for the same reason; an edit's zero is a value
// (priority 0 is none). An agent therefore cannot clear a field.
func (change TrackerChange) Normalized() TrackerChange {
	text := func(value *string) *string {
		if value == nil || strings.TrimSpace(*value) == "" {
			return nil
		}
		trimmed := strings.TrimSpace(*value)
		return &trimmed
	}
	list := func(values []string) []string {
		kept := make([]string, 0, len(values))
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" && !slices.Contains(kept, value) {
				kept = append(kept, value)
			}
		}
		if len(kept) == 0 {
			return nil
		}
		return kept
	}
	normalized := TrackerChange{
		Kind: TrackerKind(strings.ToLower(strings.TrimSpace(string(change.Kind)))), Issue: strings.TrimSpace(change.Issue),
		Body: strings.TrimSpace(change.Body), Status: strings.TrimSpace(change.Status),
		Title: text(change.Title), Description: text(change.Description), AddLabels: list(change.AddLabels), RemoveLabels: list(change.RemoveLabels),
		Estimate: change.Estimate, Priority: change.Priority, Assignee: text(change.Assignee), Cycle: text(change.Cycle), Milestone: text(change.Milestone),
		Parent: strings.TrimSpace(change.Parent),
	}
	if normalized.Kind != TrackerEdit {
		if normalized.Estimate != nil && *normalized.Estimate == 0 {
			normalized.Estimate = nil
		}
		if normalized.Priority != nil && *normalized.Priority == 0 {
			normalized.Priority = nil
		}
	}
	return normalized
}

// Problem says why a normalized change cannot be made as asked, from what it
// says alone, or "" when nothing in it is wrong: its kind is one Inkuna
// knows, it names an issue when its kind needs one, it carries exactly the
// fields its kind takes, and its texts fit their caps. Whether the step may
// ask for the kind, and whether the issue and status exist, are checked
// against the step's grant and the tracker.
func (change TrackerChange) Problem() string {
	if !ValidTrackerKind(change.Kind) {
		return fmt.Sprintf("%q is not a kind of tracker change; the kinds are status, comment, edit, and create", change.Kind)
	}
	set := change.setFields()
	allowed := map[TrackerKind][]string{
		TrackerStatus:  {"issue", "status"},
		TrackerComment: {"issue", "body"},
		TrackerEdit:    {"issue", "title", "description", "addLabels", "removeLabels", "estimate", "priority", "assignee", "cycle", "milestone"},
		TrackerCreate:  {"title", "description", "addLabels", "parent"},
	}[change.Kind]
	var extra []string
	for _, field := range set {
		if !slices.Contains(allowed, field) {
			extra = append(extra, field)
		}
	}
	switch {
	case change.Kind == TrackerCreate && change.Issue != "":
		return "a create names the issue it goes under in parent, not issue; leave parent out for an issue of its own"
	case len(extra) > 0:
		return fmt.Sprintf("a %s change does not take %s", change.Kind, strings.Join(extra, ", "))
	case change.Kind != TrackerCreate && change.Issue == "":
		return fmt.Sprintf("a %s change needs the issue it is for: %q for the run's story, or the issue's id", change.Kind, StoryIssue)
	case change.Kind == TrackerStatus && change.Status == "":
		return "a status change needs the status to move the issue to"
	case change.Kind == TrackerComment && change.Body == "":
		return "a comment needs a body"
	case change.Kind == TrackerEdit && len(set) == 1:
		return "an edit needs at least one field to change"
	case change.Kind == TrackerCreate && change.Title == nil:
		return "a new issue needs a title"
	}
	if change.Title != nil && utf8.RuneCountInString(*change.Title) > MaxTrackerTitle {
		return fmt.Sprintf("the title is %d characters; a title may have at most %d", utf8.RuneCountInString(*change.Title), MaxTrackerTitle)
	}
	for name, text := range map[string]string{"body": change.Body, "description": deref(change.Description)} {
		if count := utf8.RuneCountInString(text); count > MaxTrackerText {
			return fmt.Sprintf("the %s is %d characters; it may have at most %d", name, count, MaxTrackerText)
		}
	}
	return ""
}

// setFields names the fields the change carries, besides its kind, in the
// order the handoff's schema lists them.
func (change TrackerChange) setFields() []string {
	fields := make([]string, 0, 4)
	add := func(name string, set bool) {
		if set {
			fields = append(fields, name)
		}
	}
	add("issue", change.Issue != "")
	add("body", change.Body != "")
	add("status", change.Status != "")
	add("title", change.Title != nil)
	add("description", change.Description != nil)
	add("addLabels", len(change.AddLabels) > 0)
	add("removeLabels", len(change.RemoveLabels) > 0)
	add("estimate", change.Estimate != nil)
	add("priority", change.Priority != nil)
	add("assignee", change.Assignee != nil)
	add("cycle", change.Cycle != nil)
	add("milestone", change.Milestone != nil)
	add("parent", change.Parent != "")
	return fields
}

// Fields are what an edit sets.
func (change TrackerChange) Fields() IssueFields {
	return IssueFields{
		Title: change.Title, Description: change.Description, AddLabels: change.AddLabels, RemoveLabels: change.RemoveLabels,
		Estimate: change.Estimate, Priority: change.Priority, Assignee: change.Assignee, Cycle: change.Cycle, Milestone: change.Milestone,
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// TrackerGrant is what a step may ask of its repository's tracker: the
// kinds it was granted, as far as the repository still allows them when the
// step starts, the tracker, and the run's story, empty when it has none. A
// step granted no kind has none.
type TrackerGrant struct {
	Tracker string        `json:"tracker"`
	Kinds   []TrackerKind `json:"kinds"`
	Story   string        `json:"story,omitempty"`
}

// StageTrackerKinds lists the tracker kinds a step's stage asks for.
func StageTrackerKinds(step WorkflowStep) []TrackerKind {
	listed := step.Metadata[MetadataStageTracker]
	if listed == "" {
		return nil
	}
	kinds := make([]TrackerKind, 0, 4)
	for _, kind := range strings.Split(listed, ",") {
		kinds = append(kinds, TrackerKind(kind))
	}
	return SortedTrackerKinds(kinds)
}

// SortedTrackerKinds returns the kinds once each, in the order settings
// show them, dropping any Inkuna does not know.
func SortedTrackerKinds(kinds []TrackerKind) []TrackerKind {
	sorted := make([]TrackerKind, 0, len(kinds))
	for _, known := range TrackerKinds() {
		if slices.Contains(kinds, known) {
			sorted = append(sorted, known)
		}
	}
	return sorted
}

// JoinTrackerKinds writes kinds as a comma-separated list, for metadata and
// for people.
func JoinTrackerKinds(kinds []TrackerKind, separator string) string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}
	return strings.Join(names, separator)
}
