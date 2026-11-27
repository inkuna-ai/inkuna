package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/notify"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/tracker"
)

// The tracker changes agents ask for in their handoffs
// (docs/mcp-and-trackers.md#changes-an-agent-asks-for-155). Go checks each
// one when its step ends and keeps it, refused or pending; approving the run
// at a review gate applies the pending changes of the steps before the
// gate, apart from the run, and one that fails can be retried. Nothing an
// agent asks for reaches a tracker before a person approves it.

// TrackerChangeView is one change an agent asked for, as the run and its
// review bar show it.
type TrackerChangeView struct {
	ID     string `json:"id"`
	Round  int    `json:"round"`
	StepID string `json:"stepId"`
	// Tracker is the tracker the run was approved with.
	Tracker string             `json:"tracker,omitempty"`
	Kind    domain.TrackerKind `json:"kind"`
	// Issue is the tracker's id of the issue the change acts on: for a
	// create, its parent, empty for an issue of its own.
	Issue string `json:"issue,omitempty"`
	// Summary says what the change would do or did, and Change is all it
	// says, such as a comment's whole body.
	Summary string               `json:"summary"`
	Change  domain.TrackerChange `json:"change"`
	// State is refused, pending, discarded, applying, applied, or failed,
	// and Reason says why it was refused, discarded, or failed.
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// URL is what applying it made: a comment, or a new issue.
	URL string `json:"url,omitempty"`
	// Retry is set on a failed change the run can try again.
	Retry     bool   `json:"retry,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

// changeMarker identifies a change in what it makes, so a retry edits the
// comment or returns the issue it made rather than making another.
func changeMarker(changeID string) string { return "<!-- inkuna-change:" + changeID + " -->" }

// trackerGrant is what a step may ask of the tracker when it starts: the
// kinds it was planned with that the settings the run was approved under,
// narrowed by the repository's and this machine's as they are now, still
// allow. The second value names the kinds the settings took away since.
func (service *Service) trackerGrant(run *liveExecution, step domain.WorkflowStep) (*domain.TrackerGrant, []domain.TrackerKind) {
	if len(step.TrackerKinds) == 0 || step.Kind != domain.StepAgent {
		return nil, nil
	}
	service.runsMu.RLock()
	var story *runStory
	if run.story != nil {
		kept := *run.story
		story = &kept
	}
	root := run.project.Repository
	service.runsMu.RUnlock()
	if story == nil {
		return nil, step.TrackerKinds
	}
	settings := service.storySettings(root, *story)
	var kinds, dropped []domain.TrackerKind
	for _, kind := range step.TrackerKinds {
		if settings.Effective.Allows(kind) {
			kinds = append(kinds, kind)
		} else {
			dropped = append(dropped, kind)
		}
	}
	if len(kinds) == 0 {
		return nil, dropped
	}
	return &domain.TrackerGrant{Tracker: string(settings.Tracker), Kinds: kinds, Story: story.ID}, dropped
}

// allowedTrackerKinds are the kinds of tracker change a project's agents
// may ask for on this machine, which planning grants steps from. Settings
// that cannot be read allow none.
func (service *Service) allowedTrackerKinds(root string) []domain.TrackerKind {
	settings, err := tracker.Resolve(root, service.trackerOverride(root))
	if err != nil {
		log.Printf("inkuna: tracker settings of %s: %v", root, err)
	}
	return settings.Effective.Writes
}

// keepTrackerChanges checks the tracker changes a step asked for when it
// ended and keeps each, pending or refused with the reason; both are on the
// step's timeline, and a refused one is never sent. A handoff that asks for
// more than domain.MaxTrackerChanges has the rest refused together.
func (service *Service) keepTrackerChanges(ctx context.Context, run *liveExecution, step domain.WorkflowStep, stepRunID string, round int, asked []domain.TrackerChange) {
	service.runsMu.RLock()
	executionID, root := run.view.ID, run.project.Repository
	grant := run.contexts[step.ID].Tracker
	var story *runStory
	if run.story != nil {
		kept := *run.story
		story = &kept
	}
	a2aTaskID := ""
	for _, view := range run.view.Steps {
		if view.ID == step.ID {
			a2aTaskID = view.A2ATaskID
		}
	}
	service.runsMu.RUnlock()
	var granted []domain.TrackerKind
	if grant != nil {
		granted = grant.Kinds
	}
	settings, storyID := tracker.Settings{Note: "the run was approved without tracker settings, so it writes nothing"}, ""
	if story != nil {
		settings, storyID = service.storySettings(root, *story), story.ID
	}
	// A refusal is kept as one either way; only reading the tracker waits.
	ctx, cancel := context.WithTimeout(ctx, storyUpdateTimeout)
	defer cancel()
	// Check reads and never writes, so the guard has nothing to record.
	guard, err := service.guardTracker(ctx, root, settings, storyID, func(domain.TrackerWrite) {})
	if err != nil {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, "The step's tracker changes could not be checked, so none is kept: "+err.Error()))
		return
	}
	kept := min(len(asked), domain.MaxTrackerChanges)
	rows := make([]storage.TrackerChange, 0, kept)
	for index, change := range asked[:kept] {
		checked, err := guard.Check(ctx, change, granted)
		row := storage.TrackerChange{
			ExecutionID: executionID, ID: newID("change"), Round: round, StepID: step.ID, StepRunID: stepRunID, A2ATaskID: a2aTaskID,
			Position: index, Kind: checked.Change.Kind, Issue: checked.Issue, State: storage.TrackerChangePending,
		}
		if err != nil {
			row.State, row.Reason = storage.TrackerChangeRefused, err.Error()
			// A refused change names the issue as the agent did.
			if row.Issue == "" {
				row.Issue = firstNonEmpty(checked.Change.Issue, checked.Change.Parent)
			}
		}
		if row.Change, err = json.Marshal(checked); err != nil {
			row.State, row.Reason, row.Change = storage.TrackerChangeRefused, "the change could not be encoded: "+err.Error(), json.RawMessage(`{}`)
		}
		rows = append(rows, row)
	}
	if err := service.store.AddTrackerChanges(context.WithoutCancel(ctx), rows); err != nil {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, "The step's tracker changes could not be kept, so none of them will be made: "+err.Error()))
		return
	}
	for _, row := range rows {
		view := service.trackerChangeView(row, string(settings.Tracker), false)
		kind, summary := domain.EventLifecycle, "Kept for review at the next review gate: "+describeTrackerChange(view)
		if row.State == storage.TrackerChangeRefused {
			kind, summary = domain.EventWarning, fmt.Sprintf("Refused a tracker change, which is not sent: %s: %s", describeTrackerChange(view), row.Reason)
		}
		event := syntheticEvent(stepRunID, 0, kind, summary)
		event.Payload = map[string]any{"trackerChange": view}
		_ = service.appendRunEvent(executionID, event)
	}
	if extra := len(asked) - kept; extra > 0 {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning,
			fmt.Sprintf("The step asked for %d more tracker changes than the %d one handoff may ask for; they are refused and not kept", extra, domain.MaxTrackerChanges)))
	}
}

// describeTrackerChange says what a change is for and would do, as the
// timeline says a tracker write: the tracker and issue, then the change.
func describeTrackerChange(view TrackerChangeView) string {
	// The tracker is unknown for a run whose settings could not be read.
	where := strings.TrimSpace(trackerName(view.Tracker) + " " + view.Issue)
	if where == "" {
		return view.Summary
	}
	return fmt.Sprintf("%s (%s)", where, view.Summary)
}

// trackerChangeView decodes a kept change for the view. retry says whether
// the run can try a failed change again now.
func (service *Service) trackerChangeView(row storage.TrackerChange, trackerName string, retry bool) TrackerChangeView {
	var checked tracker.Checked
	_ = json.Unmarshal(row.Change, &checked)
	if checked.Change.Kind == "" {
		checked.Change.Kind = row.Kind
	}
	if checked.Issue == "" && row.State != storage.TrackerChangeRefused {
		checked.Issue = row.Issue
	}
	return TrackerChangeView{
		ID: row.ID, Round: row.Round, StepID: row.StepID, Tracker: trackerName, Kind: row.Kind, Issue: row.Issue,
		Summary: tracker.DescribeChange(checked), Change: checked.Change, State: row.State, Reason: row.Reason, URL: row.URL,
		Retry: retry && row.State == storage.TrackerChangeFailed, UpdatedAt: row.UpdatedAt.Format(time.RFC3339Nano),
	}
}

// discardTrackerChanges discards the pending changes of the steps a new
// round runs again: each may ask again, and what it asked before was asked
// of work it is about to redo.
func (service *Service) discardTrackerChanges(ctx context.Context, executionID string, restart map[string]bool, round int) {
	steps := make([]string, 0, len(restart))
	for id := range restart {
		steps = append(steps, id)
	}
	changes, err := service.store.TrackerChanges(ctx, executionID)
	if err != nil {
		log.Printf("inkuna: load the tracker changes of run %s: %v", executionID, err)
		return
	}
	reason := fmt.Sprintf("its step runs again in round %d, and asks again for what it still needs", round)
	discarded, err := service.store.DiscardTrackerChanges(ctx, executionID, steps, reason)
	if err != nil {
		log.Printf("inkuna: discard the tracker changes of run %s: %v", executionID, err)
		return
	}
	if discarded == 0 {
		return
	}
	for _, change := range changes {
		if change.State == storage.TrackerChangePending && restart[change.StepID] {
			_ = service.appendRunEvent(executionID, syntheticEvent(change.StepRunID, 0, domain.EventLifecycle,
				fmt.Sprintf("Discarded a tracker change that was waiting for review, since %s", reason)))
		}
	}
}

// gatedTrackerChanges are the pending changes an approval at gates applies:
// those of the steps before any of them.
func gatedTrackerChanges(plan domain.WorkflowSpec, changes []storage.TrackerChange, gates []string) []storage.TrackerChange {
	before := make(map[string]bool)
	steps := make(map[string]domain.WorkflowStep, len(plan.Steps))
	for _, step := range plan.Steps {
		steps[step.ID] = step
	}
	queue := make([]string, 0, len(gates))
	for _, gate := range gates {
		queue = append(queue, steps[gate].DependsOn...)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if before[id] {
			continue
		}
		before[id] = true
		queue = append(queue, steps[id].DependsOn...)
	}
	gated := make([]storage.TrackerChange, 0)
	for _, change := range changes {
		if change.State == storage.TrackerChangePending && before[change.StepID] {
			gated = append(gated, change)
		}
	}
	return gated
}

// applyApproved claims the pending changes the approval at gates covers
// and applies them apart from the run, which has moved on, so a slow
// tracker never holds the approval. A change that fails does not undo the
// approval or stop the others.
func (service *Service) applyApproved(ctx context.Context, execution storage.Execution, plan domain.WorkflowSpec, gates []storage.InputRequest) {
	if len(gates) == 0 {
		return
	}
	changes, err := service.store.TrackerChanges(ctx, execution.ID)
	if err != nil {
		_ = service.appendRunEvent(execution.ID, syntheticEvent(stepRunID(execution.ID, execution.Round, gates[0].StepID), 0, domain.EventWarning,
			"The tracker changes this approval covers could not be read, so they are still waiting: "+err.Error()))
		return
	}
	ids := make([]string, 0)
	gateIDs := make([]string, 0, len(gates))
	for _, gate := range gates {
		gateIDs = append(gateIDs, gate.StepID)
	}
	for _, change := range gatedTrackerChanges(plan, changes, gateIDs) {
		ids = append(ids, change.ID)
	}
	if len(ids) == 0 {
		return
	}
	claimed, err := service.store.MoveTrackerChanges(ctx, execution.ID, ids, storage.TrackerChangePending, storage.TrackerChangeApplying, "")
	if err != nil {
		_ = service.appendRunEvent(execution.ID, syntheticEvent(stepRunID(execution.ID, execution.Round, gateIDs[0]), 0, domain.EventWarning,
			"The tracker changes this approval covers could not be claimed, so they are still waiting: "+err.Error()))
		return
	}
	if len(claimed) == 0 {
		return
	}
	service.runsWG.Add(1)
	go func() {
		defer service.runsWG.Done()
		service.applyTrackerChanges(execution.ID, claimed)
	}()
}

// applyTrackerChanges makes claimed changes in the order their steps asked
// for them, one batch at a time, so two approvals of one run never race on
// the same issue, and records how each went.
func (service *Service) applyTrackerChanges(executionID string, ids []string) []TrackerChangeView {
	service.trackerApplyMu.Lock()
	defer service.trackerApplyMu.Unlock()
	ctx, cancel := context.WithTimeout(service.context(), time.Duration(len(ids))*storyUpdateTimeout)
	defer cancel()
	// A change that fails before it reaches the tracker is told to its step
	// as one the tracker refused is.
	trackerName := ""
	fail := func(id, reason string) {
		if err := service.store.FinishTrackerChange(context.WithoutCancel(ctx), executionID, id, storage.TrackerChangeFailed, reason, ""); err != nil {
			log.Printf("inkuna: record tracker change %s of run %s: %v", id, executionID, err)
			return
		}
		change, err := service.store.TrackerChange(context.WithoutCancel(ctx), executionID, id)
		if err != nil {
			log.Printf("inkuna: tell the step of run %s that tracker change %s failed: %v", executionID, id, err)
			return
		}
		if change.State == storage.TrackerChangeFailed {
			service.tellTrackerFailure(executionID, change, service.trackerChangeView(change, trackerName, false))
		}
	}
	execution, err := service.store.Execution(ctx, executionID)
	var story *runStory
	if err == nil {
		story, err = service.loadRunStory(ctx, executionID)
	}
	if story != nil {
		trackerName = string(story.Settings.Tracker)
	}
	var project storage.Project
	if err == nil {
		project, err = service.workableProject(ctx, execution.ProjectID)
	}
	if err == nil && story == nil {
		err = errors.New("the run was approved without tracker settings, so it writes nothing")
	}
	if err != nil {
		for _, id := range ids {
			fail(id, "Inkuna could not open the run's tracker: "+err.Error())
		}
		service.emit("inkuna:runs", executionID)
		return nil
	}
	settings := service.storySettings(project.Repository, *story)
	// The guard records each write under the step run that asked for it.
	var current storage.TrackerChange
	var last domain.TrackerWrite
	guard, err := service.guardTracker(ctx, project.Repository, settings, story.ID, func(write domain.TrackerWrite) {
		last = write
		service.recordTrackerWrite(executionID, current.StepRunID, write)
	})
	if err != nil {
		for _, id := range ids {
			fail(id, "Inkuna could not open the run's tracker: "+err.Error())
		}
		service.emit("inkuna:runs", executionID)
		return nil
	}
	views := make([]TrackerChangeView, 0, len(ids))
	for _, id := range ids {
		change, err := service.store.TrackerChange(ctx, executionID, id)
		if err != nil {
			fail(id, "Inkuna could not read the change: "+err.Error())
			continue
		}
		var checked tracker.Checked
		if err := json.Unmarshal(change.Change, &checked); err != nil {
			fail(id, "Inkuna could not read the change: "+err.Error())
			continue
		}
		current, last = change, domain.TrackerWrite{}
		url, err := guard.Apply(ctx, checked, changeMarker(change.ID))
		state, reason := storage.TrackerChangeApplied, ""
		if last.Outcome == domain.TrackerUnchanged {
			reason = last.Reason
		}
		if err != nil {
			state, reason = storage.TrackerChangeFailed, firstNonEmpty(last.Reason, err.Error())
		}
		if err := service.store.FinishTrackerChange(context.WithoutCancel(ctx), executionID, id, state, reason, url); err != nil {
			log.Printf("inkuna: record tracker change %s of run %s: %v", id, executionID, err)
		}
		change.State, change.Reason, change.URL = state, reason, url
		view := service.trackerChangeView(change, string(settings.Tracker), settings.Effective.Allows(change.Kind))
		views = append(views, view)
		if state == storage.TrackerChangeFailed {
			service.tellTrackerFailure(executionID, change, view)
		}
	}
	service.emit("inkuna:runs", executionID)
	return views
}

// tellTrackerFailure says on the asking step's thread, and in a desktop
// notification, that a change it asked for failed: the approval that made
// it is over, and the person may not be watching the run.
func (service *Service) tellTrackerFailure(executionID string, change storage.TrackerChange, view TrackerChangeView) {
	text := fmt.Sprintf("Inkuna could not make the tracker change this step asked for, %s: %s. Retry it from the run.", describeTrackerChange(view), view.Reason)
	if change.A2ATaskID != "" {
		message := a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.TextPart(text)}, Metadata: map[string]any{"from": "inkuna", "trackerChange": change.ID, "executionId": executionID}}
		if err := service.store.AddA2AMessage(context.WithoutCancel(service.context()), change.A2ATaskID, message); err != nil {
			log.Printf("inkuna: tell step %s of run %s that a tracker change failed: %v", change.StepID, executionID, err)
		} else {
			service.emit(EventMessages, ThreadEvent{TaskID: change.A2ATaskID})
		}
	}
	_ = service.dependenciesFor(executionID).notifier.Notify(notify.Notification{
		Title: "A tracker change failed", Body: describeTrackerChange(view) + ": " + view.Reason, Kind: "tracker-failed", Reference: executionID,
	})
}

// RetryTrackerChange tries a failed tracker change again, within what the
// run was approved under and the repository allows now, and returns how it
// went. A comment or an issue the change made before is found by its marker
// rather than made twice.
func (service *Service) RetryTrackerChange(runID, changeID string) (TrackerChangeView, error) {
	if err := service.ready(); err != nil {
		return TrackerChangeView{}, err
	}
	ctx := service.context()
	change, err := service.store.TrackerChange(ctx, runID, changeID)
	if errors.Is(err, domain.ErrNotFound) {
		return TrackerChangeView{}, fmt.Errorf("run %s has no tracker change %s", runID, changeID)
	}
	if err != nil {
		return TrackerChangeView{}, err
	}
	if change.State != storage.TrackerChangeFailed {
		return TrackerChangeView{}, fmt.Errorf("this change is %s; only a change that failed can be retried", change.State)
	}
	claimed, err := service.store.MoveTrackerChanges(ctx, runID, []string{changeID}, storage.TrackerChangeFailed, storage.TrackerChangeApplying, "")
	if err != nil {
		return TrackerChangeView{}, err
	}
	if len(claimed) == 0 {
		return TrackerChangeView{}, errors.New("this change is being retried already; reload the run")
	}
	views := service.applyTrackerChanges(runID, claimed)
	if len(views) == 1 {
		return views[0], nil
	}
	// It could not be applied at all; how it stands is what was recorded.
	change, err = service.store.TrackerChange(ctx, runID, changeID)
	if err != nil {
		return TrackerChangeView{}, err
	}
	return service.trackerChangeView(change, "", false), nil
}

// runTrackerChanges lists what a run's agents asked of its tracker, for
// its detail. A failed change can be retried while the settings still allow
// its kind.
func (service *Service) runTrackerChanges(ctx context.Context, runID, projectID string) []TrackerChangeView {
	changes, err := service.store.TrackerChanges(ctx, runID)
	if err != nil || len(changes) == 0 {
		return nil
	}
	story, _ := service.loadRunStory(ctx, runID)
	settings := tracker.Settings{}
	if story != nil {
		settings = story.Settings
		if project, err := service.store.Project(ctx, projectID); err == nil {
			settings = service.storySettings(project.Repository, *story)
		}
	}
	views := make([]TrackerChangeView, 0, len(changes))
	for _, change := range changes {
		views = append(views, service.trackerChangeView(change, string(settings.Tracker), settings.Effective.Allows(change.Kind)))
	}
	return views
}

// pendingReviewChanges are the changes approving the run at its paused
// gates would apply, for the review bar to list beside Approve.
func (service *Service) pendingReviewChanges(ctx context.Context, runID string, plan domain.WorkflowSpec, gates []storage.InputRequest) []TrackerChangeView {
	changes, err := service.store.TrackerChanges(ctx, runID)
	if err != nil || len(changes) == 0 {
		return []TrackerChangeView{}
	}
	trackerName := ""
	if story, err := service.loadRunStory(ctx, runID); err == nil && story != nil {
		trackerName = string(story.Settings.Tracker)
	}
	ids := make([]string, 0, len(gates))
	for _, gate := range gates {
		ids = append(ids, gate.StepID)
	}
	views := make([]TrackerChangeView, 0)
	for _, change := range gatedTrackerChanges(plan, changes, ids) {
		views = append(views, service.trackerChangeView(change, trackerName, false))
	}
	return views
}

// writeTrackerProtocol tells a step what it may ask of the tracker and how:
// the kinds it was granted and the fields each takes, which issue its story
// is, and that nothing it asks for is made before a person approves it.
func writeTrackerProtocol(builder *strings.Builder, grant domain.TrackerGrant) {
	name := trackerName(grant.Tracker)
	if grant.Tracker == string(branching.TrackerGitHub) {
		name = "GitHub issues"
	}
	uses := map[domain.TrackerKind]string{
		domain.TrackerStatus:  "status {issue, status} moves an issue to one of its tracker's statuses",
		domain.TrackerComment: "comment {issue, body} comments on an issue",
		domain.TrackerEdit:    "edit {issue, and any of title, description, addLabels, removeLabels, estimate, priority, assignee, cycle, milestone} changes those fields of an issue",
		domain.TrackerCreate:  "create {title, and any of description, addLabels, parent} creates an issue, or a sub-issue of parent",
	}
	kinds := make([]string, 0, len(grant.Kinds))
	for _, kind := range grant.Kinds {
		kinds = append(kinds, uses[kind])
	}
	builder.WriteString("You may ask for changes to the repository's issue tracker, " + name + ", under trackerChanges, each with its kind: " + strings.Join(kinds, "; ") + ". Leave out every field a change does not set.\n")
	issues := "Name an issue by its id"
	switch grant.Tracker {
	case string(branching.TrackerLinear):
		issues += " (such as MAD-12)"
	case string(branching.TrackerGitHub):
		issues += " (its number, such as 12)"
	}
	if grant.Story != "" {
		issues += `, or as "story" for this run's story, ` + grant.Story
	} else {
		issues += "; this run has no story"
	}
	builder.WriteString(issues + ".")
	switch {
	case grant.Tracker == string(branching.TrackerGitHub) && slices.Contains(grant.Kinds, domain.TrackerEdit):
		builder.WriteString(" GitHub issues have no estimate, priority, or cycle.")
	case grant.Tracker == string(branching.TrackerLinear) && slices.Contains(grant.Kinds, domain.TrackerEdit):
		builder.WriteString(" A Linear issue's milestone belongs to its project and cannot be set; priority is 0 (none) to 4 (low).")
	}
	fmt.Fprintf(builder, " Ask for at most %d changes, with a body or description of at most %d characters and a title of at most %d.\n", domain.MaxTrackerChanges, domain.MaxTrackerText, domain.MaxTrackerTitle)
	builder.WriteString("Inkuna checks each change when this step ends and makes none of them until a person approves the run at its review gate; a change it refuses is dropped, with the reason on the run. Change the tracker only this way.\n")
}

// trackerChangesSchema is the handoff property a step granted kinds may
// fill: the kinds it was granted, and the fields those kinds take.
func trackerChangesSchema(kinds []domain.TrackerKind) map[string]any {
	text := map[string]any{"type": "string"}
	labels := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	number := map[string]any{"type": "integer"}
	fields := map[domain.TrackerKind]map[string]any{
		domain.TrackerStatus:  {"issue": text, "status": text},
		domain.TrackerComment: {"issue": text, "body": text},
		domain.TrackerEdit: {"issue": text, "title": text, "description": text, "addLabels": labels, "removeLabels": labels,
			"estimate": number, "priority": number, "assignee": text, "cycle": text, "milestone": text},
		domain.TrackerCreate: {"title": text, "description": text, "addLabels": labels, "parent": text},
	}
	enum := make([]string, 0, len(kinds))
	properties := map[string]any{}
	for _, kind := range kinds {
		enum = append(enum, string(kind))
		for name, schema := range fields[kind] {
			properties[name] = schema
		}
	}
	properties["kind"] = map[string]any{"type": "string", "enum": enum}
	return map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"kind"}, "properties": properties}}
}

// handoffSchemaFor is the handoff schema a step is sent: with
// trackerChanges when it may ask for changes, so a step granted none
// cannot produce the field at all.
func handoffSchemaFor(grant *domain.TrackerGrant) string {
	if grant == nil || len(grant.Kinds) == 0 {
		return handoffSchema
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(handoffSchema), &schema); err != nil {
		panic(fmt.Sprintf("the handoff schema does not decode: %v", err))
	}
	schema["properties"].(map[string]any)["trackerChanges"] = trackerChangesSchema(grant.Kinds)
	encoded, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("the handoff schema does not encode: %v", err))
	}
	return string(encoded)
}
