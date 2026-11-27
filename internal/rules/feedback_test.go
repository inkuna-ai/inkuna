package rules

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// feedbackFlow has two writable stages side by side, a check loop on the
// first, and a review gate at the end.
const feedbackFlow = `version: 1
rules: []
default:
  stages:
    - id: plan
      kind: agent
      agent: auto
      permission: read-only
    - id: api
      kind: agent
      agent: auto
      dependsOn: [plan]
    - id: docs
      kind: agent
      agent: auto
      dependsOn: [plan]
    - id: verify
      kind: evaluation
      dependsOn: [api, docs]
      onFailure: feedback
      feedback:
        target: api
        maxRounds: 3
        systemExitCodes: [3, 75]
    - id: lint
      kind: evaluation
      dependsOn: [api, docs]
      onFailure: feedback
    - id: review
      kind: approval
      dependsOn: [docs, verify, lint]
      review: true
`

func plan(t *testing.T, content string, kind domain.TaskKind) (domain.WorkflowSpec, error) {
	t.Helper()
	document, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	return document.Default.Workflow(task(kind), "default", domain.RuntimeCodex, time.Now())
}

func TestFeedbackFieldsReachThePlannedSteps(t *testing.T) {
	spec, err := plan(t, feedbackFlow, domain.TaskImplementation)
	if err != nil {
		t.Fatal(err)
	}
	steps := map[string]domain.WorkflowStep{}
	for _, step := range spec.Steps {
		steps[step.ID] = step
	}
	if verify := steps["verify"]; verify.OnFailure != domain.FailureFeedback || verify.FeedbackTarget != "api" || verify.MaxRounds != 3 || !slices.Equal(verify.SystemExitCodes, []int{3, 75}) {
		t.Errorf("verify names its target, cap, and system exit codes: %+v", verify)
	}
	// Unnamed, the target is the nearest writable agent stage: api and docs
	// are as near, so the earlier in the flow wins.
	if lint := steps["lint"]; lint.FeedbackTarget != "api" || lint.MaxRounds != domain.DefaultFeedbackRounds || lint.SystemExitCodes != nil {
		t.Errorf("lint takes the default target and cap: %+v", lint)
	}
	// docs is one step from review, api two, so docs is nearer though later.
	if review := steps["review"]; !review.Review || review.FeedbackTarget != "docs" || review.OnFailure != "" || review.MaxRounds != 0 {
		t.Errorf("review pauses and sends feedback to the nearest writable stage: %+v", review)
	}
	if plan := steps["plan"]; plan.Review || plan.FeedbackTarget != "" || plan.OnFailure != "" {
		t.Errorf("a stage without feedback fields gets none: %+v", plan)
	}
}

func TestFeedbackFieldsRoundTripThroughYAML(t *testing.T) {
	document, err := Parse(feedbackFlow)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	verify := again.Default.Stages[3]
	if verify.OnFailure != domain.FailureFeedback || verify.Feedback == nil || verify.Feedback.Target != "api" || verify.Feedback.MaxRounds == nil || *verify.Feedback.MaxRounds != 3 || !slices.Equal(verify.Feedback.SystemExitCodes, []int{3, 75}) || !again.Default.Stages[5].Review {
		t.Fatalf("the fields should survive a save:\n%s", data)
	}
}

func TestFeedbackValidationRejectsUnreachableTargetsAndBadCaps(t *testing.T) {
	stages := func(body string) string {
		return "version: 1\nrules: []\ndefault:\n  stages:\n    - id: write\n      kind: agent\n      agent: auto\n    - id: notes\n      kind: agent\n      agent: auto\n      permission: read-only\n    - id: profile\n      kind: script\n" + body + "    - id: approval\n      kind: approval\n      dependsOn: [write]\n"
	}
	cases := map[string]struct{ body, want string }{
		"missing target":     {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: feedback\n      feedback: { target: nowhere }\n", "not a stage of this flow"},
		"downstream target":  {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: feedback\n      feedback: { target: approval }\n", "not upstream"},
		"unrelated target":   {"    - id: verify\n      kind: evaluation\n      dependsOn: [profile]\n      onFailure: feedback\n      feedback: { target: write }\n", "not upstream"},
		"read-only target":   {"    - id: verify\n      kind: evaluation\n      dependsOn: [notes]\n      onFailure: feedback\n      feedback: { target: notes }\n", "read-only"},
		"script target":      {"    - id: verify\n      kind: evaluation\n      dependsOn: [profile]\n      onFailure: feedback\n      feedback: { target: profile }\n", "not an agent stage"},
		"cap too high":       {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: feedback\n      feedback: { maxRounds: 6 }\n", "from 1 to 5"},
		"cap negative":       {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: feedback\n      feedback: { maxRounds: -1 }\n", "from 1 to 5"},
		"cap zero":           {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: feedback\n      feedback: { maxRounds: 0 }\n", "maxRounds to 0; it must be from 1 to 5"},
		"cap on approval":    {"    - id: gate\n      kind: approval\n      dependsOn: [write]\n      feedback: { maxRounds: 2 }\n", "only an evaluation stage"},
		"system code zero":   {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: feedback\n      feedback: { systemExitCodes: [0] }\n", "system exit code 0 must be from 1 to 255"},
		"system code high":   {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: feedback\n      feedback: { systemExitCodes: [256] }\n", "from 1 to 255"},
		"system code twice":  {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: feedback\n      feedback: { systemExitCodes: [3, 3] }\n", "listed twice"},
		"system on approval": {"    - id: gate\n      kind: approval\n      dependsOn: [write]\n      feedback: { systemExitCodes: [3] }\n", "only an evaluation stage"},
		"system no loop":     {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      feedback: { systemExitCodes: [3] }\n", "only with onFailure: feedback"},
		"review on agent":    {"    - id: more\n      kind: agent\n      agent: auto\n      review: true\n", "only an approval stage"},
		"loop on approval":   {"    - id: gate\n      kind: approval\n      onFailure: feedback\n", "only an evaluation stage"},
		"unknown action":     {"    - id: verify\n      kind: evaluation\n      onFailure: retry\n", "use fail or feedback"},
		"feedback no loop":   {"    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      feedback: { target: write }\n", "only with onFailure: feedback"},
		"feedback on agent":  {"    - id: more\n      kind: agent\n      agent: auto\n      feedback: { target: write }\n", "only approval and evaluation"},
		"unknown key":        {"    - id: verify\n      kind: evaluation\n      onFailure: feedback\n      feedback: { rounds: 2 }\n", "rounds"},
	}
	for name, test := range cases {
		if _, err := Parse(stages(test.body)); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: want an error with %q, got %v", name, test.want, err)
		}
	}
	if _, err := Parse(stages("    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: fail\n")); err != nil {
		t.Errorf("onFailure: fail is today's behaviour, spelt out: %v", err)
	}
}

func TestATargetTheTaskMakesReadOnly(t *testing.T) {
	// write has no permission, so research and review tasks make it
	// read-only; only planning can tell.
	named := "version: 1\nrules: []\ndefault:\n  stages:\n    - id: write\n      kind: agent\n      agent: auto\n    - id: approval\n      kind: approval\n      dependsOn: [write]\n      review: true\n      feedback: { target: write }\n"
	if _, err := plan(t, named, domain.TaskImplementation); err != nil {
		t.Fatalf("an implementation task can write: %v", err)
	}
	if _, err := plan(t, named, domain.TaskResearch); err == nil || !strings.Contains(err.Error(), `sends feedback to "write", which is read-only for research tasks`) {
		t.Fatalf("a named target the task makes read-only is a planning error: %v", err)
	}
	unnamed := strings.Replace(named, "      feedback: { target: write }\n", "", 1)
	spec, err := plan(t, unnamed, domain.TaskResearch)
	if err != nil || !spec.Steps[1].Review || spec.Steps[1].FeedbackTarget != "" {
		t.Fatalf("with nothing writable upstream the gate can only be approved: %+v %v", spec.Steps, err)
	}
	loop := "version: 1\nrules: []\ndefault:\n  stages:\n    - id: write\n      kind: agent\n      agent: auto\n    - id: verify\n      kind: evaluation\n      dependsOn: [write]\n      onFailure: feedback\n    - id: approval\n      kind: approval\n      dependsOn: [verify]\n"
	if _, err := plan(t, loop, domain.TaskReview); err == nil || !strings.Contains(err.Error(), "no writable agent stage is upstream of it for review tasks") {
		t.Fatalf("a check loop with nowhere to send failures is a planning error: %v", err)
	}
}

func TestRulesWithoutFeedbackFieldsPlanAsBefore(t *testing.T) {
	document := Default()
	for _, kind := range []domain.TaskKind{domain.TaskImplementation, domain.TaskBugfix, domain.TaskResearch, domain.TaskReview, domain.TaskDocumentation} {
		name, flow := document.Match(task(kind), 0)
		spec, err := flow.Workflow(task(kind), name, domain.RuntimeCodex, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{`"review"`, `"onFailure"`, `"feedbackTarget"`, `"maxRounds"`, `"systemExitCodes"`} {
			if strings.Contains(string(data), key) {
				t.Errorf("%s: the built-in %s flow should plan without %s", kind, name, key)
			}
		}
	}
}
