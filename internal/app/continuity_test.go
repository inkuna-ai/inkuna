package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// askThenFinish is a worker that explores, asks the user, and finishes once
// it has an answer; a resumed turn reports the session it continued.
func askThenFinish(t *testing.T) func(context.Context, agentruntime.RunRequest) (agentruntime.RunResult, error) {
	return func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if request.Session == "" && !strings.Contains(string(request.Prompt), "# Human responses") {
			return agentruntime.RunResult{Session: "session-1", Output: handoffJSON(t, domain.HandoffResult{
				Status: domain.HandoffInputRequired, Summary: "Read the schema and the handlers; the store needs a database",
				Changes: []domain.Change{{Path: "notes.md", Summary: "what I found"}}, HumanInput: &domain.HumanInputRequest{Question: "Which database?"},
			})}, nil
		}
		return agentruntime.RunResult{Session: request.Session, Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Used sqlite"})}, nil
	}
}

func answerTheQuestion(t *testing.T, service *Service, runID string) WorkflowRunView {
	t.Helper()
	question := waitForInbox(t, service, 1)[0]
	if _, err := service.AnswerInput(question.ID, "sqlite"); err != nil {
		t.Fatal(err)
	}
	return waitForWorkflowRun(t, service, runID, "completed")
}

func TestATurnAfterAnAnswerResumesTheSessionWithOnlyTheAnswer(t *testing.T) {
	runtime := &workflowRuntime{resume: true}
	runtime.handle = askThenFinish(t)
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	if completed := answerTheQuestion(t, service, run.ID); completed.Steps[0].Summary != "Used sqlite" {
		t.Fatalf("the step did not finish: %#v", completed.Steps[0])
	}
	requests := started(runtime)
	if len(requests) != 2 || !requests[0].Resumable || requests[0].Session != "" || !requests[1].Resumable || requests[1].Session != "session-1" {
		t.Fatalf("turn 2 must resume turn 1's session: %+v", requests)
	}
	prompt := string(requests[1].Prompt)
	if !strings.HasPrefix(prompt, "# Continue\nYou stopped this step for a decision from the user.") || !strings.Contains(prompt, "# Human responses") || !strings.Contains(prompt, "- Q: Which database?\n  A: sqlite\n") {
		t.Fatalf("turn 2 must carry what it stopped for and the answer: %s", prompt)
	}
	for _, repeated := range []string{"# Agent:", "# Inkuna protocol", "# Objective", "# Previous turn"} {
		if strings.Contains(prompt, repeated) {
			t.Errorf("a resumed turn resent %q, which its session holds: %s", repeated, prompt)
		}
	}
	usage, err := service.RunUsage(run.ID)
	if err != nil || len(usage.Calls) != 2 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	names := make([]string, 0)
	for _, section := range usage.Calls[1].Sections {
		names = append(names, section.Section)
	}
	if strings.Join(names, ",") != "schema,protocol,humanResponses" || usage.Calls[1].PromptBytes != len(prompt)+len(handoffSchema) {
		t.Fatalf("turn 2's sections are %v over %d bytes", names, usage.Calls[1].PromptBytes)
	}
}

func TestAWorkerThatCannotResumeGetsADigestOfItsLastTurn(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = askThenFinish(t)
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	answerTheQuestion(t, service, run.ID)
	requests := started(runtime)
	if len(requests) != 2 || requests[0].Resumable || requests[1].Session != "" {
		t.Fatalf("a runtime that cannot resume keeps and resumes no session: %+v", requests)
	}
	prompt := string(requests[1].Prompt)
	digest := "# Previous turn\nYou already worked on this step and stopped for a decision from the user."
	if !strings.Contains(prompt, digest) || !strings.Contains(prompt, "- Summary: Read the schema and the handlers; the store needs a database\n- Changed: `notes.md`\n") {
		t.Fatalf("turn 2 must get a digest of turn 1: %s", prompt)
	}
	if !strings.Contains(prompt, "# Objective") || !strings.Contains(prompt, "- Q: Which database?\n  A: sqlite\n") {
		t.Fatalf("a turn that starts over gets the whole prompt and the answer: %s", prompt)
	}
	if strings.Contains(string(requests[0].Prompt), "# Previous turn") {
		t.Fatal("the first turn has no previous turn")
	}
}

func TestAResumedTurnIsSentOnlyTheNewPeerReplies(t *testing.T) {
	runtime := &workflowRuntime{resume: true}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		prompt := string(request.Prompt)
		switch {
		case strings.HasPrefix(prompt, "# Agent: reviewer"):
			if request.Resumable {
				t.Errorf("a peer answers in one call and keeps no session")
			}
			reply := "First answer"
			if strings.Contains(prompt, "Second question") {
				reply = "Second answer"
			}
			return agentruntime.RunResult{Session: "peer-session", Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: reply})}, nil
		case request.Session == "":
			return agentruntime.RunResult{Session: "session-1", Output: handoffJSON(t, domain.HandoffResult{Status: HandoffWaitingPeers, Summary: "Need a review", Messages: []domain.PeerMessage{{To: "reviewer", Subject: "One", Content: "First question"}}})}, nil
		case strings.Contains(prompt, "First answer"):
			return agentruntime.RunResult{Session: request.Session, Output: handoffJSON(t, domain.HandoffResult{Status: HandoffWaitingPeers, Summary: "One more", Messages: []domain.PeerMessage{{To: "reviewer", Subject: "Two", Content: "Second question"}}})}, nil
		default:
			return agentruntime.RunResult{Session: request.Session, Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Finished after two reviews"})}, nil
		}
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	if completed := waitForWorkflowRun(t, service, run.ID, "completed"); completed.Steps[0].Summary != "Finished after two reviews" {
		t.Fatalf("the step did not finish: %#v", completed.Steps[0])
	}
	worker := make([]agentruntime.RunRequest, 0)
	for _, request := range started(runtime) {
		if !strings.HasPrefix(string(request.Prompt), "# Agent: reviewer") {
			worker = append(worker, request)
		}
	}
	if len(worker) != 3 || worker[1].Session != "session-1" || worker[2].Session != "session-1" {
		t.Fatalf("each round must resume the worker's session: %+v", worker)
	}
	second, third := string(worker[1].Prompt), string(worker[2].Prompt)
	if !strings.Contains(second, "stopped this step for replies from reviewer") || !strings.Contains(second, "First answer") {
		t.Fatalf("round 1's reply is missing: %s", second)
	}
	if !strings.Contains(third, "Second answer") || strings.Contains(third, "First answer") {
		t.Fatalf("round 2 must send only the new reply: %s", third)
	}
}

func TestACutAnswerOnAResumedTurnIsFlagged(t *testing.T) {
	runtime := &workflowRuntime{resume: true}
	runtime.handle = askThenFinish(t)
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	question := waitForInbox(t, service, 1)[0]
	if _, err := service.AnswerInput(question.ID, strings.Repeat("Use sqlite, and here is why. ", 1000)); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	requests := started(runtime)
	if len(requests) != 2 || requests[1].Session != "session-1" || !strings.Contains(string(requests[1].Prompt), "[truncated: ") {
		t.Fatalf("the resumed turn must carry the answer cut to its budget: %+v", requests)
	}
	usage, err := service.RunUsage(run.ID)
	if err != nil || len(usage.Calls) != 2 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	flagged := false
	for _, section := range usage.Calls[1].Sections {
		flagged = flagged || section.Section == domain.SectionHumanResponses && section.Truncated
	}
	if !flagged {
		t.Fatalf("the cut answer must be flagged on the resumed turn: %+v", usage.Calls[1].Sections)
	}
}

func TestAResumedTurnThatTimedOutOrActedIsNotStartedOver(t *testing.T) {
	for name, fail := range map[string]struct {
		err   error
		acted bool
	}{
		"timed out": {err: context.DeadlineExceeded},
		"acted":     {err: errors.New("claude worker failed: exit status 1"), acted: true},
	} {
		t.Run(name, func(t *testing.T) {
			runtime := &workflowRuntime{resume: true}
			finish := askThenFinish(t)
			runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
				if request.Session != "" {
					return agentruntime.RunResult{Acted: fail.acted}, fail.err
				}
				return finish(ctx, request)
			}
			service := workflowTestService(t, t.TempDir(), runtime)
			run := startSpec(t, service, workflowTestSpec(t, service))
			question := waitForInbox(t, service, 1)[0]
			if _, err := service.AnswerInput(question.ID, "sqlite"); err != nil {
				t.Fatal(err)
			}
			waitForWorkflowRun(t, service, run.ID, "failed")
			if requests := started(runtime); len(requests) != 2 {
				t.Fatalf("a resumed turn that %s must not run again: %d requests", name, len(requests))
			}
		})
	}
}

func TestASessionThatCannotBeResumedStartsTheTurnOver(t *testing.T) {
	runtime := &workflowRuntime{resume: true}
	finish := askThenFinish(t)
	runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if request.Session != "" {
			// The CLI refuses before the model is called, as when the
			// session was deleted.
			return agentruntime.RunResult{}, errors.New("No conversation found with session ID: session-1")
		}
		return finish(ctx, request)
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	completed := answerTheQuestion(t, service, run.ID)
	requests := started(runtime)
	if len(requests) != 3 || requests[1].Session != "session-1" || requests[2].Session != "" {
		t.Fatalf("a failed resume must be followed by a fresh turn: %+v", requests)
	}
	if prompt := string(requests[2].Prompt); !strings.Contains(prompt, "# Previous turn") || !strings.Contains(prompt, "A: sqlite") {
		t.Fatalf("the fresh turn needs the digest and the answer: %s", prompt)
	}
	warned := false
	for _, event := range completed.Events {
		warned = warned || strings.Contains(event.Summary, "session could not be resumed")
	}
	if !warned || completed.Steps[0].Summary != "Used sqlite" {
		t.Fatalf("the run must say the session was lost and still finish: %#v", completed)
	}
}

func TestATurnAfterTheCacheExpiredStartsOverWithTheDigest(t *testing.T) {
	for name, check := range map[string]struct {
		cachedFor time.Duration
		resumes   bool
	}{
		"expired":           {cachedFor: -time.Minute, resumes: false},
		"still cached":      {cachedFor: time.Hour, resumes: true},
		"billed by request": {cachedFor: 0, resumes: true},
	} {
		t.Run(name, func(t *testing.T) {
			runtime := &workflowRuntime{resume: true}
			finish := askThenFinish(t)
			runtime.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
				result, err := finish(ctx, request)
				if check.cachedFor != 0 {
					result.CacheUntil = time.Now().Add(check.cachedFor)
				}
				return result, err
			}
			service := workflowTestService(t, t.TempDir(), runtime)
			run := startSpec(t, service, workflowTestSpec(t, service))
			completed := answerTheQuestion(t, service, run.ID)
			requests := started(runtime)
			if len(requests) != 2 {
				t.Fatalf("expected two turns, got %d", len(requests))
			}
			resumed, prompt := requests[1].Session != "", string(requests[1].Prompt)
			if resumed != check.resumes || strings.Contains(prompt, "# Previous turn") == check.resumes {
				t.Fatalf("resumed %v, want %v: %s", resumed, check.resumes, prompt)
			}
			warned := false
			for _, event := range completed.Events {
				warned = warned || strings.Contains(event.Summary, "prompt cache expired")
			}
			if warned == check.resumes {
				t.Fatalf("the timeline must say why the turn started over, and only then: %#v", completed.Events)
			}
		})
	}
}
