package rules

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// library is a version 2 preamble: three shared stages, a group of two of
// them, and a group that nests it.
const library = `version: 2
stages:
  verify:
    kind: evaluation
    commands:
      - [task, ci]
  review:
    kind: agent
    agent: reviewer
    permission: read-only
    skills: [conventions]
  approval:
    kind: approval
    review: true
groups:
  check:
    stages:
      - use: verify
      - use: review
        dependsOn: [verify]
  gate:
    stages:
      - use: check
      - use: approval
        dependsOn: [check]
`

// mustParse parses content, failing the test when it does not parse.
func mustParse(t *testing.T, content string) Document {
	t.Helper()
	document, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return document
}

// shape lists a flow's stages as "id<-dependency,dependency".
func shape(flow Flow) []string {
	lines := make([]string, len(flow.Stages))
	for index, stage := range flow.Stages {
		lines[index] = stage.ID + "<-" + strings.Join(stage.DependsOn, ",")
	}
	return lines
}

// named is the expanded flow of the rule called name, failing the test when
// there is none or it does not validate.
func named(t *testing.T, document Document, name string) Flow {
	t.Helper()
	flow, ok := document.Named(name)
	if !ok {
		t.Fatalf("no flow %q", name)
	}
	if err := flow.Validate(); err != nil {
		t.Fatalf("flow %q does not validate once expanded: %v", name, err)
	}
	return flow
}

// What a use overrides changes that use only: the shared stage and its other
// uses keep their own values.
func TestASharedStageIsOverriddenPerUse(t *testing.T) {
	document := mustParse(t, library+`rules:
  - name: docs
    when:
      kind: [documentation]
    then:
      stages:
        - id: write
          kind: agent
          agent: documenter
        - use: verify
          dependsOn: [write]
          commands:
            - [go, test, ./internal/docs/...]
        - use: approval
          dependsOn: [verify]
          review: false
default:
  stages:
    - id: implement
      kind: agent
      agent: auto
    - use: verify
      id: check-all
      dependsOn: [implement]
      continueOnFailure: true
    - use: approval
      dependsOn: [check-all]
`)
	docs := named(t, document, "docs")
	fallback := named(t, document, DefaultName)
	if got, want := shape(docs), []string{"write<-", "verify<-write", "approval<-verify"}; !slices.Equal(got, want) {
		t.Fatalf("docs flow = %v, want %v", got, want)
	}
	if got, want := shape(fallback), []string{"implement<-", "check-all<-implement", "approval<-check-all"}; !slices.Equal(got, want) {
		t.Fatalf("default flow = %v, want %v", got, want)
	}

	verifyDocs, verifyAll := docs.Stages[1], fallback.Stages[1]
	if verifyDocs.Kind != domain.StepEvaluation || !reflect.DeepEqual(verifyDocs.Commands, [][]string{{"go", "test", "./internal/docs/..."}}) || verifyDocs.ContinueOnFailure {
		t.Fatalf("docs verify should run only its own commands: %+v", verifyDocs)
	}
	if !reflect.DeepEqual(verifyAll.Commands, [][]string{{"task", "ci"}}) || !verifyAll.ContinueOnFailure {
		t.Fatalf("default verify should keep the shared commands and continue on failure: %+v", verifyAll)
	}
	// An override written as false still overrides.
	if docs.Stages[2].Review || !fallback.Stages[2].Review {
		t.Fatalf("review: false must override the shared review gate in docs only: docs %v, default %v", docs.Stages[2].Review, fallback.Stages[2].Review)
	}
	if want := (&Provenance{Stage: "verify", Scope: ScopeRepository, Overrides: []string{"commands"}}); !reflect.DeepEqual(verifyDocs.From, want) {
		t.Fatalf("docs verify provenance = %+v, want %+v", verifyDocs.From, want)
	}
	if want := (&Provenance{Stage: "approval", Scope: ScopeRepository, Overrides: []string{"review"}}); !reflect.DeepEqual(docs.Stages[2].From, want) {
		t.Fatalf("docs approval provenance = %+v, want %+v", docs.Stages[2].From, want)
	}
	if docs.Stages[0].From != nil {
		t.Fatalf("an inline stage has no provenance: %+v", docs.Stages[0].From)
	}
	// One use's overrides never reach the definition or another use.
	if shared := document.Stages["verify"]; !reflect.DeepEqual(shared.Commands, [][]string{{"task", "ci"}}) || shared.ContinueOnFailure {
		t.Fatalf("expansion changed the shared definition: %+v", shared)
	}

	workflow, err := docs.Workflow(task(domain.TaskDocumentation), "docs", domain.RuntimeClaude, time.Now())
	if err != nil {
		t.Fatalf("expanded flow does not plan: %v", err)
	}
	if step := workflow.Steps[1]; step.ID != "verify" || step.Kind != domain.StepEvaluation || len(step.Commands) != 1 {
		t.Fatalf("expanded verify planned as %+v", step)
	}
}

// A group's first stages take the use's dependencies, and what depends on the
// use waits for the group's last stages.
func TestAGroupIsWiredBetweenTwoInlineStages(t *testing.T) {
	document := mustParse(t, library+`rules: []
default:
  stages:
    - id: implement
      kind: agent
      agent: auto
    - use: check
      dependsOn: [implement]
    - id: approval
      kind: approval
      dependsOn: [check]
`)
	flow := named(t, document, DefaultName)
	if got, want := shape(flow), []string{"implement<-", "verify<-implement", "review<-verify", "approval<-review"}; !slices.Equal(got, want) {
		t.Fatalf("expanded flow = %v, want %v", got, want)
	}
	if want := (&Provenance{Stage: "review", Groups: []string{"check"}, Scope: ScopeRepository}); !reflect.DeepEqual(flow.Stages[2].From, want) {
		t.Fatalf("review provenance = %+v, want %+v", flow.Stages[2].From, want)
	}
}

// Each use of a group gets its stages under its own id, so the stage ids of
// two uses never collide.
func TestAGroupUsedTwiceIsPrefixedWithEachUsesID(t *testing.T) {
	document := mustParse(t, library+`rules: []
default:
  stages:
    - id: implement
      kind: agent
      agent: auto
    - use: check
      id: first
      dependsOn: [implement]
    - id: fix
      kind: agent
      agent: auto
      dependsOn: [first]
    - use: check
      id: second
      dependsOn: [fix]
    - id: approval
      kind: approval
      dependsOn: [second, first.review]
`)
	flow := named(t, document, DefaultName)
	want := []string{
		"implement<-",
		"first.verify<-implement", "first.review<-first.verify",
		"fix<-first.review",
		"second.verify<-fix", "second.review<-second.verify",
		"approval<-second.review,first.review",
	}
	if got := shape(flow); !slices.Equal(got, want) {
		t.Fatalf("expanded flow = %v, want %v", got, want)
	}
}

// A group inside a group expands with the same wiring as one used directly.
func TestNestedGroupsExpandWithTheirWiring(t *testing.T) {
	document := mustParse(t, library+`rules:
  - name: once
    then:
      stages:
        - id: implement
          kind: agent
          agent: auto
        - use: gate
          dependsOn: [implement]
  - name: twice
    then:
      stages:
        - id: implement
          kind: agent
          agent: auto
        - use: gate
          id: a
          dependsOn: [implement]
        - use: gate
          id: b
          dependsOn: [a]
`)
	once := named(t, document, "once")
	if got, want := shape(once), []string{"implement<-", "verify<-implement", "review<-verify", "approval<-review"}; !slices.Equal(got, want) {
		t.Fatalf("once = %v, want %v", got, want)
	}
	if want := (&Provenance{Stage: "verify", Groups: []string{"gate", "check"}, Scope: ScopeRepository}); !reflect.DeepEqual(once.Stages[1].From, want) {
		t.Fatalf("nested provenance = %+v, want %+v", once.Stages[1].From, want)
	}
	if want := (&Provenance{Stage: "approval", Groups: []string{"gate"}, Scope: ScopeRepository}); !reflect.DeepEqual(once.Stages[3].From, want) {
		t.Fatalf("approval provenance = %+v, want %+v", once.Stages[3].From, want)
	}
	twice := named(t, document, "twice")
	want := []string{
		"implement<-",
		"a.verify<-implement", "a.review<-a.verify", "a.approval<-a.review",
		"b.verify<-a.approval", "b.review<-b.verify", "b.approval<-b.review",
	}
	if got := shape(twice); !slices.Equal(got, want) {
		t.Fatalf("twice = %v, want %v", got, want)
	}
	if label := twice.Stages[1].Label(); label != `"a.verify" (from shared stage "verify" in group "check" in group "gate")` {
		t.Fatalf("Label = %s", label)
	}
}

// A feedback target inside a group follows its stage to each use's id, and
// the group's own target is left as written.
func TestFeedbackInsideAGroupFollowsItsStage(t *testing.T) {
	document := mustParse(t, `version: 2
groups:
  loop:
    stages:
      - id: implement
        kind: agent
        agent: auto
        permission: worktree-write
      - id: check
        kind: evaluation
        onFailure: feedback
        feedback:
          target: implement
        dependsOn: [implement]
rules: []
default:
  stages:
    - use: loop
      id: api
    - use: loop
      id: ui
      dependsOn: [api]
    - id: approval
      kind: approval
      dependsOn: [ui]
`)
	flow := named(t, document, DefaultName)
	if got, want := shape(flow), []string{"api.implement<-", "api.check<-api.implement", "ui.implement<-api.check", "ui.check<-ui.implement", "approval<-ui.check"}; !slices.Equal(got, want) {
		t.Fatalf("expanded flow = %v, want %v", got, want)
	}
	if flow.Stages[1].Feedback.Target != "api.implement" || flow.Stages[3].Feedback.Target != "ui.implement" {
		t.Fatalf("feedback targets = %q, %q", flow.Stages[1].Feedback.Target, flow.Stages[3].Feedback.Target)
	}
	if document.Groups["loop"].Stages[1].Feedback.Target != "implement" {
		t.Fatal("expansion changed the group's own feedback target")
	}
}

// A bad reference is refused with the rule, the entry, and the name it used,
// so the person can find it in the file.
func TestReferenceErrorsNameTheRuleTheEntryAndTheReference(t *testing.T) {
	flow := func(stages string) string {
		return library + "rules:\n  - name: docs\n    then:\n      stages:\n" + stages
	}
	const write = "        - id: write\n          kind: agent\n          agent: documenter\n"
	const approve = "        - id: approve\n          kind: approval\n          dependsOn: [write]\n"
	cases := map[string]struct{ content, says string }{
		"unknown name": {
			flow(write + "        - use: verfy\n          dependsOn: [write]\n" + approve),
			`rule "docs": entry 2 (use "verfy"): no shared stage or group is named "verfy"`,
		},
		"group using itself": {
			"version: 2\ngroups:\n  loop:\n    stages:\n      - use: loop\nrules: []\n",
			`group "loop" uses itself`,
		},
		"group using itself through another": {
			"version: 2\ngroups:\n  a:\n    stages:\n      - use: b\n  b:\n    stages:\n      - use: a\nrules: []\n",
			`group "a" uses itself: a → b → a`,
		},
		"a stage used twice without ids": {
			flow(write + "        - use: verify\n          dependsOn: [write]\n        - use: verify\n          dependsOn: [write]\n" + approve),
			`rule "docs": entry 2 (use "verify") and entry 3 (use "verify") both give the id "verify"; set id on one of them`,
		},
		"a group used twice without ids": {
			flow(write + "        - use: check\n          dependsOn: [write]\n        - use: check\n          dependsOn: [write]\n" + approve),
			`entry 2 (use "check") and entry 3 (use "check") both give the id "check"`,
		},
		"a use whose id an inline stage has": {
			flow(write + "        - use: verify\n          id: write\n" + approve),
			`entry 1 (stage "write") and entry 2 (use "verify", id "write") both give the id "write"`,
		},
		"an override the kind does not have": {
			flow(write + "        - use: verify\n          dependsOn: [write]\n          agent: reviewer\n" + approve),
			`rule "docs": entry 2 (use "verify"): overrides agent, which an evaluation stage does not have`,
		},
		"commands on an approval": {
			flow(write + "        - use: approval\n          dependsOn: [write]\n          commands:\n            - [task, ci]\n"),
			`entry 2 (use "approval"): overrides commands, which an approval stage does not have`,
		},
		"an override on a group use": {
			flow(write + "        - use: check\n          dependsOn: [write]\n          title: Check\n" + approve),
			`entry 2 (use "check"): sets title, but a use of a group takes only id and dependsOn`,
		},
		"a kind on a use": {
			flow(write + "        - use: verify\n          kind: agent\n          dependsOn: [write]\n" + approve),
			`entry 2 (use "verify"): sets kind, but a use keeps the kind of "verify"`,
		},
		"a dangling dependsOn in a flow": {
			flow(write + "        - use: verify\n          dependsOn: [nothing]\n" + approve),
			`rule "docs": stage "verify" (from shared stage "verify") depends on "nothing" which must be declared earlier`,
		},
		"a dependsOn on a group declared later": {
			flow(write + "        - use: verify\n          dependsOn: [check]\n        - use: check\n          id: check\n          dependsOn: [write]\n" + approve),
			`stage "verify" (from shared stage "verify") depends on "check" which must be declared earlier`,
		},
		"a dangling dependsOn in a group": {
			strings.Replace(library, "        dependsOn: [verify]\n", "        dependsOn: [nothing]\n", 1) + "rules: []\n",
			`group "check": entry 2 (use "review") depends on "nothing", which is not an earlier entry of the group`,
		},
		"an override that breaks the stage": {
			flow(write + "        - use: review\n          dependsOn: [write]\n          permission: admin\n" + approve),
			`rule "docs": stage "review" (from shared stage "review") has invalid permission "admin"`,
		},
		"an override that is a shell": {
			flow(write + "        - use: verify\n          dependsOn: [write]\n          commands:\n            - [sh, -c, task ci]\n" + approve),
			`stage "verify" (from shared stage "verify"): `,
		},
		"shared stages in version 1": {
			strings.Replace(library, "version: 2", "version: 1", 1) + "rules: []\n",
			"shared stages and groups need rules version 2; this document is version 1",
		},
		"a use in version 1": {
			"version: 1\nrules: []\ndefault:\n  stages:\n    - use: approval\n",
			"shared stages and groups need rules version 2",
		},
		"a newer version": {
			"version: 3\nrules: []\nsomethingNew: true\n",
			"rules version 3 is newer than this build of Inkuna reads (up to 2); update Inkuna",
		},
		"a shared stage that wires itself": {
			"version: 2\nstages:\n  verify:\n    kind: evaluation\n    dependsOn: [build]\nrules: []\n",
			`shared stage "verify" sets dependsOn; each use wires it into its flow`,
		},
		"a shared stage with an id": {
			"version: 2\nstages:\n  verify:\n    id: check\n    kind: evaluation\nrules: []\n",
			`shared stage "verify" sets id`,
		},
		"a shared stage that uses another": {
			"version: 2\nstages:\n  verify:\n    use: check\nrules: []\n",
			`shared stage "verify" uses "check"`,
		},
		"an incomplete shared stage": {
			"version: 2\nstages:\n  write:\n    kind: agent\nrules: []\n",
			`shared stage "write": agent stage "write" needs an agent name or auto`,
		},
		"a stage and a group with one name": {
			"version: 2\nstages:\n  check:\n    kind: evaluation\ngroups:\n  check:\n    stages:\n      - use: check\nrules: []\n",
			`"check" is both a shared stage and a group`,
		},
		"an empty group": {
			"version: 2\ngroups:\n  empty:\n    stages: []\nrules: []\n",
			`group "empty" has no stages`,
		},
		"a group stage without an id": {
			"version: 2\ngroups:\n  check:\n    stages:\n      - kind: evaluation\nrules: []\n",
			`group "check": entry 1: needs an id`,
		},
		"an unused group with a broken stage": {
			"version: 2\ngroups:\n  check:\n    stages:\n      - id: run\n        kind: script\n        review: true\nrules: []\n",
			`group "check": stage "run" (from group "check") sets review, which only an approval stage has`,
		},
	}
	for name, test := range cases {
		_, err := Parse(test.content)
		if err == nil || !strings.Contains(err.Error(), test.says) {
			t.Errorf("%s: got %v, want an error saying %q", name, err, test.says)
		}
	}
}

// Loading and saving a document keeps its uses, overrides, and definitions
// as written rather than writing them expanded.
func TestReferencesSurviveALoadAndSaveUnchanged(t *testing.T) {
	const content = library + `rules:
  - name: docs
    when:
      kind: [documentation]
    then:
      stages:
        - id: write
          kind: agent
          agent: documenter
        - use: verify
          id: verify-docs
          dependsOn: [write]
          commands:
            - [go, test, ./internal/docs/...]
        - use: gate
          dependsOn: [verify-docs]
default:
  stages:
    - id: implement
      kind: agent
      agent: auto
    - use: check
      dependsOn: [implement]
    - use: approval
      dependsOn: [check]
      review: false
`
	document := mustParse(t, content)
	data, err := Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	written := string(data)
	for _, want := range []string{"use: verify\n", "id: verify-docs\n", "use: gate\n", "review: false\n", "use: check\n"} {
		if !strings.Contains(written, want) {
			t.Errorf("saved YAML lacks %q:\n%s", want, written)
		}
	}
	// Expanded stages would repeat the definitions' fields in the flows.
	if count := strings.Count(written, "kind: evaluation"); count != 1 {
		t.Errorf("saved YAML expands the uses (%d evaluation stages):\n%s", count, written)
	}
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := Write(path, document); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := LoadPath(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Source, document.Source = "", ""
	if !reflect.DeepEqual(reloaded, document) {
		t.Fatalf("a load and save changed the document:\n got %#v\nwant %#v", reloaded, document)
	}
	again, err := Marshal(reloaded)
	if err != nil || !bytes.Equal(again, data) {
		t.Fatalf("saving the reloaded document wrote something else (%v):\n%s\nwant\n%s", err, again, data)
	}

	// The view sends the document back as JSON; the overrides it carries
	// keep review: false a use's own.
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var fromView Document
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fromView); err != nil {
		t.Fatal(err)
	}
	if viaView, err := Marshal(fromView); err != nil || !bytes.Equal(viaView, data) {
		t.Fatalf("a document sent back by the view saves differently (%v):\n%s", err, viaView)
	}
}

// An override is refused for a field the shared stage's kind does not have,
// even when its value is empty.
func TestAnOverrideTheKindDoesNotHaveIsRefusedEvenWhenEmpty(t *testing.T) {
	_, err := Parse(library + "rules: []\ndefault:\n  stages:\n    - id: write\n      kind: agent\n      agent: auto\n    - use: verify\n      dependsOn: [write]\n      skills: []\n    - use: approval\n      dependsOn: [verify]\n")
	if err == nil || !strings.Contains(err.Error(), "overrides skills, which an evaluation stage does not have") {
		t.Fatalf("an empty override is still an override: %v", err)
	}
}

// The rules Inkuna ships and plans from use no shared stages, so they must
// expand to exactly what they were and plan exactly as before.
func TestRulesWithoutUsesExpandAndPlanAsBefore(t *testing.T) {
	documents := map[string]Document{"sample": mustParse(t, sample)}
	for _, path := range []string{"../../.inkuna/rules.yaml", "../../examples/inkuna/rules.yaml"} {
		document, exists, err := LoadPath(path)
		if err != nil || !exists {
			t.Fatalf("%s: exists %v, err %v", path, exists, err)
		}
		documents[path] = document
	}
	for name, document := range documents {
		if version := document.RequiredVersion(); version != 1 {
			t.Errorf("%s needs version %d, want 1", name, version)
		}
		expanded, err := document.Expand()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		flows := map[string]Flow{DefaultName: *document.Default}
		for index, rule := range document.Rules {
			flows[rule.Name] = rule.Then
			if !reflect.DeepEqual(expanded.Rules[index].Then, rule.Then) {
				t.Errorf("%s: rule %s expands to something else", name, rule.Name)
			}
		}
		if !reflect.DeepEqual(*expanded.Default, *document.Default) {
			t.Errorf("%s: the default flow expands to something else", name)
		}
		now := time.Now()
		for rule, flow := range flows {
			planned, _ := document.Named(rule)
			want, wantErr := flow.Workflow(task(domain.TaskImplementation), rule, domain.RuntimeClaude, now)
			got, gotErr := planned.Workflow(task(domain.TaskImplementation), rule, domain.RuntimeClaude, now)
			if (wantErr == nil) != (gotErr == nil) || !reflect.DeepEqual(got, want) {
				t.Errorf("%s: rule %s plans differently once expanded: %v / %v", name, rule, gotErr, wantErr)
			}
		}
	}
}

// Every stage field is either one a use overrides or one that wires the use
// in, so a field added to Stage cannot be silently left out of overrides.
func TestEveryStageFieldIsOverridableOrWiresTheUse(t *testing.T) {
	wiring := map[string]bool{"use": true, "id": true, "kind": true, "dependsOn": true}
	for name := range stageFields {
		if !wiring[name] && !overridableName(name) {
			t.Errorf("stage field %q is neither overridable nor part of a use", name)
		}
	}
	for _, field := range overridable {
		if _, ok := stageFields[field.name]; !ok {
			t.Errorf("overridable field %q is not a stage field", field.name)
		}
	}
}

// Validate is for expanded flows: a use left in one is refused, and Named
// always expands.
func TestFlowValidateRefusesAnUnexpandedUse(t *testing.T) {
	document := mustParse(t, library+"rules: []\ndefault:\n  stages:\n    - use: approval\n")
	err := document.Default.Validate()
	if err == nil || !strings.Contains(err.Error(), `entry 1 uses "approval"`) {
		t.Fatalf("an unexpanded use must not validate as a stage: %v", err)
	}
	if flow, _ := document.Named(DefaultName); flow.Validate() != nil || flow.Stages[0].ID != "approval" {
		t.Fatalf("Named must expand: %+v", flow)
	}
}

// Expand leaves plain stages that record where each came from, and nothing
// more to expand.
func TestExpandDropsTheDefinitionsAndKeepsProvenance(t *testing.T) {
	document := mustParse(t, library+"rules: []\ndefault:\n  stages:\n    - id: implement\n      kind: agent\n      agent: auto\n    - use: gate\n      dependsOn: [implement]\n")
	expanded, err := document.Expand()
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Stages != nil || expanded.Groups != nil || expanded.RequiredVersion() != 1 {
		t.Fatalf("expanded document keeps definitions or uses: %+v", expanded)
	}
	if len(expanded.Default.Stages) != 4 || expanded.Default.Stages[3].From == nil {
		t.Fatalf("expanded default = %+v", expanded.Default.Stages)
	}
	// Expanding what is already expanded changes nothing.
	again, err := expanded.Expand()
	if err != nil || !reflect.DeepEqual(again, expanded) {
		t.Fatalf("expanding twice changed the document: %v", err)
	}
}
