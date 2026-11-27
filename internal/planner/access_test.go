package planner

import (
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

func accessRegistry() agent.Registry {
	return agent.NewRegistry(agent.BuiltIn(), []agent.Definition{{
		Name: "advisor", Description: "remote", Permission: domain.PermissionReadOnly, Source: agent.SourceRemote,
	}})
}

func plannedReadOnly() domain.WorkflowStep {
	return domain.WorkflowStep{
		ID: "reproduce", Kind: domain.StepAgent, Agent: "researcher", Runtime: domain.RuntimeClaude, Permission: domain.PermissionReadOnly,
		Commands: [][]string{{"go", "test", "./..."}}, Budget: domain.Budget{WallTimeSeconds: 60},
		Complexity: &domain.ComplexityAssessment{Score: 4, Rationale: "planned"},
	}
}

func edited(step domain.WorkflowStep, agentName string, access Access) Edit {
	return Edit{Agent: agentName, Runtime: step.Runtime, Score: step.Complexity.Score, Access: &access}
}

func fieldEdit(step domain.WorkflowStep, field string) (domain.AccessEdit, bool) {
	for _, edit := range step.AccessEdits {
		if edit.Field == field {
			return edit, true
		}
	}
	return domain.AccessEdit{}, false
}

func TestApprovalWidensAStepOnlyForAWritableAgent(t *testing.T) {
	registry := accessRegistry()
	widen := Access{Permission: domain.PermissionWorktree, Commands: [][]string{{"go", "test", "./..."}}}

	refused := plannedReadOnly()
	if err := Restaff(&refused, edited(refused, "researcher", widen), registry, nil, nil, nil); err == nil || !strings.Contains(err.Error(), `agent "researcher" is read-only`) {
		t.Fatalf("a read-only agent must not be put on a worktree-write step, got %v", err)
	}
	remote := plannedReadOnly()
	if err := Restaff(&remote, edited(remote, "advisor", widen), registry, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "must stay read-only") {
		t.Fatalf("a remote advisor must stay read-only, got %v", err)
	}

	step := plannedReadOnly()
	if err := Restaff(&step, edited(step, "implementer", widen), registry, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if step.Permission != domain.PermissionWorktree || !slices.Equal(step.AllowedPaths, []string{"."}) {
		t.Fatalf("a widened step writes, and like a rule's stage gets the whole repository when given no paths: %+v", step)
	}
	permission, ok := fieldEdit(step, domain.AccessPermission)
	if !ok || !permission.Widens || permission.Note != "Permission set to worktree-write at approval; the plan said read-only." {
		t.Fatalf("the widening must be recorded against the plan: %+v", step.AccessEdits)
	}
	if paths, ok := fieldEdit(step, domain.AccessAllowedPaths); !ok || !paths.Widens || !strings.Contains(paths.Note, "the plan had none") {
		t.Fatalf("paths a read-only step never had widen it: %+v", step.AccessEdits)
	}
	if _, ok := fieldEdit(step, domain.AccessCommands); ok {
		t.Fatalf("unchanged commands are not an edit: %+v", step.AccessEdits)
	}
}

func TestApprovalNarrowsAStepAndDropsItsPaths(t *testing.T) {
	step := plannedReadOnly()
	step.Agent, step.Permission, step.AllowedPaths = "implementer", domain.PermissionWorktree, []string{"internal/"}
	if err := Restaff(&step, edited(step, "implementer", Access{Permission: domain.PermissionReadOnly, AllowedPaths: []string{"internal/"}}), accessRegistry(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if step.Permission != domain.PermissionReadOnly || step.AllowedPaths != nil || step.Commands != nil {
		t.Fatalf("a narrowed step keeps no paths, and its removed commands are gone: %+v", step)
	}
	if len(step.AccessEdits) != 2 || slices.ContainsFunc(step.AccessEdits, func(edit domain.AccessEdit) bool { return edit.Widens }) {
		t.Fatalf("narrowing records the permission and commands, neither widening: %+v", step.AccessEdits)
	}
	if commands, _ := fieldEdit(step, domain.AccessCommands); commands.Note != `Commands changed at approval; the plan had "go test ./...".` {
		t.Fatalf("the note names what the plan had: %q", commands.Note)
	}
}

func TestApprovalPathsAndCommandsWidenOnlyBeyondThePlan(t *testing.T) {
	planned := plannedReadOnly()
	planned.Agent, planned.Permission, planned.AllowedPaths = "implementer", domain.PermissionWorktree, []string{"internal/"}
	for name, test := range map[string]struct {
		access Access
		field  string
		widens bool
	}{
		"a path inside the plan's": {Access{Permission: domain.PermissionWorktree, AllowedPaths: []string{"internal/app"}, Commands: planned.Commands}, domain.AccessAllowedPaths, false},
		"a path beside the plan's": {Access{Permission: domain.PermissionWorktree, AllowedPaths: []string{" docs/ "}, Commands: planned.Commands}, domain.AccessAllowedPaths, true},
		"the whole repository":     {Access{Permission: domain.PermissionWorktree, Commands: planned.Commands}, domain.AccessAllowedPaths, true},
		"another command":          {Access{Permission: domain.PermissionWorktree, AllowedPaths: planned.AllowedPaths, Commands: [][]string{{"go", "test", "./..."}, {"go", "vet", "./..."}}}, domain.AccessCommands, true},
		"no commands":              {Access{Permission: domain.PermissionWorktree, AllowedPaths: planned.AllowedPaths}, domain.AccessCommands, false},
	} {
		step := planned
		if err := Restaff(&step, edited(step, "implementer", test.access), accessRegistry(), nil, nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		edit, ok := fieldEdit(step, test.field)
		if !ok || edit.Widens != test.widens || len(step.AccessEdits) != 1 {
			t.Errorf("%s: got %+v, want one %s edit that widens=%v", name, step.AccessEdits, test.field, test.widens)
		}
	}
	trimmed := planned
	if err := Restaff(&trimmed, edited(trimmed, "implementer", Access{Permission: domain.PermissionWorktree, AllowedPaths: []string{" docs/ "}, Commands: planned.Commands}), accessRegistry(), nil, nil, nil); err != nil || !slices.Equal(trimmed.AllowedPaths, []string{"docs/"}) {
		t.Fatalf("paths are trimmed: %v %v", trimmed.AllowedPaths, err)
	}
}

func TestApprovalRefusesShellCommandsAndPathsOutsideTheRepository(t *testing.T) {
	planned := plannedReadOnly()
	planned.Agent, planned.Permission, planned.AllowedPaths = "implementer", domain.PermissionWorktree, []string{"."}
	for says, access := range map[string]Access{
		"runs a shell":           {Permission: domain.PermissionWorktree, Commands: [][]string{{"sh", "-c", "go test ./... && go vet ./..."}}},
		"plain word":             {Permission: domain.PermissionWorktree, Commands: [][]string{{"go", "test", "-run", "TestA|TestB"}}},
		"permission checks off":  {Permission: domain.PermissionWorktree, Commands: [][]string{{"claude", "--dangerously-skip-permissions"}}},
		"outside the repository": {Permission: domain.PermissionWorktree, AllowedPaths: []string{"../elsewhere"}},
		"needs permission":       {Permission: "admin"},
	} {
		step := planned
		if err := Restaff(&step, edited(step, "implementer", access), accessRegistry(), nil, nil, nil); err == nil || !strings.Contains(err.Error(), says) || !strings.Contains(err.Error(), `"reproduce"`) {
			t.Errorf("got %v, want the step named and an error saying %q", err, says)
		}
	}
}

func TestApprovalEditsAnEvaluationStepsCommands(t *testing.T) {
	planned := domain.WorkflowStep{ID: "verify", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, Commands: [][]string{{"task", "ci"}}}
	step := planned
	if err := EditCommands(&step, [][]string{{"task", "deps"}, {"task", "ci"}, {"go", "test", "-run", "TestA|TestB", "./..."}}); err != nil {
		t.Fatal(err)
	}
	if len(step.Commands) != 3 || len(step.AccessEdits) != 1 || !step.AccessEdits[0].Widens || step.AccessEdits[0].Note != `Commands changed at approval; the plan had "task ci".` {
		t.Fatalf("an added check is run directly, so shell characters are fine, and it widens the step: %+v", step)
	}
	same := planned
	if err := EditCommands(&same, [][]string{{"task", "ci"}}); err != nil || same.AccessEdits != nil {
		t.Fatalf("unchanged commands are no edit: %+v %v", same.AccessEdits, err)
	}
	refused := planned
	if err := EditCommands(&refused, [][]string{{"bash", "-c", "task ci"}}); err == nil || !strings.Contains(err.Error(), "runs a shell") {
		t.Fatalf("a shell-string check must be refused, got %v", err)
	}
	agentStep := plannedReadOnly()
	if err := EditCommands(&agentStep, nil); err != nil || len(agentStep.Commands) != 1 {
		t.Fatalf("EditCommands leaves other steps alone: %+v %v", agentStep, err)
	}
}

func TestARerunKeepsTheEditsItsPlanWasApprovedWith(t *testing.T) {
	approved := plannedReadOnly()
	approved.Agent, approved.Permission, approved.AllowedPaths = "implementer", domain.PermissionWorktree, []string{"."}
	approved.AccessEdits = []domain.AccessEdit{{Field: domain.AccessPermission, Note: "Permission set to worktree-write at approval; the plan said read-only.", Widens: true}}
	step := approved
	access := Access{Permission: domain.PermissionWorktree, AllowedPaths: approved.AllowedPaths, Commands: [][]string{{"go", "test", "./..."}, {"go", "vet", "./..."}}}
	if err := Restaff(&step, edited(step, "implementer", access), accessRegistry(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(step.AccessEdits) != 2 || step.AccessEdits[0].Field != domain.AccessPermission || step.AccessEdits[1].Field != domain.AccessCommands {
		t.Fatalf("the first approval's edit stays beside the rerun's: %+v", step.AccessEdits)
	}
	again := approved
	if err := Restaff(&again, edited(again, "implementer", Access{Permission: domain.PermissionReadOnly}), accessRegistry(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if permission, _ := fieldEdit(again, domain.AccessPermission); permission.Widens || len(again.AccessEdits) != 2 {
		t.Fatalf("a later edit of the same field replaces the earlier one: %+v", again.AccessEdits)
	}
	// The run was approved with a command and a path its rule did not have.
	// A rerun that only reorders the commands and narrows the paths edits
	// both against that plan, and they are still wider than the rule.
	widened := approved
	widened.Commands, widened.AllowedPaths = [][]string{{"go", "test", "./..."}, {"go", "vet", "./..."}}, []string{"internal", "docs"}
	widened.AccessEdits = []domain.AccessEdit{
		{Field: domain.AccessPermission, Note: "earlier permission", Widens: true},
		{Field: domain.AccessCommands, Note: "earlier commands", Widens: true},
		{Field: domain.AccessAllowedPaths, Note: "earlier paths", Widens: true},
	}
	narrowed := Access{Permission: domain.PermissionWorktree, AllowedPaths: []string{"docs"}, Commands: [][]string{{"go", "vet", "./..."}, {"go", "test", "./..."}}}
	if err := Restaff(&widened, edited(widened, "implementer", narrowed), accessRegistry(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{domain.AccessCommands, domain.AccessAllowedPaths} {
		if edit, ok := fieldEdit(widened, field); !ok || !edit.Widens || strings.HasPrefix(edit.Note, "earlier") {
			t.Fatalf("the rerun's %s edit replaces the note and still widens: %+v", field, widened.AccessEdits)
		}
	}
	check := domain.WorkflowStep{ID: "verify", Kind: domain.StepEvaluation, Commands: [][]string{{"task", "ci"}}, AccessEdits: []domain.AccessEdit{{Field: domain.AccessCommands, Note: "earlier"}}}
	if err := EditCommands(&check, [][]string{{"task", "ci"}}); err != nil || len(check.AccessEdits) != 1 || check.AccessEdits[0].Note != "earlier" {
		t.Fatalf("unchanged commands keep their earlier edit: %+v %v", check.AccessEdits, err)
	}
}
