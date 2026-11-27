package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/inkuna-ai/inkuna/internal/sources"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// sourceTimeout bounds how long Inkuna waits for one source's snapshot. A
// source that takes longer is treated as one that cannot be reached, so its
// cached snapshot is used, rather than leaving a plan waiting. Tests
// shorten it.
var sourceTimeout = 30 * time.Second

// registeredSource is one definition source, named as a sink is: by its
// extension and its place among that extension's sources, such as
// "example/source", which is also what its cached snapshots are kept by.
type registeredSource struct {
	name   string
	source extension.DefinitionSource
}

// addSources takes one extension's definition sources.
func (registry *extensionRegistry) addSources(name string, registered []extension.DefinitionSource) error {
	for index, source := range registered {
		if source == nil {
			return errors.New("its definition source is nil")
		}
		registry.sources = append(registry.sources, registeredSource{name: sinkName(name, "source", index), source: source})
	}
	return nil
}

// hasSources reports whether the build has a definition source. Without one
// nothing asks who is acting or reads a project's origin for them, so a
// build without sources plans as before.
func (service *Service) hasSources() bool {
	return service.extensions != nil && len(service.extensions.sources) > 0
}

// readSources asks every source for the project's snapshot at once and
// reads what they share. A source that cannot be reached, or cannot be
// asked because no one can be named, is read from the snapshot it last
// gave for the project.
func (service *Service) readSources(ctx context.Context, project storage.Project) sources.Reading {
	if !service.hasSources() {
		return sources.Reading{}
	}
	registered := service.extensions.sources
	principal, principalErr := service.principal(ctx)
	asked := extension.Project{Name: project.Name, Remote: worktree.OriginURL(ctx, project.Repository)}
	states := make([]sources.State, len(registered))
	var wait sync.WaitGroup
	for index, source := range registered {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if principalErr != nil {
				states[index] = service.cachedSnapshot(source.name, project.Repository, "no one could be named to ask it for: "+principalErr.Error())
				return
			}
			snapshot, reason := askSource(ctx, source, principal, asked)
			if reason != "" {
				states[index] = service.cachedSnapshot(source.name, project.Repository, reason)
				return
			}
			states[index] = sources.State{Source: source.name, Held: true, Snapshot: snapshot, FetchedAt: time.Now().UTC()}
			service.keepSnapshot(project.Repository, states[index])
		}()
	}
	wait.Wait()
	return sources.Read(states)
}

// checkSources refuses plans in the project when what its sources share
// says to, naming each definition.
func (service *Service) checkSources(ctx context.Context, project storage.Project) (sources.Reading, error) {
	reading := service.readSources(ctx, project)
	return reading, reading.Refusal()
}

// askSource asks one source for a snapshot. The source runs apart, so one
// that ignores its context still cannot hold a plan past the deadline, and
// a panic or an error is a source that could not be reached, with the
// reason to show. The snapshot is copied before it is used, so a source
// that reuses its maps cannot change it.
func askSource(ctx context.Context, registered registeredSource, principal extension.Principal, project extension.Project) (extension.Snapshot, string) {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	type answer struct {
		snapshot extension.Snapshot
		reason   string
	}
	answered := make(chan answer, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("inkuna: definition source %q panicked: %v", registered.name, recovered)
				answered <- answer{reason: "it failed"}
			}
		}()
		principal.Groups = slices.Clone(principal.Groups)
		snapshot, err := registered.source.Snapshot(ctx, principal, project)
		if err != nil {
			// Read here, apart, as the source's own code. An empty reason is
			// what an answer has, so an error without one still fails.
			reason := err.Error()
			if strings.TrimSpace(reason) == "" {
				reason = "it failed without saying why"
			}
			answered <- answer{reason: reason}
			return
		}
		answered <- answer{snapshot: copySnapshot(snapshot)}
	}()
	select {
	case current := <-answered:
		return current.snapshot, current.reason
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return extension.Snapshot{}, fmt.Sprintf("it did not answer within %s", sourceTimeout)
		}
		return extension.Snapshot{}, ctx.Err().Error()
	}
}

func copySnapshot(snapshot extension.Snapshot) extension.Snapshot {
	copied := extension.Snapshot{Version: snapshot.Version, Definitions: make([]extension.Definition, len(snapshot.Definitions))}
	for index, definition := range snapshot.Definitions {
		files := make(map[string][]byte, len(definition.Files))
		for name, data := range definition.Files {
			files[name] = slices.Clone(data)
		}
		definition.Files = files
		copied.Definitions[index] = definition
	}
	return copied
}

// keepSnapshot saves a snapshot a source just gave as the one to use when
// it cannot be reached. A save that fails costs only that, so it is logged
// rather than refusing the plan the snapshot was asked for.
func (service *Service) keepSnapshot(repository string, state sources.State) {
	if service.dataDir == "" {
		return
	}
	entry := sources.Entry{Source: state.Source, Repository: repository, FetchedAt: state.FetchedAt, Snapshot: state.Snapshot}
	if err := sources.Save(service.dataDir, entry); err != nil {
		log.Printf("inkuna: %v", err)
	}
}

// cachedSnapshot is a source that could not be reached, with the last
// snapshot it gave for the repository when there is one.
func (service *Service) cachedSnapshot(source, repository, reason string) sources.State {
	state := sources.State{Source: source, Unreachable: reason}
	if service.dataDir == "" {
		return state
	}
	entry, found, err := sources.Load(service.dataDir, source, repository)
	if err != nil {
		state.Unreachable += "; its last snapshot could not be read: " + err.Error()
		return state
	}
	if found {
		state.Held, state.Cached, state.Snapshot, state.FetchedAt = true, true, entry.Snapshot, entry.FetchedAt
	}
	return state
}

// refreshSources asks every source for every project's snapshot, so each
// is kept for when the source cannot be reached. The app does it once it
// has started; what the sources say is read again before each plan.
func (service *Service) refreshSources() {
	if !service.hasSources() || service.store == nil {
		return
	}
	// Shutdown cancels it, so a source that is slow to answer does not hold
	// up quitting.
	ctx, cancel := context.WithCancel(service.context())
	service.sourcesCancel = cancel
	service.sourcesWG.Add(1)
	go func() {
		defer service.sourcesWG.Done()
		defer cancel()
		projects, err := service.store.AllProjects(ctx)
		if err != nil {
			log.Printf("inkuna: refresh definition sources: %v", err)
			return
		}
		for _, project := range projects {
			if project.Removed() || ctx.Err() != nil {
				continue
			}
			service.readSources(ctx, project)
		}
	}()
}
