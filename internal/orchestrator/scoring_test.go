package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/routing"
)

func TestValidateKeepsTheRulesAgentAndOnlyScoresItsStage(t *testing.T) {
	registry := agent.NewRegistry(agent.BuiltIn())
	stages := []StageBrief{
		{ID: "review", Permission: domain.PermissionReadOnly, Agent: "reviewer", Skills: []string{"conventions"}},
		{ID: "implement", Permission: domain.PermissionWorktree},
	}
	skills := map[string]bool{"conventions": true, "go": true}
	scored := func(review Assignment) Decision {
		return Decision{Assignments: []Assignment{review, {StageID: "implement", Agent: "implementer", Complexity: domain.ComplexityAssessment{Score: 7, Rationale: "the task"}}}}
	}
	validated, err := Validate(scored(Assignment{
		StageID: "review", Agent: "reviewer", Skills: []string{"go"}, Complexity: domain.ComplexityAssessment{Score: 3, Rationale: "a small diff"}, Rationale: " the rule ",
	}), stages, registry, skills)
	if err != nil {
		t.Fatal(err)
	}
	review := validated.Assignments[0]
	if review.Agent != "reviewer" || review.Complexity.Score != 3 || review.Rationale != "the rule" {
		t.Fatalf("the rule's stage keeps its agent and takes the model's score: %+v", review)
	}
	// The rule and its agent chose the stage's skills; the model only scores it.
	if len(review.Skills) != 1 || review.Skills[0] != "conventions" {
		t.Fatalf("a rule-staffed stage keeps only its own skills: %v", review.Skills)
	}

	cases := map[string]Assignment{
		"another agent": {StageID: "review", Agent: "researcher", Complexity: domain.ComplexityAssessment{Score: 3, Rationale: "r"}},
		"no agent":      {StageID: "review", Complexity: domain.ComplexityAssessment{Score: 3, Rationale: "r"}},
		"no score":      {StageID: "review", Agent: "reviewer", Complexity: domain.ComplexityAssessment{Rationale: "r"}},
		"no rationale":  {StageID: "review", Agent: "reviewer", Complexity: domain.ComplexityAssessment{Score: 3}},
	}
	for name, assignment := range cases {
		if _, err := Validate(scored(assignment), stages, registry, skills); err == nil {
			t.Errorf("%s: expected the decision to be rejected", name)
		}
	}
	unscored := Decision{Assignments: []Assignment{{StageID: "implement", Agent: "implementer", Complexity: domain.ComplexityAssessment{Score: 7, Rationale: "r"}}}}
	if _, err := Validate(unscored, stages, registry, skills); err == nil || !strings.Contains(err.Error(), `"review"`) {
		t.Fatalf("a decision that leaves the rule's stage unscored must be rejected: %v", err)
	}
}

func TestFallbackScoresEachStageAndKeepsTheRulesAgents(t *testing.T) {
	registry := agent.NewRegistry(agent.BuiltIn())
	task := sampleTask()
	task.Kind = domain.TaskBugfix
	stages := []StageBrief{
		{ID: "reproduce", Permission: domain.PermissionReadOnly, Agent: "researcher", Skills: []string{"conventions"}},
		{ID: "fix", Permission: domain.PermissionWorktree},
		{ID: "review", Permission: domain.PermissionReadOnly},
	}
	decision, err := Fallback(task, stages, registry, map[string]bool{"conventions": true})
	if err != nil {
		t.Fatal(err)
	}
	reproduce, fix, review := decision.Assignments[0], decision.Assignments[1], decision.Assignments[2]
	if reproduce.Agent != "researcher" || len(reproduce.Skills) != 1 {
		t.Fatalf("the rule's stage keeps its agent and skills: %+v", reproduce)
	}
	if fix.Complexity.Score != 7 || reproduce.Complexity.Score != 5 || review.Complexity.Score != 5 {
		t.Fatalf("a bugfix scores 7 where it writes and 2 less where it only reads: reproduce %d, fix %d, review %d", reproduce.Complexity.Score, fix.Complexity.Score, review.Complexity.Score)
	}
	if !strings.Contains(reproduce.Complexity.Rationale, "only reads") || reproduce.Complexity.Signals[len(reproduce.Complexity.Signals)-1] != "read-only stage" {
		t.Fatalf("a read-only stage's score must say why it is lower: %+v", reproduce.Complexity)
	}
}

func TestAStageThatOnlyReadsNeverScoresBelowOne(t *testing.T) {
	task := sampleTask()
	task.Kind = domain.TaskDocumentation
	task.AcceptanceCriteria = nil
	if score := AssessStage(task, StageBrief{ID: "check", Permission: domain.PermissionReadOnly}).Score; score != 1 {
		t.Fatalf("a documentation task's read-only stage scores 1, got %d", score)
	}
}

func TestTheRoutingTableIsPartOfTheStaffingPrompt(t *testing.T) {
	fake := &fakeRuntime{output: `{"assignments":[]}`}
	request := Request{
		Task: NewTaskBrief(sampleTask()), Runtime: domain.RuntimeClaude,
		Stages:  []StageBrief{{ID: "review", Permission: domain.PermissionReadOnly, Agent: "reviewer"}},
		Routing: map[domain.Runtime][]routing.Band{domain.RuntimeClaude: routing.DefaultPolicy().Bands[domain.RuntimeClaude]},
	}
	_, call, err := RuntimeRemote{Runtime: fake}.Decide(context.Background(), request, Options{RunID: "orch-1", WorkingDir: t.TempDir(), Instructions: "Plan."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(fake.request.Prompt)
	for _, want := range []string{`"runtime": "claude"`, `"agent": "reviewer"`, `"routing": {`, `"model": "haiku"`, "score every stage for its own work"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt must carry %s:\n%s", want, prompt)
		}
	}
	names := make([]string, 0, len(call.Sections))
	total := 0
	for _, section := range call.Sections {
		names = append(names, section.Section)
		total += section.Bytes
	}
	if strings.Join(names, ",") != "schema,instructions,protocol,task,repository,runtime,stages,agents,skills,routing,request" || total != len(fake.request.Prompt)+len(fake.request.Schema) {
		t.Fatalf("sections %v add to %d, want the %d bytes of prompt and schema", names, total, len(fake.request.Prompt)+len(fake.request.Schema))
	}
	// Another table asks another question, so a kept decision is not reused.
	before, _ := Key(request, Options{})
	request.Routing[domain.RuntimeClaude] = []routing.Band{{Minimum: 1, Maximum: 10, Model: "opus"}}
	if after, _ := Key(request, Options{}); after == before {
		t.Fatal("the key must change with the routing table")
	}
}
