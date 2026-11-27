// Package catalog holds the agents, skills, rules, shared stages, and
// groups that ship with Inkuna, or that a definition source offers, for a
// repository, or this machine, to adopt. Nothing in
// it is used until it is added: Add copies an item into .inkuna/ or the
// global directories, where it is reviewed like code and edited like any
// other file, and a later Inkuna never changes that copy underneath.
package catalog

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
	"testing/fstest"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/skill"
	"gopkg.in/yaml.v3"
)

// items is the catalog that ships in the binary. Its README.md says how an
// item is written.
//
//go:embed items
var items embed.FS

// Kind is what an item is.
type Kind string

// The kinds of item, each kept in its own directory of the catalog.
const (
	KindAgent Kind = "agent"
	KindRule  Kind = "rule"
	KindStage Kind = "stage"
	KindGroup Kind = "group"
	KindSkill Kind = "skill"
)

// layout is where each kind lives in the catalog and the extension its
// files have; a skill is a directory. Agents come first and rules last, so
// the stages and groups a rule uses are known when it is read.
var layout = []struct {
	kind      Kind
	directory string
	extension string
}{
	{KindAgent, "agents", ".md"},
	{KindSkill, "skills", ""},
	{KindStage, "stages", ".yaml"},
	{KindGroup, "groups", ".yaml"},
	{KindRule, "rules", ".yaml"},
}

// OriginKey is the frontmatter key an added agent records its catalog item
// under. Inkuna does not read it, so it is kept when the agent is saved.
const OriginKey = "catalog"

// The frontmatter keys an agent added from a definition source records the
// source, the scope it was shared with, and its version under, beside
// OriginKey, so a copy can be traced to what it was copied from.
const (
	SourceKey  = "catalogSource"
	ScopeKey   = "catalogScope"
	VersionKey = "catalogVersion"
)

// Needs is what an item relies on, directly or through the shared stages
// and groups it uses, built-in ones included.
type Needs struct {
	// Agents are the agents its stages name.
	Agents []string `json:"agents,omitempty"`
	// Stages are the shared stages and groups it uses.
	Stages []string `json:"stages,omitempty"`
	// Skills are the catalog's skills its agents name.
	Skills []string `json:"skills,omitempty"`
}

// Item is one thing the catalog offers.
type Item struct {
	Name        string `json:"name"`
	Kind        Kind   `json:"kind"`
	Description string `json:"description"`
	Needs       Needs  `json:"needs"`
	// Permission is what an agent may do; empty for the other kinds.
	Permission domain.Permission `json:"permission,omitempty"`
	// TaskKinds are the kinds of task an agent is for, or the kinds a
	// rule's condition names; empty when it names none.
	TaskKinds []domain.TaskKind `json:"taskKinds,omitempty"`
	// Path is the item's file within the catalog.
	Path string `json:"path"`
	// Content is the item's file as the catalog has it; a skill's SKILL.md.
	Content string `json:"content"`
	// Files are a skill's other files, by their path in its directory.
	Files []string `json:"files,omitempty"`
	// Source is the definition source that offers the item, such as
	// "example/source"; empty for the catalog that ships in Inkuna.
	Source string `json:"source,omitempty"`
	// Scope is who the source shares the item with, as it names them, and
	// Version is the item's version there.
	Scope   string `json:"scope,omitempty"`
	Version string `json:"version,omitempty"`

	// direct is what the item itself names, without what those bring in;
	// Add follows it so that a need the target already has is not looked
	// into further.
	direct     Needs
	agent      agent.Definition
	definition rules.Definition
	// node is a rule's or definition's YAML as the catalog file writes it,
	// so adding it keeps its layout.
	node *yaml.Node
	// version is the lowest rules version the item needs.
	version int
	// files are a skill's files, SKILL.md included.
	files map[string][]byte
	// index is the place of a source's definition, as entry has it.
	index int
}

// Catalog is a loaded, checked set of items.
type Catalog struct {
	items map[string]Item
	// library is the built-in library with the catalog's shared stages and
	// groups, which every catalog rule and group resolves against.
	library rules.Library
}

// Builtin loads the catalog that ships in the binary.
func Builtin() (Catalog, error) {
	sub, err := fs.Sub(items, "items")
	if err != nil {
		return Catalog{}, fmt.Errorf("load catalog: %w", err)
	}
	return Load(sub)
}

// entry is one item's files before they are checked: a file for most
// kinds, and a skill's whole directory.
type entry struct {
	kind Kind
	name string
	// path is where the item is within its catalog, as errors name it.
	path string
	data []byte
	// files are a skill's files by their path in its directory.
	files  map[string][]byte
	origin origin
	// index is the entry's place among the definitions a source gave, so a
	// problem names the one it is about even when two share a name.
	index int
}

// origin is where an item came from: the built-in catalog, with every
// field empty, or a definition source.
type origin struct {
	source, scope, version string
}

// Load reads and checks a catalog: each kind's directory holding only its
// own files, each decoded strictly and named as its file, no two items
// sharing a name or taking a built-in one, and everything an item needs
// defined by the catalog or built in. Any problem fails the whole catalog.
func Load(fsys fs.FS) (Catalog, error) {
	var entries []entry
	for _, kind := range layout {
		files, err := fs.ReadDir(fsys, kind.directory)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Catalog{}, fmt.Errorf("read catalog %s: %w", kind.directory, err)
		}
		for _, file := range files {
			filePath := path.Join(kind.directory, file.Name())
			if kind.kind == KindSkill {
				skillEntry, err := readSkill(fsys, filePath, file)
				if err != nil {
					return Catalog{}, err
				}
				entries = append(entries, skillEntry)
				continue
			}
			name, found := strings.CutSuffix(file.Name(), kind.extension)
			if file.IsDir() || !found {
				return Catalog{}, fmt.Errorf("catalog %s holds %s; it may hold only %s files, one %s each", kind.directory, filePath, kind.extension, kind.kind)
			}
			data, err := fs.ReadFile(fsys, filePath)
			if err != nil {
				return Catalog{}, fmt.Errorf("read catalog item %s: %w", filePath, err)
			}
			entries = append(entries, entry{kind: kind.kind, name: name, path: filePath, data: data})
		}
	}
	catalog, _, err := build(entries, false)
	return catalog, err
}

// readSkill reads a skill's directory in the catalog.
func readSkill(fsys fs.FS, directory string, file fs.DirEntry) (entry, error) {
	if !file.IsDir() {
		return entry{}, fmt.Errorf("catalog skills holds %s; it may hold only directories, one skill each", directory)
	}
	skillEntry := entry{kind: KindSkill, name: file.Name(), path: path.Join(directory, "SKILL.md"), files: map[string][]byte{}}
	err := fs.WalkDir(fsys, directory, func(name string, found fs.DirEntry, err error) error {
		if err != nil || found.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		skillEntry.files[strings.TrimPrefix(name, directory+"/")] = data
		return nil
	})
	if err != nil {
		return entry{}, fmt.Errorf("read catalog item %s: %w", directory, err)
	}
	return skillEntry, nil
}

// Problem is a source's definition that does not load, which its catalog
// leaves out.
type Problem struct {
	Kind   Kind   `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// Definition is the definition's place in the list FromSource was
	// given, which tells apart two that share a kind and a name.
	Definition int `json:"-"`
}

// loader builds a catalog from its entries. A strict loader, for the
// built-in catalog, refuses the whole catalog on the first problem; a
// lenient one, for a definition source, leaves out each item that has a
// problem, and every item that needs one, and reports why.
type loader struct {
	lenient  bool
	catalog  Catalog
	problems []Problem
}

// refuse records an entry's problem: the error a strict loader returns,
// or the reason a lenient one reports.
func (loader *loader) refuse(item entry, strict error, reason string) error {
	if !loader.lenient {
		return strict
	}
	loader.problems = append(loader.problems, Problem{Kind: item.kind, Name: item.name, Reason: reason, Definition: item.index})
	return nil
}

// build checks entries, given in the catalog's layout order, as a catalog.
func build(entries []entry, lenient bool) (Catalog, []Problem, error) {
	loader := &loader{lenient: lenient, catalog: Catalog{items: make(map[string]Item), library: rules.Builtin()}}
	catalog := &loader.catalog
	reserved := builtinNames()
	seen := make(map[string]string)
	checked := false
	for _, current := range entries {
		// A group may use a stage or another group of the catalog, so the
		// definitions are checked once all of them are in the library, and
		// before a rule uses them.
		if current.kind == KindRule && !checked {
			checked = true
			if err := loader.checkDefinitions(); err != nil {
				return Catalog{}, nil, err
			}
		}
		if current.kind != KindAgent && current.kind != KindSkill {
			if err := refuseAliases(current.data); err != nil {
				if err := loader.refuse(current, fmt.Errorf("catalog item %s: %w", current.path, err), err.Error()); err != nil {
					return Catalog{}, nil, err
				}
				continue
			}
		}
		item, err := catalog.parse(current)
		if err != nil {
			if err := loader.refuse(current, fmt.Errorf("catalog item %s: %w", current.path, err), err.Error()); err != nil {
				return Catalog{}, nil, err
			}
			continue
		}
		// Names are compared without case: rules are chosen by name
		// ignoring it, so a rule "Bugfix" would stand in for "bugfix".
		folded := strings.ToLower(current.name)
		if existing, taken := seen[folded]; taken {
			strict := fmt.Errorf("catalog items %s and %s are both named %q; names are shared by every kind and compared without case, so `add` can find one", existing, current.path, current.name)
			if err := loader.refuse(current, strict, fmt.Sprintf("another of the source's definitions is named %q; names are shared by every kind and compared without case", current.name)); err != nil {
				return Catalog{}, nil, err
			}
			continue
		}
		seen[folded] = current.path
		if what, taken := reserved[folded]; taken {
			strict := fmt.Errorf("catalog item %s is named %q, which is the name of %s", current.path, current.name, what)
			if err := loader.refuse(current, strict, fmt.Sprintf("it is named %q, which is the name of %s", current.name, what)); err != nil {
				return Catalog{}, nil, err
			}
			continue
		}
		catalog.items[current.name] = item
		if item.Kind == KindStage || item.Kind == KindGroup {
			catalog.library.Definitions = append(catalog.library.Definitions, item.definition)
		}
	}
	if !checked {
		if err := loader.checkDefinitions(); err != nil {
			return Catalog{}, nil, err
		}
	}
	if err := loader.resolveNeeds(); err != nil {
		return Catalog{}, nil, err
	}
	return loader.catalog, loader.problems, nil
}

// parse reads one item's file as its kind.
func (catalog Catalog) parse(current entry) (Item, error) {
	kind, name := current.kind, current.name
	item := Item{Name: name, Kind: kind, Path: current.path, Content: string(current.data), Source: current.origin.source, Scope: current.origin.scope, Version: current.origin.version, index: current.index}
	// A source's definitions are named in errors by the source, so a
	// problem in one is not taken for the built-in catalog's.
	label := "catalog"
	if current.origin.source != "" {
		label = current.origin.source
	}
	switch kind {
	case KindAgent:
		definition, err := agent.Parse(string(current.data))
		if err != nil {
			return Item{}, err
		}
		if definition.Name != name {
			return Item{}, fmt.Errorf("agent name %q must match file name %q", definition.Name, name)
		}
		// Frontmatter is lenient so a file can serve other tools too; the
		// catalog's own files serve only Inkuna, so a key it does not read is
		// a mistake.
		if definition.Extra != "" {
			return Item{}, fmt.Errorf("agent %q has frontmatter keys Inkuna does not read:\n%s", name, definition.Extra)
		}
		definition.Source = agent.SourceRepository
		item.agent, item.Description = definition, definition.Description
		item.Permission, item.TaskKinds = definition.Permission, definition.TaskKinds
		// An agent brings the skills it names that the catalog has; one it
		// does not have may be the repository's own.
		item.direct = Needs{Skills: slices.Clone(definition.Skills)}
	case KindSkill:
		files := make(fstest.MapFS, len(current.files))
		for filePath, data := range current.files {
			files[filePath] = &fstest.MapFile{Data: data, Mode: 0o644}
		}
		pkg, err := skill.LoadFS(files, name)
		if err != nil {
			return Item{}, err
		}
		item.Description, item.TaskKinds = pkg.Description, pkg.Manifest.TaskKinds
		item.Content = string(current.files["SKILL.md"])
		for filePath := range current.files {
			if filePath != "SKILL.md" {
				item.Files = append(item.Files, filePath)
			}
		}
		sort.Strings(item.Files)
		item.files = current.files
	case KindStage, KindGroup:
		definitions, err := rules.ParseLibrary(current.data, label+"/"+current.path)
		if err != nil {
			return Item{}, err
		}
		if len(definitions) != 1 || definitions[0].Name != name || Kind(definitions[0].Kind()) != kind {
			return Item{}, fmt.Errorf("a %s item defines exactly one %s, named %q as its file is", kind, kind, name)
		}
		item.definition = definitions[0]
		item.version = 2
		root, err := parseNode(current.data)
		if err != nil {
			return Item{}, err
		}
		section := "stages"
		if kind == KindGroup {
			section = "groups"
		}
		item.node = mappingValue(mappingValue(root, section), name)
		item.Description = headComment(root)
		if item.Description == "" {
			return Item{}, fmt.Errorf("%s %q needs a description: a comment at the top of the file", kind, name)
		}
		if item.definition.Stage != nil {
			item.direct = stagesNeeds([]rules.Stage{*item.definition.Stage})
		} else {
			item.direct = stagesNeeds(item.definition.Group.Stages)
		}
	case KindRule:
		rule, version, node, err := catalog.parseRule(name, current.data)
		if err != nil {
			return Item{}, err
		}
		item.version, item.node = version, node
		item.Description = strings.TrimSpace(rule.Description)
		item.TaskKinds = rule.When.Kinds
		item.direct = stagesNeeds(rule.Then.Stages)
		if item.Description == "" {
			return Item{}, fmt.Errorf("rule %q needs a description", name)
		}
		if !narrows(rule.When) {
			// Add puts a rule first, where one that matches every task would
			// take every flow the repository had.
			return Item{}, fmt.Errorf("rule %q matches every task; a catalog rule needs a when that narrows it", name)
		}
	default:
		return Item{}, fmt.Errorf("no kind of definition is named %q", kind)
	}
	return item, nil
}

// taskKinds are every kind a task can have. The rules refuse any other, so
// a condition listing all of them holds for every task.
var taskKinds = []domain.TaskKind{domain.TaskResearch, domain.TaskBugfix, domain.TaskImplementation, domain.TaskReview, domain.TaskDocumentation, domain.TaskSkillAuthoring}

// narrows reports whether a condition leaves some task out, as
// Condition.Matches reads it. Some conditions hold for every task: every
// task kind, an empty keyword (every text contains it), a complexity range
// of 1 to 10, or a minPriority of 0 or less.
func narrows(when rules.Condition) bool {
	kinds := make(map[domain.TaskKind]bool, len(when.Kinds))
	for _, kind := range when.Kinds {
		kinds[kind] = true
	}
	switch {
	case len(kinds) > 0 && len(kinds) < len(taskKinds):
		return true
	case len(when.Keywords) > 0 && !slices.Contains(when.Keywords, ""):
		return true
	case len(when.Labels) > 0, len(when.Sources) > 0, len(when.Repositories) > 0:
		return true
	case when.Complexity != nil && (when.Complexity.Min > 1 || when.Complexity.Max < 10):
		return true
	}
	return when.MinPriority > 0
}

// ruleFile is the shape of a rule item: a rules document holding exactly
// one rule and nothing else. Its shared stages and groups are items of
// their own, so each is added, or found already there, by its own name.
type ruleFile struct {
	Version int         `yaml:"version"`
	Rules   []yaml.Node `yaml:"rules"`
	Stages  yaml.Node   `yaml:"stages"`
	Groups  yaml.Node   `yaml:"groups"`
	Default yaml.Node   `yaml:"default"`
}

// parseRule reads a rule item strictly, as a rules file, with its uses
// resolved against the built-in library and the catalog's definitions.
func (catalog Catalog) parseRule(name string, data []byte) (rules.Rule, int, *yaml.Node, error) {
	var shape ruleFile
	if err := yaml.Unmarshal(data, &shape); err != nil {
		return rules.Rule{}, 0, nil, fmt.Errorf("parse rules: %w", err)
	}
	if len(shape.Rules) != 1 || shape.Stages.Kind != 0 || shape.Groups.Kind != 0 || shape.Default.Kind != 0 {
		return rules.Rule{}, 0, nil, errors.New("a rule item holds exactly one rule, and no stages, groups, or default flow; a shared stage or group it uses is an item of its own")
	}
	document, err := rules.ParseWith(string(data), catalog.library)
	if err != nil {
		return rules.Rule{}, 0, nil, err
	}
	rule := document.Rules[0]
	if rule.Name != name {
		return rules.Rule{}, 0, nil, fmt.Errorf("rule name %q must match file name %q", rule.Name, name)
	}
	// The default flow parsing filled in is the built-in one, expanded, so
	// the version needed is the rule's own.
	document.Default = nil
	node := shape.Rules[0]
	return rule, document.RequiredVersion(), &node, nil
}

// checkDefinitions checks each of the catalog's shared stages and groups
// as a rules file's own are checked, against the built-in library and each
// other. A lenient loader takes out each that fails, and checks the rest
// again, since a group may have relied on one.
func (loader *loader) checkDefinitions() error {
	catalog := &loader.catalog
	for {
		removed := false
		for _, name := range catalog.names() {
			item := catalog.items[name]
			if item.Kind != KindStage && item.Kind != KindGroup {
				continue
			}
			err := catalog.library.Check(item.definition)
			if err == nil {
				continue
			}
			if err := loader.refuse(entry{kind: item.Kind, name: name, index: item.index}, fmt.Errorf("catalog item %s: %w", item.Path, err), err.Error()); err != nil {
				return err
			}
			loader.remove(name)
			removed = true
		}
		if !removed {
			return nil
		}
	}
}

// resolveNeeds works out what each item needs. A lenient loader takes out
// each item whose needs are missing, and works the rest out again, since
// one may have needed what was taken out.
func (loader *loader) resolveNeeds() error {
	catalog := &loader.catalog
	for {
		removed := false
		for _, name := range catalog.names() {
			item := catalog.items[name]
			needs, err := catalog.needs(item)
			if err != nil {
				if err := loader.refuse(entry{kind: item.Kind, name: name, index: item.index}, fmt.Errorf("catalog item %s: %w", item.Path, err), err.Error()); err != nil {
					return err
				}
				loader.remove(name)
				removed = true
				continue
			}
			item.Needs = needs
			catalog.items[name] = item
		}
		if !removed {
			return nil
		}
	}
}

// remove takes an item out of the catalog, and out of the library a rule
// or group resolves against.
func (loader *loader) remove(name string) {
	delete(loader.catalog.items, name)
	loader.catalog.library.Definitions = slices.DeleteFunc(slices.Clone(loader.catalog.library.Definitions), func(definition rules.Definition) bool {
		return definition.Name == name
	})
}

// needs follows an item's direct needs through the shared stages, groups,
// and agents they use, and checks that every agent and definition is the
// catalog's or built in. A skill an agent names is a need only when the
// catalog has it.
func (catalog Catalog) needs(item Item) (Needs, error) {
	builtinAgents := make(map[string]bool)
	for _, definition := range agent.BuiltIn() {
		builtinAgents[definition.Name] = true
	}
	agents, stages, skills := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	var follow func(Needs) error
	follow = func(direct Needs) error {
		for _, name := range direct.Agents {
			used, ok := catalog.items[name]
			if !builtinAgents[name] && (!ok || used.Kind != KindAgent) {
				return fmt.Errorf("names agent %q, which is neither built in nor a catalog agent", name)
			}
			if agents[name] {
				continue
			}
			agents[name] = true
			if ok && used.Kind == KindAgent {
				if err := follow(used.direct); err != nil {
					return err
				}
			}
		}
		for _, name := range direct.Skills {
			if used, ok := catalog.items[name]; ok && used.Kind == KindSkill {
				skills[name] = true
			}
		}
		for _, name := range direct.Stages {
			if stages[name] {
				continue
			}
			stages[name] = true
			if used, ok := catalog.items[name]; ok && (used.Kind == KindStage || used.Kind == KindGroup) {
				if err := follow(used.direct); err != nil {
					return err
				}
				continue
			}
			definition, err := rules.Builtin().Resolve(name)
			if err != nil {
				return fmt.Errorf("uses %q, which is neither a built-in nor a catalog shared stage or group", name)
			}
			if definition.Group != nil {
				if err := follow(stagesNeeds(definition.Group.Stages)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := follow(item.direct); err != nil {
		return Needs{}, err
	}
	return Needs{Agents: sortedKeys(agents), Stages: sortedKeys(stages), Skills: sortedKeys(skills)}, nil
}

// stagesNeeds is what a run of stages names itself: the agents its stages
// are given and the shared stages and groups it uses.
func stagesNeeds(stages []rules.Stage) Needs {
	agents, uses := make(map[string]bool), make(map[string]bool)
	for _, stage := range stages {
		// auto is the orchestrator's choice and human the user, who is no
		// agent to add; the rules editor passes both the same way.
		if stage.Agent != "" && stage.Agent != agent.Auto && stage.Agent != agent.Human {
			agents[stage.Agent] = true
		}
		if stage.Use != "" {
			uses[stage.Use] = true
		}
	}
	return Needs{Agents: sortedKeys(agents), Stages: sortedKeys(uses)}
}

// Items lists every item, in name order.
func (catalog Catalog) Items() []Item {
	result := make([]Item, 0, len(catalog.items))
	for _, name := range catalog.names() {
		result = append(result, catalog.items[name])
	}
	return result
}

// Get returns the item with name.
func (catalog Catalog) Get(name string) (Item, bool) {
	item, ok := catalog.items[name]
	return item, ok
}

// Agents returns the catalog's agents, for a registry that has them all.
func (catalog Catalog) Agents() []agent.Definition {
	var definitions []agent.Definition
	for _, item := range catalog.Items() {
		if item.Kind == KindAgent {
			definitions = append(definitions, item.agent)
		}
	}
	return definitions
}

func (catalog Catalog) names() []string { return sortedKeys(catalog.items) }

// builtinNames maps every name the catalog may not take, in lower case, to
// what has it:
// the built-in agents and the reserved agent names, the built-in rules and
// the default flow, and the built-in shared stages and groups.
func builtinNames() map[string]string {
	names := map[string]string{
		agent.Auto: "the reserved agent name", agent.Human: "the reserved agent name",
		rules.DefaultName: "the default flow",
	}
	for _, definition := range agent.BuiltIn() {
		names[definition.Name] = "a built-in agent"
	}
	for _, rule := range rules.Default().Rules {
		names[rule.Name] = "a built-in rule"
	}
	for _, definition := range rules.Builtin().Definitions {
		names[definition.Name] = "a built-in " + definition.Kind()
	}
	folded := make(map[string]string, len(names))
	for name, what := range names {
		folded[strings.ToLower(name)] = what
	}
	return folded
}

func sortedKeys[V any](set map[string]V) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// parseNode parses YAML into its document node.
func parseNode(data []byte) (*yaml.Node, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	return &root, nil
}

// mappingValue is the value under key in a mapping, or the mapping a
// document holds; nil when there is none.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

// headComment is the comment a YAML file opens with, as one line of text.
// The parser keeps it on the document when a blank line follows it, and
// on the first key otherwise.
func headComment(root *yaml.Node) string {
	comment := root.HeadComment
	if comment == "" && len(root.Content) > 0 {
		top := root.Content[0]
		comment = top.HeadComment
		if comment == "" && top.Kind == yaml.MappingNode && len(top.Content) > 0 {
			comment = top.Content[0].HeadComment
		}
	}
	lines := strings.Split(comment, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
	}
	return strings.Join(slices.DeleteFunc(lines, func(line string) bool { return line == "" }), " ")
}

// refuseAliases refuses YAML anchors and aliases: Add copies a rule or
// definition out of its file on its own, where an alias would lose the
// anchor it names.
func refuseAliases(data []byte) error {
	root, err := parseNode(data)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	var walk func(*yaml.Node) error
	walk = func(node *yaml.Node) error {
		if node.Kind == yaml.AliasNode || node.Anchor != "" {
			return errors.New("a catalog item may not use YAML anchors or aliases, so each part of it can be copied on its own")
		}
		for _, child := range node.Content {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}
