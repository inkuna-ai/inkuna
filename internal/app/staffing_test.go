package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

func TestStaffingRunsWithNoToolsOutsideTheRepositoryAndIsReused(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if strings.HasPrefix(request.RunID, "branching-") {
			return agentruntime.RunResult{}, errors.New("no proposal")
		}
		decision := `{"assignments":[{"stageId":"worker","agent":"implementer","skills":[],"complexity":{"score":6,"rationale":"one package"},"rationale":"writes code"}]}`
		return agentruntime.RunResult{Output: []byte(decision)}, nil
	}
	service := workflowTestService(t, repositoryPath, runtime)
	service.config.Orchestrator.Runtime = domain.RuntimeClaude
	staffingCalls := func() []agentruntime.RunRequest {
		calls := make([]agentruntime.RunRequest, 0)
		for _, request := range started(runtime) {
			if strings.HasPrefix(request.RunID, "orchestrator-") {
				calls = append(calls, request)
			}
		}
		return calls
	}
	request := WorkflowRequestView{ProjectID: "project", Objective: "Add the login page", Kind: domain.TaskImplementation}
	first, err := service.PlanWorkflow(request)
	if err != nil {
		t.Fatal(err)
	}
	calls := staffingCalls()
	if len(calls) != 1 || !first.Orchestrated || first.StaffingReused != "" {
		t.Fatalf("the first plan must ask the orchestrator: %d calls, %+v", len(calls), first)
	}
	if !calls[0].NoTools || calls[0].WorkingDir != filepath.Join(service.dataDir, "orchestrator") {
		t.Fatalf("staffing must run with no tools in the empty directory: no tools %v in %s", calls[0].NoTools, calls[0].WorkingDir)
	}

	// The same request again is a new task, staffed without a call.
	again, err := service.PlanWorkflow(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(staffingCalls()) != 1 || again.Task.ID == first.Task.ID || !again.Orchestrated || again.StaffingReused == "" {
		t.Fatalf("an identical replan must reuse the decision: %d calls, reused %q", len(staffingCalls()), again.StaffingReused)
	}
	for _, call := range again.Usage {
		if call.StepID == "staffing" {
			t.Fatalf("a reused decision is not a call: %+v", again.Usage)
		}
	}
	if worker := again.Workflow.Steps[1]; worker.Agent != "implementer" || worker.Metadata["assignment"] != "writes code" {
		t.Fatalf("the reused decision was not applied: %+v", worker)
	}

	request.Restaff = true
	restaffed, err := service.PlanWorkflow(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(staffingCalls()) != 2 || restaffed.StaffingReused != "" {
		t.Fatalf("re-staffing must ask again: %d calls, reused %q", len(staffingCalls()), restaffed.StaffingReused)
	}

	// With read tools, staffing runs in the repository and answers from its
	// files, so it is asked every time: a kept decision cannot know the code
	// changed.
	service.config.Orchestrator.ReadTools = true
	request.Restaff = false
	root, err := filepath.EvalSymlinks(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	for want := 3; want <= 4; want++ {
		plan, err := service.PlanWorkflow(request)
		if err != nil {
			t.Fatal(err)
		}
		calls = staffingCalls()
		if last := calls[len(calls)-1]; len(calls) != want || last.NoTools || last.WorkingDir != root || plan.StaffingReused != "" {
			t.Fatalf("read tools must give staffing the repository, every time: %d calls, no tools %v in %s, reused %q", len(calls), last.NoTools, last.WorkingDir, plan.StaffingReused)
		}
	}
}
