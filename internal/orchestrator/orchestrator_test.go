package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/repository"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

type fakeRuntime struct {
	output  string
	usage   domain.Usage
	err     error
	request agentruntime.RunRequest
}

func (fake *fakeRuntime) Probe(context.Context) (agentruntime.Capabilities, error) {
	return agentruntime.Capabilities{}, nil
}

func (fake *fakeRuntime) Start(_ context.Context, request agentruntime.RunRequest, sink agentruntime.EventSink) (agentruntime.RunResult, error) {
	fake.request = request
	_ = sink(domain.RunEvent{RunID: request.RunID, Kind: domain.EventLifecycle, Summary: "started"})
	if fake.err != nil {
		return agentruntime.RunResult{Usage: fake.usage}, fake.err
	}
	return agentruntime.RunResult{Output: json.RawMessage(fake.output), Usage: fake.usage}, nil
}

func (fake *fakeRuntime) Cancel(string) error { return nil }

func (fake *fakeRuntime) Capabilities() agentruntime.Capabilities { return agentruntime.Capabilities{} }

func sampleTask() domain.TaskSpec {
	return domain.TaskSpec{Version: 1, ID: "task-1", Kind: domain.TaskImplementation, Objective: "Add a config file", Repository: "/repo", Priority: 1, Budget: domain.Budget{WallTimeSeconds: 600}}
}

func TestRuntimeRemoteSendsReadOnlySchemaRequest(t *testing.T) {
	fake := &fakeRuntime{output: `{"assignments":[{"stageId":"worker","agent":"implementer","skills":["go-desktop"],"complexity":{"score":6,"rationale":"touches config and storage","signals":["two packages"]},"rationale":"general implementation"}],"notes":"ok"}`}
	remote := RuntimeRemote{Runtime: fake}
	request := Request{Task: NewTaskBrief(sampleTask()), Stages: []StageBrief{{ID: "worker", Permission: domain.PermissionWorktree}}}
	neutral := t.TempDir()
	decision, _, err := remote.Decide(context.Background(), request, Options{Runtime: domain.RuntimeClaude, Model: "opus", MaxTurns: 4, RunID: "orch-1", WorkingDir: neutral, Instructions: "Plan."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fake.request.Permission != domain.PermissionReadOnly || string(fake.request.Schema) != Schema || fake.request.Model != "opus" || fake.request.Budget.MaxTurns != 4 {
		t.Fatalf("unexpected run request %+v", fake.request)
	}
	if !fake.request.NoTools || fake.request.WorkingDir != neutral {
		t.Fatalf("staffing must run with no tools where it is told: %+v", fake.request)
	}
	if !strings.Contains(string(fake.request.Prompt), `"stages"`) || !strings.HasPrefix(string(fake.request.Prompt), "Plan.") {
		t.Fatalf("prompt should carry the instructions and request: %s", fake.request.Prompt)
	}
	if len(decision.Assignments) != 1 || decision.Assignments[0].Agent != "implementer" || decision.Notes != "ok" {
		t.Fatalf("unexpected decision %+v", decision)
	}
	if _, _, err := remote.Decide(context.Background(), request, Options{RunID: "orch-2", WorkingDir: t.TempDir(), ReadTools: true}, nil); err != nil || fake.request.NoTools {
		t.Fatalf("the read tools setting must give staffing its tools back: %+v, %v", fake.request, err)
	}
	fake.output = ""
	if _, _, err := remote.Decide(context.Background(), request, Options{RunID: "orch-3", WorkingDir: t.TempDir()}, nil); err == nil {
		t.Fatal("empty output must be an error")
	}
}

func TestAStaffingDecisionWithNullsReadsLikeOneWithoutThem(t *testing.T) {
	// Codex's strict schema makes every key required, so the optional ones
	// arrive as null.
	request := Request{Task: NewTaskBrief(sampleTask()), Stages: []StageBrief{{ID: "worker", Permission: domain.PermissionWorktree}}}
	decide := func(output string) Decision {
		decision, _, err := RuntimeRemote{Runtime: &fakeRuntime{output: output}}.Decide(context.Background(), request, Options{RunID: "orch-1", WorkingDir: t.TempDir()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return decision
	}
	strict := decide(`{"assignments":[{"stageId":"worker","agent":"implementer","skills":[],"complexity":{"score":3,"rationale":"small","signals":null},"rationale":"general"}],"notes":null}`)
	open := decide(`{"assignments":[{"stageId":"worker","agent":"implementer","skills":[],"complexity":{"score":3,"rationale":"small"},"rationale":"general"}]}`)
	if !reflect.DeepEqual(strict, open) {
		t.Fatalf("strict decision %+v, want %+v", strict, open)
	}
}

func TestBriefsCarryWhatStaffingNeedsAndNoBoilerplate(t *testing.T) {
	task := sampleTask()
	task.Source, task.Metadata = domain.TaskSource{Kind: "request", Repository: "/Users/someone/repo"}, map[string]string{"created": "now"}
	task.AcceptanceCriteria, task.Labels = []string{"tests pass"}, []string{"ui"}
	encoded, _ := json.Marshal(NewTaskBrief(task))
	if string(encoded) != `{"kind":"implementation","objective":"Add a config file","acceptanceCriteria":["tests pass"],"labels":["ui"]}` {
		t.Fatalf("task brief = %s", encoded)
	}

	manifests := []string{"web/package.json", "go.mod", "Taskfile.yml"}
	for index := 0; index < ManifestLimit; index++ {
		manifests = append(manifests, fmt.Sprintf("services/s%02d/go.mod", index))
	}
	profile := NewRepositoryBrief(repository.Profile{Root: "/Users/someone/repo", Branch: "main", Head: "abc", Languages: []string{"Go"}, Manifests: manifests})
	if len(profile.Manifests) != ManifestLimit || profile.MoreManifests != 3 {
		t.Fatalf("manifests are capped at %d with a count of the rest: %d listed, %d more", ManifestLimit, len(profile.Manifests), profile.MoreManifests)
	}
	if strings.Join(profile.Manifests[:3], ",") != "Taskfile.yml,go.mod,web/package.json" {
		t.Fatalf("top-level manifests come first: %v", profile.Manifests[:3])
	}
	if encoded, _ := json.Marshal(profile); strings.Contains(string(encoded), "/Users/someone") || strings.Contains(string(encoded), "abc") {
		t.Fatalf("the repository brief says where the repository is: %s", encoded)
	}

	implementer, _ := agent.NewRegistry(agent.BuiltIn()).Get("implementer")
	encoded, _ = json.Marshal(NewAgentBrief(implementer))
	if string(encoded) != `{"name":"implementer","description":"General-purpose engineer that implements features and fixes inside the approved worktree.","permission":"worktree-write","kinds":["implementation","bugfix","skill-authoring"]}` {
		t.Fatalf("agent brief = %s", encoded)
	}
	advisor := NewAgentBrief(agent.Definition{Name: "advisor", Description: "Remote", Source: agent.SourceRemote, Permission: domain.PermissionReadOnly})
	if advisor.Runtime != domain.RuntimeRemote {
		t.Fatalf("a remote advisor's brief must say it is remote: %+v", advisor)
	}
}

func TestKeyChangesWithWhatDecidesTheAnswer(t *testing.T) {
	request := func() Request {
		return Request{Task: NewTaskBrief(sampleTask()), Stages: []StageBrief{{ID: "worker", Title: "Work", Permission: domain.PermissionWorktree}}, Agents: []AgentBrief{{Name: "implementer", Description: "d", Permission: domain.PermissionWorktree}}}
	}
	options := Options{Runtime: domain.RuntimeClaude, Model: "opus", Instructions: "Plan.", RunID: "orch-1", WorkingDir: "/a", WallTimeSeconds: 60}
	key := func(request Request, options Options) string {
		t.Helper()
		value, err := Key(request, options)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	base := key(request(), options)
	// A new plan has a new run id, directory, and time limit; none changes
	// what the model is asked.
	same := options
	same.RunID, same.WorkingDir, same.WallTimeSeconds, same.MaxTurns = "orch-2", "/b", 30, 3
	if key(request(), same) != base {
		t.Fatal("the key must not depend on the run id, directory, or limits")
	}
	changed := map[string]func(*Request, *Options){
		"stage":            func(request *Request, _ *Options) { request.Stages[0].Permission = domain.PermissionReadOnly },
		"agent":            func(request *Request, _ *Options) { request.Agents[0].Description = "other" },
		"skills":           func(request *Request, _ *Options) { request.Skills = []SkillBrief{{ID: "go", Description: "Go"}} },
		"objective":        func(request *Request, _ *Options) { request.Task.Objective = "Remove a config file" },
		"runtime":          func(_ *Request, options *Options) { options.Runtime = domain.RuntimeCodex },
		"model":            func(_ *Request, options *Options) { options.Model = "sonnet" },
		"reasoning effort": func(_ *Request, options *Options) { options.ReasoningEffort = "high" },
		"instructions":     func(_ *Request, options *Options) { options.Instructions = "Plan carefully." },
	}
	for name, change := range changed {
		current, currentOptions := request(), options
		change(&current, &currentOptions)
		if key(current, currentOptions) == base {
			t.Errorf("a change of %s must change the key", name)
		}
	}
}

func TestDecideReportsEveryCallsUsageAndPromptSections(t *testing.T) {
	usage := domain.Usage{InputTokens: 12, CacheWriteTokens: 9000, OutputTokens: 400, ReportedCents: 3, Turns: 1}
	fake := &fakeRuntime{output: `{"assignments":[]}`, usage: usage}
	remote := RuntimeRemote{Runtime: fake}
	request := Request{Task: NewTaskBrief(sampleTask()), Stages: []StageBrief{{ID: "worker", Permission: domain.PermissionWorktree}}, Skills: []SkillBrief{{ID: "conventions", Description: "House rules"}}}
	_, call, err := remote.Decide(context.Background(), request, Options{RunID: "orch-1", WorkingDir: t.TempDir(), Instructions: "Plan."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if call.Usage != usage || call.Outcome != domain.OutcomeCompleted || call.StartedAt.IsZero() || call.FinishedAt.Before(call.StartedAt) {
		t.Fatalf("call = %+v", call)
	}
	names := make([]string, 0, len(call.Sections))
	total := 0
	for _, section := range call.Sections {
		names = append(names, section.Section)
		total += section.Bytes
	}
	if strings.Join(names, ",") != "schema,instructions,protocol,task,repository,stages,agents,skills,request" || total != len(fake.request.Prompt)+len(fake.request.Schema) {
		t.Fatalf("sections %v add to %d, want the %d bytes of prompt and schema", names, total, len(fake.request.Prompt)+len(fake.request.Schema))
	}
	if call.Sections[1].Bytes != len("Plan.") || call.Sections[7].Bytes != len("[\n    {\n      \"id\": \"conventions\",\n      \"description\": \"House rules\"\n    }\n  ]") {
		t.Fatalf("sections = %+v", call.Sections)
	}
	// A call that times out still reports what the runtime streamed.
	fake.err = context.DeadlineExceeded
	if _, call, err := remote.Decide(context.Background(), request, Options{RunID: "orch-2", WorkingDir: t.TempDir()}, nil); err == nil || call.Usage != usage || call.Outcome != domain.OutcomeTimedOut {
		t.Fatalf("timed-out call = %+v, %v", call, err)
	}
	if _, call, err := (RuntimeRemote{}).Decide(context.Background(), request, Options{}, nil); err == nil || call.Outcome != "" {
		t.Fatalf("no runtime means no call: %+v, %v", call, err)
	}
}

func TestValidateRejectsUnsafeDecisions(t *testing.T) {
	registry := agent.NewRegistry(agent.BuiltIn())
	stages := []StageBrief{{ID: "fix", Permission: domain.PermissionWorktree, Skills: []string{"go-desktop"}}}
	skills := map[string]bool{"go-desktop": true, "security-review": true}
	good := Decision{Assignments: []Assignment{{StageID: "fix", Agent: "implementer", Skills: []string{"security-review", "security-review"}, Complexity: domain.ComplexityAssessment{Score: 7, Rationale: "r"}, Rationale: " ok "}}}
	validated, err := Validate(good, stages, registry, skills)
	if err != nil {
		t.Fatal(err)
	}
	if got := validated.Assignments[0].Skills; len(got) != 2 || got[0] != "go-desktop" || got[1] != "security-review" {
		t.Fatalf("skills must merge stage skills and dedupe: %v", got)
	}
	if validated.Assignments[0].Rationale != "ok" {
		t.Fatal("rationale must be trimmed")
	}
	cases := map[string]Decision{
		"read-only agent on writable stage": {Assignments: []Assignment{{StageID: "fix", Agent: "reviewer", Complexity: domain.ComplexityAssessment{Score: 5, Rationale: "r"}}}},
		"unknown agent":                     {Assignments: []Assignment{{StageID: "fix", Agent: "ghost", Complexity: domain.ComplexityAssessment{Score: 5, Rationale: "r"}}}},
		"orchestrator as worker":            {Assignments: []Assignment{{StageID: "fix", Agent: agent.Orchestrator, Complexity: domain.ComplexityAssessment{Score: 5, Rationale: "r"}}}},
		"bad score":                         {Assignments: []Assignment{{StageID: "fix", Agent: "implementer", Complexity: domain.ComplexityAssessment{Score: 11, Rationale: "r"}}}},
		"unknown skill":                     {Assignments: []Assignment{{StageID: "fix", Agent: "implementer", Skills: []string{"nope"}, Complexity: domain.ComplexityAssessment{Score: 5, Rationale: "r"}}}},
		"missing stage":                     {Assignments: []Assignment{}},
		"duplicate stage": {Assignments: []Assignment{
			{StageID: "fix", Agent: "implementer", Complexity: domain.ComplexityAssessment{Score: 5, Rationale: "r"}},
			{StageID: "fix", Agent: "implementer", Complexity: domain.ComplexityAssessment{Score: 5, Rationale: "r"}},
		}},
	}
	for name, decision := range cases {
		if _, err := Validate(decision, stages, registry, skills); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestFallbackPicksSpecialistWithAdequatePermission(t *testing.T) {
	registry := agent.NewRegistry(agent.BuiltIn())
	task := sampleTask()
	task.Kind = domain.TaskBugfix
	stages := []StageBrief{{ID: "reproduce", Permission: domain.PermissionReadOnly}, {ID: "fix", Permission: domain.PermissionWorktree}}
	decision, err := Fallback(task, stages, registry, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Assignments[0].Agent != "implementer" && decision.Assignments[0].Agent != "researcher" {
		t.Fatalf("reproduce should go to a bugfix specialist, got %s", decision.Assignments[0].Agent)
	}
	if decision.Assignments[1].Agent != "implementer" {
		t.Fatalf("fix needs a writable agent, got %s", decision.Assignments[1].Agent)
	}
	if decision.Assignments[1].Complexity.Score != 7 {
		t.Fatalf("bugfix should score 7, got %d", decision.Assignments[1].Complexity.Score)
	}
	task.Kind = domain.TaskReview
	readOnly := make([]agent.Definition, 0)
	for _, definition := range agent.BuiltIn() {
		if definition.Permission == domain.PermissionReadOnly && definition.Name != agent.Orchestrator {
			readOnly = append(readOnly, definition)
		}
	}
	if _, err := Fallback(task, []StageBrief{{ID: "fix", Permission: domain.PermissionWorktree}}, agent.NewRegistry(readOnly), nil); err == nil {
		t.Fatal("a registry with only read-only agents cannot staff a writable stage")
	}
	if _, err := Fallback(sampleTask(), []StageBrief{{ID: "w", Permission: domain.PermissionWorktree, Skills: []string{"missing"}}}, registry, map[string]bool{}); err == nil {
		t.Fatal("a stage requiring an unavailable skill must fail consistently")
	}
}

// The fallback takes the first capable specialist, so a built-in that serves
// the same kinds as implementer could take its writable stages whenever the
// orchestrator is unavailable. This asserts it does not, instead of relying on
// how the names sort.
func TestFallbackKeepsWritableStagesWithImplementer(t *testing.T) {
	registry := agent.NewRegistry(agent.BuiltIn())
	for _, name := range []string{"planner", "test-writer"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("expected built-in %s in the registry", name)
		}
	}
	implementer, _ := registry.Get("implementer")
	stages := []StageBrief{{ID: "plan", Permission: domain.PermissionReadOnly}, {ID: "implement", Permission: domain.PermissionWorktree}, {ID: "tests", Permission: domain.PermissionWorktree}}
	for _, kind := range implementer.TaskKinds {
		task := sampleTask()
		task.Kind = kind
		decision, err := Fallback(task, stages, registry, map[string]bool{})
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		for i, stage := range stages {
			if stage.Permission == domain.PermissionWorktree && decision.Assignments[i].Agent != "implementer" {
				t.Errorf("%s: fallback staffs writable stage %s with %s, want implementer", kind, stage.ID, decision.Assignments[i].Agent)
			}
		}
	}
}

func TestRemoteAgentsAreStaffedOnlyWhenMarkedAuto(t *testing.T) {
	stages := []StageBrief{{ID: "advise", Permission: domain.PermissionReadOnly}}
	decision := Decision{Assignments: []Assignment{{StageID: "advise", Agent: "advisor", Complexity: domain.ComplexityAssessment{Score: 4, Rationale: "r"}}}}
	registry := func(auto bool) agent.Registry {
		merged, _ := agent.NewRegistry(agent.BuiltIn()).WithRemote([]agent.Definition{{Name: "advisor", Description: "Remote advisor", Auto: auto}})
		return merged
	}
	if _, err := Validate(decision, stages, registry(false), nil); err == nil || !strings.Contains(err.Error(), "only a rule") {
		t.Fatalf("the orchestrator may not pick a remote agent that is not marked auto: %v", err)
	}
	if _, err := Validate(decision, stages, registry(true), nil); err != nil {
		t.Fatal(err)
	}
	writing := []StageBrief{{ID: "advise", Permission: domain.PermissionWorktree}}
	if _, err := Validate(decision, writing, registry(true), nil); err == nil {
		t.Fatal("a remote agent must not be given a writing stage")
	}
	// Deterministic staffing keeps work on the machine even for auto agents.
	only, _ := agent.NewRegistry(nil).WithRemote([]agent.Definition{{Name: "advisor", Description: "Remote advisor", Auto: true}})
	if _, err := Fallback(sampleTask(), stages, only, nil); err == nil {
		t.Fatal("the fallback must never choose a remote agent")
	}
}
