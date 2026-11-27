package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/routing"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// RuntimeView is one declared runtime as the Runtimes view and every runtime
// picker see it.
type RuntimeView struct {
	Name  domain.Runtime `json:"name"`
	Label string         `json:"label"`
	// BuiltIn runtimes cannot be removed; removing their entry resets them.
	BuiltIn bool `json:"builtIn"`
	// Customized is set for a built-in whose entry differs from the default
	// one, which is when resetting it would change anything.
	Customized bool `json:"customized,omitempty"`
	// Preset is the built-in adapter that builds this runtime's arguments, or
	// empty for an argument template.
	Preset domain.Runtime `json:"preset,omitempty"`
	// Binary is what will be executed, after defaults and the environment.
	Binary string `json:"binary"`
	// CanWrite is false for a template without write arguments, which may only
	// be given read-only steps.
	CanWrite bool `json:"canWrite"`
	// Config is the entry in inkuna.yaml, empty for an untouched built-in.
	Config config.RuntimeConfig `json:"config"`
	// Override names the environment variable that currently sets the binary.
	Override string `json:"override,omitempty"`
}

// RuntimeTestView is the result of a connection test.
type RuntimeTestView struct {
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Runtimes lists every declared runtime, built-ins first.
func (service *Service) Runtimes() ([]RuntimeView, error) {
	overrides := map[string]string{}
	for _, override := range config.Overrides() {
		overrides[override.Field] = override.Variable
	}
	defaults := config.DefaultFile().Runtimes
	views := make([]RuntimeView, 0, len(service.config.Runtimes))
	for _, spec := range service.config.Runtimes {
		preset, customized := spec.Preset, false
		if agentruntime.IsPreset(spec.Name) {
			preset = spec.Name
			// A save always writes the default entry back, so "has an entry"
			// says nothing; compare with what the default entry holds.
			entry, declared := service.config.File.Runtimes[spec.Name]
			customized = declared && !reflect.DeepEqual(entry, defaults[spec.Name]) && !reflect.DeepEqual(entry, config.RuntimeConfig{})
		}
		views = append(views, RuntimeView{
			Name: spec.Name, Label: runtimeLabel(spec), BuiltIn: agentruntime.IsPreset(spec.Name), Customized: customized, Preset: preset,
			Binary: service.binaryFor(spec.Name), CanWrite: spec.CanWrite(), Config: service.config.File.Runtimes[spec.Name],
			Override: overrides[fmt.Sprintf("runtimes.%s.binary", spec.Name)],
		})
	}
	return views, nil
}

// SaveRuntime validates one declaration and writes it into inkuna.yaml. Runs
// already in flight keep the runtimes they started with.
func (service *Service) SaveRuntime(name, runtimeJSON string) ([]RuntimeView, error) {
	runtimeName := domain.Runtime(strings.TrimSpace(name))
	var entry config.RuntimeConfig
	decoder := json.NewDecoder(bytes.NewReader([]byte(runtimeJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return nil, fmt.Errorf("decode runtime: %w", err)
	}
	entry.Label, entry.Binary = strings.TrimSpace(entry.Label), strings.TrimSpace(entry.Binary)
	if err := entry.Spec(runtimeName).Validate(); err != nil {
		return nil, err
	}
	return service.saveRuntimes(func(file *config.File) {
		file.Runtimes[runtimeName] = entry
	})
}

// DeleteRuntime removes an added runtime together with its routing bands. A
// built-in runtime has nothing to remove but its adjustments, so it goes back
// to its defaults. Configuration that still names the runtime refuses the save.
func (service *Service) DeleteRuntime(name string) ([]RuntimeView, error) {
	runtimeName := domain.Runtime(strings.TrimSpace(name))
	if _, declared := service.config.File.Runtimes[runtimeName]; !declared && !agentruntime.IsPreset(runtimeName) {
		return nil, fmt.Errorf("runtime %q has no entry in inkuna.yaml", runtimeName)
	}
	return service.saveRuntimes(func(file *config.File) {
		delete(file.Runtimes, runtimeName)
		if agentruntime.IsPreset(runtimeName) {
			return
		}
		bands := make(map[domain.Runtime][]routing.Band, len(file.Routing.Bands))
		for bandRuntime, entries := range file.Routing.Bands {
			if bandRuntime != runtimeName {
				bands[bandRuntime] = entries
			}
		}
		file.Routing.Bands = bands
	})
}

// saveRuntimes applies an edit to a copy of the file's runtimes and saves the
// whole file through the usual validation. The live configuration shares the
// maps in its File, so they are never edited in place.
func (service *Service) saveRuntimes(edit func(*config.File)) ([]RuntimeView, error) {
	file := service.config.File
	runtimes := make(config.RuntimesConfig, len(file.Runtimes)+1)
	for name, entry := range file.Runtimes {
		runtimes[name] = entry
	}
	file.Runtimes = runtimes
	edit(&file)
	encoded, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}
	if _, err := service.SaveConfiguration(string(encoded)); err != nil {
		return nil, err
	}
	return service.Runtimes()
}

// TestRuntime resolves a declared runtime's binary and runs its version probe.
// A missing binary is a result, not an error: the view shows what to fix.
func (service *Service) TestRuntime(name string) (RuntimeTestView, error) {
	runtimeName := domain.Runtime(strings.TrimSpace(name))
	if _, err := service.runtimeFor(runtimeName); err != nil {
		return RuntimeTestView{}, err
	}
	binary := service.binaryFor(runtimeName)
	path, err := exec.LookPath(binary)
	if err != nil {
		return RuntimeTestView{Detail: fmt.Sprintf("%s was not found; install it or set an absolute path as the binary", binary)}, nil
	}
	versionArgs := []string{"--version"}
	for _, spec := range service.config.Runtimes {
		if spec.Name == runtimeName && len(spec.VersionArgs) > 0 {
			versionArgs = spec.VersionArgs
		}
	}
	timeout := service.config.HealthTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(service.context(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, versionArgs...).CombinedOutput()
	version := strings.TrimSpace(string(output))
	if len(version) > 400 {
		version = version[:400]
	}
	if err != nil {
		detail := err.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			detail = fmt.Sprintf("no answer within %s", timeout)
		}
		return RuntimeTestView{Path: path, Version: version, Detail: fmt.Sprintf("%s %s failed: %s", binary, strings.Join(versionArgs, " "), detail)}, nil
	}
	return RuntimeTestView{Available: true, Path: path, Version: version}, nil
}
