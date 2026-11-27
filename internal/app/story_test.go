package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/notify"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/tracker"
)

// scriptedGitHub answers gh as octocat, signed in to github.com, and each
// other command by its first two words, keeping every command it ran.
type scriptedGitHub struct {
	mu      sync.Mutex
	answers map[string]func(args []string) (string, error)
	calls   [][]string
}

func (gh *scriptedGitHub) Run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) > 1 && args[0] == "auth" && args[1] == "status" {
		return []byte(`{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"octocat"}]}}`), nil
	}
	gh.mu.Lock()
	gh.calls = append(gh.calls, append([]string(nil), args...))
	answer := gh.answers[args[0]+" "+args[1]]
	gh.mu.Unlock()
	if answer == nil {
		return nil, fmt.Errorf("unexpected gh %v", args)
	}
	output, err := answer(args)
	return []byte(output), err
}

func (gh *scriptedGitHub) answer(command, output string) {
	gh.mu.Lock()
	defer gh.mu.Unlock()
	gh.answers[command] = func([]string) (string, error) { return output, nil }
}

// ran lists the commands that began with words.
func (gh *scriptedGitHub) ran(words ...string) [][]string {
	gh.mu.Lock()
	defer gh.mu.Unlock()
	matched := make([][]string, 0)
	for _, call := range gh.calls {
		if len(call) >= len(words) && strings.Join(call[:len(words)], " ") == strings.Join(words, " ") {
			matched = append(matched, call)
		}
	}
	return matched
}

const (
	storyIssue = `{"number":12,"title":"Fix login","body":"Steps","url":"https://github.com/acme/app/issues/12","state":"OPEN","stateReason":"",
"labels":[],"assignees":[],"milestone":null,"projectItems":[{"title":"Roadmap","status":{"name":"Backlog"}}]}`
	storyIssueDone = `{"number":12,"title":"Fix login","body":"Steps","url":"https://github.com/acme/app/issues/12","state":"CLOSED","stateReason":"COMPLETED",
"labels":[],"assignees":[],"milestone":null,"projectItems":[{"title":"Roadmap","status":{"name":"Done"}}]}`
	roadmapProject   = `{"id":"PVT_1","number":3,"title":"Roadmap"}`
	roadmapFields    = `{"fields":[{"id":"F_status","name":"Status","type":"ProjectV2SingleSelectField","options":[{"id":"o1","name":"Backlog"},{"id":"o2","name":"In progress"},{"id":"o3","name":"Done"}]}]}`
	storyTrackerYAML = "version: 1\nupdates: { start: true, result: true, done: true }\ngithub: { project: { owner: acme, number: 3 }, inProgress: In progress }\n"
)

// storyGitHub answers every gh command a run's story updates make on issue
// 12 of acme/app.
func storyGitHub() *scriptedGitHub {
	gh := &scriptedGitHub{answers: map[string]func([]string) (string, error){}}
	for command, output := range map[string]string{
		"issue view": storyIssue, "project view": roadmapProject, "project field-list": roadmapFields,
		"project item-add": `{"id":"PVTI_12"}`, "project item-edit": `{}`,
		"api --hostname": `[[]]`, "issue comment": "https://github.com/acme/app/issues/12#issuecomment-7\n",
		"issue close": "✓ Closed issue acme/app#12", "pr list": `[]`,
	} {
		gh.answer(command, output)
	}
	return gh
}

// storySpec is workflowTestSpec for a task imported from issue 12.
func storySpec(t *testing.T, service *Service) domain.WorkflowSpec {
	t.Helper()
	spec := workflowTestSpec(t, service)
	task, err := service.store.Task(context.Background(), spec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Source = domain.TaskSource{Kind: "github", Reference: "https://github.com/acme/app/issues/12"}
	if err := service.store.SaveTask(context.Background(), "project", "planned", task); err != nil {
		t.Fatal(err)
	}
	return spec
}

func startStoryRun(t *testing.T, service *Service, spec domain.WorkflowSpec) WorkflowRunView {
	t.Helper()
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	// The result is written apart from the run; once the run's goroutine
	// and its report have returned, every update is made.
	service.runsWG.Wait()
	run, err = service.WorkflowRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func eventIndex(run WorkflowRunView, text string) int {
	for index, event := range run.Events {
		if strings.Contains(event.Summary, text) {
			return index
		}
	}
	return -1
}

func TestARunMovesItsStoryToInProgressAndCommentsItsResult(t *testing.T) {
	root := trackerRepository(t, storyTrackerYAML)
	service := workflowTestService(t, root, &workflowRuntime{})
	gh := storyGitHub()
	service.github = gh
	run := startStoryRun(t, service, storySpec(t, service))

	if edit := gh.ran("project", "item-edit"); len(edit) != 1 || edit[0][len(edit[0])-1] != "o2" {
		t.Fatalf("item-edit = %v", edit)
	}
	moved, worked := eventIndex(run, "Wrote to GitHub issue 12: status → In progress"), eventIndex(run, "worker started")
	if moved < 0 || worked < 0 || moved > worked {
		t.Fatalf("the story did not move before the worker started: %+v", run.Events)
	}
	comments := gh.ran("issue", "comment")
	if len(comments) != 1 {
		t.Fatalf("comments = %v", gh.calls)
	}
	body := comments[0][len(comments[0])-1]
	for _, part := range []string{`Inkuna's run "Inspect the repository" completed in 1 round.`, "- Changes: none committed.", "<!-- inkuna-run:" + run.ID + " -->"} {
		if !strings.Contains(body, part) {
			t.Errorf("the comment lacks %q:\n%s", part, body)
		}
	}
	if !eventSaid(run, "Wrote to GitHub issue 12: comment: Inkuna's run") {
		t.Fatalf("the result is not on the timeline: %+v", run.Events)
	}
	if run.Story == nil || run.Story.ID != "12" || run.Story.Tracker != "github" || run.Story.MarkDone {
		t.Fatalf("story = %+v; a run without a branch has no pull request to mark done", run.Story)
	}
	if sent := service.notifier.(*notify.Recorder).Sent(); len(sent) != 1 || sent[0].Kind != "completed" {
		t.Fatalf("notifications = %+v", sent)
	}
	// Moved once: a later round never starts the story again.
	story, err := service.loadRunStory(context.Background(), run.ID)
	if err != nil || story == nil || !story.Started {
		t.Fatalf("kept story = %+v, %v", story, err)
	}
}

// A story update that fails is a warning on the timeline and a
// notification, and the run goes on and ends as it would have.
func TestAFailedStoryUpdateNeverFailsTheRun(t *testing.T) {
	root := trackerRepository(t, storyTrackerYAML)
	service := workflowTestService(t, root, &workflowRuntime{})
	gh := storyGitHub()
	gh.answers["project item-edit"] = func([]string) (string, error) {
		return "Resource not accessible: your token has not been granted the required scopes", errors.New("exit status 1")
	}
	service.github = gh
	run := startStoryRun(t, service, storySpec(t, service))
	if run.State != "completed" || !eventSaid(run, "Writing to GitHub issue 12 failed (status → In progress)") || !eventSaid(run, "required scopes") {
		t.Fatalf("run = %s, events %+v", run.State, run.Events)
	}
	var warned bool
	for _, sent := range service.notifier.(*notify.Recorder).Sent() {
		if sent.Kind == "tracker-failed" && sent.Reference == run.ID && strings.Contains(sent.Title, "12") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("notifications = %+v", service.notifier.(*notify.Recorder).Sent())
	}
	if len(gh.ran("issue", "comment")) != 1 {
		t.Fatal("a failed start stopped the result")
	}
}

// An update tracker.yaml leaves off is never tried, and one a step turns
// on in the checkout after approval stays off for the run.
func TestARunWritesOnlyWhatItWasApprovedFor(t *testing.T) {
	root := trackerRepository(t, "version: 1\nupdates: { start: false, result: false }\n")
	runtime := &workflowRuntime{handle: func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		// A step that writes in the checkout raises tracker.yaml there.
		raised := []byte(storyTrackerYAML)
		return agentruntime.RunResult{}, os.WriteFile(filepath.Join(root, ".inkuna", "tracker.yaml"), raised, 0o644)
	}}
	service := workflowTestService(t, root, runtime)
	gh := storyGitHub()
	service.github = gh
	run := startStoryRun(t, service, storySpec(t, service))
	if len(gh.calls) != 0 {
		t.Fatalf("gh was run for updates that were off: %v", gh.calls)
	}
	for _, event := range run.Events {
		if strings.Contains(event.Summary, "GitHub issue") {
			t.Fatalf("an update that was off is on the timeline: %+v", event)
		}
	}
	if run.Story == nil || run.Story.ID != "12" {
		t.Fatalf("story = %+v", run.Story)
	}
}

// storyExecution is an ended run on feature/12-login whose story is issue
// 12, kept with the tracker settings of root.
func storyExecution(t *testing.T, service *Service, root, state string) string {
	t.Helper()
	ctx := context.Background()
	spec := workflowTestSpec(t, service)
	runID := "run-story"
	if err := service.store.SaveExecution(ctx, storage.Execution{ID: runID, WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: state, Title: "Fix login", Branch: "feature/12-login", Round: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.store.StartRun(ctx, runID+"-worker", runID, spec.ID, "worker", 1, domain.RuntimeCodex, ""); err != nil {
		t.Fatal(err)
	}
	settings, err := tracker.Resolve(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.saveRunStory(ctx, runID, runStory{ID: "12", Settings: settings, Started: true}); err != nil {
		t.Fatal(err)
	}
	return runID
}

func TestMarkDoneWaitsForTheMergeAndFillsInWhatIsMissing(t *testing.T) {
	root := trackerRepository(t, storyTrackerYAML)
	service := workflowTestService(t, root, &workflowRuntime{})
	gh := storyGitHub()
	service.github = gh
	runID := storyExecution(t, service, root, "completed")
	view, err := service.WorkflowRun(runID)
	if err != nil || view.Story == nil || !view.Story.MarkDone {
		t.Fatalf("an ended run with a branch offers Mark done: %+v, %v", view.Story, err)
	}

	// Not merged yet: nothing changes.
	gh.answer("pr list", `[{"number":40,"title":"Fix login","url":"https://github.com/acme/app/pull/40","state":"OPEN"}]`)
	done, err := service.MarkStoryDone(runID)
	if err != nil || done.Merged || done.PullRequest == nil || done.PullRequest.Number != 40 || !strings.Contains(done.Summary, "#40 is open, not merged") {
		t.Fatalf("not merged: %+v, %v", done, err)
	}
	if len(gh.ran("project", "item-edit"))+len(gh.ran("issue", "close")) != 0 {
		t.Fatalf("an unmerged pull request moved the story: %v", gh.calls)
	}
	if list := gh.ran("pr", "list"); len(list) != 1 || !strings.Contains(strings.Join(list[0], " "), "--repo github.com/acme/app --head feature/12-login --state all") {
		t.Fatalf("pr list = %v", list)
	}

	// Merged: the status moves to Done and the issue closes as completed.
	gh.answer("pr list", `[{"number":40,"title":"Fix login","url":"https://github.com/acme/app/pull/40","state":"MERGED","mergedAt":"2026-11-02T10:00:00Z"}]`)
	done, err = service.MarkStoryDone(runID)
	if err != nil || !done.Merged || len(done.Notes) != 1 || done.Notes[0] != "Wrote to GitHub issue 12: status → Done (and closed #12 as completed)" {
		t.Fatalf("merged: %+v, %v", done, err)
	}
	if len(gh.ran("issue", "close")) != 1 || len(gh.ran("project", "item-edit")) != 1 {
		t.Fatalf("gh = %v", gh.calls)
	}
	// Asked again, or once the pull request closed it, nothing is written.
	gh.answer("issue view", storyIssueDone)
	done, err = service.MarkStoryDone(runID)
	if err != nil || len(done.Notes) != 1 || !strings.HasPrefix(done.Notes[0], "Left GitHub issue 12 as it was") {
		t.Fatalf("again: %+v, %v", done, err)
	}
	if len(gh.ran("issue", "close")) != 1 || len(gh.ran("project", "item-edit")) != 1 {
		t.Fatalf("a story already done was written to: %v", gh.calls)
	}
	view, err = service.WorkflowRun(runID)
	if err != nil || !eventSaid(view, "status → Done (and closed #12 as completed)") || !eventSaid(view, "#12 is Done already and closed") {
		t.Fatalf("timeline = %+v, %v", view.Events, err)
	}
	if sent := service.notifier.(*notify.Recorder).Sent(); len(sent) != 0 {
		t.Fatalf("Mark done answers where it was asked, not in notifications: %+v", sent)
	}
}

func TestMarkDoneIsOfferedOnlyWhereItCanBeMade(t *testing.T) {
	root := trackerRepository(t, storyTrackerYAML)
	service := workflowTestService(t, root, &workflowRuntime{})
	gh := storyGitHub()
	service.github = gh
	running := storyExecution(t, service, root, "running")
	if _, err := service.MarkStoryDone(running); err == nil || !strings.Contains(err.Error(), "once it has ended") {
		t.Fatalf("a running run: %v", err)
	}
	if view, _ := service.WorkflowRun(running); view.Story == nil || view.Story.MarkDone {
		t.Fatalf("a running run offers Mark done: %+v", view.Story)
	}
	if err := service.store.SaveExecution(context.Background(), storage.Execution{ID: running, WorkflowID: "workflow", TaskID: "task", ProjectID: "project", State: "completed", Title: "Fix login", Branch: "feature/12-login", Round: 1}); err != nil {
		t.Fatal(err)
	}
	// This machine turns updates.done off for the repository.
	off := false
	service.config.File.Repositories = []config.RepositoryConfig{{Name: "app", Path: root, Tracker: &config.TrackerOverride{Updates: config.TrackerUpdatesOverride{Done: &off}}}}
	if _, err := service.MarkStoryDone(running); err == nil || !strings.Contains(err.Error(), "inkuna.yaml turns updates.done off") {
		t.Fatalf("done turned off: %v", err)
	}
	if view, _ := service.WorkflowRun(running); view.Story == nil || view.Story.MarkDone {
		t.Fatalf("a run whose machine turned done off offers it: %+v", view.Story)
	}
	if len(gh.calls) != 0 {
		t.Fatalf("gh = %v", gh.calls)
	}
}

// stubLinearTransport answers Linear's GraphQL operations by name.
type stubLinearTransport struct {
	mu         sync.Mutex
	answers    map[string]string
	operations []string
}

var linearOperation = regexp.MustCompile(`^\s*(?:query|mutation)\s+(\w+)`)

func (stub *stubLinearTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return nil, err
	}
	name := ""
	if match := linearOperation.FindStringSubmatch(body.Query); match != nil {
		name = match[1]
	}
	stub.mu.Lock()
	stub.operations = append(stub.operations, name)
	answer, ok := stub.answers[name]
	stub.mu.Unlock()
	if !ok {
		answer = `{"errors":[{"message":"unexpected operation ` + name + `"}]}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(answer))}, nil
}

func (stub *stubLinearTransport) sent() []string {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]string(nil), stub.operations...)
}

// linearRepository is a checkout whose stories are Linear issues, with the
// tracker settings given.
func linearRepository(t *testing.T, trackerYAML string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	if err := runGit(root, "init"); err != nil {
		t.Fatal(err)
	}
	writeDocs(t, root, map[string]string{
		".inkuna/branching.yaml": "version: 1\ntracker: linear\nbranch: { base: main, pattern: \"{type}/{story}-{slug}\" }\n",
		".inkuna/tracker.yaml":   trackerYAML,
	})
	if err := runGit(root, "add", "."); err != nil {
		t.Fatal(err)
	}
	if err := runGit(root, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	return root
}

func linearService(t *testing.T, root string, stub *stubLinearTransport) *Service {
	t.Helper()
	service := workflowTestService(t, root, &workflowRuntime{})
	service.config.LinearEndpoint, service.config.LinearTimeout = "https://linear.test/graphql", time.Second
	service.http = &http.Client{Transport: stub}
	service.linearKey = "lin_api_test"
	return service
}

func linearStory(cycle string) string {
	return `{"data":{"issue":{"id":"issue-uuid","identifier":"ENG-12","title":"Fix login","description":"","url":"https://linear.app/acme/issue/ENG-12",
"state":{"id":"todo","name":"Todo","type":"unstarted"},"team":{"id":"team-uuid","key":"ENG","activeCycle":{"id":"cycle-7","number":7,"name":""}},
"cycle":` + cycle + `,"projectMilestone":null,"parent":null,"labels":{"nodes":[]},"assignee":null,"attachments":{"nodes":[]}}}}`
}

// With requireCurrentCycle, a story outside its team's active cycle is
// refused before planning spends anything, and so is one Linear cannot be
// asked about.
func TestPlanningRefusesAStoryOutsideTheCurrentCycle(t *testing.T) {
	root := linearRepository(t, "version: 1\nlinear: { requireCurrentCycle: true }\n")
	stub := &stubLinearTransport{answers: map[string]string{"InkunaStory": linearStory(`{"id":"cycle-6","number":6,"name":""}`)}}
	service := linearService(t, root, stub)
	request := WorkflowRequestView{ProjectID: "project", Title: "ENG-12: Fix login", Objective: "Fix the login form", Kind: domain.TaskImplementation}
	if _, err := service.PlanWorkflow(request); err == nil || !strings.Contains(err.Error(), "ENG-12 is in Cycle 6, not its team's current cycle") {
		t.Fatalf("PlanWorkflow = %v", err)
	}
	stub.answers["InkunaStory"] = linearStory("null")
	if _, err := service.PlanWorkflow(request); err == nil || !strings.Contains(err.Error(), "ENG-12 is in no cycle") {
		t.Fatalf("PlanWorkflow = %v", err)
	}
	service.linearKey = ""
	if _, err := service.PlanWorkflow(request); err == nil || !strings.Contains(err.Error(), "Linear could not say whether ENG-12 is") {
		t.Fatalf("without a key: %v", err)
	}
	tasks, err := service.store.DB().QueryContext(context.Background(), `SELECT count(*) FROM tasks`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tasks.Close() }()
	var count int
	if tasks.Next() {
		if err := tasks.Scan(&count); err != nil {
			t.Fatal(err)
		}
	}
	if count != 0 {
		t.Fatalf("a refused story was planned: %d tasks", count)
	}
	service.linearKey = "lin_api_test"
	stub.answers["InkunaStory"] = linearStory(`{"id":"cycle-7","number":7,"name":""}`)
	if _, err := service.PlanWorkflow(request); err != nil {
		t.Fatalf("a story in the current cycle: %v", err)
	}
	// A task without a story has nothing to check.
	before := len(stub.sent())
	if _, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Tidy the README", Kind: domain.TaskDocumentation}); err != nil || len(stub.sent()) != before {
		t.Fatalf("a task without a story: %v, %v", err, stub.sent()[before:])
	}
}

// On Linear, Mark done attaches the merged pull request unless Linear's
// GitHub integration has, and moves the story to the team's Done.
func TestMarkDoneOnLinearAttachesThePullRequest(t *testing.T) {
	root := linearRepository(t, "version: 1\nupdates: { done: true }\n")
	if err := runGit(root, "remote", "add", "origin", "git@github.com:Acme/App.git"); err != nil {
		t.Fatal(err)
	}
	stub := &stubLinearTransport{answers: map[string]string{
		"InkunaStory":  linearStory("null"),
		"InkunaAttach": `{"data":{"attachmentLinkURL":{"success":true}}}`,
		"InkunaStates": `{"data":{"issue":{"id":"issue-uuid","state":{"id":"review","name":"In Review","type":"started"},"team":{"key":"ENG","states":{"nodes":[
{"id":"review","name":"In Review","type":"started","position":2},{"id":"shipped","name":"Shipped","type":"completed","position":4}]}}}}}`,
		"InkunaUpdate": `{"data":{"issueUpdate":{"success":true}}}`,
	}}
	service := linearService(t, root, stub)
	gh := storyGitHub()
	gh.answer("pr list", `[{"number":40,"title":"Fix login","url":"https://github.com/acme/app/pull/40","state":"MERGED"}]`)
	service.github = gh
	ctx := context.Background()
	spec := workflowTestSpec(t, service)
	if err := service.store.SaveExecution(ctx, storage.Execution{ID: "run-linear", WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: "completed", Title: "Fix login", Branch: "feature/ENG-12-login", Round: 1}); err != nil {
		t.Fatal(err)
	}
	if err := service.store.StartRun(ctx, "run-linear-worker", "run-linear", spec.ID, "worker", 1, domain.RuntimeCodex, ""); err != nil {
		t.Fatal(err)
	}
	settings, err := tracker.Resolve(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.saveRunStory(ctx, "run-linear", runStory{ID: "ENG-12", Settings: settings}); err != nil {
		t.Fatal(err)
	}
	done, err := service.MarkStoryDone("run-linear")
	if err != nil || !done.Merged || len(done.Notes) != 2 {
		t.Fatalf("MarkStoryDone = %+v, %v", done, err)
	}
	if !strings.HasPrefix(done.Notes[0], "Wrote to Linear ENG-12: attach https://github.com/acme/app/pull/40") ||
		done.Notes[1] != `Wrote to Linear ENG-12: status → Shipped (team ENG has no state named "Done", so its first completed state)` {
		t.Fatalf("notes = %q", done.Notes)
	}
	if operations := strings.Join(stub.sent(), " "); operations != "InkunaStory InkunaAttach InkunaStates InkunaUpdate" {
		t.Fatalf("Linear was sent %s", operations)
	}
}
