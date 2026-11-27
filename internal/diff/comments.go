package diff

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Anchor is where a comment was written on a branch's changes: lines Line
// to EndLine of Path. Lines of the branch's version are numbered in Commit,
// the branch's tip then; Old lines, which the branch removed, are numbered
// in Base, where its changes start.
type Anchor struct {
	Path    string
	Old     bool
	Line    int
	EndLine int
	Base    string
	Commit  string
}

// Placement is where an anchor's lines are in a later version of the
// branch's changes. Changed says they changed or are gone, so a comment on
// them is outdated. Unknown says that version does not descend from the
// anchor's commit, so its lines cannot be followed there.
type Placement struct {
	Line    int
	EndLine int
	Changed bool
	Unknown bool
}

// Place follows anchors to the branch's changes from base to to. Lines of
// the branch's version move with the lines added or removed above them, and
// change when a commit after the anchor's touched any of them, or added a
// line between them. Old lines are numbered in base, so they never move;
// they change when the branch no longer removes every one of them, or its
// changes now start from another base.
func Place(ctx context.Context, repository, base, to string, anchors []Anchor) ([]Placement, error) {
	placements := make([]Placement, len(anchors))
	byCommit := map[string][]int{}
	var old []int
	for index, anchor := range anchors {
		placements[index] = Placement{Line: anchor.Line, EndLine: anchor.EndLine}
		switch {
		case anchor.Old && anchor.Base != base:
			placements[index].Changed = true
		case anchor.Old:
			old = append(old, index)
		case anchor.Commit != to:
			byCommit[anchor.Commit] = append(byCommit[anchor.Commit], index)
		}
	}
	for commit, indexes := range byCommit {
		descends, err := gitStatus(ctx, repository, "merge-base", "--is-ancestor", commit, to)
		if err != nil || !descends {
			for _, index := range indexes {
				placements[index].Unknown = true
			}
			continue
		}
		paths := make([]string, 0, len(indexes))
		for _, index := range indexes {
			paths = append(paths, anchors[index].Path)
		}
		// A file renamed since is a deleted file here, so a comment on it is
		// outdated rather than guessed onto the new name.
		hunks, err := zeroContext(ctx, repository, commit, to, []string{"--no-renames"}, paths)
		if err != nil {
			return nil, err
		}
		for _, index := range indexes {
			placements[index] = follow(hunks[anchors[index].Path], anchors[index].Line, anchors[index].EndLine)
		}
	}
	if len(old) == 0 {
		return placements, nil
	}
	entries, err := list(ctx, repository, Range{From: base, To: to})
	if err != nil {
		return nil, err
	}
	for _, index := range old {
		anchor := anchors[index]
		found, err := lookup(entries, anchor.Path, Range{From: base, To: to})
		if err != nil {
			// The branch no longer changes the file, so nothing of it is removed.
			placements[index].Changed = true
			continue
		}
		flags, paths := diffFlags, []string{anchor.Path}
		if found.file.OldPath != "" {
			flags = append(append([]string{}, diffFlags...), "--diff-filter=CR")
			paths = []string{found.file.OldPath, anchor.Path}
		}
		hunks, err := zeroContext(ctx, repository, base, to, flags, paths)
		if err != nil {
			return nil, err
		}
		placements[index].Changed = !removes(hunks[anchor.Path], anchor.Line, anchor.EndLine)
	}
	return placements, nil
}

// zeroContext is each file's hunks between two commits without context
// lines, so every older line in a hunk is one that changed.
func zeroContext(ctx context.Context, repository, from, to string, flags, paths []string) (map[string][]Hunk, error) {
	args := append(append(append([]string{"diff", "-U0", "--src-prefix=a/", "--dst-prefix=b/"}, diffFlags...), flags...), from, to, "--")
	output, err := git(ctx, repository, nil, listLimit, append(args, paths...)...)
	if err != nil {
		return nil, overLimit(err, "follow the commented lines")
	}
	sections, err := parsePatch(output)
	if err != nil {
		return nil, err
	}
	hunks := make(map[string][]Hunk, len(sections))
	for _, section := range sections {
		hunks[section.path()] = append(hunks[section.path()], section.hunks...)
	}
	return hunks, nil
}

// follow carries lines line to end of an older version through the hunks to
// the newer one. An insertion has no older lines, and goes after line
// OldStart.
func follow(hunks []Hunk, line, end int) Placement {
	shift := 0
	for _, hunk := range hunks {
		if hunk.OldLines == 0 {
			if hunk.OldStart < line {
				shift += hunk.NewLines
				continue
			}
			if hunk.OldStart >= end {
				break
			}
			return Placement{Line: line, EndLine: end, Changed: true}
		}
		first, last := hunk.OldStart, hunk.OldStart+hunk.OldLines-1
		if last < line {
			shift += hunk.NewLines - hunk.OldLines
			continue
		}
		if first > end {
			break
		}
		return Placement{Line: line, EndLine: end, Changed: true}
	}
	return Placement{Line: line + shift, EndLine: end + shift}
}

// removes reports whether the hunks remove every older line from line to
// end.
func removes(hunks []Hunk, line, end int) bool {
	for number := line; number <= end; number++ {
		removed := false
		for _, hunk := range hunks {
			if hunk.OldLines > 0 && number >= hunk.OldStart && number < hunk.OldStart+hunk.OldLines {
				removed = true
				break
			}
		}
		if !removed {
			return false
		}
	}
	return true
}

// Bounds of a comment's excerpt: the context around the commented lines,
// how many of those lines it shows before eliding the middle, and how long
// a line may be.
const (
	excerptContext   = 3
	excerptLines     = 20
	excerptLineBytes = 160
)

// CommentExcerpt checks that lines line to end of one side of a patch can
// take a comment, and returns the excerpt of the diff around them that the
// comment is sent with. A comment's lines are in one hunk: on the new side,
// lines the branch has, unchanged or added; on the old side, lines it
// removed. The excerpt shows each line's numbers, its marker, and its text,
// with ">" before the commented ones, and a few lines around them; a long
// range keeps its first and last lines, and a long line is cut.
func CommentExcerpt(patch FilePatch, old bool, line, end int) (string, error) {
	if line < 1 || end < line {
		return "", fmt.Errorf("lines %d to %d are not a range of lines", line, end)
	}
	if patch.TooLarge {
		return "", errors.New("this file's patch is too large to comment on")
	}
	side := "the branch's"
	if old {
		side = "removed"
	}
	// A line is on the old side when the branch removed it; an unchanged
	// line is on the new side, as the branch has it.
	numbered := func(current Line) (int, bool) {
		if old {
			return current.Old, current.Kind == LineDeleted
		}
		return current.New, current.Kind != LineDeleted
	}
	for _, hunk := range patch.Hunks {
		first, last := -1, -1
		for index, current := range hunk.Lines {
			number, on := numbered(current)
			if !on {
				continue
			}
			if number == line {
				first = index
			}
			if number == end {
				last = index
			}
		}
		if first < 0 && last < 0 {
			continue
		}
		if first < 0 || last < 0 {
			return "", fmt.Errorf("lines %d to %d of %s are not all in one hunk", line, end, patch.File.Path)
		}
		if old {
			for _, current := range hunk.Lines[first : last+1] {
				if current.Kind == LineContext {
					return "", fmt.Errorf("line %d of %s is not one the branch removed", current.Old, patch.File.Path)
				}
			}
		}
		return renderExcerpt(hunk.Lines, first, last), nil
	}
	return "", fmt.Errorf("lines %d to %d are not %s lines of the diff of %s", line, end, side, patch.File.Path)
}

// renderExcerpt writes the lines from first to last of a hunk, marked, with
// context around them.
func renderExcerpt(lines []Line, first, last int) string {
	from, to := max(0, first-excerptContext), min(len(lines)-1, last+excerptContext)
	width := 1
	for _, current := range lines[from : to+1] {
		width = max(width, len(strconv.Itoa(max(current.Old, current.New))))
	}
	number := func(value int) string {
		if value == 0 {
			return strings.Repeat(" ", width)
		}
		return fmt.Sprintf("%*d", width, value)
	}
	marker := map[LineKind]string{LineContext: " ", LineAdded: "+", LineDeleted: "-"}
	var builder strings.Builder
	write := func(index int) {
		current := lines[index]
		mark := " "
		if index >= first && index <= last {
			mark = ">"
		}
		text := current.Text
		if len(text) > excerptLineBytes {
			cut := excerptLineBytes
			for cut > 0 && !utf8Start(text[cut]) {
				cut--
			}
			text = text[:cut] + "…"
		}
		fmt.Fprintf(&builder, "%s %s %s %s %s\n", mark, number(current.Old), number(current.New), marker[current.Kind], text)
	}
	for index := from; index <= to; index++ {
		if commented := last - first + 1; commented > excerptLines && index == first+excerptLines/2 {
			hidden := commented - excerptLines
			fmt.Fprintf(&builder, "> %s ⋯ %d more lines\n", strings.Repeat(" ", 2*width+1), hidden)
			index += hidden - 1
			continue
		}
		write(index)
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

// utf8Start reports whether a byte starts a UTF-8 character, so a cut
// never splits one.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// LastChange is the commit that last changed a line of a branch's changes:
// for a line of the branch's version, the commit git blame names; for a
// line the branch removed, the latest commit in the range that changed the
// file.
func LastChange(ctx context.Context, repository string, between Range, path string, old bool, line int) (string, error) {
	if old {
		output, err := git(ctx, repository, nil, listLimit, "log", "-1", "--format=%H", between.From+".."+between.To, "--", path)
		if err != nil {
			return "", fmt.Errorf("find the commit that changed %s: %w", path, err)
		}
		return strings.TrimSpace(string(output)), nil
	}
	output, err := git(ctx, repository, nil, listLimit, "blame", "--porcelain", "-L", fmt.Sprintf("%d,%d", line, line), between.To, "--", path)
	if err != nil {
		return "", fmt.Errorf("find the commit that changed line %d of %s: %w", line, path, err)
	}
	hash, _, _ := strings.Cut(string(output), " ")
	return strings.TrimSpace(hash), nil
}
