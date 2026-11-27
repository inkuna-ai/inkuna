package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/pricing"
	"github.com/inkuna-ai/inkuna/internal/routing"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/internal/version"
)

func TestConfigurationReportsDefaultsAndEnvironmentOverrides(t *testing.T) {
	t.Setenv(config.EnvWorkerTimeout, "45m")
	t.Setenv(config.EnvGitBinary, "/opt/git") // no file field, so not an override
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	view, err := service.Configuration()
	if err != nil {
		t.Fatal(err)
	}
	if view.Defaults.Timeouts.Worker != "30m" || view.Defaults.GitHub.Binary != "gh" {
		t.Fatalf("defaults missing: %#v", view.Defaults)
	}
	if len(view.Overrides) != 1 || view.Overrides[0] != (config.Override{Field: "timeouts.worker", Variable: config.EnvWorkerTimeout, Value: "45m"}) {
		t.Fatalf("overrides = %#v", view.Overrides)
	}
}

// Settings and doctor both say which build is running, from the one place
// the build stamps.
func TestConfigurationAndDoctorReportTheBuild(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	view, err := service.Configuration()
	if err != nil {
		t.Fatal(err)
	}
	if view.Build != version.Current() {
		t.Fatalf("build = %+v, want %+v", view.Build, version.Current())
	}
	checks := service.Doctor()
	if len(checks) == 0 || checks[0] != (HealthCheck{Name: "Inkuna", Status: "ready", Detail: "dev (commit dev)"}) {
		t.Fatalf("doctor must lead with the build: %+v", checks)
	}
}

func TestASaveWaitsWhileARunIsBeingStartedFromTheConfiguration(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	encoded, err := json.Marshal(config.DefaultFile())
	if err != nil {
		t.Fatal(err)
	}
	// StartWorkflow holds this from checking an approval until the run has
	// copied its dependencies.
	service.configMu.RLock()
	saved := make(chan error, 1)
	go func() {
		_, err := service.SaveConfiguration(string(encoded))
		saved <- err
	}()
	select {
	case err := <-saved:
		service.configMu.RUnlock()
		t.Fatalf("a save must wait for a run being started: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	service.configMu.RUnlock()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the save must go ahead once the run has started")
	}
}

func TestRemoveSecretDeclarationForgetsValueDeclarationAndRuntimeExports(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	file := config.DefaultFile()
	file.Secrets = append(file.Secrets, config.SecretRef{Name: "NPM_TOKEN"}, config.SecretRef{Name: "KEEP_ME"})
	file.Runtimes[domain.RuntimeClaude] = config.RuntimeConfig{Binary: "claude", Secrets: []string{"NPM_TOKEN", "KEEP_ME"}}
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveConfiguration(string(encoded)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveSecret("NPM_TOKEN", "value"); err != nil {
		t.Fatal(err)
	}
	view, err := service.RemoveSecretDeclaration("NPM_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range view.File.Secrets {
		if secret.Name == "NPM_TOKEN" {
			t.Fatalf("declaration survived: %#v", view.File.Secrets)
		}
	}
	if got := view.File.Runtimes[domain.RuntimeClaude].Secrets; len(got) != 1 || got[0] != "KEEP_ME" {
		t.Fatalf("runtime exports = %#v, want only KEEP_ME", got)
	}
	for _, status := range view.Secrets {
		if status.Name == "NPM_TOKEN" {
			t.Fatalf("status still lists the removed secret: %#v", view.Secrets)
		}
	}
	if _, err := service.secretStore.Get("NPM_TOKEN"); err == nil {
		t.Fatal("the stored value must be deleted with the declaration")
	}
	if _, err := service.RemoveSecretDeclaration(" "); err == nil {
		t.Fatal("an empty name must be rejected")
	}
	if _, err := service.ChooseDirectory("Pick"); err == nil {
		t.Fatal("the folder picker must report that it needs the desktop runtime")
	}
}

func TestValidateConfigurationReportsFieldsAndSavedBandsReachRouting(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	file := config.DefaultFile()
	file.Timeouts.Worker = "soon"
	file.Routing.Bands = map[domain.Runtime][]routing.Band{domain.RuntimeClaude: {{Minimum: 1, Maximum: 4, Model: "sonnet"}}}
	encode := func(file config.File) string {
		data, err := json.Marshal(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	problems, err := service.ValidateConfiguration(encode(file))
	if err != nil || len(problems) != 2 || problems[0].Field != "timeouts.worker" || problems[1].Field != "routing.bands.claude" {
		t.Fatalf("ValidateConfiguration = %#v, %v", problems, err)
	}
	if _, err := service.SaveConfiguration(encode(file)); err == nil {
		t.Fatal("an invalid configuration must not be saved")
	}
	if _, err := service.ValidateConfiguration(`{"surprise":true}`); err == nil {
		t.Fatal("unknown fields must be rejected")
	}

	file.Timeouts.Worker = "20m"
	file.Routing.Bands[domain.RuntimeClaude] = []routing.Band{{Minimum: 1, Maximum: 10, Model: "house-model"}}
	input, output := 0.5, 2.0
	file.Pricing = pricing.Overrides{Models: map[string]pricing.Rates{"house-model": {Input: &input, Output: &output}}}
	if problems, err := service.ValidateConfiguration(encode(file)); err != nil || len(problems) != 0 {
		t.Fatalf("a valid file has problems: %#v, %v", problems, err)
	}
	view, err := service.SaveConfiguration(encode(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.RoutingDefaults.Bands[domain.RuntimeClaude]) != 10 || view.File.Routing.Bands[domain.RuntimeClaude][0].Model != "house-model" {
		t.Fatalf("view = %#v", view)
	}
	// Settings shows the built-in prices beside the file's, and calls are
	// priced with the saved ones.
	if _, builtIn := view.PricingDefaults.Price("house-model"); builtIn || len(view.PricingDefaults.Models) == 0 || *view.File.Pricing.Models["house-model"].Output != 2 {
		t.Fatalf("pricing view = %#v / %#v", view.PricingDefaults, view.File.Pricing)
	}
	if price, ok := service.pricing().Price("house-model"); !ok || price.Output != 2 {
		t.Fatalf("saved prices are not in effect: %+v", price)
	}
	dashboard, err := service.Dashboard()
	if err != nil {
		t.Fatal(err)
	}
	bands := dashboard.Defaults.RoutingPolicy.Bands
	if bands[domain.RuntimeClaude][0].Model != "house-model" || len(bands[domain.RuntimeCodex]) != 10 {
		t.Fatalf("effective routing policy = %#v", bands)
	}
}

// Saving a new data directory must leave startup able to find it, the way the
// setup wizard does, and must say that a restart is needed.
func TestSavingADataDirectoryKeepsItDiscoverable(t *testing.T) {
	root := t.TempDir()
	for _, variable := range []string{"HOME", "XDG_CONFIG_HOME", "AppData"} {
		t.Setenv(variable, root)
	}
	t.Setenv(config.EnvDataDir, "")
	t.Setenv(config.EnvConfigFile, "")
	defaultDir, err := config.DefaultDataDir()
	if err != nil || !strings.HasPrefix(defaultDir, root) {
		t.Skipf("the default data directory %q is outside the test root: %v", defaultDir, err)
	}
	launch := func(dataDir, path string) *Service {
		service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
		service.dataDir, service.discovered, service.config.ConfigPath = dataDir, true, path
		return service
	}
	save := func(service *Service, dataDir string) ConfigurationView {
		file := service.config.File
		file.DataDir = dataDir
		file.Concurrency.MaxTasks = 7 // proves the user's settings travel with the redirect
		encoded, err := json.Marshal(file)
		if err != nil {
			t.Fatal(err)
		}
		view, err := service.SaveConfiguration(string(encoded))
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	nextLaunch := func(wantDir string) config.Config {
		configuration, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if configuration.DataDir != wantDir || configuration.ConfigPath != filepath.Join(wantDir, "inkuna.yaml") || configuration.MaxTasks != 7 {
			t.Fatalf("next launch uses %s (%s, maxTasks %d), want %s with the saved settings", configuration.DataDir, configuration.ConfigPath, configuration.MaxTasks, wantDir)
		}
		return configuration
	}
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	defaultPath := filepath.Join(defaultDir, "inkuna.yaml")

	// From the default location: the saved file is its own locator.
	if view := save(launch(defaultDir, defaultPath), first); !view.RestartRequired {
		t.Fatal("a new data directory needs a restart")
	}
	nextLaunch(first)

	// A target that already has its own configuration is refused before
	// anything is written: startup would read that file, not these settings.
	foreign := filepath.Join(root, "foreign")
	foreignFile := config.DefaultFile()
	foreignFile.Concurrency.MaxTasks = 2
	if err := config.WriteFile(filepath.Join(foreign, "inkuna.yaml"), foreignFile); err != nil {
		t.Fatal(err)
	}
	refused := launch(first, filepath.Join(first, "inkuna.yaml"))
	attempt := refused.config.File
	attempt.DataDir, attempt.Concurrency.MaxTasks = foreign, 9
	encoded, err := json.Marshal(attempt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refused.SaveConfiguration(string(encoded)); err == nil || !strings.Contains(err.Error(), "already has its own inkuna.yaml") {
		t.Fatalf("a target with its own configuration must be refused: %v", err)
	}
	if kept, _, err := config.ReadFile(filepath.Join(foreign, "inkuna.yaml")); err != nil || kept.Concurrency.MaxTasks != 2 {
		t.Fatalf("the target's configuration changed: %#v, %v", kept.Concurrency, err)
	}
	nextLaunch(first) // neither the current file nor the locator took the refused save

	// From a redirected configuration: the locator has to follow.
	if view := save(launch(first, filepath.Join(first, "inkuna.yaml")), second); !view.RestartRequired {
		t.Fatal("a changed redirect needs a restart")
	}
	nextLaunch(second)

	// A second save before the restart keeps the copy it made current.
	third := filepath.Join(root, "third")
	twice := launch(second, filepath.Join(second, "inkuna.yaml"))
	save(twice, third)
	again := twice.config.File
	again.Orchestrator.MaxTurns = 9
	if encoded, err = json.Marshal(again); err != nil {
		t.Fatal(err)
	}
	if view, err := twice.SaveConfiguration(string(encoded)); err != nil || !view.RestartRequired {
		t.Fatalf("a second save toward the same directory = %#v, %v", view.RestartRequired, err)
	}
	if got := nextLaunch(third).Orchestrator.MaxTurns; got != 9 {
		t.Fatalf("the second save did not reach the new directory: maxTurns %d", got)
	}

	// Saving again without changing the directory is not a relocation.
	if view := save(launch(third, filepath.Join(third, "inkuna.yaml")), third); view.RestartRequired {
		t.Fatal("an unchanged data directory needs no restart")
	}

	// Clearing the redirect returns to the default location with the settings.
	if view := save(launch(third, filepath.Join(third, "inkuna.yaml")), ""); !view.RestartRequired {
		t.Fatal("returning to the default directory needs a restart")
	}
	nextLaunch(defaultDir)

	// An environment override owns startup; the save leaves the locator alone.
	t.Setenv(config.EnvDataDir, second)
	before, err := os.ReadFile(defaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if view := save(launch(second, filepath.Join(second, "inkuna.yaml")), first); view.RestartRequired {
		t.Fatal("an environment override must not report a relocation")
	}
	if after, err := os.ReadFile(defaultPath); err != nil || string(after) != string(before) {
		t.Fatalf("the locator changed under an environment override: %v", err)
	}
	t.Setenv(config.EnvDataDir, "")

	// A service that was handed its directory never touches startup files.
	handed := launch(second, filepath.Join(second, "inkuna.yaml"))
	handed.discovered = false
	if view := save(handed, first); view.RestartRequired {
		t.Fatal("a service handed its data directory must not relocate")
	}
	if after, err := os.ReadFile(defaultPath); err != nil || string(after) != string(before) {
		t.Fatalf("the locator changed for a handed data directory: %v", err)
	}
}

type lockedStore struct{ secrets.Store }

func (lockedStore) Delete(string) error { return errors.New("keychain is locked") }

func TestRemoveSecretDeclarationKeepsTheDeclarationWhenTheValueCannotBeDeleted(t *testing.T) {
	t.Setenv(config.EnvDataDir, t.TempDir())
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	file := config.DefaultFile()
	file.Secrets = append(file.Secrets, config.SecretRef{Name: "NPM_TOKEN"})
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveConfiguration(string(encoded)); err != nil {
		t.Fatal(err)
	}
	service.secretStore = lockedStore{service.secretStore}
	if _, err := service.RemoveSecretDeclaration("NPM_TOKEN"); err == nil || !strings.Contains(err.Error(), "keychain is locked") {
		t.Fatalf("a failed delete must fail the removal: %v", err)
	}
	declared := false
	for _, secret := range service.config.File.Secrets {
		declared = declared || secret.Name == "NPM_TOKEN"
	}
	if !declared {
		t.Fatal("the declaration must stay while the value could not be deleted")
	}
}
