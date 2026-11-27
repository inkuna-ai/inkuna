// Package frontmatter splits a Markdown file into its YAML header and body.
// Agents, skills, and docs share it so every file kind accepts the same
// shape: a line holding "---", YAML lines, a closing "---", then the body.
package frontmatter

import (
	"errors"
	"strings"
)

// ErrMissing is returned when the content does not begin with "---".
var ErrMissing = errors.New("file must begin with YAML frontmatter")

// ErrUnclosed is returned when the opening "---" has no closing line.
var ErrUnclosed = errors.New("frontmatter is not closed")

// Split separates the YAML header from the Markdown body using offsets into
// the original content so LF and CRLF files behave the same. The header is
// returned with LF endings and the body untouched from the line after the
// closing delimiter.
func Split(content string) (header, body string, err error) {
	nextLine := func(position int) (string, int, bool) {
		if position >= len(content) {
			return "", position, false
		}
		end := strings.IndexByte(content[position:], '\n')
		if end < 0 {
			return content[position:], len(content), true
		}
		return content[position : position+end], position + end + 1, true
	}
	line, position, ok := nextLine(0)
	if !ok || strings.TrimSpace(line) != "---" {
		return "", "", ErrMissing
	}
	var builder strings.Builder
	for {
		line, next, ok := nextLine(position)
		if !ok {
			return "", "", ErrUnclosed
		}
		if strings.TrimSpace(line) == "---" {
			return builder.String(), content[next:], nil
		}
		builder.WriteString(strings.TrimRight(line, "\r"))
		builder.WriteByte('\n')
		position = next
	}
}
