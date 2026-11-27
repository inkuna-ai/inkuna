// Package diff models a run's changes for review: the files a range of
// commits changed with their counts, and one file's hunks on demand. It runs
// git read-only, by commit hash, in the project repository, and imports
// nothing from Wails or SQLite, so the app and inkunactl render one model.
package diff

// Status is how a file changed.
type Status string

const (
	StatusAdded    Status = "added"
	StatusModified Status = "modified"
	StatusDeleted  Status = "deleted"
	StatusRenamed  Status = "renamed"
	StatusCopied   Status = "copied"
)

// A file's patch over the soft caps comes back TooLarge instead of loaded,
// and one over the hard caps is refused even when asked to load anyway. A
// patch is never cut short.
const (
	SoftBytes = 1 << 20
	SoftLines = 20_000
	HardBytes = 10 << 20
	HardLines = 200_000
)

// File is one changed file.
type File struct {
	// Path is the file's path after the change, or before it for a deleted
	// file. OldPath is where a renamed or copied file came from.
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Status  Status `json:"status"`
	// OldMode and NewMode are git's file modes, such as 100644 or 100755.
	// They differ on a mode change; an added file has no old mode and a
	// deleted file no new one.
	OldMode string `json:"oldMode,omitempty"`
	NewMode string `json:"newMode,omitempty"`
	// Similarity is git's score for a rename or copy, 0 to 100.
	Similarity int  `json:"similarity,omitempty"`
	Binary     bool `json:"binary,omitempty"`
	Additions  int  `json:"additions"`
	Deletions  int  `json:"deletions"`
	// Generated marks lockfiles, build output, and paths the repository
	// marks as generated, which views collapse.
	Generated bool `json:"generated,omitempty"`
	// Blob is the hash of the file's content after the change, or before it
	// for a deleted file. A review mark is kept against it, so a file that
	// changes again reads as not yet reviewed.
	Blob string `json:"blob,omitempty"`
}

// Change is what a range of commits changed, without patches.
type Change struct {
	// From and To are the commits compared. A view loads each file's patch
	// with a range scope of these two, so the patches match the list even
	// when the branch moves on in between.
	From      string `json:"from"`
	To        string `json:"to"`
	Files     []File `json:"files"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// LineKind is what a patch line does.
type LineKind string

const (
	LineContext LineKind = "context"
	LineAdded   LineKind = "added"
	LineDeleted LineKind = "deleted"
)

// Span is a piece of a line's text: its highlighting class, if any, and
// whether it is part of what changed within the line. A line's spans join
// to its text.
type Span struct {
	Text    string `json:"text"`
	Class   string `json:"class,omitempty"`
	Changed bool   `json:"changed,omitempty"`
}

// Line is one line of a hunk.
type Line struct {
	Kind LineKind `json:"kind"`
	// Old and New are the line's numbers in each version; an added line has
	// no old number and a deleted line no new one.
	Old  int    `json:"old,omitempty"`
	New  int    `json:"new,omitempty"`
	Text string `json:"text"`
	// CRLF is set when the line ended in a carriage return, which Text
	// leaves out.
	CRLF bool `json:"crlf,omitempty"`
	// NoNewline marks the last line of a version that has no newline at its
	// end.
	NoNewline bool `json:"noNewline,omitempty"`
	// Spans split Text for highlighting and for changes within the line;
	// they are absent when there is neither.
	Spans []Span `json:"spans,omitempty"`
}

// Hunk is one @@ section of a patch.
type Hunk struct {
	// Header is the whole @@ line, and Section the function or heading git
	// found for it.
	Header   string `json:"header"`
	Section  string `json:"section,omitempty"`
	OldStart int    `json:"oldStart"`
	OldLines int    `json:"oldLines"`
	NewStart int    `json:"newStart"`
	NewLines int    `json:"newLines"`
	Lines    []Line `json:"lines"`
}

// FilePatch is one file's patch.
type FilePatch struct {
	File File   `json:"file"`
	From string `json:"from"`
	To   string `json:"to"`
	// Language names the lexer that highlighted the file, if one did.
	Language string `json:"language,omitempty"`
	Hunks    []Hunk `json:"hunks"`
	// Bytes and Lines measure the patch, counted up to the hard caps.
	Bytes int `json:"bytes"`
	Lines int `json:"lines"`
	// NewTotal is how many lines the newer version of the file has, so a
	// view knows whether there is context below the last hunk to expand. It
	// is 0 when there is no newer version or it is over the soft byte cap.
	NewTotal int `json:"newTotal,omitempty"`
	// TooLarge is set when the patch was not loaded, with the Reason.
	// Loadable says it is under the hard caps, so it can be loaded anyway.
	TooLarge bool   `json:"tooLarge,omitempty"`
	Loadable bool   `json:"loadable,omitempty"`
	Reason   string `json:"reason,omitempty"`
}
