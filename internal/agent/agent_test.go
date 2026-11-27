package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

const securityReviewer = `---
name: security-reviewer
description: Reviews changes for security regressions
runtime: claude
permission: read-only
skills: [security-review]
taskKinds: [review]
tags: [security]
maxTurns: 12
---

You are a security reviewer. Look for injection, secrets, and privilege issues.
`

func TestParseAndCard(t *testing.T) {
	definition, err := Parse(securityReviewer)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Name != "security-reviewer" || definition.Runtime != domain.RuntimeClaude || definition.MaxTurns != 12 {
		t.Fatalf("unexpected definition %+v", definition)
	}
	if definition.Instructions != "You are a security reviewer. Look for injection, secrets, and privilege issues." {
		t.Fatalf("unexpected instructions %q", definition.Instructions)
	}
	card := definition.Card(map[string]string{"security-review": "OWASP checklist"})
	if card.Name != definition.Name || len(card.Skills) != 2 || card.Skills[0].Description != "OWASP checklist" || card.Skills[1].ID != "task:review" {
		t.Fatalf("unexpected card %+v", card)
	}
	if card.Metadata["permission"] != "read-only" || card.URL != "inkuna://agents/security-reviewer" {
		t.Fatalf("unexpected card metadata %+v", card.Metadata)
	}
}

func TestParseRejectsBadDefinitions(t *testing.T) {
	cases := map[string]string{
		"no frontmatter": "just text",
		"reserved name":  "---\nname: auto\ndescription: x\n---\nbody",
		"upper name":     "---\nname: Reviewer\ndescription: x\n---\nbody",
		"no body":        "---\nname: reviewer\ndescription: x\n---\n",
		"bad permission": "---\nname: reviewer\ndescription: x\npermission: root\n---\nbody",
		"bad runtime":    "---\nname: reviewer\ndescription: x\nruntime: Gemini CLI\n---\nbody",
		"not a mapping":  "---\n- name: reviewer\n---\nbody",
		"bad kind":       "---\nname: reviewer\ndescription: x\ntaskKinds: [deploy]\n---\nbody",
	}
	for name, content := range cases {
		if _, err := Parse(content); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestDiscoverAndRegistryPrecedence(t *testing.T) {
	global := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(global, "security-reviewer.md"), []byte(securityReviewer), 0o600); err != nil {
		t.Fatal(err)
	}
	override := "---\nname: implementer\ndescription: Repo-specific implementer\npermission: worktree-write\nruntime: codex\n---\nUse the repo conventions.\n"
	if err := os.MkdirAll(filepath.Join(repo, ".inkuna", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".inkuna", "agents", "implementer.md"), []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".inkuna", "agents", "notes.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := Build(repo, []string{global, filepath.Join(global, "missing")})
	if err != nil {
		t.Fatal(err)
	}
	implementer, ok := registry.Get("implementer")
	if !ok || implementer.Source != SourceRepository || implementer.Runtime != domain.RuntimeCodex {
		t.Fatalf("repository override was not applied: %+v", implementer)
	}
	if _, ok := registry.Get("security-reviewer"); !ok {
		t.Fatal("global agent missing")
	}
	if _, ok := registry.Get(Orchestrator); !ok {
		t.Fatal("orchestrator missing")
	}
	for _, worker := range registry.Workers() {
		if worker.Name == Orchestrator {
			t.Fatal("workers must exclude the orchestrator")
		}
	}
	reviewers := registry.ForKind(domain.TaskReview)
	if len(reviewers) < 2 || reviewers[0].Name != "reviewer" && reviewers[0].Name != "security-reviewer" {
		t.Fatalf("specialists should lead: %+v", reviewers)
	}
	if reviewers[len(reviewers)-1].Name != "implementer" {
		t.Fatalf("generalist (repo implementer without kinds) should trail: %+v", reviewers)
	}
}

func TestFileNameMustMatchAgentName(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "other.md")
	if err := os.WriteFile(path, []byte(securityReviewer), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, SourceGlobal); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestBuiltInsAreValid(t *testing.T) {
	for _, definition := range BuiltIn() {
		if err := definition.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if HumanCard().Name != Human {
		t.Fatal("human card name mismatch")
	}
}

func TestPlannerAndTestWriterBuiltIns(t *testing.T) {
	registry := NewRegistry(BuiltIn())
	planner, ok := registry.Get("planner")
	if !ok || planner.Permission != domain.PermissionReadOnly || !planner.Supports(domain.TaskImplementation) || len(planner.TaskKinds) != 1 {
		t.Fatalf("planner = %+v, %v; want a read-only implementation agent", planner, ok)
	}
	writer, ok := registry.Get("test-writer")
	if !ok || writer.Permission != domain.PermissionWorktree || !writer.Supports(domain.TaskImplementation) || !writer.Supports(domain.TaskBugfix) || len(writer.TaskKinds) != 2 {
		t.Fatalf("test-writer = %+v, %v; want a worktree-write implementation and bugfix agent", writer, ok)
	}
	// Like every built-in, both are replaced by a definition of the same name
	// in a later layer.
	override := []Definition{{Name: "test-writer", Description: "Writes table tests only.", Permission: domain.PermissionWorktree, Source: SourceRepository}}
	if got, _ := NewRegistry(BuiltIn(), override).Get("test-writer"); got.Source != SourceRepository || got.Description != "Writes table tests only." {
		t.Fatalf("test-writer override = %+v", got)
	}
}

func TestReadOnlyBuiltInsAreNotAskedToWriteFiles(t *testing.T) {
	for _, definition := range BuiltIn() {
		// Every write a read-only worker tries is refused and then put to the
		// user as a permission request.
		if definition.Permission == domain.PermissionReadOnly && strings.Contains(definition.Instructions, ".inkuna/out") {
			t.Errorf("%s is read-only but told to write under .inkuna/out", definition.Name)
		}
	}
}

func TestParseHandlesCRLFLineEndings(t *testing.T) {
	content := strings.ReplaceAll(securityReviewer, "\n", "\r\n")
	definition, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Name != "security-reviewer" || definition.Instructions != "You are a security reviewer. Look for injection, secrets, and privilege issues." {
		t.Fatalf("CRLF file parsed incorrectly: %+v", definition)
	}
}

func TestRemoteAgentsNeverReplaceLocalOnes(t *testing.T) {
	registry, skipped := NewRegistry(BuiltIn()).WithRemote([]Definition{
		{Name: "advisor", Description: "Remote advisor", Permission: domain.PermissionWorktree, Runtime: domain.RuntimeClaude, Secrets: []string{"TOKEN"}, Skills: []string{"go"}, Auto: true},
		{Name: "reviewer", Description: "Pretends to be the local reviewer"},
		{Name: "Not Valid", Description: "Bad name"},
		{Name: Human, Description: "Reserved"},
	})
	if strings.Join(skipped, ",") != "Not Valid,human,reviewer" {
		t.Fatalf("unexpected skipped names %v", skipped)
	}
	advisor, ok := registry.Get("advisor")
	if !ok || !advisor.Remote() || advisor.Permission != domain.PermissionReadOnly || advisor.Runtime != "" || len(advisor.Secrets)+len(advisor.Skills) != 0 {
		t.Fatalf("a remote agent is read-only with no runtime, secrets, or skills: %+v", advisor)
	}
	if reviewer, _ := registry.Get("reviewer"); reviewer.Remote() {
		t.Fatal("a remote agent replaced the local reviewer")
	}
	if original := NewRegistry(BuiltIn()); len(original.All()) != len(registry.All())-1 {
		t.Fatal("WithRemote must not change the registry it was called on")
	}
	offered := func(current Registry) bool {
		for _, definition := range current.ForKind(domain.TaskResearch) {
			if definition.Name == "advisor" {
				return true
			}
		}
		return false
	}
	if !offered(registry) {
		t.Fatal("an auto remote agent is a candidate")
	}
	manual, _ := NewRegistry(BuiltIn()).WithRemote([]Definition{{Name: "advisor", Description: "Remote advisor"}})
	if offered(manual) {
		t.Fatal("a remote agent that is not auto is only used where a rule names it")
	}
	if _, err := Parse("---\nname: sneaky\ndescription: d\nruntime: remote\n---\nbody\n"); err == nil {
		t.Fatal("an agent file cannot claim the remote runtime")
	}
}

func TestAnAgentNamesTheMCPServersItsStepsAreGiven(t *testing.T) {
	definition, err := Parse(strings.Replace(securityReviewer, "tags: [security]\n", "tags: [security]\nmcp: [docs-search, linear]\n", 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.MCP) != 2 || definition.MCP[0] != "docs-search" {
		t.Fatalf("mcp = %v", definition.MCP)
	}
	for _, bad := range []string{"mcp: [Docs]\n", "mcp: [edit]\n", "mcp: [docs_search]\n"} {
		if _, err := Parse(strings.Replace(securityReviewer, "tags: [security]\n", "tags: [security]\n"+bad, 1)); err == nil || !strings.Contains(err.Error(), "not a valid server name") {
			t.Fatalf("%q must be refused: %v", bad, err)
		}
	}
}

func TestAnAgentAsksForTrackerKinds(t *testing.T) {
	definition, err := Parse(strings.Replace(securityReviewer, "tags: [security]\n", "tags: [security]\ntracker: [comment, create]\n", 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.Tracker) != 2 || definition.Tracker[0] != domain.TrackerComment {
		t.Fatalf("tracker = %v", definition.Tracker)
	}
	if _, err := Parse(strings.Replace(securityReviewer, "tags: [security]\n", "tags: [security]\ntracker: [archive]\n", 1)); err == nil || !strings.Contains(err.Error(), `asks for tracker kind "archive"`) {
		t.Fatalf("an unknown kind must be refused: %v", err)
	}
}

// A Claude Code agent file, with keys only Claude Code reads.
const claudeCodeAgent = `---
name: code-explorer
description: Explores the codebase.
color: blue
tools: [Read, Grep, Glob]
permission: read-only
since: 2026-01-02
1: one
---

Explore.
`

func TestAnAgentKeepsTheFrontmatterKeysInkunaDoesNotRead(t *testing.T) {
	definition, err := Parse(claudeCodeAgent)
	if err != nil {
		t.Fatalf("an extra key refused the file: %v", err)
	}
	if definition.Name != "code-explorer" || definition.Permission != domain.PermissionReadOnly || definition.Instructions != "Explore." {
		t.Fatalf("definition = %+v", definition)
	}
	// Kept as the file has them, in its order, values untouched.
	want := "color: blue\ntools: [Read, Grep, Glob]\nsince: 2026-01-02\n1: one\n"
	if definition.Extra != want {
		t.Fatalf("extra = %q, want %q", definition.Extra, want)
	}
	// Written back after Inkuna's keys, and read back the same.
	data, err := Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n"+want+"---\n") {
		t.Fatalf("written file:\n%s", data)
	}
	again, err := Parse(string(data))
	if err != nil || again.Extra != want || again.Description != definition.Description {
		t.Fatalf("read back = %+v, %v", again, err)
	}
	// A file with none keeps none.
	if plain, err := Parse(securityReviewer); err != nil || plain.Extra != "" {
		t.Fatalf("plain = %q, %v", plain.Extra, err)
	}
}

func TestKeptKeysNeverSetWhatInkunaReads(t *testing.T) {
	base := Definition{Name: "explorer", Description: "Explores", Permission: domain.PermissionReadOnly, Instructions: "Explore."}
	for extra, reason := range map[string]string{
		"permission: worktree-write\n":                    `include "permission", which Inkuna reads itself`,
		"color: blue\ncolor: red\n":                       `name "color" twice`,
		"- a list\n":                                      "must be a mapping",
		"color: [unclosed\n":                              "not valid",
		"color: blue\n---\nsecrets: [AWS_KEY]\n":          "a single YAML document",
		"<<: {secrets: [AWS_KEY]}\n":                      `merge in "secrets"`,
		"base: &b {permission: worktree-write}\n<<: *b\n": `merge in "permission"`,
		"<<: [{color: blue}, {<<: {model: opus}}]\n":      `merge in "model"`,
	} {
		definition := base
		definition.Extra = extra
		if err := definition.Validate(); err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%q: %v", extra, err)
		}
		if _, err := Marshal(definition); err == nil {
			t.Errorf("%q was written", extra)
		}
	}
}

func TestKeptKeysStandOnTheirOwn(t *testing.T) {
	// An alias to a value anchored under a key Inkuna reads keeps its value.
	aliased, err := Parse("---\nname: explorer\ndescription: &d Explores the code\nsummary: *d\n---\nExplore.\n")
	if err != nil {
		t.Fatalf("a valid file was refused: %v", err)
	}
	if aliased.Description != "Explores the code" || aliased.Extra != "summary: Explores the code\n" {
		t.Fatalf("definition = %+v", aliased)
	}
	// An anchor among the kept keys stays with them, and a key Inkuna reads
	// may still alias it.
	anchored, err := Parse("---\nname: explorer\nbase: &b Explores the code\ndescription: *b\nsummary: *b\n---\nExplore.\n")
	if err != nil || anchored.Description != "Explores the code" || anchored.Extra != "base: &b Explores the code\nsummary: *b\n" {
		t.Fatalf("definition = %+v, %v", anchored, err)
	}
	for _, definition := range []Definition{aliased, anchored} {
		data, err := Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		if again, err := Parse(string(data)); err != nil || again.Extra != definition.Extra {
			t.Fatalf("read back = %+v, %v\n%s", again, err, data)
		}
	}
	// A merge that sets nothing Inkuna reads is kept.
	merged, err := Parse("---\nname: explorer\ndescription: Explores\n<<: {color: blue}\n---\nExplore.\n")
	if err != nil || !strings.Contains(merged.Extra, "<<: {color: blue}") {
		t.Fatalf("definition = %+v, %v", merged, err)
	}
	// One that would set a key Inkuna reads refuses the file.
	if _, err := Parse("---\nname: explorer\ndescription: Explores\n<<: {secrets: [AWS_KEY]}\n---\nExplore.\n"); err == nil || !strings.Contains(err.Error(), `merge in "secrets"`) {
		t.Fatalf("a merge of secrets was kept: %v", err)
	}
}

// Aliases that loop back on themselves, multiply without end, or name a key
// Inkuna reads are refused with the reason, never followed forever.
func TestKeptKeysRefuseAliasesThatNeverEnd(t *testing.T) {
	ten := func(item string) string { return "[" + strings.Repeat(item+", ", 9) + item + "]" }
	for name, test := range map[string]struct {
		content string
		reason  string
	}{
		"a value aliasing itself": {"description: &d [a, *d]\nsummary: *d\n", "refers to the value that holds it"},
		"aliases that multiply": {
			"description: &a " + ten("x") + "\nmodel: &b " + ten("*a") + "\nruntime: &c " + ten("*b") + "\nreasoningEffort: &e " + ten("*c") + "\nsummary: *e\n",
			"expand to more than 10000 values",
		},
		"a merge of itself":                   {"base: &b {<<: *b}\n<<: *b\n", "brings in a value that holds it"},
		"an alias key for a key Inkuna reads": {"x: &k permission\n*k : worktree-write\n", `include "permission"`},
		"a merged alias key":                  {"<<: {x: &k secrets, *k : [AWS_KEY]}\n", `merge in "secrets"`},
	} {
		header := "name: explorer\n"
		if !strings.HasPrefix(test.content, "description:") {
			header += "description: Explores\n"
		}
		_, err := Parse("---\n" + header + test.content + "---\nExplore.\n")
		if err == nil || !strings.Contains(err.Error(), test.reason) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A value merged in from two places is read, not mistaken for a cycle.
	shared, err := Parse("---\nname: explorer\ndescription: Explores\nbase: &b {color: blue}\n<<: [*b, *b]\n---\nExplore.\n")
	if err != nil || !strings.Contains(shared.Extra, "<<: [*b, *b]") {
		t.Fatalf("shared merge = %+v, %v", shared, err)
	}
}
