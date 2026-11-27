package linear

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestParseIssueReference(t *testing.T) {
	for _, test := range []struct{ input, expected string }{
		{"eng-123", "ENG-123"},
		{"https://linear.app/acme/issue/ENG-123/fix-the-thing", "ENG-123"},
		{"https://linear.app/issue/OPS-9/title", "OPS-9"},
	} {
		actual, err := ParseIssueReference(test.input)
		if err != nil || actual != test.expected {
			t.Fatalf("ParseIssueReference(%q) = %q, %v", test.input, actual, err)
		}
	}
	for _, invalid := range []string{"", "https://example.com/issue/ENG-1", "not-an-issue"} {
		if _, err := ParseIssueReference(invalid); err == nil {
			t.Fatalf("accepted invalid reference %q", invalid)
		}
	}
}

func TestIssueUsesReadOnlyGraphQLAndMapsFields(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s", request.Method)
		}
		if request.Header.Get("Authorization") != "test-token" {
			t.Error("authorization header missing")
		}
		var body struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Variables["id"] != "ENG-123" {
			t.Fatalf("variables = %#v", body.Variables)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"issue":{"id":"uuid","identifier":"ENG-123","title":"Fix it","description":"Details","url":"https://linear.app/acme/issue/ENG-123/fix-it","priority":2,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","team":{"id":"team","name":"Engineering","key":"ENG"},"state":{"id":"state","name":"Todo","type":"unstarted"},"project":{"id":"project","name":"Inkuna"},"parent":null,"labels":{"nodes":[{"id":"label","name":"Bug"}]}}}}`))}, nil
	})}
	client, err := NewWithEndpoint("test-token", "https://linear.test/graphql", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	issue, err := client.Issue(context.Background(), "eng-123")
	if err != nil {
		t.Fatal(err)
	}
	if issue.Identifier != "ENG-123" || issue.Title != "Fix it" || len(issue.Labels) != 1 || issue.Labels[0] != "Bug" {
		t.Fatalf("issue = %#v", issue)
	}
}

func TestIssueReturnsGraphQLError(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"errors":[{"message":"Unauthorized","extensions":{"code":"AUTHENTICATION_ERROR"}}]}`))}, nil
	})}
	client, _ := NewWithEndpoint("bad", "https://linear.test/graphql", httpClient)
	if _, err := client.Issue(context.Background(), "ENG-1"); err == nil {
		t.Fatal("expected GraphQL error")
	}
}

func TestViewerVerifiesAuthentication(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "test-token" {
			t.Error("authorization header missing")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":{"viewer":{"id":"user-id","name":"Ada Lovelace","email":"ada@example.com"}}}`))}, nil
	})}
	client, err := NewWithEndpoint("test-token", "https://linear.test/graphql", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := client.Viewer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if viewer.ID != "user-id" || viewer.Email != "ada@example.com" {
		t.Fatalf("unexpected viewer: %#v", viewer)
	}
}
