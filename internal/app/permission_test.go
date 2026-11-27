package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// deniedUntilGranted scripts a worker whose command is refused until the run
// carries a grant for it, and that also hits a denial no approval can lift.
func deniedUntilGranted(t *testing.T) *workflowRuntime {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if len(request.Grants) == 0 {
			return agentruntime.RunResult{
				Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Could not run the linter"}),
				Denials: []domain.PermissionDenial{
					{Tool: "Bash", Command: "golangci-lint run", Detail: "golangci-lint run"},
					{Tool: "Write", Detail: "/etc/hosts"},
				},
			}, nil
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Linted cleanly"})}, nil
	}
	return runtime
}

func TestDeniedPermissionIsApprovedFromTheInboxAndRetried(t *testing.T) {
	runtime := deniedUntilGranted(t)
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	request := waitForInbox(t, service, 1)[0]
	waitForWorkflowRun(t, service, run.ID, "waiting")
	if !reflect.DeepEqual(request.Permissions, []storage.PermissionAsk{{Command: "golangci-lint run"}}) || request.Stage {
		t.Fatalf("the request should offer the command and nothing ungrantable: %#v", request.Permissions)
	}
	if want := `researcher was denied a command while working on "Inspect". Allow it for this run?`; request.Question != want {
		t.Fatalf("the question is one readable line, with the command listed apart:\n got %q\nwant %q", request.Question, want)
	}
	if !reflect.DeepEqual(request.Options, []string{permissionAllow, permissionDeny}) || !strings.Contains(request.Context, "this run only") {
		t.Fatalf("unexpected permission request %#v", request)
	}
	if answered, err := service.AnswerInput(request.ID, permissionAllow); err != nil || answered.State != storage.InputAnswered {
		t.Fatalf("approval failed: %#v %v", answered, err)
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.Steps[0].Summary != "Linted cleanly" {
		t.Fatalf("the step did not retry with the grant: %#v", completed.Steps[0])
	}
	requests := started(runtime)
	if len(requests) != 2 || len(requests[0].Grants) != 0 || !reflect.DeepEqual(requests[1].Grants, []domain.PermissionGrant{{Command: "golangci-lint run"}}) {
		t.Fatalf("the retry should carry exactly the approved grant: %#v", requests)
	}
	if !strings.Contains(string(requests[1].Prompt), "Approved for this run") || !strings.Contains(string(requests[1].Prompt), "• run `golangci-lint run`") {
		t.Fatalf("the retry was not told about the approval: %s", requests[1].Prompt)
	}
	var explained bool
	for _, event := range completed.Events {
		explained = explained || strings.Contains(event.Summary, "Write was denied and cannot be approved")
	}
	if !explained {
		t.Fatalf("the ungrantable denial was not explained in the run log: %#v", completed.Events)
	}
}

func TestDeclinedPermissionFinishesWithTheDeniedTurn(t *testing.T) {
	runtime := deniedUntilGranted(t)
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	request := waitForInbox(t, service, 1)[0]
	if _, err := service.AnswerInput(request.ID, permissionDeny); err != nil {
		t.Fatal(err)
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.Steps[0].Summary != "Could not run the linter" {
		t.Fatalf("a declined step should keep the denied turn's handoff: %#v", completed.Steps[0])
	}
	if requests := started(runtime); len(requests) != 1 {
		t.Fatalf("declining must not pay for another turn, got %d", len(requests))
	}
}

func TestStillDeniedPermissionIsNotAskedTwice(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, _ agentruntime.RunRequest) (agentruntime.RunResult, error) {
		return agentruntime.RunResult{
			Output:  handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Still blocked"}),
			Denials: []domain.PermissionDenial{{Tool: "Bash", Command: "make lint"}},
		}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	request := waitForInbox(t, service, 1)[0]
	if _, err := service.AnswerInput(request.ID, permissionAllow); err != nil {
		t.Fatal(err)
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.Steps[0].Summary != "Still blocked" || len(started(runtime)) != 2 {
		t.Fatalf("a grant that did not help must end the step, not loop: %#v", completed.Steps[0])
	}
	if inbox, err := service.Inbox(); err != nil || len(inbox) != 0 {
		t.Fatalf("the same permission was asked again: %#v %v", inbox, err)
	}
}

func TestPermissionHeadingCountsWhatWasDenied(t *testing.T) {
	command, tool := storage.PermissionAsk{Command: "ls"}, storage.PermissionAsk{Tool: "WebFetch"}
	for _, test := range []struct {
		asks []storage.PermissionAsk
		want string
	}{
		{[]storage.PermissionAsk{command}, `researcher was denied a command while working on "Plan the work". Allow it for this run?`},
		{[]storage.PermissionAsk{command, command, command, command}, `researcher was denied 4 commands while working on "Plan the work". Allow them for this run?`},
		{[]storage.PermissionAsk{tool}, `researcher was denied a tool while working on "Plan the work". Allow it for this run?`},
		{[]storage.PermissionAsk{command, command, tool}, `researcher was denied 2 commands and a tool while working on "Plan the work". Allow them for this run?`},
	} {
		if got := permissionHeading("researcher", "Plan the work", test.asks); got != test.want {
			t.Errorf("got %q, want %q", got, test.want)
		}
	}
}
