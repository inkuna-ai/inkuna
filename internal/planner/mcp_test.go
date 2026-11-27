package planner

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
)

// serverRules is a flow whose one agent stage, staffed by agentName, names
// the given servers.
func serverRules(t *testing.T, agentName, servers string) rules.Document {
	t.Helper()
	stage := "    - id: research\n      kind: agent\n      agent: " + agentName + "\n      permission: read-only\n"
	if servers != "" {
		stage += "      mcp: [" + servers + "]\n"
	}
	document, err := rules.Parse("version: 1\nrules: []\ndefault:\n  stages:\n" + stage + "    - id: approval\n      kind: approval\n      dependsOn: [research]\n")
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func serverAgent(servers ...string) agent.Registry {
	return agent.NewRegistry(agent.BuiltIn(), []agent.Definition{{
		Name: "archivist", Description: "Reads the archive", Permission: domain.PermissionReadOnly, MCP: servers, Instructions: "Read it.", Source: agent.SourceRepository,
	}})
}

func TestAStepIsGrantedItsStagesAndItsAgentsServers(t *testing.T) {
	declared := map[string]bool{"docs-search": true, "linear": true}
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskResearch), Rules: serverRules(t, "archivist", "linear"), Registry: serverAgent("docs-search", "linear"),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, MCPServers: declared,
	})
	if err != nil {
		t.Fatal(err)
	}
	research := result.Workflow.Steps[0]
	if !slices.Equal(research.MCPServers, []string{"docs-search", "linear"}) || domain.StageServers(research)[0] != "linear" {
		t.Fatalf("the grant must be the stage's and the agent's servers, once each: %v (stage %v)", research.MCPServers, domain.StageServers(research))
	}
	if len(result.Notes) != 0 {
		t.Fatalf("nothing was left out: %v", result.Notes)
	}
}

func TestAStageNamingAnUndeclaredServerIsNotPlanned(t *testing.T) {
	_, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskResearch), Rules: serverRules(t, "researcher", "linear"), Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, MCPServers: map[string]bool{"docs-search": true},
	})
	if err == nil || !strings.Contains(err.Error(), `stage "research" names MCP server "linear", which inkuna.yaml does not declare`) {
		t.Fatalf("a stage's undeclared server must stop planning: %v", err)
	}
}

func TestAnAgentsUndeclaredServerIsLeftOutWithANote(t *testing.T) {
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskResearch), Rules: serverRules(t, "archivist", ""), Registry: serverAgent("docs-search", "linear"),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, MCPServers: map[string]bool{"docs-search": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Workflow.Steps[0].MCPServers; !slices.Equal(got, []string{"docs-search"}) {
		t.Fatalf("only the declared server may be granted: %v", got)
	}
	if len(result.Notes) != 1 || !strings.Contains(result.Notes[0], `Agent archivist names MCP server "linear", which inkuna.yaml does not declare`) {
		t.Fatalf("the plan must say what was left out: %v", result.Notes)
	}
}

func TestATemplateRuntimeTakesNoServers(t *testing.T) {
	build := func(document rules.Document) (Result, error) {
		return Build(context.Background(), Input{
			Task: testTask(domain.TaskResearch), Rules: document, Registry: serverAgent("docs-search"), DefaultRuntime: "gemini", Disabled: true,
			Runtimes: map[domain.Runtime]bool{"gemini": false}, MCPRuntimes: map[domain.Runtime]bool{"gemini": false}, MCPServers: map[string]bool{"docs-search": true},
		})
	}
	// The agent's own server is left off, with a note.
	result, err := build(serverRules(t, "archivist", ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Workflow.Steps[0].MCPServers) != 0 || len(result.Notes) != 1 || !strings.Contains(result.Notes[0], "cannot be given MCP servers") {
		t.Fatalf("an agent's server must be left off a template runtime with a note: %v, %v", result.Workflow.Steps[0].MCPServers, result.Notes)
	}
	// The stage's is refused.
	if _, err := build(serverRules(t, "archivist", "docs-search")); err == nil || !strings.Contains(err.Error(), "an argument template, which cannot be given them") {
		t.Fatalf("a stage's server on a template runtime must stop planning: %v", err)
	}
}

func TestARemoteAdvisorIsGivenNoServers(t *testing.T) {
	_, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskResearch), Rules: serverRules(t, "security-advisor", "docs-search"), Registry: remoteRegistry(false),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, MCPServers: map[string]bool{"docs-search": true},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be given stage \"research\"'s MCP servers") {
		t.Fatalf("a remote advisor must not be given a stage's servers: %v", err)
	}
	// Its own definition can name none: the registry drops them.
	registry, _ := agent.NewRegistry(agent.BuiltIn()).WithRemote([]agent.Definition{{Name: "remote-reader", Description: "Reads", MCP: []string{"docs-search"}}})
	if definition, _ := registry.Get("remote-reader"); len(definition.MCP) != 0 {
		t.Fatalf("a remote agent keeps no MCP servers: %v", definition.MCP)
	}
}

func TestRestaffSwapsTheAgentsServersAndKeepsTheStages(t *testing.T) {
	declared := map[string]bool{"docs-search": true, "linear": true, "figma": true}
	registry := agent.NewRegistry(agent.BuiltIn(), []agent.Definition{
		{Name: "archivist", Description: "Reads the archive", Permission: domain.PermissionReadOnly, MCP: []string{"docs-search"}, Instructions: "Read it.", Source: agent.SourceRepository},
		{Name: "designer", Description: "Reads designs", Permission: domain.PermissionReadOnly, MCP: []string{"figma", "undeclared"}, Instructions: "Look.", Source: agent.SourceRepository},
	})
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskResearch), Rules: serverRules(t, "archivist", "linear"), Registry: registry,
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, MCPServers: declared,
	})
	if err != nil {
		t.Fatal(err)
	}
	step := result.Workflow.Steps[0]
	if err := Restaff(&step, Edit{Agent: "designer", Runtime: domain.RuntimeClaude, Score: step.Complexity.Score}, registry, nil, declared, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(step.MCPServers, []string{"figma", "linear"}) {
		t.Fatalf("the new agent's declared servers replace the old agent's, and the stage's stay: %v", step.MCPServers)
	}
	// A server no longer declared at approval is refused at the check.
	if err := CheckServers(domain.WorkflowSpec{Steps: []domain.WorkflowStep{step}}, nil, map[string]bool{"figma": true}); err == nil || !strings.Contains(err.Error(), `"linear", which inkuna.yaml no longer declares`) {
		t.Fatalf("an undeclared server must be refused at approval: %v", err)
	}
}

func TestCheckServersRefusesServersWhereNoneCanGo(t *testing.T) {
	declared := map[string]bool{"docs-search": true}
	for name, step := range map[string]domain.WorkflowStep{
		"remote":   {ID: "advice", Kind: domain.StepAgent, Runtime: domain.RuntimeRemote, MCPServers: []string{"docs-search"}},
		"template": {ID: "work", Kind: domain.StepAgent, Runtime: "gemini", MCPServers: []string{"docs-search"}},
		"approval": {ID: "approval", Kind: domain.StepApproval, MCPServers: []string{"docs-search"}},
	} {
		if err := CheckServers(domain.WorkflowSpec{Steps: []domain.WorkflowStep{step}}, map[domain.Runtime]bool{"gemini": false}, declared); err == nil {
			t.Fatalf("%s: a step that cannot be given servers must be refused", name)
		}
	}
	ok := domain.WorkflowStep{ID: "work", Kind: domain.StepAgent, Runtime: domain.RuntimeCopilot, MCPServers: []string{"docs-search"}}
	if err := CheckServers(domain.WorkflowSpec{Steps: []domain.WorkflowStep{ok}}, nil, declared); err != nil {
		t.Fatalf("a built-in runtime takes declared servers: %v", err)
	}
}
