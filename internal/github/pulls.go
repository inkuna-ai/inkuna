package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PullRequest is a pull request as gh lists it.
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	// State is OPEN, CLOSED, or MERGED.
	State    string `json:"state"`
	MergedAt string `json:"mergedAt,omitempty"`
	// HeadRefName and IsCrossRepository are the branch it was opened from,
	// and whether that branch is in a fork; only RecentPullRequests asks
	// for them.
	HeadRefName       string `json:"headRefName,omitempty"`
	IsCrossRepository bool   `json:"isCrossRepository,omitempty"`
}

// Merged reports whether the pull request merged.
func (pull PullRequest) Merged() bool { return strings.EqualFold(pull.State, "MERGED") }

// Open reports whether the pull request is open.
func (pull PullRequest) Open() bool { return strings.EqualFold(pull.State, "OPEN") }

// PullRequestsFrom lists the pull requests whose head is branch in
// repository, HOST/OWNER/NAME as gh's --repo takes it, newest first, in
// every state. A branch nobody opened one from has none.
func PullRequestsFrom(ctx context.Context, runner Runner, repository, branch string) ([]PullRequest, error) {
	if runner == nil {
		return nil, errors.New("github CLI runner is required")
	}
	if strings.TrimSpace(repository) == "" || strings.TrimSpace(branch) == "" {
		return nil, errors.New("a repository and a branch are required to find a pull request")
	}
	output, err := runner.Run(ctx, "pr", "list", "--repo", repository, "--head", branch, "--state", "all", "--limit", "20", "--json", "number,title,url,state,mergedAt")
	if err != nil {
		if detail := string(bytes.TrimSpace(output)); detail != "" {
			return nil, fmt.Errorf("gh pr list: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	var pulls []PullRequest
	if err := json.Unmarshal(output, &pulls); err != nil {
		return nil, fmt.Errorf("decode the pull requests from %s: %w", branch, err)
	}
	return pulls, nil
}

// RecentPullRequests lists up to limit pull requests in repository,
// HOST/OWNER/NAME as gh's --repo takes it, in every state, the most
// recently updated first, with the branch each was opened from. One call
// covers every branch, where PullRequestsFrom takes one per branch, and a
// pull request that merged since the last look is among the first.
func RecentPullRequests(ctx context.Context, runner Runner, repository string, limit int) ([]PullRequest, error) {
	if runner == nil {
		return nil, errors.New("github CLI runner is required")
	}
	if strings.TrimSpace(repository) == "" || limit < 1 {
		return nil, errors.New("a repository and a limit are required to list pull requests")
	}
	output, err := runner.Run(ctx, "pr", "list", "--repo", repository, "--state", "all", "--search", "sort:updated-desc", "--limit", strconv.Itoa(limit), "--json", "number,title,url,state,mergedAt,headRefName,isCrossRepository")
	if err != nil {
		if detail := string(bytes.TrimSpace(output)); detail != "" {
			return nil, fmt.Errorf("gh pr list: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	var pulls []PullRequest
	if err := json.Unmarshal(output, &pulls); err != nil {
		return nil, fmt.Errorf("decode the pull requests of %s: %w", repository, err)
	}
	return pulls, nil
}

// MergedPull is the newest of pulls that merged.
func MergedPull(pulls []PullRequest) (PullRequest, bool) {
	for _, pull := range pulls {
		if pull.Merged() {
			return pull, true
		}
	}
	return PullRequest{}, false
}

// CurrentPull is the pull request a branch is under review in: the newest
// open one, or else the newest of any state.
func CurrentPull(pulls []PullRequest) (PullRequest, bool) {
	for _, pull := range pulls {
		if pull.Open() {
			return pull, true
		}
	}
	if len(pulls) > 0 {
		return pulls[0], true
	}
	return PullRequest{}, false
}
