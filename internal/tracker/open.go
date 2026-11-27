package tracker

import (
	"errors"
	"fmt"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/github"
	"github.com/inkuna-ai/inkuna/internal/linear"
)

// Sources are what the adapters reach their trackers with: the Linear key's
// client, and gh for the repository's origin.
type Sources struct {
	// Linear returns a client holding the Linear key, or why there is none.
	Linear func() (*linear.Client, error)
	// GitHub runs gh for Repository, the origin as gh's --repo takes it
	// (HOST/OWNER/NAME), and GitHubLogin returns gh's signed-in account
	// there. They are asked for only when the tracker is GitHub's.
	GitHub      github.Runner
	Repository  string
	GitHubLogin func() (string, error)
}

// open returns the adapter for the tracker the settings name. A tracker
// Inkuna cannot write to is an error that says why. It is not exported:
// an adapter reaches a tracker only through a Guard.
func open(settings Settings, sources Sources) (domain.Tracker, error) {
	switch settings.Tracker {
	case branching.TrackerLinear:
		if sources.Linear == nil {
			return nil, errors.New("no Linear key is available")
		}
		client, err := sources.Linear()
		if err != nil {
			return nil, fmt.Errorf("open Linear: %w", err)
		}
		return linear.NewTracker(client)
	case branching.TrackerGitHub:
		if sources.Repository == "" {
			return nil, errors.New("the repository has no GitHub origin to write its issues to")
		}
		if sources.GitHubLogin == nil {
			return nil, errors.New("gh's signed-in account is unknown")
		}
		login, err := sources.GitHubLogin()
		if err != nil {
			return nil, fmt.Errorf("find gh's account: %w", err)
		}
		var project *github.ProjectStatus
		if configured := settings.Config.GitHub.Project; configured != nil {
			project = &github.ProjectStatus{Owner: configured.Owner, Number: configured.Number, Field: settings.Config.GitHub.StatusField()}
		}
		return github.NewTracker(sources.GitHub, sources.Repository, login, project)
	}
	if settings.Note != "" {
		return nil, errors.New(settings.Note)
	}
	return nil, fmt.Errorf("inkuna cannot write to %s", trackerLabel(settings.Tracker))
}

// OpenGuard opens the repository's adapter and guards it for a run whose
// story is story. When the repository has no tracker Inkuna can write to,
// or the adapter cannot be opened, the guard still records every write it
// is asked for, refusing each with the reason.
func OpenGuard(settings Settings, sources Sources, story string, record Recorder) (*Guard, error) {
	var adapter domain.Tracker
	if Writable(settings.Tracker) && settings.Exists {
		opened, err := open(settings, sources)
		if err != nil {
			settings.Note = err.Error()
		} else {
			adapter = opened
		}
	}
	return NewGuard(adapter, settings, story, record)
}
