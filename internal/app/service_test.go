package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

type serviceRoundTripFunc func(*http.Request) (*http.Response, error)

func (function serviceRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type serviceGitHubRunner struct {
	output []byte
	err    error
}

type serviceClaudeRunner struct {
	outputs [][]byte
	errors  []error
	calls   int
}

func (runner *serviceClaudeRunner) Run(context.Context, ...string) ([]byte, error) {
	index := runner.calls
	runner.calls++
	return runner.outputs[index], runner.errors[index]
}

func (runner serviceGitHubRunner) Run(context.Context, ...string) ([]byte, error) {
	return runner.output, runner.err
}

func TestSuggestedKindFromLinearLabels(t *testing.T) {
	for _, test := range []struct {
		labels   []string
		expected domain.TaskKind
	}{
		{[]string{"Feature", "Bug"}, domain.TaskBugfix},
		{[]string{"Docs"}, domain.TaskDocumentation},
		{[]string{"Spike"}, domain.TaskResearch},
		{[]string{"Feature"}, domain.TaskImplementation},
	} {
		if actual := suggestedKind(test.labels); actual != test.expected {
			t.Fatalf("suggestedKind(%v) = %s, want %s", test.labels, actual, test.expected)
		}
	}
}

func TestAuthenticateLinearKeepsVerifiedKeyInMemory(t *testing.T) {
	httpClient := &http.Client{Transport: serviceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "session-secret" {
			t.Error("linear credential was not sent")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":{"viewer":{"id":"viewer","name":"Ada","email":"ada@example.com"}}}`))}, nil
	})}
	service := &Service{
		config: config.Config{LinearEndpoint: "https://linear.test/graphql", LinearTimeout: time.Second},
		http:   httpClient,
	}
	view, err := service.AuthenticateLinear("session-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Authenticated || view.Account != "Ada" || strings.Contains(fmt.Sprintf("%#v", view), "session-secret") {
		t.Fatalf("unexpected authentication view: %#v", view)
	}
	if service.linearCredential() != "session-secret" {
		t.Fatal("verified key was not retained for the session")
	}
	service.ForgetLinearAuthentication()
	if service.linearCredential() != "" {
		t.Fatal("linear credential was not forgotten")
	}
}

func TestAuthenticateLinearRejectsKeyWithoutRetainingIt(t *testing.T) {
	httpClient := &http.Client{Transport: serviceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"errors":[{"message":"Unauthorized"}]}`))}, nil
	})}
	service := &Service{
		config: config.Config{LinearEndpoint: "https://linear.test/graphql", LinearTimeout: time.Second},
		http:   httpClient,
	}
	if _, err := service.AuthenticateLinear("invalid-secret"); err == nil {
		t.Fatal("invalid linear credential was accepted")
	}
	if service.linearCredential() != "" {
		t.Fatal("invalid linear credential was retained")
	}
}

func TestAuthenticateLinearDoesNotRestoreForgottenCredential(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	service := &Service{
		config: config.Config{LinearEndpoint: "https://linear.test/graphql", LinearTimeout: time.Second},
		http: &http.Client{Transport: serviceRoundTripFunc(func(*http.Request) (*http.Response, error) {
			close(started)
			<-release
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":{"viewer":{"id":"viewer","name":"Ada"}}}`))}, nil
		})},
	}
	result := make(chan error, 1)
	go func() {
		_, err := service.AuthenticateLinear("session-secret")
		result <- err
	}()
	<-started
	service.ForgetLinearAuthentication()
	close(release)
	if err := <-result; err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("stale authentication error = %v", err)
	}
	key, identity, _ := service.linearCredentials()
	if key != "" || identity != "" {
		t.Fatalf("forgotten authentication was restored: key=%q identity=%q", key, identity)
	}
}

func TestLinearStatusDoesNotCacheIdentityAfterKeyReplacement(t *testing.T) {
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	service := &Service{
		config:    config.Config{LinearEndpoint: "https://linear.test/graphql", LinearTimeout: time.Second},
		linearKey: "old-secret",
		http: &http.Client{Transport: serviceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			name := "New User"
			if request.Header.Get("Authorization") == "old-secret" {
				close(oldStarted)
				<-releaseOld
				name = "Old User"
			}
			body := fmt.Sprintf(`{"data":{"viewer":{"id":"viewer","name":%q}}}`, name)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	}
	statusDone := make(chan ProviderAuthenticationView, 1)
	go func() { statusDone <- service.linearAuthenticationStatus() }()
	<-oldStarted
	if _, err := service.AuthenticateLinear("new-secret"); err != nil {
		t.Fatal(err)
	}
	close(releaseOld)
	<-statusDone
	key, identity, _ := service.linearCredentials()
	if key != "new-secret" || identity != "New User" {
		t.Fatalf("stale status replaced current identity: key=%q identity=%q", key, identity)
	}
}

func TestGitHubAuthenticationStatusDoesNotExposeCredentials(t *testing.T) {
	service := &Service{
		config: config.Config{HealthTimeout: time.Second},
		github: serviceGitHubRunner{output: []byte(`{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"octocat","gitProtocol":"ssh","token":"secret"}]}}`)},
	}
	view := service.githubAuthenticationStatus()
	if !view.Authenticated || view.Account != "octocat" || strings.Contains(fmt.Sprintf("%#v", view), "secret") {
		t.Fatalf("unexpected authentication view: %#v", view)
	}
}

func TestClaudeAuthenticationStatusReportsLoginState(t *testing.T) {
	runner := &serviceClaudeRunner{outputs: [][]byte{[]byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}`)}, errors: []error{nil}}
	service := &Service{config: config.Config{HealthTimeout: time.Second}, claudeAuth: runner}
	view := service.claudeAuthenticationStatus()
	if !view.Authenticated || view.Provider != "claude" || !strings.Contains(view.Detail, "claude.ai") {
		t.Fatalf("unexpected Claude authentication view: %#v", view)
	}
}

func TestAuthenticateClaudeUsesProviderOwnedLogin(t *testing.T) {
	runner := &serviceClaudeRunner{
		outputs: [][]byte{nil, []byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}`)},
		errors:  []error{nil, nil},
	}
	service := &Service{config: config.Config{AuthTimeout: time.Second}, claudeAuth: runner}
	view, err := service.AuthenticateClaude()
	if err != nil || !view.Authenticated || runner.calls != 2 {
		t.Fatalf("authenticate Claude: view=%#v calls=%d err=%v", view, runner.calls, err)
	}
}
