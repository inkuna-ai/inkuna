package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var issueKeyPattern = regexp.MustCompile(`(?i)^[a-z][a-z0-9]*-[0-9]+$`)

type Client struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

type Issue struct {
	ID          string    `json:"id"`
	Identifier  string    `json:"identifier"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	Priority    int       `json:"priority"`
	DueDate     string    `json:"dueDate,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Team        NamedKey  `json:"team"`
	State       State     `json:"state"`
	Project     *Named    `json:"project,omitempty"`
	Parent      *Parent   `json:"parent,omitempty"`
	Labels      []string  `json:"labels"`
}

type Named struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type NamedKey struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

type State struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type Parent struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
}

type Viewer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func NewWithEndpoint(token, endpoint string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("LINEAR_API_KEY is not configured")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("valid Linear endpoint is required")
	}
	if httpClient == nil {
		return nil, errors.New("http client is required")
	}
	return &Client{endpoint: endpoint, token: token, httpClient: httpClient}, nil
}

func ParseIssueReference(reference string) (string, error) {
	value := strings.TrimSpace(reference)
	if issueKeyPattern.MatchString(value) {
		return strings.ToUpper(value), nil
	}
	parsed, err := url.Parse(value)
	if err != nil || !strings.EqualFold(parsed.Host, "linear.app") {
		return "", errors.New("enter a Linear issue key or linear.app issue URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index, part := range parts {
		if strings.EqualFold(part, "issue") && index+1 < len(parts) && issueKeyPattern.MatchString(parts[index+1]) {
			return strings.ToUpper(parts[index+1]), nil
		}
	}
	return "", errors.New("linear URL does not contain an issue key")
}

func (client *Client) Issue(ctx context.Context, reference string) (Issue, error) {
	identifier, err := ParseIssueReference(reference)
	if err != nil {
		return Issue{}, err
	}
	const query = `query InkunaIssue($id: String!) {
  issue(id: $id) {
    id identifier title description url priority dueDate createdAt updatedAt
    team { id name key }
    state { id name type }
    project { id name }
    parent { id identifier title }
    labels(first: 50) { nodes { name } }
  }
}`
	var response struct {
		Data struct {
			Issue *struct {
				ID          string    `json:"id"`
				Identifier  string    `json:"identifier"`
				Title       string    `json:"title"`
				Description string    `json:"description"`
				URL         string    `json:"url"`
				Priority    int       `json:"priority"`
				DueDate     string    `json:"dueDate"`
				CreatedAt   time.Time `json:"createdAt"`
				UpdatedAt   time.Time `json:"updatedAt"`
				Team        NamedKey  `json:"team"`
				State       State     `json:"state"`
				Project     *Named    `json:"project"`
				Parent      *Parent   `json:"parent"`
				Labels      struct {
					Nodes []Named `json:"nodes"`
				} `json:"labels"`
			} `json:"issue"`
		} `json:"data"`
		Errors []graphqlError `json:"errors"`
	}
	if err := client.query(ctx, query, map[string]any{"id": identifier}, &response); err != nil {
		return Issue{}, err
	}
	if len(response.Errors) > 0 {
		return Issue{}, graphQLErrors(response.Errors)
	}
	if response.Data.Issue == nil {
		return Issue{}, fmt.Errorf("linear issue %s was not found or is not accessible", identifier)
	}
	source := response.Data.Issue
	issue := Issue{ID: source.ID, Identifier: source.Identifier, Title: source.Title, Description: source.Description, URL: source.URL, Priority: source.Priority, DueDate: source.DueDate, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt, Team: source.Team, State: source.State, Project: source.Project, Parent: source.Parent, Labels: make([]string, 0, len(source.Labels.Nodes))}
	for _, label := range source.Labels.Nodes {
		issue.Labels = append(issue.Labels, label.Name)
	}
	return issue, nil
}

func (client *Client) Viewer(ctx context.Context) (Viewer, error) {
	const query = `query InkunaViewer { viewer { id name email } }`
	var response struct {
		Data struct {
			Viewer *Viewer `json:"viewer"`
		} `json:"data"`
		Errors []graphqlError `json:"errors"`
	}
	if err := client.query(ctx, query, nil, &response); err != nil {
		return Viewer{}, err
	}
	if len(response.Errors) > 0 {
		return Viewer{}, graphQLErrors(response.Errors)
	}
	if response.Data.Viewer == nil || strings.TrimSpace(response.Data.Viewer.ID) == "" {
		return Viewer{}, errors.New("linear authentication returned no viewer")
	}
	return *response.Data.Viewer, nil
}

type graphqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
		// Type is Linear's class of the error, such as "forbidden" or
		// "invalid input".
		Type string `json:"type"`
	} `json:"extensions"`
}

// apiError is Linear's answer of GraphQL errors. Linear sends them with the
// HTTP status of the first error (403 for a missing scope, 400 for an entity
// it cannot find), not only with 200, so the errors are read either way.
type apiError struct {
	status int
	errors []graphqlError
}

func (failure *apiError) Error() string {
	text := graphQLErrors(failure.errors).Error()
	if failure.status >= 300 {
		text += fmt.Sprintf(" (HTTP %d)", failure.status)
	}
	return text
}

func (client *Client) query(ctx context.Context, query string, variables map[string]any, target any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", client.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Inkuna/1")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("query Linear: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read Linear response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var answer struct {
			Errors []graphqlError `json:"errors"`
		}
		if json.Unmarshal(contents, &answer) == nil && len(answer.Errors) > 0 {
			return &apiError{status: response.StatusCode, errors: answer.Errors}
		}
		return fmt.Errorf("linear returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(contents)))
	}
	if err := json.Unmarshal(contents, target); err != nil {
		return fmt.Errorf("decode Linear response: %w", err)
	}
	return nil
}

func graphQLErrors(values []graphqlError) error {
	messages := make([]string, 0, len(values))
	for _, value := range values {
		message := value.Message
		if value.Extensions.Code != "" {
			message += " (" + value.Extensions.Code + ")"
		}
		messages = append(messages, message)
	}
	return errors.New("Linear API: " + strings.Join(messages, "; "))
}
