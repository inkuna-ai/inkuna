package report

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/diff"
)

// Row is one row of a file's unified diff: a hunk header or a line.
type Row struct {
	Hunk   string
	Kind   string
	Old    string
	New    string
	Marker string
	Spans  []Span
	// NoNewline marks a version's last line that has no newline, the only
	// difference between a deleted and an added line that read the same.
	NoNewline bool
	// Threads are the comment threads whose last line this is.
	Threads []Thread
}

// SplitRow pairs a line of the older version with one of the newer; a hunk
// header spans both.
type SplitRow struct {
	Hunk    string
	Left    *Side
	Right   *Side
	Threads []Thread
}

// Side is one version's half of a split row.
type Side struct {
	Kind      string
	Number    string
	Marker    string
	Spans     []Span
	NoNewline bool
}

// Span is a piece of a line with its highlighting class.
type Span struct {
	Text  string
	Class string
}

var markers = map[diff.LineKind]string{diff.LineContext: " ", diff.LineAdded: "+", diff.LineDeleted: "−"}

func spans(line diff.Line) []Span {
	if len(line.Spans) == 0 {
		return []Span{{Text: line.Text}}
	}
	pieces := make([]Span, 0, len(line.Spans))
	for _, span := range line.Spans {
		class := span.Class
		if span.Changed {
			class = strings.TrimSpace(class + " changed")
		}
		pieces = append(pieces, Span{Text: span.Text, Class: class})
	}
	return pieces
}

func number(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func side(line diff.Line) *Side {
	value := line.New
	if line.Kind == diff.LineDeleted {
		value = line.Old
	}
	return &Side{Kind: string(line.Kind), Number: number(value), Marker: markers[line.Kind], Spans: spans(line), NoNewline: line.NoNewline}
}

// layout lays a patch out as unified rows and as split rows, which pair a
// run of deleted lines with the added lines after it, as the app does.
func layout(hunks []diff.Hunk) ([]Row, []SplitRow) {
	var unified []Row
	var split []SplitRow
	for _, hunk := range hunks {
		unified = append(unified, Row{Hunk: hunk.Header})
		split = append(split, SplitRow{Hunk: hunk.Header})
		var deleted, added []diff.Line
		flush := func() {
			for pair := 0; pair < max(len(deleted), len(added)); pair++ {
				row := SplitRow{}
				if pair < len(deleted) {
					row.Left = side(deleted[pair])
				}
				if pair < len(added) {
					row.Right = side(added[pair])
				}
				split = append(split, row)
			}
			deleted, added = nil, nil
		}
		for _, line := range hunk.Lines {
			unified = append(unified, Row{Kind: string(line.Kind), Old: number(line.Old), New: number(line.New), Marker: markers[line.Kind], Spans: spans(line), NoNewline: line.NoNewline})
			switch line.Kind {
			case diff.LineContext:
				flush()
				left, right := side(line), side(line)
				left.Number = number(line.Old)
				split = append(split, SplitRow{Left: left, Right: right})
			case diff.LineDeleted:
				if len(added) > 0 {
					flush()
				}
				deleted = append(deleted, line)
			default:
				added = append(added, line)
			}
		}
		flush()
	}
	return unified, split
}

// Node is a folder or a file of the changed-file tree.
type Node struct {
	Name     string
	Path     string
	Folder   bool
	Children []Node
	File     *File
}

var letters = map[diff.Status]string{diff.StatusAdded: "A", diff.StatusModified: "M", diff.StatusDeleted: "D", diff.StatusRenamed: "R", diff.StatusCopied: "C"}

type folder struct {
	folders map[string]*folder
	files   []diff.File
}

// tree groups the files by folder, folders before files and each by name,
// joining a folder whose only entry is another folder with it, as the
// app's file tree does. It returns the files in the tree's order too, the
// order the page shows their diffs in.
func tree(files []diff.File) ([]Node, []File) {
	root := &folder{folders: map[string]*folder{}}
	for _, file := range files {
		parts := strings.Split(file.Path, "/")
		current := root
		for _, part := range parts[:len(parts)-1] {
			next := current.folders[part]
			if next == nil {
				next = &folder{folders: map[string]*folder{}}
				current.folders[part] = next
			}
			current = next
		}
		current.files = append(current.files, file)
	}
	var ordered []File
	nodes := root.nodes("", &ordered)
	// Each node points at its file in the returned list, which no longer
	// grows.
	link(nodes, ordered)
	return nodes, ordered
}

func (folder *folder) nodes(prefix string, ordered *[]File) []Node {
	names := make([]string, 0, len(folder.folders))
	for name := range folder.folders {
		names = append(names, name)
	}
	sort.Strings(names)
	var nodes []Node
	for _, name := range names {
		joined, inner := name, folder.folders[name]
		for len(inner.files) == 0 && len(inner.folders) == 1 {
			for only, next := range inner.folders {
				joined, inner = joined+"/"+only, next
			}
		}
		path := prefix + joined
		nodes = append(nodes, Node{Name: joined, Path: path, Folder: true, Children: inner.nodes(path+"/", ordered)})
	}
	files := append([]diff.File(nil), folder.files...)
	sort.Slice(files, func(a, b int) bool { return files[a].Path < files[b].Path })
	for _, file := range files {
		*ordered = append(*ordered, File{
			Anchor: fmt.Sprintf("file-%d", len(*ordered)+1), Path: file.Path, OldPath: file.OldPath,
			Status: string(file.Status), Letter: letters[file.Status], Additions: file.Additions, Deletions: file.Deletions,
			Blob: file.Blob, Generated: file.Generated, Note: noPatch(file),
		})
		nodes = append(nodes, Node{Name: strings.TrimPrefix(file.Path, prefix), Path: file.Path})
	}
	return nodes
}

func link(nodes []Node, files []File) {
	byPath := make(map[string]*File, len(files))
	for index := range files {
		byPath[files[index].Path] = &files[index]
	}
	var walk func([]Node)
	walk = func(nodes []Node) {
		for index := range nodes {
			if nodes[index].Folder {
				walk(nodes[index].Children)
			} else {
				nodes[index].File = byPath[nodes[index].Path]
			}
		}
	}
	walk(nodes)
}

// noPatch says why a file needs no patch loaded, as the app's view does.
func noPatch(file diff.File) string {
	switch {
	case file.Binary:
		return "Binary file not shown."
	case file.Additions > 0 || file.Deletions > 0:
		return ""
	case file.OldMode != "" && file.NewMode != "" && file.OldMode != file.NewMode:
		return fmt.Sprintf("File mode changed from %s to %s.", file.OldMode, file.NewMode)
	}
	return ""
}

// LogLine is one line of a check's log, with the colours its ANSI codes
// gave it.
type LogLine struct {
	Number int
	Spans  []Span
}

var ansiEscape = regexp.MustCompile("\x1b\\[([0-9;?]*)([ -/]*[@-~])|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?|\x1b[@-Z\\\\-_]")

// logLines splits a log into lines, turning ANSI colour and weight codes
// into classes and dropping other escapes; a carriage return rewrites its
// line, as in a terminal. It matches the app's check log.
func logLines(text string) []LogLine {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	var lines []LogLine
	fg, bg := -1, -1
	bold := false
	for index, raw := range strings.Split(text, "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		if rewritten := strings.LastIndexByte(raw, '\r'); rewritten >= 0 {
			raw = raw[rewritten+1:]
		}
		line := LogLine{Number: index + 1}
		push := func(piece string) {
			if piece == "" {
				return
			}
			var classes []string
			if fg >= 0 {
				classes = append(classes, fmt.Sprintf("ansi-fg-%d", fg))
			}
			if bg >= 0 {
				classes = append(classes, fmt.Sprintf("ansi-bg-%d", bg))
			}
			if bold {
				classes = append(classes, "ansi-bold")
			}
			class := strings.Join(classes, " ")
			if last := len(line.Spans) - 1; last >= 0 && line.Spans[last].Class == class {
				line.Spans[last].Text += piece
				return
			}
			line.Spans = append(line.Spans, Span{Text: piece, Class: class})
		}
		at := 0
		for _, match := range ansiEscape.FindAllStringSubmatchIndex(raw, -1) {
			push(raw[at:match[0]])
			at = match[1]
			if match[4] >= 0 && raw[match[4]:match[5]] == "m" {
				fg, bg, bold = sgr(raw[match[2]:match[3]], fg, bg, bold)
			}
		}
		push(raw[at:])
		lines = append(lines, line)
	}
	return lines
}

func sgr(parameters string, fg, bg int, bold bool) (int, int, bool) {
	codes := []int{0}
	if parameters != "" {
		codes = codes[:0]
		for _, part := range strings.Split(parameters, ";") {
			code, _ := strconv.Atoi(part)
			codes = append(codes, code)
		}
	}
	for index := 0; index < len(codes); index++ {
		switch code := codes[index]; {
		case code == 0:
			fg, bg, bold = -1, -1, false
		case code == 1:
			bold = true
		case code == 22:
			bold = false
		case code >= 30 && code <= 37:
			fg = code - 30
		case code >= 90 && code <= 97:
			fg = code - 90 + 8
		case code == 39:
			fg = -1
		case code >= 40 && code <= 47:
			bg = code - 40
		case code >= 100 && code <= 107:
			bg = code - 100 + 8
		case code == 49:
			bg = -1
		case code == 38 || code == 48:
			if index+2 < len(codes) && codes[index+1] == 5 {
				if colour := codes[index+2]; colour < 16 {
					if code == 38 {
						fg = colour
					} else {
						bg = colour
					}
				}
				index += 2
			} else if index+1 < len(codes) && codes[index+1] == 2 {
				index += 4
			}
		}
	}
	return fg, bg, bold
}
