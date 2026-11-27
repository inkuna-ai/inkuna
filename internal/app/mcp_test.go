package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// mcpTestService is a service whose inkuna.yaml declares a docs-search
// server that needs the DOCS_TOKEN secret.
func mcpTestService(t *testing.T, runtime *workflowRuntime) *Service {
	t.Helper()
	service := workflowTestService(t, t.TempDir(), runtime)
	service.config.File.Secrets = append(service.config.File.Secrets, config.SecretRef{Name: "DOCS_TOKEN"})
	service.config.File.MCPServers = []config.MCPServerConfig{{
		Name: "docs-search", Description: "Searches the design archive", Command: []string{"docs-mcp", "--stdio"},
		Env: map[string]string{"DOCS_TOKEN": "DOCS_TOKEN"}, Tools: []string{"search"},
	}}
	return service
}

// mcpTestSpec plans one read-only step whose stage names docs-search.
func mcpTestSpec(t *testing.T, service *Service) domain.WorkflowSpec {
	t.Helper()
	task := domain.TaskSpec{
		Version: domain.ContractVersion, ID: "task", Kind: domain.TaskResearch, Objective: "Find the archived design",
		Source: domain.TaskSource{Kind: "request"}, Repository: ".", Priority: 1, Budget: domain.Budget{WallTimeSeconds: 60},
	}
	if err := service.store.SaveTask(context.Background(), "project", "planned", task); err != nil {
		t.Fatal(err)
	}
	return savePlanned(t, service, domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeCodex,
		RoutingPolicyVersion: service.routingPolicy().Version, MaxConcurrency: 1, CreatedAt: time.Now().UTC(), MCPServerDigests: mcpServerDigests(service.config.File),
		Steps: []domain.WorkflowStep{{
			ID: "worker", Title: "Search", Kind: domain.StepAgent, Runtime: domain.RuntimeCodex, Permission: domain.PermissionReadOnly, Agent: "researcher",
			MCPServers: []string{"docs-search"}, Metadata: map[string]string{domain.MetadataStageMCP: "docs-search"},
			Complexity: &domain.ComplexityAssessment{Score: 4, Rationale: "read-only search"},
			Budget:     domain.Budget{WallTimeSeconds: 60, MaxOutputBytes: 1024},
		}},
	})
}

func TestAStepGetsItsGrantedServersWithTheirSecretsOnlyInTheRuntimeRequest(t *testing.T) {
	runtime := &workflowRuntime{}
	service := mcpTestService(t, runtime)
	if err := service.secretStore.Set("DOCS_TOKEN", "docs-secret"); err != nil {
		t.Fatal(err)
	}
	run := startSpec(t, service, mcpTestSpec(t, service))
	waitForWorkflowRun(t, service, run.ID, "completed")
	requests := started(runtime)
	if len(requests) != 1 {
		t.Fatalf("want one call, got %d", len(requests))
	}
	request := requests[0]
	if len(request.MCPServers) != 1 || request.MCPServers[0].Env["DOCS_TOKEN"] != "docs-secret" || request.MCPServers[0].Tools[0] != "search" {
		t.Fatalf("the runtime must be given the server with its secret: %+v", request.MCPServers)
	}
	if want := filepath.Join(service.dataDir, "runs", run.ID, "mcp"); request.MCPDir != want {
		t.Fatalf("the configuration must go in the run's directory: %q, want %q", request.MCPDir, want)
	}
	prompt := string(request.Prompt)
	if !strings.Contains(prompt, "\n# MCP servers\n- `docs-search`: Searches the design archive (tools: `search`)\n") || !strings.Contains(prompt, "MCP servers are tools outside the repository") {
		t.Fatalf("the prompt must say which servers the step has:\n%s", prompt)
	}
	if strings.Contains(prompt, "docs-secret") {
		t.Fatal("a secret reached the prompt")
	}
	stepContext, err := service.RunContext(run.ID, "worker")
	if err != nil {
		t.Fatal(err)
	}
	servers := stepContext.Envelope.MCPServers
	if len(servers) != 1 || servers[0].Name != "docs-search" || servers[0].Tools[0] != "search" {
		t.Fatalf("the run must record which servers the step had: %+v", servers)
	}
}

func TestAStepWhoseServersSecretIsNotSetDoesNotStart(t *testing.T) {
	runtime := &workflowRuntime{}
	service := mcpTestService(t, runtime)
	run := startSpec(t, service, mcpTestSpec(t, service))
	failed := waitForWorkflowRun(t, service, run.ID, "failed")
	if len(started(runtime)) != 0 {
		t.Fatal("a step must not run without its server")
	}
	if !strings.Contains(failed.Steps[0].Summary, "needs secret DOCS_TOKEN") {
		t.Fatalf("the failure must name the missing secret: %q", failed.Steps[0].Summary)
	}
}

func TestAStepWhoseServersSecretIsTooShortToHideDoesNotStart(t *testing.T) {
	runtime := &workflowRuntime{}
	service := mcpTestService(t, runtime)
	// Seven bytes: the runtime would not scrub it from the events it keeps.
	if err := service.secretStore.Set("DOCS_TOKEN", "abc1234"); err != nil {
		t.Fatal(err)
	}
	run := startSpec(t, service, mcpTestSpec(t, service))
	failed := waitForWorkflowRun(t, service, run.ID, "failed")
	if len(started(runtime)) != 0 {
		t.Fatal("a step must not run with a secret it cannot hide")
	}
	if summary := failed.Steps[0].Summary; !strings.Contains(summary, "secret DOCS_TOKEN is shorter than 8 bytes") || strings.Contains(summary, "abc1234") {
		t.Fatalf("the failure must name the secret, never its value: %q", summary)
	}
}

func TestAServerChangedUnderTheSameNameIsRefusedAtApproval(t *testing.T) {
	service := mcpTestService(t, &workflowRuntime{})
	spec := mcpTestSpec(t, service)
	// The plan showed docs-mcp; the run would start another program.
	service.config.File.MCPServers[0].Command = []string{"other-mcp", "--stdio"}
	if _, err := service.reviseApproval(context.Background(), spec); err == nil || !strings.Contains(err.Error(), `MCP server "docs-search" changed`) || !strings.Contains(err.Error(), "plan the task again") {
		t.Fatalf("approval must refuse a server changed since planning: %v", err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "changed in inkuna.yaml") {
		t.Fatalf("starting must refuse it too: %v", err)
	}
}

func TestAServerGivenAnotherSecretIsRefusedAtApprovalButARotatedOneIsNot(t *testing.T) {
	service := mcpTestService(t, &workflowRuntime{})
	if err := service.secretStore.Set("DOCS_TOKEN", "docs-secret-one"); err != nil {
		t.Fatal(err)
	}
	spec := mcpTestSpec(t, service)
	// A new value for the same secret changes nothing the plan recorded.
	if err := service.secretStore.Set("DOCS_TOKEN", "docs-secret-two"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.reviseApproval(context.Background(), spec); err != nil {
		t.Fatalf("a rotated credential must still approve: %v", err)
	}
	// The same variable now carries another credential.
	service.config.File.Secrets = append(service.config.File.Secrets, config.SecretRef{Name: "ADMIN_TOKEN"})
	service.config.File.MCPServers[0].Env = map[string]string{"DOCS_TOKEN": "ADMIN_TOKEN"}
	if _, err := service.reviseApproval(context.Background(), spec); err == nil || !strings.Contains(err.Error(), `MCP server "docs-search" changed`) {
		t.Fatalf("approval must refuse a server given another secret: %v", err)
	}
}

func TestAServerNoLongerDeclaredIsRefusedAtApproval(t *testing.T) {
	service := mcpTestService(t, &workflowRuntime{})
	spec := mcpTestSpec(t, service)
	service.config.File.MCPServers = nil
	if _, err := service.reviseApproval(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "no longer declares") {
		t.Fatalf("approval must refuse a grant inkuna.yaml no longer declares: %v", err)
	}
}

func TestASecretAServerUsesIsNotRemovedFromUnderIt(t *testing.T) {
	service := mcpTestService(t, &workflowRuntime{})
	service.config.ConfigPath = filepath.Join(t.TempDir(), "inkuna.yaml")
	if err := service.secretStore.Set("DOCS_TOKEN", "docs-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RemoveSecretDeclaration("DOCS_TOKEN"); err == nil || !strings.Contains(err.Error(), `MCP server "docs-search" uses secret DOCS_TOKEN`) {
		t.Fatalf("removing a server's secret must be refused: %v", err)
	}
	if value, err := service.secretStore.Get("DOCS_TOKEN"); err != nil || value != "docs-secret" {
		t.Fatalf("the value must stay when the removal is refused: %q, %v", value, err)
	}
}
