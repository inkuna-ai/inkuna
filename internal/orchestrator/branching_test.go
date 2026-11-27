package orchestrator

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/branching"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

func TestProposeBranchingIsReadOnlyAndSchemaBound(t *testing.T) {
	fake := &fakeRuntime{output: `{"tracker":"linear","base":"main","pattern":"{type}/{story}-{slug}","rationale":"branch names carry MAD keys"}`}
	remote := RuntimeRemote{Runtime: fake}
	request := BranchingRequest{Missing: branching.Required, Evidence: branching.Evidence{Repository: "bakery", Branches: []string{"feature/ENG-1-x"}}}
	repository := t.TempDir()
	proposal, call, err := remote.ProposeBranching(context.Background(), request, Options{Model: "sonnet", RunID: "branching-1", WorkingDir: repository}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fake.request.WorkingDir == repository {
		t.Fatal("the proposal must not run where it could read the repository")
	}
	if _, err := os.Stat(fake.request.WorkingDir); !os.IsNotExist(err) {
		t.Fatal("the empty working directory should be removed afterwards")
	}
	if fake.request.Permission != domain.PermissionReadOnly || !fake.request.NoTools || string(fake.request.Schema) != BranchingSchema || fake.request.Model != "sonnet" || fake.request.WorkingDir == "" {
		t.Fatalf("unexpected run request %+v", fake.request)
	}
	prompt := string(fake.request.Prompt)
	if !strings.HasPrefix(prompt, BranchingInstructions) || !strings.Contains(prompt, `"feature/ENG-1-x"`) || !strings.Contains(prompt, `"allowed"`) {
		t.Fatalf("prompt should carry the instructions and the evidence: %s", prompt)
	}
	if proposal.Tracker != branching.TrackerLinear || proposal.Pattern != "{type}/{story}-{slug}" {
		t.Fatalf("unexpected proposal %+v", proposal)
	}
	total := 0
	for _, section := range call.Sections {
		total += section.Bytes
	}
	if call.Outcome != domain.OutcomeCompleted || total != len(fake.request.Prompt)+len(fake.request.Schema) {
		t.Fatalf("call %+v adds to %d bytes, want %d", call, total, len(fake.request.Prompt)+len(fake.request.Schema))
	}
	fake.output = ""
	if _, call, err := remote.ProposeBranching(context.Background(), request, Options{RunID: "branching-2", WorkingDir: t.TempDir()}, nil); err == nil || call.Outcome != domain.OutcomeCompleted {
		t.Fatalf("empty output must be an error from a call that completed: %+v, %v", call, err)
	}
}

func TestABranchingProposalWithNullsLeavesThoseKeysMissing(t *testing.T) {
	// Codex's strict schema makes every key required, so the keys a proposal
	// has nothing for arrive as null.
	fake := &fakeRuntime{output: `{"tracker":null,"base":"main","pattern":null,"location":null,"rationale":"main is the default branch"}`}
	proposal, _, err := RuntimeRemote{Runtime: fake}.ProposeBranching(context.Background(), BranchingRequest{Missing: branching.Required}, Options{RunID: "branching-1"}, nil)
	if err != nil || proposal != (branching.Proposal{Base: "main", Rationale: "main is the default branch"}) {
		t.Fatalf("proposal %+v, %v", proposal, err)
	}
}
