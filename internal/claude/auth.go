package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type Runner interface {
	Run(context.Context, ...string) ([]byte, error)
}

type CLI struct{ Binary string }

type AuthStatus struct {
	Authenticated bool
	Method        string
	Provider      string
}

func (cli CLI) Run(ctx context.Context, args ...string) ([]byte, error) {
	binary := strings.TrimSpace(cli.Binary)
	if binary == "" {
		binary = "claude"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("find Claude Code: %w", err)
	}
	output, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("claude %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func AuthenticationStatus(ctx context.Context, runner Runner) (AuthStatus, error) {
	if runner == nil {
		return AuthStatus{}, errors.New("runner is required")
	}
	output, err := runner.Run(ctx, "auth", "status")
	var response struct {
		LoggedIn    bool   `json:"loggedIn"`
		AuthMethod  string `json:"authMethod"`
		APIProvider string `json:"apiProvider"`
	}
	if decodeErr := json.Unmarshal(output, &response); decodeErr != nil {
		if err != nil {
			return AuthStatus{}, err
		}
		return AuthStatus{}, fmt.Errorf("decode Claude authentication status: %w", decodeErr)
	}
	return AuthStatus{Authenticated: response.LoggedIn, Method: response.AuthMethod, Provider: response.APIProvider}, nil
}

func Authenticate(ctx context.Context, runner Runner) (AuthStatus, error) {
	if runner == nil {
		return AuthStatus{}, errors.New("runner is required")
	}
	if _, err := runner.Run(ctx, "auth", "login"); err != nil {
		return AuthStatus{}, fmt.Errorf("authenticate Claude Code: %w", err)
	}
	status, err := AuthenticationStatus(ctx, runner)
	if err != nil {
		return AuthStatus{}, err
	}
	if !status.Authenticated {
		return AuthStatus{}, errors.New("login completed without an authenticated session")
	}
	return status, nil
}
