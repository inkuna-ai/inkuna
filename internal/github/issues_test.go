package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type fakeRunner struct {
	issues map[string]IssueLink
	calls  [][]string
}

func (fake *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	fake.calls = append(fake.calls, append([]string{}, args...))
	if len(args) > 1 && args[1] == "list" {
		search := args[indexOf(args, "--search")+1]
		for taskID, link := range fake.issues {
			if strings.Contains(search, taskID) {
				return json.Marshal([]map[string]any{{"number": link.Number, "url": link.URL, "body": Marker(taskID)}})
			}
		}
		return []byte("[]"), nil
	}
	if len(args) > 1 && args[1] == "create" {
		body := args[indexOf(args, "--body")+1]
		for _, taskID := range []string{"parent", "child"} {
			if strings.Contains(body, Marker(taskID)) {
				number := len(fake.issues) + 1
				link := IssueLink{TaskID: taskID, Number: number, URL: fmt.Sprintf("https://github.com/o/r/issues/%d", number)}
				fake.issues[taskID] = link
				return []byte(link.URL + "\n"), nil
			}
		}
	}
	return nil, fmt.Errorf("unexpected args: %v", args)
}

func TestApprovedBatchIsIdempotentAndCreatesHierarchy(t *testing.T) {
	fake := &fakeRunner{issues: map[string]IssueLink{}}
	drafts := []IssueDraft{{TaskID: "parent", Title: "Parent", Body: "body"}, {TaskID: "child", Title: "Child", Body: "body", Parent: "parent"}}
	first, err := CreateApprovedBatch(context.Background(), fake, "o/r", drafts)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(fake.issues) != 2 {
		t.Fatalf("unexpected first batch: %#v", first)
	}
	callCount := len(fake.calls)
	second, err := CreateApprovedBatch(context.Background(), fake, "o/r", drafts)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 || len(fake.calls) != callCount+2 {
		t.Fatalf("retry created issues: calls=%d %#v", len(fake.calls), second)
	}
	foundParent := false
	for _, call := range fake.calls[:callCount] {
		if index := indexOf(call, "--parent"); index >= 0 && call[index+1] == "1" {
			foundParent = true
		}
	}
	if !foundParent {
		t.Fatal("child issue was not attached to parent")
	}
}

func indexOf(values []string, value string) int {
	for index, entry := range values {
		if entry == value {
			return index
		}
	}
	return -1
}
