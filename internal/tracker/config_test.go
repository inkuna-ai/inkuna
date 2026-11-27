package tracker

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestParseRejectsUnknownKeys(t *testing.T) {
	for _, document := range []string{
		"version: 1\nwrite: [comment]\n",
		"version: 1\nupdates: { begin: true }\n",
		"version: 1\ngithub: { project: { owner: acme, number: 1, title: Roadmap } }\n",
		"version: 1\n---\nversion: 1\n",
		"",
	} {
		if _, err := Parse([]byte(document)); err == nil {
			t.Errorf("Parse(%q) accepted it", document)
		}
	}
}

func TestValidateChecksEveryKey(t *testing.T) {
	project := &Project{Owner: "acme", Number: 3}
	for _, test := range []struct {
		name    string
		config  Config
		tracker branching.Tracker
		want    string
	}{
		{"version", Config{Version: 2}, branching.TrackerLinear, "version must be 1"},
		{"unknown kind", Config{Version: 1, Writes: []domain.TrackerKind{"archive"}}, branching.TrackerLinear, `writes names "archive"`},
		{"twice", Config{Version: 1, Writes: []domain.TrackerKind{"comment", "comment"}}, branching.TrackerLinear, "writes lists comment twice"},
		{"owner", Config{Version: 1, GitHub: GitHubConfig{Project: &Project{Owner: "-acme", Number: 1}}}, branching.TrackerGitHub, "github.project.owner"},
		{"number", Config{Version: 1, GitHub: GitHubConfig{Project: &Project{Owner: "acme"}}}, branching.TrackerGitHub, "github.project.number"},
		{"two lines", Config{Version: 1, GitHub: GitHubConfig{Project: project, InProgress: "In\nProgress"}}, branching.TrackerGitHub, "github.inProgress must be one line"},
		{"status needs a project", Config{Version: 1, Writes: []domain.TrackerKind{"status"}, Updates: Updates{Done: true}}, branching.TrackerGitHub, "writes: status and updates.done set a GitHub issue's status"},
	} {
		err := test.config.Validate(test.tracker)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: Validate = %v, want %q", test.name, err, test.want)
		}
	}
	valid := []struct {
		config  Config
		tracker branching.Tracker
	}{
		{Config{Version: 1}, branching.TrackerGitHub},
		{Config{Version: 1, Writes: []domain.TrackerKind{"comment", "create", "edit"}, Updates: Updates{Result: true}}, branching.TrackerGitHub},
		{Config{Version: 1, Writes: domain.TrackerKinds(), Updates: Updates{Start: true, Done: true}, GitHub: GitHubConfig{Project: project}}, branching.TrackerGitHub},
		// Linear statuses are the team's own states, so no project is needed.
		{Config{Version: 1, Writes: []domain.TrackerKind{"status"}, Updates: Updates{Start: true}}, branching.TrackerLinear},
	}
	for _, test := range valid {
		if err := test.config.Validate(test.tracker); err != nil {
			t.Errorf("Validate(%+v) = %v", test.config, err)
		}
	}
}

func TestGitHubConfigDefaults(t *testing.T) {
	var github GitHubConfig
	if github.StatusField() != "Status" || github.InProgressOption() != "In Progress" || github.DoneOption() != "Done" {
		t.Fatalf("defaults = %q %q %q", github.StatusField(), github.InProgressOption(), github.DoneOption())
	}
	github = GitHubConfig{Field: "Stage", InProgress: "Doing", Done: "Shipped"}
	if github.StatusField() != "Stage" || github.InProgressOption() != "Doing" || github.DoneOption() != "Shipped" {
		t.Fatal("configured names were not used")
	}
}

// repository writes a checkout's .inkuna files.
func repository(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, ".inkuna", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const branchingFor = "version: 1\ntracker: %s\nbranch: { base: main, pattern: \"{type}/{story}-{slug}\" }\n"

func branchingYAML(tracker string) string { return strings.Replace(branchingFor, "%s", tracker, 1) }

func TestResolveWithoutTrackerYAMLAllowsNothing(t *testing.T) {
	root := repository(t, map[string]string{"branching.yaml": branchingYAML("github")})
	settings, err := Resolve(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Exists || !settings.Effective.None() || !strings.Contains(settings.Note, "no .inkuna/tracker.yaml") {
		t.Fatalf("settings = %+v", settings)
	}
}

func TestResolveNamesTrackersWithoutAnAdapter(t *testing.T) {
	everything := "version: 1\nwrites: [status, comment, edit, create]\nupdates: { start: true, result: true, done: true }\n"
	for tracker, note := range map[string]string{
		"jira": "cannot write to Jira yet",
		"none": "tracker is none",
	} {
		root := repository(t, map[string]string{"branching.yaml": branchingYAML(tracker), "tracker.yaml": everything})
		settings, err := Resolve(root, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !settings.Effective.None() || !strings.Contains(settings.Note, note) {
			t.Errorf("%s: settings = %+v", tracker, settings)
		}
	}
	root := repository(t, map[string]string{"tracker.yaml": everything})
	if settings, err := Resolve(root, nil); err != nil || !settings.Effective.None() || !strings.Contains(settings.Note, "names no tracker") {
		t.Fatalf("without branching.yaml: %+v, %v", settings, err)
	}
}

func TestResolveReportsAnInvalidFile(t *testing.T) {
	root := repository(t, map[string]string{"branching.yaml": branchingYAML("github"), "tracker.yaml": "version: 1\nwrites: [status]\n"})
	settings, err := Resolve(root, nil)
	if err == nil || !strings.Contains(err.Error(), "github.project") {
		t.Fatalf("Resolve = %v", err)
	}
	if !settings.Effective.None() {
		t.Fatalf("an invalid file allowed %+v", settings.Effective)
	}
}

func TestTheMachineCanOnlyLowerTheCeiling(t *testing.T) {
	root := repository(t, map[string]string{
		"branching.yaml": branchingYAML("linear"),
		"tracker.yaml":   "version: 1\nwrites: [comment, create]\nupdates: { start: true, result: true }\n",
	})
	none := []domain.TrackerKind{}
	off, on := false, true
	for _, test := range []struct {
		name     string
		override *config.TrackerOverride
		want     Allowance
	}{
		{"no override", nil, Allowance{Writes: []domain.TrackerKind{"comment", "create"}, Updates: Updates{Start: true, Result: true}}},
		{"empty override", &config.TrackerOverride{}, Allowance{Writes: []domain.TrackerKind{"comment", "create"}, Updates: Updates{Start: true, Result: true}}},
		{
			"cannot raise", &config.TrackerOverride{Writes: &[]domain.TrackerKind{"status", "edit", "comment"}, Updates: config.TrackerUpdatesOverride{Done: &on, Start: &on}},
			Allowance{Writes: []domain.TrackerKind{"comment"}, Updates: Updates{Start: true, Result: true}},
		},
		{
			"switches off", &config.TrackerOverride{Writes: &none, Updates: config.TrackerUpdatesOverride{Result: &off}},
			Allowance{Writes: []domain.TrackerKind{}, Updates: Updates{Start: true}},
		},
	} {
		settings, err := Resolve(root, test.override)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(settings.Effective, test.want) {
			t.Errorf("%s: effective = %+v, want %+v", test.name, settings.Effective, test.want)
		}
		if settings.Ceiling.Allows(domain.TrackerStatus) || settings.Ceiling.AllowsUpdate(domain.UpdateDone) {
			t.Errorf("%s: the ceiling changed: %+v", test.name, settings.Ceiling)
		}
	}
	everythingOff := &config.TrackerOverride{Writes: &none, Updates: config.TrackerUpdatesOverride{Start: &off, Result: &off}}
	if settings, _ := Resolve(root, everythingOff); !strings.Contains(settings.Note, "inkuna.yaml turns every write off") {
		t.Fatalf("note = %q", settings.Note)
	}
}

// A run writes within what it was approved under and what the repository
// and the machine allow now: a change since, such as a step's edit to
// tracker.yaml in a checkout, can switch a write off but never on.
func TestARunsSettingsCanOnlyNarrowAfterApproval(t *testing.T) {
	approved, err := Resolve(repository(t, map[string]string{
		"branching.yaml": branchingYAML("linear"),
		"tracker.yaml":   "version: 1\nwrites: [comment]\nupdates: { start: true, result: true }\n",
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	raised, err := Resolve(repository(t, map[string]string{
		"branching.yaml": branchingYAML("linear"),
		"tracker.yaml":   "version: 1\nwrites: [status, comment, edit]\nupdates: { start: true, done: true }\n",
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	within := approved.Within(raised)
	want := Allowance{Writes: []domain.TrackerKind{domain.TrackerComment}, Updates: Updates{Start: true}}
	if !reflect.DeepEqual(within.Effective, want) || !reflect.DeepEqual(within.Ceiling, want) {
		t.Fatalf("within = %+v / %+v, want %+v", within.Ceiling, within.Effective, want)
	}
	gone, _ := Resolve(repository(t, map[string]string{"branching.yaml": branchingYAML("linear")}), nil)
	if within := approved.Within(gone); !within.Effective.None() || !strings.Contains(within.Note, "no .inkuna/tracker.yaml") {
		t.Fatalf("without tracker.yaml now: %+v", within)
	}
	moved, _ := Resolve(repository(t, map[string]string{
		"branching.yaml": branchingYAML("github"),
		"tracker.yaml":   "version: 1\nwrites: [comment]\nupdates: { result: true }\n",
	}), nil)
	if within := approved.Within(moved); !within.Effective.None() || !strings.Contains(within.Note, "GitHub issues now, not Linear") {
		t.Fatalf("another tracker now: %+v", within)
	}
}
