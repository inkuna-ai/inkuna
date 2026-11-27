package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	"github.com/inkuna-ai/inkuna/internal/repository"
	"github.com/inkuna-ai/inkuna/internal/rules"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/skill"
)

type fakeRemote struct {
	called   bool
	request  orchestrator.Request
	decision orchestrator.Decision
	err      error
}

func (fake *fakeRemote) Decide(_ context.Context, request orchestrator.Request, _ orchestrator.Options, _ agentruntime.EventSink) (orchestrator.Decision, orchestrator.Call, error) {
	fake.called = true
	fake.request = request
	return fake.decision, orchestrator.Call{Usage: domain.Usage{InputTokens: 7, OutputTokens: 3}, Outcome: agentruntime.Outcome(fake.err)}, fake.err
}

func testTask(kind domain.TaskKind) domain.TaskSpec {
	return domain.TaskSpec{Version: 1, ID: "task", Kind: kind, Objective: "Build", Repository: ".", Budget: domain.Budget{WallTimeSeconds: 1800}, AcceptanceCriteria: []string{"tests pass"}, Source: domain.TaskSource{Kind: "request"}, Priority: 1, Metadata: map[string]string{"created": time.Now().Format(time.RFC3339)}}
}

func packages() []skill.Package {
	return []skill.Package{
		{ID: "go", Name: "go", Description: "Go changes", Body: "secret instructions", Manifest: skill.Manifest{Version: 1, TaskKinds: []domain.TaskKind{domain.TaskImplementation}}},
		{ID: "security-review", Name: "security-review", Description: "Security checklist", Body: "top-secret", Manifest: skill.Manifest{Version: 1}},
	}
}

func TestOrchestratorStaffsAutoStagesAndRoutesModels(t *testing.T) {
	remote := &fakeRemote{decision: orchestrator.Decision{Notes: "implementer fits", Assignments: []orchestrator.Assignment{{
		StageID: "worker", Agent: "implementer", Skills: []string{"go"}, Complexity: domain.ComplexityAssessment{Score: 8, Rationale: "cross-cutting", Signals: []string{"two packages"}}, Rationale: "generalist",
	}}}}
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeClaude, Orchestrator: remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !remote.called || !result.Orchestrated || result.RuleName != "default" || !result.Workflow.Orchestrated {
		t.Fatalf("orchestrator was not used: %+v", result)
	}
	if result.Staffing == nil || result.Staffing.Usage.InputTokens != 7 || result.Staffing.Outcome != domain.OutcomeCompleted {
		t.Fatalf("the staffing call must be reported: %+v", result.Staffing)
	}
	serialized, _ := json.Marshal(remote.request)
	if strings.Contains(string(serialized), "secret") {
		t.Fatalf("skill bodies leaked to the orchestrator: %s", serialized)
	}
	if len(remote.request.Agents) == 0 || len(remote.request.Stages) != 1 || remote.request.Stages[0].Permission != domain.PermissionWorktree {
		t.Fatalf("unexpected orchestrator request %+v", remote.request)
	}
	worker := result.Workflow.Steps[1]
	if worker.Agent != "implementer" || worker.Complexity.Score != 8 || worker.ModelSelection == nil || worker.ModelSelection.Model != "opus" {
		t.Fatalf("worker was not staffed and routed: %+v", worker)
	}
	if len(worker.SelectedSkills) != 1 || worker.SelectedSkills[0] != "go" || worker.Metadata["assignment"] != "generalist" {
		t.Fatalf("assignment details missing: %+v", worker)
	}
	if err := domain.ValidateRoutedWorkflow(result.Workflow); err != nil {
		t.Fatal(err)
	}
}

func TestPlanningThatFailsAfterStaffingStillReportsTheCall(t *testing.T) {
	remote := &fakeRemote{decision: orchestrator.Decision{Assignments: []orchestrator.Assignment{{
		StageID: "worker", Agent: "implementer", Complexity: domain.ComplexityAssessment{Score: 6, Rationale: "one package"}, Rationale: "writes code",
	}}}}
	// Staffing succeeds; the plan then fails because Claude is not declared.
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeClaude, Orchestrator: remote, Runtimes: map[domain.Runtime]bool{domain.RuntimeCodex: true},
	})
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("an undeclared runtime must fail the plan: %v", err)
	}
	if result.Staffing == nil || result.Staffing.Usage.InputTokens != 7 {
		t.Fatalf("the paid staffing call must survive the failure: %+v", result.Staffing)
	}
}

func TestOrchestratorFailureFallsBackDeterministically(t *testing.T) {
	remote := &fakeRemote{err: errors.New("claude unavailable")}
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskBugfix), Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeCodex, Orchestrator: remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Orchestrated || result.RuleName != "bugfix" || len(result.Notes) == 0 || !strings.Contains(result.Notes[0], "claude unavailable") {
		t.Fatalf("fallback provenance missing: %+v", result)
	}
	if result.Staffing == nil || result.Staffing.Outcome != domain.OutcomeFailed || result.Staffing.Usage.OutputTokens != 3 {
		t.Fatalf("a failed staffing call is still reported: %+v", result.Staffing)
	}
	var reproduce, fix domain.WorkflowStep
	for _, step := range result.Workflow.Steps {
		switch step.ID {
		case "reproduce":
			reproduce = step
		case "fix":
			fix = step
		}
	}
	if reproduce.Agent != "researcher" || reproduce.Permission != domain.PermissionReadOnly || reproduce.ModelSelection == nil {
		t.Fatalf("rule-named agent not applied: %+v", reproduce)
	}
	if fix.Agent != "implementer" || fix.Permission != domain.PermissionWorktree || fix.ModelSelection.Model != "gpt-6.1-terra" {
		t.Fatalf("fallback staffing wrong: %+v", fix)
	}
}

func TestInvalidOrchestratorDecisionIsRejectedThenFallsBack(t *testing.T) {
	remote := &fakeRemote{decision: orchestrator.Decision{Assignments: []orchestrator.Assignment{{StageID: "worker", Agent: "reviewer", Complexity: domain.ComplexityAssessment{Score: 5, Rationale: "r"}}}}}
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeCodex, Orchestrator: remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Orchestrated || !strings.Contains(strings.Join(result.Notes, " "), "rejected") {
		t.Fatalf("read-only reviewer on a writable stage must be rejected: %+v", result)
	}
	if result.Workflow.Steps[1].Agent != "implementer" {
		t.Fatalf("fallback should choose implementer, got %s", result.Workflow.Steps[1].Agent)
	}
}

func TestStaffingRequestCarriesBriefsFilteredSkillsAndCappedManifests(t *testing.T) {
	document, err := rules.Parse("version: 1\nrules: []\ndefault:\n  stages:\n    - id: work\n      title: Work\n      kind: agent\n      agent: auto\n      permission: worktree-write\n      skills: [conventions]\n    - id: approval\n      kind: approval\n      dependsOn: [work]\n")
	if err != nil {
		t.Fatal(err)
	}
	migrations := agent.Definition{Name: "migration-writer", Description: "Writes migrations", Permission: domain.PermissionWorktree, TaskKinds: []domain.TaskKind{domain.TaskImplementation}, Skills: []string{"sqlite"}, Instructions: "x", Source: agent.SourceRepository}
	skills := append(packages(),
		skill.Package{ID: "conventions", Description: "House rules"},
		skill.Package{ID: "sqlite", Description: "Migrations"},
		skill.Package{ID: "doc-style", Description: "Doc style", Manifest: skill.Manifest{Version: 1, TaskKinds: []domain.TaskKind{domain.TaskDocumentation}}},
	)
	manifests := make([]string, 0, 60)
	for index := 0; index < 60; index++ {
		manifests = append(manifests, fmt.Sprintf("services/s%02d/go.mod", index))
	}
	remote := &fakeRemote{err: errors.New("unavailable")}
	if _, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskImplementation), Repository: repository.Profile{Root: "/Users/someone/repo", Branch: "main", Head: "0123abc", Languages: []string{"Go"}, Manifests: append(manifests, "go.mod")},
		Skills: skills, Rules: document, Registry: agent.NewRegistry(agent.BuiltIn(), []agent.Definition{migrations}), DefaultRuntime: domain.RuntimeClaude, Orchestrator: remote,
	}); err != nil {
		t.Fatal(err)
	}
	request := remote.request
	// go is for implementation, the stage names conventions, and an agent
	// names sqlite; nothing leads to security-review or doc-style.
	offered := make([]string, 0, len(request.Skills))
	for _, brief := range request.Skills {
		offered = append(offered, brief.ID)
	}
	if strings.Join(offered, ",") != "conventions,go,sqlite" {
		t.Fatalf("offered skills = %v", offered)
	}
	if len(request.Repository.Manifests) != orchestrator.ManifestLimit || request.Repository.Manifests[0] != "go.mod" || request.Repository.MoreManifests != 11 {
		t.Fatalf("manifests are not capped top-level first: %d listed starting %q, %d more", len(request.Repository.Manifests), request.Repository.Manifests[0], request.Repository.MoreManifests)
	}
	briefed := false
	for _, brief := range request.Agents {
		briefed = briefed || brief.Name == "migration-writer" && strings.Join(brief.Skills, ",") == "sqlite" && brief.Permission == domain.PermissionWorktree
	}
	if !briefed {
		t.Fatalf("the repository agent is not briefed with its skills: %+v", request.Agents)
	}
	encoded, _ := json.Marshal(request)
	for _, absent := range []string{"inkuna://", "protocolVersion", "defaultInputModes", "capabilities", "Handles ", "task:", "/Users/someone", "0123abc", "budget", "metadata"} {
		if strings.Contains(string(encoded), absent) {
			t.Errorf("the request carries %q: %s", absent, encoded)
		}
	}
	if strings.Count(string(encoded), "Migrations") != 1 {
		t.Fatalf("each skill must be described once: %s", encoded)
	}
}

// memoryCache is a DecisionCache in a map.
type memoryCache struct {
	decisions map[string]orchestrator.Decision
	at        time.Time
}

func (cache *memoryCache) Recall(_ context.Context, key string) (orchestrator.Decision, time.Time, bool, error) {
	decision, ok := cache.decisions[key]
	return decision, cache.at, ok, nil
}

func (cache *memoryCache) Remember(_ context.Context, key string, decision orchestrator.Decision) error {
	cache.decisions[key] = decision
	return nil
}

func TestReplanWithUnchangedInputsReusesTheDecision(t *testing.T) {
	cache := &memoryCache{decisions: map[string]orchestrator.Decision{}, at: time.Date(2026, 10, 18, 14, 2, 0, 0, time.UTC)}
	remote := &fakeRemote{decision: orchestrator.Decision{Notes: "implementer fits", Assignments: []orchestrator.Assignment{{
		StageID: "worker", Agent: "implementer", Skills: []string{"go"}, Complexity: domain.ComplexityAssessment{Score: 8, Rationale: "cross-cutting"}, Rationale: "generalist",
	}}}}
	plan := func(task domain.TaskSpec, document rules.Document, bypass bool) Result {
		t.Helper()
		remote.called = false
		result, err := Build(context.Background(), Input{
			Task: task, Skills: packages(), Rules: document, Registry: agent.NewRegistry(agent.BuiltIn()), DefaultRuntime: domain.RuntimeClaude,
			Orchestrator: remote, Options: orchestrator.Options{Runtime: domain.RuntimeClaude, RunID: "orchestrator-" + task.ID}, Cache: cache, BypassCache: bypass,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := plan(testTask(domain.TaskImplementation), rules.Default(), false)
	if !remote.called || len(cache.decisions) != 1 || !first.Reused.IsZero() {
		t.Fatalf("the first plan must ask the orchestrator and keep its decision: called=%v kept=%d", remote.called, len(cache.decisions))
	}

	// A replan is a new task with a new id and creation time.
	again := testTask(domain.TaskImplementation)
	again.ID, again.Metadata = "task-2", map[string]string{"created": "later"}
	reused := plan(again, rules.Default(), false)
	if remote.called || reused.Staffing != nil || !reused.Reused.Equal(cache.at) || !reused.Orchestrated {
		t.Fatalf("an identical replan must reuse the decision without a call: called=%v %+v", remote.called, reused)
	}
	if worker := reused.Workflow.Steps[1]; worker.Agent != "implementer" || worker.Complexity.Score != 8 || strings.Join(reused.Notes, " ") != "orchestrator: implementer fits" {
		t.Fatalf("the reused decision was not applied: %+v %v", worker, reused.Notes)
	}

	// Re-staff asks again, and so does a plan whose auto stage changed.
	if plan(again, rules.Default(), true); !remote.called {
		t.Fatal("re-staffing must ask the orchestrator")
	}
	retitled, err := rules.Parse("version: 1\nrules: []\ndefault:\n  stages:\n    - id: worker\n      title: Build it\n      kind: agent\n      agent: auto\n      permission: worktree-write\n    - id: approval\n      kind: approval\n      dependsOn: [worker]\n")
	if err != nil {
		t.Fatal(err)
	}
	if plan(again, retitled, false); !remote.called || len(cache.decisions) != 2 {
		t.Fatalf("a changed auto stage must ask the orchestrator: called=%v kept=%d", remote.called, len(cache.decisions))
	}

	// A stored decision Go no longer accepts is asked for again.
	for key := range cache.decisions {
		cache.decisions[key] = orchestrator.Decision{Assignments: []orchestrator.Assignment{{StageID: "worker", Agent: "ghost", Complexity: domain.ComplexityAssessment{Score: 5, Rationale: "r"}}}}
	}
	if result := plan(again, rules.Default(), false); !remote.called || !result.Reused.IsZero() || result.Workflow.Steps[1].Agent != "implementer" {
		t.Fatalf("an invalid stored decision must not be used: called=%v %+v", remote.called, result)
	}
}

func TestStaffingWithReadToolsIsNeverReused(t *testing.T) {
	// A call that reads the repository answers from files no key covers, so
	// a decision made before the code changed could be reused after it.
	cache := &memoryCache{decisions: map[string]orchestrator.Decision{}}
	remote := &fakeRemote{decision: orchestrator.Decision{Assignments: []orchestrator.Assignment{{
		StageID: "worker", Agent: "implementer", Complexity: domain.ComplexityAssessment{Score: 6, Rationale: "one package"}, Rationale: "writes code",
	}}}}
	for attempt := 0; attempt < 2; attempt++ {
		remote.called = false
		result, err := Build(context.Background(), Input{
			Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()), DefaultRuntime: domain.RuntimeClaude,
			Orchestrator: remote, Options: orchestrator.Options{Runtime: domain.RuntimeClaude, ReadTools: true}, Cache: cache,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !remote.called || !result.Reused.IsZero() || len(cache.decisions) != 0 {
			t.Fatalf("plan %d with read tools must ask the orchestrator and keep nothing: called=%v kept=%d", attempt+1, remote.called, len(cache.decisions))
		}
	}
}

func TestAgentDefinitionsPinRuntimeModelAndTurns(t *testing.T) {
	custom := agent.Definition{Name: "fast-fixer", Description: "pins codex", Runtime: domain.RuntimeCodex, Model: "gpt-5.6-sol", ReasoningEffort: "high", Permission: domain.PermissionWorktree, MaxTurns: 9, Skills: []string{"go", "missing-elsewhere"}, Instructions: "go fast", Source: agent.SourceRepository}
	registry := agent.NewRegistry(agent.BuiltIn(), []agent.Definition{custom})
	document, err := rules.Parse("version: 1\nrules: []\ndefault:\n  stages:\n    - id: work\n      kind: agent\n      agent: fast-fixer\n    - id: approval\n      kind: approval\n      dependsOn: [work]\n")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Build(context.Background(), Input{Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: document, Registry: registry, DefaultRuntime: domain.RuntimeClaude, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	work := result.Workflow.Steps[0]
	if work.Runtime != domain.RuntimeCodex || work.ModelSelection.Model != "gpt-5.6-sol" || work.ModelSelection.Source != "override" || work.Budget.MaxTurns != 9 {
		t.Fatalf("agent pins were not applied: %+v", work)
	}
	if len(work.SelectedSkills) != 1 || work.SelectedSkills[0] != "go" {
		t.Fatalf("agent skills absent from the repository must be skipped, got %v", work.SelectedSkills)
	}
	if len(result.Notes) != 0 || result.Orchestrated {
		t.Fatalf("with the orchestrator off and no auto stage, nothing is orchestrated and nothing needs a note: %+v", result)
	}
}

func TestRuleCannotWidenReadOnlyAgent(t *testing.T) {
	document, err := rules.Parse("version: 1\nrules: []\ndefault:\n  stages:\n    - id: work\n      kind: agent\n      agent: reviewer\n      permission: worktree-write\n    - id: approval\n      kind: approval\n      dependsOn: [work]\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(context.Background(), Input{Task: testTask(domain.TaskImplementation), Rules: document, Registry: agent.NewRegistry(agent.BuiltIn()), DefaultRuntime: domain.RuntimeClaude, Disabled: true, Repository: repository.Profile{}})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected permission error, got %v", err)
	}
}

func TestAReviewGateWithNothingToSendFeedbackToSaysSo(t *testing.T) {
	document, err := rules.Parse("version: 1\nrules: []\ndefault:\n  stages:\n    - id: work\n      kind: agent\n      agent: researcher\n    - id: approval\n      kind: approval\n      dependsOn: [work]\n      review: true\n")
	if err != nil {
		t.Fatal(err)
	}
	input := Input{Task: testTask(domain.TaskResearch), Rules: document, Registry: agent.NewRegistry(agent.BuiltIn()), DefaultRuntime: domain.RuntimeClaude, Disabled: true, Repository: repository.Profile{}}
	result, err := Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(result.Notes, `Review gate "approval" has no writable agent stage upstream for research tasks, so it can only be approved.`) {
		t.Fatalf("notes %q", result.Notes)
	}
	input.Task = testTask(domain.TaskImplementation)
	if input.Rules, err = rules.Parse("version: 1\nrules: []\ndefault:\n  stages:\n    - id: work\n      kind: agent\n      agent: implementer\n    - id: approval\n      kind: approval\n      dependsOn: [work]\n      review: true\n"); err != nil {
		t.Fatal(err)
	}
	if result, err = Build(context.Background(), input); err != nil || len(result.Notes) != 0 || result.Workflow.Steps[1].FeedbackTarget != "work" {
		t.Fatalf("a gate with a writable stage upstream needs no note: %q %v", result.Notes, err)
	}
}

func TestPlansResolveRuntimesAgainstTheDeclaredOnes(t *testing.T) {
	build := func(defaultRuntime domain.Runtime, kind domain.TaskKind, runtimes map[domain.Runtime]bool) (Result, error) {
		return Build(context.Background(), Input{
			Task: testTask(kind), Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()),
			DefaultRuntime: defaultRuntime, Disabled: true, Runtimes: runtimes,
		})
	}
	declared := map[domain.Runtime]bool{domain.RuntimeClaude: true, "gemini": false}

	// A declared runtime without routing bands leaves the model to its CLI.
	result, err := build("gemini", domain.TaskResearch, declared)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range result.Workflow.Steps {
		if step.Kind == domain.StepAgent && (step.Runtime != "gemini" || step.ModelSelection == nil || step.ModelSelection.Model != "" || step.ModelSelection.Source != "policy") {
			t.Fatalf("step was not routed to the declared runtime: %+v", step)
		}
	}
	if err := domain.ValidateRoutedWorkflow(result.Workflow); err != nil {
		t.Fatal(err)
	}
	// A plan that writes cannot use a runtime that declares no write arguments.
	if _, err := build("gemini", domain.TaskImplementation, declared); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("a write step on a read-only runtime must be refused: %v", err)
	}
	if _, err := build("qwen", domain.TaskResearch, declared); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("an undeclared runtime must be refused: %v", err)
	}
	// Without declarations the three built-ins are what exists.
	if _, err := build(domain.RuntimeCopilot, domain.TaskImplementation, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := build("gemini", domain.TaskResearch, nil); err == nil {
		t.Fatal("only built-in runtimes exist when none are declared")
	}
}

func remoteRegistry(auto bool) agent.Registry {
	registry, _ := agent.NewRegistry(agent.BuiltIn()).WithRemote([]agent.Definition{{Name: "security-advisor", Description: "Reviews plans for security issues", Auto: auto}})
	return registry
}

const advisorRules = "version: 1\nrules: []\ndefault:\n  stages:\n    - id: advice\n      kind: agent\n      agent: security-advisor\n      permission: %s\n      skills: [security-review]\n    - id: approval\n      kind: approval\n      dependsOn: [advice]\n"

func TestRemoteAdvisorStaffsAReadOnlyStage(t *testing.T) {
	document, err := rules.Parse(strings.Replace(advisorRules, "%s", "read-only", 1))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Build(context.Background(), Input{Task: testTask(domain.TaskResearch), Skills: packages(), Rules: document, Registry: remoteRegistry(false), DefaultRuntime: domain.RuntimeClaude, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	advice := result.Workflow.Steps[0]
	if advice.Agent != "security-advisor" || advice.Runtime != domain.RuntimeRemote || advice.Metadata["agentSource"] != "remote" {
		t.Fatalf("the rule's remote agent did not staff the stage: %+v", advice)
	}
	if advice.ModelSelection == nil || advice.ModelSelection.Source != domain.ModelSourceRemote || advice.ModelSelection.Model != "" {
		t.Fatalf("a remote step has no local model: %+v", advice.ModelSelection)
	}
	if len(advice.SelectedSkills) != 0 {
		t.Fatalf("local skills must not be selected for a remote agent: %v", advice.SelectedSkills)
	}
	if err := domain.ValidateRoutedWorkflow(result.Workflow); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteAdvisorCannotWrite(t *testing.T) {
	document, err := rules.Parse(strings.Replace(advisorRules, "%s", "worktree-write", 1))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(context.Background(), Input{Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: document, Registry: remoteRegistry(true), DefaultRuntime: domain.RuntimeClaude, Disabled: true})
	if err == nil || !strings.Contains(err.Error(), "read-only advisor") {
		t.Fatalf("a remote agent on a writing stage must fail validation: %v", err)
	}
	// Approval can change a step's runtime; the remote one never writes.
	workflow := domain.WorkflowSpec{Steps: []domain.WorkflowStep{{ID: "work", Kind: domain.StepAgent, Runtime: domain.RuntimeRemote, Permission: domain.PermissionWorktree}}}
	if err := CheckRuntimes(workflow, nil); err == nil {
		t.Fatal("the remote runtime must be refused for a writing step")
	}
}

func TestOrchestratorOnlySeesRemoteAgentsMarkedAuto(t *testing.T) {
	offered := func(auto bool) bool {
		remote := &fakeRemote{err: errors.New("unavailable")}
		if _, err := Build(context.Background(), Input{Task: testTask(domain.TaskResearch), Skills: packages(), Rules: rules.Default(), Registry: remoteRegistry(auto), DefaultRuntime: domain.RuntimeClaude, Orchestrator: remote}); err != nil {
			t.Fatal(err)
		}
		for _, brief := range remote.request.Agents {
			if brief.Name == "security-advisor" {
				return true
			}
		}
		return false
	}
	if offered(false) {
		t.Fatal("a remote agent that is not marked auto was offered to the orchestrator")
	}
	if !offered(true) {
		t.Fatal("a remote agent marked auto was not offered to the orchestrator")
	}
}

func TestUserChosenRuleOverridesMatching(t *testing.T) {
	result, err := Build(context.Background(), Input{
		Task: testTask(domain.TaskImplementation), Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, RuleName: "Read-Only",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RuleName != "read-only" || result.Workflow.RuleName != "read-only" || len(result.Workflow.Steps) != 3 {
		t.Fatalf("the chosen rule was not used: %+v", result)
	}
	if !strings.Contains(strings.Join(result.Notes, "\n"), `workflow "read-only" chosen by the user; the rules would have matched "default"`) {
		t.Fatalf("the override must be noted for the approval screen: %v", result.Notes)
	}
	_, err = Build(context.Background(), Input{
		Task: testTask(domain.TaskImplementation), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true, RuleName: "ghost",
	})
	if err == nil || !strings.Contains(err.Error(), `workflow rule "ghost" is not defined`) {
		t.Fatalf("an unknown rule must fail planning, got %v", err)
	}
}

func TestLabelSelectingARuleIsNoted(t *testing.T) {
	task := testTask(domain.TaskImplementation)
	task.Labels = []string{"bugfix"}
	result, err := Build(context.Background(), Input{
		Task: task, Skills: packages(), Rules: rules.Default(), Registry: agent.NewRegistry(agent.BuiltIn()),
		DefaultRuntime: domain.RuntimeClaude, Disabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RuleName != "bugfix" || !strings.Contains(strings.Join(result.Notes, "\n"), `workflow "bugfix" selected by a task label`) {
		t.Fatalf("label selection missing: rule=%s notes=%v", result.RuleName, result.Notes)
	}
}

func TestRestaffRebuildsAStepFromAnApprovalEdit(t *testing.T) {
	pinned := agent.Definition{Name: "fast-fixer", Description: "pins codex", Runtime: domain.RuntimeCodex, Model: "gpt-5.6-sol", Permission: domain.PermissionWorktree, MaxTurns: 9, Skills: []string{"go"}, Instructions: "go fast", Source: agent.SourceRepository}
	registry := agent.NewRegistry(agent.BuiltIn(), []agent.Definition{pinned})
	available := map[string]bool{"go": true, "security-review": true}
	planned := domain.WorkflowStep{
		ID: "work", Kind: domain.StepAgent, Agent: "fast-fixer", Runtime: domain.RuntimeCodex, Permission: domain.PermissionWorktree,
		SelectedSkills: []string{"go", "security-review"}, Budget: domain.Budget{WallTimeSeconds: 60, MaxTurns: 9},
		Complexity:     &domain.ComplexityAssessment{Score: 5, Rationale: "planned"},
		ModelSelection: &domain.ModelSelection{Model: "gpt-5.6-sol", Source: "override"},
		Metadata:       map[string]string{"rule": "default", "assignment": "rule default names fast-fixer", "agentSource": "repository"},
	}

	// Another agent: its pins, skills, and turn budget leave with it; the
	// stage's own skill stays; the user's runtime and score are applied.
	step := planned
	step.SelectedSkills = append([]string(nil), planned.SelectedSkills...)
	step.Metadata = map[string]string{"rule": "default", "assignment": "rule default names fast-fixer", "agentSource": "repository"}
	if err := Restaff(&step, Edit{Agent: "implementer", Runtime: domain.RuntimeClaude, Score: 8}, registry, available, nil, nil); err != nil {
		t.Fatal(err)
	}
	if step.Agent != "implementer" || step.Runtime != domain.RuntimeClaude || step.Budget.MaxTurns != 0 || step.ModelSelection != nil {
		t.Fatalf("the old agent's pins survived: %+v", step)
	}
	if len(step.SelectedSkills) != 1 || step.SelectedSkills[0] != "security-review" {
		t.Fatalf("the old agent's skills must leave and the stage's stay, got %v", step.SelectedSkills)
	}
	if step.Complexity.Score != 8 || !strings.HasPrefix(step.Complexity.Rationale, "set to 8 at approval; the plan scored 5") {
		t.Fatalf("score edit not recorded: %+v", step.Complexity)
	}
	if step.Metadata["assignment"] != "chosen at approval; the plan assigned fast-fixer" || step.Metadata["agentSource"] != "builtin" {
		t.Fatalf("provenance not updated: %v", step.Metadata)
	}
	if planned.Complexity.Score != 5 {
		t.Fatal("restaffing must not mutate the planned assessment")
	}

	// The same agent keeps its provenance, and its pinned runtime beats the
	// edit's.
	same := planned
	same.SelectedSkills = append([]string(nil), planned.SelectedSkills...)
	same.Metadata = map[string]string{"assignment": "rule default names fast-fixer"}
	if err := Restaff(&same, Edit{Agent: "fast-fixer", Runtime: domain.RuntimeClaude, Score: 5}, registry, available, nil, nil); err != nil {
		t.Fatal(err)
	}
	if same.Runtime != domain.RuntimeCodex || same.ModelSelection == nil || same.ModelSelection.Model != "gpt-5.6-sol" || same.Metadata["assignment"] != "rule default names fast-fixer" || same.Budget.MaxTurns != 9 {
		t.Fatalf("a pinned agent's runtime and model must hold: %+v", same)
	}

	// A read-only agent cannot take a writable stage; unknown, auto, and
	// out-of-range edits are refused.
	for name, edit := range map[string]Edit{
		"read-only":        {Agent: "reviewer", Runtime: domain.RuntimeClaude, Score: 5},
		"not defined":      {Agent: "ghost", Runtime: domain.RuntimeClaude, Score: 5},
		"not been staffed": {Agent: "auto", Runtime: domain.RuntimeClaude, Score: 5},
		"1 to 10":          {Agent: "implementer", Runtime: domain.RuntimeClaude, Score: 11},
	} {
		refused := planned
		if err := Restaff(&refused, edit, registry, available, nil, nil); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: expected a refusal, got %v", name, err)
		}
	}
	script := domain.WorkflowStep{ID: "profile", Kind: domain.StepScript}
	if err := Restaff(&script, Edit{}, registry, available, nil, nil); err != nil || script.Agent != "" {
		t.Fatalf("non-agent steps are left alone: %+v %v", script, err)
	}
}
