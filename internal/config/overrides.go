package config

import (
	"os"
	"strings"
)

// Override reports an inkuna.yaml field whose value is currently replaced by
// an environment variable. Field is the dotted path the settings view uses.
type Override struct {
	Field    string `json:"field"`
	Variable string `json:"variable"`
	Value    string `json:"value"`
}

// fieldVariables maps each file field to the variable that wins over it.
// Variables without a file field (git, go, node, npm, the config path, and
// secret values) are not listed: there is nothing in the file to override.
var fieldVariables = []struct{ field, variable string }{
	{"dataDir", EnvDataDir},
	{"orchestrator.runtime", EnvOrchestratorRuntime},
	{"orchestrator.model", EnvOrchestratorModel},
	{"runtimes.codex.binary", EnvCodexBinary},
	{"runtimes.claude.binary", EnvClaudeBinary},
	{"runtimes.copilot.binary", EnvCopilotBinary},
	{"concurrency.maxTasks", EnvMaxTasks},
	{"concurrency.maxWorkers", EnvMaxConcurrency},
	{"checks.logBytes", EnvCheckLogBytes},
	{"timeouts.planning", EnvPlanningTimeout},
	{"timeouts.worker", EnvWorkerTimeout},
	{"timeouts.verifier", EnvVerifierTimeout},
	{"timeouts.approval", EnvApprovalTimeout},
	{"timeouts.health", EnvHealthTimeout},
	{"timeouts.auth", EnvAuthenticationTimeout},
	{"timeouts.linear", EnvLinearTimeout},
	{"notifications.desktop", EnvDesktopNotifications},
	{"linear.endpoint", EnvLinearEndpoint},
	{"github.host", EnvGitHubHost},
	{"github.protocol", EnvGitHubGitProtocol},
	{"github.binary", EnvGitHubBinary},
	{"stages.directories", EnvStageDirectories},
}

// Overrides lists the file fields an environment variable is overriding in
// this process. None of these variables carry secrets.
func Overrides() []Override {
	overrides := make([]Override, 0)
	// An explicit configuration path turns the locator off, so the file's
	// dataDir is never followed; INKUNA_DATA_DIR, listed below, wins over both.
	if value := strings.TrimSpace(os.Getenv(EnvConfigFile)); value != "" && strings.TrimSpace(os.Getenv(EnvDataDir)) == "" {
		overrides = append(overrides, Override{Field: "dataDir", Variable: EnvConfigFile, Value: value})
	}
	for _, entry := range fieldVariables {
		if value := strings.TrimSpace(os.Getenv(entry.variable)); value != "" {
			overrides = append(overrides, Override{Field: entry.field, Variable: entry.variable, Value: value})
		}
	}
	return overrides
}
