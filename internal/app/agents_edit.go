package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// Agent scopes name where a definition file is written.
const (
	AgentScopeRepository = "repository"
	AgentScopeGlobal     = "global"
)

// AgentLocationsView tells the view where each scope saves definitions.
type AgentLocationsView struct {
	Repository string `json:"repository,omitempty"`
	Global     string `json:"global,omitempty"`
}

// AgentLocations returns the directory behind each scope for a project.
func (service *Service) AgentLocations(projectID string) (AgentLocationsView, error) {
	if err := service.ready(); err != nil {
		return AgentLocationsView{}, err
	}
	view := AgentLocationsView{}
	if len(service.config.AgentDirectories) > 0 {
		view.Global = service.config.AgentDirectories[0]
	}
	if strings.TrimSpace(projectID) != "" {
		project, err := service.workableProject(service.context(), projectID)
		if err != nil {
			return AgentLocationsView{}, err
		}
		view.Repository = filepath.Join(project.Repository, ".inkuna", "agents")
	}
	return view, nil
}

// ParseAgent validates the text of an agent Markdown file, for importing a
// definition written elsewhere. Nothing is saved.
func (service *Service) ParseAgent(content string) (agent.Definition, error) {
	return agent.Parse(content)
}

// SaveAgent validates a definition and writes <scope dir>/<name>.md. When
// originalName differs and has a file in the same scope, that file is removed
// after the new one is in place, which is how a rename is saved. A new or
// renamed agent never replaces a definition already in the scope. Runs already
// in flight keep the registry snapshot they started with.
func (service *Service) SaveAgent(projectID, scope, originalName, definitionJSON string) (AgentView, error) {
	var definition agent.Definition
	decoder := json.NewDecoder(bytes.NewReader([]byte(definitionJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return AgentView{}, fmt.Errorf("decode agent: %w", err)
	}
	definition.Source, definition.Path = "", ""
	if err := definition.Validate(); err != nil {
		return AgentView{}, err
	}
	if definition.Runtime != "" {
		if _, err := service.runtimeFor(definition.Runtime); err != nil {
			return AgentView{}, fmt.Errorf("agent %q: %w", definition.Name, err)
		}
	}
	declared := map[string]bool{}
	for _, secret := range service.config.File.Secrets {
		declared[secret.Name] = true
	}
	for _, name := range definition.Secrets {
		if !declared[name] {
			return AgentView{}, fmt.Errorf("agent %q uses secret %q, which is not declared in inkuna.yaml", definition.Name, name)
		}
	}
	originalName = strings.TrimSpace(originalName)
	service.agentFilesMu.Lock()
	defer service.agentFilesMu.Unlock()
	directory, err := service.agentDirectory(projectID, scope, originalName, definition.Name)
	if err != nil {
		return AgentView{}, err
	}
	// Saving under the name being edited replaces that file or overrides a lower
	// layer; any other save must not land on a definition already in this scope.
	renaming := false
	if originalName != definition.Name {
		// Global directories layer over each other, so a name held by any of them
		// is taken: the new file would shadow that definition or hide behind it.
		searched := []string{directory}
		if scope == AgentScopeGlobal {
			searched = service.config.AgentDirectories
		}
		for _, candidate := range searched {
			taken, err := agent.Exists(candidate, definition.Name)
			if err != nil {
				return AgentView{}, fmt.Errorf("save agent: %w", err)
			}
			if taken {
				return AgentView{}, fmt.Errorf("agent %q already has a definition file in %s; edit or delete it first", definition.Name, candidate)
			}
		}
		// The original may be a built-in or live in another scope; only a file
		// in this scope is part of the rename.
		if renaming, err = agent.Exists(directory, originalName); err != nil {
			return AgentView{}, fmt.Errorf("save agent: %w", err)
		}
	}
	content, err := agent.Marshal(definition)
	if err != nil {
		return AgentView{}, fmt.Errorf("save agent: %w", err)
	}
	project := ""
	if scope == AgentScopeRepository {
		project = projectID
	}
	if err := service.decide(service.context(), saveDecision(extension.PointAgentSave, project, filepath.Join(directory, definition.Name+".md"), content, map[string]string{
		"agent": definition.Name, "scope": scope, "renamedFrom": renamedFrom(renaming, originalName), "runtime": string(definition.Runtime),
		"permission": string(definition.Permission), "secrets": policyList(definition.Secrets...), "mcpServers": policyList(definition.MCP...),
	}), nil); err != nil {
		return AgentView{}, err
	}
	path, err := agent.Write(directory, definition)
	if err != nil {
		return AgentView{}, fmt.Errorf("save agent: %w", err)
	}
	if renaming {
		if err := agent.Remove(directory, originalName); err != nil {
			// The new name was free before this save, so taking the new file back
			// out restores the scope instead of leaving both definitions active.
			if undo := agent.Remove(directory, definition.Name); undo != nil {
				err = errors.Join(err, undo)
			}
			return AgentView{}, fmt.Errorf("rename agent %q: %w", originalName, err)
		}
	}
	if service.auditing() {
		service.auditChange(auditAgentSave, projectID, "Saved agent "+definition.Name, map[string]string{"agent": definition.Name, "scope": scope, "renamedFrom": renamedFrom(renaming, originalName)}, path, content)
	}
	views, err := service.Agents(projectID)
	if err != nil {
		return AgentView{}, err
	}
	for _, view := range views {
		if view.Definition.Name == definition.Name {
			return view, nil
		}
	}
	return AgentView{}, fmt.Errorf("agent %q was saved but is not in the registry", definition.Name)
}

// DeleteAgent removes a definition file from a scope. Built-in agents have no
// file; deleting an override brings the lower layer back.
func (service *Service) DeleteAgent(projectID, scope, name string) error {
	name = strings.TrimSpace(name)
	service.agentFilesMu.Lock()
	defer service.agentFilesMu.Unlock()
	directory, err := service.agentDirectory(projectID, scope, name)
	if err != nil {
		return err
	}
	if err := agent.Remove(directory, name); err != nil {
		return err
	}
	service.auditChange(auditAgentDelete, projectID, "Deleted agent "+name, map[string]string{"agent": name, "scope": scope}, "", nil)
	return nil
}

// agentDirectory resolves where a scope keeps the named agents. Later global
// directories override earlier ones, so the global scope is the last directory
// holding one of the names, tried in order; new agents go to the first.
func (service *Service) agentDirectory(projectID, scope string, names ...string) (string, error) {
	locations, err := service.AgentLocations(projectID)
	if err != nil {
		return "", err
	}
	switch scope {
	case AgentScopeRepository:
		if locations.Repository == "" {
			return "", errors.New("choose a project to save a repository agent")
		}
		return locations.Repository, nil
	case AgentScopeGlobal:
		if locations.Global == "" {
			return "", errors.New("no global agent directory is configured")
		}
		directories := service.config.AgentDirectories
		for _, name := range names {
			for index := len(directories) - 1; index >= 0; index-- {
				held, err := agent.Exists(directories[index], name)
				if err != nil {
					return "", fmt.Errorf("find agent %q: %w", name, err)
				}
				if held {
					return directories[index], nil
				}
			}
		}
		return locations.Global, nil
	default:
		return "", fmt.Errorf("unknown agent scope %q", scope)
	}
}

// overriddenLayer reports which lower layer a file-backed definition shadows.
func overriddenLayer(definition agent.Definition, builtIn, global map[string]bool) agent.Source {
	switch definition.Source {
	case agent.SourceRepository:
		if global[definition.Name] {
			return agent.SourceGlobal
		}
		if builtIn[definition.Name] {
			return agent.SourceBuiltIn
		}
	case agent.SourceGlobal:
		if builtIn[definition.Name] {
			return agent.SourceBuiltIn
		}
	}
	return ""
}
