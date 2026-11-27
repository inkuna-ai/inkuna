package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
)

// RuleEditsView is a rerun's changes to what its steps may do, made to the
// rule its run was planned from for the Rules editor to show unsaved
// (docs/rules.md): the project's rules document with them made, where saving
// it writes, what each change is, and the changes left out with the reason.
// Nothing is written until the person saves the document there (SaveRules).
type RuleEditsView struct {
	ProjectID string `json:"projectId"`
	RuleName  string `json:"ruleName"`
	// RuleIndex is where the rule is in Rules.Document.Rules, as planning
	// found it by name; absent when the run was planned from the default
	// flow, so the view never matches names again.
	RuleIndex *int           `json:"ruleIndex,omitempty"`
	Rules     RulesView      `json:"rules"`
	Changes   []RuleEditView `json:"changes"`
	LeftOut   []RuleEditView `json:"leftOut"`
}

// RuleEditView is one change a rerun's plan makes to a stage's access, by
// the step it comes from and the field: what it is, or why it was left out.
type RuleEditView struct {
	StepID string `json:"stepId"`
	Field  string `json:"field"`
	Text   string `json:"text"`
}

// RerunRuleEdits makes a rerun's access changes to the rule its run was
// planned from, for the person to review and save in the Rules editor. The
// plan is Go's rebuilding of their edits against the run's plan, as
// RerunWorkflowRun would run it. A step's changes are each of its
// permission, commands, and allowed paths that differ from what its stage
// gave it when the run was planned (WorkflowStep.RuleAccess), the ones the
// run was approved with included. Each is made to the stage of the same id,
// unless the rule has no such stage any more or the stage now gives that
// field something else than it did then: an edit made to the rule since is
// never undone.
func (service *Service) RerunRuleEdits(runID, workflowJSON string) (RuleEditsView, error) {
	if err := service.ready(); err != nil {
		return RuleEditsView{}, err
	}
	var edited domain.WorkflowSpec
	if err := json.Unmarshal([]byte(workflowJSON), &edited); err != nil {
		return RuleEditsView{}, fmt.Errorf("decode workflow: %w", err)
	}
	ctx := service.context()
	execution, err := service.store.Execution(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return RuleEditsView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return RuleEditsView{}, fmt.Errorf("load run %s: %w", runID, err)
	}
	base, err := service.store.ExecutionPlan(ctx, runID)
	if err != nil {
		return RuleEditsView{}, fmt.Errorf("load the plan of run %s: %w", runID, err)
	}
	service.configMu.RLock()
	revision, err := service.reviseFrom(ctx, base, edited)
	service.configMu.RUnlock()
	if err != nil {
		return RuleEditsView{}, err
	}
	project, err := service.store.Project(ctx, execution.ProjectID)
	if err != nil {
		return RuleEditsView{}, fmt.Errorf("load project: %w", err)
	}
	task, err := service.store.Task(ctx, execution.TaskID)
	if err != nil {
		return RuleEditsView{}, fmt.Errorf("load task: %w", err)
	}
	document, exists, path, err := service.loadRules(project)
	if err != nil {
		return RuleEditsView{}, err
	}
	flow, index := ruleFlow(&document, base.RuleName)
	if flow == nil {
		return RuleEditsView{}, fmt.Errorf("rule %q is no longer in %s, so it has no stages to save these changes to", base.RuleName, path)
	}
	approved := make(map[string]domain.WorkflowStep, len(base.Steps))
	for _, step := range base.Steps {
		approved[step.ID] = step
	}
	view := RuleEditsView{ProjectID: project.ID, RuleName: base.RuleName, Changes: []RuleEditView{}, LeftOut: []RuleEditView{}}
	if index >= 0 {
		view.RuleIndex = &index
	}
	// Steps are matched to stages as planning saw them, with the flow's uses
	// expanded; a change is then made to the entry as written.
	expanded, err := document.ExpandFlow(*flow)
	if err != nil {
		return RuleEditsView{}, fmt.Errorf("rule %q in %s: %w", base.RuleName, path, err)
	}
	for _, step := range revision.spec.Steps {
		changes, leftOut := carryAccess(step, approved[step.ID], flow, expanded, base.RuleName, task.Kind)
		view.Changes = append(view.Changes, changes...)
		view.LeftOut = append(view.LeftOut, leftOut...)
	}
	view.Rules = RulesView{Document: document, Exists: exists, Path: path}
	return view, nil
}

// ruleFlow is the flow of document's rule called name, and its index, or
// its default flow and -1 for "default", as planning names them (a rule
// called default comes first); nil when there is none.
func ruleFlow(document *rules.Document, name string) (*rules.Flow, int) {
	name = strings.TrimSpace(name)
	for index := range document.Rules {
		if strings.EqualFold(name, document.Rules[index].Name) {
			return &document.Rules[index].Then, index
		}
	}
	if !strings.EqualFold(name, rules.DefaultName) {
		return nil, -1
	}
	if document.Default == nil {
		// Planning fell back on the built-in default flow, so a change to it
		// is saved as the document's own.
		document.Default = rules.Default().Default
	}
	return document.Default, -1
}

// carryAccess makes step's access changes to its stage in flow, and lists
// them, and those it leaves out with why. expanded is flow with its uses
// expanded, as planning saw it. approved is the step as the run was
// approved with it, which a plan from before RuleAccess was kept stands in
// for, except where that approval changed it.
func carryAccess(step, approved domain.WorkflowStep, flow *rules.Flow, expanded rules.Flow, ruleName string, kind domain.TaskKind) (changes, leftOut []RuleEditView) {
	var fields []string
	switch step.Kind {
	case domain.StepAgent:
		fields = []string{domain.AccessPermission, domain.AccessCommands, domain.AccessAllowedPaths}
	case domain.StepEvaluation:
		fields = []string{domain.AccessCommands}
	default:
		return nil, nil
	}
	now := domain.StepAccess{Permission: step.Permission, Commands: step.Commands, AllowedPaths: step.AllowedPaths}
	planned, known := step.RuleAccess, map[string]bool{}
	if planned == nil {
		planned = &domain.StepAccess{Permission: approved.Permission, Commands: approved.Commands, AllowedPaths: approved.AllowedPaths}
		for _, edit := range approved.AccessEdits {
			known[edit.Field] = false
		}
	}
	// What the stage gives the step now, before any change is made to it.
	stage, current, grouped := ruleStage(flow, expanded, step.ID, kind)
	for _, field := range fields {
		// A read-only step keeps no paths; making it read-only clears the
		// stage's with its permission.
		if sameAccess(field, now, *planned) || (field == domain.AccessAllowedPaths && now.Permission != domain.PermissionWorktree) {
			continue
		}
		edit := RuleEditView{StepID: step.ID, Field: field}
		switch {
		case !isKnown(known, field):
			edit.Text = fmt.Sprintf("Step %s's %s: this run was planned before Inkuna kept what a rule gave each step, so whether rule %q has changed them since is unknown.", step.ID, accessName(field), ruleName)
		case grouped != "":
			edit.Text = fmt.Sprintf("Step %s's %s: stage %s comes from group %q, which rule %q uses whole, so change it in the group.", step.ID, accessName(field), step.ID, grouped, ruleName)
		case stage == nil:
			edit.Text = fmt.Sprintf("Step %s's %s: rule %q has no stage %s any more.", step.ID, accessName(field), ruleName, step.ID)
		case !sameAccess(field, current, *planned):
			edit.Text = fmt.Sprintf("Step %s's %s: rule %q has changed them on stage %s since this run was planned, so the rule keeps its own.", step.ID, accessName(field), ruleName, step.ID)
		case field == domain.AccessPermission && now.Permission != domain.PermissionWorktree && !sameAccess(domain.AccessAllowedPaths, current, *planned):
			// A read-only stage keeps no paths, so making it read-only would
			// drop paths the rule was given since.
			edit.Text = fmt.Sprintf("Step %s's permission: rule %q has changed the allowed paths of stage %s since this run was planned, which making it read-only would drop, so the rule keeps its own.", step.ID, ruleName, step.ID)
		default:
			if !applyAccess(stage, field, now) {
				continue
			}
			edit.Text = fmt.Sprintf("Stage %s: %s %s; the rule had %s.", step.ID, accessName(field), accessText(field, now), accessText(field, *planned))
			if stage.Use != "" {
				edit.Text += fmt.Sprintf(" It overrides shared stage %q in this rule only.", stage.Use)
			}
			changes = append(changes, edit)
			continue
		}
		leftOut = append(leftOut, edit)
	}
	return changes, leftOut
}

// isKnown reports whether the stage's value for field when the run was
// planned is known: always, but for a plan from before RuleAccess was kept,
// whose approval changed the field.
func isKnown(known map[string]bool, field string) bool {
	value, listed := known[field]
	return !listed || value
}

// ruleStage finds the entry of flow, as written, that the step was planned
// from, and what its stage gives the step now. A step from a use of a
// shared stage is changed on the use, as an override in this flow only. A
// step a group brings has no entry of its own to change, so the group is
// named instead.
func ruleStage(flow *rules.Flow, expanded rules.Flow, stepID string, kind domain.TaskKind) (entry *rules.Stage, current domain.StepAccess, group string) {
	index := slices.IndexFunc(expanded.Stages, func(stage rules.Stage) bool { return stage.ID == stepID })
	if index < 0 {
		return nil, domain.StepAccess{}, ""
	}
	planned := expanded.Stages[index]
	current = planned.Access(kind)
	if planned.From != nil && len(planned.From.Groups) > 0 {
		return nil, current, planned.From.Groups[0]
	}
	for position := range flow.Stages {
		written := &flow.Stages[position]
		id := written.ID
		if written.Use != "" && id == "" {
			id = written.Use
		}
		if id == stepID {
			return written, current, ""
		}
	}
	return nil, current, ""
}

// applyAccess gives stage the field's value from access, as a rule says
// it, and reports whether the stage changed: allowed paths of the whole
// repository are what a writable stage that names none has. On a use, the
// field is listed as overridden (access fields are named as the rules
// fields that hold them), so a value that is empty still overrides the
// shared stage's rather than falling back to it. A field the use inherits
// is empty on the use, so clearing it changes nothing there: the override
// alone is the change.
func applyAccess(stage *rules.Stage, field string, access domain.StepAccess) bool {
	inherited := stage.Use != "" && !slices.Contains(stage.Overrides, field)
	if !setAccess(stage, field, access) && !inherited {
		return false
	}
	if inherited {
		stage.Overrides = append(stage.Overrides, field)
	}
	return true
}

// setAccess sets the field on stage and reports whether it changed.
func setAccess(stage *rules.Stage, field string, access domain.StepAccess) bool {
	switch field {
	case domain.AccessPermission:
		stage.Permission = access.Permission
		if access.Permission != domain.PermissionWorktree {
			stage.AllowedPaths = nil
		}
		return true
	case domain.AccessCommands:
		if sameCommands(stage.Commands, access.Commands) {
			return false
		}
		stage.Commands = cloneCommandList(access.Commands)
		return true
	default:
		paths := access.AllowedPaths
		if sameStrings(paths, []string{"."}) {
			paths = nil
		}
		if sameStrings(stage.AllowedPaths, paths) {
			return false
		}
		stage.AllowedPaths = append([]string(nil), paths...)
		return true
	}
}

func sameAccess(field string, left, right domain.StepAccess) bool {
	switch field {
	case domain.AccessPermission:
		return left.Permission == right.Permission
	case domain.AccessCommands:
		return sameCommands(left.Commands, right.Commands)
	default:
		return sameStrings(left.AllowedPaths, right.AllowedPaths)
	}
}

func accessName(field string) string {
	if field == domain.AccessAllowedPaths {
		return "allowed paths"
	}
	return field
}

// accessText is the field's value in a sentence, each command or path
// quoted so one such as . reads as one.
func accessText(field string, access domain.StepAccess) string {
	var values []string
	switch field {
	case domain.AccessPermission:
		return string(access.Permission)
	case domain.AccessCommands:
		for _, command := range access.Commands {
			values = append(values, strings.Join(command, " "))
		}
	default:
		values = access.AllowedPaths
	}
	if len(values) == 0 {
		return "none"
	}
	return `"` + strings.Join(values, `", "`) + `"`
}

func cloneCommandList(commands [][]string) [][]string {
	if len(commands) == 0 {
		return nil
	}
	cloned := make([][]string, len(commands))
	for index, command := range commands {
		cloned[index] = append([]string(nil), command...)
	}
	return cloned
}
