package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	inkunagithub "github.com/inkuna-ai/inkuna/internal/github"
	"github.com/inkuna-ai/inkuna/internal/linear"
	"github.com/inkuna-ai/inkuna/internal/tracker"
	"github.com/inkuna-ai/inkuna/internal/worktree"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// WriteAccessView says whether a tracker's credential can write, and why
// (docs/mcp-and-trackers.md#connections).
type WriteAccessView struct {
	// Status is can-write, partial, read-only, or unknown.
	Status string `json:"status"`
	Detail string `json:"detail"`
}

const (
	writeAccessFull     = "can-write"
	writeAccessPartial  = "partial"
	writeAccessReadOnly = "read-only"
	writeAccessUnknown  = "unknown"
)

// RepositoryTrackerView is what Inkuna may write to one declared
// repository's tracker on this machine: the ceiling its tracker.yaml sets,
// and what is left once its inkuna.yaml entry lowered it.
type RepositoryTrackerView struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Tracker string `json:"tracker,omitempty"`
	// File is the repository's tracker.yaml, and Exists whether it has one.
	File   string `json:"file"`
	Exists bool   `json:"exists"`
	// Project is the GitHub Project, as owner/number, whose field holds a
	// GitHub issue's status.
	Project   string            `json:"project,omitempty"`
	Ceiling   tracker.Allowance `json:"ceiling"`
	Effective tracker.Allowance `json:"effective"`
	// Note says why nothing may be written, and Error why the settings
	// could not be read.
	Note  string `json:"note,omitempty"`
	Error string `json:"error,omitempty"`
}

// repositoryTrackers reads every declared repository's tracker settings.
func repositoryTrackers(file config.File) []RepositoryTrackerView {
	views := make([]RepositoryTrackerView, 0, len(file.Repositories))
	for _, repository := range file.Repositories {
		settings, err := tracker.Resolve(repository.Path, file.TrackerOverride(repository.Path))
		view := RepositoryTrackerView{
			Path: repository.Path, Name: repository.Name, Tracker: string(settings.Tracker), File: settings.Path, Exists: settings.Exists,
			Ceiling: settings.Ceiling, Effective: settings.Effective, Note: settings.Note,
		}
		if view.Ceiling.Writes == nil {
			view.Ceiling.Writes = []domain.TrackerKind{}
		}
		if view.Effective.Writes == nil {
			view.Effective.Writes = []domain.TrackerKind{}
		}
		if project := settings.Config.GitHub.Project; project != nil && settings.Tracker == branching.TrackerGitHub {
			view.Project = project.String()
		}
		if err != nil {
			view.Error = err.Error()
		}
		views = append(views, view)
	}
	return views
}

// storyTracker opens a project's tracker for a run whose story is story:
// the adapter for the tracker its branching.yaml names, with the project's
// credential, guarded by what its tracker.yaml and this machine's
// inkuna.yaml allow. Every write asked of it is recorded on the run's
// timeline under eventRunID, a step run of executionID. When the settings
// cannot be read the guard refuses every write, and the error says why.
//
// root is the project's checkout, never a run's worktree, so a step cannot
// raise its own run's ceiling by editing tracker.yaml there. With worktrees
// off a step writes in that checkout, so a run's own story updates use the
// settings it was approved under instead (storySettings).
func (service *Service) storyTracker(ctx context.Context, root, story, executionID, eventRunID string) (*tracker.Guard, error) {
	settings, resolveErr := tracker.Resolve(root, service.trackerOverride(root))
	guard, err := service.guardTracker(ctx, root, settings, story, func(write domain.TrackerWrite) {
		service.recordTrackerWrite(executionID, eventRunID, write)
	})
	if err != nil {
		return nil, err
	}
	if resolveErr != nil {
		return guard, fmt.Errorf("read tracker settings: %w", resolveErr)
	}
	return guard, nil
}

// guardTracker opens the adapter for the tracker settings name, reaching it
// with the project's credential: the Linear key, or gh for the origin of
// the checkout at root. The guard allows what settings allow and hands
// every write to record.
func (service *Service) guardTracker(ctx context.Context, root string, settings tracker.Settings, story string, record tracker.Recorder) (*tracker.Guard, error) {
	origin := ""
	if settings.Tracker == branching.TrackerGitHub {
		origin = worktree.OriginRepository(ctx, root)
	}
	sources := tracker.Sources{
		Linear:     func() (*linear.Client, error) { return service.linearClient(service.linearCredential()) },
		GitHub:     service.github,
		Repository: origin,
		GitHubLogin: func() (string, error) {
			host, _, _ := strings.Cut(origin, "/")
			status, err := inkunagithub.AuthenticationStatus(ctx, service.github, host)
			if err != nil {
				return "", err
			}
			if !status.Authenticated || status.Account == "" {
				return "", fmt.Errorf("gh is not signed in to %s", host)
			}
			return status.Account, nil
		},
	}
	guard, err := tracker.OpenGuard(settings, sources, story, record)
	if err == nil && service.policing() {
		guard.SetPolicy(service.trackerPolicy(root))
	}
	return guard, err
}

// trackerPolicy asks the policies about each write a guard for the checkout
// at root is about to make. A checkout that is no project's leaves the
// decision without one; one Inkuna could not look up refuses, since a
// policy for its project would not recognise the write without it.
func (service *Service) trackerPolicy(root string) tracker.Policy {
	return func(ctx context.Context, write domain.TrackerWrite) error {
		project, err := service.projectAt(ctx, root)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("find the project to ask its policies: %w", err)
		}
		return service.decide(ctx, trackerDecision(project.ID, write), nil)
	}
}

// trackerDecision is a write the tracker settings allow, for tracker.write.
// A kind is a change an agent asked for that a person approved, and an
// update one of Inkuna's own. What the write says is left out, since a
// comment's or a title's text can be anything a worker wrote.
func trackerDecision(projectID string, write domain.TrackerWrite) extension.Decision {
	actor := "user"
	if write.Update != "" {
		actor = "inkuna"
	}
	return extension.Decision{Point: extension.PointTrackerWrite, Project: projectID, Actor: actor, Details: map[string]string{
		"tracker": write.Tracker, "action": write.Action, "kind": string(write.Kind), "update": string(write.Update), "issue": write.Issue,
	}}
}

// recordTrackerWrite puts a tracker write on the run's timeline with what
// it changed, and keeps it as the tracker's last write for Connections. A
// write that was not made is a warning there.
func (service *Service) recordTrackerWrite(executionID, eventRunID string, write domain.TrackerWrite) {
	kind, summary := describeTrackerWrite(write)
	event := syntheticEvent(eventRunID, 0, kind, summary)
	event.Payload = map[string]any{"trackerWrite": write}
	if err := service.appendRunEvent(executionID, event); err != nil {
		// The write itself happened or not either way; the record is what
		// was lost, and the log keeps that.
		log.Printf("inkuna: record tracker write on %s: %v", executionID, err)
	}
	service.trackerMu.Lock()
	if service.lastTrackerWrites == nil {
		service.lastTrackerWrites = make(map[string]domain.TrackerWrite)
	}
	service.lastTrackerWrites[write.Tracker] = write
	service.trackerMu.Unlock()
}

// describeTrackerWrite says what a write did, as the run's timeline says
// it, and whether that is a warning.
func describeTrackerWrite(write domain.TrackerWrite) (domain.EventKind, string) {
	where := trackerName(write.Tracker)
	if write.Issue != "" {
		where += " " + write.Issue
	}
	switch write.Outcome {
	case domain.TrackerUnchanged:
		return domain.EventLifecycle, fmt.Sprintf("Left %s as it was (%s): %s", where, write.Change, write.Reason)
	case domain.TrackerRefused:
		return domain.EventWarning, fmt.Sprintf("Did not write to %s (%s): %s", where, write.Change, write.Reason)
	case domain.TrackerFailed:
		return domain.EventWarning, fmt.Sprintf("Writing to %s failed (%s): %s", where, write.Change, write.Reason)
	}
	return domain.EventLifecycle, fmt.Sprintf("Wrote to %s: %s", where, write.Change)
}

// lastTrackerWrite is the last write this process tried on a tracker.
func (service *Service) lastTrackerWrite(name string) (domain.TrackerWrite, bool) {
	service.trackerMu.Lock()
	defer service.trackerMu.Unlock()
	write, ok := service.lastTrackerWrites[name]
	return write, ok
}

// githubWriteAccess reads gh's scopes: repo writes issues, and project sets
// a GitHub Project's Status field, which the repositories whose
// tracker.yaml names a project need for status writes.
func (service *Service) githubWriteAccess(status inkunagithub.AuthStatus) *WriteAccessView {
	if !status.Authenticated {
		return nil
	}
	needProject := make([]string, 0)
	for _, view := range repositoryTrackers(service.config.File) {
		if view.Project != "" && (view.Effective.Allows(domain.TrackerStatus) || view.Effective.AllowsUpdate(domain.UpdateStart) || view.Effective.AllowsUpdate(domain.UpdateDone)) {
			needProject = append(needProject, view.Name)
		}
	}
	if len(status.Scopes) == 0 {
		return &WriteAccessView{Status: writeAccessUnknown, Detail: "gh's token lists no scopes, as a fine-grained or app token does not, so a write it cannot make says why when it is tried"}
	}
	project := status.HasScope("project")
	switch {
	case !status.HasScope("public_repo"):
		return &WriteAccessView{Status: writeAccessReadOnly, Detail: "gh's token has no repo scope, so Inkuna cannot write to GitHub issues; run gh auth refresh -s repo"}
	case len(needProject) > 0 && !project:
		return &WriteAccessView{Status: writeAccessPartial, Detail: fmt.Sprintf("gh's token can write issues but has no project scope, which %s need to set a GitHub Project's Status; run gh auth refresh -s project", strings.Join(needProject, ", "))}
	case !status.HasScope("repo"):
		return &WriteAccessView{Status: writeAccessPartial, Detail: "gh's token has public_repo, so it can write the issues of public repositories only"}
	case project:
		return &WriteAccessView{Status: writeAccessFull, Detail: "gh's token can write issues and set a GitHub Project's Status"}
	}
	return &WriteAccessView{Status: writeAccessFull, Detail: "gh's token can write issues; it has no project scope, which setting a GitHub Project's Status would need"}
}

// linearWriteAccess asks Linear what the key may write, once per key: the
// answer is kept until the key changes.
func (service *Service) linearWriteAccess(client *linear.Client, generation uint64) *WriteAccessView {
	service.authMu.RLock()
	cached, cachedGen, current := service.linearAccess, service.linearAccessGen, service.linearGen
	service.authMu.RUnlock()
	if current != generation {
		// The key was replaced or forgotten while this one was checked, so
		// what it may write no longer matters.
		return nil
	}
	if cached != nil && cachedGen == generation {
		return cached
	}
	ctx, cancel := context.WithTimeout(service.context(), service.config.LinearTimeout)
	defer cancel()
	access, err := client.WriteAccess(ctx)
	view := describeLinearAccess(access, err)
	if view.Status == writeAccessUnknown {
		if last, ok := service.lastTrackerWrite(string(branching.TrackerLinear)); ok {
			view.Detail += " " + describeLastWrite(last)
		}
	}
	// Only a definite answer is kept; an unclear one is asked again next
	// time, since it may be a passing failure such as a rate limit.
	if err == nil && view.Status != writeAccessUnknown {
		service.authMu.Lock()
		if service.linearGen == generation {
			service.linearAccess, service.linearAccessGen = view, generation
		}
		service.authMu.Unlock()
	}
	return view
}

// describeLinearAccess says what Linear's answers mean for runs.
func describeLinearAccess(access linear.WriteAccess, err error) *WriteAccessView {
	if err != nil {
		return &WriteAccessView{Status: writeAccessUnknown, Detail: "Linear did not say whether the key can write: " + err.Error()}
	}
	sorts := []struct {
		name   string
		access linear.Access
	}{{"change issues", access.Update}, {"comment", access.Comment}, {"create issues", access.Create}}
	var allowed, denied, unknown []string
	for _, sort := range sorts {
		switch sort.access {
		case linear.AccessAllowed:
			allowed = append(allowed, sort.name)
		case linear.AccessDenied:
			denied = append(denied, sort.name)
		default:
			unknown = append(unknown, sort.name)
		}
	}
	reasons := make([]string, 0, len(access.Reasons))
	for _, name := range []string{"update", "comment", "create"} {
		if reason := access.Reasons[name]; reason != "" && !slices.Contains(reasons, reason) {
			reasons = append(reasons, reason)
		}
	}
	because := ""
	if len(reasons) > 0 {
		because = " Linear said: " + strings.Join(reasons, "; ") + "."
	}
	switch {
	case len(unknown) > 0:
		return &WriteAccessView{Status: writeAccessUnknown, Detail: "Linear did not say whether the key can " + andList(unknown) + "." + because}
	case len(denied) == 0:
		return &WriteAccessView{Status: writeAccessFull, Detail: "The key can change issues, comment, and create issues, in the teams it may reach."}
	case len(allowed) == 0:
		return &WriteAccessView{Status: writeAccessReadOnly, Detail: "The key can only read, so importing issues works and every write says why it cannot be made." + because}
	}
	return &WriteAccessView{Status: writeAccessPartial, Detail: "The key can " + andList(allowed) + ", but not " + orList(denied) + "." + because}
}

func describeLastWrite(write domain.TrackerWrite) string {
	text := fmt.Sprintf("The last write, %s at %s, was %s", write.Change, write.At.Local().Format(time.Kitchen), write.Outcome)
	if write.Reason != "" {
		text += ": " + write.Reason
	}
	return text + "."
}

// trackerName names a tracker as the timeline does.
func trackerName(name string) string {
	switch branching.Tracker(name) {
	case branching.TrackerLinear:
		return "Linear"
	case branching.TrackerGitHub:
		return "GitHub issue"
	case branching.TrackerJira:
		return "Jira"
	}
	return name
}

func andList(names []string) string { return joinList(names, "and") }

func orList(names []string) string { return joinList(names, "or") }

func joinList(names []string, conjunction string) string {
	if len(names) <= 2 {
		return strings.Join(names, " "+conjunction+" ")
	}
	return strings.Join(names[:len(names)-1], ", ") + ", " + conjunction + " " + names[len(names)-1]
}
