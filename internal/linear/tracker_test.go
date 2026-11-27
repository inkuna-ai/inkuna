package linear

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// stubLinear is a Linear GraphQL endpoint that answers each operation, by
// name, with a canned response, and keeps every request it was sent.
type stubLinear struct {
	t         *testing.T
	mu        sync.Mutex
	responses map[string]func(variables map[string]any) string
	// status is the HTTP status an operation is answered with, 200 when
	// unset: Linear sends its errors with 400 or 403.
	status map[string]int
	calls  []stubCall
}

type stubCall struct {
	Operation string
	Query     string
	Variables map[string]any
}

var operationName = regexp.MustCompile(`^\s*(?:query|mutation)\s+(\w+)`)

func (stub *stubLinear) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "lin_api_test" {
		stub.t.Errorf("the key was not sent: %q", request.Header.Get("Authorization"))
	}
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		stub.t.Fatal(err)
	}
	match := operationName.FindStringSubmatch(body.Query)
	if match == nil {
		stub.t.Fatalf("unnamed operation: %s", body.Query)
	}
	stub.mu.Lock()
	stub.calls = append(stub.calls, stubCall{Operation: match[1], Query: body.Query, Variables: body.Variables})
	respond := stub.responses[match[1]]
	status := stub.status[match[1]]
	stub.mu.Unlock()
	if respond == nil {
		stub.t.Errorf("unexpected operation %s", match[1])
		_, _ = writer.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
		return
	}
	if status != 0 {
		writer.WriteHeader(status)
	}
	_, _ = writer.Write([]byte(respond(body.Variables)))
}

// operations lists the operations sent, in order.
func (stub *stubLinear) operations() []string {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	names := make([]string, 0, len(stub.calls))
	for _, call := range stub.calls {
		names = append(names, call.Operation)
	}
	return names
}

// sent returns the variables of the last call of an operation.
func (stub *stubLinear) sent(operation string) map[string]any {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for index := len(stub.calls) - 1; index >= 0; index-- {
		if stub.calls[index].Operation == operation {
			return stub.calls[index].Variables
		}
	}
	return nil
}

func (stub *stubLinear) mutated() bool {
	for _, name := range stub.operations() {
		if name == "InkunaUpdate" || name == "InkunaComment" || name == "InkunaCommentUpdate" || name == "InkunaCreate" || name == "InkunaAttach" {
			return true
		}
	}
	return false
}

func newStub(t *testing.T, responses map[string]func(map[string]any) string) (*stubLinear, *Tracker) {
	t.Helper()
	stub := &stubLinear{t: t, responses: responses}
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	client, err := NewWithEndpoint("lin_api_test", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewTracker(client)
	if err != nil {
		t.Fatal(err)
	}
	return stub, adapter
}

func answer(body string) func(map[string]any) string {
	return func(map[string]any) string { return body }
}

const storyResponse = `{"data":{"issue":{"id":"issue-uuid","identifier":"ENG-12","title":"Fix login","description":"Steps","url":"https://linear.app/acme/issue/ENG-12/fix-login",
"state":{"id":"todo","name":"Todo","type":"unstarted"},
"team":{"id":"team-uuid","key":"ENG","activeCycle":{"id":"cycle-7","number":7,"name":""}},
"cycle":{"id":"cycle-7","number":7,"name":""},"projectMilestone":null,"parent":{"identifier":"ENG-1"},
"labels":{"nodes":[{"id":"label-bug","name":"Bug"},{"id":"label-triage","name":"Triage"}]},
"assignee":{"name":"Ada Lovelace","displayName":"ada","email":"ada@example.com"},
"attachments":{"nodes":[{"url":"https://github.com/acme/app/pull/3"}]}}}}`

const statesResponse = `{"data":{"issue":{"id":"issue-uuid","state":{"id":"todo","name":"Todo","type":"unstarted"},"team":{"key":"ENG","states":{"nodes":[
{"id":"todo","name":"Todo","type":"unstarted"},{"id":"doing","name":"In Progress","type":"started"},
{"id":"done","name":"Done","type":"completed"},{"id":"cancel","name":"Canceled","type":"canceled"}]}}}}}`

const updated = `{"data":{"issueUpdate":{"success":true}}}`

func TestStoryReadsTheIssue(t *testing.T) {
	_, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStory": answer(storyResponse)})
	issue, err := adapter.Story(context.Background(), "eng-12")
	if err != nil {
		t.Fatal(err)
	}
	if issue.ID != "ENG-12" || issue.Status != "Todo" || issue.Done || issue.Team != "ENG" || issue.Cycle != "Cycle 7" || !issue.CurrentCycle ||
		issue.Parent != "ENG-1" || strings.Join(issue.Labels, ",") != "Bug,Triage" || issue.Assignees[0] != "ada" || issue.Links[0] != "https://github.com/acme/app/pull/3" {
		t.Fatalf("issue = %+v", issue)
	}
	if _, err := adapter.Story(context.Background(), "not an issue"); err == nil {
		t.Fatal("a malformed id was read")
	}
}

// Linear answers an issue it cannot find, or one the key cannot see, with
// HTTP 400 and an error; asking again will not find it, so it is a refusal.
func TestStoryOfAnIssueTheKeyCannotSee(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStory": answer(entityNotFound)})
	stub.status = map[string]int{"InkunaStory": http.StatusBadRequest}
	var refusal domain.TrackerRefusal
	if _, err := adapter.Story(context.Background(), "ENG-404"); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "ENG-404 was not found") {
		t.Fatalf("Story = %v", err)
	}
}

const (
	entityNotFound = `{"errors":[{"message":"Entity not found: Issue","extensions":{"type":"invalid input","code":"INVALID_INPUT","statusCode":400,"userPresentableMessage":"Could not find referenced Issue."}}],"data":null}`
	scopeRefused   = `{"errors":[{"message":"Invalid scope: write required","extensions":{"type":"forbidden","code":"FORBIDDEN","statusCode":403}}],"data":null}`
)

func TestSetStatusMovesToTheNamedState(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesResponse), "InkunaUpdate": answer(updated)})
	if err := adapter.SetStatus(context.Background(), "ENG-12", "in progress"); err != nil {
		t.Fatal(err)
	}
	sent := stub.sent("InkunaUpdate")
	if sent["id"] != "issue-uuid" || sent["input"].(map[string]any)["stateId"] != "doing" {
		t.Fatalf("issueUpdate = %v", sent)
	}
}

func TestSetStatusRefusals(t *testing.T) {
	for status, reason := range map[string]string{
		"In Review": `no state named "In Review"; its states are Todo, In Progress, Done, Canceled`,
		"Canceled":  "would close it as not planned",
	} {
		stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesResponse)})
		var refusal domain.TrackerRefusal
		if err := adapter.SetStatus(context.Background(), "ENG-12", status); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, reason) {
			t.Errorf("%s: %v", status, err)
		}
		if stub.mutated() {
			t.Errorf("%s: a refused status was sent", status)
		}
	}
	stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesResponse)})
	if err := adapter.SetStatus(context.Background(), "ENG-12", "Todo"); err != nil || stub.mutated() {
		t.Fatalf("a story in the state already was moved: %v", err)
	}
}

// statesOf answers InkunaStates for an issue in state, in a team whose
// states are given as JSON nodes.
func statesOf(state, teamStates string) string {
	return `{"data":{"issue":{"id":"issue-uuid","state":` + state + `,"team":{"key":"ENG","states":{"nodes":[` + teamStates + `]}}}}}`
}

const (
	todoState    = `{"id":"todo","name":"Todo","type":"unstarted"}`
	defaultFlow  = `{"id":"todo","name":"Todo","type":"unstarted","position":0},{"id":"doing","name":"In Progress","type":"started","position":2},{"id":"review","name":"In Review","type":"started","position":3},{"id":"done","name":"Done","type":"completed","position":4},{"id":"cancel","name":"Canceled","type":"canceled","position":5}`
	renamedFlow  = `{"id":"todo","name":"Todo","type":"unstarted","position":0},{"id":"review","name":"Reviewing","type":"started","position":3},{"id":"building","name":"Building","type":"started","position":1},{"id":"merged","name":"Merged","type":"completed","position":5},{"id":"shipped","name":"Shipped","type":"completed","position":4}`
	noStartState = `{"id":"todo","name":"Todo","type":"unstarted","position":0},{"id":"done","name":"Done","type":"completed","position":4}`
)

func TestAdvanceMovesToTheNamedState(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesOf(todoState, defaultFlow)), "InkunaUpdate": answer(updated)})
	moved, err := adapter.Advance(context.Background(), "ENG-12", domain.StageStarted, "In Progress")
	if err != nil || !moved.Changed || moved.Status != "In Progress" || moved.Note != "" {
		t.Fatalf("Advance = %+v, %v", moved, err)
	}
	if sent := stub.sent("InkunaUpdate"); sent["input"].(map[string]any)["stateId"] != "doing" {
		t.Fatalf("issueUpdate = %v", sent)
	}
}

// A team without a state of the name gets its first state of the stage's
// type, by its place in the workflow, and the note says so.
func TestAdvanceFallsBackToTheTeamsFirstStateOfTheStage(t *testing.T) {
	for _, test := range []struct {
		stage  domain.StoryStage
		status string
		id     string
		name   string
	}{
		{domain.StageStarted, "In Progress", "building", "Building"},
		{domain.StageDone, "Done", "shipped", "Shipped"},
	} {
		stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesOf(todoState, renamedFlow)), "InkunaUpdate": answer(updated)})
		moved, err := adapter.Advance(context.Background(), "ENG-12", test.stage, test.status)
		if err != nil || !moved.Changed || moved.Status != test.name || !strings.Contains(moved.Note, `team ENG has no state named "`+test.status+`", so its first`) {
			t.Errorf("%s: Advance = %+v, %v", test.stage, moved, err)
		}
		if sent := stub.sent("InkunaUpdate"); sent == nil || sent["input"].(map[string]any)["stateId"] != test.id {
			t.Errorf("%s: issueUpdate = %v", test.stage, sent)
		}
	}
	stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesOf(todoState, noStartState))})
	var refusal domain.TrackerRefusal
	if _, err := adapter.Advance(context.Background(), "ENG-12", domain.StageStarted, "In Progress"); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "no started state") || stub.mutated() {
		t.Fatalf("a team without a started state: %v", err)
	}
}

// A story already in the state, or done, is left alone, whichever stage is
// asked for: Linear's GitHub integration often got there first.
func TestAdvanceLeavesAStoryThatIsThereAlready(t *testing.T) {
	for _, test := range []struct {
		state string
		stage domain.StoryStage
		note  string
	}{
		{`{"id":"doing","name":"In Progress","type":"started"}`, domain.StageStarted, "ENG-12 is In Progress already"},
		{`{"id":"done","name":"Done","type":"completed"}`, domain.StageStarted, "ENG-12 is done already, in Done"},
		{`{"id":"done","name":"Done","type":"completed"}`, domain.StageDone, "ENG-12 is done already, in Done"},
	} {
		stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesOf(test.state, defaultFlow))})
		moved, err := adapter.Advance(context.Background(), "ENG-12", test.stage, "In Progress")
		if err != nil || moved.Changed || moved.Note != test.note || stub.mutated() {
			t.Errorf("%s from %s: %+v, %v", test.stage, test.state, moved, err)
		}
	}
	// A story in review moves back to In Progress when a new run starts on it.
	stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesOf(`{"id":"review","name":"In Review","type":"started"}`, defaultFlow)), "InkunaUpdate": answer(updated)})
	if moved, err := adapter.Advance(context.Background(), "ENG-12", domain.StageStarted, "In Progress"); err != nil || !moved.Changed || !stub.mutated() {
		t.Fatalf("from In Review: %+v, %v", moved, err)
	}
}

func TestAdvanceNeverCancels(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesOf(todoState, defaultFlow))})
	var refusal domain.TrackerRefusal
	if _, err := adapter.Advance(context.Background(), "ENG-12", domain.StageDone, "Canceled"); !errors.As(err, &refusal) || stub.mutated() {
		t.Fatalf("a canceled state: %v", err)
	}
}

func TestCommentWithAMarkerEditsTheCommentItMade(t *testing.T) {
	exists := false
	stub, adapter := newStub(t, map[string]func(map[string]any) string{
		"InkunaStory": answer(storyResponse),
		"InkunaFindComment": func(variables map[string]any) string {
			if exists {
				return `{"data":{"issue":{"comments":{"nodes":[{"id":"` + variables["comment"].(string) + `"}]}}}}`
			}
			return `{"data":{"issue":{"comments":{"nodes":[]}}}}`
		},
		"InkunaComment": func(map[string]any) string {
			exists = true
			return `{"data":{"commentCreate":{"success":true,"comment":{"url":"https://linear.app/acme/issue/ENG-12#comment-1"}}}}`
		},
		"InkunaCommentUpdate": answer(`{"data":{"commentUpdate":{"success":true,"comment":{"url":"https://linear.app/acme/issue/ENG-12#comment-1"}}}}`),
	})
	ctx := context.Background()
	url, err := adapter.Comment(ctx, "ENG-12", "Completed in 1 round", "<!-- inkuna-run:r1 -->")
	if err != nil || url != "https://linear.app/acme/issue/ENG-12#comment-1" {
		t.Fatalf("Comment = %q, %v", url, err)
	}
	created := stub.sent("InkunaComment")["input"].(map[string]any)
	commentID := created["id"].(string)
	if created["issueId"] != "issue-uuid" || created["body"] != "Completed in 1 round" || !uuidPattern.MatchString(commentID) {
		t.Fatalf("commentCreate = %v", created)
	}
	if _, err := adapter.Comment(ctx, "ENG-12", "Completed in 2 rounds", "<!-- inkuna-run:r1 -->"); err != nil {
		t.Fatal(err)
	}
	edit := stub.sent("InkunaCommentUpdate")
	if edit["id"] != commentID || edit["input"].(map[string]any)["body"] != "Completed in 2 rounds" {
		t.Fatalf("the retry did not edit the comment it made: %v", edit)
	}
	if markerUUID("comment", "issue-uuid", "<!-- inkuna-run:r2 -->") == commentID {
		t.Fatal("another marker names the same comment")
	}
}

func TestCommentWithoutAMarker(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{
		"InkunaStory":   answer(storyResponse),
		"InkunaComment": answer(`{"data":{"commentCreate":{"success":true,"comment":{"url":"https://linear.app/c/2"}}}}`),
	})
	if _, err := adapter.Comment(context.Background(), "ENG-12", "Looks good", ""); err != nil {
		t.Fatal(err)
	}
	if input := stub.sent("InkunaComment")["input"].(map[string]any); input["id"] != nil || input["body"] != "Looks good" {
		t.Fatalf("commentCreate = %v", input)
	}
	var refusal domain.TrackerRefusal
	if _, err := adapter.Comment(context.Background(), "ENG-12", "  ", ""); !errors.As(err, &refusal) {
		t.Fatalf("an empty comment was sent: %v", err)
	}
}

const labelsResponse = `{"data":{"team":{"labels":{"nodes":[{"id":"label-backend","name":"Backend"}]}},"issueLabels":{"nodes":[{"id":"label-bug","name":"Bug"}]}}}`

func TestEditSetsEachField(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{
		"InkunaStory":  answer(storyResponse),
		"InkunaLabels": answer(labelsResponse),
		"InkunaViewer": answer(`{"data":{"viewer":{"id":"user-me","name":"Ada","email":"ada@example.com"}}}`),
		"InkunaCycles": answer(`{"data":{"team":{"activeCycle":{"id":"cycle-7","number":7,"name":""},"cycles":{"nodes":[{"id":"cycle-7","number":7,"name":""},{"id":"cycle-8","number":8,"name":"Hardening"}]}}}}`),
		"InkunaUpdate": answer(updated),
	})
	title, description, estimate, priority, assignee, cycle := "Fix login on Safari", "Repro inside", 3, 2, "me", "hardening"
	err := adapter.Edit(context.Background(), "ENG-12", domain.IssueFields{
		Title: &title, Description: &description, AddLabels: []string{"backend"}, RemoveLabels: []string{"triage", "absent"},
		Estimate: &estimate, Priority: &priority, Assignee: &assignee, Cycle: &cycle,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := stub.sent("InkunaUpdate")["input"].(map[string]any)
	want := map[string]any{
		"title": title, "description": description, "addedLabelIds": []any{"label-backend"}, "removedLabelIds": []any{"label-triage"},
		"estimate": float64(3), "priority": float64(2), "assigneeId": "user-me", "cycleId": "cycle-8",
	}
	if encoded, _ := json.Marshal(input); string(encoded) != mustJSON(want) {
		t.Fatalf("issueUpdate input = %s\nwant %s", encoded, mustJSON(want))
	}
}

func TestEditFindsAnAssigneeByEmail(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{
		"InkunaStory":  answer(storyResponse),
		"InkunaUser":   answer(`{"data":{"users":{"nodes":[{"id":"user-grace","active":true},{"id":"user-old","active":false}]}}}`),
		"InkunaUpdate": answer(updated),
	})
	assignee := "grace@example.com"
	if err := adapter.Edit(context.Background(), "ENG-12", domain.IssueFields{Assignee: &assignee}); err != nil {
		t.Fatal(err)
	}
	if stub.sent("InkunaUser")["value"] != assignee || stub.sent("InkunaUpdate")["input"].(map[string]any)["assigneeId"] != "user-grace" {
		t.Fatalf("calls = %v", stub.calls)
	}
}

func TestEditRefusals(t *testing.T) {
	milestone, badPriority, unknownCycle, ambiguous := "Beta", 7, "Cycle 99", "ada"
	for _, test := range []struct {
		name   string
		fields domain.IssueFields
		reason string
	}{
		{"milestone", domain.IssueFields{Milestone: &milestone}, "milestone belongs to its project"},
		{"priority", domain.IssueFields{Priority: &badPriority}, "Linear priority 7 must be"},
		{"label", domain.IssueFields{AddLabels: []string{"frontend"}}, `no label named "frontend" in team ENG`},
		{"cycle", domain.IssueFields{Cycle: &unknownCycle}, `no current or upcoming cycle "Cycle 99"`},
		{"user", domain.IssueFields{Assignee: &ambiguous}, `"ada" names 2 Linear users`},
	} {
		stub, adapter := newStub(t, map[string]func(map[string]any) string{
			"InkunaStory":  answer(storyResponse),
			"InkunaLabels": answer(labelsResponse),
			"InkunaCycles": answer(`{"data":{"team":{"activeCycle":null,"cycles":{"nodes":[{"id":"cycle-8","number":8,"name":""}]}}}}`),
			"InkunaUser":   answer(`{"data":{"users":{"nodes":[{"id":"a","active":true},{"id":"b","active":true}]}}}`),
		})
		var refusal domain.TrackerRefusal
		if err := adapter.Edit(context.Background(), "ENG-12", test.fields); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, test.reason) {
			t.Errorf("%s: %v", test.name, err)
		}
		if stub.mutated() {
			t.Errorf("%s: a refused edit was sent", test.name)
		}
	}
}

func TestCreateMakesASubIssueInItsParentsTeamOnce(t *testing.T) {
	made := ""
	stub, adapter := newStub(t, map[string]func(map[string]any) string{
		"InkunaStory": func(variables map[string]any) string {
			if variables["id"] != "ENG-12" {
				return strings.Replace(storyResponse, `"identifier":"ENG-12"`, `"identifier":"ENG-13"`, 1)
			}
			return storyResponse
		},
		"InkunaLabels": answer(labelsResponse),
		"InkunaMarked": func(variables map[string]any) string {
			if variables["id"] == made {
				return `{"data":{"issues":{"nodes":[{"id":"` + made + `"}]}}}`
			}
			return `{"data":{"issues":{"nodes":[]}}}`
		},
		"InkunaCreate": func(variables map[string]any) string {
			made = variables["input"].(map[string]any)["id"].(string)
			return `{"data":{"issueCreate":{"success":true,"issue":{"identifier":"ENG-13"}}}}`
		},
	})
	draft := domain.IssueDraft{Title: "Index care events", Description: "Found while profiling", Labels: []string{"bug"}, Parent: "ENG-12", Marker: "inkuna-change:r1:3"}
	created, err := adapter.Create(context.Background(), draft)
	if err != nil || created.ID != "ENG-13" {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	input := stub.sent("InkunaCreate")["input"].(map[string]any)
	if input["teamId"] != "team-uuid" || input["parentId"] != "issue-uuid" || input["title"] != draft.Title || input["labelIds"].([]any)[0] != "label-bug" {
		t.Fatalf("issueCreate input = %v", input)
	}
	creates := 0
	if _, err := adapter.Create(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	for _, name := range stub.operations() {
		if name == "InkunaCreate" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("a retry made the issue again: %v", stub.operations())
	}
}

func TestCreateInATeam(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{
		"InkunaTeam":   answer(`{"data":{"teams":{"nodes":[{"id":"team-ops","key":"OPS"}]}}}`),
		"InkunaCreate": answer(`{"data":{"issueCreate":{"success":true,"issue":{"identifier":"OPS-2"}}}}`),
		"InkunaStory":  answer(strings.Replace(storyResponse, `"identifier":"ENG-12"`, `"identifier":"OPS-2"`, 1)),
	})
	if created, err := adapter.Create(context.Background(), domain.IssueDraft{Title: "Rotate keys", Team: "ops"}); err != nil || created.ID != "OPS-2" {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	if input := stub.sent("InkunaCreate")["input"].(map[string]any); input["teamId"] != "team-ops" || input["id"] != nil {
		t.Fatalf("issueCreate input = %v", input)
	}
	var refusal domain.TrackerRefusal
	if _, err := adapter.Create(context.Background(), domain.IssueDraft{Title: "Orphan"}); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "needs a team or a parent") {
		t.Fatalf("an issue without a team: %v", err)
	}
}

func TestAttachLinksAURLOnce(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{
		"InkunaStory":  answer(storyResponse),
		"InkunaAttach": answer(`{"data":{"attachmentLinkURL":{"success":true}}}`),
	})
	ctx := context.Background()
	// Linear's GitHub integration attached it, with a trailing slash.
	if attached, err := adapter.Attach(ctx, "ENG-12", "https://GitHub.com/acme/app/pull/3/", "Pull request"); attached || err != nil || stub.mutated() {
		t.Fatalf("an attached link was attached again: %v, %v", attached, err)
	}
	if attached, err := adapter.Attach(ctx, "ENG-12", "https://github.com/acme/app/pull/4", "Pull request"); !attached || err != nil {
		t.Fatalf("Attach = %v, %v", attached, err)
	}
	if sent := stub.sent("InkunaAttach"); sent["issue"] != "issue-uuid" || sent["url"] != "https://github.com/acme/app/pull/4" || sent["title"] != "Pull request" {
		t.Fatalf("attachmentLinkURL = %v", sent)
	}
}

func TestAWriteLinearRejectsCarriesItsReason(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{
		"InkunaStates": answer(statesResponse),
		"InkunaUpdate": answer(scopeRefused),
	})
	stub.status = map[string]int{"InkunaUpdate": http.StatusForbidden}
	err := adapter.SetStatus(context.Background(), "ENG-12", "Done")
	var refusal domain.TrackerRefusal
	if err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), "Invalid scope: write required (FORBIDDEN)") {
		t.Fatalf("SetStatus = %v", err)
	}
}

func TestWriteAccessTellsAReadOnlyKeyFromAWritingOne(t *testing.T) {
	probes := []string{"InkunaCanUpdate", "InkunaCanComment", "InkunaCanCreate"}
	for _, test := range []struct {
		name   string
		answer func(operation string) (int, string)
		want   WriteAccess
	}{
		{
			"read only",
			func(string) (int, string) { return http.StatusForbidden, scopeRefused },
			WriteAccess{Update: AccessDenied, Comment: AccessDenied, Create: AccessDenied},
		},
		{
			"writes",
			func(string) (int, string) { return http.StatusBadRequest, entityNotFound },
			WriteAccess{Update: AccessAllowed, Comment: AccessAllowed, Create: AccessAllowed},
		},
		{
			"comments only",
			func(operation string) (int, string) {
				if operation == "InkunaCanComment" {
					return http.StatusBadRequest, entityNotFound
				}
				return http.StatusForbidden, scopeRefused
			},
			WriteAccess{Update: AccessDenied, Comment: AccessAllowed, Create: AccessDenied},
		},
		{
			// An answer of another shape, even with HTTP 200, is no answer.
			"unclear",
			func(string) (int, string) {
				return http.StatusOK, `{"errors":[{"message":"Rate limit exceeded","extensions":{"code":"RATELIMITED"}}]}`
			},
			WriteAccess{Update: AccessUnknown, Comment: AccessUnknown, Create: AccessUnknown},
		},
	} {
		responses := map[string]func(map[string]any) string{}
		statuses := map[string]int{}
		for _, operation := range probes {
			status, body := test.answer(operation)
			responses[operation], statuses[operation] = answer(body), status
		}
		stub, adapter := newStub(t, responses)
		stub.status = statuses
		access, err := adapter.client.WriteAccess(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if access.Update != test.want.Update || access.Comment != test.want.Comment || access.Create != test.want.Create {
			t.Errorf("%s: access = %+v", test.name, access)
		}
		if test.want.Update != AccessAllowed && access.Reasons["update"] == "" {
			t.Errorf("%s: no reason for update", test.name)
		}
	}
}

// Each probe names an id that cannot exist yet: a fresh random one, never
// one someone could create in advance.
func TestWriteAccessProbesNameFreshIDs(t *testing.T) {
	responses := map[string]func(map[string]any) string{}
	for _, operation := range []string{"InkunaCanUpdate", "InkunaCanComment", "InkunaCanCreate"} {
		responses[operation] = answer(entityNotFound)
	}
	stub, adapter := newStub(t, responses)
	for range 2 {
		if _, err := adapter.client.WriteAccess(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	quoted := regexp.MustCompile(`"([0-9a-f-]{36})"`)
	seen := map[string]bool{}
	for _, call := range stub.calls {
		match := quoted.FindStringSubmatch(call.Query)
		if match == nil || !uuidPattern.MatchString(match[1]) || match[1][14] != '4' {
			t.Fatalf("a probe named no version 4 id: %s", call.Query)
		}
		if seen[match[1]] {
			t.Fatalf("a probe reused the id %s", match[1])
		}
		seen[match[1]] = true
	}
	if len(seen) != 6 {
		t.Fatalf("probes = %d", len(seen))
	}
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestStatusesListTheTeamsStatesButCanceled(t *testing.T) {
	stub, adapter := newStub(t, map[string]func(map[string]any) string{"InkunaStates": answer(statesOf(todoState, defaultFlow))})
	statuses, err := adapter.Statuses(context.Background(), "ENG-12")
	if err != nil || strings.Join(statuses, ", ") != "Todo, In Progress, In Review, Done" {
		t.Fatalf("statuses = %v, %v", statuses, err)
	}
	if stub.mutated() {
		t.Fatal("listing statuses changed something")
	}
}

func TestCheckEditRefusesWhatLinearNeverTakes(t *testing.T) {
	adapter := &Tracker{}
	milestone, priority, estimate, blank := "M1", 5, -1, " "
	for reason, fields := range map[string]domain.IssueFields{
		"belongs to its project":         {Milestone: &milestone},
		"must be 0 (none)":               {Priority: &priority},
		"an estimate cannot be negative": {Estimate: &estimate},
		"a title cannot be empty":        {Title: &blank},
	} {
		var refusal domain.TrackerRefusal
		if err := adapter.CheckEdit(fields); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, reason) {
			t.Errorf("%+v: %v", fields, err)
		}
	}
	if err := adapter.CheckEdit(domain.IssueFields{Priority: new(int)}); err != nil {
		t.Fatalf("priority 0 is none: %v", err)
	}
}
