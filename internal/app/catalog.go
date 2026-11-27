package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/catalog"
	"github.com/inkuna-ai/inkuna/internal/sources"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// loadCatalog is the catalog the bindings offer; tests give one of their
// own, since what ships changes.
var loadCatalog = catalog.Builtin

// CatalogEntry is a catalog item with where the project and this machine
// already have something of its kind by its name.
type CatalogEntry struct {
	Item catalog.Item `json:"item"`
	// Repository is where the project has it, or "" when it does not or no
	// project was given.
	Repository string `json:"repository,omitempty"`
	// Global is where the global directories have it, or "" when they do
	// not. A rule has no global scope, so it is never set for one.
	Global string `json:"global,omitempty"`
	// GlobalRefusal is why the item cannot be added globally, such as a
	// rule or a definition a source shares for the project; "" when it can.
	GlobalRefusal string `json:"globalRefusal,omitempty"`
}

// CatalogListing is the catalog as a project sees it.
type CatalogListing struct {
	Items []CatalogEntry `json:"items"`
	// RepositoryProblem is why what the project has could not be read, such
	// as rules that do not load. Adding to the project is refused for the
	// same reason until it is fixed.
	RepositoryProblem string `json:"repositoryProblem,omitempty"`
	// GlobalProblem is the same for the global directories.
	GlobalProblem string `json:"globalProblem,omitempty"`
	// Sources are the project's definition sources, when the build has
	// any, with what this build does with each definition they share: a
	// catalog one is offered among the items unless it fails its checks.
	Sources []sources.Report `json:"sources,omitempty"`
}

// CatalogItems lists the agents, skills, rules, shared stages, and groups
// the catalog offers to add, each with what it needs, its file's content,
// and where the project, when one is given, and the global directories
// already have it. A project's listing adds what its definition sources
// offer, each item naming its source and scope, and reports every
// definition they share.
func (service *Service) CatalogItems(projectID string) (CatalogListing, error) {
	shipped, err := loadCatalog()
	if err != nil {
		return CatalogListing{}, err
	}
	listing := CatalogListing{Items: []CatalogEntry{}}
	var target catalog.Target
	var targetErr error
	var reading sources.Reading
	if strings.TrimSpace(projectID) != "" {
		var project storage.Project
		if target, project, targetErr = service.catalogTarget(projectID, false); targetErr == nil {
			reading = service.readSources(service.context(), project)
			listing.Sources = reading.Reports
		}
	}
	global := service.globalCatalogTarget()
	listing.addItems(shipped, projectID, target, targetErr, global)
	for _, offered := range reading.Catalogs {
		listing.addItems(offered.Catalog, projectID, target, targetErr, global)
	}
	return listing, nil
}

// addItems lists one catalog's items with where the project and the
// global directories have each.
func (listing *CatalogListing) addItems(shipped catalog.Catalog, projectID string, target catalog.Target, targetErr error, global catalog.Target) {
	var repository map[string]string
	if strings.TrimSpace(projectID) != "" {
		err := targetErr
		if err == nil {
			repository, err = shipped.Presence(target)
		}
		if err != nil && listing.RepositoryProblem == "" {
			listing.RepositoryProblem = err.Error()
		}
	}
	present, err := shipped.Presence(global)
	if err != nil && listing.GlobalProblem == "" {
		listing.GlobalProblem = err.Error()
	}
	for _, item := range shipped.Items() {
		entry := CatalogEntry{Item: item, Repository: repository[item.Name], Global: present[item.Name]}
		if err := item.GlobalRefusal(); err != nil {
			entry.GlobalRefusal = err.Error()
		}
		listing.Items = append(listing.Items, entry)
	}
}

// PreviewCatalogItem is what AddCatalogItem would do with the same
// arguments, without writing anything: the item and each agent, skill,
// shared stage, and group it would bring, its notes, such as the built-in
// rules being written with a first rule, or Go's reason for refusing it.
func (service *Service) PreviewCatalogItem(projectID, name, source string, global bool) (catalog.Result, error) {
	return service.catalogAdd(projectID, name, source, global, catalog.Catalog.Preview)
}

// AddCatalogItem copies a catalog item, with every agent, skill, shared
// stage, and group it needs that is missing there, into a project's .inkuna
// directory and rules file, or with global into this machine's global agent
// and stage library directories, where projectID is not needed. The item is
// the built-in catalog's, or with source the one that definition source
// offers the project now. It never overwrites: an item whose name is taken
// there is refused, naming where. The next plan uses what it added.
func (service *Service) AddCatalogItem(projectID, name, source string, global bool) (catalog.Result, error) {
	return service.catalogAdd(projectID, name, source, global, catalog.Catalog.Add)
}

// catalogAdd runs an add, or its preview, against the target the
// arguments name.
func (service *Service) catalogAdd(projectID, name, source string, global bool, run func(catalog.Catalog, string, catalog.Target) (catalog.Result, error)) (catalog.Result, error) {
	if err := service.ready(); err != nil {
		return catalog.Result{}, err
	}
	source = strings.TrimSpace(source)
	if source != "" {
		return service.sourceCatalogAdd(projectID, name, source, global, run)
	}
	shipped, err := loadCatalog()
	if err != nil {
		return catalog.Result{}, err
	}
	target, _, err := service.catalogTarget(projectID, global)
	if err != nil {
		return catalog.Result{}, err
	}
	return run(shipped, strings.TrimSpace(name), target)
}

// sourceCatalogAdd adds what a definition source offers the project, asked
// for again so the add copies the definition as the source has it now.
func (service *Service) sourceCatalogAdd(projectID, name, source string, global bool, run func(catalog.Catalog, string, catalog.Target) (catalog.Result, error)) (catalog.Result, error) {
	if strings.TrimSpace(projectID) == "" {
		return catalog.Result{}, fmt.Errorf("choose a project: definition source %q shares what it offers for a project", source)
	}
	// The source shares for the project, so the add needs the project even
	// when it goes to the global directories, which the catalog refuses.
	target, project, err := service.catalogTarget(projectID, false)
	if err != nil {
		return catalog.Result{}, err
	}
	if global {
		target = service.globalCatalogTarget()
	}
	for _, offered := range service.readSources(service.context(), project).Catalogs {
		if offered.Source == source {
			return run(offered.Catalog, strings.TrimSpace(name), target)
		}
	}
	return catalog.Result{}, fmt.Errorf("definition source %q offers nothing to add to this project", source)
}

// globalCatalogTarget is this machine's global directories.
func (service *Service) globalCatalogTarget() catalog.Target {
	return catalog.Target{Global: true, AgentDirectories: service.config.AgentDirectories, StageDirectories: service.config.StageDirectories}
}

// catalogTarget is where an add goes: the global directories, or the
// project's rules file, agents directory, and skills directory, which
// planning reads. A project's target comes with the project.
func (service *Service) catalogTarget(projectID string, global bool) (catalog.Target, storage.Project, error) {
	target := service.globalCatalogTarget()
	if global {
		return target, storage.Project{}, nil
	}
	target.Global = false
	if strings.TrimSpace(projectID) == "" {
		return catalog.Target{}, storage.Project{}, errors.New("choose a project to add a catalog item to")
	}
	if err := service.ready(); err != nil {
		return catalog.Target{}, storage.Project{}, err
	}
	project, err := service.workableProject(service.context(), projectID)
	if err != nil {
		return catalog.Target{}, storage.Project{}, err
	}
	locations, err := service.AgentLocations(projectID)
	if err != nil {
		return catalog.Target{}, storage.Project{}, err
	}
	target.RulesPath, target.AgentDirectory = service.rulesPath(project), locations.Repository
	target.SkillDirectory = filepath.Join(project.Repository, ".inkuna", "skills")
	return target, project, nil
}
