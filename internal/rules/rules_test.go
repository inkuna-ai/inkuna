package rules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

const sample = `version: 1
rules:
  - name: security-hotfix
    description: Security bugs get a human check before the fix lands.
    when:
      kind: [bugfix]
      labels: [security, cve]
    then:
      stages:
        - id: profile
          kind: script
        - id: triage
          kind: agent
          agent: researcher
          permission: read-only
          dependsOn: [profile]
        - id: confirm
          kind: human-input
          prompt: "Confirm the proposed remediation before code changes."
          dependsOn: [triage]
        - id: fix
          kind: agent
          agent: auto
          permission: worktree-write
          allowedPaths: [internal/]
          skills: [security-review]
          dependsOn: [confirm]
        - id: verify
          kind: evaluation
          dependsOn: [fix]
        - id: approval
          kind: approval
          dependsOn: [verify]
  - name: docs-keyword
    when:
      keywords: [readme, documentation]
      complexity: { min: 1, max: 4 }
    then:
      stages:
        - id: write
          kind: agent
          agent: documenter
        - id: approval
          kind: approval
          dependsOn: [write]
`

func task(kind domain.TaskKind, labels ...string) domain.TaskSpec {
	return domain.TaskSpec{Version: 1, ID: "task-1", Kind: kind, Objective: "Update the README badges", Labels: labels, Repository: "/repo/inkuna", Source: domain.TaskSource{Kind: "linear"}, Priority: 1, Budget: domain.Budget{WallTimeSeconds: 3600}}
}

func TestParseMatchAndWorkflow(t *testing.T) {
	document, err := Parse(sample)
	if err != nil {
		t.Fatal(err)
	}
	name, flow := document.Match(task(domain.TaskBugfix, "Security"), 0)
	if name != "security-hotfix" || len(flow.Stages) != 6 {
		t.Fatalf("expected security rule, got %s with %d stages", name, len(flow.Stages))
	}
	workflow, err := flow.Workflow(task(domain.TaskBugfix, "security"), name, domain.RuntimeClaude, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if workflow.RuleName != name || len(workflow.Steps) != 6 {
		t.Fatalf("unexpected workflow %+v", workflow)
	}
	confirm := workflow.Steps[2]
	if confirm.Kind != domain.StepHumanInput || confirm.Prompt == "" || confirm.Permission != domain.PermissionReadOnly {
		t.Fatalf("human-input step not converted: %+v", confirm)
	}
	fix := workflow.Steps[3]
	if fix.Agent != "auto" || fix.Permission != domain.PermissionWorktree || fix.AllowedPaths[0] != "internal/" || fix.SelectedSkills[0] != "security-review" || fix.Budget.WallTimeSeconds != 1800 {
		t.Fatalf("agent stage not converted: %+v", fix)
	}
	if workflow.Steps[0].Title != "Profile repository" || workflow.Steps[5].Title != "Review changes" {
		t.Fatalf("default titles missing: %q %q", workflow.Steps[0].Title, workflow.Steps[5].Title)
	}

	name, _ = document.Match(task(domain.TaskDocumentation), 3)
	if name != "docs-keyword" {
		t.Fatalf("keyword and complexity match failed: %s", name)
	}
	name, _ = document.Match(task(domain.TaskDocumentation), 8)
	if name != "default" {
		t.Fatalf("complexity outside range must fall through, got %s", name)
	}
	name, _ = document.Match(task(domain.TaskBugfix, "ui"), 0)
	if name != "default" {
		t.Fatalf("label mismatch must fall through, got %s", name)
	}
}

func TestDefaultDocumentCoversEveryKind(t *testing.T) {
	document := Default()
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []domain.TaskKind{domain.TaskBugfix, domain.TaskResearch, domain.TaskReview, domain.TaskImplementation, domain.TaskDocumentation} {
		name, flow := document.Match(task(kind), 0)
		workflow, err := flow.Workflow(task(kind), name, domain.RuntimeCodex, time.Now())
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		last := workflow.Steps[len(workflow.Steps)-1]
		if last.Kind != domain.StepApproval {
			t.Fatalf("%s must end with approval", kind)
		}
		for _, step := range workflow.Steps {
			if step.Kind == domain.StepAgent && (kind == domain.TaskResearch || kind == domain.TaskReview) && step.Permission != domain.PermissionReadOnly {
				t.Fatalf("%s agent step must be read-only", kind)
			}
		}
	}
	name, _ := document.Match(task(domain.TaskBugfix), 0)
	if name != "bugfix" {
		t.Fatalf("expected bugfix rule, got %s", name)
	}
}

func TestValidationRejectsUnsafeFlows(t *testing.T) {
	cases := map[string]string{
		"no approval":        "version: 1\nrules: []\ndefault:\n  stages:\n    - id: work\n      kind: agent\n      agent: auto\n",
		"approval not last":  "version: 1\nrules: []\ndefault:\n  stages:\n    - id: approval\n      kind: approval\n    - id: work\n      kind: agent\n      agent: auto\n",
		"forward dependency": "version: 1\nrules: []\ndefault:\n  stages:\n    - id: a\n      kind: agent\n      agent: auto\n      dependsOn: [b]\n    - id: b\n      kind: approval\n",
		"missing prompt":     "version: 1\nrules: []\ndefault:\n  stages:\n    - id: ask\n      kind: human-input\n    - id: approval\n      kind: approval\n",
		"unknown field":      "version: 1\nrules: []\nshell: rm -rf /\ndefault:\n  stages:\n    - id: approval\n      kind: approval\n",
		"bad version":        "version: 0\nrules: []\n",
		"newer version":      "version: 3\nrules: []\nlater: true\n",
		"self dependency":    "version: 1\nrules: []\ndefault:\n  stages:\n    - id: a\n      kind: agent\n      agent: auto\n      dependsOn: [a]\n    - id: approval\n      kind: approval\n",
		"bad range":          "version: 1\nrules:\n  - name: x\n    when: { complexity: { min: 0, max: 11 } }\n    then:\n      stages:\n        - id: approval\n          kind: approval\n",
		"continue a script":  "version: 1\nrules: []\ndefault:\n  stages:\n    - id: profile\n      kind: script\n      continueOnFailure: true\n    - id: approval\n      kind: approval\n",
	}
	for name, content := range cases {
		if _, err := Parse(content); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestStageCommandsAreArgvArraysWithoutAShell(t *testing.T) {
	flow := func(kind, command string) string {
		agent := ""
		if kind == "agent" {
			agent = "\n      agent: auto"
		}
		return "version: 1\nrules: []\ndefault:\n  stages:\n    - id: step\n      kind: " + kind + agent + "\n      commands:\n        - " + command + "\n    - id: approval\n      kind: approval\n"
	}
	for _, accepted := range []struct{ kind, command string }{
		{"evaluation", "[go, test, ./...]"},
		{"evaluation", `[go, test, -run, "TestA|TestB", ./...]`},
		{"agent", "[go, test, ./...]"},
	} {
		if _, err := Parse(flow(accepted.kind, accepted.command)); err != nil {
			t.Errorf("%s %s was refused: %v", accepted.kind, accepted.command, err)
		}
	}
	for _, refused := range []struct{ kind, command, says string }{
		{"evaluation", `[sh, -c, "go test ./..."]`, "runs a shell"},
		{"evaluation", `["go test ./..."]`, "whole command line"},
		{"evaluation", "[codex, exec, --yolo]", "permission checks off"},
		{"agent", `[go, test, -run, "TestA|TestB"]`, "plain word"},
		{"agent", "[bash, scripts/check.sh]", "runs a shell"},
	} {
		if _, err := Parse(flow(refused.kind, refused.command)); err == nil || !strings.Contains(err.Error(), refused.says) || !strings.Contains(err.Error(), `stage "step"`) {
			t.Errorf("%s %s: got %v, want the stage refused saying %q", refused.kind, refused.command, err, refused.says)
		}
	}
}

func TestAnEvaluationStageCanRunEveryCommand(t *testing.T) {
	document, err := Parse("version: 1\nrules: []\ndefault:\n  stages:\n    - id: verify\n      kind: evaluation\n      continueOnFailure: true\n      commands:\n        - [task, lint]\n        - [task, test]\n    - id: approval\n      kind: approval\n")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := document.Default.Workflow(task(domain.TaskImplementation, "x"), "default", domain.RuntimeCodex, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Steps[0].ContinueOnFailure || spec.Steps[1].ContinueOnFailure {
		t.Fatalf("continueOnFailure should reach the evaluation step only: %+v", spec.Steps)
	}
}

func TestLoadFromRepository(t *testing.T) {
	root := t.TempDir()
	document, exists, err := Load(root)
	if err != nil || exists || document.Source != "builtin" {
		t.Fatalf("missing rules should yield builtin: exists=%v source=%s err=%v", exists, document.Source, err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".inkuna"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".inkuna", "rules.yaml"), []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	document, exists, err = Load(root)
	if err != nil || !exists || len(document.Rules) != 2 || document.Default == nil {
		t.Fatalf("repository rules were not loaded: exists=%v err=%v doc=%+v", exists, err, document)
	}
}

func TestLabelNamingARuleSelectsIt(t *testing.T) {
	document := Default()
	name, flow := document.Match(task(domain.TaskImplementation, "ui", "BugFix"), 0)
	if name != "bugfix" || len(flow.Stages) != 5 {
		t.Fatalf("a bugfix label must pick the bugfix flow, got %s with %d stages", name, len(flow.Stages))
	}
	name, _ = document.Match(task(domain.TaskBugfix, "default"), 0)
	if name != DefaultName {
		t.Fatalf("a default label must pick the default flow, got %s", name)
	}
	name, _ = document.Match(task(domain.TaskBugfix, "ui"), 0)
	if name != "bugfix" {
		t.Fatalf("conditions still match without a rule label, got %s", name)
	}
	if _, ok := document.LabelRule([]string{"security"}); ok {
		t.Fatal("a label that names no rule selects nothing")
	}
	// A document without a default flow (never validated) falls back to the
	// built-in one instead of failing.
	bare := Document{Rules: document.Rules}
	if name, flow := bare.Match(task(domain.TaskImplementation, "default"), 0); name != DefaultName || len(flow.Stages) != 4 {
		t.Fatalf("a missing default must fall back to the built-in flow, got %s with %d stages", name, len(flow.Stages))
	}
	if name, flow := bare.Match(task(domain.TaskImplementation), 0); name != DefaultName || len(flow.Stages) != 4 {
		t.Fatalf("unmatched tasks must fall back to the built-in flow, got %s with %d stages", name, len(flow.Stages))
	}
}

func TestDefaultOnlyDocumentHasAnEmptyRulesList(t *testing.T) {
	document, err := Parse("version: 1\ndefault:\n  stages:\n    - id: approval\n      kind: approval\n")
	if err != nil {
		t.Fatal(err)
	}
	if document.Rules == nil || len(document.Rules) != 0 {
		t.Fatalf("rules = %#v, want an empty list", document.Rules)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"rules":[]`) {
		t.Fatalf("the views read the rules list; got %s", encoded)
	}
}

func TestNamedFlowLookup(t *testing.T) {
	document := Default()
	if flow, ok := document.Named("Read-Only"); !ok || len(flow.Stages) != 3 {
		t.Fatalf("Named must find rules ignoring case, got ok=%v stages=%d", ok, len(flow.Stages))
	}
	if flow, ok := document.Named("default"); !ok || len(flow.Stages) != 4 {
		t.Fatalf("Named must resolve the default flow, got ok=%v stages=%d", ok, len(flow.Stages))
	}
	if _, ok := document.Named("ghost"); ok {
		t.Fatal("an unknown rule name must not resolve")
	}
}

func TestAnAgentStageNamesTheMCPServersItsStepIsGiven(t *testing.T) {
	document, err := Parse("version: 1\nrules: []\ndefault:\n  stages:\n    - id: triage\n      kind: agent\n      agent: researcher\n      permission: read-only\n      mcp: [linear, docs-search, linear]\n    - id: approval\n      kind: approval\n      dependsOn: [triage]\n")
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := document.Default.Workflow(domain.TaskSpec{ID: "task", Kind: domain.TaskResearch}, DefaultName, domain.RuntimeClaude, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	triage := workflow.Steps[0]
	if strings.Join(triage.MCPServers, ",") != "docs-search,linear" || triage.Metadata[domain.MetadataStageMCP] != "docs-search,linear" {
		t.Fatalf("the stage's servers must be granted once each and recorded as the stage's: %v %v", triage.MCPServers, triage.Metadata)
	}
	for name, content := range map[string]string{
		"approval stage": "version: 1\nrules: []\ndefault:\n  stages:\n    - id: approval\n      kind: approval\n      mcp: [linear]\n",
		"bad name":       "version: 1\nrules: []\ndefault:\n  stages:\n    - id: a\n      kind: agent\n      agent: auto\n      mcp: [Linear]\n    - id: approval\n      kind: approval\n",
		"reserved name":  "version: 1\nrules: []\ndefault:\n  stages:\n    - id: a\n      kind: agent\n      agent: auto\n      mcp: [shell]\n    - id: approval\n      kind: approval\n",
	} {
		if _, err := Parse(content); err == nil || !strings.Contains(err.Error(), "MCP server") {
			t.Fatalf("%s: expected an MCP server error, got %v", name, err)
		}
	}
}

func TestAnAgentStageAsksForTrackerKinds(t *testing.T) {
	document, err := Parse("version: 1\nrules: []\ndefault:\n  stages:\n    - id: triage\n      kind: agent\n      agent: researcher\n      permission: read-only\n      tracker: [create, comment, create]\n    - id: approval\n      kind: approval\n      review: true\n      dependsOn: [triage]\n")
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := document.Default.Workflow(domain.TaskSpec{ID: "task", Kind: domain.TaskResearch}, DefaultName, domain.RuntimeClaude, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The stage's kinds are kept as the stage's; the planner grants them.
	triage := workflow.Steps[0]
	if triage.Metadata[domain.MetadataStageTracker] != "comment,create" || len(triage.TrackerKinds) != 0 {
		t.Fatalf("metadata %v, kinds %v", triage.Metadata, triage.TrackerKinds)
	}
	for name, content := range map[string]string{
		"approval stage": "version: 1\nrules: []\ndefault:\n  stages:\n    - id: approval\n      kind: approval\n      tracker: [comment]\n",
		"unknown kind":   "version: 1\nrules: []\ndefault:\n  stages:\n    - id: a\n      kind: agent\n      agent: auto\n      tracker: [archive]\n    - id: approval\n      kind: approval\n",
	} {
		if _, err := Parse(content); err == nil || !strings.Contains(err.Error(), "tracker") {
			t.Fatalf("%s: expected a tracker error, got %v", name, err)
		}
	}
}

func TestAStepKeepsTheAccessItsStageGaveIt(t *testing.T) {
	flow := Flow{Stages: []Stage{
		{ID: "profile", Kind: domain.StepScript},
		{ID: "fix", Kind: domain.StepAgent, Agent: "auto", AllowedPaths: []string{"internal"}, Commands: [][]string{{"go", "test", "./..."}}, DependsOn: []string{"profile"}},
		{ID: "verify", Kind: domain.StepEvaluation, Commands: [][]string{{"task", "ci"}}, DependsOn: []string{"fix"}},
		{ID: "approval", Kind: domain.StepApproval, DependsOn: []string{"verify"}},
	}}
	workflow, err := flow.Workflow(domain.TaskSpec{ID: "task-1", Kind: domain.TaskBugfix, Budget: domain.Budget{WallTimeSeconds: 600}}, "bugfix", domain.RuntimeCodex, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fix, verify := workflow.Steps[1], workflow.Steps[2]
	if fix.RuleAccess == nil || fix.RuleAccess.Permission != domain.PermissionWorktree || strings.Join(fix.RuleAccess.AllowedPaths, ",") != "internal" || len(fix.RuleAccess.Commands) != 1 {
		t.Fatalf("an agent step keeps what its stage gave it: %+v", fix.RuleAccess)
	}
	if verify.RuleAccess == nil || len(verify.RuleAccess.Commands) != 1 || verify.RuleAccess.Permission != domain.PermissionReadOnly {
		t.Fatalf("an evaluation step keeps its stage's commands: %+v", verify.RuleAccess)
	}
	if workflow.Steps[0].RuleAccess != nil || workflow.Steps[3].RuleAccess != nil {
		t.Fatal("a step approval cannot change keeps nothing")
	}
	// An approval that edits the step leaves the record of the stage alone.
	fix.Commands[0][0] = "changed"
	if fix.RuleAccess.Commands[0][0] != "go" {
		t.Fatalf("the record is a copy: %+v", fix.RuleAccess)
	}
	// What a stage leaves out follows the task: a research task's agent
	// stage reads, and a writable one with no paths has the repository.
	if access := (Stage{Kind: domain.StepAgent}).Access(domain.TaskResearch); access.Permission != domain.PermissionReadOnly || access.AllowedPaths != nil {
		t.Fatalf("research reads: %+v", access)
	}
	if access := (Stage{Kind: domain.StepAgent}).Access(domain.TaskBugfix); access.Permission != domain.PermissionWorktree || strings.Join(access.AllowedPaths, ",") != "." {
		t.Fatalf("a bugfix writes to the repository: %+v", access)
	}
}
