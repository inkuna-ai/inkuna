package orchestrator

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestProposeTitleIsReadOnlyAndSchemaBound(t *testing.T) {
	fake := &fakeRuntime{output: `{"title":"Add rate limiting to the public API"}`}
	remote := RuntimeRemote{Runtime: fake}
	request := TitleRequest{Kind: domain.TaskImplementation, Details: "Requests from one key can flood the API.\nLimit each key to 100 a minute.", AcceptanceCriteria: []string{"429 past the limit"}}
	repository := t.TempDir()
	title, call, err := remote.ProposeTitle(context.Background(), request, Options{Model: "haiku", RunID: "title-1", WorkingDir: repository}, nil)
	if err != nil || title != "Add rate limiting to the public API" {
		t.Fatalf("title %q, %v", title, err)
	}
	if fake.request.WorkingDir == repository {
		t.Fatal("the proposal must not run where it could read the repository")
	}
	if _, err := os.Stat(fake.request.WorkingDir); !os.IsNotExist(err) {
		t.Fatal("the empty working directory should be removed afterwards")
	}
	if fake.request.Permission != domain.PermissionReadOnly || !fake.request.NoTools || string(fake.request.Schema) != TitleSchema || fake.request.Model != "haiku" {
		t.Fatalf("unexpected run request %+v", fake.request)
	}
	prompt := string(fake.request.Prompt)
	if !strings.HasPrefix(prompt, TitleInstructions) || !strings.Contains(prompt, "Limit each key to 100 a minute.") || !strings.Contains(prompt, `"maxCharacters": 100`) {
		t.Fatalf("prompt should carry the instructions, the details, and the limit: %s", prompt)
	}
	total := 0
	for _, section := range call.Sections {
		total += section.Bytes
	}
	if call.Outcome != domain.OutcomeCompleted || total != len(fake.request.Prompt)+len(fake.request.Schema) {
		t.Fatalf("call %+v adds to %d bytes, want %d", call, total, len(fake.request.Prompt)+len(fake.request.Schema))
	}
	fake.output = ""
	if _, call, err := remote.ProposeTitle(context.Background(), request, Options{RunID: "title-2"}, nil); err == nil || call.Outcome != domain.OutcomeCompleted {
		t.Fatalf("empty output must be an error from a call that completed: %+v, %v", call, err)
	}
	if _, _, err := (RuntimeRemote{}).ProposeTitle(context.Background(), request, Options{}, nil); err == nil {
		t.Fatal("no runtime must be an error")
	}
}
