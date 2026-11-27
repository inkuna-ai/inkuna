package docs

import (
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// LinkKind says where a link from a doc leads.
type LinkKind string

const (
	// LinkDoc lands on a doc the index lists; Path is under the docs root.
	LinkDoc LinkKind = "doc"
	// LinkFile lands on something else in the same repository, such as a
	// source file or a doc outside the docs root; Path is relative to the
	// repository.
	LinkFile LinkKind = "file"
	// LinkURL is an http or https URL without its fragment. The doc graph
	// decides whether it names a registered project.
	LinkURL LinkKind = "url"
)

// Link is one link a doc makes, in document order and repeated as often as
// the doc repeats it. A relative link whose target is missing is not a Link
// but a problem on the doc.
type Link struct {
	Kind LinkKind `json:"kind"`
	Path string   `json:"path,omitempty"`
	URL  string   `json:"url,omitempty"`
}

// brokenLink starts every problem a broken relative link records, so
// IsLinkProblem can tell them from frontmatter problems.
const brokenLink = "broken link "

// IsLinkProblem reports whether a doc problem is a broken relative link
// rather than a frontmatter problem.
func IsLinkProblem(problem string) bool {
	return strings.HasPrefix(problem, brokenLink)
}

// destinations lists the targets of a doc's inline, reference-style, and
// angle-bracket links in document order. The Markdown parser resolves
// reference definitions and skips code, so a link shown as an example in a
// code block is not followed.
func destinations(body string) []string {
	source := []byte(body)
	tree := goldmark.DefaultParser().Parse(text.NewReader(source))
	found := []string{}
	_ = ast.Walk(tree, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch link := node.(type) {
		case *ast.Link:
			found = append(found, string(link.Destination))
		case *ast.AutoLink:
			if link.AutoLinkType == ast.AutoLinkURL {
				found = append(found, string(link.URL(source)))
			}
		}
		return ast.WalkContinue, nil
	})
	return found
}

// linkResolver resolves a repository's links against its index. docsDir is
// the docs root relative to the repository as declared, slash-separated,
// since a host renders relative links by where a doc sits in the repository
// tree; docs is every path the index lists.
type linkResolver struct {
	repository string
	docsDir    string
	docs       map[string]bool
}

// resolve sorts one doc's links into Links and problems. Same-doc anchors,
// links from the repository root ("/…"), protocol-relative links, and
// schemes other than http and https are not followed.
func (resolver linkResolver) resolve(docPath, body string) ([]Link, []string) {
	var links []Link
	var problems []string
	for _, destination := range destinations(body) {
		destination = strings.TrimSpace(destination)
		switch {
		case destination == "", strings.HasPrefix(destination, "#"), strings.HasPrefix(destination, "/"):
			continue
		case hasScheme(destination):
			if link, ok := urlLink(destination); ok {
				links = append(links, link)
			}
			continue
		}
		link, problem := resolver.relative(docPath, destination)
		switch {
		case problem != "":
			problems = append(problems, problem)
		case link.Kind != "":
			// A query-only link ("?plain=1") stays on the doc and has no target.
			links = append(links, link)
		}
	}
	return links, problems
}

func (resolver linkResolver) relative(docPath, destination string) (Link, string) {
	target, _, _ := strings.Cut(destination, "#")
	target, _, _ = strings.Cut(target, "?")
	if target == "" {
		return Link{}, ""
	}
	decoded, err := url.PathUnescape(target)
	if err != nil {
		return Link{}, brokenLink + quote(destination) + ": not a valid path"
	}
	repoPath := pathpkg.Join(resolver.docsDir, pathpkg.Dir(docPath), decoded)
	if repoPath == ".." || strings.HasPrefix(repoPath, "../") {
		return Link{}, brokenLink + quote(destination) + ": leaves the repository"
	}
	if docPath, ok := underDir(repoPath, resolver.docsDir); ok && resolver.docs[docPath] {
		return Link{Kind: LinkDoc, Path: docPath}, ""
	}
	// Only whether the target exists is read, never its content.
	if _, err := os.Lstat(filepath.Join(resolver.repository, filepath.FromSlash(repoPath))); err != nil {
		return Link{}, brokenLink + quote(destination) + ": no such file"
	}
	return Link{Kind: LinkFile, Path: repoPath}, ""
}

// underDir returns a repository-relative path's path under dir, when it is
// inside it.
func underDir(repoPath, dir string) (string, bool) {
	if dir == "." {
		return repoPath, true
	}
	rest, ok := strings.CutPrefix(repoPath, dir+"/")
	return rest, ok
}

func hasScheme(destination string) bool {
	parsed, err := url.Parse(destination)
	return err == nil && parsed.Scheme != ""
}

func urlLink(destination string) (Link, bool) {
	parsed, err := url.Parse(destination)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return Link{}, false
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return Link{Kind: LinkURL, URL: parsed.String()}, true
}

func quote(destination string) string {
	return `"` + destination + `"`
}
