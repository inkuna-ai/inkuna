package domain

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxTitleRunes bounds a task's title, which names its runs and its branch,
// so it reads at a glance. DerivedTitleRunes is the shorter cut a title
// derived from a task's details takes.
const (
	MaxTitleRunes     = 100
	DerivedTitleRunes = 72
)

// titleQuotes are the quotes a model may wrap a title in.
const titleQuotes = "\"'`“”‘’"

// CleanTitle puts a title on one line, trimmed, and refuses one that is
// blank or longer than MaxTitleRunes. Quotes a model wraps a whole title in
// go, and so do full stops at its end. Each pass can uncover more, as a full
// stop outside closing quotes does, so it repeats until nothing changes: a
// clean title cleans to itself, which is what ValidateTask holds a title to.
func CleanTitle(title string) (string, error) {
	for {
		next := strings.TrimSpace(strings.TrimRight(strings.Join(strings.Fields(title), " "), "."))
		if first, size := utf8.DecodeRuneInString(next); size > 0 && size < len(next) && strings.ContainsRune(titleQuotes, first) {
			if last, end := utf8.DecodeLastRuneInString(next); strings.ContainsRune(titleQuotes, last) {
				next = strings.TrimSpace(next[size : len(next)-end])
			}
		}
		if next == title {
			break
		}
		title = next
	}
	switch count := utf8.RuneCountInString(title); {
	case count == 0:
		return "", fmt.Errorf("%w: the title is blank", ErrInvalid)
	case count > MaxTitleRunes:
		return "", fmt.Errorf("%w: the title is %d characters; at most %d", ErrInvalid, count, MaxTitleRunes)
	}
	return title, nil
}

// listMarker is what starts a Markdown heading, quote, or list item.
var listMarker = regexp.MustCompile(`^(?:#{1,6}\s+|>\s*|[-*+]\s+|\d+[.)]\s+)`)

// DeriveTitle makes a title from a task's details when nothing better
// wrote one: their first line with text, without a Markdown marker, cut at a
// word boundary to DerivedTitleRunes with an ellipsis.
func DeriveTitle(details string) string {
	for _, line := range strings.Split(details, "\n") {
		line = strings.TrimSpace(listMarker.ReplaceAllString(strings.TrimSpace(line), ""))
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) <= DerivedTitleRunes {
			return strings.TrimSuffix(line, ".")
		}
		cut := string(runes[:DerivedTitleRunes-1])
		if space := strings.LastIndex(cut, " "); space >= len(cut)/2 {
			cut = cut[:space]
		}
		return strings.TrimRight(cut, " ,;:-") + "…"
	}
	return ""
}

// DisplayTitle is the task's title, or for a task from before titles one
// derived from its objective.
func (task TaskSpec) DisplayTitle() string {
	if task.Title != "" {
		return task.Title
	}
	return DeriveTitle(task.Objective)
}
