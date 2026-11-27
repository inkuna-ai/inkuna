// Package yamlnode copies YAML nodes so they stand alone outside the
// document they were parsed from.
package yamlnode

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// MaxNodes caps how many nodes a resolved copy may hold. Expanding aliases
// can multiply a small document many times over, as a "billion laughs" file
// does; no section of a configuration file comes near this.
const MaxNodes = 1 << 14

// Resolve returns a deep copy of node with every alias replaced by a copy of
// the node it refers to, and every anchor removed. An alias encodes as a
// reference to an anchor that may lie outside node, or be encoded after it,
// so a copy that keeps one can encode to YAML that does not parse. Comments
// are kept unless stripComments is set.
func Resolve(node *yaml.Node, stripComments bool) (*yaml.Node, error) {
	r := resolver{stripComments: stripComments, expanding: map[*yaml.Node]bool{}}
	return r.copy(node)
}

type resolver struct {
	stripComments bool
	count         int
	// expanding holds the aliased nodes being copied, to refuse an alias
	// inside the node it refers to.
	expanding map[*yaml.Node]bool
}

func (r *resolver) copy(node *yaml.Node) (*yaml.Node, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind == yaml.AliasNode {
		target := node.Alias
		if target == nil {
			return nil, fmt.Errorf("line %d: alias *%s refers to nothing", node.Line, node.Value)
		}
		if r.expanding[target] {
			return nil, fmt.Errorf("line %d: alias *%s refers to a node that contains it", node.Line, node.Value)
		}
		r.expanding[target] = true
		defer delete(r.expanding, target)
		return r.copy(target)
	}
	r.count++
	if r.count > MaxNodes {
		return nil, errors.New("too many nodes once aliases are expanded")
	}
	clone := *node
	clone.Anchor = ""
	if r.stripComments {
		clone.HeadComment, clone.LineComment, clone.FootComment = "", "", ""
	}
	if node.Content != nil {
		clone.Content = make([]*yaml.Node, len(node.Content))
	}
	for index, child := range node.Content {
		copied, err := r.copy(child)
		if err != nil {
			return nil, err
		}
		clone.Content[index] = copied
	}
	return &clone, nil
}
