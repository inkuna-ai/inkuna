package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/rules"
)

func load(t *testing.T) Catalog {
	t.Helper()
	catalog, err := Load(fixture())
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// repository is a target for an empty repository, with global directories
// of its own that hold nothing.
func repository(t *testing.T) (Target, string) {
	t.Helper()
	root := t.TempDir()
	return Target{
		RulesPath:        filepath.Join(root, filepath.FromSlash(rules.RelativePath)),
		AgentDirectory:   filepath.Join(root, ".inkuna", "agents"),
		AgentDirectories: []string{filepath.Join(root, "global", "agents")},
		StageDirectories: []string{filepath.Join(root, "global", "stages")},
	}, root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addedNames(result Result) []string {
	names := make([]string, len(result.Added))
	for index, added := range result.Added {
		names[index] = string(added.Kind) + ":" + added.Name
	}
	return names
}

func ruleNames(document rules.Document) []string {
	names := make([]string, len(document.Rules))
	for index, rule := range document.Rules {
		names[index] = rule.Name
	}
	return names
}

func TestAddingARuleToARepositoryWithoutRulesWritesTheBuiltInRulesWithIt(t *testing.T) {
	target, root := repository(t)
	result, err := load(t).Add("schema-change", target)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"rule:schema-change", "agent:schema-writer", "group:review-and-approve", "stage:schema-review", "agent:schema-reviewer"}; !slices.Equal(addedNames(result), want) {
		t.Fatalf("added %v, want %v", addedNames(result), want)
	}
	if len(result.Notes) != 1 || !strings.Contains(result.Notes[0], "keeps the flows it had") {
		t.Fatalf("notes = %q", result.Notes)
	}
	document, exists, err := rules.Load(root)
	if err != nil || !exists {
		t.Fatalf("rules after the add: exists %v, %v", exists, err)
	}
	// The new rule comes first; the built-in rules and default flow follow
	// unchanged, so every task the repository had a flow for keeps it.
	want := append([]string{"schema-change"}, ruleNames(rules.Default())...)
	if got := ruleNames(document); !slices.Equal(got, want) {
		t.Fatalf("rules = %v, want %v", got, want)
	}
	builtinDefault, _ := rules.Default().Named(rules.DefaultName)
	if got, _ := document.Named(rules.DefaultName); len(got.Stages) != len(builtinDefault.Stages) {
		t.Fatalf("default flow has %d stages, want the built-in %d", len(got.Stages), len(builtinDefault.Stages))
	}
	if _, ok := document.Stages["schema-review"]; !ok {
		t.Fatalf("schema-review was not added to the rules' stages: %v", document.Stages)
	}
	if _, ok := document.Groups["review-and-approve"]; !ok {
		t.Fatalf("review-and-approve was not added to the rules' groups: %v", document.Groups)
	}
	data, _ := os.ReadFile(target.RulesPath)
	if !strings.Contains(string(data), "# Reviews a schema change, read-only.") {
		t.Errorf("an added stage keeps its description as a comment:\n%s", data)
	}
	// An added agent records the item it came from, and keeps it when
	// Inkuna saves the agent again.
	writer, err := agent.Load(filepath.Join(target.AgentDirectory, "schema-writer.md"), agent.SourceRepository)
	if err != nil {
		t.Fatal(err)
	}
	if writer.Extra != "catalog: schema-writer\n" {
		t.Fatalf("schema-writer extra frontmatter = %q", writer.Extra)
	}
	saved, err := agent.Marshal(writer)
	if err != nil || !strings.Contains(string(saved), "catalog: schema-writer") {
		t.Fatalf("saving the agent dropped its origin: %v\n%s", err, saved)
	}
}

func TestAddingARuleKeepsTheRulesFileAndPutsTheRuleFirst(t *testing.T) {
	target, root := repository(t)
	write(t, target.RulesPath, `# Our rules.
version: 1
rules:
  - name: ours
    when:
      kind: [bugfix]
    then:
      stages:
        - {id: fix, kind: agent, agent: auto}
        # Every flow ends with approval.
        - {id: approval, kind: approval, dependsOn: [fix]}
default:
  stages:
    - {id: approval, kind: approval}
`)
	result, err := load(t).Add("schema-change", target)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 1 || !strings.Contains(result.Notes[0], "is first in") {
		t.Fatalf("notes = %q", result.Notes)
	}
	data, _ := os.ReadFile(target.RulesPath)
	for _, kept := range []string{"# Our rules.", "# Every flow ends with approval.", "version: 2"} {
		if !strings.Contains(string(data), kept) {
			t.Errorf("rules file lacks %q:\n%s", kept, data)
		}
	}
	document, _, err := rules.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleNames(document); !slices.Equal(got, []string{"schema-change", "ours"}) {
		t.Fatalf("rules = %v", got)
	}
	if flow, _ := document.Named(rules.DefaultName); len(flow.Stages) != 1 {
		t.Fatalf("default flow changed: %+v", flow)
	}
}

// What the repository already has is used as it is: its own agent and
// shared stage by those names are not replaced, and nothing that stage
// would have needed from the catalog is added.
func TestAddingARuleBringsOnlyWhatTheRepositoryLacks(t *testing.T) {
	target, _ := repository(t)
	write(t, filepath.Join(target.AgentDirectory, "schema-writer.md"), "---\nname: schema-writer\ndescription: Ours.\npermission: worktree-write\n---\n\nOurs.\n")
	write(t, target.RulesPath, `version: 2
stages:
  schema-review:
    kind: approval
rules: []
`)
	result, err := load(t).Add("schema-change", target)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"rule:schema-change", "group:review-and-approve"}; !slices.Equal(addedNames(result), want) {
		t.Fatalf("added %v, want %v", addedNames(result), want)
	}
	if exists, _ := agent.Exists(target.AgentDirectory, "schema-reviewer"); exists {
		t.Fatal("schema-reviewer was added though only the catalog's schema-review, which the repository replaces, names it")
	}
	ours, _ := os.ReadFile(filepath.Join(target.AgentDirectory, "schema-writer.md"))
	if !strings.Contains(string(ours), "Ours.") {
		t.Fatalf("the repository's own schema-writer was replaced:\n%s", ours)
	}
	// A rules file whose list was written empty takes the rule as a block.
	if data, _ := os.ReadFile(target.RulesPath); strings.Contains(string(data), "rules: [") {
		t.Fatalf("rules list left in flow style:\n%s", data)
	}
}

func TestAddNeverOverwritesAndWritesNothingWhenItRefuses(t *testing.T) {
	cases := []struct {
		name   string
		item   string
		global bool
		setup  func(t *testing.T, target Target)
		where  func(target Target) string
	}{
		{"an agent file of that name", "schema-writer", false, func(t *testing.T, target Target) {
			write(t, filepath.Join(target.AgentDirectory, "schema-writer.md"), "---\nname: schema-writer\ndescription: Ours.\n---\n\nOurs.\n")
		}, func(target Target) string { return filepath.Join(target.AgentDirectory, "schema-writer.md") }},
		{"a rule of that name", "schema-change", false, func(t *testing.T, target Target) {
			write(t, target.RulesPath, "version: 2\nrules:\n  - name: Schema-Change\n    when: {kind: [bugfix]}\n    then: {stages: [{use: approval}]}\n")
		}, func(target Target) string { return target.RulesPath }},
		{"a group of the stage's name", "schema-review", false, func(t *testing.T, target Target) {
			write(t, target.RulesPath, "version: 2\ngroups:\n  schema-review:\n    stages: [{use: approval}]\nrules: []\n")
		}, func(target Target) string { return target.RulesPath }},
		{"a global agent file of that name", "schema-reviewer", true, func(t *testing.T, target Target) {
			write(t, filepath.Join(target.AgentDirectories[0], "schema-reviewer.md"), "---\nname: schema-reviewer\ndescription: Ours.\n---\n\nOurs.\n")
		}, func(target Target) string { return filepath.Join(target.AgentDirectories[0], "schema-reviewer.md") }},
		{"a global library defining that name", "schema-review", true, func(t *testing.T, target Target) {
			write(t, filepath.Join(target.StageDirectories[0], "ours.yaml"), "version: 2\nstages:\n  schema-review:\n    kind: approval\n")
		}, func(target Target) string { return filepath.Join(target.StageDirectories[0], "ours.yaml") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			target, root := repository(t)
			target.Global = test.global
			test.setup(t, target)
			before := snapshot(t, root)
			_, err := load(t).Add(test.item, target)
			var exists *ExistsError
			if !errors.As(err, &exists) || exists.Name != test.item || exists.Where != test.where(target) {
				t.Fatalf("Add = %v, want it refused naming %s", err, test.where(target))
			}
			if after := snapshot(t, root); !slices.Equal(before, after) {
				t.Fatalf("a refused add changed files: before %v, after %v", before, after)
			}
		})
	}
}

func TestAddRefusesWhatHasNoPlace(t *testing.T) {
	target, _ := repository(t)
	if _, err := load(t).Add("nothing", target); err == nil || !strings.Contains(err.Error(), "no item") {
		t.Fatalf("an unknown item: %v", err)
	}
	target.Global = true
	if _, err := load(t).Add("schema-change", target); err == nil || !strings.Contains(err.Error(), "is a rule") {
		t.Fatalf("a rule added globally: %v", err)
	}
	target.Global = false
	write(t, target.RulesPath, "version: 2\nrules: [{name: broken}]\n")
	if _, err := load(t).Add("schema-review", target); err == nil || !strings.Contains(err.Error(), "does not load") {
		t.Fatalf("rules that do not load: %v", err)
	}
}

func TestAddingAGroupGloballyWritesALibraryFileForEachDefinition(t *testing.T) {
	target, _ := repository(t)
	target.Global = true
	result, err := load(t).Add("review-and-approve", target)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"group:review-and-approve", "stage:schema-review", "agent:schema-reviewer"}; !slices.Equal(addedNames(result), want) {
		t.Fatalf("added %v, want %v", addedNames(result), want)
	}
	library := rules.LoadLibrary(target.StageDirectories)
	if len(library.Problems) > 0 {
		t.Fatalf("the global library has problems: %+v", library.Problems)
	}
	for _, name := range []string{"review-and-approve", "schema-review"} {
		definition, err := library.Resolve(name)
		if err != nil || definition.Scope != rules.ScopeGlobal || filepath.Base(definition.File) != name+".yaml" {
			t.Fatalf("%s resolves to %+v, %v", name, definition, err)
		}
	}
	if exists, _ := agent.Exists(target.AgentDirectories[0], "schema-reviewer"); !exists {
		t.Fatal("schema-reviewer was not added to the global agents")
	}
}

func TestAddTakesBackTheAgentsItWroteWhenTheRulesCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows ignores a directory's read-only attribute, so nothing here makes the rules unwritable")
	}
	target, root := repository(t)
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	target.RulesPath = filepath.Join(locked, ".inkuna", "rules.yaml")
	if _, err := load(t).Add("schema-change", target); err == nil {
		t.Fatal("Add succeeded writing rules into a read-only directory")
	}
	entries, _ := os.ReadDir(target.AgentDirectory)
	if len(entries) != 0 {
		t.Fatalf("agents left behind: %v", entries)
	}
}

// A rules file kept from others stays that way when an add replaces it.
func TestAddKeepsTheRulesFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows files have no owner, group, and other permissions to keep")
	}
	target, _ := repository(t)
	write(t, target.RulesPath, "version: 2\nrules: []\n")
	if err := os.Chmod(target.RulesPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(t).Add("schema-review", target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target.RulesPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("rules file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

// A file that appears after Add checked the name was free is not replaced,
// and nothing is left behind.
func TestCreateFileNeverReplacesAFileMadeSince(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "schema-reviewer.md")
	write(t, path, "theirs")
	if err := createFile(path, []byte("ours")); err == nil || !strings.Contains(err.Error(), "never overwrites") {
		t.Fatalf("createFile = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "theirs" {
		t.Fatalf("the file was replaced: %q", data)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
	fresh := filepath.Join(directory, "new.md")
	if err := createFile(fresh, []byte("ours")); err != nil {
		t.Fatal(err)
	}
	// Windows files have no Unix permissions; it reports every writable file
	// as 0666.
	if info, err := os.Stat(fresh); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o644) {
		t.Fatalf("new file: %v, %v", info, err)
	}
	if entries, _ := os.ReadDir(directory); len(entries) != 2 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

// A rules list the file takes through a YAML merge is not a key the edit
// sees, so writing a rules key of its own would drop the merged rules.
func TestAddRefusesARulesFileItCannotEditInPlace(t *testing.T) {
	target, root := repository(t)
	write(t, target.RulesPath, `version: 2
<<: {rules: [{name: ours, when: {kind: [bugfix]}, then: {stages: [{id: approval, kind: approval}]}}]}
`)
	if document, _, err := rules.Load(root); err != nil || len(document.Rules) != 1 {
		t.Fatalf("the merged rules file does not load as one rule: %v, %v", document.Rules, err)
	}
	before := snapshot(t, root)
	if _, err := load(t).Add("schema-change", target); err == nil || !strings.Contains(err.Error(), "cannot be added to in place") {
		t.Fatalf("Add = %v, want it refused", err)
	}
	if after := snapshot(t, root); !slices.Equal(before, after) {
		t.Fatalf("a refused add changed files: before %v, after %v", before, after)
	}
}

// A rules file changed after the add read it, by another add or an editor,
// is not replaced, and the agents written for the add are taken back.
func TestAddRefusesARulesFileThatChangedSinceItWasRead(t *testing.T) {
	target, _ := repository(t)
	write(t, target.RulesPath, "version: 2\nrules: []\n")
	place, err := open(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := place.unchanged(); err != nil {
		t.Fatalf("an unchanged file: %v", err)
	}
	write(t, target.RulesPath, "version: 2\nrules: [] # edited\n")
	if err := place.unchanged(); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("a changed file: %v", err)
	}

	// A rules file that appears while a repository without one is added to.
	target, _ = repository(t)
	place, err = open(target)
	if err != nil {
		t.Fatal(err)
	}
	write(t, target.RulesPath, "version: 2\nrules: []\n")
	catalog := load(t)
	if _, err := place.writeRepository("schema-review", []Item{catalog.items["schema-review"], catalog.items["schema-reviewer"]}); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("writeRepository = %v, want it refused", err)
	}
	if exists, _ := agent.Exists(target.AgentDirectory, "schema-reviewer"); exists {
		t.Fatal("the agent written for the refused add was left behind")
	}
}

// Without hard links a new file is created exclusively, so it still never
// replaces one.
func TestCreateFileWithoutHardLinksStillNeverReplaces(t *testing.T) {
	previous := link
	link = func(string, string) error { return errors.New("operation not supported") }
	t.Cleanup(func() { link = previous })
	directory := t.TempDir()
	path := filepath.Join(directory, "schema-reviewer.md")
	if err := createFile(path, []byte("ours")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "ours" {
		t.Fatalf("created %q", data)
	}
	if err := createFile(path, []byte("again")); err == nil || !strings.Contains(err.Error(), "never overwrites") {
		t.Fatalf("a second create: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "ours" {
		t.Fatalf("the file was replaced: %q", data)
	}
	if entries, _ := os.ReadDir(directory); len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

// snapshot lists every file under root with its content.
func snapshot(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		files = append(files, path+"="+string(data))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
