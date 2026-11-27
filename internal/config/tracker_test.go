package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestTrackerOverrideIsChecked(t *testing.T) {
	file := DefaultFile()
	writes := []domain.TrackerKind{"comment", "archive", "comment"}
	file.Repositories = []RepositoryConfig{{Name: "lantern", Path: "/src/lantern", Tracker: &TrackerOverride{Writes: &writes}}}
	problems := file.Problems()
	messages := make([]string, 0, len(problems))
	for _, problem := range problems {
		if problem.Field != "repositories" {
			t.Errorf("problem %q is not on repositories", problem.Message)
		}
		messages = append(messages, problem.Message)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, `tracker.writes names "archive"`) || !strings.Contains(joined, "tracker.writes lists comment twice") {
		t.Fatalf("problems = %v", messages)
	}
}

// An override that turns every kind off is kept as an empty list, which is
// not the same as no list: the first allows nothing, the second whatever
// the repository allows.
func TestTrackerOverrideRoundTripsThroughInkunaYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.yaml")
	file := DefaultFile()
	none := []domain.TrackerKind{}
	off := false
	file.Repositories = []RepositoryConfig{
		{Name: "lantern", Path: "/src/lantern", Tracker: &TrackerOverride{Writes: &none, Updates: TrackerUpdatesOverride{Start: &off}}},
		{Name: "bakery", Path: "/src/bakery"},
	}
	if err := WriteFile(path, file); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "writes: []") || strings.Count(string(data), "tracker:") != 1 {
		t.Fatalf("written:\n%s", data)
	}
	read, _, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lantern := read.Repositories[0].Tracker
	if lantern == nil || lantern.Writes == nil || len(*lantern.Writes) != 0 || lantern.Keeps(domain.TrackerComment) {
		t.Fatalf("lantern override = %+v", lantern)
	}
	if lantern.KeepsUpdate(domain.UpdateStart) || !lantern.KeepsUpdate(domain.UpdateResult) {
		t.Fatalf("lantern updates = %+v", lantern.Updates)
	}
	if bakery := read.Repositories[1].Tracker; bakery != nil || !bakery.Keeps(domain.TrackerStatus) || !bakery.KeepsUpdate(domain.UpdateDone) {
		t.Fatalf("no override must keep everything: %+v", bakery)
	}
	// The settings view sends the same shape as JSON.
	var decoded RepositoryConfig
	if err := json.Unmarshal([]byte(`{"name":"lantern","path":"/src/lantern","defaultRuntime":"","tracker":{"writes":[],"updates":{"done":false}}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Tracker.Writes == nil || decoded.Tracker.KeepsUpdate(domain.UpdateDone) || !decoded.Tracker.KeepsUpdate(domain.UpdateStart) {
		t.Fatalf("decoded = %+v", decoded.Tracker)
	}
}

func TestTheLinearKeyIsNoLongerDescribedAsReadOnly(t *testing.T) {
	for _, secret := range DefaultFile().Secrets {
		if secret.Name == EnvLinearAPIKey && strings.Contains(strings.ToLower(secret.Description), "read-only") {
			t.Fatalf("description = %q", secret.Description)
		}
	}
}

// Two entries for one checkout, one of them through a symbolic link, both
// lower it: taking only the first would drop the second's switches.
func TestEveryEntryForACheckoutLowersItsTrackerWrites(t *testing.T) {
	checkout := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(checkout, link); err != nil {
		t.Fatal(err)
	}
	first, second, off := []domain.TrackerKind{domain.TrackerComment, domain.TrackerStatus}, []domain.TrackerKind{domain.TrackerStatus}, false
	file := DefaultFile()
	file.Repositories = []RepositoryConfig{
		{Name: "app", Path: checkout, Tracker: &TrackerOverride{Writes: &first}},
		{Name: "app-again", Path: link + "/", Tracker: &TrackerOverride{Writes: &second, Updates: TrackerUpdatesOverride{Done: &off}}},
		{Name: "other", Path: t.TempDir(), Tracker: &TrackerOverride{Updates: TrackerUpdatesOverride{Start: &off}}},
	}
	merged := file.TrackerOverride(checkout)
	if merged == nil || merged.Keeps(domain.TrackerComment) || !merged.Keeps(domain.TrackerStatus) || merged.KeepsUpdate(domain.UpdateDone) || !merged.KeepsUpdate(domain.UpdateStart) {
		t.Fatalf("merged = %+v", merged)
	}
	if file.TrackerOverride(t.TempDir()) != nil {
		t.Fatal("a checkout no entry declares was lowered")
	}
}
