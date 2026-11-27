package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type Manager struct {
	Repository string
	Root       string
}

type Worktree struct {
	TaskID string `json:"taskId"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Create adds an isolated branch and worktree for one writable leaf task.
func (manager Manager) Create(ctx context.Context, taskID, startPoint string) (Worktree, error) {
	if !safeID.MatchString(taskID) {
		return Worktree{}, errors.New("task id contains unsafe worktree characters")
	}
	if manager.Repository == "" || manager.Root == "" {
		return Worktree{}, errors.New("repository and worktree root are required")
	}
	if startPoint == "" {
		startPoint = "HEAD"
	}
	root, err := filepath.Abs(manager.Root)
	if err != nil {
		return Worktree{}, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Worktree{}, err
	}
	destination := filepath.Join(root, taskID)
	branch := "inkuna/" + taskID
	if _, err := os.Stat(destination); err == nil {
		return Worktree{}, fmt.Errorf("worktree already exists at %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Worktree{}, err
	}
	command := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branch, destination, startPoint)
	command.Dir = manager.Repository
	if output, err := command.CombinedOutput(); err != nil {
		return Worktree{}, fmt.Errorf("create git worktree: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return Worktree{TaskID: taskID, Branch: branch, Path: destination}, nil
}
