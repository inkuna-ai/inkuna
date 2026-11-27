package linear

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// Tracker writes to Linear with the project's key. It is the Linear adapter
// of domain.Tracker; which writes it may make is a tracker.Guard's decision,
// not its own (docs/mcp-and-trackers.md).
type Tracker struct{ client *Client }

var _ domain.Tracker = (*Tracker)(nil)

// NewTracker returns the Linear adapter for a client holding the key.
func NewTracker(client *Client) (*Tracker, error) {
	if client == nil {
		return nil, errors.New("a Linear client is required")
	}
	return &Tracker{client: client}, nil
}

// storyIssue is what the adapter reads of an issue to describe or change it.
type storyIssue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	State       State  `json:"state"`
	Team        struct {
		ID          string `json:"id"`
		Key         string `json:"key"`
		ActiveCycle *cycle `json:"activeCycle"`
	} `json:"team"`
	Cycle            *cycle `json:"cycle"`
	ProjectMilestone *Named `json:"projectMilestone"`
	Parent           *struct {
		Identifier string `json:"identifier"`
	} `json:"parent"`
	Labels   nodes[Named] `json:"labels"`
	Assignee *struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
	} `json:"assignee"`
	Attachments nodes[struct {
		URL string `json:"url"`
	}] `json:"attachments"`
}

type cycle struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Name   string `json:"name"`
}

func (value cycle) label() string {
	if strings.TrimSpace(value.Name) != "" {
		return value.Name
	}
	return "Cycle " + strconv.Itoa(value.Number)
}

type nodes[T any] struct {
	Nodes []T `json:"nodes"`
}

const storyFields = `id identifier title description url
    state { id name type }
    team { id key activeCycle { id number name } }
    cycle { id number name }
    projectMilestone { id name }
    parent { identifier }
    labels(first: 100) { nodes { id name } }
    assignee { name displayName email }
    attachments(first: 100) { nodes { url } }`

// read fetches an issue by its key or id.
func (adapter *Tracker) read(ctx context.Context, id string) (storyIssue, error) {
	reference, err := issueReference(id)
	if err != nil {
		return storyIssue{}, err
	}
	var data struct {
		Issue *storyIssue `json:"issue"`
	}
	if err := adapter.client.graphql(ctx, `query InkunaStory($id: String!) { issue(id: $id) { `+storyFields+` } }`, map[string]any{"id": reference}, &data); err != nil {
		return storyIssue{}, missing(err, reference)
	}
	if data.Issue == nil {
		return storyIssue{}, domain.TrackerRefusal{Reason: fmt.Sprintf("Linear issue %s was not found or the key cannot see it", reference)}
	}
	return *data.Issue, nil
}

// Story reads an issue.
func (adapter *Tracker) Story(ctx context.Context, id string) (domain.TrackerIssue, error) {
	source, err := adapter.read(ctx, id)
	if err != nil {
		return domain.TrackerIssue{}, err
	}
	issue := domain.TrackerIssue{
		ID: source.Identifier, Title: source.Title, Description: source.Description, URL: source.URL,
		Status: source.State.Name, Done: source.State.Type == "completed", Team: source.Team.Key,
	}
	if source.Cycle != nil {
		issue.Cycle = source.Cycle.label()
		issue.CurrentCycle = source.Team.ActiveCycle != nil && source.Team.ActiveCycle.ID == source.Cycle.ID
	}
	if source.ProjectMilestone != nil {
		issue.Milestone = source.ProjectMilestone.Name
	}
	if source.Parent != nil {
		issue.Parent = source.Parent.Identifier
	}
	for _, label := range source.Labels.Nodes {
		issue.Labels = append(issue.Labels, label.Name)
	}
	if source.Assignee != nil {
		issue.Assignees = []string{firstNonEmpty(source.Assignee.DisplayName, source.Assignee.Name, source.Assignee.Email)}
	}
	for _, attachment := range source.Attachments.Nodes {
		issue.Links = append(issue.Links, attachment.URL)
	}
	return issue, nil
}

// issueStates is an issue's state and its team's states, which a status
// write chooses from.
type issueStates struct {
	ID    string `json:"id"`
	State State  `json:"state"`
	Team  struct {
		Key    string           `json:"key"`
		States nodes[teamState] `json:"states"`
	} `json:"team"`
}

// teamState is a workflow state with its place in the team's workflow.
type teamState struct {
	State
	Position float64 `json:"position"`
}

// states reads an issue's state and its team's states.
func (adapter *Tracker) states(ctx context.Context, id string) (issueStates, string, error) {
	reference, err := issueReference(id)
	if err != nil {
		return issueStates{}, "", err
	}
	var data struct {
		Issue *issueStates `json:"issue"`
	}
	const query = `query InkunaStates($id: String!) { issue(id: $id) { id state { id name type } team { key states(first: 100) { nodes { id name type position } } } } }`
	if err := adapter.client.graphql(ctx, query, map[string]any{"id": reference}, &data); err != nil {
		return issueStates{}, "", missing(err, reference)
	}
	if data.Issue == nil {
		return issueStates{}, "", domain.TrackerRefusal{Reason: fmt.Sprintf("Linear issue %s was not found or the key cannot see it", reference)}
	}
	return *data.Issue, reference, nil
}

// named finds the team's state of that name, ignoring case, preferring an
// exact match.
func (issue issueStates) named(status string) *teamState {
	var found *teamState
	for index, state := range issue.Team.States.Nodes {
		if strings.EqualFold(strings.TrimSpace(state.Name), strings.TrimSpace(status)) && (found == nil || state.Name == status) {
			found = &issue.Team.States.Nodes[index]
		}
	}
	return found
}

// first finds the team's first state of a type, by its place in the
// workflow.
func (issue issueStates) first(stateType string) *teamState {
	var found *teamState
	for index, state := range issue.Team.States.Nodes {
		if state.Type == stateType && (found == nil || state.Position < found.Position) {
			found = &issue.Team.States.Nodes[index]
		}
	}
	return found
}

func (issue issueStates) names() string {
	names := make([]string, 0, len(issue.Team.States.Nodes))
	for _, state := range issue.Team.States.Nodes {
		names = append(names, state.Name)
	}
	return strings.Join(names, ", ")
}

// Statuses lists the states of the issue's team it can be moved to: every
// one but a canceled state, which SetStatus refuses.
func (adapter *Tracker) Statuses(ctx context.Context, id string) ([]string, error) {
	issue, _, err := adapter.states(ctx, id)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(issue.Team.States.Nodes))
	for _, state := range issue.Team.States.Nodes {
		if state.Type != "canceled" {
			names = append(names, state.Name)
		}
	}
	return names, nil
}

// SetStatus moves an issue to its team's state of that name. A canceled
// state closes the issue as not planned, so it is refused.
func (adapter *Tracker) SetStatus(ctx context.Context, id, status string) error {
	issue, reference, err := adapter.states(ctx, id)
	if err != nil {
		return err
	}
	target := issue.named(status)
	switch {
	case target == nil:
		return domain.TrackerRefusal{Reason: fmt.Sprintf("Linear team %s has no state named %q; its states are %s", issue.Team.Key, status, issue.names())}
	case target.Type == "canceled":
		return domain.TrackerRefusal{Reason: fmt.Sprintf("%q is a canceled state, and moving %s there would close it as not planned, which Inkuna never does", target.Name, reference)}
	case target.ID == issue.State.ID:
		return nil
	}
	return adapter.update(ctx, issue.ID, map[string]any{"stateId": target.ID})
}

// stageTypes are the Linear state types each stage falls back to.
var stageTypes = map[domain.StoryStage]string{domain.StageStarted: "started", domain.StageDone: "completed"}

// Advance moves an issue to its team's state of that name, or, when the
// team has none, to its first state of the stage's type: started for In
// Progress, completed for Done. An issue in a completed state is done, and
// is left alone whichever stage is asked for, as is one already in the
// state.
func (adapter *Tracker) Advance(ctx context.Context, id string, stage domain.StoryStage, status string) (domain.StageChange, error) {
	stateType, ok := stageTypes[stage]
	if !ok {
		return domain.StageChange{}, fmt.Errorf("unknown stage %q", stage)
	}
	issue, reference, err := adapter.states(ctx, id)
	if err != nil {
		return domain.StageChange{}, err
	}
	if issue.State.Type == "completed" {
		return domain.StageChange{Status: issue.State.Name, Note: fmt.Sprintf("%s is done already, in %s", reference, issue.State.Name)}, nil
	}
	target, note := issue.named(status), ""
	if target == nil {
		target = issue.first(stateType)
		if target == nil {
			return domain.StageChange{}, domain.TrackerRefusal{Reason: fmt.Sprintf("Linear team %s has no state named %q and no %s state; its states are %s", issue.Team.Key, status, stateType, issue.names())}
		}
		note = fmt.Sprintf("team %s has no state named %q, so its first %s state", issue.Team.Key, status, stateType)
	}
	switch {
	case target.Type == "canceled":
		return domain.StageChange{}, domain.TrackerRefusal{Reason: fmt.Sprintf("%q is a canceled state, and moving %s there would close it as not planned, which Inkuna never does", target.Name, reference)}
	case target.ID == issue.State.ID:
		return domain.StageChange{Status: target.Name, Note: fmt.Sprintf("%s is %s already", reference, target.Name)}, nil
	}
	if err := adapter.update(ctx, issue.ID, map[string]any{"stateId": target.ID}); err != nil {
		return domain.StageChange{}, err
	}
	return domain.StageChange{Status: target.Name, Changed: true, Note: note}, nil
}

// Comment comments on an issue and returns the comment's URL. With a
// marker the comment's id is derived from it, so a retry finds the comment
// it made and edits it, whatever Linear does to the body's markup.
func (adapter *Tracker) Comment(ctx context.Context, id, body, marker string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", domain.TrackerRefusal{Reason: "a comment needs a body"}
	}
	issue, err := adapter.read(ctx, id)
	if err != nil {
		return "", err
	}
	type commentResult struct {
		Success bool `json:"success"`
		Comment *struct {
			URL string `json:"url"`
		} `json:"comment"`
	}
	input := map[string]any{"issueId": issue.ID, "body": body}
	if marker != "" {
		commentID := markerUUID("comment", issue.ID, marker)
		var found struct {
			Issue *struct {
				Comments nodes[struct {
					ID string `json:"id"`
				}] `json:"comments"`
			} `json:"issue"`
		}
		const query = `query InkunaFindComment($issue: String!, $comment: ID!) { issue(id: $issue) { comments(filter: { id: { eq: $comment } }) { nodes { id } } } }`
		if err := adapter.client.graphql(ctx, query, map[string]any{"issue": issue.ID, "comment": commentID}, &found); err != nil {
			return "", err
		}
		if found.Issue != nil && len(found.Issue.Comments.Nodes) > 0 {
			var updated struct {
				CommentUpdate commentResult `json:"commentUpdate"`
			}
			const mutation = `mutation InkunaCommentUpdate($id: String!, $input: CommentUpdateInput!) { commentUpdate(id: $id, input: $input) { success comment { url } } }`
			if err := adapter.client.graphql(ctx, mutation, map[string]any{"id": commentID, "input": map[string]any{"body": body}}, &updated); err != nil {
				return "", err
			}
			if !updated.CommentUpdate.Success || updated.CommentUpdate.Comment == nil {
				return "", errors.New("linear did not update the comment")
			}
			return updated.CommentUpdate.Comment.URL, nil
		}
		input["id"] = commentID
	}
	var created struct {
		CommentCreate commentResult `json:"commentCreate"`
	}
	const mutation = `mutation InkunaComment($input: CommentCreateInput!) { commentCreate(input: $input) { success comment { url } } }`
	if err := adapter.client.graphql(ctx, mutation, map[string]any{"input": input}, &created); err != nil {
		return "", err
	}
	if !created.CommentCreate.Success || created.CommentCreate.Comment == nil {
		return "", errors.New("linear did not create the comment")
	}
	return created.CommentCreate.Comment.URL, nil
}

// CheckEdit refuses a milestone, which belongs to a Linear issue's project,
// a priority outside 0 to 4, a negative estimate, and an empty title.
func (adapter *Tracker) CheckEdit(fields domain.IssueFields) error {
	switch {
	case fields.Milestone != nil:
		return domain.TrackerRefusal{Reason: "a Linear issue's milestone belongs to its project, and Inkuna does not edit projects"}
	case fields.Priority != nil && (*fields.Priority < 0 || *fields.Priority > 4):
		return domain.TrackerRefusal{Reason: fmt.Sprintf("Linear priority %d must be 0 (none), 1 (urgent), 2 (high), 3 (medium), or 4 (low)", *fields.Priority)}
	case fields.Estimate != nil && *fields.Estimate < 0:
		return domain.TrackerRefusal{Reason: "an estimate cannot be negative"}
	case fields.Title != nil && strings.TrimSpace(*fields.Title) == "":
		return domain.TrackerRefusal{Reason: "a title cannot be empty"}
	}
	return nil
}

// Edit changes an issue's title, description, labels, estimate, priority,
// assignee, or cycle. A Linear milestone belongs to the issue's project,
// which Inkuna does not edit, so a milestone is refused.
func (adapter *Tracker) Edit(ctx context.Context, id string, fields domain.IssueFields) error {
	if err := adapter.CheckEdit(fields); err != nil {
		return err
	}
	issue, err := adapter.read(ctx, id)
	if err != nil {
		return err
	}
	input := make(map[string]any)
	if fields.Title != nil {
		input["title"] = *fields.Title
	}
	if fields.Description != nil {
		input["description"] = *fields.Description
	}
	if fields.Estimate != nil {
		input["estimate"] = *fields.Estimate
	}
	if fields.Priority != nil {
		input["priority"] = *fields.Priority
	}
	if len(fields.AddLabels) > 0 {
		ids, err := adapter.labelIDs(ctx, issue.Team.ID, issue.Team.Key, fields.AddLabels)
		if err != nil {
			return err
		}
		input["addedLabelIds"] = ids
	}
	if len(fields.RemoveLabels) > 0 {
		removed := make([]string, 0, len(fields.RemoveLabels))
		for _, name := range fields.RemoveLabels {
			for _, label := range issue.Labels.Nodes {
				if strings.EqualFold(label.Name, strings.TrimSpace(name)) {
					removed = append(removed, label.ID)
				}
			}
		}
		// A label the issue does not have is already removed.
		if len(removed) > 0 {
			input["removedLabelIds"] = removed
		}
	}
	if fields.Assignee != nil {
		assignee, err := adapter.userID(ctx, *fields.Assignee)
		if err != nil {
			return err
		}
		input["assigneeId"] = assignee
	}
	if fields.Cycle != nil {
		cycleID, err := adapter.cycleID(ctx, issue.Team.ID, issue.Team.Key, *fields.Cycle)
		if err != nil {
			return err
		}
		input["cycleId"] = cycleID
	}
	if len(input) == 0 {
		return nil
	}
	return adapter.update(ctx, issue.ID, input)
}

// Create makes an issue in the draft's team, or a sub-issue in its parent's.
// With a marker the issue's id is derived from it, so a retry returns the
// issue it made instead of making another.
func (adapter *Tracker) Create(ctx context.Context, draft domain.IssueDraft) (domain.TrackerIssue, error) {
	input := map[string]any{"title": draft.Title}
	if draft.Description != "" {
		input["description"] = draft.Description
	}
	var teamID, teamKey string
	switch {
	case draft.Parent != "":
		parent, err := adapter.read(ctx, draft.Parent)
		if err != nil {
			return domain.TrackerIssue{}, err
		}
		teamID, teamKey = parent.Team.ID, parent.Team.Key
		input["parentId"] = parent.ID
	case draft.Team != "":
		var data struct {
			Teams nodes[struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			}] `json:"teams"`
		}
		const query = `query InkunaTeam($key: String!) { teams(filter: { key: { eqIgnoreCase: $key } }) { nodes { id key } } }`
		if err := adapter.client.graphql(ctx, query, map[string]any{"key": draft.Team}, &data); err != nil {
			return domain.TrackerIssue{}, err
		}
		if len(data.Teams.Nodes) != 1 {
			return domain.TrackerIssue{}, domain.TrackerRefusal{Reason: fmt.Sprintf("Linear has no team with the key %s that the key can see", draft.Team)}
		}
		teamID, teamKey = data.Teams.Nodes[0].ID, data.Teams.Nodes[0].Key
	default:
		return domain.TrackerIssue{}, domain.TrackerRefusal{Reason: "a new Linear issue needs a team or a parent"}
	}
	input["teamId"] = teamID
	if len(draft.Labels) > 0 {
		ids, err := adapter.labelIDs(ctx, teamID, teamKey, draft.Labels)
		if err != nil {
			return domain.TrackerIssue{}, err
		}
		input["labelIds"] = ids
	}
	if draft.Marker != "" {
		issueID := markerUUID("issue", teamID, draft.Marker)
		var found struct {
			Issues nodes[struct {
				ID string `json:"id"`
			}] `json:"issues"`
		}
		const query = `query InkunaMarked($id: ID!) { issues(filter: { id: { eq: $id } }) { nodes { id } } }`
		if err := adapter.client.graphql(ctx, query, map[string]any{"id": issueID}, &found); err != nil {
			return domain.TrackerIssue{}, err
		}
		if len(found.Issues.Nodes) > 0 {
			return adapter.Story(ctx, issueID)
		}
		input["id"] = issueID
	}
	var created struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   *struct {
				Identifier string `json:"identifier"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	const mutation = `mutation InkunaCreate($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { identifier } } }`
	if err := adapter.client.graphql(ctx, mutation, map[string]any{"input": input}, &created); err != nil {
		return domain.TrackerIssue{}, err
	}
	if !created.IssueCreate.Success || created.IssueCreate.Issue == nil {
		return domain.TrackerIssue{}, errors.New("linear did not create the issue")
	}
	return adapter.Story(ctx, created.IssueCreate.Issue.Identifier)
}

// Attach links a URL to an issue unless it is attached already, as
// Linear's GitHub integration attaches a pull request that names the issue,
// and reports whether it did.
func (adapter *Tracker) Attach(ctx context.Context, id, url, title string) (bool, error) {
	issue, err := adapter.read(ctx, id)
	if err != nil {
		return false, err
	}
	for _, attachment := range issue.Attachments.Nodes {
		if sameURL(attachment.URL, url) {
			return false, nil
		}
	}
	var data struct {
		AttachmentLinkURL struct {
			Success bool `json:"success"`
		} `json:"attachmentLinkURL"`
	}
	variables := map[string]any{"issue": issue.ID, "url": url}
	if strings.TrimSpace(title) != "" {
		variables["title"] = title
	}
	const mutation = `mutation InkunaAttach($issue: String!, $url: String!, $title: String) { attachmentLinkURL(issueId: $issue, url: $url, title: $title) { success } }`
	if err := adapter.client.graphql(ctx, mutation, variables, &data); err != nil {
		return false, err
	}
	if !data.AttachmentLinkURL.Success {
		return false, errors.New("linear did not attach the link")
	}
	return true, nil
}

// sameURL compares two links as the same page: a trailing slash and the
// host's case do not make another.
func sameURL(left, right string) bool {
	normal := func(url string) string {
		url = strings.TrimRight(strings.TrimSpace(url), "/")
		scheme, rest, found := strings.Cut(url, "://")
		if !found {
			return url
		}
		host, path, _ := strings.Cut(rest, "/")
		return strings.ToLower(scheme) + "://" + strings.ToLower(host) + "/" + path
	}
	return normal(left) == normal(right)
}

func (adapter *Tracker) update(ctx context.Context, issueID string, input map[string]any) error {
	var data struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	const mutation = `mutation InkunaUpdate($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success } }`
	if err := adapter.client.graphql(ctx, mutation, map[string]any{"id": issueID, "input": input}, &data); err != nil {
		return err
	}
	if !data.IssueUpdate.Success {
		return errors.New("linear did not update the issue")
	}
	return nil
}

// labelIDs finds labels by name among the team's and the workspace's.
func (adapter *Tracker) labelIDs(ctx context.Context, teamID, teamKey string, names []string) ([]string, error) {
	var data struct {
		Team *struct {
			Labels nodes[Named] `json:"labels"`
		} `json:"team"`
		IssueLabels nodes[Named] `json:"issueLabels"`
	}
	const query = `query InkunaLabels($team: String!) {
  team(id: $team) { labels(first: 250) { nodes { id name } } }
  issueLabels(first: 250, filter: { team: { null: true } }) { nodes { id name } }
}`
	if err := adapter.client.graphql(ctx, query, map[string]any{"team": teamID}, &data); err != nil {
		return nil, err
	}
	known := data.IssueLabels.Nodes
	if data.Team != nil {
		known = append(known, data.Team.Labels.Nodes...)
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		found := ""
		for _, label := range known {
			if strings.EqualFold(label.Name, strings.TrimSpace(name)) {
				found = label.ID
				break
			}
		}
		if found == "" {
			return nil, domain.TrackerRefusal{Reason: fmt.Sprintf("Linear has no label named %q in team %s or the workspace", name, teamKey)}
		}
		ids = append(ids, found)
	}
	return ids, nil
}

// userID finds one active user by email, display name, or name; "me" is the
// key's own user.
func (adapter *Tracker) userID(ctx context.Context, assignee string) (string, error) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return "", domain.TrackerRefusal{Reason: "name the assignee by email, display name, or name"}
	}
	if strings.EqualFold(assignee, "me") {
		viewer, err := adapter.client.Viewer(ctx)
		if err != nil {
			return "", err
		}
		return viewer.ID, nil
	}
	var data struct {
		Users nodes[struct {
			ID     string `json:"id"`
			Active bool   `json:"active"`
		}] `json:"users"`
	}
	const query = `query InkunaUser($value: String!) { users(filter: { or: [{ email: { eqIgnoreCase: $value } }, { displayName: { eqIgnoreCase: $value } }, { name: { eqIgnoreCase: $value } }] }) { nodes { id active } } }`
	if err := adapter.client.graphql(ctx, query, map[string]any{"value": assignee}, &data); err != nil {
		return "", err
	}
	active := make([]string, 0, 1)
	for _, user := range data.Users.Nodes {
		if user.Active {
			active = append(active, user.ID)
		}
	}
	switch len(active) {
	case 1:
		return active[0], nil
	case 0:
		return "", domain.TrackerRefusal{Reason: fmt.Sprintf("Linear has no active user %q", assignee)}
	}
	return "", domain.TrackerRefusal{Reason: fmt.Sprintf("%q names %d Linear users; use their email", assignee, len(active))}
}

// cycleID finds a cycle of the team that has not ended, by number or name;
// "current" is the active one.
func (adapter *Tracker) cycleID(ctx context.Context, teamID, teamKey, value string) (string, error) {
	var data struct {
		Team *struct {
			ActiveCycle *cycle       `json:"activeCycle"`
			Cycles      nodes[cycle] `json:"cycles"`
		} `json:"team"`
	}
	const query = `query InkunaCycles($team: String!) { team(id: $team) { activeCycle { id number name } cycles(first: 50, filter: { isPast: { eq: false } }) { nodes { id number name } } } }`
	if err := adapter.client.graphql(ctx, query, map[string]any{"team": teamID}, &data); err != nil {
		return "", err
	}
	if data.Team == nil {
		return "", domain.TrackerRefusal{Reason: fmt.Sprintf("Linear team %s was not found", teamKey)}
	}
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "current") || strings.EqualFold(value, "active") {
		if data.Team.ActiveCycle == nil {
			return "", domain.TrackerRefusal{Reason: fmt.Sprintf("Linear team %s has no active cycle", teamKey)}
		}
		return data.Team.ActiveCycle.ID, nil
	}
	number := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "cycle"))
	for _, candidate := range data.Team.Cycles.Nodes {
		if strings.EqualFold(candidate.Name, value) || strconv.Itoa(candidate.Number) == number {
			return candidate.ID, nil
		}
	}
	return "", domain.TrackerRefusal{Reason: fmt.Sprintf("Linear team %s has no current or upcoming cycle %q", teamKey, value)}
}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// issueReference accepts what a run names a Linear story by: its key, its
// URL, or Linear's own id.
func issueReference(id string) (string, error) {
	id = strings.TrimSpace(id)
	if reference, err := ParseIssueReference(id); err == nil {
		return reference, nil
	}
	if uuidPattern.MatchString(id) {
		return strings.ToLower(id), nil
	}
	return "", domain.TrackerRefusal{Reason: fmt.Sprintf("%q is not a Linear issue key such as ENG-12", id)}
}

// markerUUID derives a version 4 shaped UUID from a marker, which Linear
// accepts as the id of an issue or comment it creates, so the same marker
// always names the same one.
func markerUUID(kind, scope, marker string) string {
	sum := sha256.Sum256([]byte("inkuna:" + kind + "\x00" + scope + "\x00" + marker))
	sum[6] = sum[6]&0x0f | 0x40
	sum[8] = sum[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// missing turns Linear's answer that an issue does not exist, which is how
// it answers for one the key cannot see as well, into a refusal: asking
// again will not find it.
func missing(err error, reference string) error {
	var failure *apiError
	if errors.As(err, &failure) && notFound(failure.errors) {
		return domain.TrackerRefusal{Reason: fmt.Sprintf("Linear issue %s was not found or the key cannot see it", reference)}
	}
	return err
}

// graphql runs a query or mutation and decodes its data into target. A
// GraphQL error is returned as an error, the tracker's reason in it.
func (client *Client) graphql(ctx context.Context, query string, variables map[string]any, target any) error {
	var response struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphqlError  `json:"errors"`
	}
	if err := client.query(ctx, query, variables, &response); err != nil {
		return err
	}
	if len(response.Errors) > 0 {
		return &apiError{status: http.StatusOK, errors: response.Errors}
	}
	if target == nil || len(response.Data) == 0 || string(response.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(response.Data, target); err != nil {
		return fmt.Errorf("decode Linear response: %w", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
