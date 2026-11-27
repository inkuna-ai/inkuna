package rules

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"gopkg.in/yaml.v3"
)

// Group is a named run of stages, wired among themselves, that a flow uses
// as one entry. Its entries may be inline stages or uses of shared stages
// and other groups, and their dependsOn name only entries of the group: a
// use of the group attaches its own dependsOn to the stages that depend on
// nothing, and a later dependsOn on the use's id waits for the stages
// nothing in the group depends on.
type Group struct {
	Stages []Stage `yaml:"stages" json:"stages"`
}

// Provenance says where an expanded stage came from, so a plan can show it.
type Provenance struct {
	// Stage is the shared stage the entry used. It is empty for a stage
	// written inline in a group.
	Stage string `json:"stage,omitempty"`
	// Groups are the groups the stage came through, outermost first.
	Groups []string `json:"groups,omitempty"`
	// Scope is where the definition lives: ScopeBuiltin, ScopeGlobal, or
	// ScopeRepository.
	Scope string `json:"scope"`
	// File is the global library file the definition lives in; empty for a
	// built-in one or the repository's own.
	File string `json:"file,omitempty"`
	// Overrides are the fields the use set, by YAML name.
	Overrides []string `json:"overrides,omitempty"`
}

// origin is the provenance as a planned step keeps it; nil for a stage
// written inline in a flow, which has none.
func (from *Provenance) origin() *domain.StepOrigin {
	if from == nil {
		return nil
	}
	origin := &domain.StepOrigin{Stage: from.Stage, Scope: from.Scope, File: from.File}
	// Empty lists stay nil, so a plan reads the same before and after it is
	// saved, which drops them.
	if len(from.Groups) > 0 {
		origin.Groups = slices.Clone(from.Groups)
	}
	if len(from.Overrides) > 0 {
		origin.Overrides = slices.Clone(from.Overrides)
	}
	return origin
}

// overridableField is a stage field a use may set, by YAML name, and the
// kinds of stage it applies to (every kind when none are listed).
type overridableField struct {
	name  string
	kinds []domain.StepKind
}

// overridable lists, by YAML name, the stage fields a use of a shared stage
// may set, and the kinds of stage each applies to (every kind when none are
// listed). A use's own fields, use, id, and dependsOn, wire it in, and its
// kind is always the definition's.
var overridable = []overridableField{
	{"title", nil},
	{"agent", []domain.StepKind{domain.StepAgent, domain.StepHumanInput}},
	{"runtime", []domain.StepKind{domain.StepAgent}},
	{"permission", []domain.StepKind{domain.StepAgent}},
	{"skills", []domain.StepKind{domain.StepAgent}},
	{"allowedPaths", []domain.StepKind{domain.StepAgent}},
	{"prompt", []domain.StepKind{domain.StepAgent, domain.StepHumanInput}},
	{"commands", []domain.StepKind{domain.StepAgent, domain.StepEvaluation}},
	{"optional", nil},
	{"continueOnFailure", []domain.StepKind{domain.StepEvaluation}},
	{"review", []domain.StepKind{domain.StepApproval}},
	{"onFailure", []domain.StepKind{domain.StepEvaluation}},
	{"feedback", []domain.StepKind{domain.StepApproval, domain.StepEvaluation}},
	{"mcp", []domain.StepKind{domain.StepAgent}},
	{"tracker", []domain.StepKind{domain.StepAgent}},
}

// OverridableField is a stage field a use of a shared stage may set, by
// its YAML name, and the kinds of stage it applies to.
type OverridableField struct {
	Name string `json:"name"`
	// Kinds are the stage kinds the field applies to; empty means every
	// kind.
	Kinds []domain.StepKind `json:"kinds,omitempty"`
}

// Overridable lists the stage fields a use of a shared stage may set, in
// the order a use writes them, so an editor offers exactly what Go accepts.
func Overridable() []OverridableField {
	fields := make([]OverridableField, len(overridable))
	for index, field := range overridable {
		fields[index] = OverridableField{Name: field.name, Kinds: slices.Clone(field.kinds)}
	}
	return fields
}

// stageFields maps each Stage field's YAML name to its index, so a use's
// overrides are copied by the same names the file writes them under.
var stageFields = func() map[string]int {
	fields := make(map[string]int)
	stage := reflect.TypeFor[Stage]()
	for index := range stage.NumField() {
		name, _, _ := strings.Cut(stage.Field(index).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			fields[name] = index
		}
	}
	return fields
}()

// overridableName reports whether a use may set the named field.
func overridableName(name string) bool {
	return slices.ContainsFunc(overridable, func(field overridableField) bool { return field.name == name })
}

// appliesTo reports whether an overridable field means anything on a stage
// of the given kind.
func appliesTo(name string, kind domain.StepKind) bool {
	for _, field := range overridable {
		if field.name == name {
			return len(field.kinds) == 0 || slices.Contains(field.kinds, kind)
		}
	}
	return false
}

// overridden returns the fields a use sets, by YAML name in overridable
// order: those Overrides lists and any set to a non-zero value.
func (stage Stage) overridden() []string {
	value := reflect.ValueOf(stage)
	var names []string
	for _, field := range overridable {
		if slices.Contains(stage.Overrides, field.name) || !value.Field(stageFields[field.name]).IsZero() {
			names = append(names, field.name)
		}
	}
	return names
}

// Label names a stage in an error: its id, and for an expanded stage where
// it came from, such as "verify-docs" (from shared stage "verify").
func (stage Stage) Label() string {
	label := strconv.Quote(stage.ID)
	if stage.From == nil || (stage.From.Stage == "" && len(stage.From.Groups) == 0) {
		return label
	}
	var origin []string
	if stage.From.Stage != "" {
		origin = append(origin, "shared stage "+strconv.Quote(stage.From.Stage))
	}
	for index := len(stage.From.Groups) - 1; index >= 0; index-- {
		origin = append(origin, "group "+strconv.Quote(stage.From.Groups[index]))
	}
	switch {
	case stage.From.File != "":
		return label + " (from " + strings.Join(origin, " in ") + ", " + stage.From.File + ")"
	case stage.From.Scope == ScopeBuiltin:
		return label + " (from built-in " + strings.Join(origin, " in ") + ")"
	}
	return label + " (from " + strings.Join(origin, " in ") + ")"
}

// clone copies a stage deeply enough that changing the copy, as expansion
// does, never changes the stage it came from.
func (stage Stage) clone() Stage {
	stage.DependsOn = slices.Clone(stage.DependsOn)
	stage.Skills = slices.Clone(stage.Skills)
	stage.AllowedPaths = slices.Clone(stage.AllowedPaths)
	stage.MCP = slices.Clone(stage.MCP)
	stage.Tracker = slices.Clone(stage.Tracker)
	stage.Overrides = slices.Clone(stage.Overrides)
	if stage.Commands != nil {
		commands := make([][]string, len(stage.Commands))
		for index, command := range stage.Commands {
			commands[index] = slices.Clone(command)
		}
		stage.Commands = commands
	}
	if stage.Feedback != nil {
		feedback := *stage.Feedback
		if feedback.MaxRounds != nil {
			rounds := *feedback.MaxRounds
			feedback.MaxRounds = &rounds
		}
		feedback.SystemExitCodes = slices.Clone(feedback.SystemExitCodes)
		stage.Feedback = &feedback
	}
	if stage.From != nil {
		from := *stage.From
		from.Groups = slices.Clone(from.Groups)
		from.Overrides = slices.Clone(from.Overrides)
		stage.From = &from
	}
	return stage
}

// RequiredVersion is the lowest rules version that has everything the
// document uses: 2 for shared stages, groups, and uses, otherwise 1.
func (document Document) RequiredVersion() int {
	if len(document.Stages) > 0 || len(document.Groups) > 0 {
		return sharedVersion
	}
	flows := make([]Flow, 0, len(document.Rules)+1)
	for _, rule := range document.Rules {
		flows = append(flows, rule.Then)
	}
	if document.Default != nil {
		flows = append(flows, *document.Default)
	}
	for _, flow := range flows {
		if slices.ContainsFunc(flow.Stages, func(stage Stage) bool { return stage.Use != "" }) {
			return sharedVersion
		}
	}
	return 1
}

// Expand returns the document as planning sees it: every flow with its uses
// resolved into plain stages, each recording where it came from, and no
// shared stages or groups, which the flows no longer need.
func (document Document) Expand() (Document, error) {
	expanded := document
	expanded.Stages, expanded.Groups = nil, nil
	expanded.Rules = make([]Rule, len(document.Rules))
	for index, rule := range document.Rules {
		flow, err := document.ExpandFlow(rule.Then)
		if err != nil {
			return Document{}, flowError(index, rule.Name, err)
		}
		rule.Then = flow
		expanded.Rules[index] = rule
	}
	if document.Default != nil {
		flow, err := document.ExpandFlow(*document.Default)
		if err != nil {
			return Document{}, flowError(-1, "", err)
		}
		expanded.Default = &flow
	}
	return expanded, nil
}

// ExpandFlow resolves a flow's uses of shared stages and groups into plain
// stages. A flow without uses comes back as it was. Mistakes that are the
// flow's own, such as a dependsOn on a stage that does not exist, are left
// for Flow.Validate, which names the use an expanded stage came from.
func (document Document) ExpandFlow(flow Flow) (Flow, error) {
	expanded, _, err := document.ExpandFlowEntries(flow)
	return expanded, err
}

// ExpandFlowEntries is ExpandFlow, also returning, for each expanded stage,
// the index of the flow entry it came from, so a mistake found in the
// expanded flow can be shown on the entry as written.
func (document Document) ExpandFlowEntries(flow Flow) (Flow, []int, error) {
	if len(flow.Stages) == 0 {
		return flow, nil, nil
	}
	items, err := document.expandEntries(flow.Stages, nil, Definition{})
	if err != nil {
		return Flow{}, nil, err
	}
	stages := make([]Stage, len(items))
	entries := make([]int, len(items))
	for index, item := range items {
		stages[index] = item.stage
		entries[index] = item.entry
	}
	return Flow{Stages: stages}, entries, nil
}

// item is one expanded stage.
type item struct {
	stage Stage
	// local marks a feedback target already resolved to a stage of the
	// group being expanded, so that renaming the stage renames the target.
	local bool
	// entry is the index of the entry the stage came from, among the
	// entries last expanded; for a flow's, the flow entry.
	entry int
}

// unit is one flow or group entry once expanded: an inline stage or a used
// shared stage gives one stage, and a used group gives its stages.
type unit struct {
	// index is the entry's position among the entries it is written with.
	index     int
	label     string
	use       bool
	dependsOn []string
	items     []item
	// handle is a used group's id, which later entries depend on to wait
	// for its exits: the stages nothing else in the group depends on.
	handle string
	exits  []string
}

// expandEntries resolves the entries of a flow, or of a group when groups
// (the groups being expanded, outermost first) is not empty; group is then
// the innermost one's definition, which says where its entries are written.
func (document Document) expandEntries(entries []Stage, groups []string, group Definition) ([]item, error) {
	owner := ""
	if len(groups) > 0 {
		owner = group.describe() + ": "
	}
	units := make([]unit, 0, len(entries))
	for index, entry := range entries {
		expanded, err := document.expandEntry(index, entry, groups, group, owner)
		if err != nil {
			return nil, entryError{entry: index, err: err}
		}
		expanded.index = index
		units = append(units, expanded)
	}
	prefixGroupStages(units)
	if err := checkDuplicates(units, owner, len(groups) > 0); err != nil {
		return nil, err
	}
	if err := wire(units, owner, len(groups) > 0); err != nil {
		return nil, err
	}
	var items []item
	for _, expanded := range units {
		for _, expandedItem := range expanded.items {
			expandedItem.entry = expanded.index
			items = append(items, expandedItem)
		}
	}
	if len(groups) > 0 {
		resolveLocalTargets(items)
	}
	return items, nil
}

// expandEntry turns one entry into its unit. Errors about the entry name
// it; errors from inside a group it uses already name that group. group is
// the definition of the group the entry is written in, if any.
func (document Document) expandEntry(index int, entry Stage, groups []string, group Definition, owner string) (unit, error) {
	expanded := unit{label: entryLabel(index, entry), dependsOn: entry.DependsOn}
	fail := func(format string, args ...any) (unit, error) {
		return unit{}, fmt.Errorf("%s%s: %s", owner, expanded.label, fmt.Sprintf(format, args...))
	}
	if entry.Use == "" {
		if len(entry.Overrides) > 0 {
			return fail("lists overrides, which only a use has")
		}
		if len(groups) > 0 && strings.TrimSpace(entry.ID) == "" {
			return fail("needs an id")
		}
		stage := entry.clone()
		if len(groups) > 0 {
			stage.From = group.provenance("", groups, nil)
		}
		expanded.items = []item{{stage: stage}}
		return expanded, nil
	}
	expanded.use = true
	if entry.Kind != "" {
		return fail("sets kind, but a use keeps the kind of %q", entry.Use)
	}
	for _, name := range entry.Overrides {
		if !overridableName(name) {
			return fail("lists override %q, which is not a stage field a use can set", name)
		}
	}
	id := entry.ID
	if id == "" {
		id = entry.Use
	} else if strings.TrimSpace(id) != id {
		return fail("has id %q, which must not start or end with a space", id)
	}
	overrides := entry.overridden()
	definition, err := document.resolve(entry.Use)
	if err != nil {
		reason := err.Error()
		if errors.Is(err, errNotDefined) {
			reason = fmt.Sprintf("no shared stage or group is named %q", entry.Use)
		}
		_, failed := fail("%s", reason)
		return unit{}, referenceError{failed}
	}
	if definition.Stage != nil {
		for _, name := range overrides {
			if !appliesTo(name, definition.Stage.Kind) {
				return fail("overrides %s, which %s stage does not have", name, article(definition.Stage.Kind))
			}
		}
		stage := definition.Stage.clone()
		source, target := reflect.ValueOf(entry.clone()), reflect.ValueOf(&stage).Elem()
		for _, name := range overrides {
			target.Field(stageFields[name]).Set(source.Field(stageFields[name]))
		}
		stage.ID, stage.DependsOn = id, slices.Clone(entry.DependsOn)
		stage.From = definition.provenance(entry.Use, groups, overrides)
		expanded.items = []item{{stage: stage}}
		return expanded, nil
	}
	if len(overrides) > 0 {
		return fail("sets %s, but a use of a group takes only id and dependsOn", strings.Join(overrides, ", "))
	}
	items, exits, err := document.expandGroup(definition, groups)
	if err != nil {
		return unit{}, err
	}
	expanded.handle, expanded.items, expanded.exits = id, items, exits
	return expanded, nil
}

// referenceError is a use whose name did not resolve. Whether a name
// resolves depends on the document it is looked up from, since a
// repository's own definition settles a name the library lacks or cannot
// decide, so a global group with one is checked again where it is used.
type referenceError struct{ error }

func (err referenceError) Unwrap() error { return err.error }

// provenance is where a stage expanded from this definition came from: the
// shared stage it is, when stage is not empty, the groups it came through,
// and the definition's scope and, for a global one, file.
func (definition Definition) provenance(stage string, groups, overrides []string) *Provenance {
	from := &Provenance{Stage: stage, Groups: slices.Clone(groups), Scope: definition.Scope, Overrides: overrides}
	if definition.Scope == ScopeGlobal {
		from.File = definition.File
	}
	return from
}

// expandGroup expands a group used from inside outer, returning its stages
// with ids unique within it, and its exits.
func (document Document) expandGroup(definition Definition, outer []string) ([]item, []string, error) {
	name := definition.Name
	if start := slices.Index(outer, name); start >= 0 {
		if start == len(outer)-1 {
			return nil, nil, fmt.Errorf("group %q uses itself", name)
		}
		cycle := append(slices.Clone(outer[start:]), name)
		return nil, nil, fmt.Errorf("group %q uses itself: %s", name, strings.Join(cycle, " → "))
	}
	if len(definition.Group.Stages) == 0 {
		return nil, nil, fmt.Errorf("%s has no stages", definition.describe())
	}
	items, err := document.expandEntries(definition.Group.Stages, append(slices.Clone(outer), name), definition)
	if err != nil {
		return nil, nil, err
	}
	depended := make(map[string]bool)
	for _, item := range items {
		for _, dependency := range item.stage.DependsOn {
			depended[dependency] = true
		}
	}
	var exits []string
	for _, item := range items {
		if !depended[item.stage.ID] {
			exits = append(exits, item.stage.ID)
		}
	}
	return items, exits, nil
}

// prefixGroupStages gives a used group's stage its use's id as a prefix,
// "<use id>.<stage id>", when its own id is not unique among the entries:
// another entry has it, or a stage another group brings, or a use's id.
func prefixGroupStages(units []unit) {
	count := make(map[string]int)
	for _, expanded := range units {
		if expanded.handle != "" {
			count[expanded.handle]++
		}
		for _, item := range expanded.items {
			count[item.stage.ID]++
		}
	}
	for index := range units {
		expanded := &units[index]
		if expanded.handle == "" {
			continue
		}
		renamed := make(map[string]string)
		for _, item := range expanded.items {
			if count[item.stage.ID] > 1 {
				renamed[item.stage.ID] = expanded.handle + "." + item.stage.ID
			}
		}
		if len(renamed) == 0 {
			continue
		}
		rename := func(id string) string {
			if to, ok := renamed[id]; ok {
				return to
			}
			return id
		}
		// A group's stages depend only on each other until the use's own
		// dependsOn is attached, so every dependency is renamed with them.
		for position := range expanded.items {
			stage := &expanded.items[position].stage
			stage.ID = rename(stage.ID)
			for at, dependency := range stage.DependsOn {
				stage.DependsOn[at] = rename(dependency)
			}
			if expanded.items[position].local {
				stage.Feedback.Target = rename(stage.Feedback.Target)
			}
		}
		for at, exit := range expanded.exits {
			expanded.exits[at] = rename(exit)
		}
	}
}

// checkDuplicates refuses two entries that end up with the same id. In a
// flow, two inline stages with the same id are left for Flow.Validate, so a
// flow without uses fails exactly as it always has.
func checkDuplicates(units []unit, owner string, inGroup bool) error {
	type giver struct {
		label string
		use   bool
	}
	seen := make(map[string]giver)
	for _, expanded := range units {
		ids := make([]string, 0, len(expanded.items)+1)
		if expanded.handle != "" {
			ids = append(ids, expanded.handle)
		}
		for _, item := range expanded.items {
			ids = append(ids, item.stage.ID)
		}
		for _, id := range ids {
			first, taken := seen[id]
			if !taken {
				seen[id] = giver{label: expanded.label, use: expanded.use}
				continue
			}
			if !inGroup && !first.use && !expanded.use {
				continue
			}
			return entryError{entry: expanded.index, err: fmt.Errorf("%s%s and %s both give the id %q; set id on one of them", owner, first.label, expanded.label, id)}
		}
	}
	return nil
}

// wire resolves each entry's dependsOn. A name a used group's id stands for
// becomes that group's exits, and a used group's own dependsOn is attached
// to its stages that depend on nothing. In a group every name must be an
// earlier entry of the group; in a flow a name that is not is kept, and
// Flow.Validate refuses it.
func wire(units []unit, owner string, inGroup bool) error {
	known := make(map[string][]string)
	for index := range units {
		expanded := &units[index]
		// Slicing keeps a nil dependsOn nil and an empty one empty, so a flow
		// without uses expands to exactly the flow it was.
		resolved := expanded.dependsOn[:0:0]
		for _, name := range expanded.dependsOn {
			ids, ok := known[name]
			switch {
			case ok && len(ids) == 1 && ids[0] == name:
				resolved = append(resolved, name)
			case ok:
				for _, id := range ids {
					if !slices.Contains(resolved, id) {
						resolved = append(resolved, id)
					}
				}
			case inGroup:
				return entryError{entry: expanded.index, err: fmt.Errorf("%s%s depends on %q, which is not an earlier entry of the group", owner, expanded.label, name)}
			default:
				resolved = append(resolved, name)
			}
		}
		if expanded.handle == "" {
			expanded.items[0].stage.DependsOn = resolved
			known[expanded.items[0].stage.ID] = []string{expanded.items[0].stage.ID}
			continue
		}
		for position := range expanded.items {
			stage := &expanded.items[position].stage
			if len(stage.DependsOn) == 0 {
				stage.DependsOn = slices.Clone(resolved)
			}
			known[stage.ID] = []string{stage.ID}
		}
		known[expanded.handle] = expanded.exits
	}
	return nil
}

// resolveLocalTargets marks the feedback targets in a group that name one
// of its stages, so they follow the stage when a use renames it. A target
// naming no stage of the group names one of the flow it is used in.
func resolveLocalTargets(items []item) {
	ids := make(map[string]bool, len(items))
	for _, item := range items {
		ids[item.stage.ID] = true
	}
	for index := range items {
		feedback := items[index].stage.Feedback
		if feedback != nil && feedback.Target != "" && ids[feedback.Target] {
			items[index].local = true
		}
	}
}

// entryLabel names a flow or group entry in an error.
func entryLabel(index int, entry Stage) string {
	switch {
	case entry.Use != "" && entry.ID != "":
		return fmt.Sprintf("entry %d (use %q, id %q)", index+1, entry.Use, entry.ID)
	case entry.Use != "":
		return fmt.Sprintf("entry %d (use %q)", index+1, entry.Use)
	case entry.ID != "":
		return fmt.Sprintf("entry %d (stage %q)", index+1, entry.ID)
	default:
		return fmt.Sprintf("entry %d", index+1)
	}
}

// article names a stage kind with its indefinite article.
func article(kind domain.StepKind) string {
	if kind == domain.StepAgent || kind == domain.StepApproval || kind == domain.StepEvaluation {
		return "an " + string(kind)
	}
	return "a " + string(kind)
}

// validateLibrary checks every shared stage and group on its own, used or
// not, in name order so the first mistake reported is always the same one.
func (document Document) validateLibrary() error {
	for _, name := range sortedNames(document.Stages) {
		if err := document.validateSharedStage(name); err != nil {
			return &DefinitionError{Name: name, Err: err}
		}
	}
	for _, name := range sortedNames(document.Groups) {
		if err := document.validateGroup(name); err != nil {
			return &DefinitionError{Name: name, Err: err}
		}
	}
	return nil
}

// validateSharedStage checks the document's shared stage name on its own.
func (document Document) validateSharedStage(name string) error {
	stage := document.Stages[name]
	label := fmt.Sprintf("shared stage %q", name)
	switch {
	case strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name:
		return fmt.Errorf("%s needs a name that is not blank and does not start or end with a space", label)
	case document.Groups[name].Stages != nil:
		return fmt.Errorf("%q is both a shared stage and a group; give them different names", name)
	case stage.Use != "":
		return fmt.Errorf("%s uses %q; a shared stage is written out in full, and a group is how stages are combined", label, stage.Use)
	case stage.ID != "":
		return fmt.Errorf("%s sets id; its name is its id, and a use can give it another", label)
	case len(stage.DependsOn) > 0:
		return fmt.Errorf("%s sets dependsOn; each use wires it into its flow", label)
	case len(stage.Overrides) > 0:
		return fmt.Errorf("%s lists overrides, which only a use has", label)
	}
	stage.ID = name
	if err := stage.validate(); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// validateGroup checks the document's group name on its own, expanded.
func (document Document) validateGroup(name string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
		return fmt.Errorf("group %q needs a name that is not blank and does not start or end with a space", name)
	}
	if _, ok := document.Stages[name]; ok {
		return fmt.Errorf("%q is both a shared stage and a group; give them different names", name)
	}
	group := document.Groups[name]
	items, _, err := document.expandGroup(Definition{Name: name, Scope: ScopeRepository, File: document.Source, Group: &group}, nil)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := item.stage.validate(); err != nil {
			return fmt.Errorf("group %q: %w", name, err)
		}
	}
	return nil
}

// sortedNames is the keys of definitions in order, so errors about shared
// stages and groups come out the same way every time.
func sortedNames[V any](definitions map[string]V) []string {
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// markOverrides records on every use which overridable fields the file
// writes, so one written as false or empty still overrides its definition
// and is written back.
func markOverrides(document *Document, root *yaml.Node) {
	top := resolveNode(root)
	if rules := field(top, "rules"); rules != nil && rules.Kind == yaml.SequenceNode && len(rules.Content) == len(document.Rules) {
		for index, rule := range rules.Content {
			markFlow(document.Rules[index].Then.Stages, field(field(rule, "then"), "stages"))
		}
	}
	if document.Default != nil {
		markFlow(document.Default.Stages, field(field(top, "default"), "stages"))
	}
	if groups := field(top, "groups"); groups != nil && groups.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(groups.Content); index += 2 {
			if group, ok := document.Groups[groups.Content[index].Value]; ok {
				markFlow(group.Stages, field(groups.Content[index+1], "stages"))
			}
		}
	}
}

// markFlow records the overrides each use among stages writes, reading
// them from the sequence node the stages were decoded from.
func markFlow(stages []Stage, node *yaml.Node) {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) != len(stages) {
		return
	}
	for index, entry := range node.Content {
		entry = resolveNode(entry)
		if stages[index].Use == "" || entry == nil || entry.Kind != yaml.MappingNode {
			continue
		}
		written := make(map[string]bool, len(entry.Content)/2)
		for at := 0; at+1 < len(entry.Content); at += 2 {
			written[entry.Content[at].Value] = true
		}
		var overrides []string
		for _, field := range overridable {
			if written[field.name] {
				overrides = append(overrides, field.name)
			}
		}
		stages[index].Overrides = overrides
	}
}

// resolveNode follows document nodes and aliases to the node they stand for.
func resolveNode(node *yaml.Node) *yaml.Node {
	for node != nil {
		switch node.Kind {
		case yaml.DocumentNode:
			if len(node.Content) == 0 {
				return nil
			}
			node = node.Content[0]
		case yaml.AliasNode:
			node = node.Alias
		default:
			return node
		}
	}
	return nil
}

// field returns the value under key in a mapping node, or nil.
func field(node *yaml.Node, key string) *yaml.Node {
	node = resolveNode(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return resolveNode(node.Content[index+1])
		}
	}
	return nil
}
