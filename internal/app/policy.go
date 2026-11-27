package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// policyTimeout bounds how long a decision waits for each policy. A policy
// that takes longer refuses, as one that errs does: some decisions hold a
// save or a run's lock until every policy answers. Tests shorten it.
var policyTimeout = 10 * time.Second

// policyInFlight bounds how many checks one policy may have running. A check
// abandoned at its deadline keeps running until the policy returns, so one
// that ignores its context would otherwise gain a goroutine, and the
// decision it holds, at every retry. A variable so tests can lower it.
var policyInFlight = 8

// registeredPolicy is one policy and the name its refusals give it. It is
// named as sinks are, by its extension and its place among that
// extension's policies: "example/policy", then "example/policy-2".
type registeredPolicy struct {
	name   string
	policy extension.Policy
	// running holds a token for each check still running, abandoned ones
	// included, up to policyInFlight.
	running chan struct{}
}

// addPolicies takes one extension's policies, in the order it registered
// them, which is the order they are asked in.
func (registry *extensionRegistry) addPolicies(name string, policies []extension.Policy) error {
	for index, policy := range policies {
		if policy == nil {
			return errors.New("its policy is nil")
		}
		registry.policies = append(registry.policies, registeredPolicy{name: sinkName(name, "policy", index), policy: policy, running: make(chan struct{}, policyInFlight)})
	}
	return nil
}

// policyRefusal is a decision a policy refused, or one refused because a
// policy could not decide. Its message is the reason the person sees, and
// names the policy.
type policyRefusal struct{ reason string }

func (refusal *policyRefusal) Error() string { return refusal.reason }

// policing reports whether the build has a policy. Without one no decision
// asks for a principal it did not ask for before, so Inkuna behaves as it
// did before policies existed.
func (service *Service) policing() bool {
	return service.extensions != nil && len(service.extensions.policies) > 0
}

// decide asks every policy about a decision Go has already allowed, in
// registration order, and returns the first refusal. The caller has made
// every check of its own, and changes nothing until decide returns nil, so
// a policy can refuse what Go allowed but never allow what Go refused.
//
// principal is the person deciding when the caller already asked for them,
// or nil to ask now. A provider that cannot name anyone refuses the
// decision, as it refuses the decisions that always asked for a person.
func (service *Service) decide(ctx context.Context, decision extension.Decision, principal *extension.Principal) error {
	if !service.policing() {
		return nil
	}
	if principal == nil {
		current, err := service.principal(ctx)
		if err != nil {
			return err
		}
		principal = &current
	}
	decision.Principal = *principal
	maps.DeleteFunc(decision.Details, func(_, value string) bool { return value == "" })
	for _, registered := range service.extensions.policies {
		if err := askPolicy(ctx, registered, decision); err != nil {
			return err
		}
	}
	return nil
}

// askPolicy asks one policy, failing closed: an error, a panic, or no answer
// by the deadline refuses. The policy runs apart, so one that ignores its
// context cannot hold the decision past the deadline, and gets its own copy
// of the decision, so it cannot change what the next policy is asked.
func askPolicy(ctx context.Context, registered registeredPolicy, decision extension.Decision) error {
	// Refused at once, without waiting, once the policy has as many checks
	// running as it may: they are ones it has not answered, and waiting for
	// a slot would only hold this decision too.
	select {
	case registered.running <- struct{}{}:
	default:
		return &policyRefusal{fmt.Sprintf("policy %q is still deciding %d earlier requests, so this is refused; try again", registered.name, cap(registered.running))}
	}
	ctx, cancel := context.WithTimeout(ctx, policyTimeout)
	defer cancel()
	type answer struct {
		verdict extension.Verdict
		// failed is set when Check returned an error, and failure is what
		// it said, rendered where the deadline still applies.
		failed   bool
		failure  string
		panicked bool
	}
	asked := decision
	asked.Details = maps.Clone(decision.Details)
	asked.Principal.Groups = slices.Clone(decision.Principal.Groups)
	answered := make(chan answer, 1)
	go func() {
		// A refusal until Check returns, so a check that never does, by
		// panicking or by ending its goroutine with runtime.Goexit, which
		// recover cannot see, refuses rather than sends the zero answer,
		// which would allow.
		result := answer{panicked: true}
		defer func() {
			if recovered := recover(); recovered != nil {
				// What a panic says is the extension's, and may hold anything,
				// so it goes to the log rather than to the person.
				log.Printf("inkuna: policy %s panicked deciding %s: %v", registered.name, decision.Point, recovered)
				result = answer{panicked: true}
			} else if result.panicked {
				log.Printf("inkuna: policy %s ended its goroutine deciding %s without answering", registered.name, decision.Point)
			}
			// Held until Check and its error's message have returned, so an
			// abandoned check still counts, and released before the answer is
			// sent, so a check started once this decision has its answer
			// finds the slot free.
			<-registered.running
			answered <- result
		}()
		verdict, err := registered.policy.Check(ctx, asked)
		result = answer{verdict: verdict}
		if err != nil {
			// The error is the extension's, so its message is rendered here,
			// where one that blocks meets the deadline and one that panics
			// is recovered, never on the decision's own goroutine.
			result.failed, result.failure = true, strings.TrimSpace(err.Error())
		}
	}()
	var got answer
	select {
	case got = <-answered:
	case <-ctx.Done():
	}
	// Checked after the select too: when an answer and the deadline are
	// both ready, select may take either, and an answer that came too late
	// must not allow anything.
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &policyRefusal{fmt.Sprintf("policy %q did not decide within %s, so this is refused; try again", registered.name, policyTimeout)}
		}
		return &policyRefusal{fmt.Sprintf("policy %q could not decide, so this is refused: %v", registered.name, err)}
	}
	switch {
	case got.panicked:
		return &policyRefusal{fmt.Sprintf("policy %q failed while deciding, so this is refused", registered.name)}
	case got.failed && got.failure == "":
		return &policyRefusal{fmt.Sprintf("policy %q could not decide, so this is refused", registered.name)}
	case got.failed:
		return &policyRefusal{fmt.Sprintf("policy %q could not decide, so this is refused: %s", registered.name, got.failure)}
	case !got.verdict.Refuse:
		return nil
	case strings.TrimSpace(got.verdict.Reason) == "":
		return &policyRefusal{fmt.Sprintf("policy %q refuses this", registered.name)}
	}
	return &policyRefusal{fmt.Sprintf("policy %q: %s", registered.name, strings.TrimSpace(got.verdict.Reason))}
}

// policyList is a Decision detail's list: each value once, sorted, and
// comma-separated, empty when there is none.
func policyList(values ...string) string {
	listed := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !slices.Contains(listed, value) {
			listed = append(listed, value)
		}
	}
	slices.Sort(listed)
	return strings.Join(listed, ",")
}

// planDecision is approving spec, for plan.approve: what the run would do,
// by its agent steps.
func planDecision(projectID string, spec domain.WorkflowSpec, rerunOf string) extension.Decision {
	var agents, runtimes, models, permissions, servers, userServers, trackerKinds []string
	for _, step := range spec.Steps {
		if step.Kind != domain.StepAgent {
			continue
		}
		agents = append(agents, step.Agent)
		runtimes = append(runtimes, string(step.Runtime))
		if step.ModelSelection != nil {
			models = append(models, step.ModelSelection.Model)
		}
		permissions = append(permissions, string(step.Permission))
		servers = append(servers, step.MCPServers...)
		userServers = append(userServers, step.UserMCP...)
		for _, kind := range step.TrackerKinds {
			trackerKinds = append(trackerKinds, string(kind))
		}
	}
	return extension.Decision{Point: extension.PointPlanApprove, Project: projectID, Actor: "user", Details: map[string]string{
		"workflow": spec.ID, "task": spec.TaskID, "rule": spec.RuleName, "rerunOf": rerunOf, "steps": strconv.Itoa(len(spec.Steps)),
		"agents": policyList(agents...), "runtimes": policyList(runtimes...), "models": policyList(models...), "permissions": policyList(permissions...),
		"mcpServers": policyList(servers...), "userMcpServers": policyList(userServers...), "trackerKinds": policyList(trackerKinds...),
	}}
}

// mcpDecision is a step about to start with MCP servers, for mcp.grant: the
// servers inkuna.yaml declares that it is granted, and userServers, the
// user's own it will be given, which are those its runtime still offers.
func mcpDecision(run *liveExecution, executionID string, step domain.WorkflowStep, agentName string, runtimeName domain.Runtime, userServers []string) extension.Decision {
	return extension.Decision{Point: extension.PointMCPGrant, Project: run.project.ID, Actor: agentName, Details: map[string]string{
		"run": executionID, "task": run.task.ID, "step": step.ID, "agent": agentName, "runtime": string(runtimeName), "permission": string(step.Permission),
		"servers": policyList(step.MCPServers...), "userServers": policyList(userServers...),
	}}
}

// permissionDecision is a person allowing what a worker was denied, for
// permission.grant: the commands and the tools it would be allowed. A run
// that cannot be read refuses, rather than ask the policies about a grant
// with no project, which a policy for that project would not recognise.
func (service *Service) permissionDecision(ctx context.Context, assignment *workerAssignment, request *permissionRequest) (extension.Decision, error) {
	var commands, tools []string
	for _, grant := range request.grants {
		if grant.Command != "" {
			commands = append(commands, grant.Command)
		} else {
			tools = append(tools, grant.Tool)
		}
	}
	execution, err := service.store.Execution(ctx, assignment.executionID)
	if err != nil {
		return extension.Decision{}, fmt.Errorf("read the run to ask its policies: %w", err)
	}
	return extension.Decision{Point: extension.PointPermissionGrant, Project: execution.ProjectID, Actor: "user", Details: map[string]string{
		"run": assignment.executionID, "task": assignment.task.ID, "step": assignment.step.ID, "agent": assignment.definition.Name,
		"runtime": string(assignment.runtimeName), "permission": string(assignment.step.Permission),
		// A command can hold a comma, so commands are one per line, as asked.
		"commands": strings.Join(commands, "\n"), "tools": policyList(tools...),
	}}, nil
}

// saveDecision is a save whose checks passed, for rules.save, agent.save,
// and config.save: the file it would write and the digest of what it would
// write, never the content, with the point's own details.
func saveDecision(point, projectID, path string, content []byte, details map[string]string) extension.Decision {
	if details == nil {
		details = map[string]string{}
	}
	details["path"], details["digest"] = path, contentDigest(content)
	return extension.Decision{Point: point, Project: projectID, Actor: "user", Details: details}
}

// configDecision is saving inkuna.yaml as file, for config.save, with the
// names of what it declares.
func configDecision(path string, content []byte, file config.File) extension.Decision {
	var repositories, servers, secrets []string
	for _, repository := range file.Repositories {
		repositories = append(repositories, repository.Path)
	}
	for _, server := range file.MCPServers {
		servers = append(servers, server.Name)
	}
	for _, secret := range file.Secrets {
		secrets = append(secrets, secret.Name)
	}
	return saveDecision(extension.PointConfigSave, "", path, content, map[string]string{
		"repositories": policyList(repositories...), "mcpServers": policyList(servers...), "secrets": policyList(secrets...),
	})
}

// keepAnswerer records who answered input, while the build has a policy that
// will be asked about it.
func (service *Service) keepAnswerer(inputID string, principal extension.Principal) {
	if !service.policing() {
		return
	}
	service.answerersMu.Lock()
	defer service.answerersMu.Unlock()
	if service.answerers == nil {
		service.answerers = make(map[string]extension.Principal)
	}
	service.answerers[inputID] = principal
}

// takeAnswerer returns who answered input and forgets them.
func (service *Service) takeAnswerer(inputID string) (extension.Principal, bool) {
	service.answerersMu.Lock()
	defer service.answerersMu.Unlock()
	principal, ok := service.answerers[inputID]
	delete(service.answerers, inputID)
	return principal, ok
}

// decidePermission asks the policies about a permission request a person
// allowed, as the person who answered it. An answer Inkuna has no record of
// refuses: asking who is acting now could name someone else.
func (service *Service) decidePermission(ctx context.Context, assignment *workerAssignment, request *permissionRequest) error {
	if !service.policing() {
		return nil
	}
	principal, ok := service.takeAnswerer(request.inputID)
	if !ok {
		return &policyRefusal{"Inkuna does not know who allowed this, so it is refused; ask again"}
	}
	decision, err := service.permissionDecision(ctx, assignment, request)
	if err != nil {
		return err
	}
	return service.decide(ctx, decision, &principal)
}
