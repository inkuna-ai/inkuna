package config

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a/remote"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/pricing"
	"github.com/inkuna-ai/inkuna/internal/routing"
	"gopkg.in/yaml.v3"
)

func TestFileRoundTripAndPrecedence(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(EnvDataDir, dataDir)
	t.Setenv(EnvConfigFile, "")
	file := DefaultFile()
	file.Orchestrator.Runtime = domain.RuntimeCodex
	file.Orchestrator.Model = "gpt-5.6-sol"
	file.Concurrency.MaxTasks = 5
	file.Timeouts.Worker = "12m"
	file.Secrets = append(file.Secrets, SecretRef{Name: "ANTHROPIC_API_KEY", Required: true})
	file.Runtimes[domain.RuntimeClaude] = RuntimeConfig{Binary: "/opt/claude", Secrets: []string{"ANTHROPIC_API_KEY"}}
	file.Repositories = []RepositoryConfig{{Name: "inkuna", Path: "/repo/inkuna", DefaultRuntime: domain.RuntimeClaude}}
	path := DefaultPath(dataDir)
	if err := WriteFile(path, file); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("expected owner-only permissions, got %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "lin_api") {
		t.Fatal("config must never contain secret values")
	}

	t.Setenv(EnvWorkerTimeout, "45m")
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !configuration.ConfigExists || configuration.ConfigPath != path {
		t.Fatalf("config file was not discovered: %#v", configuration)
	}
	if configuration.Orchestrator.Runtime != domain.RuntimeCodex || configuration.Orchestrator.Model != "gpt-5.6-sol" {
		t.Fatalf("orchestrator settings were not loaded: %#v", configuration.Orchestrator)
	}
	if configuration.MaxTasks != 5 || configuration.Binaries.Claude != "/opt/claude" {
		t.Fatalf("file values were not loaded: %#v", configuration)
	}
	if configuration.WorkerTimeout != 45*time.Minute {
		t.Fatalf("environment must override the file, got %v", configuration.WorkerTimeout)
	}
	if got := configuration.RuntimeSecrets[domain.RuntimeClaude]; len(got) != 1 || got[0] != "ANTHROPIC_API_KEY" {
		t.Fatalf("runtime secrets were not loaded: %v", got)
	}
	if len(configuration.SecretNames()) != 2 {
		t.Fatalf("expected two declared secrets, got %v", configuration.SecretNames())
	}
	if configuration.AgentDirectories[0] != filepath.Join(dataDir, "agents") {
		t.Fatalf("default agent directory missing: %v", configuration.AgentDirectories)
	}
	if !slices.Equal(configuration.StageDirectories, []string{filepath.Join(dataDir, "stages")}) {
		t.Fatalf("default stage directory missing: %v", configuration.StageDirectories)
	}
}

// stages.directories is used as written, and INKUNA_STAGE_DIRECTORIES
// replaces it, separated like PATH, and is listed as an override.
func TestStageDirectoriesComeFromTheFileOrTheEnvironment(t *testing.T) {
	dataDir := t.TempDir()
	file := DefaultFile()
	file.Stages.Directories = []string{"/stages/team", "/stages/mine"}
	t.Setenv(EnvStageDirectories, "")
	configuration, err := FromFile(dataDir, "", false, file)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(configuration.StageDirectories, file.Stages.Directories) {
		t.Fatalf("stage directories = %v", configuration.StageDirectories)
	}
	t.Setenv(EnvStageDirectories, "/env/one"+string(filepath.ListSeparator)+" "+string(filepath.ListSeparator)+"/env/two")
	if configuration, err = FromFile(dataDir, "", false, file); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(configuration.StageDirectories, []string{"/env/one", "/env/two"}) {
		t.Fatalf("environment stage directories = %v", configuration.StageDirectories)
	}
	if !slices.ContainsFunc(Overrides(), func(override Override) bool {
		return override.Field == "stages.directories" && override.Variable == EnvStageDirectories
	}) {
		t.Fatalf("overrides = %+v", Overrides())
	}
	file.Stages.Directories = []string{"/stages/team", "  "}
	problems := file.Problems()
	if len(problems) != 1 || problems[0].Field != "stages.directories" {
		t.Fatalf("a blank stage directory gave %+v", problems)
	}
}

func TestLocatorFileRedirectsToCustomDataDirectory(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "inkuna-data")
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
	t.Setenv(EnvDataDir, "")
	t.Setenv(EnvConfigFile, "")
	defaultDir, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user config directory in this environment")
	}
	defaultDir = filepath.Join(defaultDir, "Inkuna")
	locator := DefaultFile()
	locator.DataDir = custom
	if err := WriteFile(DefaultPath(defaultDir), locator); err != nil {
		t.Fatal(err)
	}
	real := DefaultFile()
	real.Concurrency.MaxTasks = 7
	if err := WriteFile(filepath.Join(custom, "inkuna.yaml"), real); err != nil {
		t.Fatal(err)
	}
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.DataDir != custom || configuration.MaxTasks != 7 || configuration.ConfigPath != filepath.Join(custom, "inkuna.yaml") {
		t.Fatalf("locator was not followed: %#v", configuration)
	}
}

func TestMissingFileYieldsDefaults(t *testing.T) {
	file, exists, err := ReadFile(filepath.Join(t.TempDir(), "inkuna.yaml"))
	if err != nil || exists {
		t.Fatalf("expected defaults without error, got exists=%v err=%v", exists, err)
	}
	if file.Orchestrator.Runtime != domain.RuntimeClaude || !file.Notifications.Desktop {
		t.Fatalf("unexpected defaults %#v", file)
	}
}

func TestFileValidation(t *testing.T) {
	cases := map[string]func(*File){
		"bad version":       func(f *File) { f.Version = 2 },
		"bad orchestrator":  func(f *File) { f.Orchestrator.Runtime = "gemini" },
		"lower-case secret": func(f *File) { f.Secrets = append(f.Secrets, SecretRef{Name: "token"}) },
		"undeclared runtime key": func(f *File) {
			f.Runtimes[domain.RuntimeCodex] = RuntimeConfig{Secrets: []string{"OPENAI_API_KEY"}}
		},
		"undeclared repository runtime": func(f *File) {
			f.Repositories = []RepositoryConfig{{Name: "a", Path: "/a", DefaultRuntime: "gemini"}}
		},
		"template on a built-in": func(f *File) {
			f.Runtimes[domain.RuntimeClaude] = RuntimeConfig{Args: []string{"{prompt}"}}
		},
		"bypass flag": func(f *File) {
			f.Runtimes["gemini"] = RuntimeConfig{Binary: "gemini", Stdin: "prompt", Output: "text", Args: []string{"--yolo"}, ReadOnlyArgs: []string{"--plan"}}
		},
		"runtime without a prompt": func(f *File) {
			f.Runtimes["gemini"] = RuntimeConfig{Binary: "gemini", Stdin: "none", Output: "text", ReadOnlyArgs: []string{"--plan"}}
		},
		"runtime without read-only arguments": func(f *File) {
			f.Runtimes["gemini"] = RuntimeConfig{Binary: "gemini", Stdin: "prompt", Output: "text"}
		},
		"user config on a template": func(f *File) {
			f.Runtimes["gemini"] = RuntimeConfig{Binary: "gemini", Stdin: "prompt", Output: "text", ReadOnlyArgs: []string{"--plan"}, UserConfig: true}
		},
		"user MCP servers on a template": func(f *File) {
			f.Runtimes["gemini"] = RuntimeConfig{Binary: "gemini", Stdin: "prompt", Output: "text", ReadOnlyArgs: []string{"--plan"}, UserMCP: []string{"notes"}}
		},
		"a user MCP server named twice": func(f *File) {
			f.Runtimes[domain.RuntimeClaude] = RuntimeConfig{UserMCP: []string{"notes", "notes"}}
		},
		"a user MCP server named like a declared one": func(f *File) {
			f.MCPServers = []MCPServerConfig{{Name: "notes", Command: []string{"notes-mcp"}}}
			f.Runtimes[domain.RuntimeCodex] = RuntimeConfig{UserMCP: []string{"notes"}}
		},
		"duplicate repository": func(f *File) {
			f.Repositories = []RepositoryConfig{{Name: "a", Path: "/a"}, {Name: "a", Path: "/b"}}
		},
		"bad timeout": func(f *File) { f.Timeouts.Health = "soon" },
	}
	for name, mutate := range cases {
		file := DefaultFile()
		mutate(&file)
		if err := file.Validate(); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func TestBudgetsMustBeZeroOrRoomyEnoughToCut(t *testing.T) {
	file := DefaultFile()
	file.Budgets = domain.Budgets{Objective: 100, Envelope: -1, Skill: domain.MinBudget}
	problems := file.Problems()
	fields := make([]string, 0, len(problems))
	for _, problem := range problems {
		fields = append(fields, problem.Field)
	}
	if !reflect.DeepEqual(fields, []string{"budgets.objective", "budgets.envelope"}) || !strings.Contains(problems[0].Message, "at least 1024 bytes") {
		t.Fatalf("problems = %+v", problems)
	}
	path := filepath.Join(t.TempDir(), "inkuna.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nbudgets:\n  objective: 32768\n  skills: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), "skills") {
		t.Fatalf("an unknown budget must be rejected, got %v", err)
	}
}

func TestACheckLogCapHoldsSomeLinesAndFitsInMemory(t *testing.T) {
	for _, bytes := range []int{1, MinCheckLogBytes - 1, MaxCheckLogBytes + 1} {
		file := DefaultFile()
		file.Checks.LogBytes = bytes
		if problems := file.Problems(); len(problems) != 1 || problems[0].Field != "checks.logBytes" {
			t.Fatalf("logBytes %d: problems %+v", bytes, problems)
		}
	}
	file := DefaultFile()
	if configuration, err := FromFile(t.TempDir(), "", false, file); err != nil || configuration.CheckLogBytes != DefaultCheckLogBytes {
		t.Fatalf("default cap %d, %v", configuration.CheckLogBytes, err)
	}
	file.Checks.LogBytes = 2 << 20
	t.Setenv(EnvCheckLogBytes, "65536")
	if configuration, err := FromFile(t.TempDir(), "", false, file); err != nil || configuration.CheckLogBytes != 65536 {
		t.Fatalf("the environment wins over the file: %d, %v", configuration.CheckLogBytes, err)
	}
	t.Setenv(EnvCheckLogBytes, "10")
	if _, err := FromFile(t.TempDir(), "", false, file); err == nil {
		t.Fatal("a cap below the minimum from the environment must be refused")
	}
}

func TestMultipleDocumentsAndEmptyFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n---\nversion: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(path); err == nil {
		t.Fatal("a second YAML document must be rejected")
	}
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	file, exists, err := ReadFile(path)
	if err != nil || !exists || file.Orchestrator.Runtime != domain.RuntimeClaude {
		t.Fatalf("an empty file must yield defaults: exists=%v err=%v", exists, err)
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.yaml")
	if err := os.WriteFile(path, []byte("version: 1\napiKeys:\n  linear: abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(path); err == nil {
		t.Fatal("unknown field apiKeys must be rejected so secrets never land in YAML")
	}
}

// An extension's section is taken as written, whatever it holds, while every
// other key stays strict; comments in it survive a save.
func TestExtensionSectionsAreKeptAsWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.yaml")
	content := "version: 1\nextensions:\n  example:\n    endpoint: https://records.example.com # the collector\n    anything: [goes, here]\n  absent-from-this-build:\n    token: RECORDS_TOKEN\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	file, _, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if names := slices.Sorted(maps.Keys(file.Extensions)); !slices.Equal(names, []string{"absent-from-this-build", "example"}) {
		t.Fatalf("sections %v", names)
	}
	if err := WriteFile(path, file); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"extensions:\n", "endpoint: https://records.example.com # the collector\n", "absent-from-this-build:\n        token: RECORDS_TOKEN\n"} {
		if !strings.Contains(string(written), want) {
			t.Fatalf("saved file lost %q:\n%s", want, written)
		}
	}
	if err := os.WriteFile(path, []byte("version: 1\nextension:\n  example: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), "field extension not found") {
		t.Fatalf("a misspelt extensions key must still be refused: %v", err)
	}
}

// Aliases are expanded when the file is read, so a save never writes a
// reference to an anchor that is gone, from a key decoded into Go values, or
// that comes after it once the sections are sorted.
func TestExtensionSectionAliasesSurviveASave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.yaml")
	content := "version: 1\nsecrets:\n  - &token {name: RECORDS_TOKEN}\nextensions:\n  zulu: &shared {endpoint: https://records.example.com}\n  alpha: *shared\n  token: *token\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	file, _, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, file); err != nil {
		t.Fatal(err)
	}
	saved, _, err := ReadFile(path)
	if err != nil {
		data, _ := os.ReadFile(path)
		t.Fatalf("a saved file must read back: %v\n%s", err, data)
	}
	for name, want := range map[string]map[string]string{
		"alpha": {"endpoint": "https://records.example.com"},
		"zulu":  {"endpoint": "https://records.example.com"},
		"token": {"name": "RECORDS_TOKEN"},
	} {
		section := saved.Extensions[name]
		var got map[string]string
		if err := section.Decode(&got); err != nil || !maps.Equal(got, want) {
			t.Fatalf("section %s = %v, %v; want %v", name, got, err, want)
		}
	}
	if err := os.WriteFile(path, []byte("version: 1\nextensions:\n  example: &loop [*loop]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), "extensions.example: line 3: alias *loop refers to a node that contains it") {
		t.Fatalf("a section that contains itself must be refused: %v", err)
	}
}

func extensionSection(t *testing.T, content string) yaml.Node {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		t.Fatal(err)
	}
	return *document.Content[0]
}

func encodeNode(t *testing.T, node yaml.Node) string {
	t.Helper()
	data, err := yaml.Marshal(&node)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEveryFileFieldRoundTrips(t *testing.T) {
	file := File{
		Version: FileVersion, DataDir: "/data/inkuna",
		Orchestrator: OrchestratorConfig{Runtime: domain.RuntimeCodex, Model: "gpt-5.6-sol", ReasoningEffort: "high", MaxTurns: 4, Disabled: true},
		Runtimes: RuntimesConfig{
			domain.RuntimeCodex:   {Binary: "/opt/codex", Secrets: []string{"NPM_TOKEN"}, UserConfig: true},
			domain.RuntimeClaude:  {Binary: "/opt/claude", UserMCP: []string{"linear-server", "claude.ai Gmail"}},
			domain.RuntimeCopilot: {Binary: "/opt/copilot", Secrets: []string{"NPM_TOKEN"}},
			"claude-work":         {Label: "Claude (work)", Preset: domain.RuntimeClaude, Binary: "/opt/claude-work"},
			"gemini": {
				Label: "Gemini CLI", Binary: "gemini", Stdin: "none", Output: "text", Secrets: []string{"NPM_TOKEN"},
				Args: []string{"--prompt", "{prompt}", "--model", "{model}"}, ReadOnlyArgs: []string{"--approval-mode", "plan"},
				WriteArgs: []string{"--approval-mode", "auto-edit"}, VersionArgs: []string{"--version"},
			},
		},
		Concurrency:   ConcurrencyConfig{MaxTasks: 5, MaxWorkers: 4},
		Timeouts:      TimeoutsConfig{Planning: "1m", Worker: "2m", Verifier: "3m", Approval: "4m", Health: "5s", Auth: "6m", Linear: "7s"},
		Notifications: NotificationsConfig{Desktop: true},
		Secrets:       []SecretRef{{Name: "NPM_TOKEN", Description: "Registry token", Required: true}},
		Repositories:  []RepositoryConfig{{Name: "inkuna", Path: "/src/inkuna", DefaultRuntime: domain.RuntimeClaude, Rules: "rules/house.yaml", Docs: "handbook"}},
		Agents:        AgentsConfig{Directories: []string{"/agents/base", "/agents/team"}},
		Stages:        StagesConfig{Directories: []string{"/stages/base", "/stages/team"}},
		Linear:        LinearConfig{Endpoint: "https://linear.example/graphql"},
		GitHub:        GitHubConfig{Host: "github.example", Protocol: "ssh", Binary: "/opt/gh"},
		Checks:        ChecksConfig{LogBytes: 2 << 20},
		Routing: RoutingConfig{Bands: map[domain.Runtime][]routing.Band{domain.RuntimeClaude: {
			{Minimum: 1, Maximum: 6, Model: "sonnet", ReasoningEffort: "low"},
			{Minimum: 7, Maximum: 10, Model: "opus"},
		}}},
		RemoteAgents: []RemoteAgentConfig{{
			Name: "security-advisor", CardURL: "https://advisor.example/.well-known/agent-card.json", Enabled: true, Secret: "NPM_TOKEN", Auto: true,
			SendArtifacts: true, MaxArtifactBytes: 4096, Description: "Reviews plans", Skills: []string{"Threat modelling"},
			Pinned: &remote.Pin{Digest: "sha256:abc", URL: "https://advisor.example/a2a", ProtocolVersion: "0.3.0", Streaming: true, Auth: "bearer", FetchedAt: "2026-09-19T12:00:00Z"},
		}},
		MCPServers: []MCPServerConfig{
			{Name: "docs-search", Description: "Searches the design archive", Command: []string{"npx", "-y", "@example/docs-mcp@2.1.0"}, Env: map[string]string{"DOCS_TOKEN": "NPM_TOKEN"}, Tools: []string{"search"}},
			{Name: "linear", URL: "https://mcp.linear.app/mcp", Bearer: "NPM_TOKEN", Tracker: "linear", Tools: []string{"get_issue"}},
		},
		Budgets: domain.Budgets{Objective: 32 << 10, AcceptanceCriteria: 3 << 10, DependencySummary: 2 << 10, Decisions: 4 << 10, Risks: 4 << 10, Skill: 20 << 10, HumanResponses: 6 << 10, PeerMessages: 8 << 10, Envelope: 120 << 10},
		Pricing: pricing.Overrides{
			Models:         map[string]pricing.Rates{"house-model": {Input: amount(0.5), CacheWrite: amount(0.625), LongCacheWrite: amount(1), CacheRead: amount(0.05), Output: amount(2)}},
			PremiumRequest: amount(0.04),
		},
		Extensions: map[string]yaml.Node{"example": extensionSection(t, "endpoint: https://records.example.com\nretries: 3\n")},
	}
	// A new File field must be added above, or this test would pass without
	// covering it.
	value := reflect.ValueOf(file)
	for index := 0; index < value.NumField(); index++ {
		if value.Field(index).IsZero() {
			t.Fatalf("set %s so the round trip covers it", value.Type().Field(index).Name)
		}
	}
	path := filepath.Join(t.TempDir(), "inkuna.yaml")
	if err := WriteFile(path, file); err != nil {
		t.Fatal(err)
	}
	got, exists, err := ReadFile(path)
	if err != nil || !exists {
		t.Fatalf("read the file back: exists %v, %v", exists, err)
	}
	// A section's nodes come back at their lines in the file, so compare
	// what they encode to.
	if gotSection, wantSection := encodeNode(t, got.Extensions["example"]), encodeNode(t, file.Extensions["example"]); gotSection != wantSection || len(got.Extensions) != 1 {
		t.Fatalf("round trip changed the extension sections: got %v, want %q", got.Extensions, wantSection)
	}
	sections := got.Extensions
	got.Extensions, file.Extensions = nil, nil
	if !reflect.DeepEqual(got, file) {
		t.Fatalf("round trip changed the file:\n got %#v\nwant %#v", got, file)
	}
	got.Extensions = sections
	configuration, err := FromFile("/data/inkuna", path, true, got)
	if err != nil {
		t.Fatal(err)
	}
	if bands := configuration.RoutingPolicy.Bands; bands[domain.RuntimeClaude][1].Model != "opus" || len(bands[domain.RuntimeCodex]) != 10 {
		t.Fatalf("effective routing policy = %#v", bands)
	}
	if price, ok := configuration.Pricing.Price("house-model"); !ok || price.Output != 2 || price.Source != pricing.SourceConfig || *configuration.Pricing.PremiumRequest != 0.04 {
		t.Fatalf("effective pricing = %+v", configuration.Pricing)
	}
}

func amount(value float64) *float64 { return &value }

func TestPricingIsCheckedAndOverridesTheBuiltInTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.yaml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("version: 1\npricing:\n"+body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("  models:\n    gpt-5.6-sol:\n      input: 3\n      cacheRead: 0.3\n      output: 15\n  premiumRequest: 0.04\n")
	file, _, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := FromFile(t.TempDir(), path, true, file)
	if err != nil {
		t.Fatal(err)
	}
	// The configured price wins over the built-in one; other models keep theirs.
	if price, _ := configuration.Pricing.Price("gpt-5.6-sol"); price.Input != 3 || price.CacheWrite != 3 || price.Output != 15 || price.Source != pricing.SourceConfig {
		t.Fatalf("configured price = %+v", price)
	}
	if price, _ := configuration.Pricing.Price("claude-opus-5-5"); price.Source == pricing.SourceConfig || configuration.Pricing.Version != pricing.Default().Version {
		t.Fatalf("built-in price = %+v, version %d", price, configuration.Pricing.Version)
	}
	// Rates decode strictly: an unknown key is an error, not ignored.
	write("  models:\n    gpt-5.6-sol:\n      input: 3\n      outputs: 15\n")
	if _, _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), "outputs") {
		t.Fatalf("an unknown rate must be rejected, got %v", err)
	}
	write("  models:\n    gpt-5.6-sol:\n      input: 3\n")
	if _, _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), "needs input and output") {
		t.Fatalf("a price without output must be rejected, got %v", err)
	}
	file = DefaultFile()
	file.Pricing = pricing.Overrides{Models: map[string]pricing.Rates{"gpt-5.6-sol": {Input: amount(-1), Output: amount(1)}}, PremiumRequest: amount(-0.04)}
	fields := make([]string, 0)
	for _, problem := range file.Problems() {
		fields = append(fields, problem.Field)
	}
	if !reflect.DeepEqual(fields, []string{"pricing.models.gpt-5.6-sol", "pricing.premiumRequest"}) {
		t.Fatalf("pricing problems = %v", fields)
	}
}

func TestProblemsNameTheFieldForEachError(t *testing.T) {
	file := DefaultFile()
	file.Orchestrator.Runtime = "gemini"
	file.Concurrency.MaxWorkers = -1
	file.Timeouts.Worker = "soon"
	file.Timeouts.Linear = "-1s"
	file.GitHub.Protocol = "ftp"
	file.Runtimes[domain.RuntimeCopilot] = RuntimeConfig{Secrets: []string{"UNDECLARED"}}
	file.Runtimes["qwen"] = RuntimeConfig{Binary: "qwen", Stdin: "prompt", Output: "xml"}
	file.Repositories = []RepositoryConfig{{Name: "inkuna"}, {Name: "wiki", Path: "/src/wiki", Docs: "../shared-docs"}}
	file.Routing.Bands = map[domain.Runtime][]routing.Band{
		domain.RuntimeClaude: {{Minimum: 1, Maximum: 4, Model: "sonnet"}},
		"gemini":             {{Minimum: 1, Maximum: 10, Model: "pro"}},
		"bard":               {{Minimum: 1, Maximum: 10, Model: "pro"}},
	}
	fields := make([]string, 0)
	for _, problem := range file.Problems() {
		if problem.Message == "" {
			t.Fatalf("problem without a message: %#v", problem)
		}
		fields = append(fields, problem.Field)
	}
	want := []string{"orchestrator.runtime", "concurrency.maxWorkers", "runtimes.copilot.secrets", "runtimes.qwen", "repositories", "repositories", "github.protocol", "timeouts.worker", "timeouts.linear", "routing.bands.claude", "routing.bands", "routing.bands"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %v, want %v", fields, want)
	}
	// Unknown runtimes come from a map; they are reported in name order.
	for range 20 {
		problems := file.Problems()
		if last := len(problems) - 1; !strings.Contains(problems[last-1].Message, `"bard"`) || !strings.Contains(problems[last].Message, `"gemini"`) {
			t.Fatalf("unknown runtimes out of order: %#v", problems[last-1:])
		}
	}
	if err := file.Validate(); err == nil || err.Error() != file.Problems()[0].Message {
		t.Fatalf("Validate must return the first problem: %v", err)
	}
	if problems := DefaultFile().Problems(); len(problems) != 0 {
		t.Fatalf("the default file has problems: %#v", problems)
	}
}

func TestAnExplicitConfigPathOverridesTheFileDataDir(t *testing.T) {
	t.Setenv(EnvDataDir, "")
	t.Setenv(EnvConfigFile, "/etc/inkuna.yaml")
	if got := Overrides(); len(got) != 1 || got[0] != (Override{Field: "dataDir", Variable: EnvConfigFile, Value: "/etc/inkuna.yaml"}) {
		t.Fatalf("overrides = %#v", got)
	}
	// INKUNA_DATA_DIR wins over both, so it is the one reported.
	t.Setenv(EnvDataDir, "/data")
	if got := Overrides(); len(got) != 1 || got[0].Variable != EnvDataDir {
		t.Fatalf("overrides = %#v", got)
	}
}

func TestRemoteAgentValidation(t *testing.T) {
	valid := func() File {
		file := DefaultFile()
		file.Secrets = []SecretRef{{Name: "ADVISOR_TOKEN"}}
		file.RemoteAgents = []RemoteAgentConfig{{
			Name: "security-advisor", CardURL: "https://advisor.example/.well-known/agent-card.json", Enabled: true, Secret: "ADVISOR_TOKEN",
			Description: "Reviews plans", Skills: []string{"Threat modelling"},
			Pinned: &remote.Pin{Digest: "sha256:abc", URL: "https://advisor.example/a2a", ProtocolVersion: "0.3.0"},
		}}
		return file
	}
	file := valid()
	if problems := file.Problems(); len(problems) != 0 {
		t.Fatalf("a well-formed remote agent has no problems: %+v", problems)
	}
	definitions := file.RemoteDefinitions()
	if len(definitions) != 1 || !definitions[0].Remote() || definitions[0].Permission != domain.PermissionReadOnly || !strings.Contains(definitions[0].Description, "Threat modelling") {
		t.Fatalf("unexpected remote definitions %+v", definitions)
	}
	cases := map[string]func(*RemoteAgentConfig){
		"reserved name":            func(entry *RemoteAgentConfig) { entry.Name = "human" },
		"name of a built-in agent": func(entry *RemoteAgentConfig) { entry.Name = "reviewer" },
		"must use https":           func(entry *RemoteAgentConfig) { entry.CardURL = "http://advisor.example/card.json" },
		"without credentials":      func(entry *RemoteAgentConfig) { entry.CardURL = "https://user:pass@advisor.example/card.json" },
		"must be declared":         func(entry *RemoteAgentConfig) { entry.Secret = "UNDECLARED" },
		"maxArtifactBytes":         func(entry *RemoteAgentConfig) { entry.MaxArtifactBytes = MaxRemoteArtifactBytes + 1 },
		"reviewed and pinned":      func(entry *RemoteAgentConfig) { entry.Pinned = nil },
		"different origin":         func(entry *RemoteAgentConfig) { entry.Pinned.URL = "https://elsewhere.example/a2a" },
		"not a sha256 digest":      func(entry *RemoteAgentConfig) { entry.Pinned.Digest = "md5:abc" },
	}
	for want, change := range cases {
		file := valid()
		change(&file.RemoteAgents[0])
		problems := file.Problems()
		if len(problems) == 0 || problems[0].Field != "remoteAgents" || !strings.Contains(problems[0].Message, want) {
			t.Fatalf("%s: unexpected problems %+v", want, problems)
		}
	}
	file = valid()
	file.RemoteAgents = append(file.RemoteAgents, file.RemoteAgents[0])
	if problems := file.Problems(); len(problems) == 0 || !strings.Contains(problems[0].Message, "declared twice") {
		t.Fatalf("duplicate names must be reported: %+v", problems)
	}
	// Plain http is only for an agent the user marked by hand as local development.
	file = valid()
	file.RemoteAgents[0].AllowInsecure, file.RemoteAgents[0].CardURL, file.RemoteAgents[0].Pinned.URL = true, "http://localhost:9000/card.json", "http://localhost:9000/a2a"
	if problems := file.Problems(); len(problems) != 0 {
		t.Fatalf("allowInsecure permits http: %+v", problems)
	}
	// A default port written out is the same origin; another port is not.
	file = valid()
	file.RemoteAgents[0].Pinned.URL = "https://ADVISOR.example:443/a2a"
	if problems := file.Problems(); len(problems) != 0 {
		t.Fatalf("an explicit default port is the same origin: %+v", problems)
	}
	file.RemoteAgents[0].Pinned.URL = "https://advisor.example:8443/a2a"
	if problems := file.Problems(); len(problems) == 0 || !strings.Contains(problems[0].Message, "different origin") {
		t.Fatalf("another port is another origin: %+v", problems)
	}
	// Disabled and unpinned agents are invisible to planning.
	file = valid()
	file.RemoteAgents[0].Enabled = false
	if len(file.RemoteDefinitions()) != 0 {
		t.Fatal("a disabled remote agent must not be a planning candidate")
	}
	file = DefaultFile()
	file.Runtimes["remote"] = RuntimeConfig{Binary: "x"}
	if file.Validate() == nil {
		t.Fatal("the remote runtime name is reserved")
	}
}

func TestRepositoryMatchesCleanPathsAndKeepsTheFirstEntry(t *testing.T) {
	file := File{Repositories: []RepositoryConfig{
		{Name: "first", Path: "/src/bakery/", Docs: "handbook"},
		{Name: "second", Path: "/src/bakery", Rules: "flows.yaml"},
	}}
	if entry, ok := file.Repository("/src/./bakery"); !ok || entry.Name != "first" {
		t.Fatalf("Repository = %#v, %v; want the first entry", entry, ok)
	}
	if _, ok := file.Repository("/src/bakery-worktrees/topic"); ok {
		t.Fatal("an undeclared path must not match")
	}
}
