package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// inkuna.yaml's repositories are the list of connected projects. The
// database keeps each project's id, which its history refers to, and is
// brought in line with that list whenever the file is saved and at startup
// (reconcileProjects), so the two never drift. Connecting and removing a
// project in the app write the file first.

// RepositoryCandidate is what connecting a folder would connect, worked out
// before anything is saved, so the view can fill in the form and show Go's
// reason when the folder cannot be connected.
type RepositoryCandidate struct {
	// Path is the repository's root: the main checkout of the repository
	// the chosen folder is in, which may be a subdirectory or a worktree.
	Path string `json:"path"`
	// Name is the suggested project name: origin's repository name, else
	// the folder's.
	Name string `json:"name"`
	// Refused says why the folder cannot be connected; empty when it can.
	Refused string `json:"refused,omitempty"`
	// Restores names the removed project connecting this path brings back,
	// with its history.
	Restores string `json:"restores,omitempty"`
}

// InspectRepository works out what connecting a folder would connect: the
// repository's root and a name for it, or why it cannot be connected.
func (service *Service) InspectRepository(folder string) (RepositoryCandidate, error) {
	if err := service.ready(); err != nil {
		return RepositoryCandidate{}, err
	}
	return service.inspectRepository(service.context(), folder)
}

func (service *Service) inspectRepository(ctx context.Context, folder string) (RepositoryCandidate, error) {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return RepositoryCandidate{Refused: "Choose the folder of a git repository."}, nil
	}
	if !filepath.IsAbs(folder) {
		return RepositoryCandidate{Path: folder, Refused: "Give the folder's full path."}, nil
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return RepositoryCandidate{Path: folder, Refused: folder + " is not a folder on this machine."}, nil
	}
	root, err := worktree.MainRoot(ctx, folder)
	if err != nil {
		return RepositoryCandidate{Path: folder, Refused: folder + " is not in a git repository."}, nil
	}
	candidate := RepositoryCandidate{Path: root, Name: repositoryName(ctx, root)}
	projects, err := service.store.AllProjects(ctx)
	if err != nil {
		return RepositoryCandidate{}, fmt.Errorf("list projects: %w", err)
	}
	for _, project := range projects {
		if !samePath(project.Repository, root) {
			continue
		}
		if project.Removed() {
			candidate.Name, candidate.Restores = project.Name, project.Name
			break
		}
		candidate.Refused = fmt.Sprintf("%s is already connected as %s.", root, project.Name)
		break
	}
	return candidate, nil
}

// repositoryName is origin's repository name, else the folder's.
func repositoryName(ctx context.Context, root string) string {
	if remote := worktree.OriginRepository(ctx, root); remote != "" {
		return path.Base(remote)
	}
	return filepath.Base(root)
}

// AddProject connects the repository a folder is in as a project: it adds
// the repository to inkuna.yaml, through the same checks and atomic write a
// settings save makes, and the project follows from the file. A blank name
// takes the suggested one, and a blank runtime the orchestrator's.
// Connecting a removed project's repository brings it back, history and
// all.
func (service *Service) AddProject(name, repository, runtimeName string) (ProjectView, error) {
	if err := service.ready(); err != nil {
		return ProjectView{}, err
	}
	ctx := service.context()
	candidate, err := service.inspectRepository(ctx, repository)
	if err != nil {
		return ProjectView{}, err
	}
	if candidate.Refused != "" {
		return ProjectView{}, errors.New(candidate.Refused)
	}
	runtime := domain.Runtime(strings.TrimSpace(runtimeName))
	if runtime == "" {
		runtime = service.config.Orchestrator.Runtime
	}
	if _, err := service.runtimeFor(runtime); err != nil {
		return ProjectView{}, fmt.Errorf("invalid runtime %q", runtime)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = candidate.Name
	}
	// The name and path are checked against the file under the same lock
	// that saves it, so a project connected meanwhile is seen.
	if _, err := service.editConfiguration(func(file *config.File) error {
		for _, entry := range file.Repositories {
			if entry.Name == name {
				return fmt.Errorf("a project is already named %s; choose another name", name)
			}
			if samePath(entry.Path, candidate.Path) {
				return fmt.Errorf("%s is already connected as %s", candidate.Path, entry.Name)
			}
		}
		file.Repositories = append(file.Repositories, config.RepositoryConfig{Name: name, Path: candidate.Path, DefaultRuntime: runtime})
		return nil
	}); err != nil {
		return ProjectView{}, err
	}
	project, err := service.projectAt(ctx, candidate.Path)
	if err != nil {
		return ProjectView{}, err
	}
	return projectView(project), nil
}

// projectAt is the project whose repository is path.
func (service *Service) projectAt(ctx context.Context, repository string) (storage.Project, error) {
	projects, err := service.store.AllProjects(ctx)
	if err != nil {
		return storage.Project{}, fmt.Errorf("list projects: %w", err)
	}
	for _, project := range projects {
		if samePath(project.Repository, repository) {
			return project, nil
		}
	}
	return storage.Project{}, fmt.Errorf("%w: no project for %s", domain.ErrNotFound, repository)
}

// ProjectRemovalPreview is what removing a project would do, so the person
// can choose before anything changes.
type ProjectRemovalPreview struct {
	ProjectID  string `json:"projectId"`
	Name       string `json:"name"`
	Repository string `json:"repository"`
	// Refused says why the project cannot be removed now; empty when it can.
	Refused string `json:"refused,omitempty"`
	// Tasks and Runs count the project's history.
	Tasks int `json:"tasks"`
	Runs  int `json:"runs"`
	// Worktrees lists its runs' worktrees still on disk. Removing the
	// project leaves them, as it leaves the repository and every branch.
	Worktrees []string `json:"worktrees"`
}

// PreviewRemoveProject says whether a project can be removed now, how much
// history it has, and which of its runs' worktrees are on disk.
func (service *Service) PreviewRemoveProject(projectID string) (ProjectRemovalPreview, error) {
	if err := service.ready(); err != nil {
		return ProjectRemovalPreview{}, err
	}
	ctx := service.context()
	project, err := service.workableProject(ctx, projectID)
	if err != nil {
		return ProjectRemovalPreview{}, err
	}
	preview := ProjectRemovalPreview{ProjectID: project.ID, Name: project.Name, Repository: project.Repository, Worktrees: []string{}}
	tasks, err := service.store.ProjectTaskIDs(ctx, project.ID)
	if err != nil {
		return ProjectRemovalPreview{}, err
	}
	preview.Tasks = len(tasks)
	if len(tasks) > 0 {
		previews, err := service.PreviewDeleteTasks(tasks)
		if err != nil {
			return ProjectRemovalPreview{}, err
		}
		for _, task := range previews {
			preview.Runs += len(task.Runs)
			for _, run := range task.Runs {
				if run.Worktree != nil && run.Worktree.Path != "" {
					preview.Worktrees = append(preview.Worktrees, run.Worktree.Path)
				}
			}
		}
	}
	if preview.Refused, err = service.removalRefusal(ctx, project); err != nil {
		return ProjectRemovalPreview{}, err
	}
	return preview, nil
}

// ProjectRemoval is what removing a project did.
type ProjectRemoval struct {
	Project ProjectView `json:"project"`
	// DeletedTasks counts the tasks deleted with it; zero when its history
	// was kept.
	DeletedTasks int `json:"deletedTasks"`
}

// RemoveProject removes a project: its inkuna.yaml entry goes, and it leaves
// Projects and New request. Its history is kept, readable under its name,
// unless deleteHistory asks to delete its finished tasks and runs too, as
// deleting them from Tasks would. Either way the repository on disk, its
// branches, its runs' worktrees, GitHub, and Linear are left alone. It is
// refused while any of the project's runs has not ended.
func (service *Service) RemoveProject(projectID string, deleteHistory bool) (ProjectRemoval, error) {
	if err := service.ready(); err != nil {
		return ProjectRemoval{}, err
	}
	ctx := service.context()
	project, err := service.workableProject(ctx, projectID)
	if err != nil {
		return ProjectRemoval{}, err
	}
	if err := service.detachProject(ctx, project); err != nil {
		return ProjectRemoval{}, err
	}
	if project, err = service.store.Project(ctx, project.ID); err != nil {
		return ProjectRemoval{}, fmt.Errorf("load project %s: %w", projectID, err)
	}
	removal := ProjectRemoval{Project: projectView(project)}
	service.emit("inkuna:runs", "")
	if !deleteHistory {
		return removal, nil
	}
	// The project is removed before any history goes, so a save that fails
	// leaves its history whole, and once it is removed no run of it can
	// start. A history that cannot be deleted leaves the project removed
	// with its history kept, which the person can delete from Tasks.
	tasks, err := service.store.ProjectTaskIDs(ctx, project.ID)
	if err == nil && len(tasks) > 0 {
		var report DeleteReport
		if report, err = service.DeleteTasks(tasks, DeleteOptions{}); err == nil {
			removal.DeletedTasks = len(report.Tasks)
		}
	}
	if err != nil {
		return removal, fmt.Errorf("%s was removed, but its history was kept: %w", project.Name, err)
	}
	return removal, nil
}

// detachProject removes a project: it takes its entry out of inkuna.yaml,
// which marks it removed, or, for one the file never listed, marks it
// removed directly. It holds configMu across the check and the change,
// as starting a run does, so no run of the project can start between them.
func (service *Service) detachProject(ctx context.Context, project storage.Project) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()
	if refusal, err := service.removalRefusal(ctx, project); err != nil {
		return err
	} else if refusal != "" {
		return errors.New(refusal)
	}
	file := service.config.File
	kept := make([]config.RepositoryConfig, 0, len(file.Repositories))
	for _, entry := range file.Repositories {
		if !samePath(entry.Path, project.Repository) {
			kept = append(kept, entry)
		}
	}
	if len(kept) < len(file.Repositories) {
		file.Repositories = kept
		_, err := service.saveConfigurationLocked(file)
		return err
	}
	project.RemovedAt = time.Now().UTC()
	return service.store.UpsertProject(ctx, project)
}

// removalRefusal says why a project cannot be removed now: one of its runs
// has not ended, in memory, which is ahead of the store, or as stored, since
// a run paused for review outlives the process. It is "" when nothing stops
// the removal.
func (service *Service) removalRefusal(ctx context.Context, project storage.Project) (string, error) {
	service.runsMu.RLock()
	for id, run := range service.runs {
		if run.project.ID == project.ID && liveRefusal(id, run) != nil {
			state := run.view.State
			service.runsMu.RUnlock()
			return projectRunRefusal(project, id, state), nil
		}
	}
	service.runsMu.RUnlock()
	id, state, err := service.store.ProjectActiveRun(ctx, project.ID)
	if err != nil || id == "" {
		return "", err
	}
	return projectRunRefusal(project, id, state), nil
}

func projectRunRefusal(project storage.Project, runID, state string) string {
	switch state {
	case "waiting":
		state = "waiting for an answer or a permission"
	case "reviewing":
		state = "paused for review"
	case "completed", "failed", "cancelled", "interrupted":
		state = "still finishing"
	}
	return fmt.Sprintf("%s cannot be removed while run %s is %s; let it end or cancel it first.", project.Name, runID, state)
}

// workableProject is a connected project. A removed one keeps its history,
// but takes no new work, rules or agent edits, branch changes, or tracker
// writes: inkuna.yaml no longer lists it, so nothing on this machine says
// how it may be worked on.
func (service *Service) workableProject(ctx context.Context, projectID string) (storage.Project, error) {
	project, err := service.store.Project(ctx, projectID)
	if err != nil {
		return storage.Project{}, fmt.Errorf("load project %s: %w", projectID, err)
	}
	if err := refuseRemoved(project); err != nil {
		return storage.Project{}, err
	}
	return project, nil
}

// refuseRemoved refuses work on a removed project, as workableProject does.
func refuseRemoved(project storage.Project) error {
	if project.Removed() {
		return fmt.Errorf("project %s was removed; connect %s again to work on it", project.Name, project.Repository)
	}
	return nil
}

// refuseDroppedProjects refuses a save that would remove a project whose
// runs have not all ended, before anything is written. The caller holds
// configMu, which starting a run also takes, so none can start meanwhile.
func (service *Service) refuseDroppedProjects(ctx context.Context, file config.File) error {
	if service.store == nil {
		return nil
	}
	projects, err := service.store.Projects(ctx)
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	for _, project := range projects {
		if !project.Listed || listed(file, project.Repository) {
			continue
		}
		if refusal, err := service.removalRefusal(ctx, project); err != nil {
			return err
		} else if refusal != "" {
			return errors.New(refusal)
		}
	}
	return nil
}

// reconcileProjects brings the projects in line with file's repositories:
// each one listed is connected, created or brought back, and takes the
// entry's name and runtime; each connected project that was listed before
// and is no longer is removed, keeping its history. A project the file has
// never listed, from before the file held the list, is left connected and
// returned, for startup to write into the file.
func (service *Service) reconcileProjects(ctx context.Context, file config.File) ([]storage.Project, error) {
	projects, err := service.store.AllProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	now := time.Now().UTC()
	for _, entry := range file.Repositories {
		runtime := entry.DefaultRuntime
		if runtime == "" {
			runtime = service.config.Orchestrator.Runtime
		}
		project := storage.Project{ID: newID("project"), CreatedAt: now}
		if index := slices.IndexFunc(projects, func(project storage.Project) bool { return samePath(project.Repository, entry.Path) }); index >= 0 {
			project = projects[index]
		} else {
			project.Repository = entry.Path
		}
		project.Name, project.DefaultRuntime, project.RemovedAt, project.Listed = entry.Name, runtime, time.Time{}, true
		if err := service.store.UpsertProject(ctx, project); err != nil {
			return nil, fmt.Errorf("connect %s: %w", entry.Path, err)
		}
		if index := slices.IndexFunc(projects, func(known storage.Project) bool { return known.ID == project.ID }); index >= 0 {
			projects[index] = project
		} else {
			projects = append(projects, project)
		}
	}
	unlisted := make([]storage.Project, 0)
	for _, project := range projects {
		if project.Removed() || listed(file, project.Repository) {
			continue
		}
		if !project.Listed {
			unlisted = append(unlisted, project)
			continue
		}
		project.RemovedAt = now
		if err := service.store.UpsertProject(ctx, project); err != nil {
			return nil, fmt.Errorf("remove project %s: %w", project.Name, err)
		}
	}
	return unlisted, nil
}

// adoptProjects runs at startup, once the store is open: it applies
// inkuna.yaml to the projects, then writes into the file each connected
// project it has never listed, which came from the app before the file held
// the list, so a project is not lost on upgrade. A file that cannot be
// written leaves those projects connected, to be tried again at the next
// start. It holds configMu throughout, and must not be reached from a save,
// which holds it already.
func (service *Service) adoptProjects(ctx context.Context) {
	service.configMu.Lock()
	defer service.configMu.Unlock()
	file := service.config.File
	unlisted, err := service.reconcileProjects(ctx, file)
	if err != nil {
		log.Printf("inkuna: apply inkuna.yaml's repositories to the projects: %v", err)
		return
	}
	if len(unlisted) == 0 {
		return
	}
	file.Repositories = slices.Clone(file.Repositories)
	for _, project := range unlisted {
		name := project.Name
		for suffix := 2; taken(file, name); suffix++ {
			name = fmt.Sprintf("%s-%d", project.Name, suffix)
		}
		file.Repositories = append(file.Repositories, config.RepositoryConfig{Name: name, Path: project.Repository, DefaultRuntime: project.DefaultRuntime})
	}
	if _, err := service.saveConfigurationLocked(file); err != nil {
		log.Printf("inkuna: write the projects added before inkuna.yaml listed them: %v", err)
	}
}

// refuseSharedRepositories refuses a file that lists one repository twice,
// under two names or two spellings of its path: each repository is one
// project, so the second entry could connect nothing.
func refuseSharedRepositories(file config.File) error {
	for index, entry := range file.Repositories {
		for _, earlier := range file.Repositories[:index] {
			if samePath(earlier.Path, entry.Path) {
				return fmt.Errorf("repositories %s and %s are the same repository, %s; list it once", earlier.Name, entry.Name, entry.Path)
			}
		}
	}
	return nil
}

func taken(file config.File, name string) bool {
	return slices.ContainsFunc(file.Repositories, func(entry config.RepositoryConfig) bool { return entry.Name == name })
}

func listed(file config.File, repository string) bool {
	return slices.ContainsFunc(file.Repositories, func(entry config.RepositoryConfig) bool { return samePath(entry.Path, repository) })
}
