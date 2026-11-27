package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// titledService plans under targetRules in a repository named by
// roundsBranching, with an orchestrator whose title call answers with title,
// and workers that write a file and finish.
func titledService(t *testing.T, title func() (string, error)) (*Service, *workflowRuntime) {
	t.Helper()
	repository := branchingRepository(t, roundsBranching)
	if err := os.WriteFile(filepath.Join(repository, ".inkuna", "rules.yaml"), []byte(targetRules), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "Target rules"}} {
		if err := runGit(repository, args...); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &workflowRuntime{handle: func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		switch {
		case strings.HasPrefix(request.RunID, "title-"):
			answer, err := title()
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			data, _ := json.Marshal(map[string]string{"title": answer})
			return agentruntime.RunResult{Output: data}, nil
		case strings.HasPrefix(request.RunID, "orchestrator-"):
			return agentruntime.RunResult{Output: []byte(`{"assignments":[]}`)}, nil
		}
		if request.Permission == domain.PermissionWorktree {
			if err := os.WriteFile(filepath.Join(request.WorkingDir, "farewell.go"), []byte("package greet\n"), 0o644); err != nil {
				return agentruntime.RunResult{}, err
			}
		}
		return agentruntime.RunResult{Output: handoffOutput(t, domain.HandoffResult{Summary: "Done " + request.RunID})}, nil
	}}
	service := workflowTestService(t, repository, runtime)
	service.config.Orchestrator.Runtime, service.config.Orchestrator.Disabled = domain.RuntimeClaude, false
	return service, runtime
}

func titleCalls(runtime *workflowRuntime) int {
	count := 0
	for _, request := range started(runtime) {
		if strings.HasPrefix(request.RunID, "title-") {
			count++
		}
	}
	return count
}

func noted(notes []string, text string) bool {
	for _, note := range notes {
		if strings.Contains(note, text) {
			return true
		}
	}
	return false
}

const farewellDetails = "Add a farewell function to greet.go\n\nIt should say goodbye to the name it is given, and an empty name gets a friendly default."

func TestAnUntitledTaskIsTitledByTheOrchestratorOrFromItsDetails(t *testing.T) {
	request := WorkflowRequestView{ProjectID: "project", Objective: farewellDetails, Kind: domain.TaskImplementation}
	cases := []struct {
		name, title, branch, note string
		answer                    func() (string, error)
		disabled                  bool
	}{
		{name: "proposed", answer: func() (string, error) { return `"Say goodbye from Greet."`, nil },
			title: "Say goodbye from Greet", branch: "feature/say-goodbye-from-greet", note: "The orchestrator wrote the title"},
		{name: "failed", answer: func() (string, error) { return "", errors.New("claude: timed out") },
			title: "Add a farewell function to greet.go", branch: "feature/add-a-farewell-function-to-greet-go", note: "could not write one: claude: timed out"},
		{name: "refused", answer: func() (string, error) { return strings.Repeat("Farewell ", 20), nil },
			title: "Add a farewell function to greet.go", branch: "feature/add-a-farewell-function-to-greet-go", note: "was refused: the title is"},
		{name: "off", disabled: true, answer: func() (string, error) { return "never asked", nil },
			title: "Add a farewell function to greet.go", branch: "feature/add-a-farewell-function-to-greet-go", note: "the orchestrator is off"},
	}
	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			service, runtime := titledService(t, current.answer)
			service.config.Orchestrator.Disabled = current.disabled
			plan, err := service.PlanWorkflow(request)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Task.Title != current.title || plan.Task.Objective != farewellDetails {
				t.Fatalf("task %q / %q", plan.Task.Title, plan.Task.Objective)
			}
			if plan.Workflow.Branching == nil || plan.Workflow.Branching.Branch != current.branch {
				t.Fatalf("branch %+v, want %s", plan.Workflow.Branching, current.branch)
			}
			if !noted(plan.Notes, current.note) {
				t.Fatalf("notes %v lack %q", plan.Notes, current.note)
			}
			if want := map[bool]int{true: 0, false: 1}[current.disabled]; titleCalls(runtime) != want {
				t.Fatalf("%d title calls, want %d", titleCalls(runtime), want)
			}
			recorded := false
			for _, call := range plan.Usage {
				recorded = recorded || call.StepID == "title"
			}
			if recorded == current.disabled {
				t.Fatalf("the title call is recorded only when made: %+v", plan.Usage)
			}
			stored, err := service.store.Task(context.Background(), plan.Task.ID)
			if err != nil || stored.Title != current.title {
				t.Fatalf("stored task %+v %v", stored, err)
			}
		})
	}
}

func TestAGivenTitleIsCleanedAndNeedsNoModel(t *testing.T) {
	service, runtime := titledService(t, func() (string, error) { return "never asked", nil })
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Title: "  Wave   goodbye\n", Objective: farewellDetails, Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Task.Title != "Wave goodbye" || plan.Workflow.Branching.Branch != "feature/wave-goodbye" || titleCalls(runtime) != 0 || noted(plan.Notes, "title") {
		t.Fatalf("title %q, branch %+v, %d title calls, notes %v", plan.Task.Title, plan.Workflow.Branching, titleCalls(runtime), plan.Notes)
	}
	// A title alone is the task's details too.
	alone, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Title: "Wave goodbye", Kind: domain.TaskImplementation})
	if err != nil || alone.Task.Objective != "Wave goodbye" {
		t.Fatalf("a title alone: %+v %v", alone.Task, err)
	}
	for _, request := range []WorkflowRequestView{
		{ProjectID: "project", Title: strings.Repeat("a", domain.MaxTitleRunes+1), Objective: farewellDetails},
		{ProjectID: "project", Title: "  ", Objective: " "},
	} {
		if _, err := service.PlanWorkflow(request); err == nil || strings.Contains(err.Error(), "invalid contract") {
			t.Errorf("request %+v: %v", request, err)
		}
	}
}

func TestATitleChangedOnApprovalNamesTheRunAndItsBranch(t *testing.T) {
	service, runtime := titledService(t, func() (string, error) { return "Say goodbye from Greet", nil })
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: farewellDetails, Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetPlanTitle(plan.Workflow.ID, strings.Repeat("a", domain.MaxTitleRunes+1)); err == nil {
		t.Fatal("a title over the limit was taken")
	}
	changed, err := service.SetPlanTitle(plan.Workflow.ID, "Wave goodbye")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Task.Title != "Wave goodbye" || changed.Branching == nil || changed.Branching.Branch != "feature/wave-goodbye" || changed.Task.Objective != farewellDetails {
		t.Fatalf("changed %+v", changed)
	}
	// The screen may still hold the plan as it was first shown; approval
	// reads the title and the branch back from the stored plan.
	encoded, err := json.Marshal(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if run.Title != "Wave goodbye" {
		t.Fatalf("run title %q", run.Title)
	}
	done := waitForWorkflowRun(t, service, run.ID, "completed")
	if done.Branch != "feature/wave-goodbye" || done.Title != "Wave goodbye" {
		t.Fatalf("the run's branch %q and title %q", done.Branch, done.Title)
	}
	if _, err := service.SetPlanTitle(plan.Workflow.ID, "Too late"); err == nil || !strings.Contains(err.Error(), "approved already") {
		t.Fatalf("a title changed after approval: %v", err)
	}
	// The worker reads the title above the details.
	if prompt := string(requestOf(t, runtime, run.ID+"-implement").Prompt); !strings.Contains(prompt, "# Objective\nWave goodbye\n\nAdd a farewell function") {
		t.Fatalf("the worker's prompt lacks the title:\n%s", prompt)
	}
}

// boundTitler records the deadline and wall time a title call was given.
type boundTitler struct {
	deadline time.Time
	wall     int
	calls    int
}

func (titler *boundTitler) ProposeTitle(ctx context.Context, _ orchestrator.TitleRequest, options orchestrator.Options, _ agentruntime.EventSink) (string, orchestrator.Call, error) {
	titler.calls++
	titler.deadline, _ = ctx.Deadline()
	titler.wall = options.WallTimeSeconds
	return "Say goodbye", orchestrator.Call{Outcome: domain.OutcomeCompleted}, nil
}

func TestTheTitleCallLeavesPlanningMostOfItsTime(t *testing.T) {
	plan := func(left time.Duration) (*boundTitler, domain.TaskSpec, []string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), left)
		defer cancel()
		titler := &boundTitler{}
		task := domain.TaskSpec{ID: "task-1", Kind: domain.TaskImplementation, Objective: farewellDetails}
		notes, _, err := titleTask(ctx, &task, "", titler, orchestrator.Options{WallTimeSeconds: int(left / time.Second)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return titler, task, notes
	}
	// A long planning deadline still gives the title at most titleTimeout.
	titler, task, _ := plan(5 * time.Minute)
	if titler.calls != 1 || titler.wall != int(titleTimeout/time.Second) || time.Until(titler.deadline) > titleTimeout || task.Title != "Say goodbye" {
		t.Fatalf("a long deadline: wall %d, deadline in %v, title %q", titler.wall, time.Until(titler.deadline), task.Title)
	}
	// A short one gives it at most half of what is left.
	titler, _, _ = plan(30 * time.Second)
	if titler.wall < 1 || titler.wall > 15 || time.Until(titler.deadline) > 15*time.Second {
		t.Fatalf("a short deadline: wall %d, deadline in %v", titler.wall, time.Until(titler.deadline))
	}
	// With almost nothing left, no call is made and the title is derived.
	titler, task, notes := plan(time.Second)
	if titler.calls != 0 || task.Title != "Add a farewell function to greet.go" || !noted(notes, "no time left") {
		t.Fatalf("no time left: %d calls, title %q, notes %v", titler.calls, task.Title, notes)
	}
}

func TestADerivedTitleIsCleanedOrLeftOut(t *testing.T) {
	for details, want := range map[string]string{
		"`go test` fails on main\nMore":       "`go test` fails on main",
		"\"Rename the flag\"..\nMore":         "Rename the flag",
		"\"\"\nOnly quotes on the first line": "",
	} {
		task := domain.TaskSpec{Version: domain.ContractVersion, ID: "task-1", Kind: domain.TaskImplementation, Objective: details, Budget: domain.Budget{WallTimeSeconds: 60}}
		if _, _, err := titleTask(context.Background(), &task, "", nil, orchestrator.Options{}, nil); err != nil {
			t.Fatal(err)
		}
		if task.Title != want {
			t.Errorf("details %q titled %q, want %q", details, task.Title, want)
		}
		if err := domain.ValidateTask(task); err != nil {
			t.Errorf("details %q gave a task that does not validate: %v", details, err)
		}
	}
}

func TestALongOneLineObjectiveNamesABranchGitAccepts(t *testing.T) {
	// #101: a long objective on one line, in a repository without
	// branch.maxLength, made a branch name too long for git to lock.
	service, _ := titledService(t, func() (string, error) { return "never asked", nil })
	service.config.Orchestrator.Disabled = true
	objective := strings.TrimSpace(strings.Repeat("write inkuna out handlers md documenting every http handler in this repository ", 6))
	if len(objective) < 400 {
		t.Fatalf("the objective is %d bytes; the case needs 400", len(objective))
	}
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: objective, Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	branch := plan.Workflow.Branching.Branch
	if !strings.HasPrefix(branch, "feature/write-inkuna-out-handlers-md") || len(branch) > len("feature/")+domain.DerivedTitleRunes {
		t.Fatalf("branch %q (%d bytes)", branch, len(branch))
	}
	if err := runGit(plan.Project.Repository, "check-ref-format", "--branch", branch); err != nil {
		t.Fatalf("git refuses %q: %v", branch, err)
	}
}
