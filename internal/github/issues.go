package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/proc"
)

type IssueDraft struct {
	TaskID string   `json:"taskId"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
	Parent string   `json:"parentTaskId,omitempty"`
}

type IssueLink struct {
	TaskID string `json:"taskId"`
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type Runner interface {
	Run(context.Context, ...string) ([]byte, error)
}

type CLI struct{ Binary string }

func (cli CLI) Run(ctx context.Context, args ...string) ([]byte, error) {
	binary := cli.Binary
	if binary == "" {
		binary = "gh"
	}
	command := proc.Command(ctx, binary, args...)
	return command.CombinedOutput()
}

func Marker(taskID string) string { return "<!-- inkuna-task-id:" + taskID + " -->" }

func ValidateDrafts(drafts []IssueDraft) error {
	ids := make(map[string]bool, len(drafts))
	for _, draft := range drafts {
		if draft.TaskID == "" || strings.TrimSpace(draft.Title) == "" {
			return errors.New("every issue draft requires a task id and title")
		}
		if ids[draft.TaskID] {
			return fmt.Errorf("duplicate issue task id %q", draft.TaskID)
		}
		ids[draft.TaskID] = true
	}
	for _, draft := range drafts {
		if draft.Parent != "" && !ids[draft.Parent] {
			return fmt.Errorf("issue %q has missing parent %q", draft.TaskID, draft.Parent)
		}
	}
	return nil
}

// CreateApprovedBatch creates exactly the supplied approved drafts. Existing
// marker links are consulted first so retries do not duplicate issues.
func CreateApprovedBatch(ctx context.Context, runner Runner, repository string, drafts []IssueDraft) ([]IssueLink, error) {
	if err := ValidateDrafts(drafts); err != nil {
		return nil, err
	}
	links := make(map[string]IssueLink, len(drafts))
	for _, draft := range drafts {
		if link, ok, err := findByMarker(ctx, runner, repository, draft.TaskID); err != nil {
			return nil, err
		} else if ok {
			links[draft.TaskID] = link
		}
	}
	remaining := len(drafts) - len(links)
	for remaining > 0 {
		progress := false
		for _, draft := range drafts {
			if _, exists := links[draft.TaskID]; exists {
				continue
			}
			var parentNumber int
			if draft.Parent != "" {
				parent, ready := links[draft.Parent]
				if !ready {
					continue
				}
				parentNumber = parent.Number
			}
			args := []string{"issue", "create", "--repo", repository, "--title", draft.Title, "--body", draft.Body + "\n\n" + Marker(draft.TaskID)}
			for _, label := range draft.Labels {
				args = append(args, "--label", label)
			}
			if parentNumber > 0 {
				args = append(args, "--parent", strconv.Itoa(parentNumber))
			}
			output, err := runner.Run(ctx, args...)
			if err != nil {
				return mapLinks(drafts, links), fmt.Errorf("create issue %q: %w: %s", draft.Title, err, bytes.TrimSpace(output))
			}
			url := strings.TrimSpace(string(output))
			number, err := issueNumber(url)
			if err != nil {
				return mapLinks(drafts, links), err
			}
			links[draft.TaskID] = IssueLink{TaskID: draft.TaskID, Number: number, URL: url}
			remaining--
			progress = true
		}
		if !progress {
			return mapLinks(drafts, links), errors.New("issue hierarchy contains a cycle")
		}
	}
	return mapLinks(drafts, links), nil
}

func findByMarker(ctx context.Context, runner Runner, repository, taskID string) (IssueLink, bool, error) {
	output, err := runner.Run(ctx, "issue", "list", "--repo", repository, "--state", "all", "--search", Marker(taskID)+" in:body", "--json", "number,url,body", "--limit", "1")
	if err != nil {
		return IssueLink{}, false, fmt.Errorf("search existing issue: %w", err)
	}
	var issues []struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(output, &issues); err != nil {
		return IssueLink{}, false, fmt.Errorf("decode issue search: %w", err)
	}
	if len(issues) == 0 || !strings.Contains(issues[0].Body, Marker(taskID)) {
		return IssueLink{}, false, nil
	}
	return IssueLink{TaskID: taskID, Number: issues[0].Number, URL: issues[0].URL}, true, nil
}

var issueURL = regexp.MustCompile(`/issues/(\d+)/?$`)

func issueNumber(url string) (int, error) {
	match := issueURL.FindStringSubmatch(url)
	if len(match) != 2 {
		return 0, fmt.Errorf("unexpected issue URL %q", url)
	}
	return strconv.Atoi(match[1])
}

func mapLinks(drafts []IssueDraft, values map[string]IssueLink) []IssueLink {
	result := make([]IssueLink, 0, len(values))
	for _, draft := range drafts {
		if link, ok := values[draft.TaskID]; ok {
			result = append(result, link)
		}
	}
	return result
}
