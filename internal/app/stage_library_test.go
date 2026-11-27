package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// libraryService is a workflow test service for an empty repository, which
// plans with the built-in rules, reading global stage libraries from the
// returned directory.
func libraryService(t *testing.T) (*Service, string) {
	t.Helper()
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repositoryPath, &workflowRuntime{})
	directory := t.TempDir()
	service.config.StageDirectories = []string{directory}
	return service, directory
}

// A plan records the definitions its rule expanded, and approval refuses
// it once one changes: edited in its global file, or resolving elsewhere
// because the file is gone. Put back, the plan approves again.
func TestApprovalRefusesAPlanWhoseSharedStageChangedSincePlanning(t *testing.T) {
	service, directory := libraryService(t)
	file := filepath.Join(directory, "gates.yaml")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reviewed := "version: 2\nstages:\n  approval:\n    kind: approval\n    review: true\n"
	write(reviewed)
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Explain the runner", Kind: domain.TaskResearch, RuleName: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	if digests := plan.Workflow.StageDigests; len(digests) != 2 || digests["approval"] == "" || digests["profile"] == "" {
		t.Fatalf("stage digests = %v", digests)
	}
	approval := plan.Workflow.Steps[len(plan.Workflow.Steps)-1]
	if approval.ID != "approval" || !approval.Review {
		t.Fatalf("the global approval stage was not used: %+v", approval)
	}
	encoded := string(mustJSON(t, plan.Workflow))
	if _, err := service.ReviseWorkflow(encoded); err != nil {
		t.Fatalf("an unchanged library must approve: %v", err)
	}

	write("version: 2\nstages:\n  approval:\n    kind: approval\n")
	if _, err := service.ReviseWorkflow(encoded); err == nil || !strings.Contains(err.Error(), `shared stage "approval" in `+file+` changed after the plan was made`) || !strings.Contains(err.Error(), "plan the task again") {
		t.Fatalf("an edited global stage must refuse approval: %v", err)
	}
	if _, err := service.StartWorkflow(encoded); err == nil || !strings.Contains(err.Error(), "changed after the plan was made") {
		t.Fatalf("starting must refuse it too: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReviseWorkflow(encoded); err == nil || !strings.Contains(err.Error(), `built-in shared stage "approval" changed`) {
		t.Fatalf("a stage now resolving to the built-in one must refuse approval: %v", err)
	}
	write(reviewed)
	if _, err := service.ReviseWorkflow(encoded); err != nil {
		t.Fatalf("the library put back must approve: %v", err)
	}
}

// The built-in rules record the built-in definitions they use, and a plan
// from before plans recorded any is not held to them.
func TestTheBuiltInRulesRecordTheBuiltInDefinitionsTheyUse(t *testing.T) {
	service, _ := libraryService(t)
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Connect the runner", Kind: domain.TaskImplementation})
	if err != nil {
		t.Fatal(err)
	}
	if digests := plan.Workflow.StageDigests; len(digests) != 4 || digests["verify-and-approve"] == "" {
		t.Fatalf("stage digests = %v", digests)
	}
	stored, err := service.store.Workflow(service.context(), plan.Workflow.ID)
	if err != nil || len(stored.StageDigests) != 4 {
		t.Fatalf("the digests were not saved with the plan: %v, %v", stored.StageDigests, err)
	}
	stored.StageDigests = nil
	if err := service.store.SaveWorkflow(service.context(), "planned", stored); err != nil {
		t.Fatal(err)
	}
	if err := service.checkStagesUnchanged(service.context(), stored); err != nil {
		t.Fatalf("a plan without digests is not checked: %v", err)
	}
}

// A broken global library file is shown with the configuration, but does
// not block saving it; a plan that uses the broken definition is refused.
func TestLibraryProblemsAreShownWithoutBlockingSettings(t *testing.T) {
	service, directory := libraryService(t)
	broken := filepath.Join(directory, "broken.yaml")
	if err := os.WriteFile(broken, []byte("version: 2\nstages:\n  verify:\n    kind: evaluation\n    command: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "a.yaml"), []byte("version: 2\nstages:\n  profile:\n    kind: script\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "b.yaml"), []byte("version: 2\nstages:\n  profile:\n    kind: script\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := service.Configuration()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.LibraryProblems) != 2 || view.LibraryProblems[0].File != broken || !strings.Contains(view.LibraryProblems[1].Message, "also defined in") {
		t.Fatalf("library problems = %+v", view.LibraryProblems)
	}
	if len(view.StageDirectories) != 1 || view.StageDirectories[0] != directory {
		t.Fatalf("stage directories = %v", view.StageDirectories)
	}
	encoded, err := json.Marshal(view.File)
	if err != nil {
		t.Fatal(err)
	}
	if problems, err := service.ValidateConfiguration(string(encoded)); err != nil || len(problems) != 0 {
		t.Fatalf("library problems must not block a save: %+v, %v", problems, err)
	}
	if _, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Explain the runner", Kind: domain.TaskResearch}); err == nil || !strings.Contains(err.Error(), "more than one global stage library file") {
		t.Fatalf("a plan using an ambiguous name must be refused: %v", err)
	}
}

// Where each step came from is the plan's: an approval that sends another
// origin back is refused, naming the step, and the run's steps show what
// the plan recorded.
func TestAnApprovalKeepsWhereThePlannedStepsCameFrom(t *testing.T) {
	service, _ := libraryService(t)
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Explain the runner", Kind: domain.TaskResearch, RuleName: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	last := len(plan.Workflow.Steps) - 1
	if from := plan.Workflow.Steps[last].From; from == nil || from.Stage != "approval" || from.Scope != "builtin" {
		t.Fatalf("approval step from = %+v", from)
	}
	stored, err := service.store.Workflow(service.context(), plan.Workflow.ID)
	if err != nil || !reflect.DeepEqual(stored.Steps[last].From, plan.Workflow.Steps[last].From) {
		t.Fatalf("the plan was not saved with where its steps came from: %+v, %v", stored.Steps[last].From, err)
	}
	tamper := func(index int, from *domain.StepOrigin) string {
		edited := plan.Workflow
		edited.Steps = append([]domain.WorkflowStep(nil), plan.Workflow.Steps...)
		edited.Steps[index].From = from
		return string(mustJSON(t, edited))
	}
	for index, from := range map[int]*domain.StepOrigin{
		last: {Stage: "approval", Scope: "global", File: "/elsewhere.yaml"},
		1:    {Stage: "worker", Scope: "repository"},
	} {
		step := plan.Workflow.Steps[index].ID
		if _, err := service.ReviseWorkflow(tamper(index, from)); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("approved step %q", step)) {
			t.Fatalf("an approval changing where %s came from must be refused: %v", step, err)
		}
	}
	run, err := service.StartWorkflow(string(mustJSON(t, plan.Workflow)))
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range run.Steps {
		if step.ID == "approval" && (step.From == nil || step.From.Scope != "builtin") {
			t.Fatalf("the run's approval step from = %+v", step.From)
		}
		if step.ID == "worker" && step.From != nil {
			t.Fatalf("an inline stage came from %+v", step.From)
		}
	}
}
