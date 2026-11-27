package github

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// stubGH answers gh's commands by their first two arguments, and keeps
// every command it was run with.
type stubGH struct {
	t       *testing.T
	answers map[string]func(args []string) (string, error)
	calls   [][]string
}

func (stub *stubGH) Run(_ context.Context, args ...string) ([]byte, error) {
	stub.calls = append(stub.calls, append([]string(nil), args...))
	key := args[0] + " " + args[1]
	if args[0] == "api" {
		key = "api " + args[len(args)-1]
		for index, arg := range args {
			if arg == "--method" {
				key = "api " + args[index+1]
			}
		}
	}
	answer := stub.answers[key]
	if answer == nil {
		stub.t.Errorf("unexpected gh %v", args)
		return nil, errors.New("unexpected")
	}
	output, err := answer(args)
	return []byte(output), err
}

func (stub *stubGH) ran(prefix ...string) [][]string {
	matched := make([][]string, 0)
	for _, call := range stub.calls {
		if len(call) >= len(prefix) && reflect.DeepEqual(call[:len(prefix)], prefix) {
			matched = append(matched, call)
		}
	}
	return matched
}

func reply(output string) func([]string) (string, error) {
	return func([]string) (string, error) { return output, nil }
}

const issueView = `{"number":12,"title":"Fix login","body":"Steps","url":"https://github.com/acme/app/issues/12","state":"OPEN","stateReason":"",
"labels":[{"name":"bug"}],"assignees":[{"login":"octocat"}],"milestone":{"title":"Beta"},"parent":{"number":3},
"projectItems":[{"title":"Roadmap","status":{"optionId":"o1","name":"Backlog"}},{"title":"Other","status":{"name":"Done"}}]}`

const projectView = `{"id":"PVT_1","number":3,"title":"Roadmap"}`

const fieldList = `{"fields":[{"id":"F_title","name":"Title","type":"ProjectV2Field"},
{"id":"F_status","name":"Status","type":"ProjectV2SingleSelectField","options":[{"id":"o1","name":"Backlog"},{"id":"o2","name":"In progress"},{"id":"o3","name":"Done"}]},
{"id":"F_size","name":"Size","type":"ProjectV2Field"}]}`

func newGitHub(t *testing.T, project *ProjectStatus, answers map[string]func([]string) (string, error)) (*stubGH, *Tracker) {
	t.Helper()
	stub := &stubGH{t: t, answers: answers}
	adapter, err := NewTracker(stub, "github.com/acme/app", "octocat", project)
	if err != nil {
		t.Fatal(err)
	}
	return stub, adapter
}

var roadmap = &ProjectStatus{Owner: "acme", Number: 3}

func TestGitHubStoryReadsTheIssueAndItsProjectStatus(t *testing.T) {
	_, adapter := newGitHub(t, roadmap, map[string]func([]string) (string, error){
		"issue view": reply(issueView), "project view": reply(projectView), "project field-list": reply(fieldList),
	})
	issue, err := adapter.Story(context.Background(), "#12")
	if err != nil {
		t.Fatal(err)
	}
	want := domain.TrackerIssue{ID: "12", Title: "Fix login", Description: "Steps", URL: "https://github.com/acme/app/issues/12", Status: "Backlog",
		Milestone: "Beta", Parent: "3", Labels: []string{"bug"}, Assignees: []string{"octocat"}}
	if !reflect.DeepEqual(issue, want) {
		t.Fatalf("issue = %+v\nwant %+v", issue, want)
	}
	closed := strings.Replace(strings.Replace(issueView, `"OPEN"`, `"CLOSED"`, 1), `"stateReason":""`, `"stateReason":"COMPLETED"`, 1)
	_, adapter = newGitHub(t, nil, map[string]func([]string) (string, error){"issue view": reply(closed)})
	if issue, err := adapter.Story(context.Background(), "12"); err != nil || !issue.Done || issue.Status != "" {
		t.Fatalf("closed issue = %+v, %v", issue, err)
	}
}

func TestGitHubSetStatusEditsTheProjectField(t *testing.T) {
	stub, adapter := newGitHub(t, roadmap, map[string]func([]string) (string, error){
		"issue view": reply(issueView), "project view": reply(projectView), "project field-list": reply(fieldList),
		"project item-add": reply(`{"id":"PVTI_12","title":"Fix login"}`), "project item-edit": reply(`{}`),
	})
	if err := adapter.SetStatus(context.Background(), "12", "In Progress"); err != nil {
		t.Fatal(err)
	}
	add := stub.ran("project", "item-add")
	if len(add) != 1 || !reflect.DeepEqual(add[0], []string{"project", "item-add", "3", "--owner", "acme", "--url", "https://github.com/acme/app/issues/12", "--format", "json"}) {
		t.Fatalf("item-add = %v", add)
	}
	edit := stub.ran("project", "item-edit")
	if len(edit) != 1 || !reflect.DeepEqual(edit[0], []string{"project", "item-edit", "--id", "PVTI_12", "--project-id", "PVT_1", "--field-id", "F_status", "--single-select-option-id", "o2"}) {
		t.Fatalf("item-edit = %v", edit)
	}
	// The project is read once for every write.
	if err := adapter.SetStatus(context.Background(), "12", "Done"); err != nil || len(stub.ran("project", "view")) != 1 {
		t.Fatalf("second status: %v, %d project reads", err, len(stub.ran("project", "view")))
	}
}

func TestGitHubSetStatusRefusals(t *testing.T) {
	_, adapter := newGitHub(t, nil, nil)
	var refusal domain.TrackerRefusal
	if err := adapter.SetStatus(context.Background(), "12", "Done"); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "names none under github.project") {
		t.Fatalf("without a project: %v", err)
	}
	stub, adapter := newGitHub(t, roadmap, map[string]func([]string) (string, error){
		"project view": reply(projectView), "project field-list": reply(fieldList),
	})
	if err := adapter.SetStatus(context.Background(), "12", "Blocked"); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, `no option "Blocked"; its options are Backlog, In progress, Done`) {
		t.Fatalf("unknown option: %v", err)
	}
	if len(stub.ran("project", "item-add")) > 0 || len(stub.ran("project", "item-edit")) > 0 {
		t.Fatal("a refused status was sent")
	}
	enterprise, err := NewTracker(&stubGH{t: t}, "ghe.example.com/acme/app", "octocat", roadmap)
	if err != nil {
		t.Fatal(err)
	}
	if err := enterprise.SetStatus(context.Background(), "12", "Done"); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "on github.com only") {
		t.Fatalf("an enterprise issue's status: %v", err)
	}
	_, adapter = newGitHub(t, &ProjectStatus{Owner: "acme", Number: 3, Field: "Size"}, map[string]func([]string) (string, error){
		"project view": reply(projectView), "project field-list": reply(fieldList),
	})
	if err := adapter.SetStatus(context.Background(), "12", "M"); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "not a single-select field") {
		t.Fatalf("a text field: %v", err)
	}
}

func TestGitHubCommentWithAMarkerEditsTheCommentItMade(t *testing.T) {
	marker := "<!-- inkuna-run:r1 -->"
	comments := `[[{"id":1,"body":"Thanks","html_url":"https://github.com/acme/app/issues/12#issuecomment-1"}]]`
	stub, adapter := newGitHub(t, nil, map[string]func([]string) (string, error){
		"api repos/acme/app/issues/12/comments": func([]string) (string, error) { return comments, nil },
		"issue comment": func(args []string) (string, error) {
			comments = `[[{"id":1,"body":"Thanks"},{"id":2,"body":"` + strings.ReplaceAll(args[len(args)-1], "\n", `\n`) + `","html_url":"https://github.com/acme/app/issues/12#issuecomment-2","user":{"login":"Octocat"}}]]`
			return "https://github.com/acme/app/issues/12#issuecomment-2\n", nil
		},
		"api PATCH": reply(`{"html_url":"https://github.com/acme/app/issues/12#issuecomment-2"}`),
	})
	ctx := context.Background()
	url, err := adapter.Comment(ctx, "12", "Completed in 1 round", marker)
	if err != nil || url != "https://github.com/acme/app/issues/12#issuecomment-2" {
		t.Fatalf("Comment = %q, %v", url, err)
	}
	created := stub.ran("issue", "comment")
	if len(created) != 1 || created[0][len(created[0])-1] != "Completed in 1 round\n\n"+marker {
		t.Fatalf("issue comment = %v", created)
	}
	if _, err := adapter.Comment(ctx, "12", "Completed in 2 rounds", marker); err != nil {
		t.Fatal(err)
	}
	patch := stub.ran("api", "--hostname", "github.com", "--method", "PATCH")
	if len(patch) != 1 || !reflect.DeepEqual(patch[0][5:], []string{"repos/acme/app/issues/comments/2", "-f", "body=Completed in 2 rounds\n\n" + marker}) {
		t.Fatalf("the retry did not edit the comment: %v", patch)
	}
	if len(stub.ran("issue", "comment")) != 1 {
		t.Fatal("the retry commented again")
	}
}

// A marker someone else copied into their comment or issue does not make it
// Inkuna's: their comment is not edited, and their issue is not taken for
// the one a retry was looking for.
func TestGitHubMarkersCountOnlyWhatGHsAccountWrote(t *testing.T) {
	marker := "<!-- inkuna-run:r1 -->"
	stub, adapter := newGitHub(t, nil, map[string]func([]string) (string, error){
		"api repos/acme/app/issues/12/comments": reply(`[[{"id":3,"body":"Quoting ` + marker + `","html_url":"https://github.com/acme/app/issues/12#issuecomment-3","user":{"login":"mallory"}}]]`),
		"issue comment":                         reply("https://github.com/acme/app/issues/12#issuecomment-4\n"),
		newestIssues:                            reply(`[{"number":41,"title":"Copy","body":"` + marker + `","html_url":"https://github.com/acme/app/issues/41","user":{"login":"mallory"}}]`),
		"issue list":                            reply(`[{"number":41,"title":"Copy","body":"` + marker + `","url":"https://github.com/acme/app/issues/41","author":{"login":"mallory"}}]`),
		"issue create":                          reply("https://github.com/acme/app/issues/42\n"),
	})
	ctx := context.Background()
	if url, err := adapter.Comment(ctx, "12", "Completed", marker); err != nil || url != "https://github.com/acme/app/issues/12#issuecomment-4" {
		t.Fatalf("Comment = %q, %v", url, err)
	}
	if patched := stub.ran("api", "--hostname", "github.com", "--method", "PATCH"); len(patched) != 0 {
		t.Fatalf("someone else's comment was edited: %v", patched)
	}
	if created, err := adapter.Create(ctx, domain.IssueDraft{Title: "Index care events", Marker: marker}); err != nil || created.ID != "42" {
		t.Fatalf("Create = %+v, %v", created, err)
	}
}

func TestGitHubCommentWithoutAMarker(t *testing.T) {
	stub, adapter := newGitHub(t, nil, map[string]func([]string) (string, error){
		"issue comment": reply("https://github.com/acme/app/issues/12#issuecomment-9\n"),
	})
	if url, err := adapter.Comment(context.Background(), "12", "Looks good", ""); err != nil || url != "https://github.com/acme/app/issues/12#issuecomment-9" {
		t.Fatalf("Comment = %q, %v", url, err)
	}
	if !reflect.DeepEqual(stub.calls[0], []string{"issue", "comment", "12", "--repo", "github.com/acme/app", "--body", "Looks good"}) {
		t.Fatalf("issue comment = %v", stub.calls[0])
	}
}

func TestGitHubEditSetsTheFieldsGitHubHas(t *testing.T) {
	stub, adapter := newGitHub(t, nil, map[string]func([]string) (string, error){"issue edit": reply("https://github.com/acme/app/issues/12\n")})
	title, body, assignee, milestone := "Fix login on Safari", "Repro inside", "me", ""
	err := adapter.Edit(context.Background(), "12", domain.IssueFields{
		Title: &title, Description: &body, AddLabels: []string{"backend"}, RemoveLabels: []string{"triage"}, Assignee: &assignee, Milestone: &milestone,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"issue", "edit", "12", "--repo", "github.com/acme/app", "--title", title, "--body", body, "--add-label", "backend", "--remove-label", "triage", "--add-assignee", "@me", "--remove-milestone"}
	if !reflect.DeepEqual(stub.calls[0], want) {
		t.Fatalf("issue edit = %v\nwant %v", stub.calls[0], want)
	}
}

func TestGitHubEditRefusesWhatGitHubIssuesLack(t *testing.T) {
	stub, adapter := newGitHub(t, nil, nil)
	estimate, priority, cycle := 2, 1, "current"
	var refusal domain.TrackerRefusal
	err := adapter.Edit(context.Background(), "12", domain.IssueFields{Estimate: &estimate, Priority: &priority, Cycle: &cycle})
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "GitHub issues have no estimate, priority, or cycle") {
		t.Fatalf("Edit = %v", err)
	}
	if len(stub.calls) != 0 {
		t.Fatal("a refused edit ran gh")
	}
}

const newestIssues = "api repos/acme/app/issues?state=all&sort=created&direction=desc&per_page=100"

// A retry finds the issue its first try made among the newest issues, which
// are current at once, even while search has not indexed it yet.
func TestGitHubCreateMakesASubIssueOnce(t *testing.T) {
	marker := "<!-- inkuna-change:r1:3 -->"
	newest := `[{"number":39,"title":"A pull request","body":"` + marker + `","html_url":"https://github.com/acme/app/pull/39","pull_request":{}}]`
	stub, adapter := newGitHub(t, nil, map[string]func([]string) (string, error){
		newestIssues: func([]string) (string, error) { return newest, nil },
		"issue list": reply("[]"),
		"issue create": func(args []string) (string, error) {
			newest = `[{"number":40,"title":"Index care events","body":"Found while profiling\n\n` + marker + `","html_url":"https://github.com/acme/app/issues/40","user":{"login":"octocat"}}]`
			return "https://github.com/acme/app/issues/40\n", nil
		},
	})
	draft := domain.IssueDraft{Title: "Index care events", Description: "Found while profiling", Labels: []string{"backend"}, Parent: "#12", Marker: marker}
	created, err := adapter.Create(context.Background(), draft)
	if err != nil || created.ID != "40" || created.Parent != "12" {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	want := []string{"issue", "create", "--repo", "github.com/acme/app", "--title", draft.Title, "--body", "Found while profiling\n\n" + marker, "--label", "backend", "--parent", "12"}
	if create := stub.ran("issue", "create"); len(create) != 1 || !reflect.DeepEqual(create[0], want) {
		t.Fatalf("issue create = %v\nwant %v", create, want)
	}
	again, err := adapter.Create(context.Background(), draft)
	if err != nil || again.ID != "40" || len(stub.ran("issue", "create")) != 1 {
		t.Fatalf("a retry made the issue again: %+v, %v", again, err)
	}
}

func TestGitHubCreateFindsAnOlderMarkedIssueThroughSearch(t *testing.T) {
	marker := "<!-- inkuna-change:r1:3 -->"
	stub, adapter := newGitHub(t, nil, map[string]func([]string) (string, error){
		newestIssues: reply("[]"),
		"issue list": reply(`[{"number":7,"title":"Index care events","body":"x\n\n` + marker + `","url":"https://github.com/acme/app/issues/7","author":{"login":"octocat"}}]`),
	})
	found, err := adapter.Create(context.Background(), domain.IssueDraft{Title: "Index care events", Marker: marker})
	if err != nil || found.ID != "7" || len(stub.ran("issue", "create")) != 0 {
		t.Fatalf("Create = %+v, %v", found, err)
	}
}

// A URL names an issue of one repository; another repository's number would
// name a different issue here, so it is refused before gh runs.
func TestGitHubRefusesAnotherRepositorysIssue(t *testing.T) {
	stub, adapter := newGitHub(t, nil, map[string]func([]string) (string, error){"issue comment": reply("https://github.com/acme/app/issues/5#issuecomment-1\n")})
	var refusal domain.TrackerRefusal
	for _, id := range []string{"https://github.com/acme/other/issues/5", "https://ghe.example.com/acme/app/issues/5", "https://github.com/acme/app/pull/5"} {
		if _, err := adapter.Comment(context.Background(), id, "Looks good", ""); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "is not an issue of github.com/acme/app") {
			t.Errorf("%s: %v", id, err)
		}
	}
	if len(stub.calls) != 0 {
		t.Fatalf("gh ran for another repository's issue: %v", stub.calls)
	}
	if _, err := adapter.Comment(context.Background(), "https://github.com/Acme/App/issues/5", "Looks good", ""); err != nil {
		t.Fatalf("this repository's issue URL: %v", err)
	}
}

// gh before 2.94 has no parent field and refuses a list that names it; the
// story is read without it.
func TestGitHubStoryOnAnOlderGH(t *testing.T) {
	stub, adapter := newGitHub(t, nil, map[string]func([]string) (string, error){
		"issue view": func(args []string) (string, error) {
			if strings.Contains(args[len(args)-1], "parent") {
				return `Unknown JSON field: "parent"`, errors.New("exit status 1")
			}
			return issueView, nil
		},
	})
	issue, err := adapter.Story(context.Background(), "12")
	if err != nil || issue.Title != "Fix login" || len(stub.ran("issue", "view")) != 2 {
		t.Fatalf("Story = %+v, %v", issue, err)
	}
}

func TestGitHubAttachIsRefused(t *testing.T) {
	_, adapter := newGitHub(t, nil, nil)
	var refusal domain.TrackerRefusal
	if attached, err := adapter.Attach(context.Background(), "12", "https://github.com/acme/app/pull/3", ""); attached || !errors.As(err, &refusal) {
		t.Fatalf("Attach = %v, %v", attached, err)
	}
}

// statusAnswers answer a project status write, with the issue as given.
func statusAnswers(issue string) map[string]func([]string) (string, error) {
	return map[string]func([]string) (string, error){
		"issue view": reply(issue), "project view": reply(projectView), "project field-list": reply(fieldList),
		"project item-add": reply(`{"id":"PVTI_12","title":"Fix login"}`), "project item-edit": reply(`{}`),
		"issue close": reply("✓ Closed issue acme/app#12 (Fix login)"),
	}
}

func TestGitHubAdvanceStartsAnOpenIssue(t *testing.T) {
	stub, adapter := newGitHub(t, roadmap, statusAnswers(issueView))
	moved, err := adapter.Advance(context.Background(), "12", domain.StageStarted, "In Progress")
	if err != nil || !moved.Changed || moved.Status != "In Progress" {
		t.Fatalf("Advance = %+v, %v", moved, err)
	}
	if edit := stub.ran("project", "item-edit"); len(edit) != 1 || edit[0][len(edit[0])-1] != "o2" {
		t.Fatalf("item-edit = %v", edit)
	}
	if len(stub.ran("issue", "close")) != 0 {
		t.Fatal("starting an issue closed it")
	}
}

// What the issue already has is left alone: a status it is in, and an
// issue closed as completed, which a pull request closing it has done.
func TestGitHubAdvanceLeavesWhatIsDoneAlready(t *testing.T) {
	inProgress := strings.Replace(issueView, `"name":"Backlog"`, `"name":"In progress"`, 1)
	closed := strings.Replace(strings.Replace(issueView, `"state":"OPEN","stateReason":""`, `"state":"CLOSED","stateReason":"COMPLETED"`, 1), `"name":"Backlog"`, `"name":"Done"`, 1)
	for _, test := range []struct {
		name, issue string
		stage       domain.StoryStage
		status      string
		note        string
	}{
		{"in progress already", inProgress, domain.StageStarted, "In Progress", "#12 is In progress already"},
		{"done before it started", closed, domain.StageStarted, "In Progress", "#12 is closed as completed already"},
		{"done and closed", closed, domain.StageDone, "Done", "#12 is Done already and closed"},
	} {
		stub, adapter := newGitHub(t, roadmap, statusAnswers(test.issue))
		moved, err := adapter.Advance(context.Background(), "12", test.stage, test.status)
		if err != nil || moved.Changed || moved.Note != test.note {
			t.Errorf("%s: Advance = %+v, %v", test.name, moved, err)
		}
		if len(stub.ran("project", "item-edit"))+len(stub.ran("issue", "close")) != 0 {
			t.Errorf("%s: an issue as asked already was written to: %v", test.name, stub.calls)
		}
	}
}

// Done sets the status and closes an issue still open as completed; one
// whose status says Done but is open is only closed.
func TestGitHubAdvanceDoneClosesTheIssueAsCompleted(t *testing.T) {
	stub, adapter := newGitHub(t, roadmap, statusAnswers(issueView))
	moved, err := adapter.Advance(context.Background(), "12", domain.StageDone, "Done")
	if err != nil || !moved.Changed || moved.Status != "Done" || moved.Note != "and closed #12 as completed" {
		t.Fatalf("Advance = %+v, %v", moved, err)
	}
	if edit := stub.ran("project", "item-edit"); len(edit) != 1 || edit[0][len(edit[0])-1] != "o3" {
		t.Fatalf("item-edit = %v", edit)
	}
	if closed := stub.ran("issue", "close"); len(closed) != 1 || !reflect.DeepEqual(closed[0], []string{"issue", "close", "12", "--repo", "github.com/acme/app", "--reason", "completed"}) {
		t.Fatalf("issue close = %v", closed)
	}
	statusDone := strings.Replace(issueView, `"name":"Backlog"`, `"name":"Done"`, 1)
	stub, adapter = newGitHub(t, roadmap, statusAnswers(statusDone))
	if moved, err := adapter.Advance(context.Background(), "12", domain.StageDone, "Done"); err != nil || !moved.Changed || len(stub.ran("project", "item-edit")) != 0 || len(stub.ran("issue", "close")) != 1 {
		t.Fatalf("Done but open: %+v, %v, %v", moved, err, stub.calls)
	}
	// An issue closed as not planned is never reopened or closed again.
	notPlanned := strings.Replace(issueView, `"state":"OPEN","stateReason":""`, `"state":"CLOSED","stateReason":"NOT_PLANNED"`, 1)
	stub, adapter = newGitHub(t, roadmap, statusAnswers(notPlanned))
	if _, err := adapter.Advance(context.Background(), "12", domain.StageDone, "Done"); err != nil || len(stub.ran("issue", "close")) != 0 || len(stub.ran("issue", "reopen")) != 0 {
		t.Fatalf("not planned: %v, %v", err, stub.calls)
	}
}

func TestGitHubFailureCarriesGitHubsReason(t *testing.T) {
	_, adapter := newGitHub(t, nil, map[string]func([]string) (string, error){
		"issue comment": func([]string) (string, error) {
			return "GraphQL: Resource not accessible by integration (addComment)", errors.New("exit status 1")
		},
	})
	_, err := adapter.Comment(context.Background(), "12", "Looks good", "")
	var refusal domain.TrackerRefusal
	if err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), "Resource not accessible by integration") {
		t.Fatalf("Comment = %v", err)
	}
}

func TestNewTrackerNamesTheRepository(t *testing.T) {
	adapter, err := NewTracker(&stubGH{}, "acme/app", "octocat", nil)
	if err != nil || adapter.repository != "github.com/acme/app" || adapter.path != "acme/app" {
		t.Fatalf("NewTracker = %+v, %v", adapter, err)
	}
	if _, err := NewTracker(&stubGH{}, "app", "octocat", nil); err == nil {
		t.Fatal("a repository without an owner was accepted")
	}
	if _, err := NewTracker(&stubGH{}, "acme/app", " ", nil); err == nil {
		t.Fatal("an adapter without gh's account was made")
	}
	var refusal domain.TrackerRefusal
	if _, err := adapter.Story(context.Background(), "ENG-12"); !errors.As(err, &refusal) {
		t.Fatalf("a Linear key was read as a GitHub issue: %v", err)
	}
}

func TestGitHubStatusesAreTheProjectFieldsOptions(t *testing.T) {
	stub, adapter := newGitHub(t, roadmap, map[string]func([]string) (string, error){
		"project view": reply(projectView), "project field-list": reply(fieldList),
	})
	statuses, err := adapter.Statuses(context.Background(), "12")
	if err != nil || strings.Join(statuses, ", ") != "Backlog, In progress, Done" {
		t.Fatalf("statuses = %v, %v", statuses, err)
	}
	if len(stub.ran("project", "item-add"))+len(stub.ran("project", "item-edit")) > 0 {
		t.Fatal("listing statuses changed something")
	}
	_, adapter = newGitHub(t, nil, nil)
	var refusal domain.TrackerRefusal
	if _, err := adapter.Statuses(context.Background(), "12"); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "names none under github.project") {
		t.Fatalf("without a project: %v", err)
	}
}

func TestGitHubCheckEditNeedsNoGH(t *testing.T) {
	stub, adapter := newGitHub(t, nil, nil)
	blank := ""
	var refusal domain.TrackerRefusal
	if err := adapter.CheckEdit(domain.IssueFields{Assignee: &blank}); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "GitHub login") {
		t.Fatalf("CheckEdit = %v", err)
	}
	title := "Fix login"
	if err := adapter.CheckEdit(domain.IssueFields{Title: &title, AddLabels: []string{"backend"}}); err != nil || len(stub.calls) != 0 {
		t.Fatalf("CheckEdit = %v, ran %v", err, stub.calls)
	}
}
