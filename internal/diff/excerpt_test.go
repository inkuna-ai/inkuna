package diff

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// numbered is a Go file of a package line and one var per line after it.
func numbered(lines int) string {
	var text strings.Builder
	text.WriteString("package big\n")
	for number := 2; number <= lines; number++ {
		fmt.Fprintf(&text, "var v%d = %d\n", number, number)
	}
	return text.String()
}

func TestAnExcerptReadsTheLinesBetweenHunksNumberedInBothVersions(t *testing.T) {
	repo := newRepo(t)
	original := numbered(50)
	base := repo.commit("base", map[string]string{"big.go": original})
	changed := strings.Replace(original, "var v10 = 10\n", "var v10 = 100\n", 1)
	changed = strings.Replace(changed, "var v40 = 40\n", "var v40 = 40\nvar extra1 = 1\nvar extra2 = 2\n", 1)
	tip := repo.commit("work", map[string]string{"big.go": changed})
	ctx := context.Background()
	between := Range{From: base, To: tip}

	patch, err := Patch(ctx, repo.path, between, Review{}, "big.go", false)
	if err != nil {
		t.Fatal(err)
	}
	if patch.NewTotal != 52 || len(patch.Hunks) != 2 {
		t.Fatalf("patch has %d lines in its newer version and %d hunks", patch.NewTotal, len(patch.Hunks))
	}

	// Between the hunks, old and new numbers are the same.
	excerpt, err := Excerpt(ctx, repo.path, between, "big.go", 14, 14, 3)
	if err != nil {
		t.Fatal(err)
	}
	if excerpt.NewTotal != 52 || len(excerpt.Lines) != 3 {
		t.Fatalf("excerpt %+v", excerpt)
	}
	first := excerpt.Lines[0]
	if first.Kind != LineContext || first.Old != 14 || first.New != 14 || first.Text != "var v14 = 14" {
		t.Fatalf("first line %+v", first)
	}
	if len(first.Spans) == 0 || first.Spans[0].Class != "tok-keyword" {
		t.Fatalf("an excerpt is highlighted like its patch: %+v", first.Spans)
	}

	// Below the insertion the new numbers run two ahead, and asking past the
	// end of the file returns only what is there.
	tail, err := Excerpt(ctx, repo.path, between, "big.go", 44, 46, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail.Lines) != 7 || tail.Lines[0].Old != 44 || tail.Lines[0].Text != "var v44 = 44" || tail.Lines[6].New != 52 || tail.Lines[6].NoNewline {
		t.Fatalf("tail %+v", tail.Lines)
	}
}

func TestAnExcerptKeepsLineEndingsAsItsPatchDoes(t *testing.T) {
	repo := newRepo(t)
	base := repo.commit("base", map[string]string{"notes.txt": "a\r\nb\r\nc"})
	tip := repo.commit("work", map[string]string{"notes.txt": "A\r\nb\r\nc"})
	excerpt, err := Excerpt(context.Background(), repo.path, Range{From: base, To: tip}, "notes.txt", 2, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{
		{Kind: LineContext, Old: 2, New: 2, Text: "b", CRLF: true},
		{Kind: LineContext, Old: 3, New: 3, Text: "c", NoNewline: true},
	}
	if fmt.Sprint(excerpt.Lines) != fmt.Sprint(want) || excerpt.NewTotal != 3 {
		t.Fatalf("excerpt %+v", excerpt)
	}
}

func TestAnExcerptRefusesWhatItCannotRead(t *testing.T) {
	repo := newRepo(t)
	base := repo.commit("base", map[string]string{"kept.txt": "a\nb\n", "same.txt": "same\n"})
	tip := repo.commit("work", map[string]string{"kept.txt": "a\nB\n", "added.txt": "new\n"})
	ctx := context.Background()
	between := Range{From: base, To: tip}
	for name, read := range map[string]func() error{
		"no lines": func() error { _, err := Excerpt(ctx, repo.path, between, "kept.txt", 1, 1, 0); return err },
		"too many": func() error {
			_, err := Excerpt(ctx, repo.path, between, "kept.txt", 1, 1, maxExcerptLines+1)
			return err
		},
		"line zero":    func() error { _, err := Excerpt(ctx, repo.path, between, "kept.txt", 0, 1, 1); return err },
		"unchanged":    func() error { _, err := Excerpt(ctx, repo.path, between, "same.txt", 1, 1, 1); return err },
		"an added one": func() error { _, err := Excerpt(ctx, repo.path, between, "added.txt", 1, 1, 1); return err },
	} {
		if err := read(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
