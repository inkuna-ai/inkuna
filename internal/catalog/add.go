package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"gopkg.in/yaml.v3"
)

// Target says where Add copies an item, and where to look for what is
// already there.
type Target struct {
	// Global copies into this machine's global directories rather than a
	// repository. A rule has no global scope, so it cannot be added so.
	Global bool
	// RulesPath and AgentDirectory are the repository's rules file and
	// agents directory. Global ignores them.
	RulesPath      string
	AgentDirectory string
	// SkillDirectory is the repository's skills directory, needed only to
	// add a skill. Global ignores it.
	SkillDirectory string
	// AgentDirectories and StageDirectories are the global agent and stage
	// library directories from configuration, lowest precedence first. A
	// global add writes to the first of each.
	AgentDirectories []string
	StageDirectories []string
}

// Added is one file Add wrote, or the rules file it added to.
type Added struct {
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// Result is what Add did.
type Result struct {
	Item string `json:"item"`
	// Added lists the item and each agent, shared stage, and group it
	// needed that the target lacked, in the order they were added.
	Added []Added `json:"added"`
	// Notes say what else the addition changes, such as a rule placed
	// first or a global definition a repository copy now replaces.
	Notes []string `json:"notes,omitempty"`
}

// ExistsError is Add refusing an item because the target already has
// something of its kind by its name.
type ExistsError struct {
	Kind Kind
	Name string
	// Where is the file that has it.
	Where string
}

func (err *ExistsError) Error() string {
	return fmt.Sprintf("%s already has %s %q; adding never overwrites, so rename or remove that one first", err.Where, label(err.Kind), err.Name)
}

// Add copies the named item into the target, with every agent, shared
// stage, and group it needs that the target lacks. A target has an agent
// that is built in or has a file in its own directory, and a shared stage
// or group that is built in or defined in its own rules or stage library;
// what it has is used as it is, and not looked into further. Add never
// overwrites: an item whose name the target already uses is refused, naming
// where. Everything is checked before anything is written, and a write
// that fails takes back the files written before it.
func (catalog Catalog) Add(name string, target Target) (Result, error) {
	return catalog.add(name, target, false)
}

// Preview is what Add would do now, checked as Add checks it, without
// writing anything: what it would add, its notes, or why it would refuse.
// Add checks again when it runs, since the target may change in between.
func (catalog Catalog) Preview(name string, target Target) (Result, error) {
	return catalog.add(name, target, true)
}

// Presence maps the name of each item the target already has something of
// its kind by to where: a file, or "built in". A repository whose rules
// file does not load is refused, as Add refuses it.
func (catalog Catalog) Presence(target Target) (map[string]string, error) {
	place, err := open(target)
	if err != nil {
		return nil, err
	}
	present := make(map[string]string)
	for _, name := range catalog.names() {
		item := catalog.items[name]
		if target.Global && (item.Kind == KindRule || item.Kind == KindSkill) {
			continue
		}
		where, err := place.has(item.Kind, name)
		if err != nil {
			return nil, err
		}
		if where != "" {
			present[name] = where
		}
	}
	return present, nil
}

// GlobalRefusal is why the item cannot be added to the global directories,
// or nil when it can be.
func (item Item) GlobalRefusal() error {
	switch {
	case item.Kind == KindRule:
		return fmt.Errorf("%q is a rule, and rules live in a repository's rules file; add it to a repository", item.Name)
	case item.Kind == KindSkill:
		return fmt.Errorf("%q is a skill, and skills live in a repository's .inkuna/skills; add it to a repository", item.Name)
	case item.Source != "":
		// A source shares a definition for one project; the global
		// directories would give it to every project on this machine.
		return fmt.Errorf("%q comes from definition source %s, which shares it for this project; add it to the repository", item.Name, item.Source)
	}
	return nil
}

// add plans an addition and, unless dryRun, writes it.
func (catalog Catalog) add(name string, target Target, dryRun bool) (Result, error) {
	item, ok := catalog.items[name]
	if !ok {
		return Result{}, fmt.Errorf("the catalog has no item %q", name)
	}
	if target.Global {
		if err := item.GlobalRefusal(); err != nil {
			return Result{}, err
		}
	}
	place, err := open(target)
	if err != nil {
		return Result{}, err
	}
	place.dryRun = dryRun
	where, err := place.has(item.Kind, name)
	if err != nil {
		return Result{}, err
	}
	if where != "" {
		return Result{}, &ExistsError{Kind: item.Kind, Name: name, Where: where}
	}
	plan := []Item{item}
	planned := map[string]bool{name: true}
	var follow func(Needs) error
	follow = func(direct Needs) error {
		type need struct {
			kind Kind
			name string
		}
		var needs []need
		for _, name := range direct.Agents {
			needs = append(needs, need{KindAgent, name})
		}
		for _, name := range direct.Stages {
			needs = append(needs, need{KindStage, name})
		}
		if !target.Global {
			// A global agent finds its skills in whichever repository it
			// works in, so a global add brings none.
			for _, name := range direct.Skills {
				if used, ok := catalog.items[name]; ok && used.Kind == KindSkill {
					needs = append(needs, need{KindSkill, name})
				}
			}
		}
		for _, current := range needs {
			if planned[current.name] {
				continue
			}
			where, err := place.has(current.kind, current.name)
			if err != nil {
				return err
			}
			if where != "" {
				continue
			}
			// Load checked that the catalog defines everything its items
			// need that is not built in, and nothing built in is missing.
			needed := catalog.items[current.name]
			planned[current.name] = true
			plan = append(plan, needed)
			if err := follow(needed.direct); err != nil {
				return err
			}
		}
		return nil
	}
	if err := follow(item.direct); err != nil {
		return Result{}, err
	}
	if target.Global {
		return place.writeGlobal(name, plan)
	}
	return place.writeRepository(name, plan)
}

// place is a target with what it already has loaded.
type place struct {
	target Target
	// library is the global stage library as configuration declares it.
	library rules.Library
	// rulesData and document are the repository's rules file; nil when it
	// has none.
	rulesData []byte
	document  *rules.Document
	// dryRun checks and reports an addition without writing it.
	dryRun bool
}

// open loads what the target has. A repository whose rules file does not
// load is refused, since adding to it would hide what is wrong.
func open(target Target) (place, error) {
	loaded := place{target: target, library: rules.LoadLibrary(target.StageDirectories)}
	if target.Global {
		return loaded, nil
	}
	if strings.TrimSpace(target.RulesPath) == "" || strings.TrimSpace(target.AgentDirectory) == "" {
		return place{}, errors.New("add to a repository needs its rules file and agents directory")
	}
	data, err := os.ReadFile(target.RulesPath)
	if errors.Is(err, fs.ErrNotExist) {
		return loaded, nil
	}
	if err != nil {
		return place{}, fmt.Errorf("read rules %s: %w", target.RulesPath, err)
	}
	document, err := rules.ParseWith(string(data), loaded.library)
	if err != nil {
		return place{}, fmt.Errorf("%s does not load, so nothing is added until it is fixed: %w", target.RulesPath, err)
	}
	loaded.rulesData, loaded.document = data, &document
	return loaded, nil
}

// has returns where the target already has something of kind by name: a
// file, "built in", or "" when it has none. A stage and a group share one
// namespace.
func (place place) has(kind Kind, name string) (string, error) {
	switch kind {
	case KindAgent:
		for _, definition := range agent.BuiltIn() {
			if definition.Name == name {
				return "built in", nil
			}
		}
		directories := place.target.AgentDirectories
		if !place.target.Global {
			directories = []string{place.target.AgentDirectory}
		}
		for _, directory := range directories {
			exists, err := agent.Exists(directory, name)
			if err != nil {
				return "", fmt.Errorf("look for agent %q in %s: %w", name, directory, err)
			}
			if exists {
				return filepath.Join(directory, name+".md"), nil
			}
		}
	case KindSkill:
		if place.target.Global {
			return "", nil
		}
		if strings.TrimSpace(place.target.SkillDirectory) == "" {
			return "", errors.New("add to a repository needs its skills directory to add a skill")
		}
		directory := filepath.Join(place.target.SkillDirectory, name)
		if _, err := os.Lstat(directory); err == nil {
			return directory, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("look for skill %q in %s: %w", name, place.target.SkillDirectory, err)
		}
	case KindRule:
		if place.document != nil {
			for _, rule := range place.document.Rules {
				if strings.EqualFold(rule.Name, name) {
					return place.target.RulesPath, nil
				}
			}
		}
	case KindStage, KindGroup:
		if _, err := rules.Builtin().Resolve(name); err == nil {
			return "built in", nil
		}
		if place.target.Global {
			if file := place.globalDefinition(name); file != "" {
				return file, nil
			}
			return "", nil
		}
		if place.document != nil {
			if _, ok := place.document.Stages[name]; ok {
				return place.target.RulesPath, nil
			}
			if _, ok := place.document.Groups[name]; ok {
				return place.target.RulesPath, nil
			}
		}
	}
	return "", nil
}

// globalDefinition is the global library file defining name, or "".
func (place place) globalDefinition(name string) string {
	for _, definition := range place.library.Definitions {
		if definition.Scope == rules.ScopeGlobal && definition.Name == name {
			return definition.File
		}
	}
	return ""
}

// writeRepository adds the planned items to a repository: agents as files
// in its agents directory, then the rest to its rules file in one write.
func (place place) writeRepository(name string, plan []Item) (Result, error) {
	result := Result{Item: name}
	var agents, skills, definitions []Item
	var rule *Item
	for index, item := range plan {
		switch item.Kind {
		case KindAgent:
			agents = append(agents, item)
		case KindSkill:
			skills = append(skills, item)
		case KindRule:
			rule = &plan[index]
		default:
			definitions = append(definitions, item)
		}
	}
	var rulesContent []byte
	if rule != nil || len(definitions) > 0 {
		content, notes, err := place.editedRules(rule, definitions)
		if err != nil {
			return Result{}, err
		}
		rulesContent, result.Notes = content, notes
	}
	files := make([]pendingFile, 0, len(agents))
	for _, item := range agents {
		data, err := agentFile(item)
		if err != nil {
			return Result{}, err
		}
		files = append(files, pendingFile{path: filepath.Join(place.target.AgentDirectory, item.Name+".md"), data: data})
		if global := place.globalAgent(item.Name); global != "" {
			result.Notes = append(result.Notes, fmt.Sprintf("Agent %q in this repository replaces the global one in %s.", item.Name, global))
		}
	}
	if place.dryRun {
		files, skills, rulesContent = nil, nil, nil
	}
	written, err := createFiles(files)
	if err != nil {
		return Result{}, err
	}
	var skillDirectories []string
	for _, item := range skills {
		directory, err := createSkill(place.target.SkillDirectory, item)
		if err != nil {
			return Result{}, errors.Join(err, removeFiles(written), removeSkills(skillDirectories))
		}
		skillDirectories = append(skillDirectories, directory)
	}
	if rulesContent != nil {
		// The rules file was read when the add began; one changed since, by
		// another add or an editor, would lose that change to this write.
		// This narrows the window to the moment between this read and the
		// rename, as the app's own rules save has it.
		if err := place.unchanged(); err != nil {
			return Result{}, errors.Join(err, removeFiles(written), removeSkills(skillDirectories))
		}
		if err := replaceFile(place.target.RulesPath, rulesContent); err != nil {
			return Result{}, errors.Join(fmt.Errorf("write rules %s: %w", place.target.RulesPath, err), removeFiles(written), removeSkills(skillDirectories))
		}
	}
	for _, item := range plan {
		path := place.target.RulesPath
		switch item.Kind {
		case KindAgent:
			path = filepath.Join(place.target.AgentDirectory, item.Name+".md")
		case KindSkill:
			path = filepath.Join(place.target.SkillDirectory, item.Name)
		}
		result.Added = append(result.Added, Added{Kind: item.Kind, Name: item.Name, Path: path})
	}
	return result, nil
}

// createSkill writes a skill's directory whole: its files go to a
// temporary directory beside the skills directory, which is then renamed
// into place, so a plan never discovers a skill half written. It returns
// the skill's directory.
func createSkill(skills string, item Item) (string, error) {
	directory := filepath.Join(skills, item.Name)
	if err := os.MkdirAll(skills, 0o755); err != nil {
		return "", fmt.Errorf("write skill %q: %w", item.Name, err)
	}
	// Beside the skills directory, not in it: discovery reads every
	// directory there as a skill.
	temporary, err := os.MkdirTemp(filepath.Dir(skills), ".catalog-skill-*")
	if err != nil {
		return "", fmt.Errorf("write skill %q: %w", item.Name, err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	if err := os.Chmod(temporary, 0o755); err != nil {
		return "", fmt.Errorf("write skill %q: %w", item.Name, err)
	}
	for _, name := range slices.Sorted(maps.Keys(item.files)) {
		path := filepath.Join(temporary, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("write skill %q: %w", item.Name, err)
		}
		if err := os.WriteFile(path, item.files[name], 0o644); err != nil {
			return "", fmt.Errorf("write skill %q: %w", item.Name, err)
		}
	}
	// A rename onto a directory that has files fails, so one made since
	// Add looked is never replaced.
	if _, err := os.Lstat(directory); err == nil {
		return "", fmt.Errorf("%s already exists; adding never overwrites", directory)
	}
	if err := os.Rename(temporary, directory); err != nil {
		return "", fmt.Errorf("write skill %q: %w", item.Name, err)
	}
	return directory, nil
}

// removeSkills takes back skill directories Add wrote.
func removeSkills(directories []string) error {
	var problems []error
	for _, directory := range directories {
		if err := os.RemoveAll(directory); err != nil {
			problems = append(problems, fmt.Errorf("take back %s: %w", directory, err))
		}
	}
	return errors.Join(problems...)
}

// unchanged refuses a rules file that is not as open read it.
func (place place) unchanged() error {
	current, err := os.ReadFile(place.target.RulesPath)
	switch {
	case errors.Is(err, fs.ErrNotExist) && place.rulesData == nil:
		return nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("read rules %s: %w", place.target.RulesPath, err)
	case err == nil && place.rulesData != nil && bytes.Equal(current, place.rulesData):
		return nil
	}
	return fmt.Errorf("%s changed while the add was being made, so nothing was added; add it again", place.target.RulesPath)
}

// tense is a note as an add that was made says it, or as a preview says
// it of the add to come.
func (place place) tense(made, preview string) string {
	if place.dryRun {
		return preview
	}
	return made
}

// globalAgent is the global agent file a repository agent named name
// would replace, or "".
func (place place) globalAgent(name string) string {
	for index := len(place.target.AgentDirectories) - 1; index >= 0; index-- {
		directory := place.target.AgentDirectories[index]
		if exists, err := agent.Exists(directory, name); err == nil && exists {
			return filepath.Join(directory, name+".md")
		}
	}
	return ""
}

// editedRules is the repository's rules file with the rule, placed first,
// and the shared stages and groups added, checked to load. A repository
// without one gets the built-in rules with them, so it keeps the flows it
// had.
func (place place) editedRules(rule *Item, definitions []Item) ([]byte, []string, error) {
	var notes []string
	data, indent := place.rulesData, detectIndent(place.rulesData)
	if place.document == nil {
		builtin, err := rules.Marshal(rules.Default())
		if err != nil {
			return nil, nil, fmt.Errorf("write the built-in rules: %w", err)
		}
		data, indent = builtin, 2
		if rule != nil {
			notes = append(notes, place.tense(
				fmt.Sprintf("%s did not exist, so it was written with the built-in rules after %q, and the repository keeps the flows it had.", place.target.RulesPath, rule.Name),
				fmt.Sprintf("%s does not exist yet, so it will be written with the built-in rules after %q, and the repository keeps the flows it has.", place.target.RulesPath, rule.Name)))
		} else {
			notes = append(notes, place.tense(
				fmt.Sprintf("%s did not exist, so it was written with the built-in rules, and the repository keeps the flows it had.", place.target.RulesPath),
				fmt.Sprintf("%s does not exist yet, so it will be written with the built-in rules, and the repository keeps the flows it has.", place.target.RulesPath)))
		}
	} else if rule != nil && len(place.document.Rules) > 0 {
		notes = append(notes, place.tense(
			fmt.Sprintf("Rule %q is first in %s, so it is tried before the rules already there; move it to change that.", rule.Name, place.target.RulesPath),
			fmt.Sprintf("Rule %q will be first in %s, so it is tried before the rules already there; move it once it is added to change that.", rule.Name, place.target.RulesPath)))
	}
	root, err := parseNode(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parse rules %s: %w", place.target.RulesPath, err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s is not a mapping of keys to values", place.target.RulesPath)
	}
	top := root.Content[0]
	version := 1
	for _, item := range append(slices.Clone(definitions), derefItems(rule)...) {
		version = max(version, item.version)
	}
	if err := raiseVersion(top, version); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", place.target.RulesPath, err)
	}
	for _, item := range definitions {
		section := "stages"
		after := []string{"version"}
		if item.Kind == KindGroup {
			section, after = "groups", []string{"version", "stages"}
		}
		mapping := ensure(top, section, yaml.MappingNode, after)
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item.Name, HeadComment: "# " + item.Description}
		mapping.Content = append(mapping.Content, key, copyNode(item.node))
		if global := place.globalDefinition(item.Name); global != "" {
			notes = append(notes, fmt.Sprintf("%s %q in this repository's rules replaces the global one in %s.", capitalized(label(item.Kind)), item.Name, global))
		}
	}
	if rule != nil {
		sequence := ensure(top, "rules", yaml.SequenceNode, []string{"version", "stages", "groups"})
		sequence.Content = append([]*yaml.Node{copyNode(rule.node)}, sequence.Content...)
	}
	var buffer strings.Builder
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(indent)
	if err := encoder.Encode(root); err != nil {
		return nil, nil, fmt.Errorf("write rules %s: %w", place.target.RulesPath, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, nil, fmt.Errorf("write rules %s: %w", place.target.RulesPath, err)
	}
	content := []byte(buffer.String())
	after, err := rules.ParseWith(string(content), place.library)
	if err != nil {
		return nil, nil, fmt.Errorf("%s would not load with the addition, so nothing was added: %w", place.target.RulesPath, err)
	}
	before, err := rules.ParseWith(string(data), place.library)
	if err != nil {
		return nil, nil, fmt.Errorf("parse rules %s: %w", place.target.RulesPath, err)
	}
	if err := preserved(before, after, rule, definitions); err != nil {
		return nil, nil, fmt.Errorf("%s cannot be added to in place, so nothing was added: %w; add the item by hand", place.target.RulesPath, err)
	}
	return content, notes, nil
}

// preserved checks that the edited rules say everything the file said
// before, with only the rule first and the definitions added. The edit
// works on the keys the file writes, so a value it takes through a YAML
// merge (<<) or an alias could otherwise be replaced or dropped unseen.
func preserved(before, after rules.Document, rule *Item, definitions []Item) error {
	const reason = "it would change what the file already says, as it can when the file uses YAML merges or aliases"
	offset := 0
	if rule != nil {
		offset = 1
		if len(after.Rules) == 0 || after.Rules[0].Name != rule.Name {
			return errors.New(reason)
		}
	}
	if len(after.Rules) != len(before.Rules)+offset || !reflect.DeepEqual(after.Rules[offset:], before.Rules) || !reflect.DeepEqual(after.Default, before.Default) {
		return errors.New(reason)
	}
	added := map[Kind]int{}
	for _, item := range definitions {
		added[item.Kind]++
	}
	if len(after.Stages) != len(before.Stages)+added[KindStage] || len(after.Groups) != len(before.Groups)+added[KindGroup] {
		return errors.New(reason)
	}
	for name, stage := range before.Stages {
		if !reflect.DeepEqual(after.Stages[name], stage) {
			return errors.New(reason)
		}
	}
	for name, group := range before.Groups {
		if !reflect.DeepEqual(after.Groups[name], group) {
			return errors.New(reason)
		}
	}
	return nil
}

// writeGlobal adds the planned items to this machine's global directories:
// each agent to the first agent directory, and each shared stage or group
// as a stage library file of its own in the first stage directory.
func (place place) writeGlobal(name string, plan []Item) (Result, error) {
	result := Result{Item: name}
	files := make([]pendingFile, 0, len(plan))
	library := rules.Library{Definitions: slices.Clone(place.library.Definitions)}
	var added []rules.Definition
	for _, item := range plan {
		var file pendingFile
		switch item.Kind {
		case KindAgent:
			if len(place.target.AgentDirectories) == 0 {
				return Result{}, errors.New("no global agent directory is configured")
			}
			data, err := agentFile(item)
			if err != nil {
				return Result{}, err
			}
			file = pendingFile{path: filepath.Join(place.target.AgentDirectories[0], item.Name+".md"), data: data}
		default:
			if len(place.target.StageDirectories) == 0 {
				return Result{}, errors.New("no global stage library directory is configured")
			}
			file = pendingFile{path: filepath.Join(place.target.StageDirectories[0], item.Name+".yaml"), data: []byte(item.Content)}
			definition := item.definition
			definition.File = file.path
			library.Definitions = append(library.Definitions, definition)
			added = append(added, definition)
		}
		files = append(files, file)
		result.Added = append(result.Added, Added{Kind: item.Kind, Name: item.Name, Path: file.path})
	}
	// Each new definition is checked as the library will read it, beside
	// the global definitions already there.
	for _, definition := range added {
		if err := library.Check(definition); err != nil {
			return Result{}, fmt.Errorf("%s would not load in the stage library, so nothing was added: %w", definition.File, err)
		}
	}
	if place.dryRun {
		return result, nil
	}
	if _, err := createFiles(files); err != nil {
		return Result{}, err
	}
	return result, nil
}

// agentFile is a catalog agent as the file Add writes: as Inkuna would
// save it, recording the item it came from.
func agentFile(item Item) ([]byte, error) {
	definition := item.agent
	definition.Extra = OriginKey + ": " + item.Name + "\n"
	if item.Source != "" {
		keys := map[string]string{SourceKey: item.Source}
		if item.Scope != "" {
			keys[ScopeKey] = item.Scope
		}
		if item.Version != "" {
			keys[VersionKey] = item.Version
		}
		origin, err := yaml.Marshal(keys)
		if err != nil {
			return nil, fmt.Errorf("write agent %q: %w", item.Name, err)
		}
		definition.Extra += string(origin)
	}
	data, err := agent.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("write agent %q: %w", item.Name, err)
	}
	return data, nil
}

// raiseVersion sets the document's version to at least version.
func raiseVersion(top *yaml.Node, version int) error {
	node := mappingValue(top, "version")
	if node == nil {
		return errors.New("it has no version")
	}
	current, err := strconv.Atoi(node.Value)
	if err != nil {
		return fmt.Errorf("its version %q is not a number", node.Value)
	}
	if current < version {
		node.Value, node.Tag, node.Style = strconv.Itoa(version), "!!int", 0
	}
	return nil
}

// ensure returns the value under key in top, adding an empty one of kind
// after the last of the keys in after that top has, or first, when there is
// none. An existing empty value written as [] or {} is made a block, so
// what is added to it is written as the rest of the file is.
func ensure(top *yaml.Node, key string, kind yaml.Kind, after []string) *yaml.Node {
	if value := mappingValue(top, key); value != nil && value.Kind == kind {
		if len(value.Content) == 0 {
			value.Style = 0
		}
		return value
	} else if value != nil {
		// A key with no value, such as `rules:`, decodes as null.
		*value = yaml.Node{Kind: kind}
		return value
	}
	at := 0
	for index := 0; index+1 < len(top.Content); index += 2 {
		if slices.Contains(after, top.Content[index].Value) {
			at = index + 2
		}
	}
	value := &yaml.Node{Kind: kind}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	top.Content = slices.Insert(top.Content, at, keyNode, value)
	return value
}

// detectIndent is the indentation a YAML file uses: the narrowest indent of
// a line that has one, 2 when none does.
func detectIndent(data []byte) int {
	indent := 0
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		width := len(line) - len(trimmed)
		if width == 0 || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if indent == 0 || width < indent {
			indent = width
		}
	}
	if indent < 2 || indent > 8 {
		return 2
	}
	return indent
}

// copyNode copies a node and everything under it, so an edited document
// never shares nodes with the catalog's.
func copyNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	copied := *node
	copied.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		copied.Content[index] = copyNode(child)
	}
	return &copied
}

func derefItems(item *Item) []Item {
	if item == nil {
		return nil
	}
	return []Item{*item}
}

// label names a kind in a sentence.
func label(kind Kind) string {
	switch kind {
	case KindStage:
		return "shared stage"
	default:
		return string(kind)
	}
}

func capitalized(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// pendingFile is a new file Add writes.
type pendingFile struct {
	path string
	data []byte
}

// createFiles writes each new file, and on a failure removes the ones it
// wrote before it. It returns the paths it wrote.
func createFiles(files []pendingFile) ([]string, error) {
	written := make([]string, 0, len(files))
	for _, file := range files {
		if err := createFile(file.path, file.data); err != nil {
			return nil, errors.Join(err, removeFiles(written))
		}
		written = append(written, file.path)
	}
	return written, nil
}

// removeFiles takes back files Add wrote.
func removeFiles(paths []string) error {
	var problems []error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, fmt.Errorf("take back %s: %w", path, err))
		}
	}
	return errors.Join(problems...)
}

// link is os.Link; a test replaces it to stand for a file system without
// hard links.
var link = os.Link

// createFile writes a file that must not exist yet, atomically and without
// ever replacing one: the data goes to a temporary file in the same
// directory, which is then hard-linked to path, and a link fails when path
// exists, even one made after Add checked the name was free.
func createFile(path string, data []byte) error {
	temporary, err := writeTemporary(path, data, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(temporary) }()
	err = link(temporary, path)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%s already exists; adding never overwrites", path)
	}
	// A file system without hard links, such as FAT, gets the file created
	// exclusively and written in place: it still never replaces one, though
	// a reader may see it before it is whole.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists; adding never overwrites", path)
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// replaceFile replaces path with data atomically: a temporary file in the
// same directory, then a rename, so a reader never sees half a file. A
// file that is there keeps its permissions; a new one is an ordinary source
// file.
func replaceFile(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	temporary, err := writeTemporary(path, data, mode)
	if err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// writeTemporary writes data with mode to a new temporary file beside path
// and returns its name. The pattern matches neither *.md nor *.yaml, so a
// reader of the directory never takes it for an agent or library file.
func writeTemporary(path string, data []byte, mode os.FileMode) (string, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(directory, ".catalog-*.tmp")
	if err != nil {
		return "", err
	}
	_, err = temporary.Write(data)
	if err == nil {
		// CreateTemp makes the file owner-only.
		err = temporary.Chmod(mode)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary.Name())
		return "", err
	}
	return temporary.Name(), nil
}
