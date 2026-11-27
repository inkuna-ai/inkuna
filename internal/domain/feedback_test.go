package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSettleAddressedKeepsKnownItemsInOrderAndExplainsTheRest(t *testing.T) {
	items := []FeedbackItem{{ID: "f1"}, {ID: "c1"}, {ID: "k1"}}
	settled, reasons := SettleAddressed(items, []Addressed{
		{Item: "k1", Status: " Question ", Note: " Should the fixture keep the old date format? "},
		{Item: "f1", Status: "fixed", Note: "Empty names now return an error."},
		{Item: "f9", Status: "fixed", Note: "Not an item."},
		{Item: "c1", Status: "wontfix"},
		{Item: "c1", Status: "declined", Note: "The cache is per request."},
		// A later reply on an item replaces an earlier one.
		{Item: "f1", Status: "declined", Note: "On second thought, empty names are valid."},
	})
	want := []Addressed{
		{Item: "f1", Status: AddressedDeclined, Note: "On second thought, empty names are valid."},
		{Item: "c1", Status: AddressedDeclined, Note: "The cache is per request."},
		{Item: "k1", Status: AddressedQuestion, Note: "Should the fixture keep the old date format?"},
	}
	if !reflect.DeepEqual(settled, want) {
		t.Fatalf("settled %+v, want %+v", settled, want)
	}
	if len(reasons) != 2 || !strings.Contains(reasons[0], `"f9" is not a feedback item`) || !strings.Contains(reasons[1], `c1 has status "wontfix"`) {
		t.Fatalf("reasons %q", reasons)
	}
	if settled, reasons := SettleAddressed(nil, nil); len(settled) != 0 || len(reasons) != 0 {
		t.Fatalf("nothing to settle: %+v %q", settled, reasons)
	}
}

func TestAReplyThatStartsWithAnItemsIDNamesThatItem(t *testing.T) {
	items := []FeedbackItem{{ID: "f1"}, {ID: "f10"}, {ID: "k1"}}
	// As haiku wrote it in a real round: the id, then the item's text.
	settled, reasons := SettleAddressed(items, []Addressed{
		{Item: "f1: Shout should return an error for an empty name", Status: "fixed"},
		{Item: "[F10] Rename the flag", Status: "declined"},
		{Item: "(k1)", Status: "question"},
		{Item: "f1x", Status: "fixed"},
	})
	if got := fmt.Sprint(settled); got != "[{f1 fixed } {f10 declined } {k1 question }]" || len(reasons) != 1 || !strings.Contains(reasons[0], `"f1x"`) {
		t.Fatalf("settled %s, reasons %q", got, reasons)
	}
}

func TestARoundDigestKeepsTheLatestEarlierRunsOnALineEach(t *testing.T) {
	earlier := []string{" Round 1, sent back with f1 fixed: Added\nthe flag ", strings.Repeat("long ", 200)}
	for round := 3; round < 3+MaxEarlierRounds; round++ {
		earlier = append(earlier, fmt.Sprintf("Round %d: ran", round))
	}
	handoff := HandoffResult{Summary: " Validated the name ", Changes: []Change{{Path: "name.go"}, {Path: " "}}}
	digest := NewRoundDigest(12, handoff, []string{"f1 fixed: \"Handle\nan empty name\"", " "}, earlier)
	if digest.Round != 12 || digest.Summary != "Validated the name" || !reflect.DeepEqual(digest.Changes, []string{"name.go"}) || !reflect.DeepEqual(digest.Replies, []string{"f1 fixed: \"Handle an empty name\""}) {
		t.Fatalf("digest %+v", digest)
	}
	if len(digest.Earlier) != MaxEarlierRounds || digest.MoreEarlier != 2 || digest.Earlier[0] == earlier[0] || digest.Earlier[MaxEarlierRounds-1] != fmt.Sprintf("Round %d: ran", 2+MaxEarlierRounds) {
		t.Fatalf("earlier %q, %d more", digest.Earlier, digest.MoreEarlier)
	}
	first := NewRoundDigest(2, handoff, nil, earlier[:2])
	if first.Earlier[0] != "Round 1, sent back with f1 fixed: Added the flag" || len(first.Earlier[1]) > MaxDescriptionBytes || !strings.HasSuffix(first.Earlier[1], "…") {
		t.Fatalf("each line is one line, held to %d bytes: %q", MaxDescriptionBytes, first.Earlier)
	}
}

func TestBranchChangesListAtMostMaxDigestChangesFiles(t *testing.T) {
	files := []ChangedFile{{Path: " "}, {Path: strings.Repeat("deep/", 200) + "file.go", Additions: 1}}
	for index := 0; index < MaxDigestChanges+3; index++ {
		files = append(files, ChangedFile{Path: fmt.Sprintf("file%02d.go", index), Additions: index})
	}
	changes := NewBranchChanges("base", "tip", files)
	if len(changes.Files) != MaxDigestChanges || changes.MoreFiles != 4 || len(changes.Files[0].Path) > MaxDescriptionBytes || changes.Files[1].Path != "file00.go" {
		t.Fatalf("changes %d files, %d more, first %d bytes", len(changes.Files), changes.MoreFiles, len(changes.Files[0].Path))
	}
}

func TestFeedbackReachesNeitherDownstreamStagesNorRemoteAdvisors(t *testing.T) {
	task := TaskSpec{Version: 1, ID: "task", Kind: TaskImplementation, Objective: "Validate names", Repository: ".", Budget: Budget{WallTimeSeconds: 300}}
	step := WorkflowStep{ID: "review", Title: "Review", Kind: StepAgent, Permission: PermissionReadOnly, Budget: Budget{WallTimeSeconds: 60}}
	// The target's handoff, after a round that sent it the secret feedback
	// below: its reply restates nothing, but the reply's notes are its own.
	target := HandoffResult{
		Version: 1, Status: HandoffCompleted, Summary: "Rejected empty names", Decisions: []string{"Return an error"},
		Addressed: []Addressed{{Item: "f1", Status: AddressedFixed, Note: "note-only-the-target-wrote"}},
	}
	downstream, err := BuildContext(task, step, []HandoffResult{target}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if downstream.Feedback != nil || downstream.PreviousRound != nil {
		t.Fatalf("a stage downstream of a target gets its handoff, never its feedback: %+v", downstream)
	}
	advisor, err := BuildAdvisorHandoff(task, step, []HandoffResult{target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []any{downstream, advisor} {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if text := string(data); strings.Contains(text, "note-only-the-target-wrote") || strings.Contains(text, `"addressed"`) || strings.Contains(text, `"feedback"`) {
			t.Fatalf("a target's replies on its feedback leave with its handoff: %s", text)
		}
	}
	// What leaves for a remote advisor is decided by AdvisorHandoff's
	// fields: none of them can carry a round's feedback.
	for _, field := range reflect.VisibleFields(reflect.TypeOf(AdvisorHandoff{})) {
		if strings.Contains(strings.ToLower(field.Name), "feedback") || strings.Contains(strings.ToLower(field.Name), "round") || field.Type == reflect.TypeOf(&RoundFeedback{}) {
			t.Fatalf("AdvisorHandoff has a field for feedback: %s", field.Name)
		}
	}
	if advisor.DependencySummary[0] != "Rejected empty names" {
		t.Fatalf("the advisor still gets the target's new summary: %+v", advisor)
	}
}

func TestFeedbackIsHeldToItsBudgetWithEveryItemKept(t *testing.T) {
	log := lines("log", 6<<10) + "\nFAIL: TestEmptyName"
	feedback := &RoundFeedback{Round: 2, Items: []FeedbackItem{
		{ID: "f1", From: FeedbackFromHuman, Text: lines("long request", 12<<10)},
		{ID: "f2", From: FeedbackFromHuman, Text: "Rename the flag."},
		{ID: "c1", From: FeedbackFromHuman, Text: "Leaks.", Comment: &LineComment{Path: "cache.go", Line: 42, Side: SideNew, Excerpt: lines("excerpt", 3<<10), Thread: "c1 declined"}},
		{ID: "k1", From: FeedbackFromCheck, Check: &FailedCheck{Command: []string{"go", "test", "./..."}, ExitCode: exitCode(1), OutputTail: log}},
	}, Changes: &BranchChanges{Base: "aaaa", Tip: "bbbb", Files: []ChangedFile{{Path: "name.go", Additions: 3}}}}
	envelope := ContextEnvelope{Objective: "Validate names", Feedback: feedback}
	cut := apply(t, envelope, Budgets{Feedback: 4 << 10}, func(part, text string) string { return "inkuna://artifact/" + part })
	if len(cut.Feedback.Items) != 4 || cut.Feedback.Items[1].Text != "Rename the flag." || cut.Feedback.Items[2].Comment.Line != 42 || cut.Metadata[MetadataTruncated] != SectionFeedback {
		t.Fatalf("every item is kept, the short ones whole: %+v, truncated %q", cut.Feedback.Items, cut.Metadata[MetadataTruncated])
	}
	total := 0
	for _, text := range feedbackTexts(*cut.Feedback) {
		total += len(text)
	}
	if total > 4<<10 {
		t.Fatalf("the feedback is %d bytes, over its 4 KiB budget", total)
	}
	if text := cut.Feedback.Items[0].Text; !strings.HasPrefix(text, "long request line 00000") || !strings.HasSuffix(text, "; full text in inkuna://artifact/feedback]") {
		t.Fatalf("a long text keeps its start and ends with a note: %q", text)
	}
	if comment := cut.Feedback.Items[2].Comment; !strings.HasPrefix(comment.Excerpt, "excerpt line 00000") || !strings.Contains(comment.Excerpt, "[truncated: ") || comment.Thread != "c1 declined" || cut.Feedback.Items[2].Text != "Leaks." {
		t.Fatalf("a comment's long excerpt is cut and its short thread kept: %+v", comment)
	}
	if feedback.Items[2].Comment.Excerpt == cut.Feedback.Items[2].Comment.Excerpt {
		t.Fatal("the caller's comment was changed")
	}
	tail := cut.Feedback.Items[3].Check.OutputTail
	if !strings.HasPrefix(tail, "[truncated: ") || !strings.HasSuffix(tail, "\nFAIL: TestEmptyName") || !strings.Contains(tail, "full text in inkuna://artifact/feedback]\nlog line") {
		t.Fatalf("a check's output keeps its end, after the note: %q", tail)
	}
	if feedback.Items[0].Text == cut.Feedback.Items[0].Text || feedback.Items[3].Check.OutputTail != log {
		t.Fatal("the caller's feedback was changed")
	}
	if again := apply(t, envelope, Budgets{Feedback: 32 << 10}, nil); !reflect.DeepEqual(again.Feedback, feedback) {
		t.Fatalf("feedback within its budget is sent as it is: %+v", again.Feedback)
	}

	// Over the envelope's budget, feedback gives way after everything but
	// the objective, and never below MinFeedbackText an item.
	feedback.Items[2].Comment = &LineComment{Path: "cache.go", Line: 42, Side: SideNew}
	envelope.RelevantRisks = []string{lines("risk", 8<<10)}
	cut = apply(t, envelope, Budgets{Envelope: 12 << 10}, nil)
	if EnvelopeBytes(cut) > 12<<10 || len(cut.RelevantRisks[0]) > MinBudget || cut.Objective != envelope.Objective {
		t.Fatalf("envelope %d bytes, risks %d bytes", EnvelopeBytes(cut), len(cut.RelevantRisks[0]))
	}
	// Short texts stay whole; long ones keep their start, or a check's
	// output its end, beside the note.
	sent := feedbackTexts(*cut.Feedback)
	for index, text := range feedbackTexts(*feedback) {
		switch {
		case len(text) <= MinFeedbackText && sent[index] != text:
			t.Fatalf("a short text was cut: %q", sent[index])
		case len(text) > MinFeedbackText && (!strings.Contains(sent[index], "[truncated: ") || len(sent[index]) > len(text)):
			t.Fatalf("a long text was not cut: %d bytes", len(sent[index]))
		}
	}
	if !strings.HasPrefix(sent[0], "long request line 00000") || !strings.HasSuffix(sent[len(sent)-1], "FAIL: TestEmptyName") {
		t.Fatalf("cut texts keep what matters most: %q, %q", sent[0][:40], sent[len(sent)-1])
	}
}

func TestThePreviousRoundsSummaryIsHeldToItsBudget(t *testing.T) {
	digest := &RoundDigest{Round: 1, Summary: strings.Repeat("Validated the name and wrote a test.\n", 400), Changes: []string{"name.go"}, Earlier: []string{"Round 1: started"}}
	envelope := ContextEnvelope{Objective: "Validate names", PreviousRound: digest}
	cut := apply(t, envelope, Budgets{PreviousRound: 2048}, func(part, text string) string { return "inkuna://artifact/" + part })
	summary := cut.PreviousRound.Summary
	if len(summary) > 2048 || !strings.HasSuffix(summary, "; full text in inkuna://artifact/previousRound]") || cut.Metadata[MetadataTruncated] != SectionPreviousRound {
		t.Fatalf("summary of %d bytes, truncated %q", len(summary), cut.Metadata[MetadataTruncated])
	}
	if !reflect.DeepEqual(cut.PreviousRound.Changes, digest.Changes) || !reflect.DeepEqual(cut.PreviousRound.Earlier, digest.Earlier) || digest.Summary == summary {
		t.Fatalf("only the summary is cut, and never the caller's: %+v", cut.PreviousRound)
	}
}

func TestSuffixKeepsTheEndAtALineOrACharacterBoundary(t *testing.T) {
	if got := suffix("first line\nsecond line", 15); got != "second line" {
		t.Fatalf("suffix at a line = %q", got)
	}
	if got := suffix("ééééé", 5); got != "éé" {
		t.Fatalf("suffix at a character = %q", got)
	}
	if got := suffix("short", 10); got != "short" {
		t.Fatalf("suffix of short text = %q", got)
	}
}

func exitCode(code int) *int { return &code }

func TestAFailedCheckSaysHowItEnded(t *testing.T) {
	for _, test := range []struct {
		check FailedCheck
		want  string
	}{
		{FailedCheck{ExitCode: exitCode(2)}, "exited with code 2"},
		{FailedCheck{ExitCode: exitCode(0), Error: "ignored"}, "exited with code 0"},
		{FailedCheck{Error: `exec: "golangci-lint": executable file not found in $PATH`}, `could not run: exec: "golangci-lint": executable file not found in $PATH`},
		{FailedCheck{}, "failed"},
	} {
		if got := test.check.Ended(); got != test.want {
			t.Errorf("%+v ended %q, want %q", test.check, got, test.want)
		}
	}
}

// TestASystemFailureIsOneTheWriterCannotFix checks which failed checks a
// stage's system exit codes make system failures.
func TestASystemFailureIsOneTheWriterCannotFix(t *testing.T) {
	for _, test := range []struct {
		name  string
		check FailedCheck
		codes []int
		want  bool
	}{
		{"a listed code", FailedCheck{ExitCode: exitCode(3)}, []int{3}, true},
		{"another code", FailedCheck{ExitCode: exitCode(1)}, []int{3}, false},
		{"could not start", FailedCheck{Error: `exec: "psql": executable file not found in $PATH`}, []int{3}, true},
		{"stopped by a signal", FailedCheck{Error: "signal: killed"}, []int{3}, true},
		{"no codes named", FailedCheck{ExitCode: exitCode(3)}, nil, false},
		{"no codes named, could not start", FailedCheck{Error: "exec: not found"}, nil, false},
	} {
		if got := test.check.System(test.codes); got != test.want {
			t.Errorf("%s: System = %v, want %v", test.name, got, test.want)
		}
	}
}
