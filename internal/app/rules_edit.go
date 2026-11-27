package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// SaveRules validates the edited document and writes it to the project's
// rules file: the inkuna.yaml override when one is declared, otherwise
// .inkuna/rules.yaml. The next plan for the project uses it.
func (service *Service) SaveRules(projectID, documentJSON string) (RulesView, error) {
	project, document, err := service.editedRules(projectID, documentJSON)
	if err != nil {
		return RulesView{}, err
	}
	path := service.rulesPath(project)
	content, err := rules.Marshal(document)
	if err != nil {
		return RulesView{}, fmt.Errorf("save rules: %w", err)
	}
	if err := service.decide(service.context(), saveDecision(extension.PointRulesSave, project.ID, path, content, nil), nil); err != nil {
		return RulesView{}, err
	}
	if err := rules.Write(path, document); err != nil {
		return RulesView{}, fmt.Errorf("save rules: %w", err)
	}
	service.auditChange(auditRulesSave, project.ID, "Saved the rules of "+project.Name, nil, path, content)
	return service.Rules(projectID)
}

// RulesToYAML renders the edited document as YAML for the raw editor. It
// doubles as validation: an invalid document returns the reason instead.
func (service *Service) RulesToYAML(projectID, documentJSON string) (string, error) {
	_, document, err := service.editedRules(projectID, documentJSON)
	if err != nil {
		return "", err
	}
	data, err := rules.Marshal(document)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RulesFromYAML parses raw YAML back into a document for the form editor,
// applying the same validation as a save.
func (service *Service) RulesFromYAML(projectID, content string) (rules.Document, error) {
	if err := service.ready(); err != nil {
		return rules.Document{}, err
	}
	project, err := service.store.Project(service.context(), projectID)
	if err != nil {
		return rules.Document{}, fmt.Errorf("load project: %w", err)
	}
	document, err := rules.ParseWith(content, service.stageLibrary())
	if err != nil {
		return rules.Document{}, err
	}
	if err := service.checkRuleAgents(project, document); err != nil {
		return rules.Document{}, err
	}
	document.Source = service.rulesPath(project)
	return document, nil
}

// editedRules decodes a document sent by the view and applies every check a
// saved file must pass. Go stays the authority on what a valid flow is.
func (service *Service) editedRules(projectID, documentJSON string) (storage.Project, rules.Document, error) {
	if err := service.ready(); err != nil {
		return storage.Project{}, rules.Document{}, err
	}
	project, err := service.workableProject(service.context(), projectID)
	if err != nil {
		return storage.Project{}, rules.Document{}, err
	}
	document, err := decodeRules(documentJSON)
	if err != nil {
		return storage.Project{}, rules.Document{}, err
	}
	// The view sends back the version it was given. A document that now
	// uses shared stages needs the version that has them; one that does not
	// keeps its own, so builds that know only version 1 still read it.
	if needed := document.RequiredVersion(); document.Version < needed {
		document.Version = needed
	}
	document.Source = ""
	document = document.WithLibrary(service.stageLibrary())
	if err := document.Validate(); err != nil {
		return storage.Project{}, rules.Document{}, err
	}
	if err := service.checkRuleAgents(project, document); err != nil {
		return storage.Project{}, rules.Document{}, err
	}
	return project, document, nil
}

// checkRuleAgents rejects stages staffed with an agent the project cannot
// resolve, or one that cannot write on a stage that says it writes, so the
// mistake surfaces while editing rather than at plan time. Flows are checked
// as planning sees them, with their uses expanded, so a use's override is
// held to the same agents as an inline stage; shared stages are checked too,
// whether or not a flow uses them yet. A refusal says which flow entry or
// shared stage it is about, as Document.Validate's do.
func (service *Service) checkRuleAgents(project storage.Project, document rules.Document) error {
	registry, _, err := service.agentRegistry(project.Repository)
	if err != nil {
		return fmt.Errorf("load agents: %w", err)
	}
	check := func(stage rules.Stage) error {
		name := strings.TrimSpace(stage.Agent)
		if name == "" || name == agent.Auto || name == agent.Human {
			return nil
		}
		definition, ok := registry.Get(name)
		if !ok {
			return fmt.Errorf("stage %s names unknown agent %q", stage.Label(), name)
		}
		if definition.Remote() && stage.Permission == domain.PermissionWorktree {
			return fmt.Errorf("stage %s writes to the worktree, which remote agent %q cannot do; remote agents are read-only advisors", stage.Label(), name)
		}
		// A stage that leaves its permission out writes only for the task
		// kinds that do, so only one that says so is refused.
		if stage.Permission == domain.PermissionWorktree && definition.Permission != domain.PermissionWorktree {
			return fmt.Errorf("stage %s writes to the worktree, which read-only agent %q cannot do; name a writable agent or make the stage read-only", stage.Label(), name)
		}
		return nil
	}
	names := make([]string, 0, len(document.Stages))
	for name := range document.Stages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		stage := document.Stages[name]
		stage.ID = name
		if err := check(stage); err != nil {
			return &rules.DefinitionError{Name: name, Err: fmt.Errorf("shared stage %q: %w", name, err)}
		}
	}
	checkFlow := func(rule int, ruleName string, flow rules.Flow) error {
		expanded, entries, err := document.ExpandFlowEntries(flow)
		if err != nil {
			return &rules.FlowError{Rule: rule, Name: ruleName, Entry: -1, Err: err}
		}
		for index, stage := range expanded.Stages {
			if err := check(stage); err != nil {
				return &rules.FlowError{Rule: rule, Name: ruleName, Entry: entries[index], Err: err}
			}
		}
		return nil
	}
	for index, rule := range document.Rules {
		if err := checkFlow(index, rule.Name, rule.Then); err != nil {
			return err
		}
	}
	if document.Default != nil {
		if err := checkFlow(-1, "", *document.Default); err != nil {
			return err
		}
	}
	return nil
}

// rulesPath is where a project's rules are read from and saved to.
func (service *Service) rulesPath(project storage.Project) string {
	path, _ := service.repositoryPath(project, func(repository config.RepositoryConfig) string { return repository.Rules }, rules.RelativePath)
	return path
}

// repositoryConfig returns the inkuna.yaml entry declared for a project's
// repository, if any.
func (service *Service) repositoryConfig(project storage.Project) (config.RepositoryConfig, bool) {
	return service.config.File.Repository(project.Repository)
}

// repositoryPath resolves a file or directory a project's repository may
// override in inkuna.yaml. The first entry for the repository that declares
// the override wins; one with a blank value is skipped. A declared override
// resolves against the repository unless absolute; otherwise the
// slash-separated default does. The second result reports whether
// inkuna.yaml declared an override.
func (service *Service) repositoryPath(project storage.Project, override func(config.RepositoryConfig) string, fallback string) (string, bool) {
	for _, repository := range service.config.File.Repositories {
		if filepath.Clean(repository.Path) != filepath.Clean(project.Repository) {
			continue
		}
		declared := strings.TrimSpace(override(repository))
		switch {
		case declared == "":
			continue
		case filepath.IsAbs(declared):
			return declared, true
		default:
			return filepath.Join(project.Repository, declared), true
		}
	}
	return filepath.Join(project.Repository, filepath.FromSlash(fallback)), false
}
