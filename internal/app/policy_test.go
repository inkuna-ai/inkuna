package app

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// policyMode is how a fakePolicy answers at its point.
type policyMode int

const (
	policyAllows policyMode = iota
	policyRefuses
	policyErrs
	// policyHangs never answers until the test ends, ignoring its context
	// as a careless policy would.
	policyHangs
)

// fakePolicy answers at one point as its mode says, allows every other
// point, and records every decision it is asked about.
type fakePolicy struct {
	point string
	mode  policyMode
	hung  chan struct{}

	mu    sync.Mutex
	asked []extension.Decision
}

func (policy *fakePolicy) Check(_ context.Context, decision extension.Decision) (extension.Verdict, error) {
	policy.mu.Lock()
	policy.asked = append(policy.asked, decision)
	policy.mu.Unlock()
	if decision.Point != policy.point {
		return extension.Verdict{}, nil
	}
	switch policy.mode {
	case policyRefuses:
		return extension.Verdict{Refuse: true, Reason: "Only release managers may do this"}, nil
	case policyErrs:
		return extension.Verdict{}, errors.New("the policy server is down")
	case policyHangs:
		<-policy.hung
	}
	return extension.Verdict{}, nil
}

// decisions are the decisions the policy was asked about at point.
func (policy *fakePolicy) decisions(point string) []extension.Decision {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	var at []extension.Decision
	for _, decision := range policy.asked {
		if decision.Point == point {
			at = append(at, decision)
		}
	}
	return at
}

// policyModes are the answers every point is tested with, and the reason
// each refuses with; allowing refuses nothing.
var policyModes = []struct {
	name    string
	mode    policyMode
	refusal string
}{
	{"allows", policyAllows, ""},
	{"refuses", policyRefuses, `policy "acme-policy/policy": Only release managers may do this`},
	{"errs", policyErrs, `policy "acme-policy/policy" could not decide, so this is refused: the policy server is down`},
	{"times out", policyHangs, `policy "acme-policy/policy" did not decide within 50ms, so this is refused; try again`},
}

// withPolicy registers, on a service built without New, a policy that
// answers at point as mode says, beside an identity provider naming ada,
// and shortens the policies' deadline.
func withPolicy(t *testing.T, service *Service, point string, mode policyMode) *fakePolicy {
	t.Helper()
	previous := policyTimeout
	policyTimeout = 50 * time.Millisecond
	t.Cleanup(func() { policyTimeout = previous })
	policy := &fakePolicy{point: point, mode: mode, hung: make(chan struct{})}
	t.Cleanup(func() { close(policy.hung) })
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{
		name: "acme-policy", identities: []extension.IdentityProvider{&fakeIdentity{principal: ada}}, policies: []extension.Policy{policy},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service.extensions = registry
	return policy
}

// onlyDecision is the one decision the policy was asked about at point,
// checked for the facts every decision carries.
func onlyDecision(t *testing.T, policy *fakePolicy, point, project, actor string) extension.Decision {
	t.Helper()
	decisions := policy.decisions(point)
	if len(decisions) != 1 {
		t.Fatalf("the policy was asked about %s %d times, want once: %+v", point, len(decisions), decisions)
	}
	decision := decisions[0]
	if decision.Project != project || decision.Actor != actor || !reflect.DeepEqual(decision.Principal, ada) {
		t.Fatalf("decision = %+v, want project %q, actor %q, and the principal %+v", decision, project, actor, ada)
	}
	return decision
}

// wantDetails fails unless decision has each of want's details.
func wantDetails(t *testing.T, decision extension.Decision, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if decision.Details[key] != value {
			t.Errorf("%s detail %q = %q, want %q (all: %v)", decision.Point, key, decision.Details[key], value, decision.Details)
		}
	}
}

// orderedPolicy answers with verdict, records that it was asked, and
// tampers with what it was given.
type orderedPolicy struct {
	verdict extension.Verdict
	asked   *[]extension.Decision
}

func (policy orderedPolicy) Check(_ context.Context, decision extension.Decision) (extension.Verdict, error) {
	kept := decision
	kept.Details = maps.Clone(decision.Details)
	kept.Principal.Groups = slices.Clone(decision.Principal.Groups)
	*policy.asked = append(*policy.asked, kept)
	decision.Details["rule"] = "tampered"
	decision.Principal.Groups[0] = "tampered"
	return policy.verdict, nil
}

type panickingPolicy struct{}

func (panickingPolicy) Check(context.Context, extension.Decision) (extension.Verdict, error) {
	panic("the policy's own bug")
}

func TestPoliciesAreAskedInOrderUntilOneRefuses(t *testing.T) {
	var asked []extension.Decision
	first := orderedPolicy{asked: &asked}
	second := orderedPolicy{verdict: extension.Verdict{Refuse: true, Reason: "  Not during the freeze "}, asked: &asked}
	third := orderedPolicy{asked: &asked}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{
		&fakeExtension{name: "acme", identities: []extension.IdentityProvider{&fakeIdentity{principal: ada}}, policies: []extension.Policy{first, second}},
		&fakeExtension{name: "later", policies: []extension.Policy{third}},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{extensions: registry}
	err = service.decide(context.Background(), extension.Decision{Point: extension.PointPlanApprove, Project: "project", Actor: "user", Details: map[string]string{"rule": "release", "rerunOf": ""}}, nil)
	var refusal *policyRefusal
	if !errors.As(err, &refusal) || err.Error() != `policy "acme/policy-2": Not during the freeze` {
		t.Fatalf("decide = %v, want the second policy's refusal, named by its place", err)
	}
	if len(asked) != 2 {
		t.Fatalf("asked %d policies; the third must not be asked once the second refused", len(asked))
	}
	// Each was given its own copy, with the provider's principal and no
	// empty details.
	for _, decision := range asked {
		if decision.Details["rule"] != "release" || decision.Principal.Groups[0] != "release-managers" || decision.Principal.ID != ada.ID {
			t.Fatalf("a policy saw another's changes or the wrong principal: %+v", decision)
		}
		if _, ok := decision.Details["rerunOf"]; ok {
			t.Fatalf("an empty detail was passed on: %v", decision.Details)
		}
	}
}

func TestAPolicyThatPanicsOrGivesNoReasonRefuses(t *testing.T) {
	cases := []struct {
		name   string
		policy extension.Policy
		want   string
	}{
		{"a panic", panickingPolicy{}, `policy "acme/policy" failed while deciding, so this is refused`},
		{"no reason", orderedPolicy{verdict: extension.Verdict{Refuse: true}, asked: new([]extension.Decision)}, `policy "acme/policy" refuses this`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", policies: []extension.Policy{tc.policy}}})
			if err != nil {
				t.Fatal(err)
			}
			service := &Service{extensions: registry}
			if err := service.decide(context.Background(), extension.Decision{Point: extension.PointConfigSave, Actor: "user", Details: map[string]string{"rule": "x"}}, &ada); err == nil || err.Error() != tc.want {
				t.Fatalf("decide = %v, want %q", err, tc.want)
			}
		})
	}
}

// Without a policy nothing is asked, not even who is acting; with one, a
// provider that cannot say refuses before any policy is asked.
func TestAPolicyDecisionNeedsAPrincipalOnlyWhenThereIsAPolicy(t *testing.T) {
	provider := &fakeIdentity{err: errors.New("Sign in to continue")}
	service := &Service{}
	withIdentity(t, service, provider)
	if err := service.decide(context.Background(), extension.Decision{Point: extension.PointRulesSave}, nil); err != nil || provider.calls != 0 {
		t.Fatalf("without a policy decide = %v and asked the provider %d times", err, provider.calls)
	}
	var asked []extension.Decision
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{
		name: "acme-sso", identities: []extension.IdentityProvider{provider}, policies: []extension.Policy{orderedPolicy{asked: &asked}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service.extensions = registry
	if err := service.decide(context.Background(), extension.Decision{Point: extension.PointRulesSave}, nil); err == nil || err.Error() != `identity provider "acme-sso": Sign in to continue` || len(asked) != 0 {
		t.Fatalf("decide = %v after asking %d policies; want the provider's refusal and none asked", err, len(asked))
	}
}

func TestANilPolicyStopsStartup(t *testing.T) {
	_, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", policies: []extension.Policy{nil}}})
	if err == nil || err.Error() != `register extension "acme": its policy is nil` {
		t.Fatalf("registerExtensions = %v", err)
	}
}

func TestPlanApprovalAsksThePoliciesOnceGoAllowsIt(t *testing.T) {
	for _, tc := range policyModes {
		t.Run(tc.name, func(t *testing.T) {
			runtime := &workflowRuntime{}
			service := workflowTestService(t, t.TempDir(), runtime)
			spec := workflowTestSpec(t, service)
			policy := withPolicy(t, service, extension.PointPlanApprove, tc.mode)
			encoded, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.StartWorkflow(string(encoded))
			if tc.refusal == "" {
				if err != nil {
					t.Fatal(err)
				}
				waitForWorkflowRun(t, service, run.ID, "completed")
				decision := onlyDecision(t, policy, extension.PointPlanApprove, "project", "user")
				wantDetails(t, decision, map[string]string{
					"workflow": "workflow", "task": "task", "steps": "1", "agents": "researcher", "runtimes": "codex", "models": spec.Steps[0].ModelSelection.Model, "permissions": "read-only",
				})
				return
			}
			if err == nil || err.Error() != tc.refusal {
				t.Fatalf("StartWorkflow = %v, want %q", err, tc.refusal)
			}
			if runs, err := service.store.Executions(context.Background(), 10); err != nil || len(runs) != 0 {
				t.Fatalf("a refused approval started a run: %+v %v", runs, err)
			}
			if state, err := service.store.WorkflowState(context.Background(), spec.ID); err != nil || state != "planned" {
				t.Fatalf("a refused approval left the plan %q, %v", state, err)
			}
			if len(started(runtime)) != 0 {
				t.Fatal("a refused approval reached a model")
			}
		})
	}
	t.Run("not when Go refuses", func(t *testing.T) {
		service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
		spec := workflowTestSpec(t, service)
		policy := withPolicy(t, service, extension.PointPlanApprove, policyAllows)
		spec.Steps[0].Agent = "nobody"
		encoded, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.StartWorkflow(string(encoded)); err == nil || strings.Contains(err.Error(), "policy") {
			t.Fatalf("StartWorkflow = %v, want Go's own refusal", err)
		}
		if asked := policy.decisions(extension.PointPlanApprove); len(asked) != 0 {
			t.Fatalf("a policy was asked about a plan Go refused: %+v", asked)
		}
	})
}

func TestReviewApprovalAsksThePoliciesOnceGoAllowsIt(t *testing.T) {
	for _, tc := range policyModes {
		t.Run(tc.name, func(t *testing.T) {
			service, _, run := pausedRun(t, domain.TaskResearch)
			policy := withPolicy(t, service, extension.PointReviewApprove, tc.mode)
			_, err := service.ApproveReview(run.ID)
			if tc.refusal == "" {
				if err != nil {
					t.Fatal(err)
				}
				waitForWorkflowRun(t, service, run.ID, "completed")
				decision := onlyDecision(t, policy, extension.PointReviewApprove, "project", "user")
				wantDetails(t, decision, map[string]string{"run": run.ID, "task": run.TaskID, "gates": "approval", "round": "1"})
				// Approving a run that is no longer reviewing is Go's to
				// refuse, and the policy is not asked again.
				if _, err := service.ApproveReview(run.ID); err == nil || strings.Contains(err.Error(), "policy") {
					t.Fatalf("approving a completed run = %v, want Go's own refusal", err)
				}
				if asked := policy.decisions(extension.PointReviewApprove); len(asked) != 1 {
					t.Fatalf("a policy was asked about an approval Go refused: %+v", asked)
				}
				return
			}
			if err == nil || err.Error() != tc.refusal {
				t.Fatalf("ApproveReview = %v, want %q", err, tc.refusal)
			}
			if stored, err := service.store.Execution(context.Background(), run.ID); err != nil || stored.State != storage.Reviewing {
				t.Fatalf("a refused approval moved the run to %q, %v", stored.State, err)
			}
			if gate := review(t, service, run.ID); gate.State != storage.InputPending {
				t.Fatalf("a refused approval answered the gate: %+v", gate)
			}
		})
	}
}

func TestTrackerWritesAskThePoliciesOnceTheSettingsAllowThem(t *testing.T) {
	for _, tc := range policyModes {
		t.Run(tc.name, func(t *testing.T) {
			service, _, gh, run := askingRun(t, askedChanges)
			policy := withPolicy(t, service, extension.PointTrackerWrite, tc.mode)
			if _, err := service.ApproveReview(run.ID); err != nil {
				t.Fatal(err)
			}
			var byState map[string][]TrackerChangeView
			eventually(t, "the approved changes are made or fail", func() bool {
				byState = changesByState(detail(t, service, run.ID).TrackerChanges)
				return len(byState[storage.TrackerChangePending]) == 0 && len(byState[storage.TrackerChangeApplying]) == 0
			})
			asked := policy.decisions(extension.PointTrackerWrite)
			if len(asked) != 3 {
				t.Fatalf("the policy was asked about %d writes, want the three approved: %+v", len(asked), asked)
			}
			for _, decision := range asked {
				// The two Go refused when the step ended are never asked about.
				if decision.Details["kind"] == "edit" || decision.Details["issue"] == "99" || decision.Details["issue"] != "12" {
					t.Fatalf("the policy was asked about a change Go refused: %+v", decision)
				}
				if decision.Project != "project" || decision.Actor != "user" || decision.Details["tracker"] != "github" || decision.Principal.ID != ada.ID {
					t.Fatalf("tracker decision = %+v", decision)
				}
			}
			if tc.refusal == "" {
				if len(byState[storage.TrackerChangeApplied]) != 3 {
					t.Fatalf("allowed changes = %+v", byState)
				}
				return
			}
			failed := byState[storage.TrackerChangeFailed]
			if len(failed) != 3 {
				t.Fatalf("refused changes = %+v", byState)
			}
			for _, change := range failed {
				if change.Reason != tc.refusal {
					t.Fatalf("a refused change says %q, want %q", change.Reason, tc.refusal)
				}
			}
			for _, words := range [][]string{{"issue", "comment"}, {"issue", "create"}, {"project", "item-edit"}} {
				if len(gh.ran(words...)) > 0 {
					t.Fatalf("gh %v ran for a refused write", words)
				}
			}
		})
	}
}

func TestAnMCPGrantAsksThePoliciesBeforeTheStepStarts(t *testing.T) {
	for _, tc := range policyModes {
		t.Run(tc.name, func(t *testing.T) {
			runtime := &workflowRuntime{}
			service := mcpTestService(t, runtime)
			if err := service.secretStore.Set("DOCS_TOKEN", "docs-secret"); err != nil {
				t.Fatal(err)
			}
			policy := withPolicy(t, service, extension.PointMCPGrant, tc.mode)
			run := startSpec(t, service, mcpTestSpec(t, service))
			if tc.refusal == "" {
				waitForWorkflowRun(t, service, run.ID, "completed")
				if requests := started(runtime); len(requests) != 1 || len(requests[0].MCPServers) != 1 {
					t.Fatalf("an allowed grant must start the step with its server: %+v", requests)
				}
				decision := onlyDecision(t, policy, extension.PointMCPGrant, "project", "researcher")
				wantDetails(t, decision, map[string]string{"run": run.ID, "step": "worker", "agent": "researcher", "runtime": "codex", "servers": "docs-search", "permission": "read-only"})
				if strings.Contains(strings.Join(mapValues(decision.Details), " "), "docs-secret") {
					t.Fatalf("a secret reached the policy: %v", decision.Details)
				}
				return
			}
			failed := waitForWorkflowRun(t, service, run.ID, "failed")
			if len(started(runtime)) != 0 {
				t.Fatal("a refused grant started the step")
			}
			if !strings.Contains(failed.Steps[0].Summary, tc.refusal) {
				t.Fatalf("the step's failure = %q, want it to say %q", failed.Steps[0].Summary, tc.refusal)
			}
		})
	}
	t.Run("not when Go refuses", func(t *testing.T) {
		runtime := &workflowRuntime{}
		service := mcpTestService(t, runtime)
		policy := withPolicy(t, service, extension.PointMCPGrant, policyAllows)
		run := startSpec(t, service, mcpTestSpec(t, service))
		failed := waitForWorkflowRun(t, service, run.ID, "failed")
		if !strings.Contains(failed.Steps[0].Summary, "needs secret DOCS_TOKEN") {
			t.Fatalf("the step's failure = %q, want Go's own", failed.Steps[0].Summary)
		}
		if asked := policy.decisions(extension.PointMCPGrant); len(asked) != 0 {
			t.Fatalf("a policy was asked about a grant Go refused: %+v", asked)
		}
	})
}

func TestAllowingAPermissionAsksThePolicies(t *testing.T) {
	for _, tc := range policyModes {
		t.Run(tc.name, func(t *testing.T) {
			runtime := deniedUntilGranted(t)
			service := workflowTestService(t, t.TempDir(), runtime)
			policy := withPolicy(t, service, extension.PointPermissionGrant, tc.mode)
			run := startSpec(t, service, workflowTestSpec(t, service))
			request := waitForInbox(t, service, 1)[0]
			if _, err := service.AnswerInput(request.ID, permissionAllow); err != nil {
				t.Fatal(err)
			}
			completed := waitForWorkflowRun(t, service, run.ID, "completed")
			// The Write denial cannot be approved, so Go never offers it and
			// the policy never sees it.
			decision := onlyDecision(t, policy, extension.PointPermissionGrant, "project", "user")
			wantDetails(t, decision, map[string]string{"run": run.ID, "step": "worker", "agent": "researcher", "commands": "golangci-lint run", "tools": ""})
			if kept := answererCount(service); kept != 0 {
				t.Fatalf("%d answers are still kept once the step decided", kept)
			}
			if tc.refusal == "" {
				if completed.Steps[0].Summary != "Linted cleanly" || len(started(runtime)) != 2 {
					t.Fatalf("an allowed permission must be retried with: %+v", completed.Steps[0])
				}
				return
			}
			if completed.Steps[0].Summary != "Could not run the linter" || len(started(runtime)) != 1 {
				t.Fatalf("a refused permission must finish with the denied turn: %+v, %d turns", completed.Steps[0], len(started(runtime)))
			}
			if want := "Permission was allowed but refused, so the step finishes without it: " + tc.refusal; !eventSaid(completed, want) {
				t.Fatalf("the run does not say %q: %+v", want, completed.Events)
			}
		})
	}
	t.Run("not when it is declined", func(t *testing.T) {
		runtime := deniedUntilGranted(t)
		service := workflowTestService(t, t.TempDir(), runtime)
		policy := withPolicy(t, service, extension.PointPermissionGrant, policyAllows)
		run := startSpec(t, service, workflowTestSpec(t, service))
		request := waitForInbox(t, service, 1)[0]
		if _, err := service.AnswerInput(request.ID, permissionDeny); err != nil {
			t.Fatal(err)
		}
		waitForWorkflowRun(t, service, run.ID, "completed")
		if asked := policy.decisions(extension.PointPermissionGrant); len(asked) != 0 {
			t.Fatalf("a policy was asked about a permission the person declined: %+v", asked)
		}
		if kept := answererCount(service); kept != 0 {
			t.Fatalf("a decline kept %d answers", kept)
		}
	})
}

// docsOnlyRules is the project's built-in rules with a rule added before
// them, as the rules editor saves them.
func docsOnlyRules(t *testing.T, service *Service, agentName string) string {
	t.Helper()
	view, err := service.Rules("project")
	if err != nil {
		t.Fatal(err)
	}
	edited := view.Document
	edited.Rules = append([]rules.Rule{{
		Name: "docs-only",
		When: rules.Condition{Kinds: []domain.TaskKind{domain.TaskDocumentation}, Keywords: []string{"readme"}},
		Then: rules.Flow{Stages: []rules.Stage{
			{ID: "write", Kind: domain.StepAgent, Agent: agentName, Permission: domain.PermissionWorktree},
			{ID: "approve", Kind: domain.StepApproval, DependsOn: []string{"write"}},
		}},
	}}, edited.Rules...)
	data, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSavingRulesAsksThePoliciesOnceTheyAreValid(t *testing.T) {
	for _, tc := range policyModes {
		t.Run(tc.name, func(t *testing.T) {
			repository := gitRepository(t)
			service := workflowTestService(t, repository, &workflowRuntime{})
			document := docsOnlyRules(t, service, "documenter")
			policy := withPolicy(t, service, extension.PointRulesSave, tc.mode)
			project, err := service.store.Project(context.Background(), "project")
			if err != nil {
				t.Fatal(err)
			}
			path := service.rulesPath(project)
			_, err = service.SaveRules("project", document)
			if tc.refusal == "" {
				if err != nil {
					t.Fatal(err)
				}
				written, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				decision := onlyDecision(t, policy, extension.PointRulesSave, "project", "user")
				wantDetails(t, decision, map[string]string{"path": path, "digest": contentDigest(written)})
				return
			}
			if err == nil || err.Error() != tc.refusal {
				t.Fatalf("SaveRules = %v, want %q", err, tc.refusal)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("a refused save wrote %s: %v", path, err)
			}
		})
	}
	t.Run("not when Go refuses", func(t *testing.T) {
		service := workflowTestService(t, gitRepository(t), &workflowRuntime{})
		document := docsOnlyRules(t, service, "nobody")
		policy := withPolicy(t, service, extension.PointRulesSave, policyAllows)
		if _, err := service.SaveRules("project", document); err == nil || !strings.Contains(err.Error(), `unknown agent "nobody"`) {
			t.Fatalf("SaveRules = %v, want Go's own refusal", err)
		}
		if asked := policy.decisions(extension.PointRulesSave); len(asked) != 0 {
			t.Fatalf("a policy was asked about rules Go refused: %+v", asked)
		}
	})
}

func TestSavingAnAgentAsksThePoliciesOnceItIsValid(t *testing.T) {
	encode := func(t *testing.T, definition agent.Definition) string {
		data, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	reviewer := agent.Definition{
		Name: "security-reviewer", Description: "Reviews for security issues", Permission: domain.PermissionReadOnly,
		TaskKinds: []domain.TaskKind{domain.TaskReview}, Instructions: "Check for injection.",
	}
	for _, tc := range policyModes {
		t.Run(tc.name, func(t *testing.T) {
			repository := gitRepository(t)
			service := workflowTestService(t, repository, &workflowRuntime{})
			policy := withPolicy(t, service, extension.PointAgentSave, tc.mode)
			path := filepath.Join(repository, ".inkuna", "agents", "security-reviewer.md")
			_, err := service.SaveAgent("project", AgentScopeRepository, "", encode(t, reviewer))
			if tc.refusal == "" {
				if err != nil {
					t.Fatal(err)
				}
				written, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				decision := onlyDecision(t, policy, extension.PointAgentSave, "project", "user")
				wantDetails(t, decision, map[string]string{"agent": "security-reviewer", "scope": "repository", "permission": "read-only", "path": path, "digest": contentDigest(written)})
				return
			}
			if err == nil || err.Error() != tc.refusal {
				t.Fatalf("SaveAgent = %v, want %q", err, tc.refusal)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("a refused save wrote %s: %v", path, err)
			}
		})
	}
	t.Run("not when Go refuses", func(t *testing.T) {
		service := workflowTestService(t, gitRepository(t), &workflowRuntime{})
		policy := withPolicy(t, service, extension.PointAgentSave, policyAllows)
		undeclared := reviewer
		undeclared.Secrets = []string{"NOT_DECLARED"}
		if _, err := service.SaveAgent("project", AgentScopeRepository, "", encode(t, undeclared)); err == nil || !strings.Contains(err.Error(), "not declared in inkuna.yaml") {
			t.Fatalf("SaveAgent = %v, want Go's own refusal", err)
		}
		if asked := policy.decisions(extension.PointAgentSave); len(asked) != 0 {
			t.Fatalf("a policy was asked about an agent Go refused: %+v", asked)
		}
	})
}

func TestSavingTheConfigurationAsksThePoliciesOnceItIsValid(t *testing.T) {
	encode := func(t *testing.T, file config.File) string {
		data, err := json.Marshal(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, tc := range policyModes {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvDataDir, t.TempDir())
			service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
			path := service.config.ConfigPath
			if path == "" {
				path = config.DefaultPath(service.dataDir)
			}
			before, readErr := os.ReadFile(path)
			file := service.config.File
			file.Timeouts.Worker = "20m"
			policy := withPolicy(t, service, extension.PointConfigSave, tc.mode)
			_, err := service.SaveConfiguration(encode(t, file))
			after, afterErr := os.ReadFile(path)
			if tc.refusal == "" {
				if err != nil || afterErr != nil {
					t.Fatal(errors.Join(err, afterErr))
				}
				decision := onlyDecision(t, policy, extension.PointConfigSave, "", "user")
				wantDetails(t, decision, map[string]string{"path": path, "digest": contentDigest(after)})
				return
			}
			if err == nil || err.Error() != tc.refusal {
				t.Fatalf("SaveConfiguration = %v, want %q", err, tc.refusal)
			}
			if !reflect.DeepEqual(before, after) || (readErr == nil) != (afterErr == nil) {
				t.Fatalf("a refused save changed %s", path)
			}
			if service.config.File.Timeouts.Worker == "20m" {
				t.Fatal("a refused save was put in effect")
			}
		})
	}
	t.Run("not when Go refuses", func(t *testing.T) {
		t.Setenv(config.EnvDataDir, t.TempDir())
		service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
		policy := withPolicy(t, service, extension.PointConfigSave, policyAllows)
		file := service.config.File
		file.Timeouts.Worker = "soon"
		if _, err := service.SaveConfiguration(encode(t, file)); err == nil || strings.Contains(err.Error(), "policy") {
			t.Fatalf("SaveConfiguration = %v, want Go's own refusal", err)
		}
		if asked := policy.decisions(extension.PointConfigSave); len(asked) != 0 {
			t.Fatalf("a policy was asked about a configuration Go refused: %+v", asked)
		}
	})
}

// lateAllow allows only once its deadline has passed, so its answer and the
// deadline are ready together.
type lateAllow struct{}

func (lateAllow) Check(ctx context.Context, _ extension.Decision) (extension.Verdict, error) {
	<-ctx.Done()
	return extension.Verdict{}, nil
}

func TestAnAllowThatComesAtTheDeadlineIsRefused(t *testing.T) {
	previous := policyTimeout
	policyTimeout = time.Millisecond
	t.Cleanup(func() { policyTimeout = previous })
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", policies: []extension.Policy{lateAllow{}}}})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{extensions: registry}
	// Either case of the select may be taken when both are ready; every
	// time, the late answer must refuse.
	for range 50 {
		if err := service.decide(context.Background(), extension.Decision{Point: extension.PointConfigSave}, &ada); err == nil || !strings.Contains(err.Error(), "did not decide within") {
			t.Fatalf("a late allow = %v, want it refused at the deadline", err)
		}
	}
}

// The person who allowed a permission is who the policies are asked about,
// even when the provider would name someone else by the time they are.
func TestAPermissionIsDecidedAsThePersonWhoAllowedIt(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	ctx := context.Background()
	if err := service.store.SaveExecution(ctx, storage.Execution{ID: "run-1", WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: "running", Title: "Run"}); err != nil {
		t.Fatal(err)
	}
	policy := withPolicy(t, service, extension.PointPermissionGrant, policyAllows)
	grace := extension.Principal{ID: "grace-91c2", Name: "Grace Hopper"}
	// The provider now names someone else.
	service.extensions.identity = &fakeIdentity{principal: grace}
	assignment := &workerAssignment{executionID: "run-1", step: spec.Steps[0], definition: agent.Definition{Name: "researcher"}}
	request := &permissionRequest{grants: []domain.PermissionGrant{{Command: "golangci-lint run"}}, inputID: "input-1"}

	service.keepAnswerer("input-1", ada)
	if err := service.decidePermission(ctx, assignment, request); err != nil {
		t.Fatal(err)
	}
	decision := onlyDecision(t, policy, extension.PointPermissionGrant, "project", "user")
	wantDetails(t, decision, map[string]string{"run": "run-1", "commands": "golangci-lint run"})

	// Asked again, the answer is gone: nothing says who allowed it.
	if err := service.decidePermission(ctx, assignment, request); err == nil || !strings.Contains(err.Error(), "does not know who allowed this") {
		t.Fatalf("an answer with no record = %v, want it refused", err)
	}
	// A run that cannot be read refuses rather than ask without a project.
	service.keepAnswerer("input-2", ada)
	missing := &workerAssignment{executionID: "run-gone", step: spec.Steps[0], definition: agent.Definition{Name: "researcher"}}
	if err := service.decidePermission(ctx, missing, &permissionRequest{inputID: "input-2"}); err == nil || !strings.Contains(err.Error(), "read the run to ask its policies") {
		t.Fatalf("a run that cannot be read = %v, want it refused", err)
	}
	if asked := policy.decisions(extension.PointPermissionGrant); len(asked) != 1 {
		t.Fatalf("the policy was asked %d times, want once", len(asked))
	}
}

func TestATrackerWriteWhoseProjectCannotBeLookedUpIsRefused(t *testing.T) {
	repository := gitRepository(t)
	service := workflowTestService(t, repository, &workflowRuntime{})
	policy := withPolicy(t, service, extension.PointTrackerWrite, policyAllows)
	write := domain.TrackerWrite{Tracker: "github", Action: "comment", Kind: domain.TrackerComment, Issue: "12"}
	ctx := context.Background()
	if err := service.trackerPolicy(repository)(ctx, write); err != nil {
		t.Fatal(err)
	}
	if err := service.trackerPolicy(t.TempDir())(ctx, write); err != nil {
		t.Fatal(err)
	}
	decisions := policy.decisions(extension.PointTrackerWrite)
	if len(decisions) != 2 || decisions[0].Project != "project" || decisions[1].Project != "" {
		t.Fatalf("a project's checkout names it and any other names none: %+v", decisions)
	}
	if err := service.store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.trackerPolicy(repository)(ctx, write); err == nil || !strings.Contains(err.Error(), "find the project to ask its policies") {
		t.Fatalf("a lookup that fails = %v, want the write refused", err)
	}
	if len(policy.decisions(extension.PointTrackerWrite)) != 2 {
		t.Fatal("a policy was asked about a write whose project could not be looked up")
	}
}

// A user's server the runtime no longer offers is not given, so the policy is
// asked only about the servers the step will have. Approval refuses a plan
// whose userMcp changed, so this is a step that starts after it: the second
// of two, once the server was taken off userMcp while the first ran.
func TestAnMCPGrantNamesOnlyTheUsersServersTheStepWillHave(t *testing.T) {
	first, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	runtime := &userServerRuntime{workflowRuntime: &workflowRuntime{handle: func(ctx context.Context, _ agentruntime.RunRequest) (agentruntime.RunResult, error) {
		blocked := false
		once.Do(func() { blocked = true })
		if blocked {
			close(first)
			select {
			case <-release:
			case <-ctx.Done():
				return agentruntime.RunResult{}, ctx.Err()
			}
		}
		return agentruntime.RunResult{}, nil
	}}, names: []string{"notes"}, statuses: map[string]agentruntime.UserServer{
		"notes": {Name: "notes", Source: "config", Transport: "stdio", Command: "notes-mcp", Status: agentruntime.UserServerReady},
	}}
	service := userServerService(t, runtime)
	task := domain.TaskSpec{
		Version: domain.ContractVersion, ID: "task", Kind: domain.TaskResearch, Objective: "Inspect the repository",
		Source: domain.TaskSource{Kind: "request"}, Repository: ".", Priority: 1, Budget: domain.Budget{WallTimeSeconds: 60},
	}
	if err := service.store.SaveTask(context.Background(), "project", "planned", task); err != nil {
		t.Fatal(err)
	}
	step := func(id string, dependsOn ...string) domain.WorkflowStep {
		return domain.WorkflowStep{
			ID: id, Title: id, Kind: domain.StepAgent, Runtime: domain.RuntimeCodex, Permission: domain.PermissionReadOnly, Agent: "researcher", DependsOn: dependsOn,
			Complexity: &domain.ComplexityAssessment{Score: 4, Rationale: "read-only inspection"}, Budget: domain.Budget{WallTimeSeconds: 60, MaxOutputBytes: 1024},
		}
	}
	spec := domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeCodex,
		RoutingPolicyVersion: service.routingPolicy().Version, MaxConcurrency: 1, CreatedAt: time.Now().UTC(), UserMCP: service.userMCPByRuntime(),
		Steps: []domain.WorkflowStep{step("first"), step("second", "first")},
	}
	applyUserServers(&spec, spec.UserMCP)
	spec = savePlanned(t, service, spec)
	policy := withPolicy(t, service, extension.PointMCPGrant, policyAllows)
	run := startSpec(t, service, spec)
	<-first
	runtime.signMu.Lock()
	runtime.names = nil
	runtime.signMu.Unlock()
	close(release)
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	decision := onlyDecision(t, policy, extension.PointMCPGrant, "project", "researcher")
	wantDetails(t, decision, map[string]string{"step": "first", "userServers": "notes", "servers": ""})
	if !runEventsSay(completed, `Your MCP server "notes" is no longer in runtime codex's userMcp`) {
		t.Fatalf("the second step must say it went without the server: %+v", completed.Events)
	}
}

// A second approval of a workflow with a run going is Go's to refuse, and
// its reason is not hidden behind a policy's.
func TestASecondStartIsRefusedBeforeThePoliciesAreAsked(t *testing.T) {
	release := make(chan struct{})
	runtime := &workflowRuntime{handle: func(ctx context.Context, _ agentruntime.RunRequest) (agentruntime.RunResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return agentruntime.RunResult{}, ctx.Err()
		}
		return agentruntime.RunResult{}, nil
	}}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	policy := withPolicy(t, service, extension.PointPlanApprove, policyAllows)
	run := startSpec(t, service, spec)
	defer func() {
		close(release)
		waitForWorkflowRun(t, service, run.ID, "completed")
	}()
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "already has an active run") {
		t.Fatalf("a second start = %v, want Go's own refusal", err)
	}
	if asked := policy.decisions(extension.PointPlanApprove); len(asked) != 1 {
		t.Fatalf("the policy was asked %d times, want once, for the first start", len(asked))
	}
}

// blockingPolicy holds its first decision at point until released, saying
// when it has it, and counts the decisions it is asked about there.
type blockingPolicy struct {
	point            string
	entered, release chan struct{}
	once             sync.Once
	asked            atomic.Int32
}

func (policy *blockingPolicy) Check(_ context.Context, decision extension.Decision) (extension.Verdict, error) {
	if decision.Point == policy.point {
		policy.asked.Add(1)
		policy.once.Do(func() {
			close(policy.entered)
			<-policy.release
		})
	}
	return extension.Verdict{}, nil
}

// Two saves of a new agent under one name cannot both find it free while
// the first waits for its policies: the second is refused, and the first
// one's file is kept.
func TestTwoNewAgentsOfOneNameCannotBothBeSaved(t *testing.T) {
	repository := gitRepository(t)
	service := workflowTestService(t, repository, &workflowRuntime{})
	policy := &blockingPolicy{point: extension.PointAgentSave, entered: make(chan struct{}), release: make(chan struct{})}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", policies: []extension.Policy{policy}}})
	if err != nil {
		t.Fatal(err)
	}
	service.extensions = registry
	encode := func(instructions string) string {
		data, err := json.Marshal(agent.Definition{Name: "security-reviewer", Description: "Reviews", Permission: domain.PermissionReadOnly, Instructions: instructions})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	first := make(chan error, 1)
	go func() {
		_, err := service.SaveAgent("project", AgentScopeRepository, "", encode("The first."))
		first <- err
	}()
	<-policy.entered
	second := make(chan error, 1)
	go func() {
		_, err := service.SaveAgent("project", AgentScopeRepository, "", encode("The second."))
		second <- err
	}()
	// The second save is waiting for the first; give it time to get there.
	time.Sleep(20 * time.Millisecond)
	close(policy.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err == nil || !strings.Contains(err.Error(), "already has a definition file") {
		t.Fatalf("the second save = %v, want it refused", err)
	}
	written, err := os.ReadFile(filepath.Join(repository, ".inkuna", "agents", "security-reviewer.md"))
	if err != nil || !strings.Contains(string(written), "The first.") {
		t.Fatalf("the first save's file = %q, %v", written, err)
	}
}

// Two approvals of one workflow at once: the second is refused while the
// first waits for its policies, which are asked once.
func TestTwoApprovalsOfOneWorkflowAskThePoliciesOnce(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	policy := &blockingPolicy{point: extension.PointPlanApprove, entered: make(chan struct{}), release: make(chan struct{})}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", policies: []extension.Policy{policy}}})
	if err != nil {
		t.Fatal(err)
	}
	service.extensions = registry
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan WorkflowRunView, 1)
	go func() {
		run, err := service.StartWorkflow(string(encoded))
		if err != nil {
			t.Error(err)
		}
		first <- run
	}()
	<-policy.entered
	if _, err := service.StartWorkflow(string(encoded)); err == nil || !strings.Contains(err.Error(), "is already being started") {
		t.Fatalf("a second approval = %v, want Go's own refusal", err)
	}
	close(policy.release)
	run := <-first
	waitForWorkflowRun(t, service, run.ID, "completed")
	if asked := policy.asked.Load(); asked != 1 {
		t.Fatalf("the policy was asked %d times, want once", asked)
	}
	// Released once the run was registered, so the workflow can be started
	// again now that it has ended.
	release, err := service.reserveStart(spec.ID)
	if err != nil {
		t.Fatalf("the workflow was left reserved: %v", err)
	}
	release()
}

// ignoringPolicy ignores its context and answers only once released.
type ignoringPolicy struct{ release chan struct{} }

func (policy ignoringPolicy) Check(context.Context, extension.Decision) (extension.Verdict, error) {
	<-policy.release
	return extension.Verdict{}, nil
}

// A policy that ignores its context keeps each abandoned check running, so it
// may have only so many: past them a decision is refused at once, and once
// they return it is asked again.
func TestAPolicyWithTooManyChecksRunningRefusesAtOnce(t *testing.T) {
	previousTimeout, previousInFlight := policyTimeout, policyInFlight
	policyTimeout, policyInFlight = 5*time.Millisecond, 2
	t.Cleanup(func() { policyTimeout, policyInFlight = previousTimeout, previousInFlight })
	policy := ignoringPolicy{release: make(chan struct{})}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", policies: []extension.Policy{policy}}})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{extensions: registry}
	decide := func() error {
		return service.decide(context.Background(), extension.Decision{Point: extension.PointConfigSave}, &ada)
	}
	for range 2 {
		if err := decide(); err == nil || !strings.Contains(err.Error(), "did not decide within") {
			t.Fatalf("a check that is not answered = %v, want it refused at the deadline", err)
		}
	}
	started := time.Now()
	if err := decide(); err == nil || err.Error() != `policy "acme/policy" is still deciding 2 earlier requests, so this is refused; try again` {
		t.Fatalf("a third check = %v, want it refused at once", err)
	}
	if waited := time.Since(started); waited >= policyTimeout {
		t.Fatalf("a check past the limit waited %s", waited)
	}
	close(policy.release)
	eventually(t, "the abandoned checks return", func() bool { return len(registry.policies[0].running) == 0 })
	if err := decide(); err != nil {
		t.Fatalf("once its checks returned, the policy = %v", err)
	}
}

func answererCount(service *Service) int {
	service.answerersMu.Lock()
	defer service.answerersMu.Unlock()
	return len(service.answerers)
}

// badError is a policy's error whose message blocks until released, or
// panics.
type badError struct {
	block  chan struct{}
	panics bool
}

func (err badError) Error() string {
	if err.panics {
		panic("the error's own bug")
	}
	<-err.block
	return "late"
}

type failingPolicy struct{ err error }

func (policy failingPolicy) Check(context.Context, extension.Decision) (extension.Verdict, error) {
	return extension.Verdict{}, policy.err
}

// A policy's error is rendered where the deadline and recovery apply, so an
// error whose message blocks or panics refuses, and never hangs or crashes
// the decision.
func TestAPolicysErrorThatBlocksOrPanicsRefuses(t *testing.T) {
	previous := policyTimeout
	policyTimeout = 20 * time.Millisecond
	t.Cleanup(func() { policyTimeout = previous })
	block := make(chan struct{})
	defer close(block)
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"blocks", badError{block: block}, `policy "acme/policy" did not decide within 20ms, so this is refused; try again`},
		{"panics", badError{panics: true}, `policy "acme/policy" failed while deciding, so this is refused`},
		{"says nothing", errors.New("  "), `policy "acme/policy" could not decide, so this is refused`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", policies: []extension.Policy{failingPolicy{tc.err}}}})
			if err != nil {
				t.Fatal(err)
			}
			service := &Service{extensions: registry}
			if err := service.decide(context.Background(), extension.Decision{Point: extension.PointConfigSave}, &ada); err == nil || err.Error() != tc.want {
				t.Fatalf("decide = %v, want %q", err, tc.want)
			}
		})
	}
}

// A check that has answered frees its slot before its answer arrives, so
// decisions one after another never find a policy busy.
func TestAnAnsweredCheckFreesItsSlotFirst(t *testing.T) {
	previous := policyInFlight
	policyInFlight = 1
	t.Cleanup(func() { policyInFlight = previous })
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", policies: []extension.Policy{orderedPolicy{asked: new([]extension.Decision)}}}})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{extensions: registry}
	for range 500 {
		if err := service.decide(context.Background(), extension.Decision{Point: extension.PointConfigSave, Details: map[string]string{"rule": "x"}}, &ada); err != nil {
			t.Fatalf("a decision after one that was answered = %v", err)
		}
	}
}

// exitingPolicy ends its goroutine without returning, as t.FailNow does.
type exitingPolicy struct{}

func (exitingPolicy) Check(context.Context, extension.Decision) (extension.Verdict, error) {
	runtime.Goexit()
	return extension.Verdict{}, nil
}

// A check that never returns a verdict refuses, even when it ends without a
// panic that recover could see.
func TestAPolicyThatEndsItsGoroutineRefuses(t *testing.T) {
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme", policies: []extension.Policy{exitingPolicy{}}}})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{extensions: registry}
	if err := service.decide(context.Background(), extension.Decision{Point: extension.PointConfigSave}, &ada); err == nil || err.Error() != `policy "acme/policy" failed while deciding, so this is refused` {
		t.Fatalf("decide = %v, want it refused", err)
	}
	if running := len(registry.policies[0].running); running != 0 {
		t.Fatalf("the check that ended still holds %d slots", running)
	}
}
