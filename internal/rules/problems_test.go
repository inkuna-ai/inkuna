package rules

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// A refused document says which flow and which entry, as written, the
// mistake is in, so the editor can point at it; its text is unchanged.
func TestAMistakeInAFlowRecordsTheRuleAndTheEntry(t *testing.T) {
	const shared = "version: 2\nstages:\n  verify:\n    kind: evaluation\ngroups:\n  check:\n    stages:\n      - use: verify\n      - id: approval\n        kind: approval\n        dependsOn: [verify]\n"
	cases := map[string]struct {
		content string
		rule    int
		entry   int
		says    string
	}{
		"an unknown name": {
			shared + "rules:\n  - name: first\n    then:\n      stages:\n        - id: approval\n          kind: approval\n  - name: docs\n    then:\n      stages:\n        - id: write\n          kind: agent\n          agent: auto\n        - use: verfy\n        - id: approval\n          kind: approval\n",
			1, 1, `rule "docs": entry 2 (use "verfy"): no shared stage or group is named "verfy"`,
		},
		"an override the kind does not have": {
			shared + "rules:\n  - name: docs\n    then:\n      stages:\n        - use: verify\n          agent: writer\n        - id: approval\n          kind: approval\n",
			0, 0, `overrides agent, which an evaluation stage does not have`,
		},
		"a dangling dependsOn on an expanded stage": {
			shared + "rules:\n  - name: docs\n    then:\n      stages:\n        - id: write\n          kind: agent\n          agent: auto\n        - use: check\n          dependsOn: [nothing]\n",
			0, 1, `stage "verify" (from shared stage "verify" in group "check") depends on "nothing" which must be declared earlier`,
		},
		"a duplicate id": {
			shared + "rules:\n  - name: docs\n    then:\n      stages:\n        - use: verify\n        - use: verify\n        - id: approval\n          kind: approval\n",
			0, 1, `entry 1 (use "verify") and entry 2 (use "verify") both give the id "verify"`,
		},
		"an inline stage after a group": {
			shared + "rules:\n  - name: docs\n    then:\n      stages:\n        - use: check\n        - id: write\n          kind: agent\n          agent: auto\n          permission: admin\n        - id: approve\n          kind: approval\n",
			0, 1, `invalid permission "admin"`,
		},
		"the default flow": {
			shared + "rules: []\ndefault:\n  stages:\n    - use: approval\n    - use: verify\n      dependsOn: [approval]\n",
			-1, 1, `default flow: the final stage must be an approval`,
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(test.content)
			var flow *FlowError
			if !errors.As(err, &flow) {
				t.Fatalf("Parse error = %v, want a FlowError", err)
			}
			if flow.Rule != test.rule || flow.Entry != test.entry {
				t.Fatalf("located at rule %d entry %d, want rule %d entry %d: %v", flow.Rule, flow.Entry, test.rule, test.entry, err)
			}
			if !strings.Contains(err.Error(), test.says) {
				t.Fatalf("error = %q, want it to say %q", err, test.says)
			}
		})
	}
}

// A mistake about a flow as a whole names no entry.
func TestAMistakeAboutAWholeFlowNamesNoEntry(t *testing.T) {
	_, err := Parse("version: 1\nrules:\n  - name: docs\n    then:\n      stages:\n        - id: write\n          kind: agent\n          agent: auto\n")
	var flow *FlowError
	if !errors.As(err, &flow) || flow.Rule != 0 || flow.Entry != -1 {
		t.Fatalf("error = %v (%+v), want rule 0 and no entry", err, flow)
	}
	if err.Error() != `rule "docs": flow must end with an approval stage so no work lands unreviewed` {
		t.Fatalf("error text changed: %q", err)
	}
}

// A mistake in one of the file's own shared stages or groups names it.
func TestAMistakeInADefinitionNamesIt(t *testing.T) {
	_, err := Parse("version: 2\nstages:\n  lint:\n    kind: agent\n    agent: auto\n    permission: admin\nrules: []\n")
	var definition *DefinitionError
	if !errors.As(err, &definition) || definition.Name != "lint" {
		t.Fatalf("error = %v, want a DefinitionError for lint", err)
	}
}

// Usages says which flows use each definition, through groups too, and
// skips a name that does not resolve rather than hiding the rest.
func TestUsagesListEachFlowsDefinitionsAndSkipBrokenNames(t *testing.T) {
	document := Document{
		Version: 2,
		Stages:  map[string]Stage{"lint": {Kind: domain.StepEvaluation}},
		Groups:  map[string]Group{"check": {Stages: []Stage{{Use: "lint"}, {Use: "approval", DependsOn: []string{"lint"}}}}},
		Rules: []Rule{
			{Name: "docs", Then: Flow{Stages: []Stage{{Use: "check"}}}},
			{Name: "broken", Then: Flow{Stages: []Stage{{Use: "missing"}, {Use: "verify"}}}},
		},
		Default: &Flow{Stages: []Stage{{ID: "approval", Kind: domain.StepApproval}}},
	}
	var got []string
	for _, usage := range document.Usages() {
		got = append(got, usage.Flow+":"+usage.Definition.Name+"@"+usage.Definition.Scope)
	}
	want := []string{"docs:approval@builtin", "docs:check@repository", "docs:lint@repository", "broken:verify@builtin"}
	if !slices.Equal(got, want) {
		t.Fatalf("Usages = %v, want %v", got, want)
	}
}

// The editor offers the overrides Go accepts, with the kinds each applies
// to.
func TestOverridableListsEachFieldAndItsKinds(t *testing.T) {
	fields := Overridable()
	if len(fields) != len(overridable) {
		t.Fatalf("Overridable lists %d fields, want %d", len(fields), len(overridable))
	}
	commands := fields[slices.IndexFunc(fields, func(field OverridableField) bool { return field.Name == "commands" })]
	if !slices.Equal(commands.Kinds, []domain.StepKind{domain.StepAgent, domain.StepEvaluation}) {
		t.Fatalf("commands applies to %v", commands.Kinds)
	}
	fields[0].Kinds = append(fields[0].Kinds, domain.StepScript)
	if len(overridable[0].kinds) != 0 {
		t.Fatal("changing the list Overridable returned changed Go's own")
	}
}
