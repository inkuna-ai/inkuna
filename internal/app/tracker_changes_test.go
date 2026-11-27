package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/notify"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// changeAskingRules pause an implementation at a review gate after a stage that
// may ask for every kind of tracker change.
const changeAskingRules = `version: 1
rules: []
default:
  stages:
    - id: implement
      title: Implement
      kind: agent
      agent: auto
      permission: worktree-write
      tracker: [status, comment, edit, create]
    - id: approval
      kind: approval
      review: true
      dependsOn: [implement]
`

// askingTracker allows every kind but edit, on the Roadmap project.
const askingTracker = "version: 1\nwrites: [status, comment, create]\ngithub: { project: { owner: acme, number: 3 } }\n"

// askedChanges is what the implement step asks of the tracker: three it
// may, one of a kind it was not granted, and one for an issue that does not
// exist.
func askedChanges() []domain.TrackerChange {
	title, label := "Index events by circle", "backend"
	return []domain.TrackerChange{
		{Kind: domain.TrackerComment, Issue: "story", Body: "The migration runs in about 4 s."},
		{Kind: domain.TrackerStatus, Issue: "story", Status: "in progress"},
		{Kind: domain.TrackerCreate, Title: &title, Parent: "story"},
		{Kind: domain.TrackerEdit, Issue: "story", AddLabels: []string{label}},
		{Kind: domain.TrackerComment, Issue: "99", Body: "Related."},
	}
}

// changesGitHub answers what checking and applying askedChanges takes:
// issue 12 exists and 99 does not.
func changesGitHub() *scriptedGitHub {
	gh := storyGitHub()
	gh.answers["issue view"] = func(args []string) (string, error) {
		if args[2] == "99" {
			return "", errors.New("GraphQL: Could not resolve to an issue with the number of 99.")
		}
		return storyIssue, nil
	}
	// Comments are listed as pages, the newest issues as one list.
	gh.answers["api --hostname"] = func(args []string) (string, error) {
		if strings.HasSuffix(args[len(args)-1], "/comments") {
			return `[[]]`, nil
		}
		return `[]`, nil
	}
	gh.answer("issue list", `[]`)
	gh.answer("issue create", "https://github.com/acme/app/issues/41\n")
	return gh
}

// askingRun plans and starts an implementation under changeAskingRules whose
// implement step asks for changes, and waits for it to pause at its gate.
func askingRun(t *testing.T, changes func() []domain.TrackerChange) (*Service, *workflowRuntime, *scriptedGitHub, WorkflowRunView) {
	t.Helper()
	repository := branchingRepository(t, roundsBranching)
	for name, contents := range map[string]string{"rules.yaml": changeAskingRules, "tracker.yaml": askingTracker} {
		if err := os.WriteFile(filepath.Join(repository, ".inkuna", name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "Rules"}, {"remote", "add", "origin", "git@github.com:Acme/App.git"}} {
		if err := runGit(repository, args...); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &workflowRuntime{handle: func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if err := os.WriteFile(filepath.Join(request.WorkingDir, "CHANGE-"+request.RunID+".md"), []byte("change"), 0o644); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Indexed the events", TrackerChanges: changes()})}, nil
	}}
	service := workflowTestService(t, repository, runtime)
	gh := changesGitHub()
	service.github = gh
	plan, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Index the events for #12", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(plan.Notes, func(note string) bool { return strings.Contains(note, "asks for edit tracker changes") }) {
		t.Fatalf("the plan says edit is not allowed: %v", plan.Notes)
	}
	run = waitForWorkflowRun(t, service, run.ID, storage.Reviewing)
	if err := service.settle(run.ID); err != nil {
		t.Fatal(err)
	}
	return service, runtime, gh, run
}

func changesByState(changes []TrackerChangeView) map[string][]TrackerChangeView {
	byState := make(map[string][]TrackerChangeView)
	for _, change := range changes {
		byState[change.State] = append(byState[change.State], change)
	}
	return byState
}

func detail(t *testing.T, service *Service, runID string) WorkflowRunView {
	t.Helper()
	run, err := service.WorkflowRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestAnAgentsTrackerChangesWaitForApprovalAndAreMadeOnApprove(t *testing.T) {
	service, runtime, gh, run := askingRun(t, askedChanges)

	// The step was told what it may ask for, in its schema and its prompt.
	request := startedRuns(runtime)[0]
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(request.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if kinds := schema.Properties["trackerChanges"].Items.Properties["kind"].Enum; !slices.Equal(kinds, []string{"status", "comment", "create"}) {
		t.Fatalf("schema kinds = %v", kinds)
	}
	for _, part := range []string{"GitHub issues, under trackerChanges", `or as "story" for this run's story, 12`, "makes none of them until a person approves the run"} {
		if !strings.Contains(string(request.Prompt), part) {
			t.Fatalf("the prompt lacks %q:\n%s", part, request.Prompt)
		}
	}

	// Checked when the step ended: kept, refused with the reason, and none
	// of them sent.
	view := detail(t, service, run.ID)
	byState := changesByState(view.TrackerChanges)
	if len(byState[storage.TrackerChangePending]) != 3 || len(byState[storage.TrackerChangeRefused]) != 2 {
		// A check or a save that failed says why only on the timeline.
		var warnings []string
		for _, event := range view.Events {
			if event.Kind == domain.EventWarning {
				warnings = append(warnings, event.Summary)
			}
		}
		t.Fatalf("changes = %+v; warnings: %q", view.TrackerChanges, warnings)
	}
	refused := byState[storage.TrackerChangeRefused]
	if !strings.Contains(refused[0].Reason, "may not ask for edit changes; it may ask for status, comment, create only") {
		t.Fatalf("the edit is refused for its kind: %+v", refused[0])
	}
	if refused[1].Issue != "99" || !strings.Contains(refused[1].Reason, "Could not resolve to an issue") {
		t.Fatalf("the comment on 99 is refused: %+v", refused[1])
	}
	if status := byState[storage.TrackerChangePending][1]; status.Change.Status != "In progress" || status.Summary != "status → In progress" {
		t.Fatalf("the status is named as the project names it: %+v", status)
	}
	if !eventSaid(view, "Refused a tracker change, which is not sent: GitHub issue 99 (comment: Related.)") || !eventSaid(view, "Kept for review at the next review gate: GitHub issue 12 (comment: The migration") {
		t.Fatalf("the timeline says what was kept and refused: %+v", view.Events)
	}
	for _, words := range [][]string{{"issue", "comment"}, {"issue", "create"}, {"project", "item-edit"}} {
		if len(gh.ran(words...)) > 0 {
			t.Fatalf("gh %v ran before approval", words)
		}
	}
	// The stored handoff does not carry them on.
	results, err := service.store.StepResults(context.Background(), run.ID)
	if err != nil || results["implement"].Summary == "" || len(results["implement"].TrackerChanges) > 0 {
		t.Fatalf("the stored handoff = %+v, %v", results["implement"], err)
	}
	// Nor does the turn's handoff artifact, which a remote advisor may read.
	artifacts, err := service.store.TaskArtifacts(context.Background(), run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	handoffs := 0
	for _, record := range artifacts {
		if record.Kind != "handoff" || record.StepID != "implement" {
			continue
		}
		handoffs++
		reader, err := service.artifacts.Open(record.Digest)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil || !strings.Contains(string(data), "Indexed the events") || strings.Contains(string(data), "trackerChanges") || strings.Contains(string(data), "The migration runs") {
			t.Fatalf("the handoff artifact = %s, %v", data, err)
		}
	}
	if handoffs == 0 {
		t.Fatal("the step stored no handoff artifact")
	}

	// The review bar lists what Approve makes.
	review, err := service.RunReview(run.ID)
	if err != nil || len(review.TrackerChanges) != 3 || review.TrackerChanges[0].Kind != domain.TrackerComment {
		t.Fatalf("review = %+v, %v", review.TrackerChanges, err)
	}

	// The project's status field refuses the write; the rest are made.
	gh.mu.Lock()
	gh.answers["project item-edit"] = func([]string) (string, error) { return "", errors.New("GraphQL: the item is locked") }
	gh.mu.Unlock()
	if _, err := service.ApproveReview(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	service.runsWG.Wait()
	view = detail(t, service, run.ID)
	byState = changesByState(view.TrackerChanges)
	if len(byState[storage.TrackerChangeApplied]) != 2 || len(byState[storage.TrackerChangeFailed]) != 1 {
		t.Fatalf("changes = %+v", view.TrackerChanges)
	}
	comment := gh.ran("issue", "comment")
	if len(comment) != 1 || !strings.Contains(comment[0][len(comment[0])-1], "<!-- inkuna-change:"+byState[storage.TrackerChangeApplied][0].ID+" -->") {
		t.Fatalf("the comment carries its marker: %v", comment)
	}
	created := gh.ran("issue", "create")
	if len(created) != 1 || !slices.Contains(created[0], "--parent") || byState[storage.TrackerChangeApplied][1].URL != "https://github.com/acme/app/issues/41" {
		t.Fatalf("issue create = %v, %+v", created, byState[storage.TrackerChangeApplied])
	}
	failed := byState[storage.TrackerChangeFailed][0]
	if failed.Kind != domain.TrackerStatus || !strings.Contains(failed.Reason, "the item is locked") || !failed.Retry {
		t.Fatalf("failed = %+v", failed)
	}
	if !eventSaid(view, "Wrote to GitHub issue 12: comment: The migration") || !eventSaid(view, "Writing to GitHub issue 12 failed (status → In progress)") {
		t.Fatalf("the timeline says what was made and what failed: %+v", view.Events)
	}
	// The step's thread hears of the failure, and so does the desktop.
	stored, err := service.store.TrackerChange(context.Background(), run.ID, failed.ID)
	if err != nil || stored.A2ATaskID == "" {
		t.Fatalf("the change keeps its step's thread: %+v, %v", stored, err)
	}
	thread, err := service.Thread(stored.A2ATaskID)
	if err != nil || !strings.Contains(thread.LastMessage.Text(), "could not make the tracker change this step asked for, GitHub issue 12 (status → In progress)") {
		t.Fatalf("thread = %+v, %v", thread.LastMessage, err)
	}
	notified := slices.ContainsFunc(service.notifier.(*notify.Recorder).Sent(), func(sent notify.Notification) bool {
		return sent.Kind == "tracker-failed" && sent.Reference == run.ID
	})
	if !notified {
		t.Fatal("a failed change is a notification")
	}

	// Retried once the tracker takes it, it is made, and only then.
	gh.answer("project item-edit", `{}`)
	retried, err := service.RetryTrackerChange(run.ID, failed.ID)
	if err != nil || retried.State != storage.TrackerChangeApplied || retried.Retry {
		t.Fatalf("retry = %+v, %v", retried, err)
	}
	if _, err := service.RetryTrackerChange(run.ID, failed.ID); err == nil || !strings.Contains(err.Error(), "only a change that failed can be retried") {
		t.Fatalf("an applied change is not retried: %v", err)
	}
	if _, err := service.RetryTrackerChange(run.ID, refused[0].ID); err == nil {
		t.Fatal("a refused change is never sent")
	}
}

// A change claimed for a run whose tracker cannot be opened fails before it
// reaches the tracker, and its step hears so as it would of a refusal.
func TestAChangeThatCannotReachTheTrackerIsToldToItsStep(t *testing.T) {
	root := trackerRepository(t, askingTracker)
	service := workflowTestService(t, root, &workflowRuntime{})
	ctx := context.Background()
	spec := workflowTestSpec(t, service)
	if err := service.store.SaveExecution(ctx, executionRecord(WorkflowRunView{ID: "run-1", WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: "completed"})); err != nil {
		t.Fatal(err)
	}
	task := a2a.Task{ID: "a2a-implement", ContextID: spec.TaskID, Agent: "implementer", Status: a2a.TaskStatus{State: a2a.StateCompleted},
		History: []a2a.Message{{MessageID: "m1", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.TextPart("Implement it")}}}}
	if err := service.store.SaveA2ATask(ctx, task); err != nil {
		t.Fatal(err)
	}
	// The run keeps no tracker settings, so nothing can be written for it.
	if err := service.store.AddTrackerChanges(ctx, []storage.TrackerChange{{
		ExecutionID: "run-1", ID: "change-1", Round: 1, StepID: "implement", StepRunID: "run-1-implement", A2ATaskID: task.ID,
		Kind: domain.TrackerComment, Issue: "12", Change: json.RawMessage(`{"change":{"kind":"comment","body":"Done"},"issue":"12"}`), State: storage.TrackerChangeApplying,
	}}); err != nil {
		t.Fatal(err)
	}
	service.applyTrackerChanges("run-1", []string{"change-1"})
	change, err := service.store.TrackerChange(ctx, "run-1", "change-1")
	if err != nil || change.State != storage.TrackerChangeFailed || !strings.Contains(change.Reason, "could not open the run's tracker") {
		t.Fatalf("change = %+v, %v", change, err)
	}
	thread, err := service.Thread(task.ID)
	if err != nil || !strings.Contains(thread.LastMessage.Text(), "could not make the tracker change this step asked for, 12 (comment: Done)") {
		t.Fatalf("thread = %+v, %v", thread.LastMessage, err)
	}
	if !slices.ContainsFunc(service.notifier.(*notify.Recorder).Sent(), func(sent notify.Notification) bool { return sent.Kind == "tracker-failed" }) {
		t.Fatal("the failure is a notification")
	}
}

func TestRequestingChangesDiscardsWhatTheTargetAsked(t *testing.T) {
	var mu sync.Mutex
	rounds := 0
	service, _, _, run := askingRun(t, func() []domain.TrackerChange {
		mu.Lock()
		defer mu.Unlock()
		rounds++
		body := "Round " + string(rune('0'+rounds))
		return []domain.TrackerChange{{Kind: domain.TrackerComment, Issue: "story", Body: body}}
	})
	if _, err := service.RequestChanges(run.ID, FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "implement", Text: "Index by date too"}}}); err != nil {
		t.Fatal(err)
	}
	waitForRound(t, service, run.ID, 2)
	waitForWorkflowRun(t, service, run.ID, storage.Reviewing)
	if err := service.settle(run.ID); err != nil {
		t.Fatal(err)
	}
	changes := detail(t, service, run.ID).TrackerChanges
	if len(changes) != 2 || changes[0].State != storage.TrackerChangeDiscarded || !strings.Contains(changes[0].Reason, "runs again in round 2") ||
		changes[1].State != storage.TrackerChangePending || changes[1].Change.Body != "Round 2" {
		t.Fatalf("changes = %+v", changes)
	}
	review, err := service.RunReview(run.ID)
	if err != nil || len(review.TrackerChanges) != 1 || review.TrackerChanges[0].Change.Body != "Round 2" {
		t.Fatalf("review = %+v, %v", review.TrackerChanges, err)
	}
}

func TestAStepGrantedNoKindsGetsTheSchemaWithoutTrackerChanges(t *testing.T) {
	if handoffSchemaFor(nil) != handoffSchema || strings.Contains(handoffSchema, "trackerChanges") {
		t.Fatal("a step granted nothing cannot produce trackerChanges")
	}
	schema := handoffSchemaFor(&domain.TrackerGrant{Tracker: "linear", Kinds: []domain.TrackerKind{domain.TrackerComment}})
	var decoded struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]any `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schema), &decoded); err != nil {
		t.Fatal(err)
	}
	fields := make([]string, 0)
	for name := range decoded.Properties["trackerChanges"].Items.Properties {
		fields = append(fields, name)
	}
	slices.Sort(fields)
	if !slices.Equal(fields, []string{"body", "issue", "kind"}) {
		t.Fatalf("a comment-only step's change takes %v", fields)
	}
}

func TestTheTrackerProtocolSaysWhatTheStepMayAskFor(t *testing.T) {
	var builder strings.Builder
	writeTrackerProtocol(&builder, domain.TrackerGrant{Tracker: "linear", Kinds: []domain.TrackerKind{domain.TrackerEdit, domain.TrackerCreate}})
	text := builder.String()
	for _, part := range []string{"Linear, under trackerChanges", "edit {issue, and any of title", "create {title", "(such as MAD-12); this run has no story", "milestone belongs to its project", "at most 20 changes"} {
		if !strings.Contains(text, part) {
			t.Fatalf("the protocol lacks %q:\n%s", part, text)
		}
	}
	if strings.Contains(text, "status {issue") {
		t.Fatalf("the protocol offers a kind the step was not granted:\n%s", text)
	}
}

func TestARunWithoutAStoryKeepsTheSettingsItsChangesNeed(t *testing.T) {
	root := trackerRepository(t, askingTracker)
	service := workflowTestService(t, root, &workflowRuntime{})
	task := domain.TaskSpec{ID: "task", Objective: "Tidy the readme"}
	asking := domain.WorkflowSpec{Steps: []domain.WorkflowStep{{ID: "implement", Kind: domain.StepAgent, TrackerKinds: []domain.TrackerKind{domain.TrackerComment}}}}
	story := service.approvedStory(root, task, asking)
	if story == nil || story.ID != "" || !story.Settings.Effective.Allows(domain.TrackerComment) {
		t.Fatalf("story = %+v", story)
	}
	// A run whose steps ask for nothing keeps nothing without a story.
	if quiet := service.approvedStory(root, task, domain.WorkflowSpec{Steps: []domain.WorkflowStep{{ID: "implement", Kind: domain.StepAgent}}}); quiet != nil {
		t.Fatalf("story = %+v", quiet)
	}
	// Such a run has no story to show, update, or mark done.
	spec := workflowTestSpec(t, service)
	view := WorkflowRunView{ID: "run-1", WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: "completed", Branch: "feature/x"}
	if err := service.store.SaveExecution(context.Background(), executionRecord(view)); err != nil {
		t.Fatal(err)
	}
	if err := service.saveRunStory(context.Background(), "run-1", *story); err != nil {
		t.Fatal(err)
	}
	service.attachStory(&view)
	if view.Story != nil {
		t.Fatalf("a run without a story shows one: %+v", view.Story)
	}
	if _, err := service.MarkStoryDone("run-1"); err == nil || !strings.Contains(err.Error(), "no story") {
		t.Fatalf("Mark done = %v", err)
	}
}
