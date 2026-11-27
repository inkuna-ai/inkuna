package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/inkuna-ai/inkuna/internal/rules"
)

// StageLibraryView is every shared stage and group a project's rules, as
// they are being edited, can use, and what the Rules editor needs to edit
// uses of them without keeping a copy of Go's rules.
type StageLibraryView struct {
	// Definitions are the built-in, global, and repository definitions,
	// in name order and then scope order, lowest first.
	Definitions []LibraryDefinition `json:"definitions"`
	// Problems are the mistakes in the global library files.
	Problems []rules.LibraryProblem `json:"problems"`
	// Overridable lists the stage fields a use of a shared stage may set,
	// and the kinds of stage each applies to.
	Overridable []rules.OverridableField `json:"overridable"`
}

// LibraryDefinition is one shared stage or group as the Rules editor lists
// it.
type LibraryDefinition struct {
	Name string `json:"name"`
	// Scope is builtin, global, or repository.
	Scope string `json:"scope"`
	// File is the library or rules file that defines it; empty for a
	// built-in one.
	File string `json:"file,omitempty"`
	// Kind is "stage" or "group"; exactly one of Stage and Group is set.
	Kind  string       `json:"kind"`
	Stage *rules.Stage `json:"stage,omitempty"`
	Group *rules.Group `json:"group,omitempty"`
	// Overridden marks a definition a higher scope replaces by name, so no
	// flow of this repository uses it.
	Overridden bool `json:"overridden,omitempty"`
	// Ambiguous marks a global definition another global file also
	// defines, which no use can resolve.
	Ambiguous bool `json:"ambiguous,omitempty"`
	// ReadOnly says why the definition cannot be edited here; empty when
	// it can.
	ReadOnly string `json:"readOnly,omitempty"`
	// UsedBy are the flows of the edited rules that use it, directly or
	// through a group, by rule name, or "default" for the default flow.
	UsedBy []string `json:"usedBy,omitempty"`
}

// builtinReadOnly is why a built-in definition cannot be edited.
const builtinReadOnly = "Built-in definitions are part of Inkuna and cannot be edited. Copy one to the repository to change it for this repository's flows."

// StageLibrary lists the shared stages and groups the edited rules can
// use: the built-in and global ones, and the document's own. Each says
// which flows use it, so the editor can say where a definition is used
// before it is changed or removed. The document need not be valid yet.
func (service *Service) StageLibrary(projectID, documentJSON string) (StageLibraryView, error) {
	if err := service.ready(); err != nil {
		return StageLibraryView{}, err
	}
	project, err := service.store.Project(service.context(), projectID)
	if err != nil {
		return StageLibraryView{}, fmt.Errorf("load project: %w", err)
	}
	document, err := decodeRules(documentJSON)
	if err != nil {
		return StageLibraryView{}, err
	}
	document.Source = service.rulesPath(project)
	library := service.stageLibrary()
	document = document.WithLibrary(library)
	used := make(map[string][]string)
	for _, usage := range document.Usages() {
		key := definitionKey(usage.Definition.Scope, usage.Definition.File, usage.Definition.Name)
		if !slices.Contains(used[key], usage.Flow) {
			used[key] = append(used[key], usage.Flow)
		}
	}
	view := StageLibraryView{Problems: libraryProblems(library), Overridable: rules.Overridable()}
	for _, effective := range library.Effective(&document) {
		definition := LibraryDefinition{
			Name: effective.Name, Scope: effective.Scope, File: effective.File, Kind: effective.Kind(),
			Stage: effective.Stage, Group: effective.Group, Overridden: effective.Overridden, Ambiguous: effective.Ambiguous,
			UsedBy: used[definitionKey(effective.Scope, effective.File, effective.Name)],
		}
		if effective.Scope == rules.ScopeBuiltin {
			definition.ReadOnly = builtinReadOnly
		}
		view.Definitions = append(view.Definitions, definition)
	}
	return view, nil
}

// definitionKey identifies a definition by where it is written and its
// name.
func definitionKey(scope, file, name string) string {
	if file != "" {
		file = filepath.Clean(file)
	}
	return scope + "\x00" + file + "\x00" + name
}

// RulesProblem is where Go found a mistake in edited rules, so the editor
// can point at the flow entry or definition it is about.
type RulesProblem struct {
	// Message is Go's reason, as a save would give it; empty when the
	// rules are valid.
	Message string `json:"message"`
	// Flow locates a mistake in a flow.
	Flow *FlowLocation `json:"flow,omitempty"`
	// Definition names the repository's own shared stage or group a
	// mistake is in.
	Definition string `json:"definition,omitempty"`
}

// FlowLocation is a flow entry, as written.
type FlowLocation struct {
	// Rule is the rule's index, or -1 for the default flow.
	Rule int `json:"rule"`
	// Entry is the entry's index in the flow, or -1 for a mistake about
	// the flow as a whole.
	Entry int `json:"entry"`
}

// CheckRules applies the checks a save does to edited rules and says where
// the first mistake is. Valid rules give an empty message.
func (service *Service) CheckRules(projectID, documentJSON string) (RulesProblem, error) {
	if err := service.ready(); err != nil {
		return RulesProblem{}, err
	}
	if _, err := service.store.Project(service.context(), projectID); err != nil {
		return RulesProblem{}, fmt.Errorf("load project: %w", err)
	}
	_, _, err := service.editedRules(projectID, documentJSON)
	return locateProblem(err), nil
}

// locateProblem is a mistake Go found, with the flow entry or definition
// it is about when it says.
func locateProblem(err error) RulesProblem {
	if err == nil {
		return RulesProblem{}
	}
	problem := RulesProblem{Message: err.Error()}
	var flow *rules.FlowError
	var definition *rules.DefinitionError
	switch {
	case errors.As(err, &flow):
		problem.Flow = &FlowLocation{Rule: flow.Rule, Entry: flow.Entry}
	case errors.As(err, &definition):
		problem.Definition = definition.Name
	}
	return problem
}

// ExpandRules returns the edited rules as planning sees them, every use
// expanded into plain stages that say where they came from, for the
// editor's preview. Rules that would not save are refused with the reason.
func (service *Service) ExpandRules(projectID, documentJSON string) (rules.Document, error) {
	_, document, err := service.editedRules(projectID, documentJSON)
	if err != nil {
		return rules.Document{}, err
	}
	return document.Expand()
}

// DefinitionUser is a registered repository whose saved rules use a global
// definition.
type DefinitionUser struct {
	Project    string `json:"project"`
	Repository string `json:"repository"`
	// Flows are the flows that use it, directly or through a group.
	Flows []string `json:"flows,omitempty"`
	// Problem is why the repository's rules could not be read, when they
	// could not; they may use the definition.
	Problem string `json:"problem,omitempty"`
}

// GlobalDefinitionUsers lists the registered repositories whose saved rules
// use the global definition name in file, so an edit to it says whom it
// changes. A repository whose rules do not load is listed with the reason.
func (service *Service) GlobalDefinitionUsers(file, name string) ([]DefinitionUser, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	projects, err := service.store.Projects(service.context())
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	key := definitionKey(rules.ScopeGlobal, file, name)
	users := []DefinitionUser{}
	for _, project := range projects {
		user := DefinitionUser{Project: project.Name, Repository: project.Repository}
		document, _, _, err := service.loadRules(project)
		if err != nil {
			user.Problem = err.Error()
			users = append(users, user)
			continue
		}
		for _, usage := range document.Usages() {
			if definitionKey(usage.Definition.Scope, usage.Definition.File, usage.Definition.Name) == key && !slices.Contains(user.Flows, usage.Flow) {
				user.Flows = append(user.Flows, usage.Flow)
			}
		}
		if len(user.Flows) > 0 {
			users = append(users, user)
		}
	}
	return users, nil
}

// libraryEdit is a definition the view sends to be saved: exactly one of
// a stage and a group.
type libraryEdit struct {
	Stage *rules.Stage `json:"stage,omitempty"`
	Group *rules.Group `json:"group,omitempty"`
}

// SaveGlobalDefinition replaces the global definition name in the library
// file with the one sent, checked as the library checks it. A built-in
// definition, which has no file, is refused: it is copied to a
// repository's rules instead. Plans already made that used the old one are
// refused at approval, as for any library edit.
func (service *Service) SaveGlobalDefinition(file, name, definitionJSON string) error {
	if err := service.ready(); err != nil {
		return err
	}
	var edit libraryEdit
	decoder := json.NewDecoder(bytes.NewReader([]byte(definitionJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&edit); err != nil {
		return fmt.Errorf("decode definition: %w", err)
	}
	scope := rules.ScopeGlobal
	if file == "" {
		scope = rules.ScopeBuiltin
	}
	definition := rules.Definition{Name: name, Scope: scope, File: file, Stage: edit.Stage, Group: edit.Group}
	content, err := rules.WriteLibraryDefinition(definition, service.stageLibrary())
	if err != nil {
		return fmt.Errorf("save global definition: %w", err)
	}
	service.auditChange(auditLibrarySave, "", "Saved "+definition.Kind()+" "+name, map[string]string{"definition": name, "kind": definition.Kind()}, file, content)
	return nil
}

// decodeRules decodes a document the view sent, strictly, without
// checking it.
func decodeRules(documentJSON string) (rules.Document, error) {
	var document rules.Document
	decoder := json.NewDecoder(bytes.NewReader([]byte(documentJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return rules.Document{}, fmt.Errorf("decode rules: %w", err)
	}
	return document, nil
}
