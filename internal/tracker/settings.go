package tracker

import (
	"fmt"
	"slices"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// Allowance is what Inkuna may write: the kinds agents may ask for and the
// updates it makes on its own.
type Allowance struct {
	Writes  []domain.TrackerKind `json:"writes"`
	Updates Updates              `json:"updates"`
}

// Allows reports whether agents may ask for kind.
func (allowance Allowance) Allows(kind domain.TrackerKind) bool {
	return slices.Contains(allowance.Writes, kind)
}

// AllowsUpdate reports whether Inkuna makes update on its own.
func (allowance Allowance) AllowsUpdate(update domain.TrackerUpdate) bool {
	return allowance.Updates.On(update)
}

// None reports whether nothing at all may be written.
func (allowance Allowance) None() bool {
	return len(allowance.Writes) == 0 && allowance.Updates == Updates{}
}

// Settings is what Inkuna may write to one repository's tracker on this
// machine, and why.
type Settings struct {
	// Tracker is the one the repository's branching.yaml names.
	Tracker branching.Tracker `json:"tracker"`
	// Config is the repository's tracker.yaml; Exists is false without one.
	Config Config `json:"config"`
	Exists bool   `json:"exists"`
	Path   string `json:"path"`
	// Ceiling is what tracker.yaml allows. Effective is what is left once
	// the machine's inkuna.yaml lowered it, and what a Guard enforces.
	Ceiling   Allowance `json:"ceiling"`
	Effective Allowance `json:"effective"`
	// Note says why nothing may be written, when nothing may.
	Note string `json:"note,omitempty"`
}

// InProgressStatus is the status updates.start moves a story to: the
// GitHub Project's option for In Progress, or the Linear state tracker.yaml
// names, In Progress by default. A Linear team without a state of that name
// gets its first started state instead.
func (settings Settings) InProgressStatus() string {
	if settings.Tracker == branching.TrackerGitHub {
		return settings.Config.GitHub.InProgressOption()
	}
	return firstNonEmpty(settings.Config.Linear.InProgress, DefaultInProgress)
}

// DoneStatus is the status updates.done moves a story to: the GitHub
// Project's option for Done, or Linear's Done, which a team without a state
// of that name replaces with its first completed state.
func (settings Settings) DoneStatus() string {
	if settings.Tracker == branching.TrackerGitHub {
		return settings.Config.GitHub.DoneOption()
	}
	return DefaultDone
}

// Within is what settings saved when a run was approved still allow, given
// the repository's and this machine's settings now: a kind or update stays
// on only when both allow it, so a change since can switch a run's writes
// off, never on, and a step that edited tracker.yaml in a checkout cannot
// raise its own run's ceiling. A repository whose tracker changed since
// allows the run nothing.
func (settings Settings) Within(current Settings) Settings {
	narrowed := settings
	narrowed.Ceiling = intersect(settings.Ceiling, current.Ceiling)
	narrowed.Effective = intersect(settings.Effective, current.Effective)
	switch {
	case current.Tracker != settings.Tracker:
		narrowed.Ceiling, narrowed.Effective = Allowance{Writes: []domain.TrackerKind{}}, Allowance{Writes: []domain.TrackerKind{}}
		narrowed.Note = fmt.Sprintf("the repository's tracker is %s now, not %s as when the run was approved, so nothing is written", trackerLabel(current.Tracker), trackerLabel(settings.Tracker))
	case narrowed.Note == "" && narrowed.Effective.None():
		narrowed.Note = firstNonEmpty(current.Note, "what the repository allows now leaves this run nothing to write")
	}
	return narrowed
}

func intersect(left, right Allowance) Allowance {
	both := Allowance{Writes: []domain.TrackerKind{}}
	for _, kind := range left.Writes {
		if right.Allows(kind) {
			both.Writes = append(both.Writes, kind)
		}
	}
	both.Updates = Updates{
		Start:  left.Updates.Start && right.Updates.Start,
		Result: left.Updates.Result && right.Updates.Result,
		Done:   left.Updates.Done && right.Updates.Done,
	}
	return both
}

// Writable reports whether Inkuna has an adapter for the tracker.
func Writable(tracker branching.Tracker) bool {
	return tracker == branching.TrackerLinear || tracker == branching.TrackerGitHub
}

// Resolve works out what Inkuna may write to a repository's tracker: the
// tracker its branching.yaml names, the ceiling its tracker.yaml sets, and
// what is left once the machine's override lowered it. An error means a
// file is unreadable or invalid; the settings then allow nothing.
func Resolve(repositoryRoot string, override *config.TrackerOverride) (Settings, error) {
	branchingConfig, err := branching.Load(repositoryRoot)
	if err != nil {
		return Settings{Note: "nothing is written while branching.yaml is invalid"}, err
	}
	settings := Settings{Tracker: branchingConfig.Config.Tracker}
	if slices.Contains(branchingConfig.Missing, "tracker") {
		settings.Tracker = ""
	}
	loaded, err := Load(repositoryRoot, settings.Tracker)
	settings.Path = loaded.Path
	if err != nil {
		settings.Note = "nothing is written while tracker.yaml is invalid"
		return settings, err
	}
	settings.Config, settings.Exists = loaded.Config, loaded.Exists
	settings.Ceiling = Allowance{Writes: append([]domain.TrackerKind{}, loaded.Config.Writes...), Updates: loaded.Config.Updates}
	settings.Effective = Allowance{Writes: []domain.TrackerKind{}}
	switch {
	case settings.Tracker == "":
		settings.Note = "branching.yaml names no tracker, so Inkuna writes to none"
	case settings.Tracker == branching.TrackerNone:
		settings.Note = "the repository's tracker is none, so there is nothing to write to"
	case !Writable(settings.Tracker):
		settings.Note = fmt.Sprintf("Inkuna cannot write to %s yet, so nothing is written", trackerLabel(settings.Tracker))
	case !loaded.Exists:
		settings.Note = "the repository has no " + RelativePath + ", so nothing is written"
	}
	if settings.Note != "" {
		return settings, nil
	}
	settings.Effective = Narrow(settings.Ceiling, override)
	switch {
	case settings.Ceiling.None():
		settings.Note = RelativePath + " allows no writes"
	case settings.Effective.None():
		settings.Note = "inkuna.yaml turns every write off for this repository on this machine"
	}
	return settings, nil
}

// Narrow is what is left of a repository's ceiling once a machine's
// override lowered it: a kind or update stays on only when the ceiling
// allows it and the override, where it says anything, keeps it.
func Narrow(ceiling Allowance, override *config.TrackerOverride) Allowance {
	narrowed := Allowance{Writes: []domain.TrackerKind{}}
	for _, kind := range ceiling.Writes {
		if override.Keeps(kind) {
			narrowed.Writes = append(narrowed.Writes, kind)
		}
	}
	narrowed.Updates = Updates{
		Start:  ceiling.Updates.Start && override.KeepsUpdate(domain.UpdateStart),
		Result: ceiling.Updates.Result && override.KeepsUpdate(domain.UpdateResult),
		Done:   ceiling.Updates.Done && override.KeepsUpdate(domain.UpdateDone),
	}
	return narrowed
}

// trackerLabel names a tracker as people write it.
func trackerLabel(tracker branching.Tracker) string {
	switch tracker {
	case branching.TrackerLinear:
		return "Linear"
	case branching.TrackerGitHub:
		return "GitHub issues"
	case branching.TrackerJira:
		return "Jira"
	}
	return string(tracker)
}
