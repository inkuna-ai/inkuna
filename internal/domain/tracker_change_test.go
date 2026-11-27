package domain

import (
	"reflect"
	"strings"
	"testing"
)

func text(value string) *string { return &value }
func number(value int) *int     { return &value }

func TestATrackerChangeReadsEmptyFieldsAsLeftOut(t *testing.T) {
	// What a model held to a strict schema sends for a comment: every field,
	// the unused ones empty.
	strict := TrackerChange{
		Kind: " Comment ", Issue: " story ", Body: " Took 4 s. ", Title: text(""), Description: text("  "), AddLabels: []string{"", " "},
		Estimate: number(0), Priority: number(0), Assignee: text(""), Cycle: text(""), Milestone: text(""),
	}
	want := TrackerChange{Kind: TrackerComment, Issue: "story", Body: "Took 4 s."}
	if got := strict.Normalized(); !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized = %+v, want %+v", got, want)
	}
	if problem := strict.Normalized().Problem(); problem != "" {
		t.Fatalf("a strict model's comment is refused: %s", problem)
	}
	// An edit's zero is a value: priority 0 is none.
	edit := TrackerChange{Kind: TrackerEdit, Issue: "MAD-1", Priority: number(0), AddLabels: []string{"backend", " backend "}}.Normalized()
	if edit.Priority == nil || *edit.Priority != 0 || !reflect.DeepEqual(edit.AddLabels, []string{"backend"}) {
		t.Fatalf("edit = %+v", edit)
	}
}

func TestATrackerChangeCarriesExactlyWhatItsKindTakes(t *testing.T) {
	long := strings.Repeat("é", MaxTrackerText+1)
	for _, test := range []struct {
		name   string
		change TrackerChange
		want   string
	}{
		{"status", TrackerChange{Kind: TrackerStatus, Issue: "story", Status: "In Review"}, ""},
		{"comment", TrackerChange{Kind: TrackerComment, Issue: "MAD-12", Body: "Done"}, ""},
		{"edit", TrackerChange{Kind: TrackerEdit, Issue: "12", AddLabels: []string{"backend"}, Estimate: number(2)}, ""},
		{"create", TrackerChange{Kind: TrackerCreate, Title: text("Index events"), Parent: "story"}, ""},
		{"unknown kind", TrackerChange{Kind: "archive", Issue: "story"}, `"archive" is not a kind`},
		{"no issue", TrackerChange{Kind: TrackerComment, Body: "Done"}, "needs the issue it is for"},
		{"create names an issue", TrackerChange{Kind: TrackerCreate, Issue: "story", Title: text("A")}, "names the issue it goes under in parent"},
		{"another kind's field", TrackerChange{Kind: TrackerComment, Issue: "story", Body: "Done", Status: "Done", Parent: "story"}, "a comment change does not take status, parent"},
		{"status without one", TrackerChange{Kind: TrackerStatus, Issue: "story"}, "needs the status"},
		{"comment without a body", TrackerChange{Kind: TrackerComment, Issue: "story"}, "needs a body"},
		{"edit of nothing", TrackerChange{Kind: TrackerEdit, Issue: "story"}, "at least one field"},
		{"create without a title", TrackerChange{Kind: TrackerCreate, Description: text("Why")}, "needs a title"},
		{"long title", TrackerChange{Kind: TrackerCreate, Title: text(strings.Repeat("a", MaxTrackerTitle+1))}, "may have at most 256"},
		{"long body", TrackerChange{Kind: TrackerComment, Issue: "story", Body: long}, "the body is 10001 characters"},
		{"long description", TrackerChange{Kind: TrackerEdit, Issue: "story", Description: &long}, "the description is 10001 characters"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := test.change.Normalized().Problem()
			if test.want == "" && got != "" || test.want != "" && !strings.Contains(got, test.want) {
				t.Fatalf("problem = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStageTrackerKindsReadTheStagesOwnInOrder(t *testing.T) {
	step := WorkflowStep{Metadata: map[string]string{MetadataStageTracker: "create,comment,archive,comment"}}
	if got := StageTrackerKinds(step); !reflect.DeepEqual(got, []TrackerKind{TrackerComment, TrackerCreate}) {
		t.Fatalf("stage kinds = %v", got)
	}
	if got := StageTrackerKinds(WorkflowStep{}); got != nil {
		t.Fatalf("a stage asking for none = %v", got)
	}
}
