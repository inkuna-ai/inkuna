package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// ProjectStatus names the GitHub Project (v2) whose single-select field
// holds a GitHub issue's status.
type ProjectStatus struct {
	Owner  string
	Number int
	// Field is the single-select field's name.
	Field string
}

// Tracker writes to a repository's GitHub issues through gh, with gh's own
// credential. It is the GitHub adapter of domain.Tracker; which writes it
// may make is a tracker.Guard's decision, not its own
// (docs/mcp-and-trackers.md).
type Tracker struct {
	runner Runner
	// repository is HOST/OWNER/NAME, as gh's --repo takes it.
	repository, host, path string
	// login is gh's signed-in account. A marked comment or issue counts as
	// one Inkuna made only when this account wrote it, so a marker someone
	// else copied never gets their comment edited or their issue reused.
	login   string
	project *ProjectStatus

	metaMu sync.Mutex
	meta   *projectMeta
}

var _ domain.Tracker = (*Tracker)(nil)

// projectMeta is what a status write needs of the project, read once.
type projectMeta struct {
	id, title, fieldID string
	options            []projectOption
}

type projectOption struct{ ID, Name string }

// NewTracker returns the GitHub adapter for a repository, named
// HOST/OWNER/NAME or OWNER/NAME on github.com, writing as login, gh's
// signed-in account. project may be nil; a status write is then refused.
func NewTracker(runner Runner, repository, login string, project *ProjectStatus) (*Tracker, error) {
	if runner == nil {
		return nil, errors.New("github CLI runner is required")
	}
	if strings.TrimSpace(login) == "" {
		return nil, errors.New("gh's signed-in account is required to tell the comments and issues Inkuna made from others'")
	}
	parts := strings.Split(strings.Trim(strings.TrimSpace(repository), "/"), "/")
	if len(parts) == 2 {
		parts = append([]string{DefaultHost}, parts...)
	}
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("github repository %q must be OWNER/NAME or HOST/OWNER/NAME", repository)
	}
	if project != nil && strings.TrimSpace(project.Field) == "" {
		copied := *project
		copied.Field = "Status"
		project = &copied
	}
	return &Tracker{runner: runner, repository: strings.Join(parts, "/"), host: parts[0], path: parts[1] + "/" + parts[2], login: strings.TrimSpace(login), project: project}, nil
}

// issueJSON is gh issue view's answer.
type issueJSON struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	URL         string `json:"url"`
	State       string `json:"state"`
	StateReason string `json:"stateReason"`
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	Parent *struct {
		Number int `json:"number"`
	} `json:"parent"`
	ProjectItems []struct {
		Title  string `json:"title"`
		Status *struct {
			Name string `json:"name"`
		} `json:"status"`
	} `json:"projectItems"`
}

// issueFields are the fields Story reads. gh before 2.94 has no parent
// field and refuses the whole list for it, so it is asked for without.
const (
	issueFields       = "number,title,body,url,state,stateReason,labels,assignees,milestone,projectItems"
	issueParentFields = issueFields + ",parent"
)

func (adapter *Tracker) view(ctx context.Context, id string) (issueJSON, error) {
	number, err := adapter.number(id)
	if err != nil {
		return issueJSON{}, err
	}
	output, err := adapter.gh(ctx, "issue", "view", number, "--repo", adapter.repository, "--json", issueParentFields)
	if err != nil && strings.Contains(err.Error(), "Unknown JSON field") {
		output, err = adapter.gh(ctx, "issue", "view", number, "--repo", adapter.repository, "--json", issueFields)
	}
	if err != nil {
		return issueJSON{}, err
	}
	var issue issueJSON
	if err := json.Unmarshal(output, &issue); err != nil {
		return issueJSON{}, fmt.Errorf("decode issue %s: %w", number, err)
	}
	return issue, nil
}

// Story reads an issue. Its status is the option the project's field has
// for it, which gh reports for a field named Status; for another field, or
// an issue not on the project, the status is empty.
func (adapter *Tracker) Story(ctx context.Context, id string) (domain.TrackerIssue, error) {
	source, err := adapter.view(ctx, id)
	if err != nil {
		return domain.TrackerIssue{}, err
	}
	issue := domain.TrackerIssue{
		ID: strconv.Itoa(source.Number), Title: source.Title, Description: source.Body, URL: source.URL,
		Done: strings.EqualFold(source.State, "closed") && strings.EqualFold(source.StateReason, "completed"),
	}
	for _, label := range source.Labels {
		issue.Labels = append(issue.Labels, label.Name)
	}
	for _, assignee := range source.Assignees {
		issue.Assignees = append(issue.Assignees, assignee.Login)
	}
	if source.Milestone != nil {
		issue.Milestone = source.Milestone.Title
	}
	if source.Parent != nil {
		issue.Parent = strconv.Itoa(source.Parent.Number)
	}
	issue.Status = adapter.statusOf(ctx, source)
	return issue, nil
}

// statusOf is the issue's option of the project's status field, empty when
// it is not on the project or the field is not the one gh reports. gh
// reports an issue's project items with their Status field only, so a
// project whose status is another field reads as no status.
func (adapter *Tracker) statusOf(ctx context.Context, source issueJSON) string {
	if adapter.project == nil || !strings.EqualFold(adapter.project.Field, "Status") {
		return ""
	}
	meta, err := adapter.projectMeta(ctx)
	if err != nil {
		return ""
	}
	for _, item := range source.ProjectItems {
		if item.Title == meta.title && item.Status != nil {
			return item.Status.Name
		}
	}
	return ""
}

// statusProject is the project whose field holds an issue's status, or the
// refusal that says why an issue's status cannot be set.
func (adapter *Tracker) statusProject(ctx context.Context) (projectMeta, error) {
	if adapter.project == nil {
		return projectMeta{}, domain.TrackerRefusal{Reason: "a GitHub issue's status is the Status field of a GitHub Project, and tracker.yaml names none under github.project"}
	}
	// gh project has no --hostname: it reaches github.com, so a project
	// of another host's repository would be looked for there.
	if !strings.EqualFold(adapter.host, DefaultHost) {
		return projectMeta{}, domain.TrackerRefusal{Reason: fmt.Sprintf("gh sets a Project's fields on %s only, so the status of an issue on %s cannot be set", DefaultHost, adapter.host)}
	}
	return adapter.projectMeta(ctx)
}

// Statuses lists the options of the project's status field, which every
// issue of the repository shares.
func (adapter *Tracker) Statuses(ctx context.Context, _ string) ([]string, error) {
	meta, err := adapter.statusProject(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(meta.options))
	for _, option := range meta.options {
		names = append(names, option.Name)
	}
	return names, nil
}

// SetStatus sets the issue's option of the project's status field, adding
// the issue to the project first when it is not on it.
func (adapter *Tracker) SetStatus(ctx context.Context, id, status string) error {
	meta, err := adapter.statusProject(ctx)
	if err != nil {
		return err
	}
	var option *projectOption
	names := make([]string, 0, len(meta.options))
	for index, candidate := range meta.options {
		names = append(names, candidate.Name)
		if strings.EqualFold(strings.TrimSpace(candidate.Name), strings.TrimSpace(status)) && (option == nil || candidate.Name == status) {
			option = &meta.options[index]
		}
	}
	if option == nil {
		return domain.TrackerRefusal{Reason: fmt.Sprintf("the %s field of project %s/%d has no option %q; its options are %s", adapter.project.Field, adapter.project.Owner, adapter.project.Number, status, strings.Join(names, ", "))}
	}
	number, err := adapter.number(id)
	if err != nil {
		return err
	}
	url := "https://" + adapter.host + "/" + adapter.path + "/issues/" + number
	output, err := adapter.gh(ctx, "project", "item-add", strconv.Itoa(adapter.project.Number), "--owner", adapter.project.Owner, "--url", url, "--format", "json")
	if err != nil {
		return err
	}
	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(output, &item); err != nil || item.ID == "" {
		return fmt.Errorf("gh project item-add did not report the item for %s", url)
	}
	_, err = adapter.gh(ctx, "project", "item-edit", "--id", item.ID, "--project-id", meta.id, "--field-id", meta.fieldID, "--single-select-option-id", option.ID)
	return err
}

// Comment comments on an issue and returns the comment's URL. With a
// marker the body ends with it, and a comment that already has it is
// edited instead of adding another.
func (adapter *Tracker) Comment(ctx context.Context, id, body, marker string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", domain.TrackerRefusal{Reason: "a comment needs a body"}
	}
	number, err := adapter.number(id)
	if err != nil {
		return "", err
	}
	if marker == "" {
		output, err := adapter.gh(ctx, "issue", "comment", number, "--repo", adapter.repository, "--body", body)
		if err != nil {
			return "", err
		}
		return lastLine(output), nil
	}
	if !strings.Contains(body, marker) {
		body = strings.TrimRight(body, "\n") + "\n\n" + marker
	}
	output, err := adapter.gh(ctx, "api", "--hostname", adapter.host, "--paginate", "--slurp", "repos/"+adapter.path+"/issues/"+number+"/comments")
	if err != nil {
		return "", err
	}
	var pages [][]struct {
		ID      int64  `json:"id"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		User    author `json:"user"`
	}
	if err := json.Unmarshal(output, &pages); err != nil {
		return "", fmt.Errorf("decode the comments of issue %s: %w", number, err)
	}
	for _, page := range pages {
		for _, comment := range page {
			if !strings.Contains(comment.Body, marker) || !adapter.wrote(comment.User) {
				continue
			}
			edited, err := adapter.gh(ctx, "api", "--hostname", adapter.host, "--method", "PATCH", "repos/"+adapter.path+"/issues/comments/"+strconv.FormatInt(comment.ID, 10), "-f", "body="+body)
			if err != nil {
				return "", err
			}
			var result struct {
				HTMLURL string `json:"html_url"`
			}
			if err := json.Unmarshal(edited, &result); err != nil || result.HTMLURL == "" {
				return comment.HTMLURL, nil
			}
			return result.HTMLURL, nil
		}
	}
	output, err = adapter.gh(ctx, "issue", "comment", number, "--repo", adapter.repository, "--body", body)
	if err != nil {
		return "", err
	}
	return lastLine(output), nil
}

// CheckEdit refuses an estimate, a priority, and a cycle, which GitHub
// issues do not have, an empty title, and an assignee without a login.
func (adapter *Tracker) CheckEdit(fields domain.IssueFields) error {
	unsupported := make([]string, 0, 3)
	if fields.Estimate != nil {
		unsupported = append(unsupported, "estimate")
	}
	if fields.Priority != nil {
		unsupported = append(unsupported, "priority")
	}
	if fields.Cycle != nil {
		unsupported = append(unsupported, "cycle")
	}
	switch {
	case len(unsupported) > 0:
		return domain.TrackerRefusal{Reason: "GitHub issues have no " + orList(unsupported)}
	case fields.Title != nil && strings.TrimSpace(*fields.Title) == "":
		return domain.TrackerRefusal{Reason: "a title cannot be empty"}
	case fields.Assignee != nil && strings.TrimSpace(*fields.Assignee) == "":
		return domain.TrackerRefusal{Reason: "name the assignee by their GitHub login"}
	}
	return nil
}

// Edit changes an issue's title, body, labels, assignees, or milestone.
// GitHub issues have no estimate, priority, or cycle, so those are refused.
func (adapter *Tracker) Edit(ctx context.Context, id string, fields domain.IssueFields) error {
	if err := adapter.CheckEdit(fields); err != nil {
		return err
	}
	number, err := adapter.number(id)
	if err != nil {
		return err
	}
	args := []string{"issue", "edit", number, "--repo", adapter.repository}
	if fields.Title != nil {
		args = append(args, "--title", *fields.Title)
	}
	if fields.Description != nil {
		args = append(args, "--body", *fields.Description)
	}
	for _, label := range fields.AddLabels {
		args = append(args, "--add-label", label)
	}
	for _, label := range fields.RemoveLabels {
		args = append(args, "--remove-label", label)
	}
	if fields.Assignee != nil {
		assignee := strings.TrimPrefix(strings.TrimSpace(*fields.Assignee), "@")
		if strings.EqualFold(assignee, "me") {
			assignee = "@me"
		}
		args = append(args, "--add-assignee", assignee)
	}
	if fields.Milestone != nil {
		if strings.TrimSpace(*fields.Milestone) == "" {
			args = append(args, "--remove-milestone")
		} else {
			args = append(args, "--milestone", *fields.Milestone)
		}
	}
	if len(args) == 5 {
		return nil
	}
	_, err = adapter.gh(ctx, args...)
	return err
}

// Create makes an issue, or a sub-issue of the draft's parent. With a
// marker the body ends with it, and an issue that already has it is
// returned instead of making another.
func (adapter *Tracker) Create(ctx context.Context, draft domain.IssueDraft) (domain.TrackerIssue, error) {
	body := draft.Description
	if draft.Marker != "" {
		existing, found, err := adapter.findMarked(ctx, draft.Marker)
		if err != nil || found {
			return existing, err
		}
		body = strings.TrimRight(body, "\n") + "\n\n" + draft.Marker
	}
	args := []string{"issue", "create", "--repo", adapter.repository, "--title", draft.Title, "--body", body}
	for _, label := range draft.Labels {
		args = append(args, "--label", label)
	}
	parent := ""
	if draft.Parent != "" {
		number, err := adapter.number(draft.Parent)
		if err != nil {
			return domain.TrackerIssue{}, err
		}
		parent = number
		args = append(args, "--parent", number)
	}
	output, err := adapter.gh(ctx, args...)
	if err != nil {
		return domain.TrackerIssue{}, err
	}
	url := lastLine(output)
	number, err := issueNumber(url)
	if err != nil {
		return domain.TrackerIssue{}, err
	}
	return domain.TrackerIssue{ID: strconv.Itoa(number), Title: draft.Title, Description: body, URL: url, Labels: draft.Labels, Parent: parent}, nil
}

// Attach is refused: a GitHub issue has no attachments, and a pull request
// that closes it is linked to it already.
func (adapter *Tracker) Attach(context.Context, string, string, string) (bool, error) {
	return false, domain.TrackerRefusal{Reason: "GitHub issues have no attachments; a pull request that closes the issue links to it on its own"}
}

// Advance sets the issue's option of the project's status field, unless it
// has it already. Done also closes an open issue as completed, which a pull
// request that closes it has usually done on merging. An issue closed as
// completed is done, and is left alone whichever stage is asked for; one
// closed otherwise is never reopened.
func (adapter *Tracker) Advance(ctx context.Context, id string, stage domain.StoryStage, status string) (domain.StageChange, error) {
	if stage != domain.StageStarted && stage != domain.StageDone {
		return domain.StageChange{}, fmt.Errorf("unknown stage %q", stage)
	}
	source, err := adapter.view(ctx, id)
	if err != nil {
		return domain.StageChange{}, err
	}
	number := strconv.Itoa(source.Number)
	current := adapter.statusOf(ctx, source)
	open := strings.EqualFold(source.State, "open")
	if stage == domain.StageStarted && !open && strings.EqualFold(source.StateReason, "completed") {
		return domain.StageChange{Status: current, Note: "#" + number + " is closed as completed already"}, nil
	}
	setStatus := !strings.EqualFold(strings.TrimSpace(current), strings.TrimSpace(status))
	closeIssue := stage == domain.StageDone && open
	if !setStatus && !closeIssue {
		note := "#" + number + " is " + current + " already"
		if stage == domain.StageDone {
			note += " and closed"
		}
		return domain.StageChange{Status: current, Note: note}, nil
	}
	if setStatus {
		if err := adapter.SetStatus(ctx, number, status); err != nil {
			return domain.StageChange{}, err
		}
		current = status
	}
	change := domain.StageChange{Status: current, Changed: true}
	if closeIssue {
		if _, err := adapter.gh(ctx, "issue", "close", number, "--repo", adapter.repository, "--reason", "completed"); err != nil {
			return domain.StageChange{}, err
		}
		change.Note = "and closed #" + number + " as completed"
	}
	return change, nil
}

// findMarked finds an issue whose body has the marker. The repository's
// newest issues come first, from the REST API, which is always current: a
// retry soon after a create whose answer was lost would not find the issue
// through search, whose index lags. Search covers older ones.
func (adapter *Tracker) findMarked(ctx context.Context, marker string) (domain.TrackerIssue, bool, error) {
	recent, err := adapter.gh(ctx, "api", "--hostname", adapter.host, "repos/"+adapter.path+"/issues?state=all&sort=created&direction=desc&per_page=100")
	if err != nil {
		return domain.TrackerIssue{}, false, err
	}
	var newest []struct {
		Number      int             `json:"number"`
		Title       string          `json:"title"`
		Body        string          `json:"body"`
		HTMLURL     string          `json:"html_url"`
		PullRequest json.RawMessage `json:"pull_request"`
		User        author          `json:"user"`
	}
	if err := json.Unmarshal(recent, &newest); err != nil {
		return domain.TrackerIssue{}, false, fmt.Errorf("decode the newest issues: %w", err)
	}
	for _, issue := range newest {
		if issue.PullRequest == nil && strings.Contains(issue.Body, marker) && adapter.wrote(issue.User) {
			return domain.TrackerIssue{ID: strconv.Itoa(issue.Number), Title: issue.Title, Description: issue.Body, URL: issue.HTMLURL}, true, nil
		}
	}
	output, err := adapter.gh(ctx, "issue", "list", "--repo", adapter.repository, "--state", "all", "--search", marker+" in:body", "--json", "number,title,body,url,author", "--limit", "5")
	if err != nil {
		return domain.TrackerIssue{}, false, err
	}
	var issues []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"url"`
		Author author `json:"author"`
	}
	if err := json.Unmarshal(output, &issues); err != nil {
		return domain.TrackerIssue{}, false, fmt.Errorf("decode issue search: %w", err)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Body, marker) && adapter.wrote(issue.Author) {
			return domain.TrackerIssue{ID: strconv.Itoa(issue.Number), Title: issue.Title, Description: issue.Body, URL: issue.URL}, true, nil
		}
	}
	return domain.TrackerIssue{}, false, nil
}

// author is who wrote a comment or an issue, as both the REST API (user)
// and gh's JSON (author) name it.
type author struct {
	Login string `json:"login"`
}

// wrote reports whether gh's own account wrote it.
func (adapter *Tracker) wrote(by author) bool {
	return strings.EqualFold(by.Login, adapter.login)
}

// projectMeta reads the project's id, title, and status field once.
func (adapter *Tracker) projectMeta(ctx context.Context) (projectMeta, error) {
	adapter.metaMu.Lock()
	defer adapter.metaMu.Unlock()
	if adapter.meta != nil {
		return *adapter.meta, nil
	}
	project := adapter.project
	number := strconv.Itoa(project.Number)
	output, err := adapter.gh(ctx, "project", "view", number, "--owner", project.Owner, "--format", "json")
	if err != nil {
		return projectMeta{}, err
	}
	var view struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(output, &view); err != nil || view.ID == "" {
		return projectMeta{}, fmt.Errorf("gh project view did not report project %s/%d", project.Owner, project.Number)
	}
	output, err = adapter.gh(ctx, "project", "field-list", number, "--owner", project.Owner, "--format", "json", "--limit", "100")
	if err != nil {
		return projectMeta{}, err
	}
	var fields struct {
		Fields []struct {
			ID      string          `json:"id"`
			Name    string          `json:"name"`
			Type    string          `json:"type"`
			Options []projectOption `json:"options"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(output, &fields); err != nil {
		return projectMeta{}, fmt.Errorf("decode the fields of project %s/%d: %w", project.Owner, project.Number, err)
	}
	for _, field := range fields.Fields {
		if !strings.EqualFold(field.Name, project.Field) {
			continue
		}
		if field.Type != "ProjectV2SingleSelectField" {
			return projectMeta{}, domain.TrackerRefusal{Reason: fmt.Sprintf("the %s field of project %s/%d is not a single-select field", field.Name, project.Owner, project.Number)}
		}
		adapter.meta = &projectMeta{id: view.ID, title: view.Title, fieldID: field.ID, options: field.Options}
		return *adapter.meta, nil
	}
	return projectMeta{}, domain.TrackerRefusal{Reason: fmt.Sprintf("project %s/%d has no field named %s", project.Owner, project.Number, project.Field)}
}

// gh runs gh, with what it printed in the error when it fails, since that is
// GitHub's reason.
func (adapter *Tracker) gh(ctx context.Context, args ...string) ([]byte, error) {
	output, err := adapter.runner.Run(ctx, args...)
	if err != nil {
		detail := string(bytes.TrimSpace(output))
		if detail == "" {
			return nil, fmt.Errorf("gh %s %s: %w", args[0], args[1], err)
		}
		return nil, fmt.Errorf("gh %s %s: %w: %s", args[0], args[1], err, detail)
	}
	return output, nil
}

// number accepts what a run names a GitHub story by: 12, #12, or the
// issue's URL. A URL must be one of this repository's issues; another's
// number would name a different issue here.
func (adapter *Tracker) number(id string) (string, error) {
	id = strings.TrimSpace(id)
	if number, err := strconv.Atoi(strings.TrimPrefix(id, "#")); err == nil && number > 0 {
		return strconv.Itoa(number), nil
	}
	if parsed, err := neturl.Parse(id); err == nil && parsed.Host != "" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) == 4 && parts[2] == "issues" {
			number, err := strconv.Atoi(parts[3])
			if err == nil && number > 0 && strings.EqualFold(parsed.Host+"/"+parts[0]+"/"+parts[1], adapter.repository) {
				return strconv.Itoa(number), nil
			}
		}
		return "", domain.TrackerRefusal{Reason: fmt.Sprintf("%s is not an issue of %s", id, adapter.repository)}
	}
	return "", domain.TrackerRefusal{Reason: fmt.Sprintf("%q is not a GitHub issue number such as 12", id)}
}

// orList joins names as "a", "a or b", or "a, b, or c".
func orList(names []string) string {
	if len(names) <= 2 {
		return strings.Join(names, " or ")
	}
	return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
}

func lastLine(output []byte) string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
