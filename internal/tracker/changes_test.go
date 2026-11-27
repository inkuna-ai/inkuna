package tracker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// issueTracker is a fakeTracker with issues of its own: Story finds only
// those, counting each read, and readErr makes every read fail as a tracker
// that cannot be reached does.
type issueTracker struct {
	fakeTracker
	issues  map[string]domain.TrackerIssue
	reads   int
	readErr error
}

func (fake *issueTracker) Story(_ context.Context, id string) (domain.TrackerIssue, error) {
	fake.reads++
	if fake.readErr != nil {
		return domain.TrackerIssue{}, fake.readErr
	}
	issue, ok := fake.issues[strings.TrimPrefix(id, "#")]
	if !ok {
		return domain.TrackerIssue{}, domain.TrackerRefusal{Reason: "issue " + id + " was not found"}
	}
	return issue, nil
}

func checkingGuard(t *testing.T, fake domain.Tracker, tracker branching.Tracker, story string, override *config.TrackerOverride) *Guard {
	t.Helper()
	ceiling := allowanceOf(0b1111)
	settings := Settings{Tracker: tracker, Exists: true, Ceiling: ceiling, Effective: Narrow(ceiling, override)}
	guard, err := NewGuard(fake, settings, story, func(domain.TrackerWrite) { t.Fatal("Check recorded a write") })
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

func TestCheckAcceptsWhatTheStepAndTheRepositoryAllow(t *testing.T) {
	ctx := context.Background()
	fake := &issueTracker{issues: map[string]domain.TrackerIssue{"12": {ID: "12", Team: "MAD"}, "40": {ID: "40"}}}
	guard := checkingGuard(t, fake, branching.TrackerGitHub, "12", nil)
	all := domain.TrackerKinds()
	title := "Index events"
	for _, test := range []struct {
		change domain.TrackerChange
		want   Checked
	}{
		{domain.TrackerChange{Kind: domain.TrackerComment, Issue: "story", Body: "Took 4 s"}, Checked{Issue: "12"}},
		// The status is kept as the tracker names it.
		{domain.TrackerChange{Kind: domain.TrackerStatus, Issue: "#40", Status: "in review"}, Checked{Issue: "40"}},
		{domain.TrackerChange{Kind: domain.TrackerCreate, Title: &title}, Checked{}},
		{domain.TrackerChange{Kind: domain.TrackerCreate, Title: &title, Parent: "story"}, Checked{Issue: "12"}},
	} {
		checked, err := guard.Check(ctx, test.change, all)
		if err != nil {
			t.Fatalf("%+v: %v", test.change, err)
		}
		if checked.Issue != test.want.Issue || checked.Team != "" {
			t.Fatalf("%+v checked as %+v", test.change, checked)
		}
		if test.change.Kind == domain.TrackerStatus && checked.Change.Status != "In Review" {
			t.Fatalf("status = %q", checked.Change.Status)
		}
	}
	// Issue 12 was read once, however many changes named it.
	if fake.reads != 2 {
		t.Fatalf("reads = %d", fake.reads)
	}
}

func TestCheckRefusesWithTheReason(t *testing.T) {
	ctx := context.Background()
	title := "Index events"
	cycle := "current"
	comments := []domain.TrackerKind{domain.TrackerComment}
	all := domain.TrackerKinds()
	off := config.TrackerOverride{Writes: &[]domain.TrackerKind{domain.TrackerStatus, domain.TrackerEdit, domain.TrackerCreate}}
	for _, test := range []struct {
		name     string
		tracker  branching.Tracker
		story    string
		override *config.TrackerOverride
		granted  []domain.TrackerKind
		change   domain.TrackerChange
		want     string
	}{
		{"malformed", branching.TrackerGitHub, "12", nil, all, domain.TrackerChange{Kind: domain.TrackerComment, Issue: "story"}, "a comment needs a body"},
		{"not granted", branching.TrackerGitHub, "12", nil, comments, domain.TrackerChange{Kind: domain.TrackerStatus, Issue: "story", Status: "Done"}, "may not ask for status changes; it may ask for comment only"},
		{"granted none", branching.TrackerGitHub, "12", nil, nil, domain.TrackerChange{Kind: domain.TrackerComment, Issue: "story", Body: "Hi"}, "it may ask for no change at all"},
		{"turned off here", branching.TrackerGitHub, "12", &off, all, domain.TrackerChange{Kind: domain.TrackerComment, Issue: "story", Body: "Hi"}, "inkuna.yaml turns comment off"},
		{"no story", branching.TrackerGitHub, "", nil, all, domain.TrackerChange{Kind: domain.TrackerComment, Issue: "story", Body: "Hi"}, `the run has no story`},
		{"unknown issue", branching.TrackerGitHub, "12", nil, all, domain.TrackerChange{Kind: domain.TrackerComment, Issue: "99", Body: "Hi"}, "issue 99 was not found"},
		{"unknown parent", branching.TrackerGitHub, "12", nil, all, domain.TrackerChange{Kind: domain.TrackerCreate, Title: &title, Parent: "99"}, "issue 99 was not found"},
		{"unknown status", branching.TrackerGitHub, "12", nil, all, domain.TrackerChange{Kind: domain.TrackerStatus, Issue: "story", Status: "Shipped"}, `12 cannot be moved to "Shipped"; its statuses are Backlog, In Review`},
		{"field the tracker lacks", branching.TrackerGitHub, "12", nil, all, domain.TrackerChange{Kind: domain.TrackerEdit, Issue: "story", Cycle: &cycle}, "this tracker has no cycle"},
		{"Linear issue with no team", branching.TrackerLinear, "", nil, all, domain.TrackerChange{Kind: domain.TrackerCreate, Title: &title}, "the run has no story to take one from"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &issueTracker{issues: map[string]domain.TrackerIssue{"12": {ID: "12"}}}
			_, err := checkingGuard(t, fake, test.tracker, test.story, test.override).Check(ctx, test.change, test.granted)
			var refusal domain.TrackerRefusal
			if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, test.want) {
				t.Fatalf("err = %v, want a refusal saying %q", err, test.want)
			}
		})
	}
}

func TestCheckRefusesWhatItCannotAskTheTrackerAbout(t *testing.T) {
	fake := &issueTracker{readErr: errors.New("dial tcp: i/o timeout")}
	_, err := checkingGuard(t, fake, branching.TrackerGitHub, "12", nil).Check(context.Background(), domain.TrackerChange{Kind: domain.TrackerComment, Issue: "story", Body: "Hi"}, domain.TrackerKinds())
	var refusal domain.TrackerRefusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "could not read 12 to check the change, so it is not kept: dial tcp") {
		t.Fatalf("err = %v", err)
	}
}

func TestANewLinearIssueGoesInTheStorysTeam(t *testing.T) {
	fake := &issueTracker{issues: map[string]domain.TrackerIssue{"MAD-12": {ID: "MAD-12", Team: "MAD"}}}
	title := "Index events"
	checked, err := checkingGuard(t, fake, branching.TrackerLinear, "MAD-12", nil).Check(context.Background(), domain.TrackerChange{Kind: domain.TrackerCreate, Title: &title}, domain.TrackerKinds())
	if err != nil || checked.Team != "MAD" || checked.Issue != "" {
		t.Fatalf("checked = %+v, %v", checked, err)
	}
}

func TestApplyMakesEachKindThroughTheGuard(t *testing.T) {
	ctx := context.Background()
	title, label := "Index events", "backend"
	var writes []domain.TrackerWrite
	fake := &fakeTracker{}
	guard, err := NewGuard(fake, settingsAllowing(allowanceOf(0b1111), nil), "12", func(write domain.TrackerWrite) { writes = append(writes, write) })
	if err != nil {
		t.Fatal(err)
	}
	for _, checked := range []Checked{
		{Change: domain.TrackerChange{Kind: domain.TrackerStatus, Status: "In Review"}, Issue: "12"},
		{Change: domain.TrackerChange{Kind: domain.TrackerComment, Body: "Took 4 s"}, Issue: "12"},
		{Change: domain.TrackerChange{Kind: domain.TrackerEdit, AddLabels: []string{label}}, Issue: "40"},
		{Change: domain.TrackerChange{Kind: domain.TrackerCreate, Title: &title}, Issue: "12"},
	} {
		if _, err := guard.Apply(ctx, checked, "<!-- inkuna-change:c1 -->"); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(fake.calls, []string{"status", "comment", "edit", "create"}) {
		t.Fatalf("calls = %v", fake.calls)
	}
	changes := make([]string, 0, len(writes))
	for _, write := range writes {
		if write.Kind == "" || write.Outcome != domain.TrackerApplied {
			t.Fatalf("write = %+v", write)
		}
		changes = append(changes, write.Change)
	}
	want := []string{"status → In Review", "comment: Took 4 s", "edit: labels +backend", "new sub-issue of 12: Index events"}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("recorded %q, want %q", changes, want)
	}
}
