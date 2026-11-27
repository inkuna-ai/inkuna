package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// savedRepositories reads the repositories inkuna.yaml lists, as written.
func savedRepositories(t *testing.T, service *Service) []config.RepositoryConfig {
	t.Helper()
	file, _, err := config.ReadFile(config.DefaultPath(service.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	return file.Repositories
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConnectingAProjectWritesInkunaYAMLAndResolvesTheRepositoryRoot(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	repository := branchingRepository(t, "")
	inside := filepath.Join(repository, "internal", "app")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	candidate, err := service.InspectRepository(inside)
	if err != nil || candidate.Refused != "" || realPath(t, candidate.Path) != realPath(t, repository) || candidate.Name != "repository" {
		t.Fatalf("a folder inside a repository names its root: %#v, %v", candidate, err)
	}
	for folder, want := range map[string]string{
		"":                  "Choose the folder",
		"relative/path":     "full path",
		t.TempDir():         "is not in a git repository",
		inside + "-missing": "is not a folder",
	} {
		if candidate, err := service.InspectRepository(folder); err != nil || !strings.Contains(candidate.Refused, want) {
			t.Fatalf("InspectRepository(%q) = %#v, %v; want a refusal saying %q", folder, candidate, err, want)
		}
		if _, err := service.AddProject("", folder, "codex"); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("AddProject(%q) = %v; want %q", folder, err, want)
		}
	}

	project, err := service.AddProject("", inside, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "repository" || realPath(t, project.Repository) != realPath(t, repository) || project.DefaultRuntime != domain.RuntimeCodex {
		t.Fatalf("the project is the repository's root under its suggested name: %#v", project)
	}
	if repositories := savedRepositories(t, service); len(repositories) != 1 || repositories[0].Name != "repository" || !samePath(repositories[0].Path, repository) || repositories[0].DefaultRuntime != domain.RuntimeCodex {
		t.Fatalf("inkuna.yaml lists the connected project: %#v", repositories)
	}
	dashboard, err := service.Dashboard()
	if err != nil {
		t.Fatal(err)
	}
	if !containsProject(dashboard.Projects, project.ID) {
		t.Fatalf("Projects lists the connected project: %#v", dashboard.Projects)
	}

	// The same repository, from any folder in it, is already connected.
	if _, err := service.AddProject("Again", repository, "codex"); err == nil || !strings.Contains(err.Error(), "already connected as repository") {
		t.Fatalf("a connected repository must be refused, naming its project: %v", err)
	}
	// Another repository cannot take a name inkuna.yaml already gives one.
	if _, err := service.AddProject("repository", branchingRepository(t, ""), "codex"); err == nil || !strings.Contains(err.Error(), "already named repository") {
		t.Fatalf("a taken name must be refused: %v", err)
	}
}

func containsProject(projects []ProjectView, id string) bool {
	for _, project := range projects {
		if project.ID == id {
			return true
		}
	}
	return false
}

// listTestProject writes the project workflowTestService made, with id
// "project", into inkuna.yaml, as startup does for a project from before
// the file listed them.
// Saving the file installs the configuration it holds, with its runtimes,
// orchestrator, and notifier, so the test's own are put back, keeping only
// the file, for the runs that follow.
func listTestProject(t *testing.T, service *Service) {
	t.Helper()
	service.runtimesMu.RLock()
	runtimes := service.runtimes
	service.runtimesMu.RUnlock()
	configuration, notifier := service.config, service.notifier
	service.adoptProjects(context.Background())
	if repositories := savedRepositories(t, service); len(repositories) != 1 {
		t.Fatalf("the project must be listed: %#v", repositories)
	}
	configuration.File = service.config.File
	service.config, service.notifier = configuration, notifier
	service.runtimesMu.Lock()
	service.runtimes = runtimes
	service.runtimesMu.Unlock()
}

func TestRemovingAProjectIsRefusedWhileARunIsActiveAndKeepsItsHistory(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	release := make(chan struct{})
	writer := writingRuntime()
	write := writer.handle
	writer.handle = func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return write(ctx, request)
	}
	repository := branchingRepository(t, manualWorktrees)
	service := workflowTestService(t, repository, writer)
	listTestProject(t, service)

	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the login crash #42", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "running")
	preview, err := service.PreviewRemoveProject("project")
	if err != nil || !strings.Contains(preview.Refused, "cannot be removed while run "+run.ID+" is running") || preview.Tasks != 1 {
		t.Fatalf("a running run must stop the removal: %#v, %v", preview, err)
	}
	if _, err := service.RemoveProject("project", false); err == nil || !strings.Contains(err.Error(), "is running") {
		t.Fatalf("RemoveProject must refuse while a run is active: %v", err)
	}
	// A settings save that drops the repository is refused the same way.
	view := service.configurationView()
	view.File.Repositories = nil
	encoded, err := json.Marshal(view.File)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveConfiguration(string(encoded)); err == nil || !strings.Contains(err.Error(), "is running") {
		t.Fatalf("a settings save must not remove a project with an active run: %v", err)
	}
	if len(savedRepositories(t, service)) != 1 {
		t.Fatal("a refused save must write nothing")
	}

	close(release)
	finished := settledRun(t, service, run.ID, "completed")
	preview, err = service.PreviewRemoveProject("project")
	if err != nil || preview.Refused != "" || preview.Tasks != 1 || preview.Runs != 1 || len(preview.Worktrees) != 1 || !samePath(preview.Worktrees[0], finished.Worktree) {
		t.Fatalf("a project whose runs ended can be removed: %#v, %v", preview, err)
	}
	removal, err := service.RemoveProject("project", false)
	if err != nil || removal.Project.RemovedAt == "" || removal.DeletedTasks != 0 {
		t.Fatalf("RemoveProject = %#v, %v", removal, err)
	}
	if repositories := savedRepositories(t, service); len(repositories) != 0 {
		t.Fatalf("the project's inkuna.yaml entry must go: %#v", repositories)
	}
	dashboard, err := service.Dashboard()
	if err != nil {
		t.Fatal(err)
	}
	if containsProject(dashboard.Projects, "project") || !containsProject(dashboard.RemovedProjects, "project") {
		t.Fatalf("a removed project leaves Projects and is named for its history: %#v", dashboard)
	}
	if tasks, err := service.store.ProjectTaskIDs(context.Background(), "project"); err != nil || len(tasks) != 1 {
		t.Fatalf("keeping history keeps the task: %v, %v", tasks, err)
	}
	if _, err := service.WorkflowRun(run.ID); err != nil {
		t.Fatalf("the run stays readable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repository, "README.md")); err != nil {
		t.Fatal("removing a project must leave the repository alone")
	}
	if _, err := os.Stat(finished.Worktree); err != nil || !branchExists(repository, finished.Branch) {
		t.Fatal("removing a project must leave its runs' worktrees and branches alone")
	}
	// A removed project takes no new work.
	if _, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "More", Kind: domain.TaskResearch}); err == nil || !strings.Contains(err.Error(), "was removed") {
		t.Fatalf("planning for a removed project must be refused: %v", err)
	}
	if _, err := service.RemoveProject("project", false); err == nil || !strings.Contains(err.Error(), "was removed") {
		t.Fatalf("removing a removed project must be refused: %v", err)
	}

	// Connecting its repository again brings it back, history and all.
	candidate, err := service.InspectRepository(repository)
	if err != nil || candidate.Refused != "" || candidate.Restores != "Project" {
		t.Fatalf("the candidate must say it restores the project: %#v, %v", candidate, err)
	}
	restored, err := service.AddProject("", repository, "codex")
	if err != nil || restored.ID != "project" || restored.Name != "Project" || restored.RemovedAt != "" {
		t.Fatalf("connecting the repository again restores the project under its name: %#v, %v", restored, err)
	}
	if tasks, err := service.store.ProjectTaskIDs(context.Background(), "project"); err != nil || len(tasks) != 1 {
		t.Fatalf("the restored project keeps its history: %v, %v", tasks, err)
	}
}

func TestRemovingAProjectCanDeleteItsHistory(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	repository := branchingRepository(t, manualWorktrees)
	service := workflowTestService(t, repository, writingRuntime())
	listTestProject(t, service)
	run := finishedTask(t, service, "Fix the login crash #42")

	removal, err := service.RemoveProject("project", true)
	if err != nil || removal.DeletedTasks != 1 || removal.Project.RemovedAt == "" {
		t.Fatalf("RemoveProject with its history = %#v, %v", removal, err)
	}
	if tasks, err := service.store.ProjectTaskIDs(context.Background(), "project"); err != nil || len(tasks) != 0 {
		t.Fatalf("the project's tasks must be deleted: %v, %v", tasks, err)
	}
	if _, err := service.WorkflowRun(run.ID); err == nil {
		t.Fatal("the deleted run must be gone")
	}
	if _, err := os.Stat(run.Worktree); err != nil || !branchExists(repository, run.Branch) {
		t.Fatal("deleting history with the project must keep its worktrees and branches")
	}
}

func TestStartupBringsProjectsInLineWithInkunaYAML(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	repository := branchingRepository(t, "")
	service := workflowTestService(t, repository, &workflowRuntime{})
	ctx := context.Background()

	// A project from before inkuna.yaml listed projects is written into it,
	// not taken for one the person removed.
	service.adoptProjects(ctx)
	repositories := savedRepositories(t, service)
	if len(repositories) != 1 || repositories[0].Name != "Project" || !samePath(repositories[0].Path, repository) {
		t.Fatalf("startup must write the unlisted project into inkuna.yaml: %#v", repositories)
	}
	project, err := service.store.Project(ctx, "project")
	if err != nil || !project.Listed || project.Removed() {
		t.Fatalf("the adopted project is listed and connected: %#v, %v", project, err)
	}

	// An entry removed by hand removes the project, keeping its history, and
	// one added by hand connects its repository.
	other := branchingRepository(t, "")
	file := service.config.File
	file.Repositories = []config.RepositoryConfig{{Name: "other", Path: other, DefaultRuntime: domain.RuntimeCodex}}
	if err := config.WriteFile(config.DefaultPath(service.dataDir), file); err != nil {
		t.Fatal(err)
	}
	service.config.File = file
	service.adoptProjects(ctx)
	if project, err = service.store.Project(ctx, "project"); err != nil || !project.Removed() {
		t.Fatalf("a project whose entry was removed by hand is removed: %#v, %v", project, err)
	}
	connected, err := service.store.Projects(ctx)
	if err != nil || len(connected) != 1 || connected[0].Name != "other" || !samePath(connected[0].Repository, other) {
		t.Fatalf("an entry added by hand connects its repository: %#v, %v", connected, err)
	}
	if repositories := savedRepositories(t, service); len(repositories) != 1 || repositories[0].Name != "other" {
		t.Fatalf("a removed project must not be written back: %#v", repositories)
	}
}

func TestRemovingAProjectKeepsItsHistoryWhenInkunaYAMLCannotBeSaved(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	repository := branchingRepository(t, manualWorktrees)
	service := workflowTestService(t, repository, writingRuntime())
	listTestProject(t, service)
	finishedTask(t, service, "Fix the login crash #42")

	// A file where the configuration's folder should be makes the save fail.
	blocker := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	service.config.ConfigPath = filepath.Join(blocker, "inkuna.yaml")
	if _, err := service.RemoveProject("project", true); err == nil {
		t.Fatal("a removal whose save fails must report it")
	}
	if tasks, err := service.store.ProjectTaskIDs(context.Background(), "project"); err != nil || len(tasks) != 1 {
		t.Fatalf("history must not be deleted when the project could not be removed: %v, %v", tasks, err)
	}
	if project, err := service.store.Project(context.Background(), "project"); err != nil || project.Removed() {
		t.Fatalf("the project stays connected: %#v, %v", project, err)
	}
}

func TestConcurrentConnectsAllReachInkunaYAML(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	repositories := make([]string, 6)
	for index := range repositories {
		repositories[index] = branchingRepository(t, "")
	}
	errs := make(chan error, len(repositories))
	for index, repository := range repositories {
		go func() {
			_, err := service.AddProject(fmt.Sprintf("project-%d", index), repository, "codex")
			errs <- err
		}()
	}
	for range repositories {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	// Each connect read the file under the lock that saved it, so none was
	// lost to another's save.
	if saved := savedRepositories(t, service); len(saved) != len(repositories) {
		t.Fatalf("every connected project must be in inkuna.yaml, got %d of %d: %#v", len(saved), len(repositories), saved)
	}
	connected, err := service.store.Projects(context.Background())
	if err != nil || len(connected) != len(repositories)+1 {
		t.Fatalf("every connected project must stay connected: %d, %v", len(connected), err)
	}
}

func TestASaveListingOneRepositoryTwiceIsRefusedBeforeWriting(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	repository := branchingRepository(t, "")
	file := config.DefaultFile()
	file.Repositories = []config.RepositoryConfig{
		{Name: "one", Path: repository, DefaultRuntime: domain.RuntimeCodex},
		{Name: "two", Path: repository + string(filepath.Separator), DefaultRuntime: domain.RuntimeCodex},
	}
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveConfiguration(string(encoded)); err == nil || !strings.Contains(err.Error(), "are the same repository") {
		t.Fatalf("one repository listed twice must be refused: %v", err)
	}
	if _, err := os.Stat(config.DefaultPath(service.dataDir)); !os.IsNotExist(err) {
		t.Fatal("a refused save must write nothing")
	}
}

func TestASaveThatOpensTheStoreDoesNotWaitOnItself(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(config.EnvDataDir, dataDir)
	t.Setenv(config.EnvConfigFile, "")
	service, err := New(dataDir, extension.Product{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Shutdown(context.Background()) })
	file := config.DefaultFile()
	file.Repositories = []config.RepositoryConfig{{Name: "bakery", Path: branchingRepository(t, ""), DefaultRuntime: domain.RuntimeCodex}}
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	// The save holds configMu while it opens the store, so opening it must
	// not take configMu again.
	done := make(chan error, 1)
	go func() {
		_, err := service.SaveConfiguration(string(encoded))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("SaveConfiguration on a service whose store was not open yet did not return")
	}
	if connected, err := service.store.Projects(context.Background()); err != nil || len(connected) != 1 || connected[0].Name != "bakery" {
		t.Fatalf("the save connects its repository: %#v, %v", connected, err)
	}
}
