package catalog

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/skill"
)

const (
	sourceAgent = "---\nname: api-reviewer\ndescription: Reviews APIs.\npermission: read-only\nskills: [api-style]\n---\n\nReview the API.\n"
	sourceSkill = "---\nname: api-style\ndescription: How this team writes APIs.\n---\n\nUse plural nouns.\n"
	sourceStage = "# Reviews an API change.\nversion: 2\nstages:\n  api-review: {kind: agent, agent: api-reviewer, permission: read-only}\n"
	sourceRule  = `version: 2
rules:
  - name: api-change
    description: API changes get an API review.
    when: {kind: [implementation], keywords: [endpoint]}
    then:
      stages:
        - use: profile
        - {id: implement, kind: agent, agent: auto, dependsOn: [profile]}
        - {id: review, use: api-review, dependsOn: [implement]}
        - {use: verify-and-approve, dependsOn: [review]}
`
)

// sourceDefinitions are a source's agent, the skill it names, a stage
// using the agent, and a rule using the stage.
func sourceDefinitions() []SourceDefinition {
	return []SourceDefinition{
		{Kind: KindRule, Name: "api-change", Scope: "team:platform", Version: "r1", Files: map[string][]byte{"api-change.yaml": []byte(sourceRule)}},
		{Kind: KindStage, Name: "api-review", Scope: "team:platform", Version: "s1", Files: map[string][]byte{"api-review.yaml": []byte(sourceStage)}},
		{Kind: KindAgent, Name: "api-reviewer", Scope: "team:platform", Version: "a1", Files: map[string][]byte{"api-reviewer.md": []byte(sourceAgent)}},
		{Kind: KindSkill, Name: "api-style", Scope: "team:platform", Version: "k1", Files: map[string][]byte{
			"SKILL.md": []byte(sourceSkill), "inkuna.yaml": []byte("version: 1\ntaskKinds: [implementation]\n"), "examples/good.md": []byte("GET /orders\n"),
		}},
	}
}

func TestASourcesDefinitionsAreCheckedAsCatalogItems(t *testing.T) {
	catalog, problems := FromSource("example/source", sourceDefinitions())
	if len(problems) != 0 {
		t.Fatalf("problems = %+v", problems)
	}
	var names []string
	for _, item := range catalog.Items() {
		names = append(names, string(item.Kind)+":"+item.Name)
		if item.Source != "example/source" || item.Scope != "team:platform" || item.Version == "" {
			t.Fatalf("item %s does not name its source: %+v", item.Name, item)
		}
	}
	if want := []string{"rule:api-change", "stage:api-review", "agent:api-reviewer", "skill:api-style"}; !slices.Equal(names, want) {
		t.Fatalf("items = %v, want %v", names, want)
	}
	rule, _ := catalog.Get("api-change")
	if !slices.Equal(rule.Needs.Agents, []string{"api-reviewer"}) || !slices.Contains(rule.Needs.Stages, "api-review") || !slices.Equal(rule.Needs.Skills, []string{"api-style"}) {
		t.Fatalf("the rule needs %+v", rule.Needs)
	}
	style, _ := catalog.Get("api-style")
	if style.Content != sourceSkill || !slices.Equal(style.Files, []string{"examples/good.md", "inkuna.yaml"}) || !slices.Equal(style.TaskKinds, rule.TaskKinds) {
		t.Fatalf("the skill = %+v", style)
	}
}

func TestADefinitionThatFailsItsChecksIsLeftOutWithWhatNeedsIt(t *testing.T) {
	definitions := sourceDefinitions()
	// The stage names an agent nobody offers, so the stage fails, and the
	// rule that uses it goes with it, for the same reason.
	definitions[1].Files["api-review.yaml"] = []byte(strings.ReplaceAll(sourceStage, "agent: api-reviewer", "agent: nobody"))
	definitions = append(definitions,
		SourceDefinition{Kind: KindAgent, Name: "planner", Files: map[string][]byte{"planner.md": []byte("---\nname: planner\ndescription: Plans.\n---\n\nPlan.\n")}},
		SourceDefinition{Kind: KindAgent, Name: "two-files", Files: map[string][]byte{"two-files.md": []byte(sourceAgent), "extra.md": nil}},
		SourceDefinition{Kind: KindSkill, Name: "escape", Files: map[string][]byte{"SKILL.md": []byte(sourceSkill), "../outside.md": nil}},
		SourceDefinition{Kind: KindSkill, Name: "misnamed", Files: map[string][]byte{"SKILL.md": []byte(sourceSkill)}},
		SourceDefinition{Kind: KindSkill, Name: "nested", Files: map[string][]byte{"SKILL.md": []byte(sourceSkill), "docs": nil, "docs/a.md": nil}},
		SourceDefinition{Kind: KindRule, Name: "../rules", Files: map[string][]byte{"../rules.yaml": nil}},
	)
	catalog, problems := FromSource("example/source", definitions)
	reasons := map[string]string{}
	for _, problem := range problems {
		reasons[string(problem.Kind)+":"+problem.Name] = problem.Reason
	}
	for key, want := range map[string]string{
		"stage:api-review":   `"nobody"`,
		"rule:api-change":    `"nobody"`,
		"agent:planner":      "the name of a built-in agent",
		"agent:two-files":    `is one file, two-files.md`,
		"skill:escape":       "not a relative path inside its directory",
		"skill:misnamed":     `must match directory "misnamed"`,
		"skill:nested":       `"docs/a.md" is inside its file "docs"`,
		"rule:../rules":      "not a name a file can have",
		"agent:api-reviewer": "",
		"skill:api-style":    "",
	} {
		if !strings.Contains(reasons[key], want) || (want == "" && reasons[key] != "") {
			t.Errorf("problem with %s = %q, want one saying %q", key, reasons[key], want)
		}
	}
	var names []string
	for _, item := range catalog.Items() {
		names = append(names, item.Name)
	}
	if want := []string{"api-reviewer", "api-style"}; !slices.Equal(names, want) {
		t.Fatalf("the catalog kept %v, want %v", names, want)
	}
}

func TestAddingASourcesAgentBringsTheSkillItNamesAndRecordsTheSource(t *testing.T) {
	catalog, problems := FromSource("example/source", sourceDefinitions())
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	target, root := repository(t)
	target.SkillDirectory = filepath.Join(root, ".inkuna", "skills")
	preview, err := catalog.Preview("api-reviewer", target)
	if err != nil || !slices.Equal(addedNames(preview), []string{"agent:api-reviewer", "skill:api-style"}) {
		t.Fatalf("Preview = %+v, %v", preview, err)
	}
	if _, err := os.Stat(target.SkillDirectory); !os.IsNotExist(err) {
		t.Fatalf("a preview wrote the skill: %v", err)
	}
	result, err := catalog.Add("api-reviewer", target)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(target.SkillDirectory, "api-style"); result.Added[1].Path != want {
		t.Fatalf("the skill went to %s, want %s", result.Added[1].Path, want)
	}
	packages, err := skill.Discover(target.SkillDirectory)
	if err != nil || len(packages) != 1 || packages[0].Body != "Use plural nouns." {
		t.Fatalf("the repository's skills = %+v, %v", packages, err)
	}
	if data, err := os.ReadFile(filepath.Join(target.SkillDirectory, "api-style", "examples", "good.md")); err != nil || string(data) != "GET /orders\n" {
		t.Fatalf("the skill's own file = %q, %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".inkuna"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".catalog") {
			t.Fatalf("the add left %s behind", entry.Name())
		}
	}
	added, err := os.ReadFile(filepath.Join(target.AgentDirectory, "api-reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	definition, err := agent.Parse(string(added))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"catalog: api-reviewer", "catalogSource: example/source", "catalogScope: team:platform", "catalogVersion: a1"} {
		if !strings.Contains(definition.Extra, line) {
			t.Fatalf("the agent's frontmatter records %q, want %q in it", definition.Extra, line)
		}
	}
	// What the repository has now is found, so the skill is not added twice.
	if _, err := catalog.Add("api-style", target); err == nil || !strings.Contains(err.Error(), "already has skill") {
		t.Fatalf("adding the skill again: %v", err)
	}
}

func TestASourcesItemsAndSkillsAreNeverAddedGlobally(t *testing.T) {
	catalog, _ := FromSource("example/source", sourceDefinitions())
	target, _ := repository(t)
	target.Global = true
	if _, err := catalog.Add("api-reviewer", target); err == nil || !strings.Contains(err.Error(), "shares it for this project") {
		t.Fatalf("a global add of a source's agent: %v", err)
	}
	if _, err := catalog.Add("api-style", target); err == nil || !strings.Contains(err.Error(), "skills live in a repository") {
		t.Fatalf("a global add of a skill: %v", err)
	}
}

func TestTheCatalogLoadsSkillsFromItsSkillsDirectory(t *testing.T) {
	catalog, err := Load(fstest.MapFS{
		"skills/api-style/SKILL.md": {Data: []byte(sourceSkill)},
		"agents/api-reviewer.md":    {Data: []byte(sourceAgent)},
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, _ := catalog.Get("api-reviewer")
	if !slices.Equal(reviewer.Needs.Skills, []string{"api-style"}) {
		t.Fatalf("the agent needs %+v", reviewer.Needs)
	}
	if _, err := Load(fstest.MapFS{"skills/loose.md": {Data: []byte("x")}}); err == nil || !strings.Contains(err.Error(), "only directories") {
		t.Fatalf("a file in skills: %v", err)
	}
	if _, err := Load(fstest.MapFS{"skills/misnamed/SKILL.md": {Data: []byte(sourceSkill)}}); err == nil || !strings.Contains(err.Error(), "catalog item skills/misnamed/SKILL.md") {
		t.Fatalf("a misnamed skill: %v", err)
	}
	target, root := repository(t)
	if _, err := catalog.Add("api-style", target); err == nil || !strings.Contains(err.Error(), "skills directory") {
		t.Fatalf("a skill added without a skills directory: %v", err)
	}
	target.SkillDirectory = filepath.Join(root, ".inkuna", "skills")
	presence, err := catalog.Presence(target)
	if err != nil || len(presence) != 0 {
		t.Fatalf("Presence = %v, %v", presence, err)
	}
}
