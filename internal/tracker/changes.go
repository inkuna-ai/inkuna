package tracker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// Checked is a change an agent asked for that Go accepted when its step
// ended: what the Guard makes once a person approves the run.
type Checked struct {
	// Change is the change as the agent asked for it, normalized, with a
	// status named as the tracker names it.
	Change domain.TrackerChange `json:"change"`
	// Issue is the tracker's id of the issue the change acts on, the run's
	// story resolved: for a create, its parent, empty for an issue of its
	// own.
	Issue string `json:"issue,omitempty"`
	// Team is the Linear team a new issue without a parent goes in: the
	// run's story's.
	Team string `json:"team,omitempty"`
}

// readResult is an issue read once while checking, or why it could not be.
type readResult struct {
	issue domain.TrackerIssue
	err   error
}

// Check decides, when a step ends, whether a change it asked for can be
// made: the change is whole and within its caps, the step was granted its
// kind and the settings allow it, the issue it names exists, a status is
// one the tracker has, and an edit's fields are ones the tracker takes.
// Every refusal is a domain.TrackerRefusal whose reason the run shows; a
// tracker that cannot be asked refuses too, saying so, since nothing is
// sent that Go did not check. Check reads but never writes, and records
// nothing.
func (guard *Guard) Check(ctx context.Context, change domain.TrackerChange, granted []domain.TrackerKind) (Checked, error) {
	change = change.Normalized()
	checked := Checked{Change: change}
	if problem := change.Problem(); problem != "" {
		return checked, domain.TrackerRefusal{Reason: problem}
	}
	if !slices.Contains(granted, change.Kind) {
		may := "no change at all"
		if len(granted) > 0 {
			may = domain.JoinTrackerKinds(granted, ", ") + " only"
		}
		return checked, domain.TrackerRefusal{Reason: fmt.Sprintf("this step may not ask for %s changes; it may ask for %s", change.Kind, may)}
	}
	if err := guard.allow(forKind(change.Kind)); err != nil {
		return checked, err
	}
	named := change.Issue
	if change.Kind == domain.TrackerCreate {
		named = change.Parent
	}
	if named != "" {
		issue, err := guard.checkIssue(ctx, named)
		if err != nil {
			return checked, err
		}
		checked.Issue = issue.ID
	}
	switch change.Kind {
	case domain.TrackerStatus:
		status, err := guard.checkStatus(ctx, checked.Issue, change.Status)
		if err != nil {
			return checked, err
		}
		checked.Change.Status = status
	case domain.TrackerEdit:
		if err := guard.tracker.CheckEdit(change.Fields()); err != nil {
			return checked, refusal(err, "check the edit")
		}
	case domain.TrackerCreate:
		if guard.settings.Tracker != branching.TrackerLinear || checked.Issue != "" {
			break
		}
		// A Linear issue lives in a team, and the agent named no parent to
		// take one from, so it goes in the story's.
		if guard.story == "" {
			return checked, domain.TrackerRefusal{Reason: "a new Linear issue goes in a team, and the run has no story to take one from; name a parent for it"}
		}
		story, err := guard.checkIssue(ctx, domain.StoryIssue)
		if err != nil {
			return checked, err
		}
		if story.Team == "" {
			return checked, domain.TrackerRefusal{Reason: fmt.Sprintf("Linear did not say which team %s is in, so a new issue has no team to go in; name a parent for it", story.ID)}
		}
		checked.Team = story.Team
	}
	return checked, nil
}

// checkIssue reads the issue a change names, the run's story for "story",
// once per check pass.
func (guard *Guard) checkIssue(ctx context.Context, named string) (domain.TrackerIssue, error) {
	id := named
	if strings.EqualFold(named, domain.StoryIssue) {
		if guard.story == "" {
			return domain.TrackerIssue{}, domain.TrackerRefusal{Reason: "the run has no story in the repository's tracker, so \"story\" names no issue; name the issue by its id"}
		}
		id = guard.story
	}
	guard.readMu.Lock()
	defer guard.readMu.Unlock()
	if guard.reads == nil {
		guard.reads = make(map[string]readResult)
	}
	if read, ok := guard.reads[id]; ok {
		return read.issue, read.err
	}
	issue, err := guard.tracker.Story(ctx, id)
	if err != nil {
		err = refusal(err, "read "+id)
	} else if issue.ID == "" {
		issue.ID = id
	}
	guard.reads[id] = readResult{issue: issue, err: err}
	return issue, err
}

// checkStatus finds the status a change names among those the tracker can
// move the issue to, ignoring case and preferring an exact match, and
// returns it as the tracker names it.
func (guard *Guard) checkStatus(ctx context.Context, issue, status string) (string, error) {
	statuses, err := guard.tracker.Statuses(ctx, issue)
	if err != nil {
		return "", refusal(err, "read the statuses of "+issue)
	}
	found := ""
	for _, candidate := range statuses {
		if strings.EqualFold(strings.TrimSpace(candidate), status) && (found == "" || candidate == status) {
			found = candidate
		}
	}
	if found == "" {
		return "", domain.TrackerRefusal{Reason: fmt.Sprintf("%s cannot be moved to %q; its statuses are %s", issue, status, strings.Join(statuses, ", "))}
	}
	return found, nil
}

// refusal keeps a tracker's own refusal, and makes any other error one that
// says the change could not be checked.
func refusal(err error, doing string) error {
	var refused domain.TrackerRefusal
	if errors.As(err, &refused) {
		return refused
	}
	return domain.TrackerRefusal{Reason: fmt.Sprintf("Inkuna could not %s to check the change, so it is not kept: %v", doing, err)}
}

// Apply makes a change Check accepted, as the kind it is, and returns the
// URL it made: a comment's, or a new issue's. marker identifies the change
// across retries, so a comment or an issue is made once however often it is
// retried. Like every write, it is recorded.
func (guard *Guard) Apply(ctx context.Context, checked Checked, marker string) (string, error) {
	change := checked.Change
	switch change.Kind {
	case domain.TrackerStatus:
		return "", guard.SetStatus(ctx, checked.Issue, change.Status)
	case domain.TrackerComment:
		return guard.Comment(ctx, checked.Issue, change.Body, marker)
	case domain.TrackerEdit:
		return "", guard.Edit(ctx, checked.Issue, change.Fields())
	case domain.TrackerCreate:
		draft := domain.IssueDraft{Title: deref(change.Title), Description: deref(change.Description), Labels: change.AddLabels, Parent: checked.Issue, Team: checked.Team, Marker: marker}
		created, err := guard.Create(ctx, draft)
		return created.URL, err
	}
	return "", fmt.Errorf("unknown tracker change kind %q", change.Kind)
}

// DescribeChange says what a checked change would do, as the run shows it:
// the same words its write is recorded with.
func DescribeChange(checked Checked) string {
	change := checked.Change
	switch change.Kind {
	case domain.TrackerStatus:
		return "status → " + change.Status
	case domain.TrackerComment:
		return "comment: " + excerpt(change.Body)
	case domain.TrackerEdit:
		return DescribeFields(change.Fields())
	case domain.TrackerCreate:
		if checked.Issue != "" {
			return "new sub-issue of " + checked.Issue + ": " + deref(change.Title)
		}
		return "new issue: " + deref(change.Title)
	}
	return string(change.Kind)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
