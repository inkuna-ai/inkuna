package catalog

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// fixture is a small catalog: two agents, a shared stage naming one, a
// group using it, and a rule using the group and naming the other agent.
func fixture() fstest.MapFS {
	return fstest.MapFS{
		"agents/schema-reviewer.md": {Data: []byte(`---
name: schema-reviewer
description: Reviews schema and API changes.
permission: read-only
taskKinds: [review]
---

Review the schema change.
`)},
		"agents/schema-writer.md": {Data: []byte(`---
name: schema-writer
description: Writes schema migrations.
permission: worktree-write
taskKinds: [implementation]
---

Write the migration.
`)},
		"stages/schema-review.yaml": {Data: []byte(`# Reviews a schema change, read-only.
version: 2
stages:
  schema-review:
    kind: agent
    agent: schema-reviewer
    permission: read-only
`)},
		"groups/review-and-approve.yaml": {Data: []byte(`# Verifies, reviews the schema, then approves.

version: 2
groups:
  review-and-approve:
    stages:
      - use: verify
      - use: schema-review
        dependsOn: [verify]
      - use: approval
        dependsOn: [schema-review]
`)},
		"rules/schema-change.yaml": {Data: []byte(`version: 2
rules:
  - name: schema-change
    description: Migrations go to the schema writer, then a schema review.
    when:
      kind: [implementation]
      keywords: [migration]
    then:
      stages:
        - use: profile
        - id: migrate
          kind: agent
          agent: schema-writer
          dependsOn: [profile]
        - use: review-and-approve
          dependsOn: [migrate]
`)},
	}
}

func TestLoadReadsEveryItemWithWhatItNeeds(t *testing.T) {
	catalog, err := Load(fixture())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, item := range catalog.Items() {
		names = append(names, string(item.Kind)+":"+item.Name)
	}
	if want := []string{"group:review-and-approve", "rule:schema-change", "stage:schema-review", "agent:schema-reviewer", "agent:schema-writer"}; !slices.Equal(names, want) {
		t.Fatalf("items = %v, want %v", names, want)
	}
	rule, _ := catalog.Get("schema-change")
	if want := []string{"schema-reviewer", "schema-writer"}; !slices.Equal(rule.Needs.Agents, want) {
		t.Errorf("rule needs agents %v, want %v", rule.Needs.Agents, want)
	}
	if want := []string{"approval", "profile", "review-and-approve", "schema-review", "verify"}; !slices.Equal(rule.Needs.Stages, want) {
		t.Errorf("rule needs stages %v, want %v", rule.Needs.Stages, want)
	}
	if rule.Description != "Migrations go to the schema writer, then a schema review." {
		t.Errorf("rule description = %q", rule.Description)
	}
	// A comment followed by a blank line and one that runs into the first
	// key are both the description.
	if group, _ := catalog.Get("review-and-approve"); group.Description != "Verifies, reviews the schema, then approves." {
		t.Errorf("group description = %q", group.Description)
	}
	if stage, _ := catalog.Get("schema-review"); stage.Description != "Reviews a schema change, read-only." || !slices.Equal(stage.Needs.Agents, []string{"schema-reviewer"}) {
		t.Errorf("stage = %q needing %v", stage.Description, stage.Needs.Agents)
	}
	if reviewer, _ := catalog.Get("schema-reviewer"); len(reviewer.Needs.Agents)+len(reviewer.Needs.Stages) != 0 || reviewer.Content == "" {
		t.Errorf("agent needs %+v, content %q", reviewer.Needs, reviewer.Content)
	}
}

func TestEmbeddedCatalogLoads(t *testing.T) {
	if _, err := Builtin(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRefusesWhatIsNotAWellFormedItem(t *testing.T) {
	agentFile := func(name, extra string) []byte {
		return []byte("---\nname: " + name + "\ndescription: An agent.\n" + extra + "---\n\nDo it.\n")
	}
	ruleWith := func(body string) []byte {
		return []byte("version: 2\nrules:\n  - name: schema-change\n    description: A rule.\n" + body)
	}
	cases := []struct {
		name    string
		change  fstest.MapFS
		remove  []string
		message string
	}{
		{"agent without a description", fstest.MapFS{"agents/schema-reviewer.md": {Data: []byte("---\nname: schema-reviewer\n---\n\nDo it.\n")}}, nil, "needs a description"},
		{"agent with a key Inkuna does not read", fstest.MapFS{"agents/schema-reviewer.md": {Data: agentFile("schema-reviewer", "color: blue\n")}}, nil, "frontmatter keys Inkuna does not read"},
		{"agent named unlike its file", fstest.MapFS{"agents/schema-reviewer.md": {Data: agentFile("other-reviewer", "")}}, nil, "must match file name"},
		{"stage with an unknown key", fstest.MapFS{"stages/schema-review.yaml": {Data: []byte("# A stage.\nversion: 2\nstages:\n  schema-review:\n    kind: approval\n    colour: red\n")}}, nil, "field colour not found"},
		{"stage file with two stages", fstest.MapFS{"stages/schema-review.yaml": {Data: []byte("# Two.\nversion: 2\nstages:\n  schema-review:\n    kind: approval\n  other:\n    kind: approval\n")}}, nil, "defines exactly one stage"},
		{"stage in the groups directory", fstest.MapFS{"groups/review-and-approve.yaml": {Data: []byte("# A stage.\nversion: 2\nstages:\n  review-and-approve:\n    kind: approval\n")}}, nil, "defines exactly one group"},
		{"stage without a description", fstest.MapFS{"stages/schema-review.yaml": {Data: []byte("version: 2\nstages:\n  schema-review:\n    kind: approval\n")}}, nil, "needs a description"},
		{"stage with an anchor", fstest.MapFS{"stages/schema-review.yaml": {Data: []byte("# A stage.\nversion: 2\nstages:\n  schema-review: &shared\n    kind: approval\n")}}, nil, "anchors or aliases"},
		{"rule with its own stages", fstest.MapFS{"rules/schema-change.yaml": {Data: []byte("version: 2\nstages:\n  extra:\n    kind: approval\nrules:\n  - name: schema-change\n    description: A rule.\n    when: {kind: [bugfix]}\n    then: {stages: [{use: approval}]}\n")}}, nil, "exactly one rule"},
		{"rule with an unknown key", fstest.MapFS{"rules/schema-change.yaml": {Data: ruleWith("    when: {kind: [bugfix]}\n    then: {stages: [{use: approval}]}\n    weight: 3\n")}}, nil, "field weight not found"},
		{"rule that matches every task", fstest.MapFS{"rules/schema-change.yaml": {Data: ruleWith("    when: {}\n    then: {stages: [{use: approval}]}\n")}}, nil, "matches every task"},
		{"rule without a description", fstest.MapFS{"rules/schema-change.yaml": {Data: []byte("version: 2\nrules:\n  - name: schema-change\n    when: {kind: [bugfix]}\n    then: {stages: [{use: approval}]}\n")}}, nil, "needs a description"},
		{"rule naming an agent nobody defines", fstest.MapFS{"rules/schema-change.yaml": {Data: ruleWith("    when: {kind: [bugfix]}\n    then:\n      stages:\n        - {id: fix, kind: agent, agent: ghost-writer}\n        - {use: approval, dependsOn: [fix]}\n")}}, nil, `names agent "ghost-writer"`},
		{"rule using a stage nobody defines", fstest.MapFS{"rules/schema-change.yaml": {Data: ruleWith("    when: {kind: [bugfix]}\n    then: {stages: [{use: nowhere}, {use: approval, dependsOn: [nowhere]}]}\n")}}, nil, "nowhere"},
		{"group using a stage nobody defines", fstest.MapFS{"groups/review-and-approve.yaml": {Data: []byte("# A group.\nversion: 2\ngroups:\n  review-and-approve:\n    stages:\n      - use: nowhere\n      - {use: approval, dependsOn: [nowhere]}\n")}}, nil, "nowhere"},
		{"two items with one name", fstest.MapFS{"stages/schema-reviewer.yaml": {Data: []byte("# Clash.\nversion: 2\nstages:\n  schema-reviewer:\n    kind: approval\n")}}, nil, `both named "schema-reviewer"`},
		{"an item named as a built-in agent", fstest.MapFS{"agents/reviewer.md": {Data: agentFile("reviewer", "")}}, nil, "a built-in agent"},
		{"an item named as a built-in stage", fstest.MapFS{"stages/verify.yaml": {Data: []byte("# Mine.\nversion: 2\nstages:\n  verify:\n    kind: evaluation\n")}}, nil, "a built-in stage"},
		{"an item named as a built-in rule", fstest.MapFS{"rules/bugfix.yaml": {Data: []byte("version: 2\nrules:\n  - name: bugfix\n    description: Mine.\n    when: {kind: [bugfix]}\n    then: {stages: [{use: approval}]}\n")}}, nil, "a built-in rule"},
		{"a rule named as a built-in rule in another case", fstest.MapFS{"rules/Bugfix.yaml": {Data: []byte("version: 2\nrules:\n  - name: Bugfix\n    description: Mine.\n    when: {kind: [bugfix]}\n    then: {stages: [{use: approval}]}\n")}}, nil, "a built-in rule"},
		{"two items whose names differ only in case", fstest.MapFS{"stages/Schema-Review.yaml": {Data: []byte("# Clash.\nversion: 2\nstages:\n  Schema-Review:\n    kind: approval\n")}}, nil, "compared without case"},
		{"rule whose only condition is a minPriority every task meets", fstest.MapFS{"rules/schema-change.yaml": {Data: ruleWith("    when: {minPriority: -1}\n    then: {stages: [{use: approval}]}\n")}}, nil, "matches every task"},
		{"rule whose only keyword is empty", fstest.MapFS{"rules/schema-change.yaml": {Data: ruleWith("    when: {keywords: [migration, \"\"]}\n    then: {stages: [{use: approval}]}\n")}}, nil, "matches every task"},
		{"rule listing every task kind", fstest.MapFS{"rules/schema-change.yaml": {Data: ruleWith("    when: {kind: [research, bugfix, implementation, review, documentation, skill-authoring]}\n    then: {stages: [{use: approval}]}\n")}}, nil, "matches every task"},
		{"rule whose complexity range is every score", fstest.MapFS{"rules/schema-change.yaml": {Data: ruleWith("    when: {complexity: {min: 1, max: 10}}\n    then: {stages: [{use: approval}]}\n")}}, nil, "matches every task"},
		{"a file that is not an item", fstest.MapFS{"agents/notes.txt": {Data: []byte("notes")}}, nil, "may hold only .md files"},
		{"a group without the stage it uses", nil, []string{"stages/schema-review.yaml"}, "schema-review"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fsys := fixture()
			maps.Copy(fsys, test.change)
			for _, name := range test.remove {
				delete(fsys, name)
			}
			_, err := Load(fsys)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Load = %v, want an error containing %q", err, test.message)
			}
		})
	}
}
