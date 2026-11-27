package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// BranchingRequest asks for values for the branching configuration keys a
// repository lacks. The model sees only the evidence, never file contents.
type BranchingRequest struct {
	Missing  []string           `json:"missing"`
	Evidence branching.Evidence `json:"evidence"`
	Allowed  map[string]any     `json:"allowed"`
}

// BranchingSchema constrains the model's proposal. Go validates every value
// again and falls back to branching.Infer for any it rejects.
const BranchingSchema = `{
  "type": "object",
  "required": ["rationale"],
  "additionalProperties": false,
  "properties": {
    "tracker": {"type": "string", "enum": ["linear", "jira", "github", "none"]},
    "base": {"type": "string"},
    "pattern": {"type": "string"},
    "location": {"type": "string"},
    "rationale": {"type": "string"}
  }
}`

// BranchingInstructions tell the model what a good proposal is.
const BranchingInstructions = `You are the Inkuna orchestrator. A repository has no complete branching configuration and you propose the missing values from the evidence: its recent branch names, remote host, default branch, and existing worktrees.
- tracker: the system its stories come from. Branch names with keys like ABC-123 suggest linear or jira; numbered names like 23-docs on a GitHub remote suggest github; otherwise none.
- base: the branch new work starts from, normally the default branch. It must be a branch in the evidence.
- pattern: how branches are named, using {type}, {story}, and {slug}; it must contain {slug}. Match what the recent branch names already do.
- location: only when existing worktrees share a directory beside the repository, as a path relative to it using {repo} for the repository name, such as ../{repo}-worktrees.
Propose only what the evidence supports and explain it in one sentence. Respond only with JSON matching the provided schema.`

// BranchingProposer proposes missing branching configuration and reports
// what the call cost.
type BranchingProposer interface {
	ProposeBranching(ctx context.Context, request BranchingRequest, options Options, sink agentruntime.EventSink) (branching.Proposal, Call, error)
}

// branchingDirective sits between the instructions and the request.
const branchingDirective = "\n\nPropose values for the missing keys. Reply with JSON only.\n\n"

// ProposeBranching runs the orchestrator read-only with the branching schema,
// no tools, and in an empty directory rather than options.WorkingDir: the
// evidence is all it should see.
func (remote RuntimeRemote) ProposeBranching(ctx context.Context, request BranchingRequest, options Options, sink agentruntime.EventSink) (branching.Proposal, Call, error) {
	if remote.Runtime == nil {
		return branching.Proposal{}, Call{}, errors.New("orchestrator runtime is not configured")
	}
	if request.Allowed == nil {
		request.Allowed = map[string]any{"trackers": []string{"linear", "jira", "github", "none"}, "placeholders": []string{"{type}", "{story}", "{slug}"}}
	}
	body, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return branching.Proposal{}, Call{}, err
	}
	prompt := BranchingInstructions + branchingDirective + string(body)
	sections := promptSections(BranchingSchema, BranchingInstructions, branchingDirective, len(body), []requestField{
		{"missing", request.Missing}, {"evidence", request.Evidence}, {"allowed", request.Allowed},
	})
	if options.WallTimeSeconds <= 0 {
		options.WallTimeSeconds = 300
	}
	if sink == nil {
		sink = func(domain.RunEvent) error { return nil }
	}
	empty, err := os.MkdirTemp("", "inkuna-branching-")
	if err != nil {
		return branching.Proposal{}, Call{}, err
	}
	defer func() { _ = os.RemoveAll(empty) }()
	call := Call{RunID: options.RunID, Sections: sections, StartedAt: time.Now().UTC()}
	result, err := remote.Runtime.Start(ctx, agentruntime.RunRequest{
		RunID: options.RunID, WorkingDir: empty, Prompt: []byte(prompt), Schema: []byte(BranchingSchema),
		Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: options.WallTimeSeconds, MaxTurns: options.MaxTurns, MaxOutputBytes: 1 << 20},
		Model: options.Model, ReasoningEffort: options.ReasoningEffort, NoTools: true,
	}, sink)
	call.Usage, call.ModelUsed, call.AuthMode, call.Outcome, call.FinishedAt = result.Usage, result.Model, result.AuthMode, agentruntime.Outcome(err), time.Now().UTC()
	if err != nil {
		return branching.Proposal{}, call, err
	}
	if len(result.Output) == 0 {
		return branching.Proposal{}, call, errors.New("orchestrator returned no branching proposal")
	}
	var proposal branching.Proposal
	if err := json.Unmarshal(result.Output, &proposal); err != nil {
		return branching.Proposal{}, call, fmt.Errorf("decode branching proposal: %w", err)
	}
	return proposal, call, nil
}
