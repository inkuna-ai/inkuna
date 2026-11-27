package config

import (
	"fmt"
	"path/filepath"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// TrackerOverride lowers, on this machine, what a repository's
// .inkuna/tracker.yaml lets Inkuna write to its tracker. It can switch a
// kind or an update off, never on: a key it leaves out keeps what the
// repository allows (docs/mcp-and-trackers.md).
type TrackerOverride struct {
	// Writes, when set, keeps only the kinds it lists; an empty list allows
	// none.
	Writes *[]domain.TrackerKind `yaml:"writes,omitempty" json:"writes,omitempty"`
	// Updates switches Inkuna's own updates off one by one.
	Updates TrackerUpdatesOverride `yaml:"updates,omitempty" json:"updates,omitempty"`
}

// TrackerUpdatesOverride holds one switch per update; nil keeps the
// repository's setting and false turns the update off.
type TrackerUpdatesOverride struct {
	Start  *bool `yaml:"start,omitempty" json:"start,omitempty"`
	Result *bool `yaml:"result,omitempty" json:"result,omitempty"`
	Done   *bool `yaml:"done,omitempty" json:"done,omitempty"`
}

// Keeps reports whether the override leaves an update on where the
// repository turns it on.
func (updates TrackerUpdatesOverride) Keeps(update domain.TrackerUpdate) bool {
	var value *bool
	switch update {
	case domain.UpdateStart:
		value = updates.Start
	case domain.UpdateResult:
		value = updates.Result
	case domain.UpdateDone:
		value = updates.Done
	}
	return value == nil || *value
}

// Keeps reports whether the override leaves a kind on where the repository
// allows it.
func (override *TrackerOverride) Keeps(kind domain.TrackerKind) bool {
	if override == nil || override.Writes == nil {
		return true
	}
	for _, kept := range *override.Writes {
		if kept == kind {
			return true
		}
	}
	return false
}

// KeepsUpdate is Keeps for one of Inkuna's own updates.
func (override *TrackerOverride) KeepsUpdate(update domain.TrackerUpdate) bool {
	return override == nil || override.Updates.Keeps(update)
}

// TrackerOverride is what every entry declared for a repository path adds
// up to: a kind or update stays on only when each entry that says anything
// about it keeps it. A path can be declared more than once, and spelt
// through a symbolic link (/tmp and /private/tmp on macOS), so the entries
// are matched after links resolve; taking only the first match could drop
// a later entry that switches a write off. Nil means no entry lowers
// anything.
func (file File) TrackerOverride(path string) *TrackerOverride {
	var merged *TrackerOverride
	for _, entry := range file.Repositories {
		if entry.Tracker == nil || !samePath(entry.Path, path) {
			continue
		}
		if merged == nil {
			merged = &TrackerOverride{}
		}
		if entry.Tracker.Writes != nil {
			kept := make([]domain.TrackerKind, 0, len(*entry.Tracker.Writes))
			for _, kind := range *entry.Tracker.Writes {
				if merged.Keeps(kind) {
					kept = append(kept, kind)
				}
			}
			merged.Writes = &kept
		}
		off := false
		for _, update := range []struct{ from, into **bool }{
			{&entry.Tracker.Updates.Start, &merged.Updates.Start},
			{&entry.Tracker.Updates.Result, &merged.Updates.Result},
			{&entry.Tracker.Updates.Done, &merged.Updates.Done},
		} {
			if *update.from != nil && !**update.from {
				*update.into = &off
			}
		}
	}
	return merged
}

// samePath reports whether two paths name one directory, after cleaning and,
// where both exist, resolving symbolic links.
func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if left == right {
		return true
	}
	resolvedLeft, leftErr := filepath.EvalSymlinks(left)
	resolvedRight, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && resolvedLeft == resolvedRight
}

// trackerProblems checks a repository's override.
func (repository RepositoryConfig) trackerProblems() []string {
	if repository.Tracker == nil || repository.Tracker.Writes == nil {
		return nil
	}
	problems := make([]string, 0)
	seen := make(map[domain.TrackerKind]bool)
	for _, kind := range *repository.Tracker.Writes {
		switch {
		case !domain.ValidTrackerKind(kind):
			problems = append(problems, fmt.Sprintf("repository %q: tracker.writes names %q; the kinds are status, comment, edit, and create", repository.Name, kind))
		case seen[kind]:
			problems = append(problems, fmt.Sprintf("repository %q: tracker.writes lists %s twice", repository.Name, kind))
		}
		seen[kind] = true
	}
	return problems
}
