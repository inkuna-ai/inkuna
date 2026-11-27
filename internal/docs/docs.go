// Package docs indexes a project's documentation: which Markdown files exist
// under the docs root and what their frontmatter says about them. The index
// never carries bodies; agents open those from the worktree. Planning, the
// CLI, and the UI all read this one index.
package docs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/frontmatter"
	"gopkg.in/yaml.v3"
)

// DefaultDir is the docs root relative to a repository when inkuna.yaml
// declares none.
const DefaultDir = "docs"

// Status is the lifecycle state a doc declares in its frontmatter.
type Status string

const (
	StatusDraft      Status = "draft"
	StatusCurrent    Status = "current"
	StatusDeprecated Status = "deprecated"
)

// Statuses lists every accepted status in display order.
var Statuses = []Status{StatusDraft, StatusCurrent, StatusDeprecated}

// ProblemMissing is the problem recorded for a doc with no frontmatter at
// all. Such a doc gets no other frontmatter problem, though it can still have
// broken links (IsLinkProblem), so callers that phase the contract in, such
// as `inkunactl docs validate --changed-since`, can tell it apart from
// frontmatter that is present but invalid.
const ProblemMissing = "missing frontmatter"

// Doc is one indexed Markdown file. Path is slash-separated and relative to
// the docs root. A doc with Problems is still indexed so one bad file never
// hides the rest; its other fields hold whatever could be read.
type Doc struct {
	Path        string   `json:"path"`
	Title       string   `json:"title"`
	LastUpdated string   `json:"lastUpdated"`
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
	Status      Status   `json:"status"`
	Problems    []string `json:"problems,omitempty"`
	Links       []Link   `json:"links,omitempty"`
}

// Frontmatter is the YAML contract Inkuna owns. Repository-specific
// conventions, such as which tags are allowed, belong to that repository's
// own checks.
type Frontmatter struct {
	Title       string   `yaml:"title"`
	LastUpdated string   `yaml:"lastUpdated"`
	Author      string   `yaml:"author"`
	Tags        []string `yaml:"tags"`
	Status      Status   `yaml:"status"`
}

// Dir resolves the docs root for a repository: the declared directory, or
// DefaultDir when it is empty. The result is confined to the repository; a
// declared directory that is absolute or escapes it is an error.
func Dir(repository, declared string) (string, error) {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		declared = DefaultDir
	}
	if err := ValidateDir(declared); err != nil {
		return "", err
	}
	return filepath.Join(repository, filepath.FromSlash(declared)), nil
}

// ValidateDir checks that a declared docs directory stays inside the
// repository: relative, and not climbing out with "..".
func ValidateDir(declared string) error {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		return nil
	}
	if filepath.IsAbs(declared) || strings.HasPrefix(declared, "/") || strings.HasPrefix(declared, "\\") {
		return fmt.Errorf("docs directory %q must be relative to the repository", declared)
	}
	clean := filepath.Clean(filepath.FromSlash(declared))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("docs directory %q escapes the repository", declared)
	}
	return nil
}

// Discover indexes every *.md file under the docs root of a repository,
// sorted by path. A docs root that does not exist yields an empty index;
// only reading the tree itself can fail. Directories and files starting with
// "." are skipped, and symbolic links are not followed. Both roots are
// resolved before walking, so a docs root that reaches outside the
// repository through a symlink is rejected rather than read.
func Discover(repository, declared string) ([]Doc, error) {
	root, err := resolveRoot(repository, declared)
	if errors.Is(err, errNoDocsRoot) {
		return []Doc{}, nil
	}
	if err != nil {
		return nil, err
	}
	// Links resolve against the whole index, so every path is listed first;
	// docs are then read one at a time rather than held together.
	files := map[string]string{}
	err = walkDocs(root, func(full, docPath string) error {
		files[docPath] = full
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk docs: %w", err)
	}
	resolver := newLinkResolver(repository, declared, files)
	index := make([]Doc, 0, len(files))
	for docPath, full := range files {
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("walk docs: %w", err)
		}
		index = append(index, resolver.doc(docPath, string(data)))
	}
	sort.Slice(index, func(i, j int) bool { return index[i].Path < index[j].Path })
	return index, nil
}

// walkDocs calls visit for every file discovery indexes under a resolved
// docs root, with its path on disk and its path under the root.
func walkDocs(root string, visit func(full, docPath string) error) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		hidden := path != root && strings.HasPrefix(entry.Name(), ".")
		if entry.IsDir() {
			if hidden {
				return fs.SkipDir
			}
			return nil
		}
		if hidden || !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return visit(path, filepath.ToSlash(relative))
	})
}

// RelativeDir is the docs root relative to its repository as declared,
// slash-separated and clean, without resolving symbolic links: where a
// repository host shows the docs, which is what relative links count from.
func RelativeDir(declared string) string {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		declared = DefaultDir
	}
	return pathpkg.Clean(filepath.ToSlash(declared))
}

// newLinkResolver resolves links for a repository whose index lists the
// keys of docs.
func newLinkResolver[V any](repository, declared string, docs map[string]V) linkResolver {
	listed := make(map[string]bool, len(docs))
	for docPath := range docs {
		listed[docPath] = true
	}
	return linkResolver{repository: repository, docsDir: RelativeDir(declared), docs: listed}
}

// doc indexes one doc: its frontmatter, then its links, whose problems
// follow the frontmatter's.
func (resolver linkResolver) doc(docPath, content string) Doc {
	doc := Parse(content)
	doc.Path = docPath
	body := content
	if _, rest, err := frontmatter.Split(content); err == nil {
		body = rest
	}
	links, problems := resolver.resolve(docPath, body)
	doc.Links = links
	doc.Problems = append(doc.Problems, problems...)
	return doc
}

// errNoDocsRoot marks a docs root that does not exist, which Discover indexes
// as empty. Only that case carries it, so a missing repository, which also
// wraps os.ErrNotExist, stays an error.
var errNoDocsRoot = errors.New("does not exist")

// resolveRoot resolves symbolic links in a repository's docs root and
// refuses one that lands outside the repository. A docs root that does not
// exist is reported with errNoDocsRoot.
func resolveRoot(repository, declared string) (string, error) {
	root, err := Dir(repository, declared)
	if err != nil {
		return "", err
	}
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return "", fmt.Errorf("repository: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("docs root %s %w", root, errNoDocsRoot)
	}
	if err != nil {
		return "", fmt.Errorf("docs root: %w", err)
	}
	if resolvedRoot != resolvedRepository && !strings.HasPrefix(resolvedRoot, resolvedRepository+string(filepath.Separator)) {
		return "", fmt.Errorf("docs root %s resolves outside the repository", root)
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("docs root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("docs root %s is not a directory", root)
	}
	return resolvedRoot, nil
}

// Parse reads one doc's frontmatter into an index entry. Every contract
// violation becomes a problem rather than an error, so the caller can show
// the doc alongside what is wrong with it. Path is left for the caller.
func Parse(content string) Doc {
	doc := Doc{Tags: []string{}}
	header, _, err := frontmatter.Split(content)
	switch {
	case errors.Is(err, frontmatter.ErrMissing):
		return problem(doc, ProblemMissing)
	case err != nil:
		return problem(doc, err.Error())
	}
	// A key the contract does not name is ignored: a doc may carry keys
	// for other tools, such as a site generator's.
	var meta Frontmatter
	decoder := yaml.NewDecoder(strings.NewReader(header))
	if err := decoder.Decode(&meta); err != nil && !errors.Is(err, io.EOF) {
		return problem(doc, "frontmatter: "+strings.TrimSpace(err.Error()))
	}
	doc.Title = strings.TrimSpace(meta.Title)
	doc.LastUpdated = strings.TrimSpace(meta.LastUpdated)
	doc.Author = strings.TrimSpace(meta.Author)
	doc.Status = meta.Status
	for _, tag := range meta.Tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			doc.Tags = append(doc.Tags, tag)
		}
	}
	if doc.Title == "" {
		doc = problem(doc, "title is required")
	}
	if doc.Author == "" {
		doc = problem(doc, "author is required")
	}
	if len(doc.Tags) == 0 {
		doc = problem(doc, "tags must be a non-empty list")
	}
	if !ValidStatus(doc.Status) {
		doc = problem(doc, fmt.Sprintf("status %q must be draft, current, or deprecated", doc.Status))
	}
	if _, err := time.Parse("2006-01-02", doc.LastUpdated); err != nil {
		doc = problem(doc, fmt.Sprintf("lastUpdated %q must be a YYYY-MM-DD date", doc.LastUpdated))
	}
	return doc
}

// ValidStatus reports whether a status is one the contract accepts.
func ValidStatus(status Status) bool {
	for _, candidate := range Statuses {
		if status == candidate {
			return true
		}
	}
	return false
}

func problem(doc Doc, message string) Doc {
	doc.Problems = append(doc.Problems, message)
	return doc
}
