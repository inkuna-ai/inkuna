package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// Inbox options for a sign-in request. Anything but signing in skips.
const (
	signInAccept = "Sign in"
	signInSkip   = "Skip"
)

// signInRequest is a paused step waiting for the user to sign in to some of
// their own MCP servers, which it is given once they have; failed says why
// the last attempt did not work, when it did not.
type signInRequest struct {
	servers []agentruntime.UserServer
	failed  string
}

// userMCPByRuntime is each declared runtime's userMcp, for the runtimes that
// name any: what a plan records, and approval holds it to.
func (service *Service) userMCPByRuntime() map[domain.Runtime][]string {
	byRuntime := map[domain.Runtime][]string{}
	for name, adapter := range service.declaredAdapters() {
		if names := adapter.Capabilities().UserMCP; len(names) > 0 {
			byRuntime[name] = append([]string(nil), names...)
		}
	}
	if len(byRuntime) == 0 {
		return nil
	}
	return byRuntime
}

// applyUserServers gives each local agent step the user's own MCP servers
// its runtime opts in to, from the plan's record of every runtime's.
func applyUserServers(workflow *domain.WorkflowSpec, byRuntime map[domain.Runtime][]string) {
	for index := range workflow.Steps {
		step := &workflow.Steps[index]
		step.UserMCP = nil
		if runtime := stepRuntime(*workflow, *step); step.Kind == domain.StepAgent && runtime != domain.RuntimeRemote {
			step.UserMCP = append([]string(nil), byRuntime[runtime]...)
		}
		if len(step.UserMCP) == 0 {
			step.UserMCP = nil
		}
	}
}

// checkUserServersUnchanged refuses a plan with a step whose runtime opts in
// to other servers of the user's than the plan shows: the run would give the
// step what userMcp says now, not what was approved. Only names are
// compared, since a server's definition is the CLI's, which Inkuna does not
// read.
func checkUserServersUnchanged(spec domain.WorkflowSpec, current map[domain.Runtime][]string) error {
	for _, step := range spec.Steps {
		runtime := stepRuntime(spec, step)
		if step.Kind != domain.StepAgent || runtime == domain.RuntimeRemote {
			continue
		}
		planned, now := sortedCopy(spec.UserMCP[runtime]), sortedCopy(current[runtime])
		if !slices.Equal(planned, now) {
			return fmt.Errorf("the MCP servers of your own that runtime %q gives each step changed after the plan was made (it showed %s, and userMcp now names %s), so step %q would not be given what the plan shows; plan the task again", runtime, namesOrNone(planned), namesOrNone(now), step.ID)
		}
	}
	return nil
}

// stepRuntime is the runtime a step runs on: its own, or the plan's default
// when it names none, as executeWorkflowStep resolves it.
func stepRuntime(spec domain.WorkflowSpec, step domain.WorkflowStep) domain.Runtime {
	if step.Runtime == "" {
		return spec.DefaultRuntime
	}
	return step.Runtime
}

func sortedCopy(values []string) []string {
	copied := append([]string(nil), values...)
	slices.Sort(copied)
	return copied
}

func namesOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// userServerRuntime is a declared runtime that can list its user's servers.
func (service *Service) userServerRuntime(name string) (agentruntime.UserServers, error) {
	adapter, err := service.runtimeFor(domain.Runtime(strings.TrimSpace(name)))
	if err != nil {
		return nil, err
	}
	servers, ok := adapter.(agentruntime.UserServers)
	if !ok || !adapter.Capabilities().MCPServers {
		return nil, fmt.Errorf("runtime %q is an argument template, which cannot be given MCP servers", name)
	}
	return servers, nil
}

// ListUserMCPServers lists the MCP servers a runtime's CLI finds in the
// user's own setup, with what it says of each, for Settings to choose from.
// It runs in the home directory, so a repository's project servers are not
// among them. Claude Code connects to every server to list them, which can
// take a few seconds.
func (service *Service) ListUserMCPServers(name string) ([]agentruntime.UserServer, error) {
	servers, err := service.userServerRuntime(name)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find the home directory to list your MCP servers from: %w", err)
	}
	listed, err := servers.ListUserServers(service.context(), home)
	if listed == nil {
		listed = []agentruntime.UserServer{}
	}
	return listed, err
}

// SignInUserMCPServer runs a runtime's own sign-in for one of the user's MCP
// servers, which opens the browser, and returns once it has ended.
func (service *Service) SignInUserMCPServer(runtimeName, server string) error {
	servers, err := service.userServerRuntime(runtimeName)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find the home directory to sign in from: %w", err)
	}
	return servers.SignInUserServer(service.context(), home, server)
}

// resolveUserServers asks the step's runtime about the user's servers the
// step is given, in its working directory: the usable ones it is given,
// the ones waiting for the user to sign in, and a record of each for the
// envelope. A server that is neither, or a runtime that cannot answer, is
// left out with a note on the timeline: the user's own setup is not
// something a step can fix.
func (service *Service) resolveUserServers(ctx context.Context, adapter agentruntime.AgentRuntime, executionID, stepRunID, workingDir string, names []string) (given, pending []agentruntime.UserServer, briefs []domain.UserMCPServer) {
	if len(names) == 0 {
		return nil, nil, nil
	}
	note := func(message string) {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, message))
	}
	lister, ok := adapter.(agentruntime.UserServers)
	if !ok {
		note(fmt.Sprintf("The step's runtime cannot be given your own MCP servers, so it runs without %s", strings.Join(names, ", ")))
		return nil, nil, nil
	}
	resolved, err := lister.ResolveUserServers(ctx, workingDir, names)
	if err != nil {
		note(fmt.Sprintf("Your own MCP servers could not be listed, so the step runs without %s: %v", strings.Join(names, ", "), err))
		for _, name := range names {
			briefs = append(briefs, domain.UserMCPServer{Name: name, Status: agentruntime.UserServerFailed, Detail: "could not be listed"})
		}
		return nil, nil, briefs
	}
	for _, server := range resolved {
		briefs = append(briefs, userServerBrief(server))
		switch {
		case server.Usable():
			given = append(given, server)
		case server.Status == agentruntime.UserServerNeedsSignIn && server.SignIn:
			pending = append(pending, server)
		default:
			note(fmt.Sprintf("Your MCP server %q is %s, so the step runs without it%s", server.Name, statusWords(server.Status), prefixed(": ", server.Detail)))
		}
	}
	return given, pending, briefs
}

// userServerBrief is what the run keeps of one of the user's servers. Its
// URL loses any user information, query, and fragment, where a server's
// credential can sit; the runtime keeps the whole URL, which is what
// allows a claude.ai connector.
func userServerBrief(server agentruntime.UserServer) domain.UserMCPServer {
	return domain.UserMCPServer{Name: server.Name, Source: server.Source, Transport: server.Transport, URL: briefURL(server.URL), Command: server.Command, Status: server.Status, Detail: server.Detail}
}

// briefURL is a URL without its user information, query, or fragment, or
// "" when it does not parse, since then what holds a credential is unknown.
func briefURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User, parsed.RawQuery, parsed.ForceQuery, parsed.Fragment, parsed.RawFragment = nil, "", false, "", ""
	return parsed.String()
}

func statusWords(status string) string {
	switch status {
	case agentruntime.UserServerNeedsSignIn:
		return "not signed in"
	case agentruntime.UserServerMissing:
		return "no longer configured"
	case agentruntime.UserServerDisabled:
		return "switched off"
	default:
		return "not starting"
	}
}

// signInQuestion is what the inbox asks of a step waiting on sign-in.
func signInQuestion(assignment *workerAssignment, request *signInRequest) string {
	names := make([]string, 0, len(request.servers))
	for _, server := range request.servers {
		names = append(names, fmt.Sprintf("%q", server.Name))
	}
	subject := "your MCP server " + names[0] + " needs"
	if len(names) > 1 {
		subject = "your MCP servers " + strings.Join(names, ", ") + " need"
	}
	question := fmt.Sprintf("Before %s starts on %q, %s you to sign in. Sign in opens the browser; Skip runs the step without it.", assignment.definition.Name, firstNonEmpty(assignment.step.Title, assignment.step.ID), subject)
	if request.failed != "" {
		question += "\n\nThe last sign-in did not work: " + request.failed
	}
	return question
}

// raiseSignIn pauses the step's A2A task for the user to sign in.
func (service *Service) raiseSignIn(ctx context.Context, bus *a2a.Bus, task a2a.Task, assignment *workerAssignment, request *signInRequest) error {
	assignment.mu.Lock()
	assignment.signIn = request
	assignment.mu.Unlock()
	question := signInQuestion(assignment, request)
	input := storage.InputRequest{
		ID: newID("input"), ExecutionID: assignment.executionID, StepID: assignment.step.ID, A2ATaskID: task.ID,
		Agent: task.Agent, AskedBy: assignment.definition.Name, Question: question, Options: []string{signInAccept, signInSkip},
		Context: fmt.Sprintf("The step runs on %s, which you opted in to these servers of yours (userMcp). Signing in is the CLI's own; Inkuna never sees the credentials.", assignment.runtimeName),
		State:   storage.InputPending, CreatedAt: time.Now().UTC(),
	}
	parts := []a2a.Part{a2a.TextPart(question), a2a.DataPart(map[string]any{"options": input.Options, "inputId": input.ID, "kind": "sign-in"})}
	if err := bus.UpdateStatus(ctx, task.ID, a2a.StateInputRequired, &a2a.Message{Role: a2a.RoleAgent, Parts: parts}); err != nil {
		return err
	}
	return service.raiseInput(input)
}

// answerSignIn acts on the user's answer to a sign-in request: it signs in
// to each server, giving the step the ones that worked, or skips them. It
// returns the request to put to the user again when a sign-in failed.
func (service *Service) answerSignIn(ctx context.Context, assignment *workerAssignment, request *signInRequest, answer string) *signInRequest {
	note := func(kind domain.EventKind, message string) {
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, kind, message))
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "sign") {
		for _, server := range request.servers {
			note(domain.EventWarning, fmt.Sprintf("Skipped your MCP server %q, which needs signing in; the step runs without it", server.Name))
		}
		return nil
	}
	adapter, err := service.dependenciesFor(assignment.executionID).runtime(assignment.runtimeName)
	signer, ok := adapter.(agentruntime.UserServers)
	if err != nil || !ok {
		note(domain.EventWarning, "The step's runtime cannot sign in to MCP servers, so the step runs without them")
		return nil
	}
	retry := &signInRequest{}
	var failures []string
	for _, server := range request.servers {
		if err := signer.SignInUserServer(ctx, assignment.workingDir, server.Name); err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			retry.servers = append(retry.servers, server)
			failures = append(failures, err.Error())
			note(domain.EventWarning, err.Error())
			continue
		}
		note(domain.EventLifecycle, fmt.Sprintf("Signed in to your MCP server %q; the step is given it", server.Name))
		server.Status, server.Detail = agentruntime.UserServerReady, "signed in"
		// The prompt names the servers its envelope says are ready, and the
		// Context tab shows the run's copy, so both say it is given now.
		assignment.mu.Lock()
		assignment.userServers = append(assignment.userServers, server)
		assignment.envelope.UserMCPServers = signedIn(assignment.envelope.UserMCPServers, server)
		assignment.mu.Unlock()
		service.runsMu.Lock()
		if run := service.runs[assignment.executionID]; run != nil {
			if envelope, ok := run.contexts[assignment.step.ID]; ok {
				envelope.UserMCPServers = signedIn(envelope.UserMCPServers, server)
				run.contexts[assignment.step.ID] = envelope
			}
		}
		service.runsMu.Unlock()
	}
	if len(retry.servers) == 0 {
		return nil
	}
	retry.failed = strings.Join(failures, "; ")
	return retry
}

// signedIn is a step's record of the user's servers with one it signed in to
// shown as ready. It is a copy: the envelope and the run's copy of it can
// share the slice.
func signedIn(briefs []domain.UserMCPServer, server agentruntime.UserServer) []domain.UserMCPServer {
	updated := append([]domain.UserMCPServer(nil), briefs...)
	for index := range updated {
		if updated[index].Name == server.Name {
			updated[index] = userServerBrief(server)
		}
	}
	return updated
}

// takeUserServer takes one of the step's user servers off it and returns
// it, for the user to sign in to before the step is given it again.
func (assignment *workerAssignment) takeUserServer(name string) agentruntime.UserServer {
	assignment.mu.Lock()
	defer assignment.mu.Unlock()
	taken := agentruntime.UserServer{Name: name, Status: agentruntime.UserServerNeedsSignIn, SignIn: true}
	kept := assignment.userServers[:0:0]
	for _, server := range assignment.userServers {
		if server.Name == name {
			taken = server
			continue
		}
		kept = append(kept, server)
	}
	assignment.userServers = kept
	return taken
}

// dropUserServer takes a server the CLI said did not start off the step, so
// the next turn runs without it.
func (assignment *workerAssignment) dropUserServer(name string) {
	assignment.mu.Lock()
	defer assignment.mu.Unlock()
	kept := assignment.userServers[:0:0]
	for _, server := range assignment.userServers {
		if server.Name != name {
			kept = append(kept, server)
		}
	}
	assignment.userServers = kept
}
