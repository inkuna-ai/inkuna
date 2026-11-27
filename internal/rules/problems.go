package rules

import (
	"errors"
	"fmt"
)

// FlowError is a mistake in one flow of a document: a rule's, or the
// default flow. Its text is the mistake's, named by the flow, so it reads
// as it always has; its fields let an editor open the flow and point at the
// entry the mistake is about.
type FlowError struct {
	// Rule is the rule's index in Document.Rules, or -1 for the default
	// flow.
	Rule int
	// Name is the rule's name; empty for the default flow.
	Name string
	// Entry is the index of the flow entry, as written, that the mistake is
	// about: the use a stage expanded from, or the inline stage. It is -1
	// for a mistake about the flow as a whole.
	Entry int
	Err   error
}

func (err *FlowError) Error() string {
	if err.Rule < 0 {
		return "default flow: " + err.Err.Error()
	}
	return fmt.Sprintf("rule %q: %v", err.Name, err.Err)
}

func (err *FlowError) Unwrap() error { return err.Err }

// flowError names a mistake in the flow of rule (-1 for the default flow)
// and records the entry it is about, when it is about one.
func flowError(rule int, name string, err error) error {
	entry := -1
	var located entryError
	if errors.As(err, &located) {
		entry = located.entry
	}
	return &FlowError{Rule: rule, Name: name, Entry: entry, Err: err}
}

// entryError is a mistake about one entry of a flow or group, by its index
// there. Its text is the mistake's own. A mistake inside a used group is
// wrapped again by the entry that used it, so the outermost one names the
// flow's entry.
type entryError struct {
	entry int
	err   error
}

func (err entryError) Error() string { return err.err.Error() }

func (err entryError) Unwrap() error { return err.err }

// DefinitionError is a mistake in one of a document's own shared stages or
// groups. Its text names the definition already; Name lets an editor point
// at it.
type DefinitionError struct {
	Name string
	Err  error
}

func (err *DefinitionError) Error() string { return err.Err.Error() }

func (err *DefinitionError) Unwrap() error { return err.Err }
