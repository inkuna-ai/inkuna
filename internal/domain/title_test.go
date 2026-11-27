package domain

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanTitlePutsATitleOnOneTrimmedLine(t *testing.T) {
	for raw, want := range map[string]string{
		"  Add rate limiting  ":            "Add rate limiting",
		"Add rate\n  limiting":             "Add rate limiting",
		`"Add rate limiting."`:             "Add rate limiting",
		"“Fix the empty-name crash”":       "Fix the empty-name crash",
		"`Rename the flag`":                "Rename the flag",
		"Rename the flag..":                "Rename the flag",
		`"Rename the flag".`:               "Rename the flag",
		"`go test` fails on main":          "`go test` fails on main",
		strings.Repeat("é", MaxTitleRunes): strings.Repeat("é", MaxTitleRunes),
	} {
		if got, err := CleanTitle(raw); err != nil || got != want {
			t.Errorf("CleanTitle(%q) = %q, %v; want %q", raw, got, err, want)
		}
		// A clean title cleans to itself, as ValidateTask requires.
		if again, err := CleanTitle(want); err != nil || again != want {
			t.Errorf("CleanTitle(%q) = %q, %v; a clean title must stay as it is", want, again, err)
		}
	}
	for _, raw := range []string{"", "   \n ", `""`, ".", strings.Repeat("a", MaxTitleRunes+1)} {
		if _, err := CleanTitle(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("CleanTitle(%q) took it: %v", raw, err)
		}
	}
}

func TestDeriveTitleTakesTheFirstLineCutAtAWord(t *testing.T) {
	for details, want := range map[string]string{
		"Add rate limiting.\n\nEvery key gets 100 a minute.": "Add rate limiting",
		"\n\n## Rate limiting\nDetails":                      "Rate limiting",
		"- Fix the crash\n- Add a test":                      "Fix the crash",
		"1. Rename the flag":                                 "Rename the flag",
		"> Quoted   request with   spaces":                   "Quoted request with spaces",
		"":                                                   "",
	} {
		if got := DeriveTitle(details); got != want {
			t.Errorf("DeriveTitle(%q) = %q, want %q", details, got, want)
		}
	}
	long := "In greet.go, add a function Farewell(name string) string that returns Goodbye plus the name, and a Shout function"
	got := DeriveTitle(long)
	if !strings.HasSuffix(got, "…") || utf8.RuneCountInString(got) > DerivedTitleRunes || !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) || strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") {
		t.Fatalf("a long first line is cut at a word with an ellipsis: %q", got)
	}
	if _, err := CleanTitle(got); err != nil {
		t.Fatalf("a derived title is a valid title: %v", err)
	}
}

func TestATaskKeepsATitleOnlyWhenItIsClean(t *testing.T) {
	task := TaskSpec{Version: ContractVersion, ID: "task-1", Kind: TaskImplementation, Objective: "Details", Budget: Budget{WallTimeSeconds: 60}}
	if err := ValidateTask(task); err != nil {
		t.Fatalf("a task from before titles is valid: %v", err)
	}
	task.Title = "Add rate limiting"
	if err := ValidateTask(task); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{" Add rate limiting", "Add\nrate limiting", strings.Repeat("a", MaxTitleRunes+1)} {
		task.Title = title
		if err := ValidateTask(task); !errors.Is(err, ErrInvalid) {
			t.Errorf("title %q was kept: %v", title, err)
		}
	}
	if (TaskSpec{Objective: "Rename the flag.\nMore"}).DisplayTitle() != "Rename the flag" || (TaskSpec{Title: "Given", Objective: "Other"}).DisplayTitle() != "Given" {
		t.Fatal("DisplayTitle is the title, or one derived from the objective")
	}
}
