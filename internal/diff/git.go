package diff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/proc"
)

// listLimit bounds the output of a listing command. A change this large is
// refused rather than listed in part.
const listLimit = 64 << 20

// pinned comes before every git command. It fixes the settings a user's
// configuration could change in the output the parser reads, and makes
// pathspecs literal, so a path is never taken as a glob.
var pinned = []string{
	"--literal-pathspecs",
	"-c", "core.quotePath=false",
	"-c", "diff.noprefix=false",
	"-c", "diff.mnemonicPrefix=false",
	"-c", "diff.relative=false",
	"-c", "diff.suppressBlankEmpty=false",
	"-c", "color.ui=false",
}

// diffFlags are the flags every diff command shares, so the listing and a
// file's patch detect the same renames and copies.
var diffFlags = []string{"--no-color", "--no-ext-diff", "--no-textconv", "-M", "-C"}

// errOverLimit reports output longer than the caller's limit.
var errOverLimit = errors.New("output over limit")

// git runs a read-only git command in repository and returns its stdout,
// at most limit bytes; longer output stops the command and returns
// errOverLimit with what was read.
func git(ctx context.Context, repository string, stdin []byte, limit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := proc.Command(ctx, "git", append(append([]string{}, pinned...), args...)...)
	command.Dir = repository
	// Diffs between commits never need the index, so git takes no locks a
	// worker's git could be waiting on.
	command.Env = append(command.Environ(), "GIT_OPTIONAL_LOCKS=0")
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, int64(limit)+1))
	if len(output) > limit {
		cancel()
		_ = command.Wait()
		return output[:limit], errOverLimit
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	if readErr != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], readErr)
	}
	return output, nil
}

// gitStatus runs a git command that answers with its exit status: true for
// 0, false for 1, and an error for anything else.
func gitStatus(ctx context.Context, repository string, args ...string) (bool, error) {
	command := proc.Command(ctx, "git", append(append([]string{}, pinned...), args...)...)
	command.Dir = repository
	command.Env = append(command.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	default:
		return false, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
}
