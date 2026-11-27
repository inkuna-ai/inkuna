package github

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type authRunner struct {
	responses [][]byte
	errors    []error
	calls     [][]string
}

func (runner *authRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, append([]string(nil), args...))
	index := len(runner.calls) - 1
	return runner.responses[index], runner.errors[index]
}

func TestAuthenticationStatusReturnsActiveAccount(t *testing.T) {
	runner := &authRunner{
		responses: [][]byte{[]byte(`{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"octocat","gitProtocol":"ssh"}]}}`)},
		errors:    []error{nil},
	}
	status, err := AuthenticationStatus(context.Background(), runner, DefaultHost)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Authenticated || status.Account != "octocat" || status.GitProtocol != "ssh" {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestAuthenticateUsesWebFlowWhenDisconnected(t *testing.T) {
	disconnected := []byte(`{"hosts":{"github.com":[]}}`)
	connected := []byte(`{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"octocat","gitProtocol":"ssh"}]}}`)
	runner := &authRunner{
		responses: [][]byte{disconnected, nil, connected},
		errors:    []error{nil, nil, nil},
	}
	status, err := Authenticate(context.Background(), runner, DefaultHost, "ssh")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Authenticated || len(runner.calls) != 3 {
		t.Fatalf("unexpected authentication result: %#v calls=%v", status, runner.calls)
	}
	wantLogin := []string{"auth", "login", "--hostname", DefaultHost, "--git-protocol", "ssh", "--skip-ssh-key", "--web", "--clipboard"}
	if !reflect.DeepEqual(runner.calls[1], wantLogin) {
		t.Fatalf("login args = %v, want %v", runner.calls[1], wantLogin)
	}
}

func TestAuthenticateDoesNotRetryFailedLogin(t *testing.T) {
	runner := &authRunner{
		responses: [][]byte{[]byte(`{"hosts":{"github.com":[]}}`), nil},
		errors:    []error{nil, errors.New("login failed")},
	}
	if _, err := Authenticate(context.Background(), runner, DefaultHost, "https"); err == nil {
		t.Fatal("failed login was accepted")
	}
}

func TestAuthenticateRejectsNilRunner(t *testing.T) {
	if _, err := Authenticate(context.Background(), nil, DefaultHost, "https"); err == nil || err.Error() != "github CLI runner is required" {
		t.Fatalf("nil runner error = %v", err)
	}
}

func TestAuthenticationStatusReadsTheTokensScopes(t *testing.T) {
	runner := &authRunner{
		responses: [][]byte{[]byte(`{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"octocat","scopes":"gist, project, repo"}]}}`)},
		errors:    []error{nil},
	}
	status, err := AuthenticationStatus(context.Background(), runner, DefaultHost)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status.Scopes, []string{"gist", "project", "repo"}) {
		t.Fatalf("scopes = %v", status.Scopes)
	}
	for scope, held := range map[string]bool{"project": true, "read:project": true, "repo": true, "public_repo": true, "workflow": false} {
		if status.HasScope(scope) != held {
			t.Errorf("HasScope(%s) = %v", scope, !held)
		}
	}
	if scopes := splitScopes("'repo', 'read:org'"); !reflect.DeepEqual(scopes, []string{"repo", "read:org"}) {
		t.Fatalf("quoted scopes = %v", scopes)
	}
	if scopes := splitScopes(""); len(scopes) != 0 {
		t.Fatalf("no scopes = %v", scopes)
	}
}
