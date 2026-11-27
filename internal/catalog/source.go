package catalog

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
)

// SourceDefinition is one definition a definition source offers to the
// catalog, with its files as the source gave them.
type SourceDefinition struct {
	Kind    Kind
	Name    string
	Scope   string
	Version string
	// Files are the definition's files by slash-separated path: "<name>.md"
	// for an agent, "<name>.yaml" for a shared stage, group, or rule, and a
	// skill's SKILL.md with the files beside it.
	Files map[string][]byte
}

// FromSource is the catalog a definition source offers: its definitions,
// each checked as an item of the built-in catalog is, needing only what is
// built in or offered by the same source. A definition that fails a check,
// or needs one that does, is left out and reported, so one broken
// definition does not take the rest of the source with it.
func FromSource(source string, definitions []SourceDefinition) (Catalog, []Problem) {
	var entries []entry
	var problems []Problem
	for index, definition := range definitions {
		current, err := sourceEntry(source, definition)
		if err != nil {
			problems = append(problems, Problem{Kind: definition.Kind, Name: definition.Name, Reason: err.Error(), Definition: index})
			continue
		}
		current.index = index
		entries = append(entries, current)
	}
	// In the catalog's layout order, so the stages and groups a rule uses
	// are known when it is read.
	order := make(map[Kind]int, len(layout))
	for index, kind := range layout {
		order[kind.kind] = index
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if order[entries[i].kind] != order[entries[j].kind] {
			return order[entries[i].kind] < order[entries[j].kind]
		}
		return entries[i].name < entries[j].name
	})
	catalog, found, _ := build(entries, true)
	return catalog, append(problems, found...)
}

// sourceEntry checks that a definition's files are laid out as its kind's
// are in a repository, and makes it an entry.
func sourceEntry(source string, definition SourceDefinition) (entry, error) {
	name := definition.Name
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return entry{}, fmt.Errorf("%q is not a name a file can have", name)
	}
	current := entry{kind: definition.Kind, name: name, origin: origin{source: source, scope: definition.Scope, version: definition.Version}}
	for _, kind := range layout {
		if kind.kind != definition.Kind {
			continue
		}
		if kind.kind == KindSkill {
			if _, ok := definition.Files["SKILL.md"]; !ok {
				return entry{}, fmt.Errorf("a skill needs a SKILL.md file; it has %s", fileList(definition.Files))
			}
			for filePath := range definition.Files {
				if !fs.ValidPath(filePath) || filePath == "." || strings.Contains(filePath, `\`) {
					return entry{}, fmt.Errorf("a skill's file %q is not a relative path inside its directory", filePath)
				}
				// A file cannot also be a directory another file is in, which
				// adding the skill would find only when it wrote them.
				for parent := path.Dir(filePath); parent != "."; parent = path.Dir(parent) {
					if _, ok := definition.Files[parent]; ok {
						return entry{}, fmt.Errorf("a skill's file %q is inside its file %q, which cannot also be a directory", filePath, parent)
					}
				}
			}
			current.path = kind.directory + "/" + name + "/SKILL.md"
			current.files = definition.Files
			return current, nil
		}
		file := name + kind.extension
		data, ok := definition.Files[file]
		if !ok || len(definition.Files) != 1 {
			return entry{}, fmt.Errorf("a %s is one file, %s; it has %s", label(kind.kind), file, fileList(definition.Files))
		}
		current.path, current.data = kind.directory+"/"+file, data
		return current, nil
	}
	return entry{}, fmt.Errorf("no kind of definition is named %q", definition.Kind)
}

func fileList(files map[string][]byte) string {
	if len(files) == 0 {
		return "none"
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, fmt.Sprintf("%q", name))
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
