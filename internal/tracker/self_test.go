package tracker

import (
	"testing"

	"github.com/inkuna-ai/inkuna/internal/branching"
)

// Inkuna's own tracker settings are valid for the tracker its branching
// configuration names, so a change to them that would refuse every write,
// or name a status no Project could hold, fails the build instead.
func TestInkunaTrackerSettingsAreValid(t *testing.T) {
	branchingConfig, err := branching.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load("../..", branchingConfig.Config.Tracker)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Exists {
		// Without the file Inkuna writes nothing to its issues, which is
		// valid; there is nothing to check.
		return
	}
	settings, err := Resolve("../..", nil)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Tracker != branching.TrackerGitHub || settings.Config.GitHub.Project == nil {
		t.Fatalf("Inkuna's issues are on GitHub, whose status is a Project's field: %+v", settings)
	}
}
