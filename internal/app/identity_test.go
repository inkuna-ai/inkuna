package app

import (
	"context"
	"encoding/json"
	"errors"
	"os/user"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// fakeIdentity is an identity provider whose answer a test can change.
type fakeIdentity struct {
	mu        sync.Mutex
	principal extension.Principal
	err       error
	// block, when set, holds Current until the test closes it.
	block chan struct{}
	calls int
}

func (provider *fakeIdentity) Current(ctx context.Context) (extension.Principal, error) {
	provider.mu.Lock()
	provider.calls++
	block := provider.block
	provider.mu.Unlock()
	if block != nil {
		<-block
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.principal, provider.err
}

func (provider *fakeIdentity) set(principal extension.Principal, err error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.principal, provider.err = principal, err
}

// withIdentity registers provider, as the build's only identity provider,
// on a service built without New.
func withIdentity(t *testing.T, service *Service, provider extension.IdentityProvider) {
	t.Helper()
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "acme-sso", identities: []extension.IdentityProvider{provider}}})
	if err != nil {
		t.Fatal(err)
	}
	service.extensions = registry
}

var ada = extension.Principal{ID: "ada-7f3e", Name: "Ada Lovelace", Email: "ada@example.com", Groups: []string{"release-managers"}}

func TestWithoutAProviderThePrincipalIsTheOSUser(t *testing.T) {
	got, err := (&Service{}).principal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := extension.Principal{}
	if current, err := user.Current(); err == nil {
		want = extension.Principal{ID: current.Username, Name: current.Name}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("principal() = %+v, want the OS user %+v with no groups", got, want)
	}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{&fakeExtension{name: "audit"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := (&Service{extensions: registry}).principal(context.Background()); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("an extension without a provider leaves the OS user: %+v %v", got, err)
	}
}

func TestAProviderNamesThePrincipal(t *testing.T) {
	// The provider holds its own copy, so changing its slice below leaves
	// the ada other tests share alone.
	own := ada
	own.Groups = slices.Clone(ada.Groups)
	provider := &fakeIdentity{principal: own}
	service := &Service{}
	withIdentity(t, service, provider)
	got, err := service.principal(context.Background())
	if err != nil || !reflect.DeepEqual(got, ada) {
		t.Fatalf("principal() = %+v, %v; want %+v", got, err, ada)
	}
	// What Inkuna holds is its own copy.
	provider.principal.Groups[0] = "someone-else"
	if got.Groups[0] != "release-managers" {
		t.Fatalf("the provider's slice changed the principal Inkuna holds: %v", got.Groups)
	}
}

func TestAProviderThatCannotNameSomeoneRefuses(t *testing.T) {
	cases := []struct {
		name      string
		principal extension.Principal
		err       error
		want      string
	}{
		{"an error", ada, errors.New("Sign in to continue"), `identity provider "acme-sso": Sign in to continue`},
		{"no ID", extension.Principal{Name: "Ada"}, nil, `identity provider "acme-sso" named no one: its principal has no ID`},
		{"a blank ID", extension.Principal{ID: "  "}, nil, `identity provider "acme-sso" named no one: its principal has no ID`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{}
			withIdentity(t, service, &fakeIdentity{principal: tc.principal, err: tc.err})
			if _, err := service.principal(context.Background()); err == nil || err.Error() != tc.want {
				t.Fatalf("principal() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestASlowProviderRefusesAtItsDeadline(t *testing.T) {
	previous := identityTimeout
	identityTimeout = 20 * time.Millisecond
	t.Cleanup(func() { identityTimeout = previous })
	block := make(chan struct{})
	defer close(block)
	service := &Service{}
	withIdentity(t, service, &fakeIdentity{principal: ada, block: block})
	if _, err := service.principal(context.Background()); err == nil || !strings.Contains(err.Error(), `identity provider "acme-sso" did not say who is acting within 20ms`) {
		t.Fatalf("principal() = %v, want the deadline refused", err)
	}
}

func TestABuildHasAtMostOneIdentityProvider(t *testing.T) {
	one, two := &fakeIdentity{principal: ada}, &fakeIdentity{principal: ada}
	cases := []struct {
		name       string
		extensions []extension.Extension
		want       string
	}{
		{"a nil provider", []extension.Extension{&fakeExtension{name: "acme-sso", identities: []extension.IdentityProvider{nil}}},
			`register extension "acme-sso": its identity provider is nil`},
		{"two from one extension", []extension.Extension{&fakeExtension{name: "acme-sso", identities: []extension.IdentityProvider{one, two}}},
			`register extension "acme-sso": it registers more than one identity provider`},
		{"one from each of two extensions", []extension.Extension{
			&fakeExtension{name: "acme-sso", identities: []extension.IdentityProvider{one}},
			&fakeExtension{name: "audit"},
			&fakeExtension{name: "other-sso", identities: []extension.IdentityProvider{two}},
		}, `register extension "other-sso": extension "acme-sso" already registers an identity provider; a build has at most one`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := registerExtensions(extension.Product{}, tc.extensions); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("registerExtensions: %v, want an error containing %q", err, tc.want)
			}
		})
	}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{
		&fakeExtension{name: "audit"}, &fakeExtension{name: "acme-sso", identities: []extension.IdentityProvider{one}},
	})
	if err != nil || registry.identity != one || registry.identityFrom != "acme-sso" {
		t.Fatalf("one provider is registered: %+v %v", registry, err)
	}
}

// The task records who planned it and the run who approved it, and neither
// reaches a model: not the orchestrator planning the task, nor the worker.
func TestTheTaskAndRunRecordWhoActedAndNoPromptNamesThem(t *testing.T) {
	repositoryPath := t.TempDir()
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	runtime := &workflowRuntime{}
	service := workflowTestService(t, repositoryPath, runtime)
	// The orchestrator's calls get no usable answer and planning falls
	// back, but their prompts were sent, and are checked below.
	service.config.Orchestrator.Runtime = domain.RuntimeCodex
	provider := &fakeIdentity{principal: ada}
	withIdentity(t, service, provider)
	plan, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository", Kind: domain.TaskResearch})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if creator, err := service.store.TaskCreator(ctx, plan.Task.ID); err != nil || creator != ada.ID {
		t.Fatalf("the task's creator = %q, %v; want %q", creator, err, ada.ID)
	}
	// Someone else approves the plan.
	grace := extension.Principal{ID: "grace-91c2", Name: "Grace Hopper", Email: "grace@example.com"}
	provider.set(grace, nil)
	encoded, err := json.Marshal(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	if stored, err := service.store.Execution(ctx, run.ID); err != nil || stored.ApprovedBy != grace.ID {
		t.Fatalf("the run's approver = %q, %v; want %q", stored.ApprovedBy, err, grace.ID)
	}
	if creator, err := service.store.TaskCreator(ctx, plan.Task.ID); err != nil || creator != ada.ID {
		t.Fatalf("approving keeps the task's creator: %q %v", creator, err)
	}
	requests := started(runtime)
	if len(requests) < 2 {
		t.Fatalf("want the orchestrator's calls and the worker's, got %d requests", len(requests))
	}
	for _, request := range requests {
		for _, person := range []extension.Principal{ada, grace} {
			for _, value := range []string{person.ID, person.Name, person.Email} {
				if strings.Contains(string(request.Prompt), value) {
					t.Fatalf("the prompt of %s names %q:\n%s", request.RunID, value, request.Prompt)
				}
			}
		}
	}
}

func TestAFailingProviderRefusesPlanningAndApprovingAndChangesNothing(t *testing.T) {
	repositoryPath := t.TempDir()
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	runtime := &workflowRuntime{}
	service := workflowTestService(t, repositoryPath, runtime)
	spec := workflowTestSpec(t, service)
	provider := &fakeIdentity{err: errors.New("Sign in to continue")}
	withIdentity(t, service, provider)
	const reason = `identity provider "acme-sso": Sign in to continue`
	if _, err := service.PlanWorkflow(WorkflowRequestView{ProjectID: "project", Objective: "Inspect the repository"}); err == nil || err.Error() != reason {
		t.Fatalf("PlanWorkflow: %v, want %q", err, reason)
	}
	ids, err := service.store.ProjectTaskIDs(context.Background(), "project")
	if err != nil || len(ids) != 1 {
		t.Fatalf("a refused plan creates no task: %v %v", ids, err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWorkflow(string(encoded)); err == nil || err.Error() != reason {
		t.Fatalf("StartWorkflow: %v, want %q", err, reason)
	}
	if runs, err := service.store.Executions(context.Background(), 10); err != nil || len(runs) != 0 {
		t.Fatalf("a refused approval starts no run: %+v %v", runs, err)
	}
	if state, err := service.store.WorkflowState(context.Background(), spec.ID); err != nil || state != "planned" {
		t.Fatalf("a refused approval leaves the plan planned: %q %v", state, err)
	}
	if len(started(runtime)) != 0 {
		t.Fatalf("a refused decision reached a model: %d requests", len(started(runtime)))
	}
}

// Every other decision a person makes on a run is refused before it reads
// the run, so the provider's reason is all the person sees, and the
// question they answered is still waiting.
func TestAFailingProviderRefusesEveryDecisionOnARun(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	ctx := context.Background()
	if err := service.store.SaveExecution(ctx, storage.Execution{ID: "run-1", WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: storage.Reviewing, Title: "Run"}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []storage.InputRequest{
		{ID: "question", ExecutionID: "run-1", StepID: "worker", Agent: "human", Question: "Which database?", State: storage.InputPending},
		{ID: "review", ExecutionID: "run-1", StepID: "worker", Agent: "human", Question: "Approve?", State: storage.InputPending, Review: true, ReviewTarget: "worker"},
	} {
		if err := service.store.SaveInput(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	withIdentity(t, service, &fakeIdentity{err: errors.New("Sign in to continue")})
	const reason = `identity provider "acme-sso": Sign in to continue`
	decisions := map[string]func() error{
		"approving a review": func() error { _, err := service.ApproveReview("run-1"); return err },
		"sending changes": func() error {
			_, err := service.RequestChanges("run-1", FeedbackRequest{Round: 1, Items: []FeedbackRequestItem{{Target: "worker", Text: "More."}}})
			return err
		},
		"answering a review":   func() error { _, err := service.AnswerInput("review", "More detail."); return err },
		"answering a question": func() error { _, err := service.AnswerInput("question", "sqlite"); return err },
		"resuming":             func() error { _, err := service.ResumeWorkflowRun("run-1", ResumeRequest{Round: 1}); return err },
	}
	for name, decide := range decisions {
		if err := decide(); err == nil || err.Error() != reason {
			t.Errorf("%s: %v, want %q", name, err, reason)
		}
	}
	for _, id := range []string{"question", "review"} {
		if input, err := service.store.Input(ctx, id); err != nil || input.State != storage.InputPending {
			t.Errorf("input %s is %q after a refusal, %v; want it still pending", id, input.State, err)
		}
	}
	if run, err := service.store.Execution(ctx, "run-1"); err != nil || run.State != storage.Reviewing {
		t.Errorf("the run is %q after the refusals, %v; want it still reviewing", run.State, err)
	}
}

// A run that started goes on when the provider later fails: the principal
// is asked for decisions, never while a run works.
func TestARunAlreadyStartedGoesOnWhenTheProviderFails(t *testing.T) {
	release := make(chan struct{})
	runtime := &workflowRuntime{handle: func(ctx context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return agentruntime.RunResult{}, ctx.Err()
		}
		return agentruntime.RunResult{}, nil
	}}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	provider := &fakeIdentity{principal: ada}
	withIdentity(t, service, provider)
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	provider.set(extension.Principal{}, errors.New("Sign in to continue"))
	close(release)
	waitForWorkflowRun(t, service, run.ID, "completed")
	if stored, err := service.store.Execution(context.Background(), run.ID); err != nil || stored.ApprovedBy != ada.ID {
		t.Fatalf("the run keeps the approver it started with: %q %v", stored.ApprovedBy, err)
	}
}
