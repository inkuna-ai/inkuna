package diff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// Changes lists what changed between a range's two commits: each file with
// its line counts and generated flag, but no patch.
func Changes(ctx context.Context, repository string, between Range, review Review) (Change, error) {
	entries, err := list(ctx, repository, between)
	if err != nil {
		return Change{}, err
	}
	output, err := git(ctx, repository, nil, listLimit, append(append([]string{"diff", "--numstat", "-z"}, diffFlags...), between.From, between.To, "--")...)
	if err != nil {
		return Change{}, overLimit(err, "count the changed lines")
	}
	numbers, err := parseNumstat(output)
	if err != nil {
		return Change{}, err
	}
	paths := make([]string, len(entries))
	for index, entry := range entries {
		paths[index] = entry.file.Path
	}
	rules, err := rulesFor(ctx, repository, between.To, review, paths)
	if err != nil {
		return Change{}, err
	}
	change := Change{From: between.From, To: between.To, Files: make([]File, 0, len(entries))}
	for _, entry := range entries {
		file := entry.file
		count := numbers[file.Path]
		file.Additions, file.Deletions, file.Binary = count.additions, count.deletions, count.binary
		file.Generated = rules.flags(file.Path)
		change.Additions += file.Additions
		change.Deletions += file.Deletions
		change.Files = append(change.Files, file)
	}
	return change, nil
}

// Patch loads one file's patch between a range's two commits. A patch over
// the soft caps comes back TooLarge unless loadAnyway is set, and one over
// the hard caps comes back TooLarge regardless; neither is ever cut short.
func Patch(ctx context.Context, repository string, between Range, review Review, path string, loadAnyway bool) (FilePatch, error) {
	entries, err := list(ctx, repository, between)
	if err != nil {
		return FilePatch{}, err
	}
	found, err := lookup(entries, path, between)
	if err != nil {
		return FilePatch{}, err
	}
	rules, err := rulesFor(ctx, repository, between.To, review, []string{path})
	if err != nil {
		return FilePatch{}, err
	}
	return patchOf(ctx, repository, between, found, rules.flags(path), loadAnyway)
}

// Patches loads several files' patches, in the order given, as Patch
// loads one. It lists the range and reads its generated rules once for
// them all, where calling Patch for each file would list the whole range
// again every time.
func Patches(ctx context.Context, repository string, between Range, review Review, paths []string, loadAnyway bool) ([]FilePatch, error) {
	entries, err := list(ctx, repository, between)
	if err != nil {
		return nil, err
	}
	found := make([]entry, len(paths))
	for index, path := range paths {
		if found[index], err = lookup(entries, path, between); err != nil {
			return nil, err
		}
	}
	rules, err := rulesFor(ctx, repository, between.To, review, paths)
	if err != nil {
		return nil, err
	}
	patches := make([]FilePatch, len(paths))
	for index, path := range paths {
		if patches[index], err = patchOf(ctx, repository, between, found[index], rules.flags(path), loadAnyway); err != nil {
			return nil, fmt.Errorf("load the patch of %s: %w", path, err)
		}
	}
	return patches, nil
}

func lookup(entries []entry, path string, between Range) (entry, error) {
	for _, candidate := range entries {
		if candidate.file.Path == path {
			return candidate, nil
		}
	}
	return entry{}, fmt.Errorf("%s did not change between %s and %s", path, short(between.From), short(between.To))
}

// patchOf loads the patch of a file the range listed.
func patchOf(ctx context.Context, repository string, between Range, found entry, generated bool, loadAnyway bool) (FilePatch, error) {
	path := found.file.Path
	result := FilePatch{File: found.file, From: between.From, To: between.To, Hunks: []Hunk{}}
	result.File.Generated = generated

	// A renamed or copied file's patch needs its old path too, or git
	// would see an added file. Only the rename or copy itself is wanted: a
	// copy's source can have changes of its own, which would otherwise come
	// along and count toward this file's caps.
	flags, paths := diffFlags, []string{path}
	if found.file.OldPath != "" {
		flags = append(append([]string{}, diffFlags...), "--diff-filter=CR")
		paths = []string{found.file.OldPath, path}
	}
	// The counts come from numstat, so a patch too large to load still has
	// them.
	numbers, err := git(ctx, repository, nil, listLimit, append(append(append([]string{"diff", "--numstat", "-z"}, flags...), between.From, between.To, "--"), paths...)...)
	if err != nil {
		return FilePatch{}, overLimit(err, "count the changed lines")
	}
	counted, err := parseNumstat(numbers)
	if err != nil {
		return FilePatch{}, err
	}
	count := counted[path]
	result.File.Additions, result.File.Deletions, result.File.Binary = count.additions, count.deletions, count.binary
	output, err := git(ctx, repository, nil, HardBytes, append(append(append([]string{"diff", "-U3", "--src-prefix=a/", "--dst-prefix=b/"}, flags...), between.From, between.To, "--"), paths...)...)
	over := errors.Is(err, errOverLimit)
	if err != nil && !over {
		return FilePatch{}, err
	}
	result.Bytes, result.Lines = len(output), bytes.Count(output, []byte("\n"))
	switch {
	case over || result.Lines > HardLines:
		result.TooLarge = true
		result.Reason = fmt.Sprintf("This patch is over %d MB or %d lines, more than Inkuna shows.", HardBytes>>20, HardLines)
		return result, nil
	case !loadAnyway && (result.Bytes > SoftBytes || result.Lines > SoftLines):
		result.TooLarge, result.Loadable = true, true
		result.Reason = fmt.Sprintf("This patch is over %d MB or %d lines, so it is not loaded until asked.", SoftBytes>>20, SoftLines)
		return result, nil
	}
	sections, err := parsePatch(output)
	if err != nil {
		return FilePatch{}, fmt.Errorf("%s: %w", path, err)
	}
	// A file that changed type, such as one replaced by a symlink, comes as
	// two sections for the same path: the old version deleted, then the new
	// one added. Both belong to the patch.
	matched := false
	for _, section := range sections {
		if section.path() == path {
			matched = true
			result.File.Binary = result.File.Binary || section.binary
			result.Hunks = append(result.Hunks, section.hunks...)
		}
	}
	if !matched {
		return FilePatch{}, fmt.Errorf("git wrote no patch for %s", path)
	}
	if result.File.Binary {
		return result, nil
	}
	oldLines, newLines := result.highlight(ctx, repository, found)
	budget := maxPatchWordCells
	for hunkIndex := range result.Hunks {
		hunk := &result.Hunks[hunkIndex]
		changed := pairChanges(hunk.Lines, &budget)
		for lineIndex := range hunk.Lines {
			line := &hunk.Lines[lineIndex]
			var pieces []Span
			if line.Kind == LineDeleted {
				pieces = lineOf(oldLines, line.Old)
			} else {
				pieces = lineOf(newLines, line.New)
			}
			line.Spans = lineSpans(line.Text, pieces, changed[lineIndex])
		}
	}
	return result, nil
}

// highlight tokenizes both versions of the file, each read whole when it
// is no larger than the soft byte cap; a larger version goes unhighlighted.
// It counts the newer version's lines while it has them.
func (patch *FilePatch) highlight(ctx context.Context, repository string, file entry) ([][]Span, [][]Span) {
	lexer := lexerFor(file.file.Path)
	if lexer == nil {
		lexer = lexerFor(file.file.OldPath)
	}
	if lexer != nil {
		patch.Language = lexer.Config().Name
	}
	read := func(blob string) (string, bool) {
		if missingBlob(blob) {
			return "", false
		}
		text, err := git(ctx, repository, nil, SoftBytes, "cat-file", "blob", blob)
		return string(text), err == nil
	}
	var oldLines, newLines [][]Span
	if text, ok := read(file.newBlob); ok {
		patch.NewTotal = countLines(text)
		if lexer != nil {
			newLines = highlight(lexer, text)
		}
	}
	if lexer != nil {
		if text, ok := read(file.oldBlob); ok {
			oldLines = highlight(lexer, text)
		}
	}
	return oldLines, newLines
}

// countLines counts a file's lines, the last one with or without its
// newline.
func countLines(text string) int {
	count := strings.Count(text, "\n")
	if text != "" && !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

func lineOf(lines [][]Span, number int) []Span {
	if number < 1 || number > len(lines) {
		return nil
	}
	return lines[number-1]
}

// list runs git diff --raw for the range.
func list(ctx context.Context, repository string, between Range) ([]entry, error) {
	output, err := git(ctx, repository, nil, listLimit, append(append([]string{"diff", "--raw", "-z", "--no-abbrev"}, diffFlags...), between.From, between.To, "--")...)
	if err != nil {
		return nil, overLimit(err, "list the changed files")
	}
	return parseRaw(output)
}

// rulesFor builds the generated rules for some paths, with the
// .gitattributes the range's newer commit has. Attributes only collapse
// files, so a git too old to read them from a commit leaves the flags to
// the other rules rather than failing the view.
func rulesFor(ctx context.Context, repository, commit string, review Review, paths []string) (generated, error) {
	rules, err := newGenerated(review)
	if err != nil {
		return generated{}, err
	}
	rules.attributes, _ = readAttributes(ctx, repository, commit, paths)
	return rules, nil
}

func overLimit(err error, doing string) error {
	if errors.Is(err, errOverLimit) {
		return fmt.Errorf("%s: git wrote more than %d MB, too much to show", doing, listLimit>>20)
	}
	return fmt.Errorf("%s: %w", doing, err)
}
