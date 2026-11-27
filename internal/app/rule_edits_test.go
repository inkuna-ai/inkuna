package app

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
)

// stageOf is the stage of the rule called name with the id, or a zero one.
func stageOf(document rules.Document, name, id string) rules.Stage {
	if flow, _ := ruleFlow(&document, name); flow != nil {
		for _, stage := range flow.Stages {
			if stage.ID == id {
				return stage
			}
		}
	}
	return rules.Stage{}
}

func encodeRules(t *testing.T, document rules.Document) string {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// suggestedRerun is the plan of a run that failed on a denied command with
// Go's suggestion to allow it made, as the rerun screen shows it.
func suggestedRerun(t *testing.T) (*Service, WorkflowRunView, domain.WorkflowSpec) {
	t.Helper()
	service, _, run := failedOnADeniedCommand(t)
	failure, err := service.RunFailure(run.ID)
	if err != nil || len(failure.Suggestions) != 1 {
		t.Fatalf("one suggestion: %+v %v", failure, err)
	}
	rerun, err := service.PlanRerun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := service.ReviseRerun(run.ID, encodeWorkflow(t, applySuggestion(rerun.Plan.Workflow, failure.Suggestions[0])))
	if err != nil {
		t.Fatal(err)
	}
	return service, run, revision.Workflow
}

func TestARerunsChangesAreMadeToItsRuleAndSavedOnlyByThePerson(t *testing.T) {
	service, run, plan := suggestedRerun(t)
	edits, err := service.RerunRuleEdits(run.ID, encodeWorkflow(t, plan))
	if err != nil {
		t.Fatal(err)
	}
	if edits.RuleName != "bugfix" || edits.RuleIndex == nil || edits.Rules.Document.Rules[*edits.RuleIndex].Name != "bugfix" || len(edits.Changes) != 1 || edits.Changes[0].StepID != "fix" || edits.Changes[0].Field != domain.AccessCommands || len(edits.LeftOut) != 0 {
		t.Fatalf("the rule gets fix's command: %+v", edits)
	}
	if !strings.Contains(edits.Changes[0].Text, `"go test ./..."`) || !strings.Contains(edits.Changes[0].Text, "the rule had none") {
		t.Fatalf("the change says what it is: %q", edits.Changes[0].Text)
	}
	fix := stageOf(edits.Rules.Document, "bugfix", "fix")
	if !slices.ContainsFunc(fix.Commands, func(command []string) bool { return strings.Join(command, " ") == "go test ./..." }) {
		t.Fatalf("the document allows it on stage fix: %+v", fix)
	}
	if fix.Permission != domain.PermissionWorktree || fix.Agent != "auto" {
		t.Fatalf("the rest of the stage is the rule's: %+v", fix)
	}
	if _, err := os.Stat(edits.Rules.Path); !os.IsNotExist(err) || edits.Rules.Exists {
		t.Fatalf("nothing is written until the person saves: %v", err)
	}

	project := edits.ProjectID
	if _, err := service.SaveRules(project, encodeRules(t, edits.Rules.Document)); err != nil {
		t.Fatal(err)
	}
	saved, err := service.Rules(project)
	if err != nil || !saved.Exists {
		t.Fatalf("saving writes the rules: %+v %v", saved, err)
	}
	if fix := stageOf(saved.Document, "bugfix", "fix"); !slices.ContainsFunc(fix.Commands, func(command []string) bool { return strings.Join(command, " ") == "go test ./..." }) {
		t.Fatalf("the saved rule allows it: %+v", fix)
	}
	// The rule now says what the plan does, so there is nothing to carry.
	if again, err := service.RerunRuleEdits(run.ID, encodeWorkflow(t, plan)); err != nil || len(again.Changes) != 0 || len(again.LeftOut) != 1 {
		t.Fatalf("the rule changed since the run was planned: %+v %v", again, err)
	}
}

func TestAChangeIsLeftOutWhereTheRuleChangedSinceTheRunWasPlanned(t *testing.T) {
	service, run, plan := suggestedRerun(t)
	edits, err := service.RerunRuleEdits(run.ID, encodeWorkflow(t, plan))
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.Rules(edits.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	changed := current.Document
	flow, _ := ruleFlow(&changed, "bugfix")
	for index := range flow.Stages {
		if flow.Stages[index].ID == "fix" {
			flow.Stages[index].Commands = [][]string{{"go", "vet", "./..."}}
		}
	}
	if _, err := service.SaveRules(edits.ProjectID, encodeRules(t, changed)); err != nil {
		t.Fatal(err)
	}
	edits, err = service.RerunRuleEdits(run.ID, encodeWorkflow(t, plan))
	if err != nil || len(edits.Changes) != 0 || len(edits.LeftOut) != 1 || !strings.Contains(edits.LeftOut[0].Text, "has changed them on stage fix since this run was planned") {
		t.Fatalf("the rule's newer commands are kept: %+v %v", edits, err)
	}
	if fix := stageOf(edits.Rules.Document, "bugfix", "fix"); !slices.EqualFunc(fix.Commands, [][]string{{"go", "vet", "./..."}}, slices.Equal[[]string]) {
		t.Fatalf("stage fix is as the rule has it: %+v", fix)
	}

	for index := range flow.Stages {
		if flow.Stages[index].ID == "fix" {
			flow.Stages[index].ID, flow.Stages[index].Commands = "implement", nil
		}
		if slices.Contains(flow.Stages[index].DependsOn, "fix") {
			flow.Stages[index].DependsOn = []string{"implement"}
		}
	}
	if _, err := service.SaveRules(edits.ProjectID, encodeRules(t, changed)); err != nil {
		t.Fatal(err)
	}
	edits, err = service.RerunRuleEdits(run.ID, encodeWorkflow(t, plan))
	if err != nil || len(edits.Changes) != 0 || len(edits.LeftOut) != 1 || !strings.Contains(edits.LeftOut[0].Text, `rule "bugfix" has no stage fix any more`) {
		t.Fatalf("a stage the rule no longer has gets nothing: %+v %v", edits, err)
	}

	changed.Rules = slices.DeleteFunc(changed.Rules, func(rule rules.Rule) bool { return rule.Name == "bugfix" })
	if _, err := service.SaveRules(edits.ProjectID, encodeRules(t, changed)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RerunRuleEdits(run.ID, encodeWorkflow(t, plan)); err == nil || !strings.Contains(err.Error(), `rule "bugfix" is no longer in`) {
		t.Fatalf("a rule that is gone has nothing to save to: %v", err)
	}
}

func TestARuleThatWouldNotPlanIsRefusedWhenSaved(t *testing.T) {
	service, run, plan := suggestedRerun(t)
	// The rerun gives reproduce a writable agent and lets it write; only its
	// access is carried, so the rule's read-only researcher stays on it.
	writes := editStep(plan, "reproduce", func(step *domain.WorkflowStep) {
		step.Agent, step.Permission = "implementer", domain.PermissionWorktree
	})
	edits, err := service.RerunRuleEdits(run.ID, encodeWorkflow(t, writes))
	if err != nil {
		t.Fatal(err)
	}
	if reproduce := stageOf(edits.Rules.Document, "bugfix", "reproduce"); reproduce.Permission != domain.PermissionWorktree || reproduce.Agent != "researcher" {
		t.Fatalf("reproduce is made writable: %+v", reproduce)
	}
	if _, err := service.SaveRules(edits.ProjectID, encodeRules(t, edits.Rules.Document)); err == nil || !strings.Contains(err.Error(), `read-only agent "researcher"`) {
		t.Fatalf("Go refuses the rule: %v", err)
	}
	if _, err := os.Stat(edits.Rules.Path); !os.IsNotExist(err) {
		t.Fatalf("a refused rule is not written: %v", err)
	}
}

func TestARerunsChangesAreRebuiltByGo(t *testing.T) {
	service, run, plan := suggestedRerun(t)
	shell := editStep(plan, "fix", func(step *domain.WorkflowStep) {
		step.Commands = append(step.Commands, []string{"sh", "-c", "go test ./..."})
	})
	if _, err := service.RerunRuleEdits(run.ID, encodeWorkflow(t, shell)); err == nil {
		t.Fatal("a command a rule could not allow is refused before it reaches the rule")
	}
}

func TestMakingAStageReadOnlyKeepsPathsTheRuleGotSince(t *testing.T) {
	service, run, plan := suggestedRerun(t)
	readOnly := editStep(plan, "fix", func(step *domain.WorkflowStep) {
		step.Permission, step.AllowedPaths = domain.PermissionReadOnly, nil
	})
	edits, err := service.RerunRuleEdits(run.ID, encodeWorkflow(t, readOnly))
	if err != nil {
		t.Fatal(err)
	}
	if fix := stageOf(edits.Rules.Document, "bugfix", "fix"); fix.Permission != domain.PermissionReadOnly || !slices.ContainsFunc(edits.Changes, func(change RuleEditView) bool { return change.Field == domain.AccessPermission }) {
		t.Fatalf("with the rule as planned, fix is made read-only: %+v %+v", fix, edits.Changes)
	}

	// The rule has since given fix paths of its own.
	current, err := service.Rules(edits.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	changed := current.Document
	flow, _ := ruleFlow(&changed, "bugfix")
	for index := range flow.Stages {
		if flow.Stages[index].ID == "fix" {
			flow.Stages[index].AllowedPaths = []string{"internal"}
		}
	}
	if _, err := service.SaveRules(edits.ProjectID, encodeRules(t, changed)); err != nil {
		t.Fatal(err)
	}
	edits, err = service.RerunRuleEdits(run.ID, encodeWorkflow(t, readOnly))
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(edits.Changes, func(change RuleEditView) bool { return change.Field == domain.AccessPermission }) ||
		!slices.ContainsFunc(edits.LeftOut, func(change RuleEditView) bool {
			return change.Field == domain.AccessPermission && strings.Contains(change.Text, "has changed the allowed paths of stage fix")
		}) {
		t.Fatalf("the permission is left out with the reason: %+v", edits)
	}
	if fix := stageOf(edits.Rules.Document, "bugfix", "fix"); fix.Permission != domain.PermissionWorktree || strings.Join(fix.AllowedPaths, ",") != "internal" {
		t.Fatalf("fix keeps the rule's paths and permission: %+v", fix)
	}
}

func TestARuleCalledDefaultComesBeforeTheDefaultFlow(t *testing.T) {
	document := rules.Document{
		Rules:   []rules.Rule{{Name: "other"}, {Name: "Default"}},
		Default: &rules.Flow{},
	}
	if flow, index := ruleFlow(&document, "default"); index != 1 || flow != &document.Rules[1].Then {
		t.Fatalf("the rule called default is the one planning took: %d", index)
	}
	document.Rules = document.Rules[:1]
	if flow, index := ruleFlow(&document, "default"); index != -1 || flow != document.Default {
		t.Fatalf("otherwise it is the default flow: %d", index)
	}
	if flow, _ := ruleFlow(&document, "gone"); flow != nil {
		t.Fatal("a rule that is gone has no flow")
	}
}

// sharedBugfix is the built-in bugfix rule written with its fix stage
// shared, and with reproduce and fix in a group when grouped is set, so a
// run planned from the built-in rule planned the same stages.
func sharedBugfix(grouped bool) string {
	const head = `version: 2
stages:
  fix:
    title: Implement the fix
    kind: agent
    agent: auto
    permission: worktree-write
groups:
  repair:
    stages:
      - id: reproduce
        title: Reproduce and locate the defect
        kind: agent
        agent: researcher
        permission: read-only
      - use: fix
        dependsOn: [reproduce]
rules:
  - name: bugfix
    when:
      kind: [bugfix]
    then:
      stages:
        - id: profile
          kind: script
`
	middle := `        - id: reproduce
          title: Reproduce and locate the defect
          kind: agent
          agent: researcher
          permission: read-only
          dependsOn: [profile]
        - use: fix
          dependsOn: [reproduce]
        - id: verify
          kind: evaluation
          dependsOn: [fix]
`
	if grouped {
		middle = `        - use: repair
          dependsOn: [profile]
        - id: verify
          kind: evaluation
          dependsOn: [repair]
`
	}
	return head + middle + `        - id: approval
          kind: approval
          dependsOn: [verify]
`
}

// A rerun's change to a step from a shared stage overrides it on the use in
// that rule, never on the shared stage other rules use; a step a group
// brings is left out, naming the group to change instead.
func TestARerunsChangeToASharedStageIsMadeOnItsUseOnly(t *testing.T) {
	service, run, plan := suggestedRerun(t)
	first, err := service.RerunRuleEdits(run.ID, encodeWorkflow(t, plan))
	if err != nil {
		t.Fatal(err)
	}
	project := first.ProjectID
	parsed, err := service.RulesFromYAML(project, sharedBugfix(false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveRules(project, encodeRules(t, parsed)); err != nil {
		t.Fatal(err)
	}

	edits, err := service.RerunRuleEdits(run.ID, encodeWorkflow(t, plan))
	if err != nil || len(edits.Changes) != 1 || len(edits.LeftOut) != 0 || edits.Changes[0].StepID != "fix" {
		t.Fatalf("the change goes to the use of shared stage fix: %+v %v", edits, err)
	}
	if !strings.Contains(edits.Changes[0].Text, `It overrides shared stage "fix" in this rule only.`) {
		t.Fatalf("the change says it overrides the shared stage: %q", edits.Changes[0].Text)
	}
	document := edits.Rules.Document
	flow, _ := ruleFlow(&document, "bugfix")
	use := flow.Stages[2]
	if use.Use != "fix" || !slices.Contains(use.Overrides, domain.AccessCommands) || !slices.ContainsFunc(use.Commands, func(command []string) bool { return strings.Join(command, " ") == "go test ./..." }) {
		t.Fatalf("the use carries the command as its override: %+v", use)
	}
	if shared := document.Stages["fix"]; len(shared.Commands) != 0 {
		t.Fatalf("the shared stage is left alone: %+v", shared)
	}
	if _, err := service.SaveRules(project, encodeRules(t, document)); err != nil {
		t.Fatal(err)
	}
	saved, err := service.Rules(project)
	if err != nil {
		t.Fatal(err)
	}
	planned, _ := saved.Document.Named("bugfix")
	if fix := planned.Stages[2]; fix.ID != "fix" || len(fix.Commands) != 1 || fix.From == nil || fix.From.Stage != "fix" {
		t.Fatalf("the saved rule plans fix with the command: %+v", fix)
	}

	// A stage a group brings has no entry of its own in the rule.
	parsed, err = service.RulesFromYAML(project, sharedBugfix(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveRules(project, encodeRules(t, parsed)); err != nil {
		t.Fatal(err)
	}
	edits, err = service.RerunRuleEdits(run.ID, encodeWorkflow(t, plan))
	if err != nil || len(edits.Changes) != 0 || len(edits.LeftOut) != 1 || !strings.Contains(edits.LeftOut[0].Text, `stage fix comes from group "repair", which rule "bugfix" uses whole, so change it in the group`) {
		t.Fatalf("a change to a grouped stage is left out naming the group: %+v %v", edits, err)
	}
}

func TestClearingWhatAUseInheritsOverridesTheSharedStage(t *testing.T) {
	cleared := domain.StepAccess{Permission: domain.PermissionWorktree, AllowedPaths: []string{"."}}
	for _, field := range []string{domain.AccessCommands, domain.AccessAllowedPaths} {
		use := rules.Stage{Use: "fix"}
		if !applyAccess(&use, field, cleared) {
			t.Fatalf("clearing %s a use inherits is a change", field)
		}
		if !slices.Equal(use.Overrides, []string{field}) || len(use.Commands) != 0 || len(use.AllowedPaths) != 0 {
			t.Fatalf("the use overrides %s with nothing: %+v", field, use)
		}
		// Once overridden, or on a stage of the rule's own, an empty field
		// stays unchanged.
		if applyAccess(&use, field, cleared) {
			t.Fatalf("clearing an overridden, empty %s again is no change", field)
		}
		if own := (rules.Stage{ID: "fix", Kind: "agent"}); applyAccess(&own, field, cleared) {
			t.Fatalf("clearing an empty %s on a stage of the rule's own is no change", field)
		}
	}
}
