package tracker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// purpose is what a write is for, which decides whether it is allowed: a
// kind an agent asked for, or one of Inkuna's own updates.
type purpose struct {
	kind   domain.TrackerKind
	update domain.TrackerUpdate
}

func forKind(kind domain.TrackerKind) purpose       { return purpose{kind: kind} }
func forUpdate(update domain.TrackerUpdate) purpose { return purpose{update: update} }

func (purpose purpose) String() string {
	if purpose.update != "" {
		return "updates." + string(purpose.update)
	}
	return string(purpose.kind)
}

// Recorder receives every write a Guard is asked for, whether it was
// applied, refused, or failed, so the run that asked can show it.
type Recorder func(domain.TrackerWrite)

// Policy is asked about a write once the settings allow it, before it is
// made, and refuses it by returning an error, whose message is the reason
// the write is recorded with. It sees the write as it will be recorded,
// without its outcome.
type Policy func(context.Context, domain.TrackerWrite) error

// Guard is the only way Inkuna writes to a tracker. Each of its writes
// serves one purpose, a kind an agent asked for or one of Inkuna's own
// updates; the Guard makes it only when the repository's settings allow that
// purpose, and records every write, the ones it refuses included. Inkuna's
// own updates act only on the run's story, and Start and Finish only move it
// to the statuses tracker.yaml names for them, or, where the tracker has none
// of that name, to its first status of that stage.
type Guard struct {
	tracker  domain.Tracker
	settings Settings
	story    string
	record   Recorder
	policy   Policy
	now      func() time.Time
	// reads are the issues Check has read, so a handoff that names one
	// issue many times reads it once.
	readMu sync.Mutex
	reads  map[string]readResult
}

// NewGuard guards tracker with settings for a run whose story is story,
// empty when the run has none, which refuses Inkuna's own updates. tracker
// may be nil when the repository has no tracker Inkuna can write to; every
// write is then refused, saying why.
func NewGuard(tracker domain.Tracker, settings Settings, story string, record Recorder) (*Guard, error) {
	if record == nil {
		return nil, errors.New("a tracker guard needs a recorder, so every write is on the run")
	}
	return &Guard{tracker: tracker, settings: settings, story: strings.TrimSpace(story), record: record, now: time.Now}, nil
}

// SetPolicy has the Guard ask policy about every write the settings allow.
// Without one, what the settings allow is made. It is set before the Guard
// is used.
func (guard *Guard) SetPolicy(policy Policy) { guard.policy = policy }

// Settings is what the Guard allows.
func (guard *Guard) Settings() Settings { return guard.settings }

// Story reads an issue. Reading is never limited.
func (guard *Guard) Story(ctx context.Context, id string) (domain.TrackerIssue, error) {
	if guard.tracker == nil {
		return domain.TrackerIssue{}, domain.TrackerRefusal{Reason: guard.unavailable()}
	}
	issue, err := guard.tracker.Story(ctx, id)
	if err != nil {
		return domain.TrackerIssue{}, fmt.Errorf("read %s: %w", id, err)
	}
	return issue, nil
}

// SetStatus moves an issue to a status, as an agent asked.
func (guard *Guard) SetStatus(ctx context.Context, id, status string) error {
	return guard.setStatus(ctx, forKind(domain.TrackerStatus), id, status)
}

// Comment comments on an issue, as an agent asked, and returns the
// comment's URL.
func (guard *Guard) Comment(ctx context.Context, id, body, marker string) (string, error) {
	return guard.comment(ctx, forKind(domain.TrackerComment), id, body, marker)
}

// Edit changes an issue's fields, as an agent asked.
func (guard *Guard) Edit(ctx context.Context, id string, fields domain.IssueFields) error {
	check := func() error {
		if fields.Empty() {
			return domain.TrackerRefusal{Reason: "the edit sets no field"}
		}
		return nil
	}
	_, err := guard.write(ctx, "edit", forKind(domain.TrackerEdit), id, DescribeFields(fields), check, func(tracker domain.Tracker) (applied, error) {
		return applied{}, tracker.Edit(ctx, id, fields)
	})
	return err
}

// Create makes an issue, or a sub-issue of draft.Parent, as an agent asked.
func (guard *Guard) Create(ctx context.Context, draft domain.IssueDraft) (domain.TrackerIssue, error) {
	change := DescribeChange(Checked{Change: domain.TrackerChange{Kind: domain.TrackerCreate, Title: &draft.Title}, Issue: draft.Parent})
	var created domain.TrackerIssue
	check := func() error {
		if strings.TrimSpace(draft.Title) == "" {
			return domain.TrackerRefusal{Reason: "a new issue needs a title"}
		}
		return nil
	}
	_, err := guard.write(ctx, "create", forKind(domain.TrackerCreate), draft.Parent, change, check, func(tracker domain.Tracker) (applied, error) {
		var err error
		created, err = tracker.Create(ctx, draft)
		return applied{url: created.URL}, err
	})
	return created, err
}

// Start moves the run's story to In Progress, for updates.start: to the
// status tracker.yaml names, or the tracker's first started status when it
// has none of that name. A story already there, or done, is left alone.
func (guard *Guard) Start(ctx context.Context) error {
	return guard.advance(ctx, forUpdate(domain.UpdateStart), domain.StageStarted, guard.settings.InProgressStatus())
}

// Report comments the run's result on its story, for updates.result.
func (guard *Guard) Report(ctx context.Context, body, marker string) (string, error) {
	return guard.comment(ctx, forUpdate(domain.UpdateResult), guard.story, body, marker)
}

// Finish moves the run's story to Done, for updates.done, closing a GitHub
// issue as completed. A story already done is left alone.
func (guard *Guard) Finish(ctx context.Context) error {
	return guard.advance(ctx, forUpdate(domain.UpdateDone), domain.StageDone, guard.settings.DoneStatus())
}

// Attach links a URL, the run's pull request, to its story, for
// updates.done, unless it is linked already.
func (guard *Guard) Attach(ctx context.Context, url, title string) error {
	_, err := guard.write(ctx, "attach", forUpdate(domain.UpdateDone), guard.story, "attach "+url, nil, func(tracker domain.Tracker) (applied, error) {
		attached, err := tracker.Attach(ctx, guard.story, url, title)
		if err == nil && !attached {
			return applied{unchanged: "it is attached already"}, nil
		}
		return applied{}, err
	})
	return err
}

func (guard *Guard) setStatus(ctx context.Context, purpose purpose, id, status string) error {
	_, err := guard.write(ctx, "status", purpose, id, "status → "+status, nil, func(tracker domain.Tracker) (applied, error) {
		return applied{}, tracker.SetStatus(ctx, id, status)
	})
	return err
}

// advance moves the run's story to a stage, recording the status the
// tracker chose and anything else it did, or why it changed nothing.
func (guard *Guard) advance(ctx context.Context, purpose purpose, stage domain.StoryStage, status string) error {
	_, err := guard.write(ctx, "status", purpose, guard.story, "status → "+status, nil, func(tracker domain.Tracker) (applied, error) {
		moved, err := tracker.Advance(ctx, guard.story, stage, status)
		if err != nil {
			return applied{}, err
		}
		if !moved.Changed {
			return applied{unchanged: firstNonEmpty(moved.Note, "it is "+firstNonEmpty(moved.Status, status)+" already")}, nil
		}
		result := applied{change: "status → " + firstNonEmpty(moved.Status, status)}
		if moved.Note != "" {
			result.change += " (" + moved.Note + ")"
		}
		return result, nil
	})
	return err
}

func (guard *Guard) comment(ctx context.Context, purpose purpose, id, body, marker string) (string, error) {
	return guard.write(ctx, "comment", purpose, id, "comment: "+excerpt(body), nil, func(tracker domain.Tracker) (applied, error) {
		url, err := tracker.Comment(ctx, id, body, marker)
		return applied{url: url}, err
	})
}

// applied is what a write did.
type applied struct {
	url string
	// change, when set, is what the write changed in place of what it was
	// asked to, such as the status a tracker chose for a stage.
	change string
	// unchanged, when set, says why nothing was written: the issue already
	// was as asked.
	unchanged string
}

// write checks a write against the settings, then check, the write's own
// checks when it has any, and then the policy, makes it when all allow it,
// and records it either way. A policy is asked only about a write Go would
// make, so its refusal never hides Go's own reason.
func (guard *Guard) write(ctx context.Context, action string, purpose purpose, issue, change string, check func() error, apply func(domain.Tracker) (applied, error)) (string, error) {
	record := domain.TrackerWrite{
		Tracker: string(guard.settings.Tracker), Action: action, Kind: purpose.kind, Update: purpose.update,
		Issue: issue, Change: change,
	}
	var result applied
	err := guard.allow(purpose)
	if err == nil && check != nil {
		err = check()
	}
	if err == nil && guard.policy != nil {
		if refused := guard.policy(ctx, record); refused != nil {
			err = domain.TrackerRefusal{Reason: refused.Error()}
		}
	}
	if err == nil {
		result, err = apply(guard.tracker)
	}
	var refusal domain.TrackerRefusal
	switch {
	case err == nil && result.unchanged != "":
		record.Outcome, record.Reason = domain.TrackerUnchanged, result.unchanged
	case err == nil:
		record.Outcome, record.URL = domain.TrackerApplied, result.url
		if result.change != "" {
			record.Change = result.change
		}
	case errors.As(err, &refusal):
		record.Outcome, record.Reason = domain.TrackerRefused, refusal.Reason
	default:
		record.Outcome, record.Reason = domain.TrackerFailed, err.Error()
	}
	record.At = guard.now().UTC()
	guard.record(record)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", action, firstNonEmpty(issue, "the tracker"), err)
	}
	return result.url, nil
}

// allow refuses a purpose the settings do not allow, naming which file
// switched it off.
func (guard *Guard) allow(purpose purpose) error {
	settings := guard.settings
	if guard.tracker == nil || !settings.Exists {
		return domain.TrackerRefusal{Reason: guard.unavailable()}
	}
	if purpose.kind != "" {
		switch {
		case settings.Effective.Allows(purpose.kind):
			return nil
		case !settings.Ceiling.Allows(purpose.kind):
			return domain.TrackerRefusal{Reason: fmt.Sprintf("%s does not list %s under writes", RelativePath, purpose.kind)}
		}
		return domain.TrackerRefusal{Reason: fmt.Sprintf("inkuna.yaml turns %s off for this repository on this machine", purpose.kind)}
	}
	switch {
	case !settings.Ceiling.AllowsUpdate(purpose.update):
		return domain.TrackerRefusal{Reason: fmt.Sprintf("%s does not turn %s on", RelativePath, purpose)}
	case !settings.Effective.AllowsUpdate(purpose.update):
		return domain.TrackerRefusal{Reason: fmt.Sprintf("inkuna.yaml turns %s off for this repository on this machine", purpose)}
	case guard.story == "":
		return domain.TrackerRefusal{Reason: "the run has no story in the repository's tracker, so Inkuna's own updates have nothing to update"}
	}
	return nil
}

func (guard *Guard) unavailable() string {
	return firstNonEmpty(guard.settings.Note, "the repository has no tracker Inkuna can write to")
}

// DescribeFields says what an edit sets, for the run's record of it.
func DescribeFields(fields domain.IssueFields) string {
	parts := make([]string, 0, 9)
	set := func(name string, value *string) {
		if value != nil {
			parts = append(parts, name+" → "+excerpt(*value))
		}
	}
	set("title", fields.Title)
	set("description", fields.Description)
	if len(fields.AddLabels) > 0 || len(fields.RemoveLabels) > 0 {
		labels := make([]string, 0, len(fields.AddLabels)+len(fields.RemoveLabels))
		for _, label := range fields.AddLabels {
			labels = append(labels, "+"+label)
		}
		for _, label := range fields.RemoveLabels {
			labels = append(labels, "-"+label)
		}
		parts = append(parts, "labels "+strings.Join(labels, ", "))
	}
	if fields.Estimate != nil {
		parts = append(parts, "estimate → "+strconv.Itoa(*fields.Estimate))
	}
	if fields.Priority != nil {
		parts = append(parts, "priority → "+strconv.Itoa(*fields.Priority))
	}
	set("assignee", fields.Assignee)
	set("cycle", fields.Cycle)
	set("milestone", fields.Milestone)
	if len(parts) == 0 {
		return "edit: nothing"
	}
	return "edit: " + strings.Join(parts, "; ")
}

// excerpt keeps a record short: the first line, at most 120 characters.
func excerpt(text string) string {
	text = strings.TrimSpace(text)
	line, rest, _ := strings.Cut(text, "\n")
	runes := []rune(line)
	if len(runes) > 120 {
		return string(runes[:119]) + "…"
	}
	if rest != "" {
		return line + " …"
	}
	return line
}
