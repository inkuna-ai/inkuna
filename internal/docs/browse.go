package docs

import (
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/frontmatter"
)

// MaxBytes caps the doc Read returns, so one oversized file cannot stall the
// view that renders it.
const MaxBytes = 2 << 20

// Folder is one directory of the docs tree. Path is slash-separated and
// relative to the docs root; the root folder's Name and Path are empty.
// Folders and docs are each sorted by name.
type Folder struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Folders []Folder `json:"folders"`
	Docs    []Doc    `json:"docs"`
}

// Tree arranges an index into the folders its paths name. Only folders that
// hold a doc, directly or below, appear, since discovery lists files only.
func Tree(index []Doc) Folder {
	root := newFolder("", "")
	for _, doc := range index {
		folder := &root
		segments := strings.Split(doc.Path, "/")
		for depth, name := range segments[:len(segments)-1] {
			folder = subfolder(folder, name, strings.Join(segments[:depth+1], "/"))
		}
		folder.Docs = append(folder.Docs, doc)
	}
	sortFolder(&root)
	return root
}

func newFolder(name, path string) Folder {
	return Folder{Name: name, Path: path, Folders: []Folder{}, Docs: []Doc{}}
}

// subfolder finds or adds a child folder. The pointer it returns is only held
// while one doc is placed, before the parent's slice can grow again.
func subfolder(folder *Folder, name, path string) *Folder {
	for index := range folder.Folders {
		if folder.Folders[index].Name == name {
			return &folder.Folders[index]
		}
	}
	folder.Folders = append(folder.Folders, newFolder(name, path))
	return &folder.Folders[len(folder.Folders)-1]
}

func sortFolder(folder *Folder) {
	sort.Slice(folder.Folders, func(i, j int) bool { return folder.Folders[i].Name < folder.Folders[j].Name })
	sort.Slice(folder.Docs, func(i, j int) bool { return folder.Docs[i].Path < folder.Docs[j].Path })
	for index := range folder.Folders {
		sortFolder(&folder.Folders[index])
	}
}

// Content is one doc as the Docs view reads it: its index entry and the
// Markdown after the frontmatter. A doc whose frontmatter is missing or
// cannot be split keeps its whole content as Markdown.
type Content struct {
	Doc      Doc    `json:"doc"`
	Markdown string `json:"markdown"`
}

// Read returns one doc under a repository's docs root, by the path the index
// lists it under. It reads only what discovery could have indexed: a path
// ValidatePath accepts, inside the resolved docs root, reached without a
// symbolic link below that root, and a regular file of at most MaxBytes.
func Read(repository, declared, path string) (Content, error) {
	if err := ValidatePath(path); err != nil {
		return Content{}, err
	}
	root, err := resolveRoot(repository, declared)
	if err != nil {
		return Content{}, err
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	// The root is already resolved, so any difference after resolving the
	// doc means a link below it, which discovery would not have followed.
	resolved, err := filepath.EvalSymlinks(full)
	if errors.Is(err, os.ErrNotExist) {
		return Content{}, fmt.Errorf("doc %s does not exist", path)
	}
	if err != nil {
		return Content{}, fmt.Errorf("doc %s: %w", path, err)
	}
	if resolved != full {
		return Content{}, fmt.Errorf("doc %s is reached through a symbolic link", path)
	}
	file, err := os.Open(full)
	if err != nil {
		return Content{}, fmt.Errorf("open doc %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return Content{}, fmt.Errorf("doc %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return Content{}, fmt.Errorf("doc %s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil {
		return Content{}, fmt.Errorf("read doc %s: %w", path, err)
	}
	if len(data) > MaxBytes {
		return Content{}, fmt.Errorf("doc %s is larger than %d MiB", path, MaxBytes>>20)
	}
	// Links resolve against the index, so list it; only names are read.
	listed := map[string]bool{}
	if err := walkDocs(root, func(_, docPath string) error {
		listed[docPath] = true
		return nil
	}); err != nil {
		return Content{}, fmt.Errorf("walk docs: %w", err)
	}
	content := string(data)
	markdown := content
	if _, body, err := frontmatter.Split(content); err == nil {
		markdown = body
	}
	doc := newLinkResolver(repository, declared, listed).doc(path, content)
	return Content{Doc: doc, Markdown: markdown}, nil
}

// ValidatePath checks that a doc path has the form the index uses:
// slash-separated, relative to the docs root and clean, with no "." or ".."
// segment or hidden entry, naming a *.md file.
func ValidatePath(path string) error {
	switch {
	case path == "":
		return errors.New("a doc path is required")
	case strings.Contains(path, `\`):
		return fmt.Errorf("doc path %q must use forward slashes", path)
	case strings.HasPrefix(path, "/") || filepath.IsAbs(path):
		return fmt.Errorf("doc path %q must be relative to the docs root", path)
	case path == ".." || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") || strings.HasSuffix(path, "/.."):
		return fmt.Errorf("doc path %q escapes the docs root", path)
	case pathpkg.Clean(path) != path:
		return fmt.Errorf("doc path %q is not clean", path)
	}
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, ".") {
			return fmt.Errorf("doc path %q names a hidden entry", path)
		}
	}
	if !strings.EqualFold(pathpkg.Ext(path), ".md") {
		return fmt.Errorf("doc path %q is not a Markdown file", path)
	}
	return nil
}
