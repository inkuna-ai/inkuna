package diff

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// missingBlob reports the all-zero object name git gives the missing side
// of an added or deleted file: 40 zeros, or 64 in a SHA-256 repository.
func missingBlob(name string) bool { return strings.Trim(name, "0") == "" }

// entry is one file of `git diff --raw -z`, with the blobs its patch and
// highlighting are read from.
type entry struct {
	file             File
	oldBlob, newBlob string
}

// parseRaw reads `git diff --raw -z --no-abbrev`: for each file a
// ":oldmode newmode oldblob newblob status" record, then its path, and for
// a rename or copy the old path before the new one.
func parseRaw(data []byte) ([]entry, error) {
	fields := splitZ(data)
	entries := make([]entry, 0, len(fields)/2)
	for index := 0; index < len(fields); {
		record := fields[index]
		parts := strings.Fields(strings.TrimPrefix(record, ":"))
		if !strings.HasPrefix(record, ":") || len(parts) != 5 || index+1 >= len(fields) {
			return nil, fmt.Errorf("parse git diff --raw: unexpected record %q", record)
		}
		file := File{OldMode: mode(parts[0]), NewMode: mode(parts[1])}
		status := parts[4]
		file.Path = fields[index+1]
		index += 2
		switch status[0] {
		case 'A':
			file.Status = StatusAdded
		case 'D':
			file.Status = StatusDeleted
		case 'M', 'T':
			file.Status = StatusModified
		case 'R', 'C':
			if index >= len(fields) {
				return nil, fmt.Errorf("parse git diff --raw: %s %q has no new path", status, file.Path)
			}
			file.Status = StatusRenamed
			if status[0] == 'C' {
				file.Status = StatusCopied
			}
			file.OldPath, file.Path = file.Path, fields[index]
			index++
			file.Similarity, _ = strconv.Atoi(status[1:])
		default:
			return nil, fmt.Errorf("parse git diff --raw: unknown status %q for %q", status, file.Path)
		}
		file.Blob = parts[3]
		if missingBlob(file.Blob) {
			file.Blob = parts[2]
		}
		entries = append(entries, entry{file: file, oldBlob: parts[2], newBlob: parts[3]})
	}
	return entries, nil
}

// mode drops the all-zero mode git gives the missing side of a file.
func mode(value string) string {
	if strings.Trim(value, "0") == "" {
		return ""
	}
	return value
}

// counts is one file's line counts from --numstat.
type counts struct {
	additions, deletions int
	binary               bool
}

// parseNumstat reads `git diff --numstat -z`, keyed by the file's new path:
// "added\tdeleted\tpath", or "added\tdeleted\t" then the old and new paths
// for a rename or copy. A binary file counts "-" for both.
func parseNumstat(data []byte) (map[string]counts, error) {
	fields := splitZ(data)
	result := make(map[string]counts, len(fields))
	for index := 0; index < len(fields); index++ {
		parts := strings.SplitN(fields[index], "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("parse git diff --numstat: unexpected record %q", fields[index])
		}
		path := parts[2]
		if path == "" {
			if index+2 >= len(fields) {
				return nil, fmt.Errorf("parse git diff --numstat: rename %q has no paths", fields[index])
			}
			path = fields[index+2]
			index += 2
		}
		var count counts
		if parts[0] == "-" && parts[1] == "-" {
			count.binary = true
		} else {
			var addErr, delErr error
			count.additions, addErr = strconv.Atoi(parts[0])
			count.deletions, delErr = strconv.Atoi(parts[1])
			if addErr != nil || delErr != nil {
				return nil, fmt.Errorf("parse git diff --numstat: unexpected counts %q", fields[index])
			}
		}
		result[path] = count
	}
	return result, nil
}

// splitZ splits NUL-terminated output, dropping the empty field after the
// last terminator.
func splitZ(data []byte) []string {
	data = bytes.TrimSuffix(data, []byte{0})
	if len(data) == 0 {
		return nil
	}
	return strings.Split(string(data), "\x00")
}

// section is one file of a unified patch.
type section struct {
	oldPath, newPath string
	binary           bool
	hunks            []Hunk
}

// path is the section's file as the listing names it: the new path, or the
// old one for a deleted file.
func (section section) path() string {
	if section.newPath != "" {
		return section.newPath
	}
	return section.oldPath
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

// parsePatch reads the unified patch git diff writes with prefixes a/ and
// b/: one section per file, each a header and its hunks. Paths come from
// the --- and +++ lines, the rename and copy lines, or for a change with
// neither, such as a mode change, from the diff --git line.
func parsePatch(data []byte) ([]section, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var sections []section
	var current *section
	var hunk *Hunk
	oldLeft, newLeft, oldLine, newLine := 0, 0, 0, 0
	finishHunk := func() {
		if current != nil && hunk != nil {
			current.hunks = append(current.hunks, *hunk)
		}
		hunk = nil
	}
	for number, line := range lines {
		if hunk != nil && (oldLeft > 0 || newLeft > 0) {
			body, crlf := strings.CutSuffix(line, "\r")
			// An empty line is an empty context line written without its
			// leading space.
			kind, text := byte(' '), ""
			if body != "" {
				kind, text = body[0], body[1:]
			}
			switch kind {
			case ' ':
				hunk.Lines = append(hunk.Lines, Line{Kind: LineContext, Old: oldLine, New: newLine, Text: text, CRLF: crlf})
				oldLine, newLine, oldLeft, newLeft = oldLine+1, newLine+1, oldLeft-1, newLeft-1
			case '-':
				hunk.Lines = append(hunk.Lines, Line{Kind: LineDeleted, Old: oldLine, Text: text, CRLF: crlf})
				oldLine, oldLeft = oldLine+1, oldLeft-1
			case '+':
				hunk.Lines = append(hunk.Lines, Line{Kind: LineAdded, New: newLine, Text: text, CRLF: crlf})
				newLine, newLeft = newLine+1, newLeft-1
			case '\\':
				markNoNewline(hunk)
			default:
				return nil, fmt.Errorf("parse patch: line %d: unexpected hunk line %q", number+1, line)
			}
			continue
		}
		if strings.HasPrefix(line, `\`) && hunk != nil {
			// "\ No newline at end of file" after a hunk's last line.
			markNoNewline(hunk)
			continue
		}
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			finishHunk()
			if current != nil {
				sections = append(sections, *current)
			}
			oldPath, newPath, err := gitLinePaths(rest)
			if err != nil {
				return nil, fmt.Errorf("parse patch: line %d: %w", number+1, err)
			}
			current = &section{oldPath: oldPath, newPath: newPath}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("parse patch: line %d: %q before any diff --git line", number+1, line)
		}
		if match := hunkHeader.FindStringSubmatch(line); match != nil {
			finishHunk()
			// The section comes from the file's own text, so a CRLF file
			// leaves a carriage return on it.
			hunk = &Hunk{Header: strings.TrimSuffix(line, "\r"), Section: strings.TrimSuffix(match[5], "\r"), Lines: []Line{}}
			hunk.OldStart, hunk.OldLines = atoi(match[1]), count(match[2])
			hunk.NewStart, hunk.NewLines = atoi(match[3]), count(match[4])
			oldLeft, newLeft, oldLine, newLine = hunk.OldLines, hunk.NewLines, hunk.OldStart, hunk.NewStart
			continue
		}
		if hunk != nil {
			// After a hunk only another hunk or file may follow; anything else
			// means the counts and the lines disagree.
			return nil, fmt.Errorf("parse patch: line %d: %q after a hunk's last line", number+1, line)
		}
		if err := current.header(line); err != nil {
			return nil, fmt.Errorf("parse patch: line %d: %w", number+1, err)
		}
	}
	finishHunk()
	if current != nil {
		sections = append(sections, *current)
	}
	return sections, nil
}

// header reads one line of a section's extended header.
func (section *section) header(line string) error {
	var err error
	switch {
	case strings.HasPrefix(line, "--- "):
		section.oldPath, err = patchPath(strings.TrimPrefix(line, "--- "), "a/")
	case strings.HasPrefix(line, "+++ "):
		section.newPath, err = patchPath(strings.TrimPrefix(line, "+++ "), "b/")
	case strings.HasPrefix(line, "rename from "), strings.HasPrefix(line, "copy from "):
		section.oldPath, err = unquote(line[strings.Index(line, " from ")+len(" from "):])
	case strings.HasPrefix(line, "rename to "), strings.HasPrefix(line, "copy to "):
		section.newPath, err = unquote(line[strings.Index(line, " to ")+len(" to "):])
	case strings.HasPrefix(line, "Binary files "):
		section.binary = true
	case strings.HasPrefix(line, "new file mode "):
		section.oldPath = ""
	case strings.HasPrefix(line, "deleted file mode "):
		section.newPath = ""
	}
	return err
}

// markNoNewline marks a hunk's last line as having no newline at its end.
func markNoNewline(hunk *Hunk) {
	if len(hunk.Lines) > 0 {
		hunk.Lines[len(hunk.Lines)-1].NoNewline = true
	}
}

// patchPath reads a --- or +++ path: /dev/null for the missing side, or the
// prefixed path, quoted when it holds characters git escapes. Git ends the
// line with a tab when the path has a space, so patch(1) can tell where it
// stops; a tab inside a path is always quoted.
func patchPath(value, prefix string) (string, error) {
	value = strings.TrimSuffix(value, "\t")
	if value == "/dev/null" {
		return "", nil
	}
	path, err := unquote(value)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(path, prefix) {
		return "", fmt.Errorf("path %q lacks the %s prefix", value, prefix)
	}
	return strings.TrimPrefix(path, prefix), nil
}

// gitLinePaths reads "a/<old> b/<new>" from a diff --git line. Unquoted
// paths may hold spaces, so they are split where the two halves name the
// same file, which they do whenever the patch has no rename lines to say
// otherwise.
func gitLinePaths(rest string) (string, string, error) {
	if strings.HasPrefix(rest, `"`) {
		end := closingQuote(rest)
		if end < 0 || end+2 > len(rest) {
			return "", "", fmt.Errorf("unterminated path in diff --git %q", rest)
		}
		oldPath, err := patchPath(rest[:end+1], "a/")
		if err != nil {
			return "", "", err
		}
		newPath, err := patchPath(rest[end+2:], "b/")
		return oldPath, newPath, err
	}
	half := (len(rest) - len(" ")) / 2
	if len(rest)%2 == 1 && strings.HasPrefix(rest, "a/") && rest[half:half+3] == " b/" && rest[2:half] == rest[half+3:] {
		return rest[2:half], rest[half+3:], nil
	}
	// Different paths without rename lines can only be a rename git wrote
	// in full below; the header lines fill them in.
	return "", "", nil
}

// closingQuote is the index of the quote that ends the quoted string at the
// start of value, or -1.
func closingQuote(value string) int {
	for index := 1; index < len(value); index++ {
		switch value[index] {
		case '\\':
			index++
		case '"':
			return index
		}
	}
	return -1
}

// unquote reads a path git may have written in C quotes; git's escapes are
// a subset of Go's.
func unquote(value string) (string, error) {
	if !strings.HasPrefix(value, `"`) {
		return value, nil
	}
	path, err := strconv.Unquote(value)
	if err != nil {
		return "", fmt.Errorf("unquote path %s: %w", value, err)
	}
	return path, nil
}

func atoi(value string) int {
	number, _ := strconv.Atoi(value)
	return number
}

// count reads a hunk's line count, which git leaves out when it is one.
func count(value string) int {
	if value == "" {
		return 1
	}
	return atoi(value)
}
