package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const DefaultHost = "github.com"

type AuthStatus struct {
	Authenticated bool
	Account       string
	Host          string
	GitProtocol   string
	// Scopes are the OAuth scopes gh's token has. gh lists none for a token
	// that has no scopes to list, such as a fine-grained one.
	Scopes []string
}

// HasScope reports whether the token has the scope, or the one that
// includes it: repo includes public_repo, and project read:project.
func (status AuthStatus) HasScope(scope string) bool {
	for _, held := range status.Scopes {
		if held == scope || (scope == "public_repo" && held == "repo") || (scope == "read:project" && held == "project") {
			return true
		}
	}
	return false
}

func AuthenticationStatus(ctx context.Context, runner Runner, host string) (AuthStatus, error) {
	if runner == nil {
		return AuthStatus{}, errors.New("github CLI runner is required")
	}
	if strings.TrimSpace(host) == "" {
		host = DefaultHost
	}
	output, err := runner.Run(ctx, "auth", "status", "--hostname", host, "--json", "hosts")
	if err != nil {
		return AuthStatus{Host: host}, fmt.Errorf("check GitHub authentication: %w", err)
	}
	var response struct {
		Hosts map[string][]struct {
			State       string `json:"state"`
			Active      bool   `json:"active"`
			Host        string `json:"host"`
			Login       string `json:"login"`
			GitProtocol string `json:"gitProtocol"`
			Scopes      string `json:"scopes"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return AuthStatus{Host: host}, fmt.Errorf("decode GitHub authentication status: %w", err)
	}
	for _, account := range response.Hosts[host] {
		if account.Active {
			return AuthStatus{
				Authenticated: account.State == "success",
				Account:       account.Login,
				Host:          account.Host,
				GitProtocol:   account.GitProtocol,
				Scopes:        splitScopes(account.Scopes),
			}, nil
		}
	}
	return AuthStatus{Host: host}, nil
}

func Authenticate(ctx context.Context, runner Runner, host, gitProtocol string) (AuthStatus, error) {
	if runner == nil {
		return AuthStatus{}, errors.New("github CLI runner is required")
	}
	status, err := AuthenticationStatus(ctx, runner, host)
	if err == nil && status.Authenticated {
		return status, nil
	}
	if strings.TrimSpace(host) == "" {
		host = DefaultHost
	}
	gitProtocol = strings.ToLower(strings.TrimSpace(gitProtocol))
	if gitProtocol == "" {
		gitProtocol = "https"
	}
	if gitProtocol != "https" && gitProtocol != "ssh" {
		return AuthStatus{Host: host}, errors.New("github git protocol must be https or ssh")
	}
	args := []string{
		"auth", "login",
		"--hostname", host,
		"--git-protocol", gitProtocol,
	}
	if gitProtocol == "ssh" {
		args = append(args, "--skip-ssh-key")
	}
	args = append(args, "--web", "--clipboard")
	if _, loginErr := runner.Run(ctx, args...); loginErr != nil {
		return AuthStatus{Host: host}, fmt.Errorf("authenticate GitHub: %w", loginErr)
	}
	status, err = AuthenticationStatus(ctx, runner, host)
	if err != nil {
		return status, err
	}
	if !status.Authenticated {
		return status, errors.New("github authentication did not complete")
	}
	return status, nil
}

// splitScopes reads gh's scope list, "repo, project" or "'repo', 'project'".
func splitScopes(list string) []string {
	scopes := make([]string, 0)
	for _, scope := range strings.Split(list, ",") {
		if scope = strings.Trim(strings.TrimSpace(scope), "'\""); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}
