package diff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// maxExcerptLines bounds one excerpt. A view expands context a few lines at
// a time, so a request for more than this is a mistake rather than a need.
const maxExcerptLines = 1000

// FileExcerpt is unchanged lines of a file, read to show context its patch
// left out.
type FileExcerpt struct {
	Lines []Line `json:"lines"`
	// NewTotal is how many lines the newer version of the file has.
	NewTotal int `json:"newTotal"`
}

// Excerpt reads up to count lines of a file a range changed, from line
// newStart of its newer version, to show context the patch left out. Lines
// between hunks are the same in both versions, so each is numbered in the
// older one too, counting from oldStart; the view knows that offset from
// the hunks around the gap. Lines past the end of the file are left out.
func Excerpt(ctx context.Context, repository string, between Range, path string, oldStart, newStart, count int) (FileExcerpt, error) {
	if oldStart < 1 || newStart < 1 || count < 1 || count > maxExcerptLines {
		return FileExcerpt{}, fmt.Errorf("an excerpt starts at line 1 or later and has 1 to %d lines", maxExcerptLines)
	}
	entries, err := list(ctx, repository, between)
	if err != nil {
		return FileExcerpt{}, err
	}
	var found *entry
	for index := range entries {
		if entries[index].file.Path == path {
			found = &entries[index]
			break
		}
	}
	if found == nil {
		return FileExcerpt{}, fmt.Errorf("%s did not change between %s and %s", path, short(between.From), short(between.To))
	}
	if missingBlob(found.oldBlob) || missingBlob(found.newBlob) {
		return FileExcerpt{}, fmt.Errorf("%s was %s, so its patch already has every line", path, found.file.Status)
	}
	text, err := git(ctx, repository, nil, HardBytes, "cat-file", "blob", found.newBlob)
	if errors.Is(err, errOverLimit) {
		return FileExcerpt{}, fmt.Errorf("%s is over %d MB, too large to expand", path, HardBytes>>20)
	}
	if err != nil {
		return FileExcerpt{}, fmt.Errorf("read %s: %w", path, err)
	}
	if bytes.IndexByte(text, 0) >= 0 {
		return FileExcerpt{}, fmt.Errorf("%s is binary", path)
	}
	content := string(text)
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var highlighted [][]Span
	if lexer := lexerFor(path); lexer != nil && len(text) <= SoftBytes {
		highlighted = highlight(lexer, content)
	}
	excerpt := FileExcerpt{Lines: []Line{}, NewTotal: len(lines)}
	for number := newStart; number < newStart+count && number <= len(lines); number++ {
		raw := lines[number-1]
		line := Line{Kind: LineContext, Old: oldStart + number - newStart, New: number}
		line.NoNewline = !strings.HasSuffix(raw, "\n")
		raw = strings.TrimSuffix(raw, "\n")
		line.CRLF = strings.HasSuffix(raw, "\r")
		line.Text = strings.TrimSuffix(raw, "\r")
		line.Spans = lineSpans(line.Text, lineOf(highlighted, number), nil)
		excerpt.Lines = append(excerpt.Lines, line)
	}
	return excerpt, nil
}
