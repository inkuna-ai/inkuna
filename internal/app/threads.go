package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// EventMessages tells the view that a conversation changed. Its payload is a
// ThreadEvent, so the view refreshes only the thread it is showing.
const EventMessages = "inkuna:messages"

// ThreadEvent identifies the conversation behind an inkuna:messages event.
type ThreadEvent struct {
	TaskID    string `json:"taskId"`
	ContextID string `json:"contextId"`
	Agent     string `json:"agent"`
	State     string `json:"state"`
}

// ThreadView is one agent conversation in the Messages list. A thread is an
// A2A task; threads that share a run are grouped by the view.
type ThreadView struct {
	storage.A2AThread
	RunID    string `json:"runId,omitempty"`
	RunTitle string `json:"runTitle,omitempty"`
	Preview  string `json:"preview,omitempty"`
	// PendingInput is the question waiting for the user, when there is one.
	// Replying to the thread answers it.
	PendingInput *InputView `json:"pendingInput,omitempty"`
}

// ThreadDetailView is a full conversation: every message, the artifacts the
// agent returned, and every question raised on it with its answer.
type ThreadDetailView struct {
	ThreadView
	StepID    string         `json:"stepId,omitempty"`
	Messages  []a2a.Message  `json:"messages"`
	Artifacts []a2a.Artifact `json:"artifacts"`
	Inputs    []InputView    `json:"inputs"`
	// Feedback is what a round sent the step back with, and its replies,
	// when the thread is a round's target's task.
	Feedback []storage.FeedbackItem `json:"feedback,omitempty"`
}

// Threads lists agent conversations. Threads waiting for the user come
// first, then the most recently active.
func (service *Service) Threads() ([]ThreadView, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	ctx := service.context()
	threads, err := service.store.A2AThreads(ctx, 0)
	if err != nil {
		return nil, err
	}
	taskIDs := make([]string, 0, len(threads))
	contextIDs := make([]string, 0, len(threads))
	for _, thread := range threads {
		taskIDs = append(taskIDs, thread.TaskID)
		contextIDs = append(contextIDs, thread.ContextID)
	}
	// Both lookups are keyed by the listed threads, so neither a long backlog
	// of questions nor a long run history can hide what belongs to a thread.
	waiting, err := service.store.OldestPendingInputs(ctx, taskIDs)
	if err != nil {
		return nil, err
	}
	runs, err := service.store.LatestExecutionsByTask(ctx, contextIDs)
	if err != nil {
		return nil, err
	}
	views := make([]ThreadView, 0, len(threads))
	for _, thread := range threads {
		view := ThreadView{A2AThread: thread, Preview: truncateSummary(thread.LastMessage.Text(), 160)}
		if run, ok := runs[thread.ContextID]; ok {
			view.RunID, view.RunTitle = run.ID, run.Title
		}
		if input, ok := waiting[thread.TaskID]; ok {
			inputView := service.inputView(input)
			view.PendingInput = &inputView
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool {
		return views[i].awaitingReply() && !views[j].awaitingReply()
	})
	return views, nil
}

// Thread returns one conversation in full.
func (service *Service) Thread(taskID string) (ThreadDetailView, error) {
	if err := service.ready(); err != nil {
		return ThreadDetailView{}, err
	}
	ctx := service.context()
	task, err := service.store.A2ATask(ctx, taskID)
	if err != nil {
		return ThreadDetailView{}, err
	}
	messages, err := service.store.A2AMessages(ctx, taskID)
	if err != nil {
		return ThreadDetailView{}, err
	}
	inputs, err := service.store.TaskInputs(ctx, taskID)
	if err != nil {
		return ThreadDetailView{}, err
	}
	runs, err := service.store.LatestExecutionsByTask(ctx, []string{task.ContextID})
	if err != nil {
		return ThreadDetailView{}, err
	}
	detail := ThreadDetailView{
		ThreadView: ThreadView{A2AThread: storage.A2AThread{TaskID: task.ID, ContextID: task.ContextID, Agent: task.Agent, State: string(task.Status.State), MessageCount: len(messages), UpdatedAt: task.Status.Timestamp}},
		Messages:   messages,
		Artifacts:  task.Artifacts,
		Inputs:     make([]InputView, 0, len(inputs)),
	}
	if detail.Artifacts == nil {
		detail.Artifacts = []a2a.Artifact{}
	}
	if len(messages) > 0 {
		detail.LastMessage = messages[len(messages)-1]
		detail.Preview = truncateSummary(detail.LastMessage.Text(), 160)
	}
	detail.StepID, _ = task.Metadata["stepId"].(string)
	if detail.Feedback, err = service.taskFeedback(ctx, task.Metadata, messages); err != nil {
		return ThreadDetailView{}, err
	}
	if run, ok := runs[task.ContextID]; ok {
		detail.RunID, detail.RunTitle = run.ID, run.Title
	}
	for _, input := range inputs {
		view := service.inputView(input)
		detail.Inputs = append(detail.Inputs, view)
		if input.State == storage.InputPending && detail.PendingInput == nil {
			pending := view
			detail.PendingInput = &pending
		}
		if detail.StepID == "" {
			detail.StepID = input.StepID
		}
	}
	return detail, nil
}

func (view ThreadView) awaitingReply() bool {
	return view.PendingInput != nil && !view.PendingInput.Expired
}

// inputView adds the run title and marks a pending question that can no
// longer be delivered: its run is gone, or is kept only as a finished record.
// A review can be answered while its run is paused at the gate, which after
// a restart only storage knows.
func (service *Service) inputView(input storage.InputRequest) InputView {
	view := InputView{InputRequest: input, Permissions: input.Permissions}
	// The run's view is only read under the lock that guards its updates.
	state := ""
	service.runsMu.RLock()
	if run := service.runs[input.ExecutionID]; run != nil {
		view.RunTitle, state = run.view.Title, run.view.State
	}
	service.runsMu.RUnlock()
	if state == "" && input.Review && input.State == storage.InputPending {
		if execution, err := service.store.Execution(service.context(), input.ExecutionID); err == nil {
			view.RunTitle, state = execution.Title, execution.State
		}
	}
	deliverable := state != "" && !terminalRunState(state)
	if input.Review {
		deliverable = state == storage.Reviewing
	}
	view.Expired = input.State == storage.InputPending && !deliverable
	// Offering it is a convenience, so a failed read offers nothing.
	if input.State == storage.InputPending && !input.Review && !view.Expired {
		view.PreviousAnswer, _ = service.store.PreviousAnswer(service.context(), input)
	}
	return view
}

// runFinished reports whether a retained run has reached a terminal state.
func (service *Service) runFinished(run *liveExecution) bool {
	service.runsMu.RLock()
	defer service.runsMu.RUnlock()
	return terminalRunState(run.view.State)
}

// notifyThread forwards a bus change to the view.
func (service *Service) notifyThread(task a2a.Task) {
	service.emit(EventMessages, ThreadEvent{TaskID: task.ID, ContextID: task.ContextID, Agent: task.Agent, State: string(task.Status.State)})
}

// taskFeedback is what a round sent a target back with, and its replies,
// for the A2A task that round's run of the step was sent as: the task names
// the run and the step, and its first message the round.
func (service *Service) taskFeedback(ctx context.Context, metadata map[string]any, messages []a2a.Message) ([]storage.FeedbackItem, error) {
	executionID, _ := metadata["executionId"].(string)
	stepID, _ := metadata["stepId"].(string)
	if executionID == "" || stepID == "" || len(messages) == 0 {
		return nil, nil
	}
	round := metadataInt(messages[0].Metadata["round"])
	if round < 2 {
		return nil, nil
	}
	items, err := service.store.Feedback(ctx, executionID, round)
	if err != nil {
		return nil, fmt.Errorf("load the feedback of round %d: %w", round, err)
	}
	mine := make([]storage.FeedbackItem, 0, len(items))
	for _, item := range items {
		if item.Target == stepID {
			mine = append(mine, item)
		}
	}
	return mine, nil
}

// metadataInt reads a number from A2A metadata, which holds an int while a
// task is in memory and a float64 once it was read back from JSON.
func metadataInt(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	}
	return 0
}
