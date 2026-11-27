package branching

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

const complete = `version: 1
tracker: linear
branch:
  base: main
  pattern: "{type}/{story}-{slug}"
  types: [feature, bug, chore, hotfix, enabler]
  kindTypes: {documentation: enabler}
  maxLength: 50
worktrees:
  enabled: true
  location: ../{repo}-worktrees
  cleanup: listener
`

func mustParse(t *testing.T, content string) Config {
	t.Helper()
	loaded, err := Parse([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Complete() {
		t.Fatalf("expected a complete config, missing %v", loaded.Missing)
	}
	return loaded.Config
}

func TestLoadReportsMissingFileAndKeys(t *testing.T) {
	loaded, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Exists || len(loaded.Missing) != len(Required) || !loaded.Config.Worktrees.Enabled || loaded.Config.Worktrees.Cleanup != CleanupManual {
		t.Fatalf("missing file should report every required key with defaults: %#v", loaded)
	}
	partial, err := Parse([]byte("version: 1\nworktrees:\n  enabled: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !partial.Exists || strings.Join(partial.Missing, ",") != "tracker,branch.base,branch.pattern" || partial.Config.Worktrees.Enabled {
		t.Fatalf("partial file: %#v", partial)
	}
}

func TestOutCleanupDefaultsToArchive(t *testing.T) {
	if config := mustParse(t, complete); config.Out.Cleanup != OutArchive {
		t.Fatalf("a config without out archives: %q", config.Out.Cleanup)
	}
	for _, policy := range []OutCleanup{OutArchive, OutRemove, OutKeep} {
		if config := mustParse(t, complete+"out:\n  cleanup: "+string(policy)+"\n"); config.Out.Cleanup != policy {
			t.Fatalf("out.cleanup %s read as %q", policy, config.Out.Cleanup)
		}
	}
	// What Render writes reads back the same.
	config := mustParse(t, complete+"out:\n  cleanup: keep\n")
	if again := mustParse(t, string(Render(config))); again.Out.Cleanup != OutKeep {
		t.Fatalf("rendered out.cleanup read as %q", again.Out.Cleanup)
	}
}

func TestEveryWorktreeCleanupPolicyReadsAndRendersBack(t *testing.T) {
	for _, policy := range []Cleanup{CleanupListener, CleanupPrune, CleanupMerged, CleanupManual} {
		content := strings.Replace(complete, "cleanup: listener", "cleanup: "+string(policy), 1)
		config := mustParse(t, content)
		if config.Worktrees.Cleanup != policy {
			t.Fatalf("worktrees.cleanup %s read as %q", policy, config.Worktrees.Cleanup)
		}
		if again := mustParse(t, string(Render(config))); again.Worktrees.Cleanup != policy {
			t.Fatalf("rendered worktrees.cleanup %s read as %q", policy, again.Worktrees.Cleanup)
		}
	}
}

func TestParseRejectsInvalidKeys(t *testing.T) {
	cases := map[string]string{
		"unknown key":        "version: 1\ntracker: linear\nbranches: {}\n",
		"tracker":            "version: 1\ntracker: asana\n",
		"version":            "version: 2\n",
		"pattern slug":       "version: 1\nbranch:\n  pattern: \"{type}/{story}\"\n",
		"pattern token":      "version: 1\nbranch:\n  pattern: \"{type}/{owner}-{slug}\"\n",
		"pattern characters": "version: 1\nbranch:\n  pattern: \"{type} {slug}\"\n",
		"base option":        "version: 1\nbranch:\n  base: --upload-pack=x\n",
		"base expression":    "version: 1\nbranch:\n  base: main~1\n",
		"kind type":          "version: 1\nbranch:\n  types: [feature]\n  kindTypes: {bugfix: bug}\n",
		"type case":          "version: 1\nbranch:\n  types: [Feature]\n",
		"max length":         "version: 1\nbranch:\n  maxLength: 5\n",
		"cleanup":            "version: 1\nworktrees:\n  cleanup: nightly\n",
		"out cleanup":        "version: 1\nout:\n  cleanup: shred\n",
		"out key":            "version: 1\nout:\n  archive: true\n",
		"location token":     "version: 1\nworktrees:\n  location: ../{user}\n",
		"lock component":     "version: 1\nbranch:\n  base: release.lock/main\n",
		"reserved base":      "version: 1\nbranch:\n  base: HEAD\n",
	}
	for name, content := range cases {
		if _, err := Parse([]byte(content)); err == nil {
			t.Errorf("%s: expected an error for %q", name, content)
		}
	}
}

func TestNameFollowsTrackerAndPattern(t *testing.T) {
	config := mustParse(t, complete)
	name, err := config.Name(Story{Type: "feature", ID: "eng-12", Description: "Add user authentication!"})
	if err != nil || name != "feature/ENG-12-add-user-authentication" {
		t.Fatalf("linear name = %q, %v", name, err)
	}
	config.Tracker = TrackerGitHub
	if name, err := config.Name(Story{Type: "bug", ID: "#53", Description: "fix login"}); err != nil || name != "bug/53-fix-login" {
		t.Fatalf("github name = %q, %v", name, err)
	}
	config.Tracker = TrackerNone
	if name, err := config.Name(Story{Type: "chore", Description: "update deps"}); err != nil || name != "chore/update-deps" {
		t.Fatalf("no-tracker name = %q, %v", name, err)
	}
	config.Tracker = TrackerLinear
	for _, story := range []Story{
		{Type: "spike", ID: "ENG-1", Description: "x"},
		{Type: "feature", Description: "no story"},
		{Type: "feature", ID: "12", Description: "wrong id"},
		{Type: "feature", ID: "ENG-1", Description: "!!!"},
		{Type: "feature", ID: "ENG-510", Description: "request sign in links through lantern"},
	} {
		if name, err := config.Name(story); err == nil {
			t.Errorf("expected %#v to be refused, got %q", story, name)
		}
	}
}

func TestForTaskDerivesTypeStoryAndSlug(t *testing.T) {
	config := mustParse(t, complete)
	task := func(kind domain.TaskKind, objective, reference string, labels ...string) domain.TaskSpec {
		return domain.TaskSpec{Kind: kind, Objective: objective, Source: domain.TaskSource{Kind: "linear", Reference: reference}, Labels: labels}
	}
	cases := []struct {
		task domain.TaskSpec
		want string
	}{
		{task(domain.TaskImplementation, "Add login", "https://linear.app/acme/issue/ENG-12/add-login"), "feature/ENG-12-add-login"},
		{task(domain.TaskBugfix, "Fix the crash", "", "hotfix", "ENG-7"), "hotfix/ENG-7-fix-the-crash"},
		{task(domain.TaskDocumentation, "ENG-9: Write the runbook", ""), "enabler/ENG-9-write-the-runbook"},
		{task(domain.TaskImplementation, "Fix UTF-8 decoding", ""), "feature/fix-utf-8-decoding"},
		{task(domain.TaskBugfix, "Request sign in links through lantern and remember them", "ENG-510"), "bug/ENG-510-request-sign-in-links-through-lantern"},
	}
	for _, current := range cases {
		got, err := config.ForTask(current.task)
		if err != nil || got != current.want {
			t.Errorf("ForTask(%q) = %q, %v; want %q", current.task.Objective, got, err, current.want)
		}
		if len(got) > config.Branch.MaxLength {
			t.Errorf("%q exceeds the cap", got)
		}
	}
	config.Tracker = TrackerGitHub
	github := domain.TaskSpec{Kind: domain.TaskImplementation, Objective: "Docs index", Source: domain.TaskSource{Kind: "github", Reference: "https://github.com/inkuna-ai/inkuna/issues/23"}}
	if got, err := config.ForTask(github); err != nil || got != "feature/23-docs-index" {
		t.Fatalf("github ForTask = %q, %v", got, err)
	}
	if got, err := config.ForTask(domain.TaskSpec{Kind: domain.TaskImplementation, Objective: "Support C# projects, closes #4"}); err != nil || got != "feature/4-support-c-projects-closes" {
		t.Fatalf("github objective ForTask = %q, %v", got, err)
	}
	// Only the #12 reference leaves the slug; the same digits elsewhere stay.
	for objective, want := range map[string]string{
		"Render 120 FPS, closes #12": "feature/12-render-120-fps-closes",
		"Fix #12 before #120":        "feature/12-fix-before-120",
		"Support C#12, closes #12":   "feature/12-support-c-12-closes",
	} {
		if got, err := config.ForTask(domain.TaskSpec{Kind: domain.TaskImplementation, Objective: objective}); err != nil || got != want {
			t.Errorf("ForTask(%q) = %q, %v; want %q", objective, got, err, want)
		}
	}
}

func TestForTaskNamesTheBranchFromTheTitle(t *testing.T) {
	config := mustParse(t, complete)
	titled := domain.TaskSpec{Kind: domain.TaskImplementation, Title: "Rate limit the public API",
		Objective: "Requests from one key can flood the API, so every endpoint slows down.\nLimit each key."}
	if got, err := config.ForTask(titled); err != nil || got != "feature/rate-limit-the-public-api" {
		t.Fatalf("a titled task's branch = %q, %v", got, err)
	}
	// A story key leading the title names the story.
	titled.Title = "ENG-31: Rate limit the public API"
	if got, err := config.ForTask(titled); err != nil || got != "feature/ENG-31-rate-limit-the-public-api" {
		t.Fatalf("a title leading with its story = %q, %v", got, err)
	}
	// A task from before titles keeps the objective's first line, held to a
	// title's length when it is longer.
	untitled := domain.TaskSpec{Kind: domain.TaskImplementation, Objective: "Rate limit the API\nLimit each key."}
	if got, err := config.ForTask(untitled); err != nil || got != "feature/rate-limit-the-api" {
		t.Fatalf("an untitled task's branch = %q, %v", got, err)
	}
	unbounded := mustParse(t, complete)
	unbounded.Branch.MaxLength = 0
	untitled.Objective = strings.Repeat("handler ", 60) + "\nMore"
	if got, err := unbounded.ForTask(untitled); err != nil || len(got) > len("feature/")+domain.MaxTitleRunes || !strings.HasPrefix(got, "feature/handler-handler") {
		t.Fatalf("a long untitled first line's branch = %q (%d bytes), %v", got, len(got), err)
	}
}

func TestARefComponentFitsInAFileName(t *testing.T) {
	fits := strings.Repeat("a", maxRefComponent)
	directory := strings.Repeat("d", maxFileName)
	for name, ok := range map[string]bool{
		"feature/" + fits:              true,
		"feature/" + fits + "b":        false,
		fits + "b/short":               true,
		directory + "/short":           true,
		directory + "d/short":          false,
		"feature/" + fits + "/" + fits: true,
	} {
		if err := checkRefName(name); (err == nil) != ok {
			t.Errorf("checkRefName(%d-byte name) = %v, want ok %v", len(name), err, ok)
		}
	}
	// A description typed by hand has no cap without branch.maxLength, so
	// the name is refused with the reason before git tries to lock it.
	config := mustParse(t, complete)
	config.Branch.MaxLength = 0
	_, err := config.Name(Story{Type: "feature", ID: "ENG-1", Description: strings.Repeat("handler ", 40)})
	if err == nil || !strings.Contains(err.Error(), "has a component of 325 bytes; at most 250 fit in a file name") {
		t.Fatalf("a 325-byte component: %v", err)
	}
}

func TestRenderDropsAbsentStory(t *testing.T) {
	cases := map[string]string{
		"{type}/{story}-{slug}": "feature/slug",
		"{slug}-{story}":        "slug",
		"{type}/{story}/{slug}": "feature/slug",
		"{story}/{slug}":        "slug",
		"{type}/{slug}/{story}": "feature/slug",
		"inkuna/{slug}":         "inkuna/slug",
	}
	for pattern, want := range cases {
		if got := render(pattern, "feature", "", "slug"); got != want {
			t.Errorf("render(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestUniqueSuffixesWithinCap(t *testing.T) {
	taken := map[string]bool{"feature/abc": true, "feature/abc-2": true}
	got, err := Unique("feature/abc", 0, func(name string) bool { return taken[name] })
	if err != nil || got != "feature/abc-3" {
		t.Fatalf("Unique = %q, %v", got, err)
	}
	long := "feature/ENG-1-" + strings.Repeat("x", 36)
	got, err = Unique(long, 50, func(name string) bool { return name == long })
	if err != nil || len(got) > 50 || !strings.HasSuffix(got, "-2") {
		t.Fatalf("Unique within cap = %q, %v", got, err)
	}
}

func TestRootResolvesLocation(t *testing.T) {
	parent := t.TempDir()
	repository := filepath.Join(parent, "bakery")
	config := Defaults()
	data := t.TempDir()
	if root, err := config.Root(repository, data); err != nil || root != filepath.Join(data, "worktrees", "bakery") {
		t.Fatalf("default root = %q, %v", root, err)
	}
	config.Worktrees.Location = "../{repo}-worktrees"
	if root, err := config.Root(repository, ""); err != nil || root != filepath.Join(parent, "bakery-worktrees") {
		t.Fatalf("sibling root = %q, %v", root, err)
	}
	config.Worktrees.Location = ".worktrees"
	if _, err := config.Root(repository, ""); err == nil {
		t.Fatal("a location inside the repository must be refused")
	}
	if got := Path("/w", "feature/ENG-1-x"); got != filepath.Join("/w", "feature", "ENG-1-x") {
		t.Fatalf("Path = %q", got)
	}
}

func TestInferAndResolve(t *testing.T) {
	evidence := Evidence{
		Repository: "bakery", RemoteHost: "github.com", DefaultBranch: "main", CurrentBranch: "main",
		Branches:  []string{"feature/ENG-527-settings", "bug/ENG-575-switch", "main"},
		Worktrees: []string{filepath.Join("..", "bakery-worktrees", "feature", "ENG-527-settings")},
	}
	inferred := Infer(evidence)
	if inferred.Tracker != TrackerLinear || inferred.Pattern != "{type}/{story}-{slug}" || inferred.Base != "main" || inferred.Location != "../{repo}-worktrees" {
		t.Fatalf("Infer = %#v", inferred)
	}
	if got := Infer(Evidence{RemoteHost: "github.com", Branches: []string{"feature/23-docs-index", "main"}}); got.Tracker != TrackerGitHub {
		t.Fatalf("github evidence inferred %q", got.Tracker)
	}
	if got := Infer(Evidence{Branches: []string{"wip", "master"}}); got.Tracker != TrackerNone || got.Base != "master" || got.Pattern != "{type}/{slug}" {
		t.Fatalf("plain evidence inferred %#v", got)
	}

	proposed := &Proposal{Tracker: TrackerJira, Base: "develop", Pattern: "{type}/{story}_{slug}"}
	config, values, notes := Resolve(Defaults(), Required, proposed, evidence)
	if config.Tracker != TrackerJira || config.Branch.Pattern != "{type}/{story}_{slug}" {
		t.Fatalf("valid proposals were not used: %#v", config)
	}
	if config.Branch.Base != "main" || values["branch.base"] != "main" || !strings.Contains(strings.Join(notes, "\n"), "no branch \"develop\"") {
		t.Fatalf("a base the repository lacks must fall back: %#v %v", config.Branch, notes)
	}
	if values["worktrees.location"] != "../{repo}-worktrees" {
		t.Fatalf("a new file should take the inferred location: %v", values)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	_, values, _ = Resolve(Defaults(), []string{"branch.pattern"}, nil, evidence)
	if len(values) != 1 || values["branch.pattern"] == "" {
		t.Fatalf("only the missing key may be filled: %v", values)
	}
}

func TestEmptyOrSkeletonFilesAreMissingKeysNotErrors(t *testing.T) {
	for _, content := range []string{"", "# branching settings go here\n", "tracker: none\nbranch:\n"} {
		loaded, err := Parse([]byte(content))
		if err != nil || !loaded.Exists || len(loaded.Missing) == 0 {
			t.Fatalf("Parse(%q) = %#v, %v", content, loaded, err)
		}
		repository := t.TempDir()
		path := filepath.Join(repository, filepath.FromSlash(RelativePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		values := map[string]string{"tracker": "github", "branch.base": "main", "branch.pattern": "{story}/{slug}"}
		if _, err := Apply(repository, values); err != nil {
			t.Fatalf("Apply onto %q: %v", content, err)
		}
		completed, err := Load(repository)
		if err != nil || !completed.Complete() {
			t.Fatalf("Apply onto %q left %#v, %v", content, completed, err)
		}
	}
}

func TestApplyCreatesOrCompletesFile(t *testing.T) {
	repository := t.TempDir()
	values := map[string]string{"tracker": "github", "branch.base": "main", "branch.pattern": "{type}/{story}-{slug}", "worktrees.location": "../{repo}-worktrees"}
	written, err := Apply(repository, values)
	if err != nil || len(written) != len(Required) {
		t.Fatalf("Apply new file = %v, %v", written, err)
	}
	loaded, err := Load(repository)
	if err != nil || !loaded.Complete() || loaded.Config.Tracker != TrackerGitHub || loaded.Config.Worktrees.Location != "../{repo}-worktrees" {
		t.Fatalf("written file does not load complete: %#v %v", loaded, err)
	}

	partial := "# team choice: keep worktrees in the checkout\nversion: 1\nbranch:\n  base: trunk # protected\nworktrees:\n  enabled: false\n"
	path := filepath.Join(repository, filepath.FromSlash(RelativePath))
	if err := os.WriteFile(path, []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err = Apply(repository, values)
	if err != nil || strings.Join(written, ",") != "tracker,branch.pattern" {
		t.Fatalf("Apply partial = %v, %v", written, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "# team choice") || !strings.Contains(text, "# protected") || !strings.Contains(text, "base: trunk") {
		t.Fatalf("existing content and comments were not kept:\n%s", text)
	}
	loaded, err = Load(repository)
	if err != nil || !loaded.Complete() || loaded.Config.Branch.Base != "trunk" || loaded.Config.Worktrees.Enabled || loaded.Config.Branch.Pattern != "{type}/{story}-{slug}" {
		t.Fatalf("completed file: %#v %v\n%s", loaded, err, text)
	}
}
