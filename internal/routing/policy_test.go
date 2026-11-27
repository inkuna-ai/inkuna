package routing

import (
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestPolicyRoutesByComplexityWithinSelectedRuntime(t *testing.T) {
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeCopilot, MaxConcurrency: 2, Steps: []domain.WorkflowStep{
		{ID: "small", Title: "Small", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Complexity: &domain.ComplexityAssessment{Score: 2, Rationale: "bounded change"}},
		{ID: "large", Title: "Large", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Complexity: &domain.ComplexityAssessment{Score: 9, Rationale: "cross-cutting change"}},
	}}
	if err := DefaultPolicy().Apply(&workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.Steps[0].Runtime != domain.RuntimeCopilot || workflow.Steps[0].ModelSelection.Model != "claude-haiku-4.5" {
		t.Fatalf("unexpected low-complexity selection: %#v", workflow.Steps[0])
	}
	if workflow.Steps[1].ModelSelection.Model != "gpt-5.3-codex" {
		t.Fatalf("unexpected high-complexity selection: %#v", workflow.Steps[1])
	}
}

func TestPolicyHonorsRuntimeAndModelOverrides(t *testing.T) {
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeCodex, MaxConcurrency: 1, Steps: []domain.WorkflowStep{{
		ID: "step", Title: "Step", Kind: domain.StepAgent, Runtime: domain.RuntimeClaude, Permission: domain.PermissionReadOnly,
		Complexity:     &domain.ComplexityAssessment{Score: 10, Rationale: "novel architecture"},
		ModelSelection: &domain.ModelSelection{Model: "custom-model", Source: "override", Rationale: "user selected it"},
	}}}
	if err := DefaultPolicy().Apply(&workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.Steps[0].Runtime != domain.RuntimeClaude || workflow.Steps[0].ModelSelection.Model != "custom-model" {
		t.Fatalf("override lost: %#v", workflow.Steps[0])
	}
}

func TestPolicyRejectsUnscoredAgentStep(t *testing.T) {
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeCodex, MaxConcurrency: 1, Steps: []domain.WorkflowStep{{ID: "step", Title: "Step", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly}}}
	if err := DefaultPolicy().Apply(&workflow); err == nil {
		t.Fatal("expected missing score to fail")
	}
}

func TestValidateBandsRequiresFullOrderedCoverage(t *testing.T) {
	for name, bands := range map[string][]Band{
		"gap":       {{Minimum: 1, Maximum: 3, Model: "small"}, {Minimum: 5, Maximum: 10, Model: "large"}},
		"overlap":   {{Minimum: 1, Maximum: 5, Model: "small"}, {Minimum: 5, Maximum: 10, Model: "large"}},
		"short":     {{Minimum: 1, Maximum: 9, Model: "small"}},
		"late":      {{Minimum: 2, Maximum: 10, Model: "small"}},
		"inverted":  {{Minimum: 1, Maximum: 0, Model: "small"}},
		"no model":  {{Minimum: 1, Maximum: 10, Model: " "}},
		"too far":   {{Minimum: 1, Maximum: 11, Model: "small"}},
		"no bands":  {},
		"unordered": {{Minimum: 6, Maximum: 10, Model: "large"}, {Minimum: 1, Maximum: 5, Model: "small"}},
	} {
		if err := ValidateBands(bands); err == nil {
			t.Fatalf("%s: bands were accepted: %#v", name, bands)
		}
	}
	for runtimeName, bands := range DefaultPolicy().Bands {
		if err := ValidateBands(bands); err != nil {
			t.Fatalf("built-in %s bands: %v", runtimeName, err)
		}
	}
}

func TestWithBandsReplacesOnlyTheNamedRuntimes(t *testing.T) {
	custom := []Band{{Minimum: 1, Maximum: 10, Model: "house-model", ReasoningEffort: "medium"}}
	policy := DefaultPolicy().WithBands(map[domain.Runtime][]Band{domain.RuntimeClaude: custom, domain.RuntimeCodex: nil})
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeClaude, MaxConcurrency: 1, Steps: []domain.WorkflowStep{
		{ID: "claude", Title: "Claude", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Complexity: &domain.ComplexityAssessment{Score: 9, Rationale: "hard"}},
		{ID: "codex", Title: "Codex", Kind: domain.StepAgent, Runtime: domain.RuntimeCodex, Permission: domain.PermissionReadOnly, Complexity: &domain.ComplexityAssessment{Score: 9, Rationale: "hard"}},
	}}
	if err := policy.Apply(&workflow); err != nil {
		t.Fatal(err)
	}
	if got := workflow.Steps[0].ModelSelection; got.Model != "house-model" || got.ReasoningEffort != "medium" || got.PolicyVersion != DefaultPolicy().Version {
		t.Fatalf("custom bands were not used: %#v", got)
	}
	if got := workflow.Steps[1].ModelSelection.Model; got != "gpt-6.1-sol" {
		t.Fatalf("a runtime without its own bands must keep the built-in ones, got %q", got)
	}
	if DefaultPolicy().Bands[domain.RuntimeClaude][0].Model == "house-model" {
		t.Fatal("WithBands must not change the built-in policy")
	}
}

// Each built-in score on Claude Code and Codex must buy something its
// neighbour does not: a model covers a run of scores, each point inside it
// raises the effort, and 10 is the top model at xhigh.
func TestDefaultPolicyGivesEachScoreItsOwnModelAndEffort(t *testing.T) {
	efforts := map[string]int{"low": 1, "medium": 2, "high": 3, "xhigh": 4}
	for runtimeName, want := range map[domain.Runtime][]string{
		domain.RuntimeClaude: {"haiku", "haiku", "haiku", "sonnet", "sonnet", "sonnet", "sonnet", "opus", "opus", "opus"},
		domain.RuntimeCodex:  {"gpt-6-luna", "gpt-6-luna", "gpt-6-luna", "gpt-6.1-terra", "gpt-6.1-terra", "gpt-6.1-terra", "gpt-6.1-terra", "gpt-6.1-sol", "gpt-6.1-sol", "gpt-6.1-sol"},
	} {
		bands := DefaultPolicy().Bands[runtimeName]
		if len(bands) != 10 {
			t.Fatalf("%s: want a band per score, got %d bands", runtimeName, len(bands))
		}
		for index, band := range bands {
			score := index + 1
			if band.Minimum != score || band.Maximum != score || band.Model != want[index] {
				t.Fatalf("%s score %d: got %d-%d %s", runtimeName, score, band.Minimum, band.Maximum, band.Model)
			}
			if efforts[band.ReasoningEffort] == 0 {
				t.Fatalf("%s score %d: effort %q is not one both CLIs take", runtimeName, score, band.ReasoningEffort)
			}
			if index > 0 && bands[index-1].Model == band.Model && efforts[band.ReasoningEffort] <= efforts[bands[index-1].ReasoningEffort] {
				t.Fatalf("%s score %d: %s effort %s does not rise from %s", runtimeName, score, band.Model, band.ReasoningEffort, bands[index-1].ReasoningEffort)
			}
		}
		if bands[0].ReasoningEffort != "medium" || bands[9].ReasoningEffort != "xhigh" {
			t.Fatalf("%s: want medium at 1 and xhigh at 10, got %s and %s", runtimeName, bands[0].ReasoningEffort, bands[9].ReasoningEffort)
		}
	}
}

func TestPolicyRationaleNamesTheModelAndEffortOfASingleScoreBand(t *testing.T) {
	policy := DefaultPolicy().WithBands(map[domain.Runtime][]Band{domain.RuntimeCopilot: {
		{Minimum: 1, Maximum: 4, Model: "small"},
		{Minimum: 5, Maximum: 5, Model: "middle"},
		{Minimum: 6, Maximum: 10, Model: "large"},
	}})
	workflow := domain.WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: domain.RuntimeCodex, MaxConcurrency: 1, Steps: []domain.WorkflowStep{
		{ID: "codex", Title: "Codex", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Complexity: &domain.ComplexityAssessment{Score: 6, Rationale: "a few files"}},
		{ID: "single", Title: "Single", Kind: domain.StepAgent, Runtime: domain.RuntimeCopilot, Permission: domain.PermissionReadOnly, Complexity: &domain.ComplexityAssessment{Score: 5, Rationale: "middling"}},
		{ID: "range", Title: "Range", Kind: domain.StepAgent, Runtime: domain.RuntimeCopilot, Permission: domain.PermissionReadOnly, Complexity: &domain.ComplexityAssessment{Score: 2, Rationale: "small"}},
	}}
	if err := policy.Apply(&workflow); err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{
		"complexity 6 routes codex to gpt-6.1-terra at high effort",
		"complexity 5 routes copilot to middle",
		"complexity 2 falls in the 1-4 copilot band",
	} {
		if got := workflow.Steps[index].ModelSelection.Rationale; got != want {
			t.Fatalf("step %d: rationale %q, want %q", index, got, want)
		}
	}
	if got := workflow.Steps[0].ModelSelection; got.ReasoningEffort != "high" || got.PolicyVersion != 2 {
		t.Fatalf("score 6 on codex: %#v", got)
	}
}
