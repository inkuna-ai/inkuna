package agent

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// knownKeys are the frontmatter keys Inkuna reads from an agent file: the
// YAML names of Definition's fields.
var knownKeys = func() map[string]bool {
	keys := make(map[string]bool)
	definition := reflect.TypeOf(Definition{})
	for index := range definition.NumField() {
		name, _, _ := strings.Cut(definition.Field(index).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}()

// splitFrontmatter parses an agent file's frontmatter and parts it into the
// keys Inkuna reads, to decode into a Definition, and the rest, as YAML in
// the order the file has them, to keep. An empty header has no keys.
func splitFrontmatter(header string) (*yaml.Node, string, error) {
	known := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping, err := frontmatterMapping(header)
	if err != nil || mapping == nil {
		return known, "", err
	}
	extra := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key, value := mapping.Content[index], mapping.Content[index+1]
		target := extra
		if key.Kind == yaml.ScalarNode && knownKeys[key.Value] {
			target = known
		}
		target.Content = append(target.Content, key, value)
	}
	if len(extra.Content) == 0 {
		return known, "", nil
	}
	if err := selfContained(extra); err != nil {
		return nil, "", fmt.Errorf("keep the frontmatter keys Inkuna does not read: %w", err)
	}
	text, err := yaml.Marshal(extra)
	if err != nil {
		return nil, "", fmt.Errorf("keep the frontmatter keys Inkuna does not read: %w", err)
	}
	return known, string(text), nil
}

// maxExpandedNodes bounds what selfContained copies, so aliases that
// multiply at every level cannot make a small file expand without end.
const maxExpandedNodes = 10000

// selfContained replaces each alias among the kept keys whose anchor is on
// a key Inkuna reads, which is not kept with them, by a copy of the value it
// names, so the kept YAML reads back on its own. An alias inside a value it
// is copying that names that value again, or a copy that grows past
// maxExpandedNodes, is an error rather than endless copying.
func selfContained(extra *yaml.Node) error {
	kept := make(map[*yaml.Node]bool)
	var collect func(*yaml.Node)
	collect = func(node *yaml.Node) {
		kept[node] = true
		for _, child := range node.Content {
			collect(child)
		}
	}
	collect(extra)
	copied := 0
	expanding := make(map[*yaml.Node]bool)
	var resolve func(*yaml.Node) error
	resolve = func(node *yaml.Node) error {
		for index, child := range node.Content {
			if child.Kind != yaml.AliasNode || child.Alias == nil || kept[child.Alias] {
				if err := resolve(child); err != nil {
					return err
				}
				continue
			}
			target := child.Alias
			if expanding[target] {
				return fmt.Errorf("alias *%s refers to the value that holds it", child.Value)
			}
			expanded := copyNode(target, &copied)
			if copied > maxExpandedNodes {
				return fmt.Errorf("aliases expand to more than %d values", maxExpandedNodes)
			}
			node.Content[index] = expanded
			expanding[target] = true
			err := resolve(expanded)
			delete(expanding, target)
			if err != nil {
				return err
			}
		}
		return nil
	}
	return resolve(extra)
}

// copyNode copies a node and what it holds, without its anchor, which stays
// where it was, and counts the nodes it made. An alias inside is copied as
// an alias, for selfContained to resolve in turn.
func copyNode(node *yaml.Node, count *int) *yaml.Node {
	*count++
	copied := *node
	copied.Anchor = ""
	if node.Kind == yaml.AliasNode {
		return &copied
	}
	copied.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		copied.Content[index] = copyNode(child, count)
	}
	return &copied
}

// frontmatterMapping parses YAML that must be one document holding a
// mapping, or nothing at all, which yields nil. A second document is
// refused rather than silently dropped.
func frontmatterMapping(text string) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(strings.NewReader(text))
	var document yaml.Node
	if err := decoder.Decode(&document); errors.Is(err, io.EOF) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var next yaml.Node
	if err := decoder.Decode(&next); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("frontmatter must be a single YAML document")
	}
	if document.Kind == 0 {
		return nil, nil
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("frontmatter must be a mapping of keys to values")
	}
	return document.Content[0], nil
}

// extraYAML checks the frontmatter kept from an agent file and returns it as
// it is written back: a mapping whose keys are all ones Inkuna does not
// read, so it can never set a field, such as permission, behind the form's
// back. Empty text keeps nothing.
func extraYAML(extra string) (string, error) {
	if strings.TrimSpace(extra) == "" {
		return "", nil
	}
	mapping, err := frontmatterMapping(extra)
	if err != nil {
		return "", fmt.Errorf("its other frontmatter keys are not valid: %w", err)
	}
	if mapping == nil {
		return "", nil
	}
	seen := make(map[string]bool, len(mapping.Content)/2)
	merges := mergeCheck{path: make(map[*yaml.Node]bool), clean: make(map[*yaml.Node]bool)}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := resolvedKey(mapping.Content[index])
		if key.Kind != yaml.ScalarNode {
			continue
		}
		if knownKeys[key.Value] {
			return "", fmt.Errorf("its other frontmatter keys include %q, which Inkuna reads itself", key.Value)
		}
		if key.ShortTag() == "!!merge" {
			merged, err := merges.knownKey(mapping.Content[index+1])
			if err != nil {
				return "", fmt.Errorf("its other frontmatter keys are not valid: %w", err)
			}
			if merged != "" {
				return "", fmt.Errorf("its other frontmatter keys merge in %q, which Inkuna reads itself", merged)
			}
			continue
		}
		if seen[key.Value] {
			return "", fmt.Errorf("its other frontmatter keys name %q twice", key.Value)
		}
		seen[key.Value] = true
	}
	text, err := yaml.Marshal(mapping)
	if err != nil {
		return "", fmt.Errorf("write its other frontmatter keys: %w", err)
	}
	return string(text), nil
}

// resolvedKey is a mapping key as a reader of the file sees it: an alias
// key is the value it names.
func resolvedKey(key *yaml.Node) *yaml.Node {
	if key.Kind == yaml.AliasNode && key.Alias != nil {
		return key.Alias
	}
	return key
}

// mergeCheck looks through merges (<<) for a key Inkuna reads. path holds
// the merged values being looked through, so a merge that reaches one of
// them again is a cycle; clean holds those already looked through and found
// to bring in none, so a value merged in from several places is read once.
type mergeCheck struct {
	path  map[*yaml.Node]bool
	clean map[*yaml.Node]bool
}

// knownKey is a key Inkuna reads that a merge would bring in, through an
// alias, a list of mappings, or a merge inside a merged mapping, or "" when
// it brings in none. Inkuna never applies a kept merge, but any other
// reader of the file would, so a kept merge may not set what Inkuna reads
// either.
func (check mergeCheck) knownKey(value *yaml.Node) (string, error) {
	for value.Kind == yaml.AliasNode && value.Alias != nil {
		value = value.Alias
	}
	if check.clean[value] {
		return "", nil
	}
	if check.path[value] {
		return "", errors.New("a merge (<<) brings in a value that holds it")
	}
	check.path[value] = true
	defer delete(check.path, value)
	switch value.Kind {
	case yaml.SequenceNode:
		for _, item := range value.Content {
			if key, err := check.knownKey(item); key != "" || err != nil {
				return key, err
			}
		}
	case yaml.MappingNode:
		for index := 0; index+1 < len(value.Content); index += 2 {
			key := resolvedKey(value.Content[index])
			if key.Kind != yaml.ScalarNode {
				continue
			}
			if key.ShortTag() == "!!merge" {
				if merged, err := check.knownKey(value.Content[index+1]); merged != "" || err != nil {
					return merged, err
				}
				continue
			}
			if knownKeys[key.Value] {
				return key.Value, nil
			}
		}
	}
	check.clean[value] = true
	return "", nil
}
