package claude

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

func TestAuthenticationStatus(t *testing.T) {
	runner := &authRunner{responses: [][]byte{[]byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}`)}, errors: []error{nil}}
	status, err := AuthenticationStatus(context.Background(), runner)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Authenticated || status.Method != "claude.ai" || status.Provider != "firstParty" {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestAuthenticateLogsInAndVerifiesSession(t *testing.T) {
	runner := &authRunner{
		responses: [][]byte{nil, []byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}`)},
		errors:    []error{nil, nil},
	}
	status, err := Authenticate(context.Background(), runner)
	if err != nil || !status.Authenticated {
		t.Fatalf("authenticate: status=%#v err=%v", status, err)
	}
	expected := [][]string{{"auth", "login"}, {"auth", "status"}}
	if !reflect.DeepEqual(runner.calls, expected) {
		t.Fatalf("calls=%v, want %v", runner.calls, expected)
	}
}

func TestAuthenticationStatusReportsCommandFailure(t *testing.T) {
	runner := &authRunner{responses: [][]byte{nil}, errors: []error{errors.New("not installed")}}
	if _, err := AuthenticationStatus(context.Background(), runner); err == nil {
		t.Fatal("missing Claude Code was accepted")
	}
}

func TestAuthenticationStatusAcceptsLoggedOutJSONWithNonzeroExit(t *testing.T) {
	runner := &authRunner{
		responses: [][]byte{[]byte(`{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`)},
		errors:    []error{errors.New("exit status 1")},
	}
	status, err := AuthenticationStatus(context.Background(), runner)
	if err != nil || status.Authenticated {
		t.Fatalf("logged-out status: %#v err=%v", status, err)
	}
}
