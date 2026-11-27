package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// userServerRuntime is a workflow runtime opted in to some of the user's own
// MCP servers, which reports on them as scripted and records sign-ins.
type userServerRuntime struct {
	*workflowRuntime
	names    []string
	statuses map[string]agentruntime.UserServer
	signMu   sync.Mutex
	signedIn []string
	signErr  error
}

func (runtime *userServerRuntime) Capabilities() agentruntime.Capabilities {
	capabilities := runtime.workflowRuntime.Capabilities()
	// A test may change names while a run reads them.
	runtime.signMu.Lock()
	defer runtime.signMu.Unlock()
	capabilities.UserMCP, capabilities.MCPDisabled = slices.Clone(runtime.names), false
	return capabilities
}

func (runtime *userServerRuntime) ListUserServers(context.Context, string) ([]agentruntime.UserServer, error) {
	listed := make([]agentruntime.UserServer, 0, len(runtime.statuses))
	for _, server := range runtime.statuses {
		listed = append(listed, server)
	}
	return listed, nil
}

func (runtime *userServerRuntime) ResolveUserServers(_ context.Context, _ string, names []string) ([]agentruntime.UserServer, error) {
	runtime.signMu.Lock()
	defer runtime.signMu.Unlock()
	resolved := make([]agentruntime.UserServer, 0, len(names))
	for _, name := range names {
		server, ok := runtime.statuses[name]
		if !ok {
			server = agentruntime.UserServer{Name: name, Status: agentruntime.UserServerMissing}
		}
		resolved = append(resolved, server)
	}
	return resolved, nil
}

func (runtime *userServerRuntime) SignInUserServer(_ context.Context, _, name string) error {
	runtime.signMu.Lock()
	defer runtime.signMu.Unlock()
	runtime.signedIn = append(runtime.signedIn, name)
	return runtime.signErr
}

// userServerSpec plans one read-only codex step, recording the runtimes'
// userMcp as planning does.
func userServerSpec(t *testing.T, service *Service) domain.WorkflowSpec {
	t.Helper()
	task := domain.TaskSpec{
		Version: domain.ContractVersion, ID: "task", Kind: domain.TaskResearch, Objective: "Inspect the repository",
		Source: domain.TaskSource{Kind: "request"}, Repository: ".", Priority: 1, Budget: domain.Budget{WallTimeSeconds: 60},
	}
	if err := service.store.SaveTask(context.Background(), "project", "planned", task); err != nil {
		t.Fatal(err)
	}
	spec := domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeCodex,
		RoutingPolicyVersion: service.routingPolicy().Version, MaxConcurrency: 1, CreatedAt: time.Now().UTC(), UserMCP: service.userMCPByRuntime(),
		Steps: []domain.WorkflowStep{{
			ID: "worker", Title: "Inspect", Kind: domain.StepAgent, Runtime: domain.RuntimeCodex, Permission: domain.PermissionReadOnly, Agent: "researcher",
			Complexity: &domain.ComplexityAssessment{Score: 4, Rationale: "read-only inspection"},
			Budget:     domain.Budget{WallTimeSeconds: 60, MaxOutputBytes: 1024},
		}},
	}
	applyUserServers(&spec, spec.UserMCP)
	return savePlanned(t, service, spec)
}

func userServerService(t *testing.T, runtime *userServerRuntime) *Service {
	t.Helper()
	service := workflowTestService(t, t.TempDir(), runtime)
	service.runtimes = map[domain.Runtime]agentruntime.AgentRuntime{domain.RuntimeCodex: runtime, domain.RuntimeClaude: runtime.workflowRuntime}
	return service
}

func runEventsSay(view WorkflowRunView, text string) bool {
	return slices.ContainsFunc(view.Events, func(event domain.RunEvent) bool { return strings.Contains(event.Summary, text) })
}

func TestAStepIsGivenTheUsersOwnServersItsRuntimeOptsIn(t *testing.T) {
	runtime := &userServerRuntime{workflowRuntime: &workflowRuntime{}, names: []string{"notes", "wiki", "broken", "gone"}, statuses: map[string]agentruntime.UserServer{
		"notes":  {Name: "notes", Source: "config", Transport: "stdio", Command: "notes-mcp", Status: agentruntime.UserServerReady},
		"wiki":   {Name: "wiki", Source: "config", Transport: "http", URL: "https://wiki.example/mcp", Status: agentruntime.UserServerUnknown},
		"broken": {Name: "broken", Source: "config", Status: agentruntime.UserServerFailed, Detail: "Failed to connect"},
	}}
	service := userServerService(t, runtime)
	spec := userServerSpec(t, service)
	if !reflect.DeepEqual(spec.Steps[0].UserMCP, []string{"notes", "wiki", "broken", "gone"}) {
		t.Fatalf("the plan must give the step its runtime's servers: %v", spec.Steps[0].UserMCP)
	}
	run := startSpec(t, service, spec)
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	requests := started(runtime.workflowRuntime)
	if len(requests) != 1 {
		t.Fatalf("want one call, got %d", len(requests))
	}
	var given []string
	for _, server := range requests[0].UserServers {
		given = append(given, server.Name)
	}
	if !reflect.DeepEqual(given, []string{"notes", "wiki"}) {
		t.Fatalf("the call must be given the usable servers: %v", given)
	}
	if !runEventsSay(completed, `Your MCP server "broken" is not starting, so the step runs without it: Failed to connect`) || !runEventsSay(completed, `Your MCP server "gone" is no longer configured`) {
		t.Fatalf("the timeline must say which servers the step went without: %#v", completed.Events)
	}
	prompt := string(requests[0].Prompt)
	if !strings.Contains(prompt, "\n# Your MCP servers\n- `notes` (all its tools)\n- `wiki` (all its tools)\n") || !strings.Contains(prompt, "no review stands between them") {
		t.Fatalf("the prompt must name the user's servers:\n%s", prompt)
	}
	stepContext, err := service.RunContext(run.ID, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if recorded := stepContext.Envelope.UserMCPServers; len(recorded) != 4 || recorded[0].Command != "notes-mcp" || recorded[3].Status != agentruntime.UserServerMissing {
		t.Fatalf("the run must record every server and what became of it: %+v", recorded)
	}
}

func TestAStepWaitsForTheUserToSignInToTheirServer(t *testing.T) {
	runtime := &userServerRuntime{workflowRuntime: &workflowRuntime{}, names: []string{"linear"}, statuses: map[string]agentruntime.UserServer{
		"linear": {Name: "linear", Source: "config", Transport: "http", Status: agentruntime.UserServerNeedsSignIn, SignIn: true},
	}}
	service := userServerService(t, runtime)
	run := startSpec(t, service, userServerSpec(t, service))
	request := waitForInbox(t, service, 1)[0]
	waitForWorkflowRun(t, service, run.ID, "waiting")
	if len(started(runtime.workflowRuntime)) != 0 {
		t.Fatal("the step must not start before the user signs in")
	}
	if !reflect.DeepEqual(request.Options, []string{signInAccept, signInSkip}) || !strings.Contains(request.Question, `"linear" needs you to sign in`) {
		t.Fatalf("unexpected sign-in request %#v", request)
	}
	if _, err := service.AnswerInput(request.ID, signInAccept); err != nil {
		t.Fatal(err)
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if !reflect.DeepEqual(runtime.signedIn, []string{"linear"}) {
		t.Fatalf("the runtime must sign in to the server: %v", runtime.signedIn)
	}
	requests := started(runtime.workflowRuntime)
	if len(requests) != 1 || len(requests[0].UserServers) != 1 || requests[0].UserServers[0].Name != "linear" {
		t.Fatalf("the step must start with the server it signed in to: %#v", requests)
	}
	if !runEventsSay(completed, `Signed in to your MCP server "linear"`) {
		t.Fatal("the timeline must say the user signed in")
	}
	if prompt := string(requests[0].Prompt); !strings.Contains(prompt, "\n# Your MCP servers\n- `linear` (all its tools)\n") {
		t.Fatalf("the prompt must name the server the user signed in to:\n%s", prompt)
	}
	stepContext, err := service.RunContext(run.ID, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if recorded := stepContext.Envelope.UserMCPServers; len(recorded) != 1 || recorded[0].Status != agentruntime.UserServerReady || recorded[0].Detail != "signed in" {
		t.Fatalf("the run must record that the server was signed in to: %+v", recorded)
	}
}

func TestAFailedSignInIsAskedAgainAndASkipRunsWithoutTheServer(t *testing.T) {
	runtime := &userServerRuntime{workflowRuntime: &workflowRuntime{}, names: []string{"linear"}, signErr: errors.New("sign in to MCP server \"linear\": the browser was closed"), statuses: map[string]agentruntime.UserServer{
		"linear": {Name: "linear", Source: "config", Transport: "http", Status: agentruntime.UserServerNeedsSignIn, SignIn: true},
	}}
	service := userServerService(t, runtime)
	run := startSpec(t, service, userServerSpec(t, service))
	first := waitForInbox(t, service, 1)[0]
	if _, err := service.AnswerInput(first.ID, signInAccept); err != nil {
		t.Fatal(err)
	}
	var again InputView
	deadline := time.Now().Add(settleTimeout)
	for again.ID == "" || again.ID == first.ID {
		inbox, err := service.Inbox()
		if err != nil {
			t.Fatal(err)
		}
		if len(inbox) == 1 {
			again = inbox[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("a failed sign-in must be asked again")
		}
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(again.Question, "the browser was closed") {
		t.Fatalf("the request must say why the sign-in failed: %q", again.Question)
	}
	if _, err := service.AnswerInput(again.ID, signInSkip); err != nil {
		t.Fatal(err)
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if requests := started(runtime.workflowRuntime); len(requests) != 1 || len(requests[0].UserServers) != 0 {
		t.Fatalf("a skipped server must not be given: %#v", requests)
	}
	if !runEventsSay(completed, `Skipped your MCP server "linear"`) {
		t.Fatal("the timeline must say the server was skipped")
	}
}

func TestAUserServerTheCLISaysDidNotStartIsDroppedOrSignedIn(t *testing.T) {
	runtime := &userServerRuntime{workflowRuntime: &workflowRuntime{}, names: []string{"notes", "linear"}, statuses: map[string]agentruntime.UserServer{
		"notes":  {Name: "notes", Source: "config", Status: agentruntime.UserServerUnknown},
		"linear": {Name: "linear", Source: "config", Transport: "http", Status: agentruntime.UserServerReady, SignIn: true},
	}}
	calls := 0
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		calls++
		names := make([]string, 0, len(request.UserServers))
		for _, server := range request.UserServers {
			names = append(names, server.Name)
		}
		switch {
		case slices.Contains(names, "notes"):
			return agentruntime.RunResult{}, &agentruntime.UserServerError{Server: "notes", Reason: "exited with 1"}
		case calls == 2 && slices.Contains(names, "linear"):
			return agentruntime.RunResult{}, &agentruntime.UserServerError{Server: "linear", Reason: "OAuth authorization required", SignIn: true}
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "done with " + strings.Join(names, ",")})}, nil
	}
	service := userServerService(t, runtime)
	run := startSpec(t, service, userServerSpec(t, service))
	request := waitForInbox(t, service, 1)[0]
	if !strings.Contains(request.Question, `"linear" needs you to sign in`) {
		t.Fatalf("a server Codex says needs signing in must be asked about: %q", request.Question)
	}
	if _, err := service.AnswerInput(request.ID, signInAccept); err != nil {
		t.Fatal(err)
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.Steps[0].Summary != "done with linear" {
		t.Fatalf("the step must finish with the server it signed in to and without the one that failed: %q", completed.Steps[0].Summary)
	}
	if !runEventsSay(completed, `your MCP server "notes" did not start: exited with 1; the step goes on without it`) {
		t.Fatal("the timeline must say which server failed")
	}
}

func TestApprovalRefusesARuntimeOptedInToOtherServersSincePlanning(t *testing.T) {
	runtime := &userServerRuntime{workflowRuntime: &workflowRuntime{}, names: []string{"notes"}}
	service := userServerService(t, runtime)
	spec := userServerSpec(t, service)
	if _, err := service.reviseApproval(context.Background(), spec); err != nil {
		t.Fatalf("an unchanged opt-in must approve: %v", err)
	}
	runtime.names = []string{"notes", "wiki"}
	if _, err := service.reviseApproval(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "plan the task again") || !strings.Contains(err.Error(), "notes, wiki") {
		t.Fatalf("approval must refuse servers the plan did not show: %v", err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "changed after the plan was made") {
		t.Fatalf("starting must refuse it too: %v", err)
	}
	// Moving a step to a runtime with no servers of the user's drops them.
	runtime.names = []string{"notes"}
	edited := spec
	edited.Steps = append([]domain.WorkflowStep(nil), spec.Steps...)
	edited.Steps[0].Runtime = domain.RuntimeClaude
	revised, err := service.ReviseWorkflow(string(mustJSON(t, edited)))
	if err != nil {
		t.Fatal(err)
	}
	if len(revised.Steps[0].UserMCP) != 0 {
		t.Fatalf("a step on claude, which names none, must get none: %v", revised.Steps[0].UserMCP)
	}
	run := startSpec(t, service, revised)
	waitForWorkflowRun(t, service, run.ID, "completed")
	if requests := started(runtime.workflowRuntime); len(requests) != 1 || len(requests[0].UserServers) != 0 {
		t.Fatalf("the moved step must run without the servers: %#v", requests)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAStepWithNoRuntimeOfItsOwnIsGivenTheDefaultRuntimesServers(t *testing.T) {
	spec := domain.WorkflowSpec{DefaultRuntime: domain.RuntimeCodex, UserMCP: map[domain.Runtime][]string{domain.RuntimeCodex: {"notes"}}, Steps: []domain.WorkflowStep{
		{ID: "worker", Kind: domain.StepAgent},
		{ID: "advisor", Kind: domain.StepAgent, Runtime: domain.RuntimeRemote},
	}}
	applyUserServers(&spec, spec.UserMCP)
	if !reflect.DeepEqual(spec.Steps[0].UserMCP, []string{"notes"}) || spec.Steps[1].UserMCP != nil {
		t.Fatalf("a step without a runtime runs on the default, and gets its servers: %v, %v", spec.Steps[0].UserMCP, spec.Steps[1].UserMCP)
	}
	err := checkUserServersUnchanged(spec, map[domain.Runtime][]string{domain.RuntimeCodex: {"wiki"}})
	if err == nil || !strings.Contains(err.Error(), `runtime "codex"`) {
		t.Fatalf("a change on the default runtime must be refused: %v", err)
	}
}

func TestTheRunKeepsAUserServersURLWithoutWhereACredentialCanSit(t *testing.T) {
	for raw, want := range map[string]string{
		"https://user:token@mcp.example.com/sse?api_key=secret#part": "https://mcp.example.com/sse",
		"https://mcp.linear.app/mcp":                                 "https://mcp.linear.app/mcp",
		"https://mcp.example.com/%zz?key=secret":                     "",
		"":                                                           "",
	} {
		if got := userServerBrief(agentruntime.UserServer{Name: "s", URL: raw}).URL; got != want {
			t.Errorf("brief of %q = %q, want %q", raw, got, want)
		}
	}
}
