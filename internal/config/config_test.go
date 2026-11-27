package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadUsesEnvironmentOverrides(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Setenv(EnvDataDir, dataDir)
	t.Setenv(EnvLinearEndpoint, "https://linear.test/graphql")
	t.Setenv(EnvWorkerTimeout, "45m")
	t.Setenv(EnvAuthenticationTimeout, "7m")
	t.Setenv(EnvMaxConcurrency, "4")
	t.Setenv(EnvCodexBinary, "/opt/tools/codex")
	t.Setenv(EnvGitHubHost, "github.example.com")
	t.Setenv(EnvGitHubGitProtocol, "ssh")

	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.DataDir != dataDir || configuration.LinearEndpoint != "https://linear.test/graphql" {
		t.Fatalf("environment paths were not loaded: %#v", configuration)
	}
	if configuration.WorkerTimeout != 45*time.Minute || configuration.AuthTimeout != 7*time.Minute || configuration.MaxConcurrency != 4 {
		t.Fatalf("environment limits were not loaded: %#v", configuration)
	}
	if configuration.Binaries.Codex != "/opt/tools/codex" {
		t.Fatalf("binary override was not loaded: %#v", configuration.Binaries)
	}
	if configuration.GitHubHost != "github.example.com" || configuration.GitHubProtocol != "ssh" {
		t.Fatalf("github settings were not loaded: %#v", configuration)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv(EnvDataDir, t.TempDir())
	t.Setenv(EnvHealthTimeout, "immediately")
	if _, err := Load(); err == nil {
		t.Fatal("invalid duration was accepted")
	}
}

// The default data directory is Inkuna's under the user configuration
// directory, whatever else that directory holds.
func TestDefaultDataDirIsInkunasUnderTheUserConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", home)
	root, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user config directory in this environment")
	}
	dataDir, err := DefaultDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "Inkuna"); dataDir != want {
		t.Fatalf("default data directory %s, want %s", dataDir, want)
	}
}
