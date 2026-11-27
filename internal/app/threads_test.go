package app

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

func TestThreadsKeepAnsweredQuestionsAndAnnounceChanges(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if !strings.Contains(string(request.Prompt), "# Human responses") {
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffInputRequired, Summary: "Need a decision", HumanInput: &domain.HumanInputRequest{Question: "Which database?", Options: []string{"sqlite", "postgres"}}})}, nil
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Used the chosen database"})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	var mu sync.Mutex
	announced := map[string]bool{}
	service.emitMu.Lock()
	service.emitter = func(name string, data any) {
		if event, ok := data.(ThreadEvent); ok && name == EventMessages {
			mu.Lock()
			announced[event.TaskID] = true
			mu.Unlock()
		}
	}
	service.emitMu.Unlock()

	run := startSpec(t, service, workflowTestSpec(t, service))
	question := waitForInbox(t, service, 1)[0]

	threads, err := service.Threads()
	if err != nil || len(threads) == 0 {
		t.Fatalf("Threads() = %#v, %v", threads, err)
	}
	first := threads[0]
	if first.PendingInput == nil || first.PendingInput.ID != question.ID || first.PendingInput.Expired || first.RunID != run.ID || first.RunTitle == "" || first.Agent != "researcher" {
		t.Fatalf("the waiting thread should come first with its run and question: %#v", first)
	}
	mu.Lock()
	seen := announced[first.TaskID]
	mu.Unlock()
	if !seen {
		t.Fatalf("no %s event for thread %s: %v", EventMessages, first.TaskID, announced)
	}

	if _, err := service.AnswerInput(question.ID, "sqlite"); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")

	detail, err := service.Thread(first.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.PendingInput != nil || len(detail.Inputs) != 1 || detail.Inputs[0].State != storage.InputAnswered || detail.Inputs[0].Answer != "sqlite" {
		t.Fatalf("the answered question must stay on the thread: %#v", detail.Inputs)
	}
	if len(detail.Messages) != 3 || detail.Messages[2].Text() != "sqlite" || detail.RunID != run.ID || detail.StepID == "" {
		t.Fatalf("thread should hold the request, the pause, and the answer: %#v", detail)
	}
	if _, err := service.Thread("absent"); err == nil {
		t.Fatal("an unknown thread must be an error")
	}
}

func TestPendingQuestionExpiresWhenItsRunIsGoneOrFinished(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Done"})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	gone := service.inputView(storage.InputRequest{ID: "input-1", ExecutionID: "run-gone", State: storage.InputPending})
	if !gone.Expired {
		t.Fatalf("a pending question without a run must be expired: %#v", gone)
	}
	answered := service.inputView(storage.InputRequest{ID: "input-2", ExecutionID: "run-gone", State: storage.InputAnswered})
	if answered.Expired {
		t.Fatalf("only pending questions expire: %#v", answered)
	}

	// A finished run is retained in memory as a record. A question left
	// pending on it is expired, and an answer is refused before delivery.
	run := startSpec(t, service, workflowTestSpec(t, service))
	waitForWorkflowRun(t, service, run.ID, "completed")
	stale := storage.InputRequest{ID: "input-3", ExecutionID: run.ID, StepID: "worker", Agent: "researcher", Question: "Too late?", State: storage.InputPending}
	if err := service.store.SaveInput(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if view := service.inputView(stale); !view.Expired || view.RunTitle == "" {
		t.Fatalf("a pending question on a finished run must be expired: %#v", view)
	}
	if _, err := service.AnswerInput(stale.ID, "yes"); err == nil || !strings.Contains(err.Error(), "no longer active") {
		t.Fatalf("a finished run must not receive input: %v", err)
	}
	if current, err := service.store.Input(context.Background(), stale.ID); err != nil || current.State != storage.InputPending || current.Answer != "" {
		t.Fatalf("a refused answer must not be claimed: %#v %v", current, err)
	}
}
