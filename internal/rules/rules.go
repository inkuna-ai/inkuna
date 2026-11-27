// Package rules turns a declarative YAML document into the workflow a task
// must follow. Rules are evaluated in order; the first whose conditions all
// match wins, otherwise the default flow applies. Rules never execute code:
// they only shape the DAG, which the orchestrator then staffs and the user
// then approves.
package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"gopkg.in/yaml.v3"
)

// Version is the newest rules schema version this build reads. Version 1
// files, from before shared stages and groups, still parse. A document
// declares the lowest version that has what it uses (RequiredVersion), so a
// file without shared stages stays readable by builds that know only 1.
const Version = 2

// sharedVersion is the first version with shared stages and groups.
const sharedVersion = 2

// RelativePath is where a repository keeps its rules.
const RelativePath = ".inkuna/rules.yaml"

// Document is a parsed rules file.
type Document struct {
	Version int `yaml:"version" json:"version"`
	// Stages are shared stage definitions, by name, that a flow or a group
	// uses with `use: <name>`.
	Stages map[string]Stage `yaml:"stages,omitempty" json:"stages,omitempty"`
	// Groups are named runs of stages, wired among themselves, that a flow
	// uses as one entry.
	Groups  map[string]Group `yaml:"groups,omitempty" json:"groups,omitempty"`
	Rules   []Rule           `yaml:"rules" json:"rules"`
	Default *Flow            `yaml:"default,omitempty" json:"default,omitempty"`
	// Source records where the document came from for the UI.
	Source string `yaml:"-" json:"source"`
	// library holds the shared stages and groups from outside the file that
	// a use may name; nil means the built-in library (WithLibrary).
	library *Library
}

// Rule pairs a condition with the flow it produces.
type Rule struct {
	Name        string    `yaml:"name" json:"name"`
	Description string    `yaml:"description,omitempty" json:"description,omitempty"`
	When        Condition `yaml:"when" json:"when"`
	Then        Flow      `yaml:"then" json:"then"`
}

// Condition lists predicates that must all hold. Empty lists match anything;
// non-empty lists match when any entry matches.
type Condition struct {
	Kinds        []domain.TaskKind `yaml:"kind,omitempty" json:"kind,omitempty"`
	Labels       []string          `yaml:"labels,omitempty" json:"labels,omitempty"`
	Sources      []string          `yaml:"source,omitempty" json:"source,omitempty"`
	Keywords     []string          `yaml:"keywords,omitempty" json:"keywords,omitempty"`
	Repositories []string          `yaml:"repositories,omitempty" json:"repositories,omitempty"`
	Complexity   *Range            `yaml:"complexity,omitempty" json:"complexity,omitempty"`
	MinPriority  int               `yaml:"minPriority,omitempty" json:"minPriority,omitempty"`
}

// Range is an inclusive integer band.
type Range struct {
	Min int `yaml:"min" json:"min"`
	Max int `yaml:"max" json:"max"`
}

// Flow is the ordered set of stages a matched task goes through.
type Flow struct {
	Stages []Stage `yaml:"stages" json:"stages"`
}

// Stage becomes one workflow step. Agent may be a definition name or "auto".
// A flow or group entry that sets Use stands for a shared stage or group
// instead; see Use.
type Stage struct {
	// Use names the shared stage or group this entry stands for. Its id
	// (the name when left out) and dependsOn wire it in, and the stage
	// fields it sets override a shared stage's for this use. Expansion
	// replaces it with plain stages before planning.
	Use string `yaml:"use,omitempty" json:"use,omitempty"`
	// ID and Kind are omitted when empty only so that a use, which has
	// neither of its own, is written back as it was; every plain stage
	// has both.
	ID           string            `yaml:"id,omitempty" json:"id"`
	Title        string            `yaml:"title,omitempty" json:"title,omitempty"`
	Kind         domain.StepKind   `yaml:"kind,omitempty" json:"kind"`
	Agent        string            `yaml:"agent,omitempty" json:"agent,omitempty"`
	Runtime      domain.Runtime    `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Permission   domain.Permission `yaml:"permission,omitempty" json:"permission,omitempty"`
	DependsOn    []string          `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`
	Skills       []string          `yaml:"skills,omitempty" json:"skills,omitempty"`
	AllowedPaths []string          `yaml:"allowedPaths,omitempty" json:"allowedPaths,omitempty"`
	Prompt       string            `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	Commands     [][]string        `yaml:"commands,omitempty" json:"commands,omitempty"`
	Optional     bool              `yaml:"optional,omitempty" json:"optional,omitempty"`
	// ContinueOnFailure runs an evaluation stage's remaining commands after
	// one fails, failing the stage once they have all run; without it the
	// stage stops at the first failure.
	ContinueOnFailure bool `yaml:"continueOnFailure,omitempty" json:"continueOnFailure,omitempty"`
	// Review makes an approval stage pause for a person to approve or
	// request changes.
	Review bool `yaml:"review,omitempty" json:"review,omitempty"`
	// OnFailure is fail, the default, or feedback, which sends an evaluation
	// stage's failed commands back to its feedback target instead of
	// failing the run.
	OnFailure domain.FailureAction `yaml:"onFailure,omitempty" json:"onFailure,omitempty"`
	// Feedback says where an approval or evaluation stage's feedback goes.
	Feedback *Feedback `yaml:"feedback,omitempty" json:"feedback,omitempty"`
	// MCP names the MCP servers inkuna.yaml declares that an agent stage's
	// step is given, besides its agent's. Planning refuses one the machine
	// does not declare, since the repository asked for it.
	MCP []string `yaml:"mcp,omitempty" json:"mcp,omitempty"`
	// Tracker names the kinds of tracker change an agent stage's step may
	// ask for in its handoff, besides its agent's. The step gets only those
	// the repository's tracker.yaml, as this machine's inkuna.yaml narrows
	// it, allows, and needs a review gate after it where they are shown.
	Tracker []domain.TrackerKind `yaml:"tracker,omitempty" json:"tracker,omitempty"`
	// Overrides lists, by YAML name, the stage fields a use sets. A field
	// set to a non-zero value overrides whether or not it is listed; the
	// list is what lets one set to false or empty override too. Parsing
	// fills it from the keys the file writes, and the view sends it back.
	Overrides []string `yaml:"-" json:"overrides,omitempty"`
	// From says where an expanded stage came from. Expansion sets it; a
	// stage written inline in a flow has none.
	From *Provenance `yaml:"-" json:"from,omitempty"`
}

// Feedback is where a stage's feedback goes: Target, a writable agent
// stage upstream of it, or when empty the nearest such stage. On an
// evaluation stage that sends its failures back, MaxRounds caps the
// automatic rounds, domain.DefaultFeedbackRounds when left out. It is a
// pointer so that a cap written as 0 is refused rather than read as left
// out. SystemExitCodes are the exit codes with which the stage's checks
// say their environment broke; a stage that lists any pauses the run for
// a person on such a failure, or on a check that could not start or was
// stopped, instead of sending it back.
type Feedback struct {
	Target          string `yaml:"target,omitempty" json:"target,omitempty"`
	MaxRounds       *int   `yaml:"maxRounds,omitempty" json:"maxRounds,omitempty"`
	SystemExitCodes []int  `yaml:"systemExitCodes,omitempty" json:"systemExitCodes,omitempty"`
}

// Load reads a repository's rules. A missing file yields the built-in
// document and exists=false.
func Load(repositoryRoot string) (Document, bool, error) {
	return LoadPath(filepath.Join(repositoryRoot, filepath.FromSlash(RelativePath)))
}

// LoadPath reads a specific rules file, resolving its uses against the
// built-in library.
func LoadPath(path string) (Document, bool, error) {
	return LoadPathWith(path, Builtin())
}

// LoadPathWith reads a specific rules file, resolving the uses of names it
// does not define against library. A missing file yields the built-in
// document, using library too, and exists=false; it is validated against
// the library, since a global definition can replace one it uses.
func LoadPathWith(path string, library Library) (Document, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		document := Default().WithLibrary(library)
		if err := document.Validate(); err != nil {
			return Document{}, false, fmt.Errorf("built-in rules: %w", err)
		}
		return document, false, nil
	}
	if err != nil {
		return Document{}, false, fmt.Errorf("read rules %s: %w", path, err)
	}
	// The source is known before validation, so an error about one of the
	// file's own definitions names it like a library file's.
	document, err := parse(string(data), library, path)
	if err != nil {
		return Document{}, true, fmt.Errorf("%s: %w", path, err)
	}
	return document, true, nil
}

// Parse decodes and validates a rules document, resolving its uses against
// the built-in library.
func Parse(content string) (Document, error) {
	return ParseWith(content, Builtin())
}

// ParseWith decodes and validates a rules document, resolving the uses of
// names it does not define against library.
func ParseWith(content string, library Library) (Document, error) {
	return parse(content, library, "inline")
}

// parse is ParseWith naming where content came from, so a problem with it
// says which file to fix.
func parse(content string, library Library, source string) (Document, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return Document{}, fmt.Errorf("parse rules: %w", err)
	}
	// The version is checked before the strict decode, so a file written for
	// a newer build is refused by its version rather than by the first field
	// this build does not know.
	var version int
	if node := field(&root, "version"); node != nil && node.Decode(&version) == nil && version > Version {
		return Document{}, checkVersion(version)
	}
	var document Document
	decoder := yaml.NewDecoder(strings.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("parse rules: %w", err)
	}
	document.library = &library
	markOverrides(&document, &root)
	if document.Default == nil {
		// The built-in default flow uses the library; it is taken expanded,
		// so a file that leaves its default out needs no newer version than
		// it declares, and is saved with plain stages as before.
		fallback, err := document.ExpandFlow(*Default().Default)
		if err != nil {
			return Document{}, fmt.Errorf("built-in default flow: %w", err)
		}
		document.Default = &fallback
	}
	// A file with only a default flow has no rules list; the views read the
	// list either way, so it is empty rather than null.
	if document.Rules == nil {
		document.Rules = []Rule{}
	}
	if err := document.Validate(); err != nil {
		return Document{}, err
	}
	document.Source = source
	return document, nil
}

// Validate checks the shared stages and groups, then every rule and flow
// with its uses expanded, so an expanded stage passes exactly the checks an
// inline one does.
func (document Document) Validate() error {
	if err := checkVersion(document.Version); err != nil {
		return err
	}
	if needed := document.RequiredVersion(); document.Version < needed {
		return fmt.Errorf("shared stages and groups need rules version %d; this document is version %d", needed, document.Version)
	}
	if err := document.validateLibrary(); err != nil {
		return err
	}
	names := make(map[string]bool, len(document.Rules))
	for index, rule := range document.Rules {
		if strings.TrimSpace(rule.Name) == "" {
			return errors.New("every rule needs a name")
		}
		if names[rule.Name] {
			return fmt.Errorf("rule %q is declared twice", rule.Name)
		}
		names[rule.Name] = true
		for _, kind := range rule.When.Kinds {
			if !validTaskKind(kind) {
				return fmt.Errorf("rule %q matches unknown task kind %q", rule.Name, kind)
			}
		}
		if rule.When.Complexity != nil && (rule.When.Complexity.Min < 1 || rule.When.Complexity.Max > 10 || rule.When.Complexity.Min > rule.When.Complexity.Max) {
			return fmt.Errorf("rule %q complexity range must sit inside 1-10", rule.Name)
		}
		if err := document.validateFlow(rule.Then); err != nil {
			return flowError(index, rule.Name, err)
		}
	}
	if document.Default == nil {
		return errors.New("a default flow is required")
	}
	if err := document.validateFlow(*document.Default); err != nil {
		return flowError(-1, "", err)
	}
	return nil
}

// checkVersion refuses a version this build cannot read.
func checkVersion(version int) error {
	if version > Version {
		return fmt.Errorf("rules version %d is newer than this build of Inkuna reads (up to %d); update Inkuna to use this file", version, Version)
	}
	if version < 1 {
		return fmt.Errorf("unsupported rules version %d", version)
	}
	return nil
}

// validateFlow expands a flow's uses and validates the result. A mistake
// about one stage records the entry the stage came from.
func (document Document) validateFlow(flow Flow) error {
	expanded, entries, err := document.ExpandFlowEntries(flow)
	if err != nil {
		return err
	}
	index, err := expanded.check()
	if err != nil && index >= 0 {
		return entryError{entry: entries[index], err: err}
	}
	return err
}

// Validate checks stage identifiers, kinds, and dependencies. A flow is
// validated with its uses expanded (Document.ExpandFlow); one still holding
// a use is refused.
func (flow Flow) Validate() error {
	_, err := flow.check()
	return err
}

// check is Validate, also returning the index of the stage a mistake is
// about, or -1 for one about the flow as a whole, so an editor can point at
// the entry the stage came from.
func (flow Flow) check() (int, error) {
	if len(flow.Stages) == 0 {
		return -1, errors.New("flow needs at least one stage")
	}
	seen := make(map[string]bool, len(flow.Stages))
	all := make(map[string]Stage, len(flow.Stages))
	for _, stage := range flow.Stages {
		all[stage.ID] = stage
	}
	// ancestors holds, for each stage, every stage it depends on directly or
	// through others; dependencies are declared earlier, so they are known.
	ancestors := make(map[string]map[string]bool, len(flow.Stages))
	approvals := 0
	for index, stage := range flow.Stages {
		if stage.Use != "" {
			return index, fmt.Errorf("entry %d uses %q, and a flow is validated and planned only once its uses are expanded", index+1, stage.Use)
		}
		if strings.TrimSpace(stage.ID) == "" {
			return index, errors.New("every stage needs an id")
		}
		if seen[stage.ID] {
			return index, fmt.Errorf("stage %s is declared twice", stage.Label())
		}
		seen[stage.ID] = true
		if err := stage.validate(); err != nil {
			return index, err
		}
		if stage.Kind == domain.StepApproval {
			approvals++
		}
		above := make(map[string]bool)
		for _, dependency := range stage.DependsOn {
			if dependency == stage.ID {
				return index, fmt.Errorf("stage %s cannot depend on itself", stage.Label())
			}
			if !seen[dependency] {
				return index, fmt.Errorf("stage %s depends on %q which must be declared earlier", stage.Label(), dependency)
			}
			above[dependency] = true
			for id := range ancestors[dependency] {
				above[id] = true
			}
		}
		ancestors[stage.ID] = above
		if err := validateTarget(stage, above, all); err != nil {
			return index, err
		}
	}
	if approvals == 0 {
		return -1, errors.New("flow must end with an approval stage so no work lands unreviewed")
	}
	if flow.Stages[len(flow.Stages)-1].Kind != domain.StepApproval {
		return len(flow.Stages) - 1, errors.New("the final stage must be an approval")
	}
	return -1, nil
}

// validate checks what a stage gets right or wrong on its own, wherever it
// is written: inline, as a shared definition, or in a group. Flow.Validate
// adds what depends on the rest of the flow.
func (stage Stage) validate() error {
	name := stage.Label()
	switch stage.Kind {
	case domain.StepAgent:
		if stage.Agent == "" {
			return fmt.Errorf("agent stage %s needs an agent name or auto", name)
		}
		if stage.Permission != "" && stage.Permission != domain.PermissionReadOnly && stage.Permission != domain.PermissionWorktree {
			return fmt.Errorf("stage %s has invalid permission %q", name, stage.Permission)
		}
		// Only the form is checked here; the planner knows which runtimes
		// the configuration declares.
		if stage.Runtime != "" && !domain.ValidRuntimeName(stage.Runtime) {
			return fmt.Errorf("stage %s has invalid runtime %q", name, stage.Runtime)
		}
	case domain.StepHumanInput:
		if strings.TrimSpace(stage.Prompt) == "" {
			return fmt.Errorf("human-input stage %s needs a prompt", name)
		}
	case domain.StepApproval, domain.StepScript, domain.StepEvaluation:
	default:
		return fmt.Errorf("stage %s has unknown kind %q", name, stage.Kind)
	}
	if stage.ContinueOnFailure && stage.Kind != domain.StepEvaluation {
		return fmt.Errorf("stage %s sets continueOnFailure, which only an evaluation stage has", name)
	}
	if len(stage.MCP) > 0 && stage.Kind != domain.StepAgent {
		return fmt.Errorf("stage %s names MCP servers, which only an agent stage is given", name)
	}
	for _, server := range stage.MCP {
		if !agentruntime.ValidMCPServerName(server) {
			return fmt.Errorf("stage %s names MCP server %q, which is not a valid server name", name, server)
		}
	}
	if len(stage.Tracker) > 0 && stage.Kind != domain.StepAgent {
		return fmt.Errorf("stage %s asks for tracker changes, which only an agent stage may ask for", name)
	}
	for _, kind := range stage.Tracker {
		if !domain.ValidTrackerKind(kind) {
			return fmt.Errorf("stage %s asks for tracker kind %q; the kinds are status, comment, edit, and create", name, kind)
		}
	}
	// An agent stage's commands are what its worker may run without
	// asking, as exact command lines; any other stage's are run directly.
	// The approval screen holds an edited command to the same checks.
	check := agentruntime.CheckCommand
	if stage.Kind == domain.StepAgent {
		check = agentruntime.CheckAllowedCommand
	}
	for _, command := range stage.Commands {
		if err := check(command); err != nil {
			return fmt.Errorf("stage %s: %w", name, err)
		}
	}
	return validateFeedback(stage)
}

// validateFeedback checks a stage's review gate and check loop on their
// own; validateTarget checks the target against the flow.
func validateFeedback(stage Stage) error {
	name := stage.Label()
	if stage.Review && stage.Kind != domain.StepApproval {
		return fmt.Errorf("stage %s sets review, which only an approval stage has", name)
	}
	switch stage.OnFailure {
	case "", domain.FailureFail, domain.FailureFeedback:
	default:
		return fmt.Errorf("stage %s has invalid onFailure %q; use fail or feedback", name, stage.OnFailure)
	}
	if stage.OnFailure != "" && stage.Kind != domain.StepEvaluation {
		return fmt.Errorf("stage %s sets onFailure, which only an evaluation stage has", name)
	}
	if stage.Feedback == nil {
		return nil
	}
	if stage.Kind != domain.StepApproval && stage.Kind != domain.StepEvaluation {
		return fmt.Errorf("stage %s sets feedback, which only approval and evaluation stages have", name)
	}
	if stage.Kind == domain.StepEvaluation && stage.OnFailure != domain.FailureFeedback {
		return fmt.Errorf("stage %s sets feedback, which an evaluation stage uses only with onFailure: feedback", name)
	}
	if rounds := stage.Feedback.MaxRounds; rounds != nil {
		if stage.Kind != domain.StepEvaluation {
			return fmt.Errorf("stage %s sets feedback.maxRounds, which only an evaluation stage has", name)
		}
		if *rounds < 1 || *rounds > domain.MaxFeedbackRounds {
			return fmt.Errorf("stage %s sets feedback.maxRounds to %d; it must be from 1 to %d", name, *rounds, domain.MaxFeedbackRounds)
		}
	}
	if codes := stage.Feedback.SystemExitCodes; len(codes) > 0 {
		if stage.Kind != domain.StepEvaluation {
			return fmt.Errorf("stage %s sets feedback.systemExitCodes, which only an evaluation stage has", name)
		}
		if err := domain.ValidateSystemExitCodes(codes); err != nil {
			return fmt.Errorf("stage %s sets feedback.systemExitCodes: %w", name, err)
		}
	}
	return nil
}

// validateTarget checks a named feedback target against the flow. Whether
// the target can write may still depend on the task, which Workflow checks.
func validateTarget(stage Stage, above map[string]bool, all map[string]Stage) error {
	if stage.Feedback == nil || stage.Feedback.Target == "" {
		return nil
	}
	name, target := stage.Label(), stage.Feedback.Target
	found, ok := all[target]
	switch {
	case !ok:
		return fmt.Errorf("stage %s sends feedback to %q, which is not a stage of this flow", name, target)
	case !above[target]:
		return fmt.Errorf("stage %s sends feedback to %q, which is not upstream of it", name, target)
	case found.Kind != domain.StepAgent:
		return fmt.Errorf("stage %s sends feedback to %q, which is not an agent stage", name, target)
	case found.Permission == domain.PermissionReadOnly:
		return fmt.Errorf("stage %s sends feedback to %q, which is read-only", name, target)
	}
	return nil
}

// DefaultName names the flow used when no rule applies. A label or an
// explicit choice may name it too.
const DefaultName = "default"

// Match picks the flow for a task. A task label that names a rule chooses
// that rule outright, so a request tagged "bugfix" goes through the bugfix
// flow whatever else it looks like; otherwise the first rule whose condition
// holds wins, then the default. complexity may be zero when unknown;
// complexity conditions then fail. The flow comes with its uses expanded.
func (document Document) Match(task domain.TaskSpec, complexity int) (string, Flow) {
	if name, ok := document.LabelRule(task.Labels); ok {
		if flow, ok := document.Named(name); ok {
			return name, flow
		}
		return DefaultName, document.defaultFlow()
	}
	for _, rule := range document.Rules {
		if rule.When.Matches(task, complexity) {
			return rule.Name, document.planned(rule.Then)
		}
	}
	return DefaultName, document.defaultFlow()
}

// defaultFlow is the document's fallback flow, or the built-in one for a
// document that was never validated and has none.
func (document Document) defaultFlow() Flow {
	if document.Default != nil {
		return document.planned(*document.Default)
	}
	return document.planned(*Default().Default)
}

// planned is a flow as planning sees it, with its uses expanded. A document
// that passed Validate always expands; for one that did not, the flow comes
// back as written and Flow.Workflow refuses it with the reason.
func (document Document) planned(flow Flow) Flow {
	if expanded, err := document.ExpandFlow(flow); err == nil {
		return expanded
	}
	return flow
}

// LabelRule returns the name of the first rule (or "default") that one of
// the labels names, ignoring case. Rules are checked in document order so a
// task carrying two rule names gets the earlier rule.
func (document Document) LabelRule(labels []string) (string, bool) {
	for _, rule := range document.Rules {
		if containsFold(labels, rule.Name) {
			return rule.Name, true
		}
	}
	if containsFold(labels, DefaultName) {
		return DefaultName, true
	}
	return "", false
}

// Named returns the flow a rule produces, or the default flow for "default",
// ignoring case, with its uses expanded. The user chooses one at planning
// time to bypass matching.
func (document Document) Named(name string) (Flow, bool) {
	for _, rule := range document.Rules {
		if strings.EqualFold(strings.TrimSpace(name), rule.Name) {
			return document.planned(rule.Then), true
		}
	}
	if strings.EqualFold(strings.TrimSpace(name), DefaultName) {
		return document.defaultFlow(), true
	}
	return Flow{}, false
}

// Matches evaluates a condition against a task.
func (condition Condition) Matches(task domain.TaskSpec, complexity int) bool {
	if len(condition.Kinds) > 0 && !containsKind(condition.Kinds, task.Kind) {
		return false
	}
	if len(condition.Sources) > 0 && !containsFold(condition.Sources, task.Source.Kind) {
		return false
	}
	if len(condition.Labels) > 0 && !anyFold(condition.Labels, task.Labels) {
		return false
	}
	if len(condition.Repositories) > 0 && !containsFold(condition.Repositories, filepath.Base(task.Repository)) && !containsFold(condition.Repositories, task.Repository) {
		return false
	}
	if len(condition.Keywords) > 0 {
		haystack := strings.ToLower(task.Objective + "\n" + strings.Join(task.AcceptanceCriteria, "\n"))
		found := false
		for _, keyword := range condition.Keywords {
			if strings.Contains(haystack, strings.ToLower(keyword)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if condition.Complexity != nil && (complexity < condition.Complexity.Min || complexity > condition.Complexity.Max) {
		return false
	}
	if condition.MinPriority > 0 && task.Priority < condition.MinPriority {
		return false
	}
	return true
}

// Access is what a stage gives the step it becomes for a task of kind. An
// agent stage writes to the worktree unless it says otherwise or the task
// only reads, and a writable one keeps its writes to its allowed paths, the
// whole repository when it names none; every other stage is read-only.
func (stage Stage) Access(kind domain.TaskKind) domain.StepAccess {
	access := domain.StepAccess{Permission: domain.PermissionReadOnly, Commands: stage.Commands}
	if stage.Kind != domain.StepAgent {
		return access
	}
	access.Permission = stage.Permission
	if access.Permission == "" {
		access.Permission = domain.PermissionWorktree
		if kind == domain.TaskResearch || kind == domain.TaskReview {
			access.Permission = domain.PermissionReadOnly
		}
	}
	if access.Permission == domain.PermissionWorktree {
		access.AllowedPaths = stage.AllowedPaths
		if len(access.AllowedPaths) == 0 {
			access.AllowedPaths = []string{"."}
		}
	}
	return access
}

func cloneCommands(commands [][]string) [][]string {
	if commands == nil {
		return nil
	}
	cloned := make([][]string, len(commands))
	for index, command := range commands {
		cloned[index] = slices.Clone(command)
	}
	return cloned
}

// Workflow converts a flow into a workflow spec. Agent stages with agent
// "auto" are left for the orchestrator to staff; complexity is filled later.
func (flow Flow) Workflow(task domain.TaskSpec, ruleName string, defaultRuntime domain.Runtime, now time.Time) (domain.WorkflowSpec, error) {
	if err := flow.Validate(); err != nil {
		return domain.WorkflowSpec{}, err
	}
	steps := make([]domain.WorkflowStep, 0, len(flow.Stages))
	for _, stage := range flow.Stages {
		step := domain.WorkflowStep{
			ID: stage.ID, Title: stage.Title, Kind: stage.Kind, DependsOn: append([]string(nil), stage.DependsOn...),
			Runtime: stage.Runtime, Agent: stage.Agent, Prompt: stage.Prompt, SelectedSkills: append([]string(nil), stage.Skills...), MCPServers: domain.SortedServerNames(stage.MCP),
			Commands: stage.Commands, ContinueOnFailure: stage.ContinueOnFailure, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 900},
			Metadata: map[string]string{"rule": ruleName},
			From:     stage.From.origin(),
		}
		if stage.Optional {
			step.Metadata["optional"] = "true"
		}
		if len(step.MCPServers) > 0 {
			step.Metadata[domain.MetadataStageMCP] = strings.Join(step.MCPServers, ",")
		}
		// The planner grants the kinds the project allows; the stage's are
		// kept apart from its agent's so approval can rebuild them.
		if kinds := domain.SortedTrackerKinds(stage.Tracker); len(kinds) > 0 {
			step.Metadata[domain.MetadataStageTracker] = domain.JoinTrackerKinds(kinds, ",")
		}
		if step.Title == "" {
			step.Title = defaultTitle(stage)
		}
		access := stage.Access(task.Kind)
		step.Permission, step.AllowedPaths = access.Permission, access.AllowedPaths
		if stage.Kind == domain.StepAgent || stage.Kind == domain.StepEvaluation {
			planned := domain.StepAccess{Permission: access.Permission, Commands: cloneCommands(access.Commands), AllowedPaths: slices.Clone(access.AllowedPaths)}
			step.RuleAccess = &planned
		}
		switch stage.Kind {
		case domain.StepAgent:
			step.Budget = task.Budget
			if step.Budget.WallTimeSeconds > 1800 || step.Budget.WallTimeSeconds <= 0 {
				step.Budget.WallTimeSeconds = 1800
			}
			step.ExpectedOutputs = []string{"handoff", "diff"}
		case domain.StepScript:
			step.Budget = domain.Budget{WallTimeSeconds: 300}
			step.ExpectedOutputs = []string{"repository-profile"}
		case domain.StepEvaluation:
			step.ExpectedOutputs = []string{"verification"}
		}
		steps = append(steps, step)
	}
	for index := range steps {
		if err := resolveFeedback(flow.Stages[index], &steps[index], steps, task.Kind); err != nil {
			return domain.WorkflowSpec{}, err
		}
	}
	workflow := domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow-" + task.ID, TaskID: task.ID, DefaultRuntime: defaultRuntime,
		RuleName: ruleName, MaxConcurrency: 2, Steps: steps, CreatedAt: now,
	}
	if err := domain.ValidateWorkflow(workflow); err != nil {
		return domain.WorkflowSpec{}, err
	}
	return workflow, nil
}

// resolveFeedback carries a stage's review gate and check loop onto its
// step, naming the target now that the task has decided which stages can
// write. A stage that sets none of them plans exactly as before.
func resolveFeedback(stage Stage, step *domain.WorkflowStep, steps []domain.WorkflowStep, kind domain.TaskKind) error {
	named := ""
	if stage.Feedback != nil {
		named = stage.Feedback.Target
	}
	loop := stage.OnFailure == domain.FailureFeedback
	if !stage.Review && !loop && named == "" {
		return nil
	}
	step.Review = stage.Review
	if loop {
		step.OnFailure = domain.FailureFeedback
		step.MaxRounds = domain.DefaultFeedbackRounds
		if stage.Feedback != nil && stage.Feedback.MaxRounds != nil {
			step.MaxRounds = *stage.Feedback.MaxRounds
		}
		if stage.Feedback != nil && len(stage.Feedback.SystemExitCodes) > 0 {
			step.SystemExitCodes = slices.Clone(stage.Feedback.SystemExitCodes)
		}
	}
	if named != "" {
		for _, target := range steps {
			if target.ID == named && target.Permission != domain.PermissionWorktree {
				return fmt.Errorf("stage %s sends feedback to %q, which is read-only for %s tasks", stage.Label(), named, kind)
			}
		}
		step.FeedbackTarget = named
		return nil
	}
	step.FeedbackTarget = nearestWritable(steps, *step)
	if step.FeedbackTarget == "" && loop {
		return fmt.Errorf("stage %s sends its failures back as feedback, but no writable agent stage is upstream of it for %s tasks", stage.Label(), kind)
	}
	return nil
}

// nearestWritable is the writable agent step with the fewest dependency
// steps between it and from, the earliest in the flow when several are as
// near, or empty when none is upstream.
func nearestWritable(steps []domain.WorkflowStep, from domain.WorkflowStep) string {
	order := make(map[string]int, len(steps))
	byID := make(map[string]domain.WorkflowStep, len(steps))
	for index, step := range steps {
		order[step.ID], byID[step.ID] = index, step
	}
	seen := make(map[string]bool)
	for level := from.DependsOn; len(level) > 0; {
		nearest := ""
		var next []string
		for _, id := range level {
			if seen[id] {
				continue
			}
			seen[id] = true
			step := byID[id]
			if step.Kind == domain.StepAgent && step.Permission == domain.PermissionWorktree && (nearest == "" || order[id] < order[nearest]) {
				nearest = id
			}
			next = append(next, step.DependsOn...)
		}
		if nearest != "" {
			return nearest
		}
		level = next
	}
	return ""
}

func defaultTitle(stage Stage) string {
	switch stage.Kind {
	case domain.StepAgent:
		if stage.Agent == "auto" {
			return "Agent work: " + stage.ID
		}
		return strings.ToUpper(stage.Agent[:1]) + stage.Agent[1:] + ": " + stage.ID
	case domain.StepScript:
		return "Profile repository"
	case domain.StepEvaluation:
		return "Run deterministic verification"
	case domain.StepHumanInput:
		return "Human input: " + stage.ID
	case domain.StepApproval:
		return "Review changes"
	default:
		return stage.ID
	}
}

func containsKind(kinds []domain.TaskKind, kind domain.TaskKind) bool {
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func anyFold(wanted, actual []string) bool {
	for _, value := range actual {
		if containsFold(wanted, value) {
			return true
		}
	}
	return false
}

func validTaskKind(kind domain.TaskKind) bool {
	switch kind {
	case domain.TaskResearch, domain.TaskBugfix, domain.TaskImplementation, domain.TaskReview, domain.TaskDocumentation, domain.TaskSkillAuthoring:
		return true
	default:
		return false
	}
}
