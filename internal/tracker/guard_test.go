package tracker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// fakeTracker counts the writes that reach it, keeps the issue and status
// of the last, and answers with err. Advance answers with moved when it is
// set, and attached says whether Attach linked the URL.
type fakeTracker struct {
	calls    []string
	issue    string
	status   string
	err      error
	moved    *domain.StageChange
	stage    domain.StoryStage
	attached *bool
}

func (fake *fakeTracker) Story(_ context.Context, id string) (domain.TrackerIssue, error) {
	return domain.TrackerIssue{ID: id}, fake.err
}

func (fake *fakeTracker) Statuses(context.Context, string) ([]string, error) {
	return []string{"Backlog", "In Review"}, fake.err
}

func (fake *fakeTracker) CheckEdit(fields domain.IssueFields) error {
	if fields.Cycle != nil {
		return domain.TrackerRefusal{Reason: "this tracker has no cycle"}
	}
	return nil
}

func (fake *fakeTracker) SetStatus(_ context.Context, id, status string) error {
	fake.calls, fake.issue, fake.status = append(fake.calls, "status"), id, status
	return fake.err
}

func (fake *fakeTracker) Advance(_ context.Context, id string, stage domain.StoryStage, status string) (domain.StageChange, error) {
	fake.calls, fake.issue, fake.status, fake.stage = append(fake.calls, "advance"), id, status, stage
	if fake.moved != nil {
		return *fake.moved, fake.err
	}
	return domain.StageChange{Status: status, Changed: true}, fake.err
}

func (fake *fakeTracker) Comment(_ context.Context, id, _, _ string) (string, error) {
	fake.calls, fake.issue = append(fake.calls, "comment"), id
	return "https://tracker.test/comment/1", fake.err
}

func (fake *fakeTracker) Edit(_ context.Context, id string, _ domain.IssueFields) error {
	fake.calls, fake.issue = append(fake.calls, "edit"), id
	return fake.err
}

func (fake *fakeTracker) Create(_ context.Context, draft domain.IssueDraft) (domain.TrackerIssue, error) {
	fake.calls = append(fake.calls, "create")
	return domain.TrackerIssue{ID: "7", Title: draft.Title, URL: "https://tracker.test/issue/7"}, fake.err
}

func (fake *fakeTracker) Attach(_ context.Context, id, _, _ string) (bool, error) {
	fake.calls, fake.issue = append(fake.calls, "attach"), id
	return fake.attached == nil || *fake.attached, fake.err
}

func settingsAllowing(ceiling Allowance, override *config.TrackerOverride) Settings {
	return Settings{Tracker: branching.TrackerGitHub, Exists: true, Ceiling: ceiling, Effective: Narrow(ceiling, override)}
}

// method is one of a Guard's writes and the purpose it serves.
type method struct {
	name    string
	purpose purpose
	call    func(*Guard) error
}

func methods() []method {
	ctx := context.Background()
	title := "Index the events"
	return []method{
		{"SetStatus", forKind(domain.TrackerStatus), func(guard *Guard) error { return guard.SetStatus(ctx, "12", "In Review") }},
		{"Comment", forKind(domain.TrackerComment), func(guard *Guard) error {
			_, err := guard.Comment(ctx, "12", "Done in 4 s", "")
			return err
		}},
		{"Edit", forKind(domain.TrackerEdit), func(guard *Guard) error { return guard.Edit(ctx, "12", domain.IssueFields{Title: &title}) }},
		{"Create", forKind(domain.TrackerCreate), func(guard *Guard) error {
			_, err := guard.Create(ctx, domain.IssueDraft{Title: title, Parent: "12"})
			return err
		}},
		{"Start", forUpdate(domain.UpdateStart), func(guard *Guard) error { return guard.Start(ctx) }},
		{"Report", forUpdate(domain.UpdateResult), func(guard *Guard) error {
			_, err := guard.Report(ctx, "Completed", "<!-- inkuna-run:r1 -->")
			return err
		}},
		{"Finish", forUpdate(domain.UpdateDone), func(guard *Guard) error { return guard.Finish(ctx) }},
		{"Attach", forUpdate(domain.UpdateDone), func(guard *Guard) error {
			return guard.Attach(ctx, "https://github.com/acme/app/pull/3", "Pull request")
		}},
	}
}

// allowanceOf builds an allowance from bits: one per kind, then one per
// update.
func allowanceOf(bits int) Allowance {
	allowance := Allowance{Writes: []domain.TrackerKind{}}
	for index, kind := range domain.TrackerKinds() {
		if bits&(1<<index) != 0 {
			allowance.Writes = append(allowance.Writes, kind)
		}
	}
	allowance.Updates = Updates{Start: bits&(1<<4) != 0, Result: bits&(1<<5) != 0, Done: bits&(1<<6) != 0}
	return allowance
}

// overrideOf builds an override that keeps what bits sets: one per kind,
// then one per update, with a nil switch where a bit beyond 7 says so.
func overrideOf(bits int) *config.TrackerOverride {
	kept := allowanceOf(bits)
	writes := append([]domain.TrackerKind{}, kept.Writes...)
	flag := func(on bool) *bool { return &on }
	return &config.TrackerOverride{Writes: &writes, Updates: config.TrackerUpdatesOverride{
		Start: flag(kept.Updates.Start), Result: flag(kept.Updates.Result), Done: flag(kept.Updates.Done),
	}}
}

// Across every ceiling a tracker.yaml can set and every override an
// inkuna.yaml can add, a write reaches the tracker exactly when both files
// allow the purpose it serves; every other write is refused without being
// sent, and every write is recorded.
func TestNoWriteGoesBeyondWhatBothFilesAllow(t *testing.T) {
	for ceilingBits := 0; ceilingBits < 1<<7; ceilingBits++ {
		ceiling := allowanceOf(ceilingBits)
		for overrideBits := -1; overrideBits < 1<<7; overrideBits++ {
			override := (*config.TrackerOverride)(nil)
			if overrideBits >= 0 {
				override = overrideOf(overrideBits)
			}
			fake := &fakeTracker{}
			records := 0
			guard, err := NewGuard(fake, settingsAllowing(ceiling, override), "12", func(domain.TrackerWrite) { records++ })
			if err != nil {
				t.Fatal(err)
			}
			for _, method := range methods() {
				allowed := ceiling.Allows(method.purpose.kind) && override.Keeps(method.purpose.kind)
				if method.purpose.update != "" {
					allowed = ceiling.AllowsUpdate(method.purpose.update) && override.KeepsUpdate(method.purpose.update)
				}
				before := len(fake.calls)
				err := method.call(guard)
				sent := len(fake.calls) > before
				if sent != allowed || (err == nil) != allowed {
					t.Fatalf("ceiling %07b, override %d: %s sent=%v err=%v, want allowed=%v", ceilingBits, overrideBits, method.name, sent, err, allowed)
				}
				var refusal domain.TrackerRefusal
				if !allowed && !errors.As(err, &refusal) {
					t.Fatalf("a write that is not allowed must be a refusal: %v", err)
				}
			}
			if want := len(methods()); records != want {
				t.Fatalf("recorded %d writes of %d", records, want)
			}
		}
	}
}

// Inkuna's own updates act on the run's story only, and Start and Finish
// move it only to the statuses tracker.yaml names for them.
func TestInkunasOwnUpdatesTouchOnlyTheRunsStory(t *testing.T) {
	everything := Allowance{Writes: domain.TrackerKinds(), Updates: Updates{Start: true, Result: true, Done: true}}
	settings := settingsAllowing(everything, nil)
	settings.Config.GitHub = GitHubConfig{InProgress: "In progress", Done: "Shipped"}
	fake := &fakeTracker{}
	guard, _ := NewGuard(fake, settings, "#12", func(domain.TrackerWrite) {})
	ctx := context.Background()
	if err := guard.Start(ctx); err != nil || fake.issue != "#12" || fake.status != "In progress" || fake.stage != domain.StageStarted {
		t.Fatalf("Start: %v, %s → %s (%s)", err, fake.issue, fake.status, fake.stage)
	}
	if err := guard.Finish(ctx); err != nil || fake.status != "Shipped" || fake.stage != domain.StageDone {
		t.Fatalf("Finish: %v, → %s (%s)", err, fake.status, fake.stage)
	}
	if _, err := guard.Report(ctx, "Completed", ""); err != nil || fake.issue != "#12" {
		t.Fatalf("Report: %v, on %s", err, fake.issue)
	}
	settings.Tracker = branching.TrackerLinear
	settings.Config.Linear.InProgress = "Doing"
	guard, _ = NewGuard(fake, settings, "ENG-12", func(domain.TrackerWrite) {})
	if err := guard.Start(ctx); err != nil || fake.status != "Doing" {
		t.Fatalf("Linear Start: %v, → %s", err, fake.status)
	}
	var record domain.TrackerWrite
	storyless, _ := NewGuard(fake, settings, "", func(write domain.TrackerWrite) { record = write })
	calls := len(fake.calls)
	for _, update := range []func() error{
		func() error { return storyless.Start(ctx) },
		func() error { return storyless.Finish(ctx) },
		func() error { _, err := storyless.Report(ctx, "Completed", ""); return err },
		func() error { return storyless.Attach(ctx, "https://github.com/acme/app/pull/3", "") },
	} {
		if err := update(); err == nil || !strings.Contains(record.Reason, "no story") {
			t.Errorf("a run without a story was updated: %v, %+v", err, record)
		}
	}
	if len(fake.calls) != calls {
		t.Fatal("an update without a story reached the tracker")
	}
}

func TestGuardRecordsWhatEachWriteChanged(t *testing.T) {
	fake := &fakeTracker{}
	var records []domain.TrackerWrite
	ceiling := Allowance{Writes: domain.TrackerKinds(), Updates: Updates{Start: true, Result: true, Done: true}}
	guard, err := NewGuard(fake, settingsAllowing(ceiling, nil), "12", func(write domain.TrackerWrite) { records = append(records, write) })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	estimate, assignee := 2, "me"
	if err := guard.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if url, err := guard.Report(ctx, "Completed in 2 rounds\nChecks 3 of 3", "<!-- inkuna-run:r1 -->"); err != nil || url != "https://tracker.test/comment/1" {
		t.Fatalf("Report = %q, %v", url, err)
	}
	if err := guard.Edit(ctx, "12", domain.IssueFields{AddLabels: []string{"backend"}, RemoveLabels: []string{"triage"}, Estimate: &estimate, Assignee: &assignee}); err != nil {
		t.Fatal(err)
	}
	if created, err := guard.Create(ctx, domain.IssueDraft{Title: "Index care events", Parent: "12"}); err != nil || created.ID != "7" {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	want := []domain.TrackerWrite{
		{Tracker: "github", Action: "status", Update: domain.UpdateStart, Issue: "12", Change: "status → In Progress", Outcome: domain.TrackerApplied},
		{Tracker: "github", Action: "comment", Update: domain.UpdateResult, Issue: "12", Change: "comment: Completed in 2 rounds …", Outcome: domain.TrackerApplied, URL: "https://tracker.test/comment/1"},
		{Tracker: "github", Action: "edit", Kind: domain.TrackerEdit, Issue: "12", Change: "edit: labels +backend, -triage; estimate → 2; assignee → me", Outcome: domain.TrackerApplied},
		{Tracker: "github", Action: "create", Kind: domain.TrackerCreate, Issue: "12", Change: "new sub-issue of 12: Index care events", Outcome: domain.TrackerApplied, URL: "https://tracker.test/issue/7"},
	}
	if len(records) != len(want) {
		t.Fatalf("records = %+v", records)
	}
	for index, record := range records {
		if record.At.IsZero() {
			t.Errorf("record %d has no time", index)
		}
		record.At = want[index].At
		if record != want[index] {
			t.Errorf("record %d = %+v\nwant %+v", index, record, want[index])
		}
	}
}

// An update the story already had is recorded as unchanged, with how it
// was, and a status the tracker chose for a stage is recorded as chosen.
func TestGuardRecordsAnUpdateTheStoryHadAlready(t *testing.T) {
	everything := Allowance{Writes: domain.TrackerKinds(), Updates: Updates{Start: true, Result: true, Done: true}}
	settings := settingsAllowing(everything, nil)
	settings.Tracker = branching.TrackerLinear
	var records []domain.TrackerWrite
	no := false
	fake := &fakeTracker{moved: &domain.StageChange{Status: "In Progress", Note: "ENG-12 is In Progress already"}, attached: &no}
	guard, _ := NewGuard(fake, settings, "ENG-12", func(write domain.TrackerWrite) { records = append(records, write) })
	ctx := context.Background()
	if err := guard.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := guard.Attach(ctx, "https://github.com/acme/app/pull/3", "Fix login"); err != nil {
		t.Fatal(err)
	}
	fake.moved = &domain.StageChange{Status: "Shipped", Changed: true, Note: `team ENG has no state named "Done", so its first completed state`}
	if err := guard.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	want := []struct{ outcome, change, reason string }{
		{domain.TrackerUnchanged, "status → In Progress", "ENG-12 is In Progress already"},
		{domain.TrackerUnchanged, "attach https://github.com/acme/app/pull/3", "it is attached already"},
		{domain.TrackerApplied, `status → Shipped (team ENG has no state named "Done", so its first completed state)`, ""},
	}
	if len(records) != len(want) {
		t.Fatalf("records = %+v", records)
	}
	for index, record := range records {
		if record.Outcome != want[index].outcome || record.Change != want[index].change || record.Reason != want[index].reason || record.Update == "" {
			t.Errorf("record %d = %+v\nwant %+v", index, record, want[index])
		}
	}
}

func TestGuardNamesTheFileThatRefusedAWrite(t *testing.T) {
	ceiling := Allowance{Writes: []domain.TrackerKind{domain.TrackerComment}, Updates: Updates{Result: true}}
	off := false
	override := &config.TrackerOverride{Updates: config.TrackerUpdatesOverride{Result: &off}}
	var records []domain.TrackerWrite
	guard, _ := NewGuard(&fakeTracker{}, settingsAllowing(ceiling, override), "12", func(write domain.TrackerWrite) { records = append(records, write) })
	ctx := context.Background()
	if err := guard.SetStatus(ctx, "12", "Done"); err == nil || !strings.Contains(err.Error(), ".inkuna/tracker.yaml does not list status under writes") {
		t.Errorf("status = %v", err)
	}
	if _, err := guard.Report(ctx, "body", ""); err == nil || !strings.Contains(err.Error(), "inkuna.yaml turns updates.result off") {
		t.Errorf("result = %v", err)
	}
	if err := guard.Finish(ctx); err == nil || !strings.Contains(err.Error(), "does not turn updates.done on") {
		t.Errorf("done = %v", err)
	}
	for _, record := range records {
		if record.Outcome != domain.TrackerRefused || record.Reason == "" {
			t.Errorf("record = %+v", record)
		}
	}
}

func TestGuardRecordsTheTrackersAnswer(t *testing.T) {
	ceiling := Allowance{Writes: domain.TrackerKinds()}
	for _, test := range []struct {
		err     error
		outcome string
	}{
		{domain.TrackerRefusal{Reason: "GitHub issues have no estimate"}, domain.TrackerRefused},
		{errors.New("gh issue edit: exit status 1: HTTP 502"), domain.TrackerFailed},
	} {
		var record domain.TrackerWrite
		guard, _ := NewGuard(&fakeTracker{err: test.err}, settingsAllowing(ceiling, nil), "12", func(write domain.TrackerWrite) { record = write })
		err := guard.Edit(context.Background(), "12", domain.IssueFields{RemoveLabels: []string{"x"}})
		if !errors.Is(err, test.err) || record.Outcome != test.outcome || record.Reason != test.err.Error() {
			t.Errorf("err = %v, record = %+v", err, record)
		}
	}
	var record domain.TrackerWrite
	guard, _ := NewGuard(&fakeTracker{}, settingsAllowing(ceiling, nil), "12", func(write domain.TrackerWrite) { record = write })
	if err := guard.Edit(context.Background(), "12", domain.IssueFields{}); err == nil || record.Reason != "the edit sets no field" {
		t.Errorf("empty edit: %v, %+v", err, record)
	}
}

func TestAGuardWithoutATrackerRefusesWithTheReason(t *testing.T) {
	var record domain.TrackerWrite
	settings := Settings{Tracker: branching.TrackerJira, Note: "Inkuna cannot write to Jira yet, so nothing is written"}
	guard, err := OpenGuard(settings, Sources{}, "PROJ-1", func(write domain.TrackerWrite) { record = write })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Comment(context.Background(), "PROJ-1", "body", ""); err == nil || record.Reason != settings.Note {
		t.Fatalf("err = %v, record = %+v", err, record)
	}
	if _, err := guard.Story(context.Background(), "PROJ-1"); err == nil {
		t.Fatal("a guard without a tracker read a story")
	}
	if _, err := NewGuard(&fakeTracker{}, settings, "PROJ-1", nil); err == nil {
		t.Fatal("a guard without a recorder was made")
	}
}

func TestOpenPicksTheRepositorysAdapter(t *testing.T) {
	github := Settings{Tracker: branching.TrackerGitHub, Exists: true, Config: Config{Version: 1, GitHub: GitHubConfig{Project: &Project{Owner: "acme", Number: 3}}}}
	login := func() (string, error) { return "octocat", nil }
	if _, err := open(github, Sources{GitHub: noRunner{}, Repository: "github.com/acme/app", GitHubLogin: login}); err != nil {
		t.Fatal(err)
	}
	signedOut := func() (string, error) { return "", errors.New("gh is not signed in to github.com") }
	if _, err := open(github, Sources{GitHub: noRunner{}, Repository: "github.com/acme/app", GitHubLogin: signedOut}); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("without gh's account: %v", err)
	}
	if _, err := open(github, Sources{GitHub: noRunner{}}); err == nil || !strings.Contains(err.Error(), "no GitHub origin") {
		t.Fatalf("without an origin: %v", err)
	}
	linearSettings := Settings{Tracker: branching.TrackerLinear, Exists: true}
	var record domain.TrackerWrite
	guard, err := OpenGuard(linearSettings, Sources{}, "ENG-1", func(write domain.TrackerWrite) { record = write })
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Start(context.Background()); err == nil || !strings.Contains(record.Reason, "no Linear key") {
		t.Fatalf("without a key: %v, %+v", err, record)
	}
}

type noRunner struct{}

func (noRunner) Run(context.Context, ...string) ([]byte, error) {
	return nil, errors.New("not run")
}

// A policy is asked only about a write the settings allow, after they allow
// it, and a refusal is recorded as one the settings make is.
func TestAPolicyIsAskedOnlyAboutWritesTheSettingsAllow(t *testing.T) {
	ceiling := Allowance{Writes: []domain.TrackerKind{domain.TrackerComment}, Updates: Updates{Result: true}}
	fake := &fakeTracker{}
	var records []domain.TrackerWrite
	guard, _ := NewGuard(fake, settingsAllowing(ceiling, nil), "12", func(write domain.TrackerWrite) { records = append(records, write) })
	var asked []domain.TrackerWrite
	var answer error
	guard.SetPolicy(func(_ context.Context, write domain.TrackerWrite) error {
		asked = append(asked, write)
		return answer
	})
	ctx := context.Background()

	answer = errors.New(`policy "acme/policy": Not on release day`)
	_, err := guard.Comment(ctx, "12", "Done.", "")
	var refusal domain.TrackerRefusal
	if !errors.As(err, &refusal) || refusal.Reason != answer.Error() {
		t.Fatalf("a refused comment = %v", err)
	}
	if len(fake.calls) != 0 || records[0].Outcome != domain.TrackerRefused || records[0].Reason != answer.Error() {
		t.Fatalf("a refused write reached the tracker or was recorded wrongly: %v %+v", fake.calls, records[0])
	}
	if len(asked) != 1 || asked[0].Kind != domain.TrackerComment || asked[0].Issue != "12" || asked[0].Action != "comment" || asked[0].Outcome != "" {
		t.Fatalf("the policy was asked %+v", asked)
	}

	// What the settings refuse never reaches the policy, which cannot allow it.
	answer = nil
	if err := guard.SetStatus(ctx, "12", "Done"); err == nil || !strings.Contains(err.Error(), "does not list status under writes") {
		t.Fatalf("status = %v", err)
	}
	if err := guard.Finish(ctx); err == nil || !strings.Contains(err.Error(), "does not turn updates.done on") {
		t.Fatalf("done = %v", err)
	}
	if len(asked) != 1 || len(fake.calls) != 0 {
		t.Fatalf("a write the settings refuse asked the policy or was made: %+v %v", asked, fake.calls)
	}

	// One it allows is made.
	if _, err := guard.Report(ctx, "Finished.", ""); err != nil || len(fake.calls) != 1 {
		t.Fatalf("an allowed update = %v, %v", err, fake.calls)
	}
	if len(asked) != 2 || asked[1].Update != domain.UpdateResult || asked[1].Issue != "12" {
		t.Fatalf("the update asked %+v", asked[1:])
	}
}

// A write Go refuses on its own checks, such as an empty edit, is refused
// with Go's reason, and the policy is never asked.
func TestAWriteThatFailsItsOwnChecksNeverReachesThePolicy(t *testing.T) {
	var records []domain.TrackerWrite
	guard, _ := NewGuard(&fakeTracker{}, settingsAllowing(Allowance{Writes: domain.TrackerKinds()}, nil), "12", func(write domain.TrackerWrite) { records = append(records, write) })
	asked := 0
	guard.SetPolicy(func(context.Context, domain.TrackerWrite) error {
		asked++
		return errors.New(`policy "acme/policy": Not today`)
	})
	ctx := context.Background()
	if err := guard.Edit(ctx, "12", domain.IssueFields{}); err == nil || !strings.Contains(err.Error(), "the edit sets no field") {
		t.Fatalf("an empty edit = %v", err)
	}
	if _, err := guard.Create(ctx, domain.IssueDraft{Title: "  ", Parent: "12"}); err == nil || !strings.Contains(err.Error(), "a new issue needs a title") {
		t.Fatalf("an untitled issue = %v", err)
	}
	if asked != 0 {
		t.Fatalf("the policy was asked %d times about writes Go refused", asked)
	}
	if len(records) != 2 || records[0].Reason != "the edit sets no field" || records[1].Reason != "a new issue needs a title" {
		t.Fatalf("records = %+v", records)
	}
}
