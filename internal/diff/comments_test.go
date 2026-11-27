package diff

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// lines is a file of the given lines, one per line.
func lines(values ...string) string { return strings.Join(values, "\n") + "\n" }

// counted is l1 to ln, with changes put in place of some of them.
func counted(n int, changes map[int]string) []string {
	values := make([]string, 0, n)
	for number := 1; number <= n; number++ {
		if change, ok := changes[number]; ok {
			if change != "" {
				values = append(values, strings.Split(change, "\n")...)
			}
			continue
		}
		values = append(values, fmt.Sprintf("l%d", number))
	}
	return values
}

// commentRepo has a base of l1 to l20, then on its branch a commit that
// changes l3, adds x after l7, and changes l18; then one that adds two
// lines at the top, changes the new L3, puts z between l5 and l6, and adds
// w at the end; then one that puts l3 back as the base had it.
type commentRepo struct {
	repo
	base, first, second, third string
}

func newCommentRepo(t *testing.T) commentRepo {
	result := commentRepo{repo: newRepo(t)}
	result.base = result.commit("base", map[string]string{"n.go": lines(counted(20, nil)...)})
	result.git("switch", "-q", "-c", "feature")
	result.first = result.commit("first", map[string]string{"n.go": lines(counted(20, map[int]string{3: "L3", 7: "l7\nx", 18: "L18"})...)})
	second := append([]string{"t1", "t2"}, counted(20, map[int]string{3: "L3'", 5: "l5\nz", 7: "l7\nx", 18: "L18", 20: "l20\nw"})...)
	result.second = result.commit("second", map[string]string{"n.go": lines(second...)})
	third := append([]string{"t1", "t2"}, counted(20, map[int]string{5: "l5\nz", 7: "l7\nx", 18: "L18", 20: "l20\nw"})...)
	result.third = result.commit("third", map[string]string{"n.go": lines(third...)})
	return result
}

func TestACommentFollowsItsLinesUntilTheyChange(t *testing.T) {
	repo := newCommentRepo(t)
	ctx := context.Background()
	at := func(line, end int) Anchor {
		return Anchor{Path: "n.go", Line: line, EndLine: end, Base: repo.base, Commit: repo.first}
	}
	removed := func(line int, base string) Anchor {
		return Anchor{Path: "n.go", Old: true, Line: line, EndLine: line, Base: base, Commit: repo.first}
	}
	anchors := []Anchor{
		at(9, 10),              // l8 and l9 move down past t1, t2, and z
		at(3, 3),               // L3 is changed to L3'
		at(5, 6),               // z goes between l5 and l6
		at(21, 21),             // w goes after l20, the last line
		removed(3, repo.base),  // l3 is still removed
		removed(4, repo.base),  // l4 never was
		removed(3, repo.first), // the branch's changes start elsewhere now
	}
	got, err := Place(ctx, repo.path, repo.base, repo.second, anchors)
	if err != nil {
		t.Fatal(err)
	}
	want := []Placement{
		{Line: 12, EndLine: 13},
		{Line: 3, EndLine: 3, Changed: true},
		{Line: 5, EndLine: 6, Changed: true},
		{Line: 24, EndLine: 24},
		{Line: 3, EndLine: 3},
		{Line: 4, EndLine: 4, Changed: true},
		{Line: 3, EndLine: 3, Changed: true},
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("anchor %d %+v placed at %+v, want %+v", index, anchors[index], got[index], want[index])
		}
	}

	// Once l3 is back, the comment on its removal is outdated.
	if got, err := Place(ctx, repo.path, repo.base, repo.third, []Anchor{removed(3, repo.base)}); err != nil || !got[0].Changed {
		t.Fatalf("a removed line put back: %+v %v", got, err)
	}
	// A version from before the comment cannot show it.
	later := Anchor{Path: "n.go", Line: 1, EndLine: 1, Base: repo.base, Commit: repo.second}
	if got, err := Place(ctx, repo.path, repo.base, repo.first, []Anchor{later}); err != nil || !got[0].Unknown {
		t.Fatalf("an older version: %+v %v", got, err)
	}
	// At the commit it was written on, a comment is where it was.
	if got, err := Place(ctx, repo.path, repo.base, repo.first, []Anchor{at(3, 3)}); err != nil || got[0] != (Placement{Line: 3, EndLine: 3}) {
		t.Fatalf("at its own commit: %+v %v", got, err)
	}
}

func TestACommentsExcerptMarksItsLinesInTheirHunk(t *testing.T) {
	repo := newCommentRepo(t)
	patch, err := Patch(context.Background(), repo.path, Range{From: repo.base, To: repo.first}, Review{}, "n.go", false)
	if err != nil {
		t.Fatal(err)
	}
	added, err := CommentExcerpt(patch, false, 3, 3)
	want := strings.Join([]string{
		"  1 1   l1",
		"  2 2   l2",
		"  3   - l3",
		">   3 + L3",
		"  4 4   l4",
		"  5 5   l5",
		"  6 6   l6",
	}, "\n")
	if err != nil || added != want {
		t.Fatalf("the excerpt of the branch's line 3, %v:\n%s\nwant:\n%s", err, added, want)
	}
	if removed, err := CommentExcerpt(patch, true, 3, 3); err != nil || !strings.Contains(removed, "\n> 3   - l3\n") {
		t.Fatalf("the excerpt of the removed line 3, %v:\n%s", err, removed)
	}
	for name, try := range map[string]struct {
		old       bool
		line, end int
	}{
		"a range across hunks":           {false, 3, 19},
		"an unchanged line as removed":   {true, 4, 4},
		"a line past the diff":           {false, 30, 30},
		"a range that ends before":       {false, 5, 4},
		"a removed range over unchanged": {true, 3, 4},
	} {
		if _, err := CommentExcerpt(patch, try.old, try.line, try.end); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
}

func TestALongCommentRangeKeepsItsEndsAndCutsLongLines(t *testing.T) {
	hunk := Hunk{}
	for number := 1; number <= 40; number++ {
		text := fmt.Sprintf("added %d", number)
		if number == 2 {
			text = strings.Repeat("é", 200)
		}
		hunk.Lines = append(hunk.Lines, Line{Kind: LineAdded, New: number, Text: text})
	}
	excerpt, err := CommentExcerpt(FilePatch{File: File{Path: "long.go"}, Hunks: []Hunk{hunk}}, false, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{">     1 + added 1\n", ">    10 + added 10\n", "⋯ 20 more lines\n", ">    31 + added 31\n", ">    40 + added 40"} {
		if !strings.Contains(excerpt, part) {
			t.Errorf("the excerpt lacks %q:\n%s", part, excerpt)
		}
	}
	if strings.Contains(excerpt, "added 11\n") || strings.Contains(excerpt, "added 30\n") {
		t.Errorf("the middle of the range was kept:\n%s", excerpt)
	}
	second := strings.Split(excerpt, "\n")[1]
	if !strings.HasSuffix(second, "…") || len(second) > excerptLineBytes+32 || strings.ContainsRune(second, '\uFFFD') {
		t.Errorf("a long line was not cut whole characters short: %q", second)
	}
}

func TestLastChangeNamesTheCommitThatWroteALine(t *testing.T) {
	repo := newCommentRepo(t)
	ctx := context.Background()
	between := Range{From: repo.base, To: repo.third}
	for name, try := range map[string]struct {
		old  bool
		line int
		want string
	}{
		"a line the first commit wrote":  {false, 22, repo.first}, // L18
		"a line the second commit wrote": {false, 1, repo.second}, // t1
		"a line the base had":            {false, 3, repo.base},   // l1
		"a removed line":                 {true, 18, repo.third},  // the last commit that changed the file
	} {
		got, err := LastChange(ctx, repo.path, between, "n.go", try.old, try.line)
		if err != nil || got != try.want {
			t.Errorf("%s: %s %v, want %s", name, got, err, try.want)
		}
	}
}
