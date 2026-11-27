package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/docs"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/pricing"
	"github.com/inkuna-ai/inkuna/internal/routing"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/internal/skill"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/version"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

type ConfigurationView struct {
	Path    string           `json:"path"`
	Exists  bool             `json:"exists"`
	DataDir string           `json:"dataDir"`
	File    config.File      `json:"file"`
	Secrets []secrets.Status `json:"secrets"`
	// Defaults is what an omitted field resolves to, for placeholders.
	Defaults config.File `json:"defaults"`
	// Overrides lists fields an environment variable currently wins over, so
	// the view can show that editing them has no effect in this process.
	Overrides []config.Override `json:"overrides"`
	// RoutingDefaults is the built-in policy a runtime without its own bands
	// in inkuna.yaml keeps using.
	RoutingDefaults routing.Policy `json:"routingDefaults"`
	// PricingDefaults is the built-in pricing table that inkuna.yaml's
	// prices add to or replace.
	PricingDefaults pricing.Table `json:"pricingDefaults"`
	// RestartRequired is set by a save that changed the data directory: this
	// process keeps its open stores, and the next launch uses the new one.
	RestartRequired bool `json:"restartRequired,omitempty"`
	// Trackers is, for each declared repository, what its tracker.yaml
	// allows and what this machine's override leaves of it.
	Trackers []RepositoryTrackerView `json:"trackers"`
	// StageDirectories are the global stage library directories in use,
	// after the environment and the default, and LibraryProblems the
	// mistakes found in their files. Settings shows them beside
	// stages.directories; they never block a save, since the files are
	// edited outside inkuna.yaml, and planning refuses a flow that uses a
	// broken definition.
	StageDirectories []string               `json:"stageDirectories"`
	LibraryProblems  []rules.LibraryProblem `json:"libraryProblems"`
	// Build is the version and commit this app was built from.
	Build version.Info `json:"build"`
}

type AgentView struct {
	Definition agent.Definition `json:"definition"`
	Card       a2a.AgentCard    `json:"card"`
	// Overrides names the lower layer this definition shadows, if any.
	Overrides agent.Source `json:"overrides,omitempty"`
}

type RulesView struct {
	Document rules.Document `json:"document"`
	Exists   bool           `json:"exists"`
	Path     string         `json:"path"`
}

// DocIndexView is a project's doc index and the directory it was read from.
type DocIndexView struct {
	Path string     `json:"path"`
	Docs []docs.Doc `json:"docs"`
}

type InputView struct {
	storage.InputRequest
	// Permissions is the request's own list. It is set here, not through
	// the embedded request, because Wails generates bindings only for the
	// types it finds on a view's own fields.
	Permissions []storage.PermissionAsk `json:"permissions,omitempty"`
	RunTitle    string                  `json:"runTitle,omitempty"`
	// Expired marks a pending question whose run is no longer active.
	Expired bool `json:"expired,omitempty"`
	// PreviousAnswer is what the user answered when the same step of the run
	// asked the same question before, as a human-input stage asks again in
	// a later round, offered as a one-click reply.
	PreviousAnswer string `json:"previousAnswer,omitempty"`
}

type StepContextView struct {
	StepID   string                 `json:"stepId"`
	Agent    string                 `json:"agent"`
	Envelope domain.ContextEnvelope `json:"envelope"`
	Handoff  *domain.HandoffResult  `json:"handoff,omitempty"`
	// Prompt is what a remote advisor was sent. A local worker's prompts are
	// stored per call, each named by its usage record's PromptDigest.
	Prompt string `json:"prompt"`
	// DocReads are the docs the step's agent opened, in order, each marked
	// with whether the doc index its envelope delivered listed it.
	DocReads []DocReadView `json:"docReads"`
	// Usage is every model call the step made, its turns and its peers'
	// answers, in order, each with the sections of the prompt it sent. Calls
	// still running come last, with the outcome running.
	Usage []domain.UsageRecord `json:"usage"`
	// Feedback is what the step was sent back with in each round it was a
	// target of, oldest first, with its replies.
	Feedback []FeedbackRoundView `json:"feedback"`
}

// FeedbackRoundView is the feedback one round sent a step, item by item,
// each with the step's reply once its run ended.
type FeedbackRoundView struct {
	Round int                    `json:"round"`
	Items []storage.FeedbackItem `json:"items"`
}

// DocReadView is a step's doc read as the run view shows it. Delivered says
// the doc was in the index the step's envelope carried; a directory search
// never is.
type DocReadView struct {
	domain.DocRead
	Delivered bool `json:"delivered"`
}

type ArtifactView struct {
	storage.ArtifactRecord
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
}

// Configuration returns inkuna.yaml and the status of every declared secret.
// Secret values never leave the keychain.
func (service *Service) Configuration() (ConfigurationView, error) {
	return service.configurationView(), nil
}

func (service *Service) configurationView() ConfigurationView {
	return ConfigurationView{
		Path: service.config.ConfigPath, Exists: service.config.ConfigExists, DataDir: service.dataDir, File: service.config.File,
		Secrets:  service.secrets.Statuses(service.config.SecretNames()),
		Defaults: config.DefaultFile(), Overrides: config.Overrides(), RoutingDefaults: routing.DefaultPolicy(), PricingDefaults: pricing.Default(),
		Trackers:         repositoryTrackers(service.config.File),
		StageDirectories: slices.Clone(service.config.StageDirectories),
		LibraryProblems:  libraryProblems(service.stageLibrary()),
		Build:            version.Current(),
	}
}

// libraryProblems is library's problems, empty rather than nil for the view.
func libraryProblems(library rules.Library) []rules.LibraryProblem {
	if library.Problems == nil {
		return []rules.LibraryProblem{}
	}
	return library.Problems
}

// ValidateConfiguration reports every problem SaveConfiguration would reject,
// keyed by field so the settings view can show each beside its control.
// Nothing is saved.
func (service *Service) ValidateConfiguration(fileJSON string) ([]config.FieldError, error) {
	file, err := decodeConfigurationFile(fileJSON)
	if err != nil {
		return nil, err
	}
	file.RemoteAgents = service.config.File.RemoteAgents
	return file.Problems(), nil
}

func decodeConfigurationFile(fileJSON string) (config.File, error) {
	file := config.DefaultFile()
	decoder := json.NewDecoder(strings.NewReader(fileJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return config.File{}, fmt.Errorf("decode configuration: %w", err)
	}
	file.Version = config.FileVersion
	return file, nil
}

// SaveConfiguration validates and writes inkuna.yaml, then reloads the
// effective configuration. Runs already in flight are unaffected.
func (service *Service) SaveConfiguration(fileJSON string) (ConfigurationView, error) {
	file, err := decodeConfigurationFile(fileJSON)
	if err != nil {
		return ConfigurationView{}, err
	}
	// Remote agents carry a pinned card that only a fetch-and-review can
	// produce, and allowInsecure, which only a hand edit may set. The settings
	// form edits neither; they change through the remote agent bindings.
	// Extension sections never reach the view, so the form cannot carry them.
	return service.editConfiguration(func(current *config.File) error {
		file.RemoteAgents = current.RemoteAgents
		file.Extensions = current.Extensions
		*current = file
		return nil
	})
}

// editConfiguration applies edit to a copy of inkuna.yaml as it stands and
// saves the result. It holds configMu from reading the file to writing it,
// so two edits, such as connecting a project while Settings saves, never
// overwrite each other. An edit that returns an error saves nothing.
func (service *Service) editConfiguration(edit func(*config.File) error) (ConfigurationView, error) {
	service.configMu.Lock()
	defer service.configMu.Unlock()
	file := service.config.File
	file.Repositories = slices.Clone(file.Repositories)
	if err := edit(&file); err != nil {
		return ConfigurationView{}, err
	}
	return service.saveConfigurationLocked(file)
}

// saveConfigurationLocked validates and writes file, then installs it and
// brings the projects in line with it. The caller holds configMu.
func (service *Service) saveConfigurationLocked(file config.File) (ConfigurationView, error) {
	path := service.config.ConfigPath
	if path == "" {
		path = config.DefaultPath(service.dataDir)
	}
	// A rejected file must not get as far as refusing a relocation for it.
	if err := file.Validate(); err != nil {
		return ConfigurationView{}, err
	}
	relocation, err := service.planDataDirRelocation(file, path)
	if err != nil {
		return ConfigurationView{}, err
	}
	if err := refuseSharedRepositories(file); err != nil {
		return ConfigurationView{}, err
	}
	if err := service.refuseDroppedProjects(service.context(), file); err != nil {
		return ConfigurationView{}, err
	}
	content, err := config.Encode(file)
	if err != nil {
		return ConfigurationView{}, fmt.Errorf("save %s: %w", path, err)
	}
	// configMu is held, so a slow policy holds up other saves, for at most
	// its deadline; the file must not change between its checks and the
	// write.
	if err := service.decide(service.context(), configDecision(path, content, file), nil); err != nil {
		return ConfigurationView{}, err
	}
	if err := config.WriteFile(path, file); err != nil {
		return ConfigurationView{}, err
	}
	service.auditChange(auditConfigSave, "", "Saved "+path, nil, path, content)
	configuration, err := config.FromFile(service.dataDir, path, true, file)
	if err != nil {
		return ConfigurationView{}, err
	}
	store := service.secretStore
	service.applyConfig(configuration)
	service.secretStore = store
	service.secrets = secrets.NewResolver(store)
	if err := service.ready(); err != nil {
		return ConfigurationView{}, err
	}
	if _, err := service.reconcileProjects(service.context(), file); err != nil {
		return ConfigurationView{}, err
	}
	if relocation != nil {
		if err := relocation.apply(service, file, path); err != nil {
			return ConfigurationView{}, err
		}
	}
	service.emit("inkuna:config", path)
	view := service.configurationView()
	view.RestartRequired = relocation != nil
	return view, nil
}

// dataDirRelocation is a save that changes which directory the next launch
// uses. It is worked out before anything is written so a relocation that
// cannot carry the saved settings is refused instead of half applied.
type dataDirRelocation struct {
	target, targetPath, defaultPath string
	toDefault                       bool
}

// planDataDirRelocation returns nil when the save leaves startup alone: the
// directory is unchanged, an environment override owns startup, or this
// service was handed its directory. A target that already holds a
// configuration this process did not put there is refused: startup would read
// that file, so the settings being saved would silently not apply.
func (service *Service) planDataDirRelocation(file config.File, path string) (*dataDirRelocation, error) {
	if !service.discovered || strings.TrimSpace(os.Getenv(config.EnvDataDir)) != "" || strings.TrimSpace(os.Getenv(config.EnvConfigFile)) != "" {
		return nil, nil
	}
	defaultDir, err := config.DefaultDataDir()
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(file.DataDir)
	if target == "" {
		target = defaultDir
	}
	target = filepath.Clean(target)
	if target == filepath.Clean(service.dataDir) {
		return nil, nil
	}
	plan := &dataDirRelocation{
		target: target, targetPath: filepath.Join(target, "inkuna.yaml"),
		defaultPath: filepath.Join(defaultDir, "inkuna.yaml"), toDefault: target == filepath.Clean(defaultDir),
	}
	// The file at the default location is only a locator while the data lives
	// elsewhere, so going back to the default may replace it.
	if !plan.toDefault && plan.targetPath != path && !service.relocatedTo[plan.targetPath] {
		if _, exists, err := config.ReadFile(plan.targetPath); err != nil {
			return nil, err
		} else if exists {
			return nil, fmt.Errorf("data directory %s already has its own inkuna.yaml, so these settings would not apply there; remove that file, or start Inkuna with %s=%s to use it as it is", target, config.EnvDataDir, target)
		}
	}
	return plan, nil
}

// apply makes the new directory discoverable the way the setup wizard does:
// the configuration goes into it and the default location names it. Nothing
// else is moved.
func (plan *dataDirRelocation) apply(service *Service, file config.File, path string) error {
	if plan.targetPath != path {
		if err := config.WriteFile(plan.targetPath, file); err != nil {
			return fmt.Errorf("write %s: %w", plan.targetPath, err)
		}
		if service.relocatedTo == nil {
			service.relocatedTo = make(map[string]bool)
		}
		// Later saves in this process keep that copy current.
		service.relocatedTo[plan.targetPath] = true
	}
	if plan.toDefault || plan.defaultPath == path {
		return nil
	}
	locator, _, err := config.ReadFile(plan.defaultPath)
	if err != nil {
		return err
	}
	locator.DataDir = plan.target
	if err := config.WriteFile(plan.defaultPath, locator); err != nil {
		return fmt.Errorf("write locator: %w", err)
	}
	return nil
}

// syncRepository upserts a project declared in inkuna.yaml.
// SaveSecret stores a value in the OS keychain and declares the name in
// inkuna.yaml when it is new. The value is never logged or persisted here.
func (service *Service) SaveSecret(name, value string) (ConfigurationView, error) {
	name = strings.TrimSpace(name)
	if err := secrets.ValidateName(name); err != nil {
		return ConfigurationView{}, err
	}
	if service.secretStore == nil {
		return ConfigurationView{}, errors.New("secret store is unavailable")
	}
	if err := service.secretStore.Set(name, value); err != nil {
		return ConfigurationView{}, err
	}
	declared := false
	for _, secret := range service.config.File.Secrets {
		if secret.Name == name {
			declared = true
			break
		}
	}
	if name == config.EnvLinearAPIKey {
		service.ForgetLinearAuthentication()
	}
	if !declared {
		file := service.config.File
		file.Secrets = append(file.Secrets, config.SecretRef{Name: name})
		encoded, err := json.Marshal(file)
		if err != nil {
			return ConfigurationView{}, err
		}
		return service.SaveConfiguration(string(encoded))
	}
	return service.configurationView(), nil
}

// DeleteSecret removes a value from the keychain. The declaration stays so
// the UI keeps showing that the secret is expected.
func (service *Service) DeleteSecret(name string) (ConfigurationView, error) {
	if service.secretStore == nil {
		return ConfigurationView{}, errors.New("secret store is unavailable")
	}
	name = strings.TrimSpace(name)
	if err := service.secretStore.Delete(name); err != nil {
		return ConfigurationView{}, err
	}
	if name == config.EnvLinearAPIKey {
		service.ForgetLinearAuthentication()
	}
	return service.configurationView(), nil
}

// Agents lists the agents available to a project: built-in, global, and
// repository definitions merged by name.
func (service *Service) Agents(projectID string) ([]AgentView, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	root := ""
	descriptions := map[string]string{}
	if strings.TrimSpace(projectID) != "" {
		project, err := service.store.Project(service.context(), projectID)
		if err != nil {
			return nil, fmt.Errorf("load project: %w", err)
		}
		root = project.Repository
		if packages, err := skill.Discover(filepath.Join(root, ".inkuna", "skills")); err == nil {
			for _, pkg := range packages {
				descriptions[pkg.ID] = pkg.Description
			}
		}
	}
	registry, err := agent.Build(root, service.config.AgentDirectories)
	if err != nil {
		return nil, err
	}
	builtIn, global := map[string]bool{}, map[string]bool{}
	for _, definition := range agent.BuiltIn() {
		builtIn[definition.Name] = true
	}
	for _, directory := range service.config.AgentDirectories {
		if definitions, err := agent.Discover(directory, agent.SourceGlobal); err == nil {
			for _, definition := range definitions {
				global[definition.Name] = true
			}
		}
	}
	views := make([]AgentView, 0)
	for _, definition := range registry.All() {
		views = append(views, AgentView{Definition: definition, Card: definition.Card(descriptions), Overrides: overriddenLayer(definition, builtIn, global)})
	}
	views = append(views, AgentView{Definition: agent.Definition{Name: agent.Human, Description: agent.HumanCard().Description, Permission: domain.PermissionReadOnly, Source: agent.SourceBuiltIn, Instructions: "Answers through the inbox."}, Card: agent.HumanCard()})
	return views, nil
}

// Rules returns the effective rules document for a project.
func (service *Service) Rules(projectID string) (RulesView, error) {
	if err := service.ready(); err != nil {
		return RulesView{}, err
	}
	project, err := service.store.Project(service.context(), projectID)
	if err != nil {
		return RulesView{}, fmt.Errorf("load project: %w", err)
	}
	document, exists, path, err := service.loadRules(project)
	if err != nil {
		return RulesView{}, err
	}
	return RulesView{Document: document, Exists: exists, Path: path}, nil
}

// loadRules resolves the rules file for a project: the inkuna.yaml override
// for that repository when declared (relative paths resolve against the
// repository), otherwise .inkuna/rules.yaml. A declared override that does
// not exist is an error; a missing default yields the built-in document.
func (service *Service) loadRules(project storage.Project) (rules.Document, bool, string, error) {
	path, declared := service.repositoryPath(project, func(repository config.RepositoryConfig) string { return repository.Rules }, rules.RelativePath)
	document, exists, err := rules.LoadPathWith(path, service.stageLibrary())
	if err != nil {
		return rules.Document{}, false, path, fmt.Errorf("load rules: %w", err)
	}
	if declared && !exists {
		return rules.Document{}, false, path, fmt.Errorf("load rules: %s declared in inkuna.yaml does not exist", path)
	}
	return document, exists, path, nil
}

// stageLibrary is the built-in stage library with the global library files
// in the configured directories, read now, so an edit to one applies to the
// next plan without a restart, as an edit to an agent does.
func (service *Service) stageLibrary() rules.Library {
	return rules.LoadLibrary(service.config.StageDirectories)
}

// DocIndex lists the docs under a project's documentation root with their
// frontmatter. Bodies are never included; a doc with problems is listed with
// them rather than hiding the rest.
func (service *Service) DocIndex(projectID string) (DocIndexView, error) {
	if err := service.ready(); err != nil {
		return DocIndexView{}, err
	}
	project, err := service.store.Project(service.context(), projectID)
	if err != nil {
		return DocIndexView{}, fmt.Errorf("load project: %w", err)
	}
	root, index, err := service.indexDocs(project, project.Repository)
	if err != nil {
		return DocIndexView{}, err
	}
	return DocIndexView{Path: root, Docs: index}, nil
}

// DocsView is a project's docs as the Docs view browses them: the resolved
// docs root and the index arranged as folders.
type DocsView struct {
	Path string      `json:"path"`
	Tree docs.Folder `json:"tree"`
}

// Docs returns a project's doc tree with each doc's frontmatter and
// problems, from the same index as DocIndex.
func (service *Service) Docs(projectID string) (DocsView, error) {
	view, err := service.DocIndex(projectID)
	if err != nil {
		return DocsView{}, err
	}
	return DocsView{Path: view.Path, Tree: docs.Tree(view.Docs)}, nil
}

// Doc graph scopes: one project's docs and what links to or from them, or
// every registered project's.
const (
	DocGraphProject = "project"
	DocGraphAll     = "all"
)

// DocGraphView is the doc graph for the Docs view. Warnings name registered
// projects whose docs could not be indexed; the graph leaves them out.
type DocGraphView struct {
	Nodes    []docs.GraphNode `json:"nodes"`
	Edges    []docs.GraphEdge `json:"edges"`
	Warnings []string         `json:"warnings,omitempty"`
}

// DocGraph links docs to each other and to other registered projects, built
// on demand from every registered project's index; nothing is stored. A
// GitHub link reaches another project through that project's origin remote.
// Scope DocGraphProject keeps projectID's docs and every edge that starts or
// ends in that project; DocGraphAll keeps every project.
func (service *Service) DocGraph(projectID, scope string) (DocGraphView, error) {
	if err := service.ready(); err != nil {
		return DocGraphView{}, err
	}
	if scope != DocGraphProject && scope != DocGraphAll {
		return DocGraphView{}, fmt.Errorf("doc graph scope %q must be %s or %s", scope, DocGraphProject, DocGraphAll)
	}
	ctx := service.context()
	if _, err := service.store.Project(ctx, projectID); err != nil {
		return DocGraphView{}, fmt.Errorf("load project: %w", err)
	}
	projects, err := service.store.Projects(ctx)
	if err != nil {
		return DocGraphView{}, fmt.Errorf("list projects: %w", err)
	}
	sources := make([]docs.GraphSource, 0, len(projects))
	warnings := []string{}
	for _, project := range projects {
		entry, _ := service.repositoryConfig(project)
		index, err := docs.Discover(project.Repository, entry.Docs)
		if err != nil {
			// The project being browsed must index; another project that
			// cannot only drops out of the graph.
			if project.ID == projectID {
				return DocGraphView{}, fmt.Errorf("index docs: %w", err)
			}
			warnings = append(warnings, project.Name+": "+err.Error())
			continue
		}
		sources = append(sources, docs.GraphSource{
			Project: project.ID, Name: project.Name, Index: index,
			Remote:  worktree.OriginRepository(ctx, project.Repository),
			DocsDir: docs.RelativeDir(entry.Docs),
		})
	}
	focus := ""
	if scope == DocGraphProject {
		focus = projectID
	}
	host := service.config.GitHubHost
	if host == "" {
		host = "github.com"
	}
	graph := docs.BuildGraph(sources, focus, host)
	return DocGraphView{Nodes: graph.Nodes, Edges: graph.Edges, Warnings: warnings}, nil
}

// DocContent returns one of a project's docs for the Docs view to render, by
// its path relative to the docs root. Only a doc the index could list is
// read: docs.Read refuses ".." and absolute paths, hidden entries, and
// symbolic links below the docs root.
func (service *Service) DocContent(projectID, path string) (docs.Content, error) {
	if err := service.ready(); err != nil {
		return docs.Content{}, err
	}
	project, err := service.store.Project(service.context(), projectID)
	if err != nil {
		return docs.Content{}, fmt.Errorf("load project: %w", err)
	}
	entry, _ := service.repositoryConfig(project)
	content, err := docs.Read(project.Repository, entry.Docs, path)
	if err != nil {
		return docs.Content{}, fmt.Errorf("read doc: %w", err)
	}
	return content, nil
}

// indexDocs resolves a project's docs root inside a copy of its repository,
// the checkout or a run's worktree, and indexes it. The Docs view and the
// context envelope both read the index through it, so they agree.
func (service *Service) indexDocs(project storage.Project, repository string) (string, []docs.Doc, error) {
	entry, _ := service.repositoryConfig(project)
	root, err := docs.Dir(repository, entry.Docs)
	if err != nil {
		return "", nil, fmt.Errorf("index docs: %w", err)
	}
	index, err := docs.Discover(repository, entry.Docs)
	if err != nil {
		return "", nil, fmt.Errorf("index docs: %w", err)
	}
	return root, index, nil
}

// docBriefs is the doc index of a step's working directory as the context
// envelope carries it. Paths become relative to that directory rather than
// the docs root, since a worker opens them from there and is not told the
// root.
func (service *Service) docBriefs(project storage.Project, workingDirectory string) ([]domain.DocBrief, error) {
	root, index, err := service.indexDocs(project, workingDirectory)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(workingDirectory, root)
	if err != nil {
		return nil, fmt.Errorf("index docs: %w", err)
	}
	briefs := make([]domain.DocBrief, 0, len(index))
	for _, doc := range index {
		briefs = append(briefs, domain.DocBrief{
			Path: filepath.ToSlash(filepath.Join(relative, filepath.FromSlash(doc.Path))), Title: doc.Title, Tags: doc.Tags, Status: string(doc.Status),
			LastUpdated: doc.LastUpdated, Author: doc.Author, Problems: doc.Problems,
		})
	}
	return briefs, nil
}

// Inbox lists unanswered questions, newest first.
func (service *Service) Inbox() ([]InputView, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	inputs, err := service.store.Inputs(service.context(), storage.InputPending, 0)
	if err != nil {
		return nil, err
	}
	views := make([]InputView, 0, len(inputs))
	for _, input := range inputs {
		views = append(views, service.inputView(input))
	}
	return views, nil
}

// AnswerInput delivers the user's answer over the A2A bus, resuming the
// paused task.
func (service *Service) AnswerInput(inputID, answer string) (InputView, error) {
	if err := service.ready(); err != nil {
		return InputView{}, err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return InputView{}, errors.New("an answer is required")
	}
	input, err := service.store.Input(service.context(), inputID)
	if err != nil {
		return InputView{}, err
	}
	if input.State != storage.InputPending {
		return InputView{}, fmt.Errorf("input %s is already %s", inputID, input.State)
	}
	// A reply to a review asks for the principal as sending changes does.
	if input.Review {
		return service.answerReview(input, answer)
	}
	principal, err := service.principal(service.context())
	if err != nil {
		return InputView{}, err
	}
	// A check pause decides what the run does next, so it takes one of the
	// choices it offers and nothing else.
	if input.CheckPause && !slices.Contains(input.Options, answer) {
		return InputView{}, fmt.Errorf("answer with one of: %s", strings.Join(input.Options, ", "))
	}
	service.runsMu.RLock()
	run := service.runs[input.ExecutionID]
	service.runsMu.RUnlock()
	// A finished run stays in memory for a while as a record; nothing in it is
	// listening for an answer any more.
	if run == nil || service.runFinished(run) {
		return InputView{}, errors.New("the run that asked this question is no longer active")
	}
	// Two-phase delivery keyed by the input id: atomically claim the pending
	// row as delivering with the answer, send once, then confirm. Concurrent
	// answers race on the conditional update and only one proceeds; a failed
	// send restores pending so the user can retry.
	claimed, err := service.store.ClaimInput(service.context(), input.ID, answer)
	if err != nil {
		return InputView{}, err
	}
	if !claimed {
		return InputView{}, fmt.Errorf("input %s was answered concurrently", inputID)
	}
	input.State = storage.InputDelivering
	input.Answer = answer
	// Kept before sending, since the worker may ask its policies about it
	// before SendMessage returns. Only an answer that allows is ever asked
	// about.
	if len(input.Permissions) > 0 && permissionApproved(answer) {
		service.keepAnswerer(input.ID, principal)
	}
	if _, err := service.bus.SendMessage(run.ctx, input.Agent, a2a.MessageSendParams{
		Message:  a2a.Message{Role: a2a.RoleUser, TaskID: input.A2ATaskID, Parts: []a2a.Part{a2a.TextPart(answer)}, Metadata: map[string]any{"inputId": input.ID}},
		Metadata: map[string]any{"inputId": input.ID},
	}); err != nil {
		service.takeAnswerer(input.ID)
		// Restore pending only if the row is still delivering; a cancellation
		// that landed meanwhile must not be overwritten.
		if _, restoreErr := service.store.FinishDelivery(service.context(), input.ID, false); restoreErr != nil {
			return InputView{}, fmt.Errorf("resume %s: %w (and the question could not be returned to pending: %w)", input.Agent, err, restoreErr)
		}
		return InputView{}, fmt.Errorf("resume %s: %w", input.Agent, err)
	}
	finished, err := service.store.FinishDelivery(service.context(), input.ID, true)
	if err != nil {
		return InputView{}, fmt.Errorf("the answer was delivered but could not be marked answered: %w", err)
	}
	if finished {
		input.State = storage.InputAnswered
		input.ResolvedAt = time.Now().UTC()
	} else if current, loadErr := service.store.Input(service.context(), input.ID); loadErr == nil {
		input = current
	}
	service.updateRun(input.ExecutionID, func(view *WorkflowRunView) {
		if view.PendingInputs > 0 {
			view.PendingInputs--
		}
		if view.PendingInputs == 0 && view.State == "waiting" {
			view.State = "running"
		}
		for index := range view.Steps {
			if view.Steps[index].ID == input.StepID && view.Steps[index].State == "waiting" {
				view.Steps[index].State = "running"
			}
		}
	})
	service.persistRun(input.ExecutionID)
	_ = service.appendRunEvent(input.ExecutionID, syntheticEvent(service.liveStepRunID(input.ExecutionID, input.StepID), 0, domain.EventMessage, "User answered: "+truncateSummary(answer, 200)))
	// A choice among the options offered is a decision worth naming; a
	// written answer is part of the exchange with the agent, and stays out.
	choice := ""
	if slices.Contains(input.Options, answer) {
		choice = answer
	}
	if service.auditing() {
		if execution, err := service.store.Execution(service.context(), input.ExecutionID); err == nil {
			service.auditDecision("input.answer", principal, execution, "Answered "+input.Agent+"'s question", map[string]string{"input": input.ID, "step": input.StepID, "choice": choice})
		}
	}
	service.emit("inkuna:inbox", input.ID)
	service.emit("inkuna:runs", input.ExecutionID)
	return InputView{InputRequest: input, Permissions: input.Permissions, RunTitle: run.view.Title}, nil
}

// RunContext shows exactly what an agent received for a step, the envelope
// and the prompt of each call, and the handoff it returned, which docs it
// read, what each of its model calls consumed, and the feedback each round
// sent it back with.
func (service *Service) RunContext(runID, stepID string) (StepContextView, error) {
	view, err := service.runContext(runID, stepID)
	if err != nil {
		return view, err
	}
	view.DocReads = []DocReadView{}
	view.Usage = []domain.UsageRecord{}
	view.Feedback = []FeedbackRoundView{}
	execution, err := service.store.Execution(service.context(), runID)
	if err != nil {
		return view, nil
	}
	items, err := service.store.TargetFeedback(service.context(), runID, stepID)
	if err != nil {
		return StepContextView{}, fmt.Errorf("load the step's feedback: %w", err)
	}
	for _, item := range items {
		if last := len(view.Feedback) - 1; last >= 0 && view.Feedback[last].Round == item.Round {
			view.Feedback[last].Items = append(view.Feedback[last].Items, item)
		} else {
			view.Feedback = append(view.Feedback, FeedbackRoundView{Round: item.Round, Items: []storage.FeedbackItem{item}})
		}
	}
	// Running calls are read first: a call is stored before it is
	// forgotten, so one that ends meanwhile is then among the stored calls.
	running := service.runningCalls(runID, stepID)
	if view.Usage, err = service.store.StepUsage(service.context(), runID, stepID); err != nil {
		return StepContextView{}, fmt.Errorf("load step usage: %w", err)
	}
	stored := make(map[string]bool, len(view.Usage))
	for _, call := range view.Usage {
		stored[call.CallID] = true
	}
	for _, call := range running {
		if !stored[call.CallID] {
			view.Usage = append(view.Usage, call)
		}
	}
	reads, err := service.stepDocReads(service.context(), execution.TaskID, runID, stepID)
	if err != nil {
		return StepContextView{}, err
	}
	// The envelope lists docs by their path in the repository; reads are
	// kept by their path under the docs root the step had, which today's
	// configuration may have moved.
	delivered := make(map[string]bool, len(view.Envelope.Docs))
	for _, doc := range view.Envelope.Docs {
		delivered[doc.Path] = true
	}
	for _, read := range reads {
		listed := !strings.HasSuffix(read.DocPath, "/") && delivered[pathpkg.Join(read.DocsDir, read.DocPath)]
		view.DocReads = append(view.DocReads, DocReadView{DocRead: read, Delivered: listed})
	}
	return view, nil
}

func (service *Service) runContext(runID, stepID string) (StepContextView, error) {
	service.runsMu.RLock()
	run := service.runs[runID]
	if run == nil {
		service.runsMu.RUnlock()
		return WorkflowRunViewContext(service, runID, stepID)
	}
	envelope, ok := run.contexts[stepID]
	result, hasResult := run.results[stepID]
	outbound, remoteStep := run.outbound[stepID]
	service.runsMu.RUnlock()
	if remoteStep {
		// A remote advisor has no envelope or prompt: what it received is the
		// outbound payload, shown as it was sent.
		view := StepContextView{StepID: stepID, Prompt: outbound}
		for _, step := range run.spec.Steps {
			if step.ID == stepID {
				view.Agent = step.Agent
			}
		}
		if hasResult {
			view.Handoff = &result
		}
		return view, nil
	}
	if !ok {
		return StepContextView{}, fmt.Errorf("step %q has no context yet", stepID)
	}
	view := StepContextView{StepID: stepID, Agent: envelope.Agent, Envelope: envelope}
	if hasResult {
		view.Handoff = &result
	}
	return view, nil
}

// WorkflowRunViewContext recovers a step context from stored artifacts for
// runs that are no longer in memory.
func WorkflowRunViewContext(service *Service, runID, stepID string) (StepContextView, error) {
	if service.store == nil {
		return StepContextView{}, fmt.Errorf("run %q was not found", runID)
	}
	execution, err := service.store.Execution(service.context(), runID)
	if errors.Is(err, domain.ErrNotFound) {
		return StepContextView{}, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return StepContextView{}, err
	}
	{
		records, err := service.store.TaskArtifacts(service.context(), execution.TaskID)
		if err != nil {
			return StepContextView{}, err
		}
		view := StepContextView{StepID: stepID}
		for _, record := range records {
			if record.ExecutionID != runID || record.StepID != stepID {
				continue
			}
			switch record.Kind {
			case "context":
				data, _, err := service.readArtifact(record.Digest, 4<<20)
				if err == nil {
					_ = json.Unmarshal([]byte(data), &view.Envelope)
					view.Agent = view.Envelope.Agent
				}
			case RemoteRequestArtifact:
				if data, _, err := service.readArtifact(record.Digest, 4<<20); err == nil {
					view.Prompt, view.Agent = data, record.Agent
				}
			case "handoff":
				data, _, err := service.readArtifact(record.Digest, 4<<20)
				if err == nil {
					var handoff domain.HandoffResult
					if json.Unmarshal([]byte(data), &handoff) == nil {
						view.Handoff = &handoff
					}
				}
			}
		}
		if view.Envelope.TaskID == "" && view.Prompt == "" {
			return StepContextView{}, fmt.Errorf("step %q has no stored context", stepID)
		}
		return view, nil
	}
}

// RunArtifacts lists the artifacts produced for the run's task.
func (service *Service) RunArtifacts(runID string) ([]ArtifactView, error) {
	view, err := service.WorkflowRun(runID)
	if err != nil {
		return nil, err
	}
	records, err := service.store.TaskArtifacts(service.context(), view.TaskID)
	if err != nil {
		return nil, err
	}
	views := make([]ArtifactView, 0, len(records))
	for _, record := range records {
		views = append(views, ArtifactView{ArtifactRecord: record})
	}
	return views, nil
}

// ReadArtifact returns a bounded preview of one artifact.
func (service *Service) ReadArtifact(digest string) (ArtifactView, error) {
	if err := service.ready(); err != nil {
		return ArtifactView{}, err
	}
	record, err := service.store.ArtifactRecord(service.context(), digest)
	if err != nil {
		return ArtifactView{}, err
	}
	view := ArtifactView{ArtifactRecord: record}
	if !textual(record.MediaType) {
		view.Binary = true
		return view, nil
	}
	content, truncated, err := service.readArtifact(digest, maxArtifactPreview)
	if err != nil {
		return ArtifactView{}, err
	}
	view.Content = content
	view.Truncated = truncated
	return view, nil
}

func (service *Service) readArtifact(digest string, limit int64) (string, bool, error) {
	reader, err := service.artifacts.Open(digest)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return "", false, err
	}
	if int64(len(data)) > limit {
		return string(data[:limit]), true, nil
	}
	return string(data), false, nil
}

// RunTaskView is one A2A task of a run's conversation. A round's target's
// task also carries what the round sent its step back with and the step's
// replies, which its messages hold only inside the prompt.
type RunTaskView struct {
	Task     a2a.Task               `json:"task"`
	Feedback []storage.FeedbackItem `json:"feedback,omitempty"`
}

// RunMessages returns the A2A tasks exchanged for the run's task, which is
// the agent-to-agent conversation history.
func (service *Service) RunMessages(runID string) ([]RunTaskView, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	ctx := service.context()
	execution, err := service.execution(ctx, runID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("run %q was not found", runID)
	}
	if err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}
	var tasks []a2a.Task
	if service.bus != nil {
		tasks = service.bus.Tasks(execution.TaskID)
	}
	if len(tasks) == 0 {
		if tasks, err = service.store.A2ATasks(ctx, execution.TaskID); err != nil {
			return nil, fmt.Errorf("list the A2A tasks of run %s: %w", runID, err)
		}
	}
	views := make([]RunTaskView, 0, len(tasks))
	for _, task := range tasks {
		feedback, err := service.taskFeedback(ctx, task.Metadata, task.History)
		if err != nil {
			return nil, err
		}
		views = append(views, RunTaskView{Task: task, Feedback: feedback})
	}
	return views, nil
}

func textual(mediaType string) bool {
	mediaType = strings.ToLower(mediaType)
	return strings.HasPrefix(mediaType, "text/") || strings.Contains(mediaType, "json") || strings.Contains(mediaType, "yaml") || strings.Contains(mediaType, "xml") || strings.Contains(mediaType, "markdown") || strings.Contains(mediaType, "diff")
}
