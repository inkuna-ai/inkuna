package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// TitleRequest asks for a short title for a task its author left untitled.
// The model sees the task as written, never the repository.
type TitleRequest struct {
	Kind               domain.TaskKind `json:"kind"`
	Details            string          `json:"details"`
	AcceptanceCriteria []string        `json:"acceptanceCriteria,omitempty"`
	MaxCharacters      int             `json:"maxCharacters"`
}

// TitleSchema constrains the model's answer. Go cleans the title and checks
// it again, and derives one from the details when it refuses it.
const TitleSchema = `{
  "type": "object",
  "required": ["title"],
  "additionalProperties": false,
  "properties": {
    "title": {"type": "string"}
  }
}`

// TitleInstructions tell the model what a good title is.
const TitleInstructions = `You are the Inkuna orchestrator. A task was written without a title; write one.
- Name the change itself, in the imperative, like a good commit subject or issue title: "Add rate limiting to the public API", "Fix the empty-name crash in Greet".
- One line, at most maxCharacters characters, ideally under 60. No quotes, no trailing full stop, no issue keys or numbers, no Markdown.
- Use only what the details say; do not invent scope.
Respond only with JSON matching the provided schema.`

// TitleProposer writes a title for an untitled task and reports what the
// call cost.
type TitleProposer interface {
	ProposeTitle(ctx context.Context, request TitleRequest, options Options, sink agentruntime.EventSink) (string, Call, error)
}

// titleDirective sits between the instructions and the request.
const titleDirective = "\n\nWrite the title for this task. Reply with JSON only.\n\n"

// ProposeTitle runs the orchestrator read-only with the title schema, no
// tools, and in an empty directory rather than options.WorkingDir: the task
// is all it should see. The title comes back as the model wrote it.
func (remote RuntimeRemote) ProposeTitle(ctx context.Context, request TitleRequest, options Options, sink agentruntime.EventSink) (string, Call, error) {
	if remote.Runtime == nil {
		return "", Call{}, errors.New("orchestrator runtime is not configured")
	}
	if request.MaxCharacters <= 0 {
		request.MaxCharacters = domain.MaxTitleRunes
	}
	body, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return "", Call{}, err
	}
	prompt := TitleInstructions + titleDirective + string(body)
	sections := promptSections(TitleSchema, TitleInstructions, titleDirective, len(body), []requestField{
		{"kind", request.Kind}, {"details", request.Details}, {"acceptanceCriteria", request.AcceptanceCriteria}, {"maxCharacters", request.MaxCharacters},
	})
	if options.WallTimeSeconds <= 0 {
		options.WallTimeSeconds = 300
	}
	if sink == nil {
		sink = func(domain.RunEvent) error { return nil }
	}
	empty, err := os.MkdirTemp("", "inkuna-title-")
	if err != nil {
		return "", Call{}, err
	}
	defer func() { _ = os.RemoveAll(empty) }()
	call := Call{RunID: options.RunID, Sections: sections, StartedAt: time.Now().UTC()}
	result, err := remote.Runtime.Start(ctx, agentruntime.RunRequest{
		RunID: options.RunID, WorkingDir: empty, Prompt: []byte(prompt), Schema: []byte(TitleSchema),
		Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: options.WallTimeSeconds, MaxTurns: options.MaxTurns, MaxOutputBytes: 64 << 10},
		Model: options.Model, ReasoningEffort: options.ReasoningEffort, NoTools: true,
	}, sink)
	call.Usage, call.ModelUsed, call.AuthMode, call.Outcome, call.FinishedAt = result.Usage, result.Model, result.AuthMode, agentruntime.Outcome(err), time.Now().UTC()
	if err != nil {
		return "", call, err
	}
	var answer struct {
		Title string `json:"title"`
	}
	if len(result.Output) == 0 {
		return "", call, errors.New("orchestrator returned no title")
	}
	if err := json.Unmarshal(result.Output, &answer); err != nil {
		return "", call, fmt.Errorf("decode title: %w", err)
	}
	return answer.Title, call, nil
}
