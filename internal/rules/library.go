package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"gopkg.in/yaml.v3"
)

// Scopes a shared stage or group can be defined in, lowest precedence
// first. A definition overrides one with the same name in a lower scope.
const (
	ScopeBuiltin = "builtin"
	ScopeGlobal  = "global"
	// ScopeRepository is a definition in the repository's own rules file.
	ScopeRepository = "repository"
)

// Definition is one shared stage or group and where it is defined.
type Definition struct {
	Name string `json:"name"`
	// Scope is ScopeBuiltin, ScopeGlobal, or ScopeRepository.
	Scope string `json:"scope"`
	// File is the library or rules file that defines it; empty for a
	// built-in one.
	File string `json:"file,omitempty"`
	// Exactly one of Stage and Group is set.
	Stage *Stage `json:"stage,omitempty"`
	Group *Group `json:"group,omitempty"`
	// problem is why a global definition failed its checks. Resolve refuses
	// it rather than let a plan use what the file did not mean, and the
	// file's other definitions stay usable.
	problem error
}

// Kind says whether the definition is a stage or a group.
func (definition Definition) Kind() string {
	if definition.Group != nil {
		return "group"
	}
	return "stage"
}

// Digest identifies what the definition says: its stage or group, not where
// it is written, so moving a definition to another file keeps its digest
// and any change to a field changes it.
func (definition Definition) Digest() string {
	// A stage and a group are plain data, so they always encode, maps in key
	// order.
	data, _ := json.Marshal(struct {
		Stage *Stage `json:"stage,omitempty"`
		Group *Group `json:"group,omitempty"`
	}{definition.Stage, definition.Group})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// describe names the definition in an error: a built-in one says so, and a
// global one names its file.
func (definition Definition) describe() string {
	label := definition.Kind()
	if label == "stage" {
		label = "shared stage"
	}
	label += " " + fmt.Sprintf("%q", definition.Name)
	switch definition.Scope {
	case ScopeBuiltin:
		return "built-in " + label
	case ScopeGlobal:
		return label + " in " + definition.File
	}
	return label
}

// LibraryProblem is a mistake in a global library file, reported where
// configuration is shown rather than only when a plan uses the definition.
type LibraryProblem struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// Library is the shared stages and groups a rules document may use besides
// its own: the built-in ones, then those in the global library directories
// inkuna.yaml declares. A repository's own definitions override both, and a
// global one overrides a built-in one, by name.
type Library struct {
	// Definitions are every definition, lowest precedence first: the
	// built-in ones, then the global ones in directory and file order. A
	// name may appear more than once; Resolve picks the one that applies.
	Definitions []Definition `json:"definitions"`
	// Problems are the mistakes found in the global library files.
	Problems []LibraryProblem `json:"problems,omitempty"`
}

// libraryFile is a global library file: shared stages and groups in the
// rules schema, and no rules.
type libraryFile struct {
	Version int              `yaml:"version"`
	Stages  map[string]Stage `yaml:"stages,omitempty"`
	Groups  map[string]Group `yaml:"groups,omitempty"`
}

// builtinStages and builtinGroups are the built-in library. The built-in
// rules use them, and a global or repository definition with the same name
// replaces one for every flow that uses it.
var (
	builtinStages = map[string]Stage{
		"profile":  {Kind: domain.StepScript},
		"verify":   {Kind: domain.StepEvaluation},
		"approval": {Kind: domain.StepApproval},
	}
	builtinGroups = map[string]Group{
		"verify-and-approve": {Stages: []Stage{
			{Use: "verify"},
			{Use: "approval", DependsOn: []string{"verify"}},
		}},
	}
)

// Builtin returns the built-in library: the profile, verify, and approval
// stages, and the verify-and-approve group.
func Builtin() Library {
	var library Library
	for _, name := range sortedNames(builtinStages) {
		stage := builtinStages[name].clone()
		library.Definitions = append(library.Definitions, Definition{Name: name, Scope: ScopeBuiltin, Stage: &stage})
	}
	for _, name := range sortedNames(builtinGroups) {
		group := Group{Stages: make([]Stage, len(builtinGroups[name].Stages))}
		for index, stage := range builtinGroups[name].Stages {
			group.Stages[index] = stage.clone()
		}
		library.Definitions = append(library.Definitions, Definition{Name: name, Scope: ScopeBuiltin, Group: &group})
	}
	return library
}

// LoadLibrary returns the built-in library with the global library files in
// directories added: each *.yaml file directly in each directory, in
// directory order and then by file name. A directory that does not exist
// adds nothing. A file that cannot be read or parsed adds nothing and is
// reported; a mistake in a definition it holds, or a name two global files
// define, is reported and fails the plans that use it.
func LoadLibrary(directories []string) Library {
	library := Builtin()
	for _, directory := range directories {
		directory = strings.TrimSpace(directory)
		if directory == "" {
			continue
		}
		entries, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			library.Problems = append(library.Problems, LibraryProblem{File: directory, Message: fmt.Sprintf("read stage library directory: %v", err)})
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			definitions, err := loadLibraryFile(path)
			if err != nil {
				library.Problems = append(library.Problems, LibraryProblem{File: path, Message: err.Error()})
				continue
			}
			library.Definitions = append(library.Definitions, definitions...)
		}
	}
	library.Problems = append(library.Problems, library.duplicateProblems()...)
	// Every definition is checked before any is marked, so a group is checked
	// against the stages beside it as they were written; a use of a broken
	// one is refused when a plan resolves it.
	problems := make([]error, len(library.Definitions))
	for index, definition := range library.Definitions {
		if definition.Scope == ScopeGlobal {
			problems[index] = library.validateDefinition(definition)
		}
	}
	for index, problem := range problems {
		if problem == nil {
			continue
		}
		message := problem.Error()
		// A group using a name the library does not resolve may be meant for
		// repositories that define it, so it is reported but stays usable;
		// expanding it with a repository's rules checks it again, and a
		// reference that still does not resolve fails that plan.
		if errors.As(problem, new(referenceError)) {
			message += "; only a repository whose rules define it can use this group"
		} else {
			library.Definitions[index].problem = problem
		}
		library.Problems = append(library.Problems, LibraryProblem{File: library.Definitions[index].File, Message: message})
	}
	return library
}

// loadLibraryFile reads one global library file, strictly, as the rules
// file is read.
func loadLibraryFile(path string) ([]Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read stage library: %w", err)
	}
	return ParseLibrary(data, path)
}

// ParseLibrary decodes one stage library file, strictly, as the rules file
// is read, and returns its definitions as global ones from source, stages
// and then groups in name order. It checks the file's form, not the
// definitions: Library.Check does that against a library.
func ParseLibrary(data []byte, source string) ([]Definition, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse stage library: %w", err)
	}
	var version int
	if node := field(&root, "version"); node != nil && node.Decode(&version) == nil && version > Version {
		return nil, checkVersion(version)
	}
	var file libraryFile
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse stage library: %w", err)
	}
	if err := checkVersion(file.Version); err != nil {
		return nil, err
	}
	if file.Version < sharedVersion {
		return nil, fmt.Errorf("a stage library needs rules version %d; this file is version %d", sharedVersion, file.Version)
	}
	document := Document{Stages: file.Stages, Groups: file.Groups}
	markOverrides(&document, &root)
	var definitions []Definition
	for _, name := range sortedNames(document.Stages) {
		stage := document.Stages[name]
		definitions = append(definitions, Definition{Name: name, Scope: ScopeGlobal, File: source, Stage: &stage})
	}
	for _, name := range sortedNames(document.Groups) {
		group := document.Groups[name]
		definitions = append(definitions, Definition{Name: name, Scope: ScopeGlobal, File: source, Group: &group})
	}
	return definitions, nil
}

// Check validates one definition as a rules file's own are validated,
// resolving the names it uses against the library. Unlike LoadLibrary, a
// group using a name the library does not define is an error here too.
func (library Library) Check(definition Definition) error {
	if (definition.Stage == nil) == (definition.Group == nil) {
		return fmt.Errorf("%q needs exactly one of a stage and a group", definition.Name)
	}
	return library.validateDefinition(definition)
}

// duplicateProblems reports each name more than one global file defines.
func (library Library) duplicateProblems() []LibraryProblem {
	files := make(map[string][]string)
	var names []string
	for _, definition := range library.Definitions {
		if definition.Scope != ScopeGlobal {
			continue
		}
		if len(files[definition.Name]) == 0 {
			names = append(names, definition.Name)
		}
		files[definition.Name] = append(files[definition.Name], definition.File)
	}
	var problems []LibraryProblem
	for _, name := range names {
		if defined := files[name]; len(defined) > 1 {
			for _, file := range defined[1:] {
				problems = append(problems, LibraryProblem{File: file, Message: fmt.Sprintf("%q is also defined in %s; a name may be defined in only one global stage library file", name, defined[0])})
			}
		}
	}
	return problems
}

// validateDefinition checks one global definition as a rules file's own
// are checked, resolving the names it uses against the rest of the library.
func (library Library) validateDefinition(definition Definition) error {
	document := Document{Version: Version, Stages: map[string]Stage{}, Groups: map[string]Group{}, library: &library, Source: definition.File}
	if definition.Stage != nil {
		document.Stages[definition.Name] = *definition.Stage
	} else {
		document.Groups[definition.Name] = *definition.Group
	}
	return document.validateLibrary()
}

// Resolve returns the library definition a use of name gets when the
// rules file does not define it: a global one over a built-in one. A name
// two global files define resolves to neither, since which one was meant is
// unknown.
func (library Library) Resolve(name string) (Definition, error) {
	var global []Definition
	var builtin *Definition
	for index, definition := range library.Definitions {
		if definition.Name != name {
			continue
		}
		switch definition.Scope {
		case ScopeGlobal:
			global = append(global, definition)
		case ScopeBuiltin:
			builtin = &library.Definitions[index]
		}
	}
	switch {
	case len(global) > 1:
		files := make([]string, len(global))
		for index, definition := range global {
			files[index] = definition.File
		}
		return Definition{}, fmt.Errorf("%q is defined in more than one global stage library file (%s); remove all but one", name, strings.Join(files, ", "))
	case len(global) == 1 && global[0].problem != nil:
		return Definition{}, fmt.Errorf("%s cannot be used until it is fixed: %w", global[0].describe(), global[0].problem)
	case len(global) == 1:
		return global[0], nil
	case builtin != nil:
		return *builtin, nil
	}
	return Definition{}, errNotDefined
}

// errNotDefined is Resolve finding no definition at all.
var errNotDefined = errors.New("not defined")

// Effective lists the definitions of the library and of document, each
// marked with whether a definition in a higher scope overrides it, in name
// order and then scope order. It is what `inkunactl stages list` prints.
func (library Library) Effective(document *Document) []EffectiveDefinition {
	definitions := slices.Clone(library.Definitions)
	if document != nil {
		for _, name := range sortedNames(document.Stages) {
			stage := document.Stages[name]
			definitions = append(definitions, Definition{Name: name, Scope: ScopeRepository, File: document.Source, Stage: &stage})
		}
		for _, name := range sortedNames(document.Groups) {
			group := document.Groups[name]
			definitions = append(definitions, Definition{Name: name, Scope: ScopeRepository, File: document.Source, Group: &group})
		}
	}
	rank := map[string]int{ScopeBuiltin: 0, ScopeGlobal: 1, ScopeRepository: 2}
	highest := make(map[string]int)
	count := make(map[string]map[string]int)
	for _, definition := range definitions {
		if rank[definition.Scope] > highest[definition.Name] {
			highest[definition.Name] = rank[definition.Scope]
		}
		if count[definition.Name] == nil {
			count[definition.Name] = make(map[string]int)
		}
		count[definition.Name][definition.Scope]++
	}
	effective := make([]EffectiveDefinition, 0, len(definitions))
	for _, definition := range definitions {
		entry := EffectiveDefinition{Definition: definition}
		entry.Overridden = rank[definition.Scope] < highest[definition.Name]
		entry.Ambiguous = !entry.Overridden && definition.Scope == ScopeGlobal && count[definition.Name][ScopeGlobal] > 1
		effective = append(effective, entry)
	}
	sort.SliceStable(effective, func(i, j int) bool {
		if effective[i].Name != effective[j].Name {
			return effective[i].Name < effective[j].Name
		}
		return rank[effective[i].Scope] < rank[effective[j].Scope]
	})
	return effective
}

// EffectiveDefinition is a definition as `inkunactl stages list` shows it.
type EffectiveDefinition struct {
	Definition
	// Overridden marks a definition a higher scope replaces by name.
	Overridden bool `json:"overridden,omitempty"`
	// Ambiguous marks a global definition another global file also
	// defines, which no use can resolve.
	Ambiguous bool `json:"ambiguous,omitempty"`
}

// WithLibrary returns the document using library for the names it does not
// define itself. A document without one uses the built-in library.
func (document Document) WithLibrary(library Library) Document {
	document.library = &library
	return document
}

// resolve returns the definition a use of name gets: the document's own,
// or else the library's.
func (document Document) resolve(name string) (Definition, error) {
	if stage, ok := document.Stages[name]; ok {
		return Definition{Name: name, Scope: ScopeRepository, File: document.Source, Stage: &stage}, nil
	}
	if group, ok := document.Groups[name]; ok {
		return Definition{Name: name, Scope: ScopeRepository, File: document.Source, Group: &group}, nil
	}
	if document.library == nil {
		return builtinLibrary.Resolve(name)
	}
	return document.library.Resolve(name)
}

// builtinLibrary is the library a document without one resolves against.
var builtinLibrary = Builtin()

// Uses returns every definition the named rule's flow uses, directly or
// through the groups it uses, in name order, as it resolves now. ruleName
// is matched as Named matches it. A plan records their digests, so an
// approval can tell when one changed after planning.
func (document Document) Uses(ruleName string) ([]Definition, error) {
	flow, ok := document.written(ruleName)
	if !ok {
		return nil, fmt.Errorf("no rule is named %q", ruleName)
	}
	found, err := document.usesOf(flow, true)
	if err != nil {
		return nil, err
	}
	definitions := make([]Definition, 0, len(found))
	for _, name := range sortedNames(found) {
		definitions = append(definitions, found[name])
	}
	return definitions, nil
}

// Usage is a flow of a document using a definition, directly or through a
// group.
type Usage struct {
	// Flow is the rule's name, or DefaultName for the default flow.
	Flow       string
	Definition Definition
}

// Usages lists, for each flow of the document, the rules in order and
// then the default flow, the definitions it uses as they resolve now, in
// name order. A name that does not resolve is left out, so a document
// being edited still says where the rest are used; validation reports it.
func (document Document) Usages() []Usage {
	var usages []Usage
	add := func(name string, flow Flow) {
		found, _ := document.usesOf(flow, false)
		for _, definition := range sortedNames(found) {
			usages = append(usages, Usage{Flow: name, Definition: found[definition]})
		}
	}
	for _, rule := range document.Rules {
		add(rule.Name, rule.Then)
	}
	if document.Default != nil {
		add(DefaultName, *document.Default)
	}
	return usages
}

// usesOf resolves every name flow uses, directly or through the groups it
// uses. When strict, a name that does not resolve is an error; otherwise
// it is skipped.
func (document Document) usesOf(flow Flow, strict bool) (map[string]Definition, error) {
	found := make(map[string]Definition)
	var walk func(entries []Stage) error
	use := func(name string) error {
		if _, seen := found[name]; seen {
			return nil
		}
		definition, err := document.resolve(name)
		if err != nil {
			if !strict {
				return nil
			}
			return fmt.Errorf("use %q: %w", name, err)
		}
		found[name] = definition
		if definition.Group != nil {
			return walk(definition.Group.Stages)
		}
		return nil
	}
	walk = func(entries []Stage) error {
		for _, entry := range entries {
			names := []string{entry.Use}
			// A flow taken already expanded, as the built-in default flow is
			// for a file that leaves its own out, names what it used in its
			// stages' provenance.
			if entry.Use == "" && entry.From != nil {
				names = append(slices.Clone(entry.From.Groups), entry.From.Stage)
			}
			for _, name := range names {
				if name == "" {
					continue
				}
				if err := use(name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(flow.Stages); err != nil {
		return nil, err
	}
	return found, nil
}

// Replacement is one of a repository's own definitions that a flow uses
// and the global or built-in definitions of the same name it replaces.
type Replacement struct {
	Definition Definition
	Replaced   []Definition
}

// Replaced lists the repository's own definitions the named rule's flow
// uses, directly or through its groups, that replace a global or built-in
// one, in name order, so a plan can say its repository changed what the
// library would have given it. ruleName is matched as Named matches it.
func (document Document) Replaced(ruleName string) ([]Replacement, error) {
	used, err := document.Uses(ruleName)
	if err != nil {
		return nil, err
	}
	library := builtinLibrary
	if document.library != nil {
		library = *document.library
	}
	var replacements []Replacement
	for _, definition := range used {
		if definition.Scope != ScopeRepository {
			continue
		}
		replacement := Replacement{Definition: definition}
		for _, lower := range library.Definitions {
			if lower.Name == definition.Name {
				replacement.Replaced = append(replacement.Replaced, lower)
			}
		}
		if len(replacement.Replaced) > 0 {
			replacements = append(replacements, replacement)
		}
	}
	return replacements, nil
}

// Note says what the replacement did, for a plan's notes: "Shared stage
// "verify" is this repository's own, which replaces the global one in
// /stages/checks.yaml."
func (replacement Replacement) Note() string {
	label := "Shared stage"
	if replacement.Definition.Group != nil {
		label = "Group"
	}
	replaced := make([]string, len(replacement.Replaced))
	for index, lower := range replacement.Replaced {
		switch lower.Scope {
		case ScopeGlobal:
			replaced[index] = "the global one in " + lower.File
		default:
			replaced[index] = "the built-in one"
		}
	}
	// The highest scope replaced comes first: the one the plan would
	// otherwise have used.
	slices.Reverse(replaced)
	return fmt.Sprintf("%s %q is this repository's own, which replaces %s.", label, replacement.Definition.Name, strings.Join(replaced, " and "))
}

// written is the flow a rule name stands for, as the file writes it.
func (document Document) written(name string) (Flow, bool) {
	for _, rule := range document.Rules {
		if strings.EqualFold(strings.TrimSpace(name), rule.Name) {
			return rule.Then, true
		}
	}
	if !strings.EqualFold(strings.TrimSpace(name), DefaultName) {
		return Flow{}, false
	}
	if document.Default != nil {
		return *document.Default, true
	}
	return *Default().Default, true
}

// WriteLibraryDefinition replaces the definition of definition.Name in the
// global library file definition.File with definition's stage or group,
// keeping the file's other definitions and their comments, and replaces
// the file atomically. library is the library as loaded now, which must
// read that file and find the name there: a built-in definition, or a file
// Inkuna does not read, is refused. The new definition is checked as
// LoadLibrary checks one, and so is every global definition it would
// break, so an edit that would make a working definition unusable is
// refused naming it. It returns the content it wrote.
func WriteLibraryDefinition(definition Definition, library Library) ([]byte, error) {
	path, name := definition.File, definition.Name
	if definition.Scope == ScopeBuiltin || path == "" {
		return nil, fmt.Errorf("built-in %s %q is read-only; copy it to a repository's rules to change it there", definition.Kind(), name)
	}
	if (definition.Stage == nil) == (definition.Group == nil) {
		return nil, fmt.Errorf("%q needs exactly one of a stage and a group", name)
	}
	at := slices.IndexFunc(library.Definitions, func(existing Definition) bool {
		return existing.Scope == ScopeGlobal && existing.Name == name && filepath.Clean(existing.File) == filepath.Clean(path)
	})
	if at < 0 {
		return nil, fmt.Errorf("%s does not define %q in a stage library directory Inkuna reads", path, name)
	}
	if existing := library.Definitions[at]; existing.Kind() != definition.Kind() {
		return nil, fmt.Errorf("%q is a %s in %s and stays one; define a %s under another name", name, existing.Kind(), path, definition.Kind())
	}
	// The file is read again rather than trusted from the library, so a
	// change made to it since is not lost, and one that broke it is fixed
	// by hand first.
	if _, err := loadLibraryFile(path); err != nil {
		return nil, fmt.Errorf("%s has a problem to fix first: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read stage library: %w", err)
	}
	updated := Definition{Name: name, Scope: ScopeGlobal, File: library.Definitions[at].File, Stage: definition.Stage, Group: definition.Group}
	if updated.Stage != nil {
		stage := updated.Stage.clone()
		stage.From = nil
		updated.Stage = &stage
	}
	next := Library{Definitions: slices.Clone(library.Definitions)}
	next.Definitions[at] = updated
	if err := next.validateDefinition(updated); err != nil && !errors.As(err, new(referenceError)) {
		return nil, err
	}
	for index, other := range library.Definitions {
		if index == at || other.Scope != ScopeGlobal || other.problem != nil {
			continue
		}
		if library.validateDefinition(other) != nil {
			continue
		}
		if err := next.validateDefinition(other); err != nil && !errors.As(err, new(referenceError)) {
			return nil, fmt.Errorf("the change would break %s: %w", other.describe(), err)
		}
	}
	content, err := replaceDefinition(data, updated)
	if err != nil {
		return nil, fmt.Errorf("write stage library %s: %w", path, err)
	}
	if err := writeFile(path, content); err != nil {
		return nil, fmt.Errorf("write stage library %s: %w", path, err)
	}
	return content, nil
}

// replaceDefinition returns the library file data with the definition of
// definition.Name, under stages: or groups:, replaced by definition's, and
// everything else, comments included, as it was parsed.
func replaceDefinition(data []byte, definition Definition) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	section := "stages"
	var value any = definition.Stage
	if definition.Group != nil {
		section, value = "groups", definition.Group
	}
	definitions := field(&root, section)
	if definitions == nil || definitions.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("no %s section defines %q", section, definition.Name)
	}
	var encoded yaml.Node
	if err := encoded.Encode(value); err != nil {
		return nil, err
	}
	commandsInFlow(&encoded)
	replaced := false
	for index := 0; index+1 < len(definitions.Content); index += 2 {
		if definitions.Content[index].Value == definition.Name {
			definitions.Content[index+1] = &encoded
			replaced = true
		}
	}
	if !replaced {
		return nil, fmt.Errorf("no %s section defines %q", section, definition.Name)
	}
	var buffer strings.Builder
	encoder := yaml.NewEncoder(&buffer)
	// Library files are written by hand, usually with two spaces.
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return []byte(buffer.String()), nil
}

// commandsInFlow writes each command under node as [name, arg, ...], as a
// person writes one, rather than one argument per line.
func commandsInFlow(node *yaml.Node) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			if value := node.Content[index+1]; node.Content[index].Value == "commands" && value.Kind == yaml.SequenceNode {
				for _, command := range value.Content {
					if command.Kind == yaml.SequenceNode {
						command.Style = yaml.FlowStyle
					}
				}
			}
		}
	}
	for _, child := range node.Content {
		commandsInFlow(child)
	}
}
