package diff

import (
	"reflect"
	"strings"
	"testing"
)

func TestWordChangesMarkOnlyTheWordsThatDiffer(t *testing.T) {
	budget := maxPatchWordCells
	old, updated := "total := price * quantity", "total := price * count"
	oldChanged, newChanged, ok := wordChanges(old, updated, &budget)
	if !ok || cut(old, oldChanged) != "quantity" || cut(updated, newChanged) != "count" {
		t.Fatalf("changes %v %v %v", oldChanged, newChanged, ok)
	}
	if _, _, ok := wordChanges("func Hello() {", "return errors.New(\"nothing alike\")", &budget); ok {
		t.Fatal("lines that are mostly different get no marks")
	}
	if oldChanged, newChanged, ok := wordChanges("same", "same", &budget); !ok || oldChanged != nil || newChanged != nil {
		t.Fatalf("identical lines: %v %v %v", oldChanged, newChanged, ok)
	}
}

func TestPairChangesPairsDeletedAndAddedLinesInOrder(t *testing.T) {
	lines := []Line{
		{Kind: LineContext, Text: "keep"},
		{Kind: LineDeleted, Text: "keep the first apple here"},
		{Kind: LineDeleted, Text: "keep the second apples here"},
		{Kind: LineAdded, Text: "keep the first pear here"},
		{Kind: LineAdded, Text: "keep the second pears here"},
		{Kind: LineAdded, Text: "a third line with nothing to pair"},
	}
	budget := maxPatchWordCells
	changes := pairChanges(lines, &budget)
	got := map[int]string{}
	for index, ranges := range changes {
		got[index] = cut(lines[index].Text, ranges)
	}
	want := map[int]string{1: "apple", 2: "apples", 3: "pear", 4: "pears"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes %v, want %v", got, want)
	}
}

func TestAPatchStopsMarkingWordsOnceItsBudgetIsSpent(t *testing.T) {
	hunk := []Line{
		{Kind: LineDeleted, Text: "keep the first apple here"},
		{Kind: LineAdded, Text: "keep the first pear here"},
	}
	budget := len(words(hunk[0].Text)) * len(words(hunk[1].Text))
	if changes := pairChanges(hunk, &budget); len(changes) != 2 || budget != 0 {
		t.Fatalf("within the budget: %v, %d left", changes, budget)
	}
	// The next hunk of the same patch draws on what the first left.
	if changes := pairChanges(hunk, &budget); len(changes) != 0 {
		t.Fatalf("past the budget: %v", changes)
	}
	// One pair over its own cap gets no marks, however much budget is left.
	budget = maxPatchWordCells
	old := strings.Repeat("a ", 260)
	if _, _, ok := wordChanges(old, old+"b", &budget); ok || budget != maxPatchWordCells {
		t.Fatalf("a pair over its own cap was compared, %d left", budget)
	}
}

func TestWordsKeepMultibyteCharactersWhole(t *testing.T) {
	var parts []string
	for _, word := range words("café → naïve") {
		parts = append(parts, word.text("café → naïve"))
	}
	if strings.Join(parts, "|") != "café| |→| |naïve" {
		t.Fatalf("words %q", parts)
	}
}

func cut(line string, ranges []span) string {
	var text strings.Builder
	for _, current := range ranges {
		text.WriteString(line[current.start:current.end])
	}
	return text.String()
}
