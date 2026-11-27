package domain

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// lines is about size bytes of numbered lines, so a cut can be seen to fall
// between two of them.
func lines(label string, size int) string {
	var builder strings.Builder
	for index := 0; builder.Len() < size; index++ {
		fmt.Fprintf(&builder, "%s line %05d of filler text\n", label, index)
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

// apply is ApplyBudgets for an envelope that fits.
func apply(t *testing.T, envelope ContextEnvelope, budgets Budgets, fullText FullText) ContextEnvelope {
	t.Helper()
	budgeted, err := ApplyBudgets(envelope, budgets, fullText)
	if err != nil {
		t.Fatal(err)
	}
	return budgeted
}

var notePattern = regexp.MustCompile(`\[truncated: (\d+) bytes omitted(?:; full text in ([^\]]+))?\]$`)

// omitted reads the note a cut text ends with: the bytes it says were left
// out and where it says the full text is.
func omitted(t *testing.T, text string) (int, string) {
	t.Helper()
	match := notePattern.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("no cut note at the end of %q", text[max(0, len(text)-120):])
	}
	count, _ := strconv.Atoi(match[1])
	return count, match[2]
}

func TestApplyBudgetsCutsOversizeFieldsWithANoteAndAFlag(t *testing.T) {
	envelope := ContextEnvelope{
		Objective:         lines("objective", 20<<10),
		DependencySummary: []string{"Planned the table", lines("summary", 5<<10)},
		RelevantDecisions: []string{lines("decision one", 5<<10), lines("decision two", 5<<10), "Keep bytes"},
		RelevantRisks:     []string{"Cents may round"},
		SkillExcerpts:     map[string]string{"big": lines("skill", 13<<10), "small": "Wrap errors."},
		HumanResponses:    []HumanResponse{{Question: "First?", Answer: lines("old answer", 6<<10)}, {Question: "Latest?", Answer: lines("new answer", 3<<10)}},
		PeerMessages:      []PeerMessage{{From: "reviewer", Content: lines("old reply", 10<<10)}, {From: "documenter", Subject: "Re: docs", Content: lines("new reply", 8<<10)}},
		Metadata:          map[string]string{MetadataPermission: string(PermissionReadOnly)},
	}
	original := envelope
	sizes := []int{len(envelope.SkillExcerpts["big"]), len(envelope.RelevantDecisions[0]), len(envelope.PeerMessages[0].Content), len(envelope.HumanResponses[0].Answer)}
	asked := map[string]string{}
	budgeted := apply(t, envelope, Budgets{}, func(part, text string) string {
		asked[part] = text
		return "inkuna://artifact/" + part
	})
	budgets := DefaultBudgets()

	if len(budgeted.Objective) > budgets.Objective || !strings.HasPrefix(original.Objective, strings.Split(budgeted.Objective, "\n[truncated")[0]) {
		t.Fatalf("objective is %d bytes, over %d or not a prefix", len(budgeted.Objective), budgets.Objective)
	}
	// The note's count is exact, and the cut falls between two lines.
	kept := strings.TrimSuffix(budgeted.Objective[:strings.LastIndex(budgeted.Objective, "\n[truncated")], "\n")
	if count, where := omitted(t, budgeted.Objective); count != len(original.Objective)-len(kept) || where != "inkuna://artifact/objective" || !strings.HasSuffix(kept, "of filler text") {
		t.Fatalf("objective note says %d bytes in %q; %d were left out", count, where, len(original.Objective)-len(kept))
	}
	if budgeted.DependencySummary[0] != "Planned the table" || len(budgeted.DependencySummary[1]) > budgets.DependencySummary {
		t.Fatalf("summaries = %d and %d bytes", len(budgeted.DependencySummary[0]), len(budgeted.DependencySummary[1]))
	}
	// Decisions keep their first entries and end with the note.
	decisions := budgeted.RelevantDecisions
	if listBytes(decisions) > budgets.Decisions || decisions[0] != original.RelevantDecisions[0] || !strings.HasPrefix(decisions[len(decisions)-1], "[truncated: ") {
		t.Fatalf("decisions = %d bytes in %d entries", listBytes(decisions), len(decisions))
	}
	if !reflect.DeepEqual(budgeted.RelevantRisks, original.RelevantRisks) || budgeted.SkillExcerpts["small"] != "Wrap errors." {
		t.Fatal("parts within their budgets must be left as they were")
	}
	if len(budgeted.SkillExcerpts["big"]) > budgets.Skill {
		t.Fatalf("skill is %d bytes", len(budgeted.SkillExcerpts["big"]))
	}
	// Answers and replies keep the newest whole; the older one is cut short
	// and ends with its own note.
	responses := budgeted.HumanResponses
	if humanResponseBytes(responses) > budgets.HumanResponses || len(responses) != 2 || responses[0].Question != "First?" || responses[1] != original.HumanResponses[1] {
		t.Fatalf("human responses = %d bytes in %d entries", humanResponseBytes(responses), len(responses))
	}
	if count, _ := omitted(t, responses[0].Answer); count != len(original.HumanResponses[0].Answer)-len(strings.Split(responses[0].Answer, "\n[truncated")[0]) {
		t.Fatalf("the cut answer's note says %d bytes", count)
	}
	messages := budgeted.PeerMessages
	if peerMessageBytes(messages) > budgets.PeerMessages || len(messages) != 2 || messages[0].From != "reviewer" || messages[1] != original.PeerMessages[1] {
		t.Fatalf("peer messages = %d bytes in %d entries", peerMessageBytes(messages), len(messages))
	}
	omitted(t, messages[0].Content)
	if got, want := budgeted.Metadata[MetadataTruncated], "objective,dependencies,decisions,humanResponses,peerMessages,skill:big"; got != want {
		t.Fatalf("truncated = %q, want %q", got, want)
	}
	// The full text handed over is the whole part, before any cut.
	if asked["objective"] != original.Objective || asked["skill:big"] != original.SkillExcerpts["big"] || !strings.Contains(asked["decisions"], "decision two line") {
		t.Fatal("the full text must be the part as it was")
	}
	// Nothing the caller holds was changed.
	after := []int{len(original.SkillExcerpts["big"]), len(original.RelevantDecisions[0]), len(original.PeerMessages[0].Content), len(original.HumanResponses[0].Answer)}
	if len(original.Metadata) != 1 || len(original.RelevantDecisions) != 3 || len(original.PeerMessages) != 2 || !reflect.DeepEqual(after, sizes) {
		t.Fatal("ApplyBudgets modified the caller's envelope")
	}
}

func TestApplyBudgetsLeavesAnEnvelopeWithinItsBudgetsAlone(t *testing.T) {
	envelope := ContextEnvelope{
		Objective: "Add a usage table", AcceptanceCriteria: []string{"rows add up"}, DependencySummary: []string{"Planned"},
		RelevantDecisions: []string{"Keep bytes"}, SkillExcerpts: map[string]string{"conventions": "Wrap errors."},
		HumanResponses: []HumanResponse{{Question: "Cents?", Answer: "Yes"}}, PeerMessages: []PeerMessage{{From: "reviewer", Content: "Fine"}},
		Docs: []DocBrief{{Path: "docs/a.md", Title: "A"}},
	}
	budgeted := apply(t, envelope, Budgets{}, func(string, string) string {
		t.Fatal("nothing should be cut")
		return ""
	})
	if !reflect.DeepEqual(budgeted, envelope) {
		t.Fatalf("an envelope within its budgets changed: %+v", budgeted)
	}
	// Cutting again changes nothing: a cut part, note included, fits.
	cut := apply(t, ContextEnvelope{Objective: lines("objective", 40<<10), RelevantDecisions: []string{lines("decision", 20<<10)}}, Budgets{}, nil)
	if again := apply(t, cut, Budgets{}, func(string, string) string { t.Fatal("a cut part was cut again"); return "" }); !reflect.DeepEqual(again, cut) {
		t.Fatal("applying the budgets twice changed the envelope")
	}
	if _, where := omitted(t, cut.Objective); where != "" {
		t.Fatalf("without a FullText the note names no place, got %q", where)
	}
}

func TestA2MBLinearDescriptionStaysUnderTheEnvelopeBudget(t *testing.T) {
	task := TaskSpec{Version: 1, ID: "task", Kind: TaskImplementation, Objective: "MAD-1: import\n\n" + lines("description", 2<<20), Repository: ".", Budget: Budget{WallTimeSeconds: 300}}
	step := WorkflowStep{ID: "code", Title: "Code", Kind: StepAgent, Permission: PermissionWorktree, SelectedSkills: []string{"one", "two", "three"}, Budget: Budget{WallTimeSeconds: 60}}
	skills := map[string]string{"one": lines("one", 30<<10), "two": lines("two", 30<<10), "three": lines("three", 30<<10)}
	index := make([]DocBrief, 0, 300)
	for number := range 300 {
		index = append(index, DocBrief{Path: fmt.Sprintf("docs/area/doc-%03d.md", number), Title: "A doc about part of the system", Tags: []string{"area"}, Status: "current", LastUpdated: fmt.Sprintf("2026-%02d-01", 1+number%12), Author: "A. Author"})
	}
	dependencies := []HandoffResult{{Summary: lines("summary", 50<<10), Decisions: []string{lines("decision", 50<<10)}}}
	envelope, err := BuildContext(task, step, dependencies, skills, index)
	if err != nil {
		t.Fatal(err)
	}
	if EnvelopeBytes(envelope) < 2<<20 {
		t.Fatalf("the envelope should start over 2 MB, is %d bytes", EnvelopeBytes(envelope))
	}
	budgeted := apply(t, envelope, Budgets{}, nil)
	if size := EnvelopeBytes(budgeted); size > DefaultBudgets().Envelope {
		t.Fatalf("the envelope is %d bytes, over its %d-byte budget", size, DefaultBudgets().Envelope)
	}
	if len(budgeted.Objective) > DefaultBudgets().Objective || !strings.HasPrefix(budgeted.Objective, "MAD-1: import") {
		t.Fatalf("objective is %d bytes", len(budgeted.Objective))
	}
	// The doc index goes first: it is cut, flagged, and reported.
	if len(budgeted.Docs) >= len(index) || budgeted.Metadata[MetadataDocsTruncated] != "true" || !strings.Contains(budgeted.Metadata[MetadataTruncated], "docs") {
		t.Fatalf("doc index = %d of %d, metadata %v", len(budgeted.Docs), len(index), budgeted.Metadata)
	}
}

func TestEnvelopeBudgetCutsTheDocIndexBeforeAnythingElse(t *testing.T) {
	index := make([]DocBrief, 0, 200)
	for number := range 200 {
		index = append(index, DocBrief{Path: fmt.Sprintf("docs/doc-%03d.md", number), Title: "A doc", LastUpdated: fmt.Sprintf("2026-01-%02d", 1+number%28)})
	}
	envelope := ContextEnvelope{Objective: lines("objective", 10<<10), SkillExcerpts: map[string]string{"one": lines("skill", 10<<10)}, Docs: index}
	budgets := Budgets{Envelope: 24 << 10}
	budgeted := apply(t, envelope, budgets, nil)
	if budgeted.Objective != envelope.Objective || budgeted.SkillExcerpts["one"] != envelope.SkillExcerpts["one"] {
		t.Fatal("the doc index can make the room, so nothing else is cut")
	}
	if size := EnvelopeBytes(budgeted); size > budgets.Envelope || len(budgeted.Docs) == 0 || budgeted.Metadata[MetadataTruncated] != "docs" {
		t.Fatalf("envelope is %d bytes with %d docs, truncated %q", size, len(budgeted.Docs), budgeted.Metadata[MetadataTruncated])
	}
	// With the index gone and still over, the largest skill is cut next.
	tight := apply(t, envelope, Budgets{Envelope: 12 << 10}, nil)
	if len(tight.Docs) != 0 || len(tight.SkillExcerpts["one"]) >= 10<<10 || tight.Objective != envelope.Objective {
		t.Fatalf("docs %d, skill %d bytes, objective cut %v", len(tight.Docs), len(tight.SkillExcerpts["one"]), tight.Objective != envelope.Objective)
	}
}

func TestPrefixCutsAtALineOrACharacterBoundary(t *testing.T) {
	if got := prefix("one\ntwo\nthree", 10); got != "one\ntwo" {
		t.Fatalf("prefix = %q", got)
	}
	// A line boundary that keeps less than half the room is passed over.
	if got := prefix("a\n"+strings.Repeat("b", 20), 12); got != "a\n"+strings.Repeat("b", 10) {
		t.Fatalf("prefix = %q", got)
	}
	// Never inside a character.
	if got := prefix("héllo", 2); got != "h" {
		t.Fatalf("prefix = %q", got)
	}
}

func TestBudgetsTakeDefaultsAndRefuseTinyValues(t *testing.T) {
	budgets := Budgets{Objective: 32 << 10}.WithDefaults()
	if budgets.Objective != 32<<10 || budgets.Skill != DefaultBudgets().Skill || budgets.Envelope != DefaultBudgets().Envelope {
		t.Fatalf("budgets = %+v", budgets)
	}
	if invalid := (Budgets{Skill: 10, Envelope: -1, Objective: MinBudget}).Invalid(); !reflect.DeepEqual(invalid, []string{"skill", "envelope"}) {
		t.Fatalf("invalid = %v", invalid)
	}
}

func TestIndexSkillsSendsTheNamedSkillsByReference(t *testing.T) {
	envelope := ContextEnvelope{SkillExcerpts: map[string]string{"lazy": "Long body", "inline": "Short body"}}
	indexed := IndexSkills(envelope, []SkillBrief{{ID: "lazy", Description: "The lazy skill", Path: ".inkuna/skills/lazy/SKILL.md"}, {ID: "unselected", Path: "x"}})
	if !reflect.DeepEqual(indexed.SkillExcerpts, map[string]string{"inline": "Short body"}) || len(indexed.SkillIndex) != 1 || indexed.SkillIndex[0].ID != "lazy" {
		t.Fatalf("indexed = %+v", indexed)
	}
	if len(envelope.SkillExcerpts) != 2 {
		t.Fatal("IndexSkills modified the caller's map")
	}
}

func TestTheAnswerCutShortCarriesItsOwnNote(t *testing.T) {
	// The newest answer alone is over the budget: it is cut, it says so, and
	// nothing claims older answers were left out.
	spec := lines("spec", 30<<10)
	cut := apply(t, ContextEnvelope{HumanResponses: []HumanResponse{{Question: "Paste the spec?", Answer: spec}}}, Budgets{}, nil)
	if len(cut.HumanResponses) != 1 || cut.HumanResponses[0].Question != "Paste the spec?" {
		t.Fatalf("responses = %+v", cut.HumanResponses)
	}
	answer := cut.HumanResponses[0].Answer
	if count, _ := omitted(t, answer); count != len(spec)-len(strings.Split(answer, "\n[truncated")[0]) || humanResponseBytes(cut.HumanResponses) > DefaultBudgets().HumanResponses {
		t.Fatalf("note says %d bytes; %d bytes sent", count, humanResponseBytes(cut.HumanResponses))
	}
	// A question longer than the room gives way, so the answer still arrives.
	long := apply(t, ContextEnvelope{HumanResponses: []HumanResponse{{Question: lines("question", 20<<10), Answer: "Use sqlite"}}}, Budgets{}, nil)
	if len(long.HumanResponses) != 1 || !strings.HasPrefix(long.HumanResponses[0].Answer, "Use sqlite") {
		t.Fatalf("the answer was lost: %+v", long.HumanResponses)
	}
	// Answers left out entirely are stood for by one entry, before the rest.
	many := []HumanResponse{{Question: "One?", Answer: lines("one", 6<<10)}, {Question: "Two?", Answer: lines("two", 6<<10)}, {Question: "Three?", Answer: "Yes"}}
	dropped := apply(t, ContextEnvelope{HumanResponses: many}, Budgets{}, nil).HumanResponses
	if dropped[0].Question != "Earlier answers, left out" || dropped[len(dropped)-1] != many[2] || strings.Contains(dropped[1].Question, "One?") {
		t.Fatalf("responses = %+v", dropped)
	}
	if count, _ := omitted(t, dropped[0].Answer); count != len("One?")+len(many[0].Answer) {
		t.Fatalf("the entry for left-out answers says %d bytes", count)
	}
}

func TestEnvelopeBudgetCutsEqualSkillsAlike(t *testing.T) {
	skills := map[string]string{}
	for _, id := range []string{"a", "b", "c", "d"} {
		skills[id] = lines("skill "+id, 10<<10)
	}
	budgeted := apply(t, ContextEnvelope{Objective: "Do it", SkillExcerpts: skills}, Budgets{Envelope: 24 << 10}, nil)
	sizes := make([]int, 0, len(skills))
	for _, id := range []string{"a", "b", "c", "d"} {
		sizes = append(sizes, len(budgeted.SkillExcerpts[id]))
	}
	for _, size := range sizes {
		if size < 5<<10 || size > 6<<10 || size-sizes[0] > 64 || sizes[0]-size > 64 {
			t.Fatalf("equal skills were cut unequally: %v", sizes)
		}
	}
	if EnvelopeBytes(budgeted) > 24<<10 {
		t.Fatalf("envelope is %d bytes", EnvelopeBytes(budgeted))
	}
}

func TestLevelDownShedsFromTheLargestFirst(t *testing.T) {
	sizes := []int{10000, 4000, 9000, 2000}
	levelDown(sizes, 3000, 1024)
	if !reflect.DeepEqual(sizes, []int{8000, 4000, 8000, 2000}) {
		t.Fatalf("sizes = %v", sizes)
	}
	floor := []int{3000, 2000}
	levelDown(floor, 100000, 1024)
	if !reflect.DeepEqual(floor, []int{1024, 1024}) {
		t.Fatalf("sizes = %v", floor)
	}
}

func TestAcceptanceCriteriaAndDescriptionsAreBoundedToo(t *testing.T) {
	criteria := make([]string, 0, 400)
	for number := range 400 {
		criteria = append(criteria, fmt.Sprintf("- [ ] criterion %03d imported from a long checklist", number))
	}
	envelope := ContextEnvelope{
		Objective: "Import", AcceptanceCriteria: criteria,
		DependencyOutputs: []ArtifactOutput{{Path: ".inkuna/out/plan.md", Description: lines("description", 2<<10)}},
		SkillIndex:        []SkillBrief{{ID: "lazy", Path: ".inkuna/skills/lazy/SKILL.md", Description: strings.Repeat("é", 400)}},
	}
	budgeted := apply(t, envelope, Budgets{}, nil)
	if listBytes(budgeted.AcceptanceCriteria) > DefaultBudgets().AcceptanceCriteria || budgeted.AcceptanceCriteria[0] != criteria[0] {
		t.Fatalf("criteria = %d bytes", listBytes(budgeted.AcceptanceCriteria))
	}
	omitted(t, budgeted.AcceptanceCriteria[len(budgeted.AcceptanceCriteria)-1])
	if budgeted.Metadata[MetadataTruncated] != PartAcceptanceCriteria {
		t.Fatalf("truncated = %q", budgeted.Metadata[MetadataTruncated])
	}
	// A description says what a file is; the file has the rest.
	for _, description := range []string{budgeted.DependencyOutputs[0].Description, budgeted.SkillIndex[0].Description} {
		if len(description) > MaxDescriptionBytes || !strings.HasSuffix(description, "…") || !utf8.ValidString(description) {
			t.Fatalf("description is %d bytes: %q", len(description), description[max(0, len(description)-20):])
		}
	}
	if len(envelope.DependencyOutputs[0].Description) < 2<<10 || strings.HasSuffix(envelope.SkillIndex[0].Description, "…") {
		t.Fatal("ApplyBudgets modified the caller's descriptions")
	}
}

func TestTheStageAndTheConstraintsAreHeldToTheirBudgets(t *testing.T) {
	constraints := make([]string, 0, 300)
	for number := range 300 {
		constraints = append(constraints, fmt.Sprintf("constraint %03d: leave the public API as it is", number))
	}
	envelope := ContextEnvelope{
		Objective: "Fix the crash", StageTitle: strings.Repeat("Reproduce ", 100), StagePrompt: lines("stage", 10<<10), Constraints: constraints,
	}
	original := append([]string(nil), constraints...)
	budgeted := apply(t, envelope, Budgets{}, func(part, text string) string { return "inkuna://artifact/" + part })
	budgets := DefaultBudgets()
	if len(budgeted.StagePrompt) > budgets.StagePrompt || !strings.HasPrefix(envelope.StagePrompt, strings.Split(budgeted.StagePrompt, "\n[truncated")[0]) {
		t.Fatalf("stage prompt is %d bytes, over %d or not a prefix", len(budgeted.StagePrompt), budgets.StagePrompt)
	}
	if _, where := omitted(t, budgeted.StagePrompt); where != "inkuna://artifact/stage" {
		t.Fatalf("the stage prompt's full text is in %q", where)
	}
	if listBytes(budgeted.Constraints) > budgets.Constraints || budgeted.Constraints[0] != constraints[0] {
		t.Fatalf("constraints = %d bytes", listBytes(budgeted.Constraints))
	}
	if _, where := omitted(t, budgeted.Constraints[len(budgeted.Constraints)-1]); where != "inkuna://artifact/constraints" {
		t.Fatalf("the constraints' full text is in %q", where)
	}
	// The title names the stage; past a description's length it says no
	// more, so it is shortened rather than noted.
	if title := budgeted.StageTitle; len(title) > MaxDescriptionBytes || !strings.HasSuffix(title, "…") {
		t.Fatalf("stage title is %d bytes: %q", len(title), title[max(0, len(title)-20):])
	}
	if budgeted.Metadata[MetadataTruncated] != SectionStage+","+SectionConstraints {
		t.Fatalf("truncated = %q", budgeted.Metadata[MetadataTruncated])
	}
	if !reflect.DeepEqual(envelope.Constraints, original) {
		t.Fatal("ApplyBudgets modified the caller's envelope")
	}
	if again := apply(t, budgeted, Budgets{}, nil); !reflect.DeepEqual(again, budgeted) {
		t.Fatal("applying the budgets again changed the envelope")
	}

	// Over the envelope's budget, the constraints give way first, then the
	// stage prompt, and the objective last.
	envelope = ContextEnvelope{Objective: lines("objective", 6<<10), StagePrompt: lines("stage", 6<<10), Constraints: []string{lines("constraint", 6<<10)}}
	budgeted = apply(t, envelope, Budgets{Envelope: 12 << 10}, nil)
	if budgeted.Objective != envelope.Objective || EnvelopeBytes(budgeted) > 12<<10 {
		t.Fatalf("objective cut: %v, envelope %d bytes", budgeted.Objective != envelope.Objective, EnvelopeBytes(budgeted))
	}
	if stage, kept := len(budgeted.StagePrompt), listBytes(budgeted.Constraints); stage >= len(envelope.StagePrompt) || kept > MinBudget || stage <= kept {
		t.Fatalf("stage prompt %d bytes, constraints %d", stage, kept)
	}
}

func TestAnEnvelopeThatCannotFitIsRefused(t *testing.T) {
	envelope := ContextEnvelope{Objective: lines("objective", 5<<10), AcceptanceCriteria: []string{lines("criterion", 3<<10)}}
	if _, err := ApplyBudgets(envelope, Budgets{Envelope: MinBudget}, nil); !errors.Is(err, ErrOverBudget) || !strings.Contains(err.Error(), "budgets.envelope") {
		t.Fatalf("an envelope over its budget at every floor must be refused, got %v", err)
	}
	// What cannot be cut counts too.
	paths := ContextEnvelope{Objective: "Go", AllowedPaths: []string{strings.Repeat("p", 4<<10)}}
	if _, err := ApplyBudgets(paths, Budgets{Envelope: 2 << 10}, nil); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("allowed paths over the budget must be refused, got %v", err)
	}
	if size := EnvelopeBytes(apply(t, envelope, Budgets{Envelope: 3 << 10}, nil)); size > 3<<10 {
		t.Fatalf("an envelope that can fit must: %d bytes", size)
	}
}

func TestThePreviousTurnsSummaryIsHeldToItsBudget(t *testing.T) {
	digest := &TurnDigest{Turn: 1, StoppedFor: "a decision from the user", Summary: strings.Repeat("Read a handler and noted what it does.\n", 400), Changes: []string{"notes.md"}}
	envelope := ContextEnvelope{Objective: "Add a flag", PreviousTurn: digest}
	cut, err := ApplyBudgets(envelope, Budgets{PreviousTurn: 2048}, func(part, text string) string { return "inkuna://artifact/" + part })
	if err != nil {
		t.Fatal(err)
	}
	summary := cut.PreviousTurn.Summary
	if len(summary) > 2048 || !strings.HasSuffix(summary, "; full text in inkuna://artifact/previousTurn]") || cut.Metadata[MetadataTruncated] != SectionPreviousTurn {
		t.Fatalf("summary of %d bytes, truncated %q: %q", len(summary), cut.Metadata[MetadataTruncated], summary[max(0, len(summary)-120):])
	}
	if cut.PreviousTurn.StoppedFor != digest.StoppedFor || strings.Join(cut.PreviousTurn.Changes, ",") != "notes.md" || len(digest.Summary) == len(summary) {
		t.Fatalf("only the summary is cut, and never the caller's: %+v", cut.PreviousTurn)
	}

	// Over the envelope's budget, the digest gives way before the user's
	// answers do.
	envelope.HumanResponses = []HumanResponse{{Question: "Which database?", Answer: strings.Repeat("sqlite ", 1000)}}
	cut, err = ApplyBudgets(envelope, Budgets{Envelope: 8 << 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cut.PreviousTurn.Summary) >= len(digest.Summary) || EnvelopeBytes(cut) > 8<<10 || cut.HumanResponses[0].Answer != envelope.HumanResponses[0].Answer {
		t.Fatalf("summary %d bytes in an envelope of %d, answer cut: %v", len(cut.PreviousTurn.Summary), EnvelopeBytes(cut), cut.HumanResponses[0].Answer != envelope.HumanResponses[0].Answer)
	}
}

func TestATurnDigestListsAtMostMaxDigestChanges(t *testing.T) {
	handoff := HandoffResult{Summary: "  Changed many files  "}
	for index := 0; index < MaxDigestChanges+7; index++ {
		handoff.Changes = append(handoff.Changes, Change{Path: fmt.Sprintf("file%02d.go", index)})
	}
	handoff.Changes = append([]Change{{Path: strings.Repeat("deep/", 200) + "file.go"}, {Path: " "}}, handoff.Changes...)
	digest := NewTurnDigest(2, "replies from reviewer", handoff)
	if digest.Turn != 2 || digest.Summary != "Changed many files" || len(digest.Changes) != MaxDigestChanges || digest.MoreChanges != 8 || digest.Changes[1] != "file00.go" {
		t.Fatalf("digest = %+v", digest)
	}
	if long := digest.Changes[0]; len(long) > MaxDescriptionBytes || !strings.HasSuffix(long, "…") {
		t.Fatalf("a long path must be held to %d bytes: %d", MaxDescriptionBytes, len(long))
	}
}
