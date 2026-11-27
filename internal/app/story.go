package app

import (
	"context"
	"encoding/json"
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
	"github.com/inkuna-ai/inkuna/internal/notify"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/tracker"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// Inkuna's own updates to a run's story (docs/mcp-and-trackers.md): In
// Progress when the run's first step starts, a comment of its result each
// time it ends, and, when a person asks, Done once its pull request merged.
// None of them ever fails or holds up a run.

// storyUpdateTimeout bounds one update, all the tracker calls it makes
// included, so a tracker that does not answer never holds a run for long.
const storyUpdateTimeout = time.Minute

// RunStoryView is the story a run is for in its repository's tracker.
type RunStoryView struct {
	Tracker string `json:"tracker"`
	ID      string `json:"id"`
	// MarkDone is set when the run offers Mark done: it has ended, it has a
	// branch to look for a pull request from, and updates.done is on for it.
	MarkDone bool `json:"markDone,omitempty"`
}

// StoryDoneView is what Mark done found and did.
type StoryDoneView struct {
	// PullRequest is the branch's merged pull request, or, when none
	// merged, the one it is under review in; absent when it has none.
	PullRequest *inkunagithub.PullRequest `json:"pullRequest,omitempty"`
	Merged      bool                      `json:"merged"`
	// Summary says what Mark done found, and Notes what each update did,
	// as the run's timeline says it.
	Summary string   `json:"summary"`
	Notes   []string `json:"notes"`
}

// runStory is a run's story and the tracker settings the run was approved
// under, as the run keeps them (storage.ExecutionStory). A run with no story
// whose steps may ask for tracker changes keeps the settings with no ID.
type runStory struct {
	ID       string           `json:"id"`
	Settings tracker.Settings `json:"settings"`
	// Started is set once the run has moved, or tried to move, the story to
	// In Progress, which it does once, when its first step starts.
	Started bool `json:"started,omitempty"`
}

// storyMarker names a run's result comment, so each time the run ends it
// edits the one comment instead of adding another.
func storyMarker(runID string) string { return "<!-- inkuna-run:" + runID + " -->" }

// approvedStory finds the story a task is for in the tracker of the
// project's checkout, with the tracker settings as they are when the run is
// approved, before any step can change them. A task without a story has
// none, unless a step of its plan may ask for tracker changes, which the
// settings then bound. The caller holds configMu.
func (service *Service) approvedStory(root string, task domain.TaskSpec, spec domain.WorkflowSpec) *runStory {
	settings, err := tracker.Resolve(root, service.config.File.TrackerOverride(root))
	if err != nil {
		// Kept as resolved: it allows nothing, and a fix to tracker.yaml
		// later cannot raise what this run may write.
		log.Printf("inkuna: tracker settings of %s: %v", root, err)
	}
	id := branching.StoryFor(settings.Tracker, task)
	asks := slices.ContainsFunc(spec.Steps, func(step domain.WorkflowStep) bool { return len(step.TrackerKinds) > 0 })
	if id == "" && !asks {
		return nil
	}
	return &runStory{ID: id, Settings: settings}
}

// saveRunStory keeps a run's story with the run.
func (service *Service) saveRunStory(ctx context.Context, runID string, story runStory) error {
	data, err := json.Marshal(story)
	if err != nil {
		return fmt.Errorf("encode the run's story: %w", err)
	}
	return service.store.SaveExecutionStory(ctx, runID, data)
}

// loadRunStory reads a run's story, or nil when it has none.
func (service *Service) loadRunStory(ctx context.Context, runID string) (*runStory, error) {
	data, err := service.store.ExecutionStory(ctx, runID)
	if err != nil || data == nil {
		return nil, err
	}
	var story runStory
	if err := json.Unmarshal(data, &story); err != nil {
		return nil, fmt.Errorf("decode the story of run %s: %w", runID, err)
	}
	return &story, nil
}

// storySettings is what a run's story updates may do now: what the run was
// approved under, narrowed by the project's tracker.yaml and this machine's
// inkuna.yaml as they are now, so either can switch an update off but
// neither can switch one on.
func (service *Service) storySettings(root string, story runStory) tracker.Settings {
	current, err := tracker.Resolve(root, service.trackerOverride(root))
	if err != nil {
		log.Printf("inkuna: tracker settings of %s: %v", root, err)
	}
	return story.Settings.Within(current)
}

func (service *Service) trackerOverride(root string) *config.TrackerOverride {
	service.configMu.RLock()
	defer service.configMu.RUnlock()
	return service.config.File.TrackerOverride(root)
}

// storyRecorder records a story update on the run's timeline under
// eventRunID, one of its step runs. With notify, an update that was not
// made is also a desktop notification, since nobody asked for it and the
// timeline may not be open.
func (service *Service) storyRecorder(runID, eventRunID string, notifyFailure bool, also func(domain.TrackerWrite)) tracker.Recorder {
	return func(write domain.TrackerWrite) {
		service.recordTrackerWrite(runID, eventRunID, write)
		if also != nil {
			also(write)
		}
		if notifyFailure && (write.Outcome == domain.TrackerRefused || write.Outcome == domain.TrackerFailed) {
			_, summary := describeTrackerWrite(write)
			_ = service.dependenciesFor(runID).notifier.Notify(notify.Notification{
				Title: "Inkuna could not update " + firstNonEmpty(write.Issue, "the story"), Body: summary, Kind: "tracker-failed", Reference: runID,
			})
		}
	}
}

// startStory moves the run's story to In Progress the first time one of
// its steps starts, with updates.start on, and records it under that step's
// run. It is tried once per run, even if it fails, and the step goes on
// whatever the tracker answers.
func (service *Service) startStory(ctx context.Context, run *liveExecution, stepRunID string) {
	service.runsMu.Lock()
	if run.story == nil || run.story.ID == "" || run.story.Started {
		service.runsMu.Unlock()
		return
	}
	run.story.Started = true
	story, runID, root := *run.story, run.view.ID, run.project.Repository
	service.runsMu.Unlock()
	// Kept before the write, so a restart never makes it twice.
	if err := service.saveRunStory(context.WithoutCancel(ctx), runID, story); err != nil {
		log.Printf("inkuna: keep the story of run %s: %v", runID, err)
	}
	settings := service.storySettings(root, story)
	if !settings.Effective.AllowsUpdate(domain.UpdateStart) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, storyUpdateTimeout)
	defer cancel()
	guard, err := service.guardTracker(ctx, root, settings, story.ID, service.storyRecorder(runID, stepRunID, true, nil))
	if err != nil {
		log.Printf("inkuna: open the tracker of run %s: %v", runID, err)
		return
	}
	// The guard records the outcome, refused or failed included.
	_ = guard.Start(ctx)
}

// reportStory comments the run's result on its story once the run has
// ended, with updates.result on. It runs apart from the run, so a slow
// tracker never holds up the run's end or the next round, and one report at
// a time, so two rounds' results never race to make the run's one comment
// twice.
func (service *Service) reportStory(runID string) {
	if service.store == nil {
		return
	}
	service.runsWG.Add(1)
	go func() {
		defer service.runsWG.Done()
		service.reportMu.Lock()
		defer service.reportMu.Unlock()
		ctx, cancel := context.WithTimeout(service.context(), storyUpdateTimeout)
		defer cancel()
		if err := service.writeResult(ctx, runID); err != nil {
			log.Printf("inkuna: report run %s on its story: %v", runID, err)
		}
	}()
}

// writeResult comments the run's result on its story, under the last step
// run of the run's round. A run none of whose steps started has nothing to
// report.
func (service *Service) writeResult(ctx context.Context, runID string) error {
	story, err := service.loadRunStory(ctx, runID)
	if err != nil || story == nil || story.ID == "" {
		return err
	}
	execution, err := service.store.Execution(ctx, runID)
	if err != nil {
		return err
	}
	project, err := service.store.Project(ctx, execution.ProjectID)
	if err != nil {
		return err
	}
	settings := service.storySettings(project.Repository, *story)
	if !settings.Effective.AllowsUpdate(domain.UpdateResult) {
		return nil
	}
	eventRunID, err := service.lastStepRun(ctx, execution)
	if err != nil || eventRunID == "" {
		return err
	}
	origin := worktree.OriginRepository(ctx, project.Repository)
	body := service.resultBody(ctx, execution, origin)
	guard, err := service.guardTracker(ctx, project.Repository, settings, story.ID, service.storyRecorder(runID, eventRunID, true, nil))
	if err != nil {
		return err
	}
	_, _ = guard.Report(ctx, body, storyMarker(runID))
	return nil
}

// lastStepRun is the step run that started last in the run's current
// round, or in any round when none did in this one; story updates are
// recorded on the timeline under it. Empty when no step of the run started.
func (service *Service) lastStepRun(ctx context.Context, execution storage.Execution) (string, error) {
	runs, err := service.store.StepRuns(ctx, execution.ID)
	if err != nil {
		return "", fmt.Errorf("load the run's steps: %w", err)
	}
	last := ""
	for _, run := range runs {
		if run.Round == execution.Round {
			last = run.ID
		}
	}
	if last == "" && len(runs) > 0 {
		last = runs[len(runs)-1].ID
	}
	return last, nil
}

// resultBody is a run's result as its story's comment says it: how the run
// ended and in how many rounds, its checks, what it committed, and the pull
// request for its branch when there is one.
func (service *Service) resultBody(ctx context.Context, execution storage.Execution, origin string) string {
	rounds := "1 round"
	if execution.Round > 1 {
		rounds = fmt.Sprintf("%d rounds", execution.Round)
	}
	title := strings.TrimSpace(execution.Title)
	var headline string
	switch execution.State {
	case "completed":
		headline = fmt.Sprintf("Inkuna's run %q completed in %s.", title, rounds)
	case "cancelled":
		headline = fmt.Sprintf("Inkuna's run %q was cancelled in round %d.", title, max(execution.Round, 1))
	default:
		headline = fmt.Sprintf("Inkuna's run %q %s in round %d", title, execution.State, max(execution.Round, 1))
		if reason := strings.TrimSpace(execution.Error); reason != "" {
			headline += ": " + truncateSummary(firstLine(reason), 300)
		}
		headline += "."
	}
	lines := []string{headline, ""}
	if byRun, err := service.store.ChecksFor(ctx, []string{execution.ID}); err == nil {
		if totals := checkTotals(byRun[execution.ID]); totals != nil && totals.Total > 0 {
			lines = append(lines, fmt.Sprintf("- Checks: %d of %d passed.", totals.Passed, totals.Total))
		}
	}
	if commits, err := service.store.Commits(ctx, execution.ID); err == nil {
		switch {
		case len(commits) == 0:
			lines = append(lines, "- Changes: none committed.")
		default:
			noun := "commits"
			if len(commits) == 1 {
				noun = "commit"
			}
			on := ""
			if execution.Branch != "" {
				on = " on `" + execution.Branch + "`"
			}
			lines = append(lines, fmt.Sprintf("- Changes: %d %s%s:", len(commits), noun, on))
			const shown = 5
			for index, commit := range commits {
				if index == shown {
					lines = append(lines, fmt.Sprintf("  - and %d more", len(commits)-shown))
					break
				}
				lines = append(lines, "  - "+truncateSummary(commit.Subject, 120))
			}
		}
	}
	if origin != "" && execution.Branch != "" {
		pulls, err := inkunagithub.PullRequestsFrom(ctx, service.github, origin, execution.Branch)
		if err != nil {
			log.Printf("inkuna: find the pull request of run %s: %v", execution.ID, err)
		} else if pull, ok := inkunagithub.CurrentPull(pulls); ok {
			lines = append(lines, fmt.Sprintf("- Pull request: [#%d %s](%s), %s.", pull.Number, pull.Title, pull.URL, strings.ToLower(pull.State)))
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// MarkStoryDone checks whether an ended run's pull request merged, and when
// it did, attaches it to the run's Linear story and moves the story to
// Done, closing a GitHub issue as completed, as far as the tracker's own
// GitHub integration has not done these already. It looks the pull request
// up itself rather than trusting the Tasks view's last check
// (CheckPullRequests), and only a person's Mark done moves a story. A pull
// request that has not merged changes nothing.
func (service *Service) MarkStoryDone(runID string) (StoryDoneView, error) {
	if err := service.ready(); err != nil {
		return StoryDoneView{}, err
	}
	ctx, cancel := context.WithTimeout(service.context(), storyUpdateTimeout)
	defer cancel()
	execution, err := service.store.Execution(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return StoryDoneView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return StoryDoneView{}, err
	}
	// A live run's state is ahead of what is stored.
	state := execution.State
	service.runsMu.RLock()
	if live := service.runs[runID]; live != nil {
		state = live.view.State
	}
	service.runsMu.RUnlock()
	if !terminalRunState(state) || !terminalRunState(execution.State) {
		return StoryDoneView{}, fmt.Errorf("this run is %s; Mark done is offered once it has ended", state)
	}
	story, err := service.loadRunStory(ctx, runID)
	if err != nil {
		return StoryDoneView{}, err
	}
	if story == nil || story.ID == "" {
		return StoryDoneView{}, errors.New("this run has no story in its repository's tracker")
	}
	project, err := service.workableProject(ctx, execution.ProjectID)
	if err != nil {
		return StoryDoneView{}, err
	}
	settings := service.storySettings(project.Repository, *story)
	if !settings.Effective.AllowsUpdate(domain.UpdateDone) {
		return StoryDoneView{}, errors.New(doneUnavailable(settings))
	}
	if execution.Branch == "" {
		return StoryDoneView{}, errors.New("the run has no branch, so it has no pull request to look for")
	}
	origin := worktree.OriginRepository(ctx, project.Repository)
	if origin == "" {
		return StoryDoneView{}, errors.New("the repository has no origin to look for the run's pull request on")
	}
	pulls, err := inkunagithub.PullRequestsFrom(ctx, service.github, origin, execution.Branch)
	if err != nil {
		return StoryDoneView{}, fmt.Errorf("look for the pull request from %s: %w", execution.Branch, err)
	}
	view := StoryDoneView{Notes: []string{}}
	merged, ok := inkunagithub.MergedPull(pulls)
	if !ok {
		if current, found := inkunagithub.CurrentPull(pulls); found {
			view.PullRequest = &current
			view.Summary = fmt.Sprintf("Pull request #%d is %s, not merged, so %s is left as it is.", current.Number, strings.ToLower(current.State), story.ID)
		} else {
			view.Summary = fmt.Sprintf("No pull request has been opened from %s, so %s is left as it is.", execution.Branch, story.ID)
		}
		return view, nil
	}
	view.PullRequest, view.Merged = &merged, true
	eventRunID, err := service.lastStepRun(ctx, execution)
	if err != nil {
		return StoryDoneView{}, err
	}
	notes := func(write domain.TrackerWrite) {
		_, summary := describeTrackerWrite(write)
		view.Notes = append(view.Notes, summary)
	}
	// Mark done answers the person who asked, so a failure is said here
	// and on the timeline, not in a notification.
	guard, err := service.guardTracker(ctx, project.Repository, settings, story.ID, service.storyRecorder(runID, eventRunID, false, notes))
	if err != nil {
		return StoryDoneView{}, err
	}
	// A GitHub issue has no attachments; the pull request links to it.
	if settings.Tracker == branching.TrackerLinear {
		_ = guard.Attach(ctx, merged.URL, firstNonEmpty(merged.Title, fmt.Sprintf("Pull request #%d", merged.Number)))
	}
	_ = guard.Finish(ctx)
	view.Summary = fmt.Sprintf("Pull request #%d merged.", merged.Number)
	service.emit("inkuna:runs", runID)
	return view, nil
}

// doneUnavailable says why a run does not offer Mark done.
func doneUnavailable(settings tracker.Settings) string {
	switch {
	case !settings.Exists || !tracker.Writable(settings.Tracker):
		return firstNonEmpty(settings.Note, "the repository has no tracker Inkuna can write to")
	case !settings.Ceiling.AllowsUpdate(domain.UpdateDone):
		return tracker.RelativePath + " does not turn updates.done on, now or when the run was approved"
	}
	return "inkuna.yaml turns updates.done off for this repository on this machine"
}

// attachStory puts a run's story on its view, and whether the run offers
// Mark done, with the tracker changes its agents asked for. A run whose
// story cannot be read is shown without one.
func (service *Service) attachStory(view *WorkflowRunView) {
	if service.store == nil {
		return
	}
	ctx := service.context()
	view.TrackerChanges = service.runTrackerChanges(ctx, view.ID, view.ProjectID)
	story, err := service.loadRunStory(ctx, view.ID)
	if err != nil || story == nil || story.ID == "" {
		return
	}
	view.Story = &RunStoryView{Tracker: string(story.Settings.Tracker), ID: story.ID}
	if !terminalRunState(view.State) || view.Branch == "" || !story.Settings.Effective.AllowsUpdate(domain.UpdateDone) {
		return
	}
	if project, err := service.store.Project(ctx, view.ProjectID); err == nil {
		view.Story.MarkDone = service.storySettings(project.Repository, *story).Effective.AllowsUpdate(domain.UpdateDone)
	}
}

// requireCurrentCycle refuses a task whose story is a Linear issue outside
// its team's active cycle, when the repository's tracker.yaml asks for
// that, before anything is spent planning it. Linear that cannot be asked
// refuses too, saying why: the repository asked for the check. It returns
// the story it checked, empty when there was nothing to check, and does not
// ask again about checked, a story it already passed.
func (service *Service) requireCurrentCycle(ctx context.Context, root string, task domain.TaskSpec, checked string) (string, error) {
	settings, err := tracker.Resolve(root, service.trackerOverride(root))
	if err != nil || settings.Tracker != branching.TrackerLinear || !settings.Config.Linear.RequireCurrentCycle {
		return "", nil
	}
	story := branching.StoryFor(settings.Tracker, task)
	if story == "" || story == checked {
		return story, nil
	}
	timeout := service.config.LinearTimeout
	if timeout <= 0 {
		timeout = storyUpdateTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Reading is never limited, so the guard records nothing.
	guard, err := service.guardTracker(ctx, root, settings, story, func(domain.TrackerWrite) {})
	if err != nil {
		return story, err
	}
	issue, err := guard.Story(ctx, story)
	if err != nil {
		return story, fmt.Errorf("%s requires the story to be in its team's current cycle, and Linear could not say whether %s is: %w", tracker.RelativePath, story, err)
	}
	if !issue.CurrentCycle {
		where := "is in no cycle"
		if issue.Cycle != "" {
			where = "is in " + issue.Cycle + ", not its team's current cycle"
		}
		return story, fmt.Errorf("%s %s, and %s requires a story in its team's current cycle; move it into the current cycle and plan again", firstNonEmpty(issue.ID, story), where, tracker.RelativePath)
	}
	return story, nil
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}
