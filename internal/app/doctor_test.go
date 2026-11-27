package app

import (
	"testing"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/prereq"
)

func TestPrerequisitesBecomeHealthRows(t *testing.T) {
	brew := []prereq.Step{{Command: []string{"brew", "install", "go"}}}
	for _, test := range []struct {
		item prereq.Item
		want HealthCheck
	}{
		{
			prereq.Item{ID: "go", Name: "Go", Status: prereq.Present, Detail: "1.25.5", Required: true, Install: brew},
			HealthCheck{Name: "Go", Status: "ready", Detail: "1.25.5", Required: true},
		},
		{
			prereq.Item{ID: "go", Name: "Go", Status: prereq.Missing, Detail: "go was not found in PATH", Required: true, Install: brew},
			HealthCheck{Name: "Go", Status: "missing", Required: true,
				Detail: "go was not found in PATH; install with brew install go; or set its path with the INKUNA_GO_BINARY environment variable"},
		},
		{
			prereq.Item{ID: "go", Name: "Go", Status: prereq.TooOld, Detail: "1.22.1 is older than 1.25", Required: true, Install: brew},
			HealthCheck{Name: "Go", Status: "error", Required: true, Detail: "1.22.1 is older than 1.25; install with brew install go"},
		},
		{
			prereq.Item{ID: "claude", Name: "Claude Code", Status: prereq.SignedOut, Detail: "not signed in", SignIn: []string{"claude", "auth", "login"}},
			HealthCheck{Name: "Claude Code", Status: "missing", Detail: "not signed in; sign in with claude auth login", Action: HealthConnections},
		},
		// Connections signs in to the GitHub CLI too, but not to Codex,
		// which signs in in a terminal, as its detail says.
		{
			prereq.Item{ID: "gh", Name: "GitHub CLI", Status: prereq.SignedOut, Detail: "not signed in", Required: true, SignIn: []string{"gh", "auth", "login"}},
			HealthCheck{Name: "GitHub CLI", Status: "missing", Detail: "not signed in; sign in with gh auth login", Required: true, Action: HealthConnections},
		},
		{
			prereq.Item{ID: "codex", Name: "Codex CLI", Status: prereq.SignedOut, Detail: "not signed in", SignIn: []string{"codex", "login"}},
			HealthCheck{Name: "Codex CLI", Status: "missing", Detail: "not signed in; sign in with codex login"},
		},
		// Connections cannot install a CLI that is missing.
		{
			prereq.Item{ID: "claude", Name: "Claude Code", Status: prereq.Missing, Detail: "claude was not found in PATH"},
			HealthCheck{Name: "Claude Code", Status: "missing", Detail: "claude was not found in PATH; or set its path with runtimes.claude.binary in inkuna.yaml"},
		},
		{
			prereq.Item{ID: "webview2", Name: "WebView2 runtime", Status: prereq.Broken, Detail: "reg was not found in PATH", Required: true},
			HealthCheck{Name: "WebView2 runtime", Status: "error", Detail: "reg was not found in PATH", Required: true},
		},
	} {
		if got := prerequisiteHealth(test.item); got != test.want {
			t.Errorf("%s %s:\n got %+v\nwant %+v", test.item.ID, test.item.Status, got, test.want)
		}
	}
}

// A check the app's own screens fix names the screen, so the view offers
// it without knowing the check.
func TestDoctorNamesTheScreenThatFixesACheck(t *testing.T) {
	t.Setenv(config.EnvLinearAPIKey, "")
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	service.config.ConfigExists = false
	service.config.File.Secrets = []config.SecretRef{{Name: "DOCS_TOKEN"}}
	for name, want := range map[string]string{"Configuration": HealthSetup, "Linear": HealthConnections, "Secret DOCS_TOKEN": HealthSetup, "Inkuna": ""} {
		if check, ok := healthOf(service, name); !ok || check.Action != want {
			t.Errorf("%s: action %q, want %q (%+v)", name, check.Action, want, check)
		}
	}
	// Once fixed, a check names nothing to do.
	service.config.ConfigExists = true
	if check, _ := healthOf(service, "Configuration"); check.Status != "ready" || check.Action != "" {
		t.Errorf("a ready check has no action: %+v", check)
	}
}
