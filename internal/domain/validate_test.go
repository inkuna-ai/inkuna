package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidateWorkflowRejectsCycleAndUnsafeWrites(t *testing.T) {
	workflow := WorkflowSpec{Version: 1, ID: "w1", TaskID: "t1", DefaultRuntime: RuntimeCodex, MaxConcurrency: 2, Steps: []WorkflowStep{
		{ID: "a", Title: "A", Kind: StepAgent, Permission: PermissionReadOnly, DependsOn: []string{"b"}},
		{ID: "b", Title: "B", Kind: StepAgent, Permission: PermissionReadOnly, DependsOn: []string{"a"}},
	}}
	if err := ValidateWorkflow(workflow); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
	workflow.Steps = []WorkflowStep{{ID: "a", Title: "A", Kind: StepAgent, Permission: PermissionWorktree, AllowedPaths: []string{"../outside"}}}
	if err := ValidateWorkflow(workflow); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("expected unsafe path error, got %v", err)
	}
}

func TestAllowedPathsStayInsideTheRepository(t *testing.T) {
	for _, inside := range []string{".", "internal/", "docs", "./frontend/src", "a/../b", `internal\app`} {
		if err := CheckAllowedPath(inside); err != nil {
			t.Errorf("%q was refused: %v", inside, err)
		}
	}
	for _, outside := range []string{"", "  ", "..", "../sibling", "a/../..", "a/../../b", "/etc", `\Windows`, `C:\Users`, "C:x", `..\up`} {
		if err := CheckAllowedPath(outside); err == nil {
			t.Errorf("%q was accepted", outside)
		}
	}
}

func TestBuildContextDisclosesSelectedSkillsAndHandoffsOnly(t *testing.T) {
	task := TaskSpec{Version: 1, ID: "task", Kind: TaskImplementation, Objective: "Build it", Repository: ".", Budget: Budget{WallTimeSeconds: 300}}
	step := WorkflowStep{ID: "code", Title: "Code", Kind: StepAgent, Permission: PermissionReadOnly, SelectedSkills: []string{"chosen"}, Budget: Budget{WallTimeSeconds: 60}}
	dependencies := []HandoffResult{{
		Summary: "Dependency passed", Decisions: []string{"Keep API"}, Risks: []string{"Open: which locale?"}, Verification: []string{"go test ./..."},
		Artifacts: []ArtifactRef{{Digest: strings.Repeat("a", 64)}}, Outputs: []ArtifactOutput{{Path: ".inkuna/out/plan.md", Description: "The plan"}},
	}}
	context, err := BuildContext(task, step, dependencies, map[string]string{"chosen": "full selected body", "unrelated": "must stay hidden"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if context.SkillExcerpts["chosen"] == "" {
		t.Fatal("selected skill not included")
	}
	if _, present := context.SkillExcerpts["unrelated"]; present {
		t.Fatal("unrelated skill leaked")
	}
	if len(context.DependencySummary) != 1 || context.DependencySummary[0] != "Dependency passed" {
		t.Fatalf("unexpected handoff: %#v", context)
	}
	// A researcher's open questions are its risks; the next step needs them.
	if !reflect.DeepEqual(context.RelevantDecisions, []string{"Keep API"}) || !reflect.DeepEqual(context.RelevantRisks, []string{"Open: which locale?"}) {
		t.Fatalf("decisions = %v, risks = %v", context.RelevantDecisions, context.RelevantRisks)
	}
	if !reflect.DeepEqual(context.DependencyOutputs, dependencies[0].Outputs) {
		t.Fatalf("outputs = %v", context.DependencyOutputs)
	}
}

func TestBuildContextCarriesTheStageAndTheTaskConstraints(t *testing.T) {
	task := TaskSpec{
		Version: 1, ID: "task", Kind: TaskBugfix, Objective: "Fix the crash on save", Repository: ".", Budget: Budget{WallTimeSeconds: 300},
		Constraints: []string{"Don't change the public API"}, Labels: []string{"label-picks-the-rule"},
	}
	step := WorkflowStep{
		ID: "reproduce", Title: "Reproduce and locate the defect", Kind: StepAgent, Agent: "researcher", Permission: PermissionReadOnly,
		Prompt: "Find the failing path; do not fix it.", Budget: Budget{WallTimeSeconds: 60},
	}
	envelope, err := BuildContext(task, step, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.StageTitle != step.Title || envelope.StagePrompt != step.Prompt || !reflect.DeepEqual(envelope.Constraints, task.Constraints) {
		t.Fatalf("stage %q %q, constraints %v", envelope.StageTitle, envelope.StagePrompt, envelope.Constraints)
	}
	// Labels select the rule; they say nothing to the worker.
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "label-picks-the-rule") {
		t.Fatalf("the envelope carries the task's labels: %s", encoded)
	}
	// A remote advisor already had the stage; the constraints bind it too.
	advisor, err := BuildAdvisorHandoff(task, step, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if advisor.Stage.Title != step.Title || advisor.Stage.Prompt != step.Prompt || !reflect.DeepEqual(advisor.Constraints, task.Constraints) {
		t.Fatalf("advisor stage %+v, constraints %v", advisor.Stage, advisor.Constraints)
	}
}

func TestBuildContextGivesEveryAgentStepTheDocIndexOnly(t *testing.T) {
	task := TaskSpec{Version: 1, ID: "task", Kind: TaskImplementation, Objective: "Build it", Repository: ".", Budget: Budget{WallTimeSeconds: 300}}
	index := []DocBrief{
		{Path: "docs/architecture.md", Title: "Architecture", Tags: []string{"architecture"}, Status: "current", LastUpdated: "2026-10-01", Author: "A. Author"},
		{Path: "docs/legacy.md", Title: "Legacy", Tags: []string{"old"}, Status: DocStatusDeprecated, LastUpdated: "2025-01-01", Author: "A. Author"},
		{Path: "docs/notes.md", Problems: []string{"missing frontmatter"}},
	}
	// Steps a rule staffed by name and steps the orchestrator staffed look
	// the same here; either way the index arrives.
	steps := []WorkflowStep{
		{ID: "research", Title: "Research", Kind: StepAgent, Agent: "researcher", Permission: PermissionReadOnly, Budget: Budget{WallTimeSeconds: 60}},
		{ID: "code", Title: "Code", Kind: StepAgent, Agent: "implementer", Permission: PermissionWorktree, AllowedPaths: []string{"."}, Budget: Budget{WallTimeSeconds: 60}},
	}
	allowed := map[string]bool{"path": true, "title": true, "tags": true, "status": true, "lastUpdated": true, "author": true, "problems": true}
	for _, step := range steps {
		envelope, err := BuildContext(task, step, nil, nil, index)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(envelope.Docs, index) || envelope.Metadata[MetadataDocsTruncated] != "" {
			t.Fatalf("%s: docs = %#v, metadata = %v", step.ID, envelope.Docs, envelope.Metadata)
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Docs []map[string]json.RawMessage `json:"docs"`
		}
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		for _, doc := range decoded.Docs {
			for key := range doc {
				if !allowed[key] {
					t.Fatalf("%s: doc entry has a %q field beyond the frontmatter", step.ID, key)
				}
			}
		}
	}
	advisor, err := BuildAdvisorHandoff(task, steps[0], nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(advisor)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{`"docs"`, "docs/architecture.md", "Architecture"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("the advisor handoff carries %s: %s", private, encoded)
		}
	}
}

func TestBuildContextTruncatesTheDocIndex(t *testing.T) {
	task := TaskSpec{Version: 1, ID: "task", Kind: TaskImplementation, Objective: "Build it", Repository: ".", Budget: Budget{WallTimeSeconds: 300}}
	step := WorkflowStep{ID: "code", Title: "Code", Kind: StepAgent, Permission: PermissionReadOnly, Budget: Budget{WallTimeSeconds: 60}}
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	current := func(count int, title string) []DocBrief {
		docs := make([]DocBrief, 0, count)
		for i := range count {
			docs = append(docs, DocBrief{Path: fmt.Sprintf("docs/%04d.md", i), Title: title, Status: "current", LastUpdated: day.AddDate(0, 0, i).Format("2006-01-02")})
		}
		return docs
	}
	paths := func(docs []DocBrief) map[string]bool {
		set := make(map[string]bool, len(docs))
		for _, doc := range docs {
			set[doc.Path] = true
		}
		return set
	}

	// Exactly at the entry limit nothing is dropped.
	envelope, err := BuildContext(task, step, nil, nil, current(MaxDocBriefs, "Doc"))
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.Docs) != MaxDocBriefs || envelope.Metadata[MetadataDocsTruncated] != "" {
		t.Fatalf("an index at the limit was cut: %d docs, metadata %v", len(envelope.Docs), envelope.Metadata)
	}

	// Over the entry limit: deprecated docs go first even when newest, then
	// an undated doc, then the oldest dated ones.
	index := current(MaxDocBriefs, "Doc")
	index = append(index,
		DocBrief{Path: "docs/deprecated-a.md", Status: DocStatusDeprecated, LastUpdated: "2030-01-01"},
		DocBrief{Path: "docs/broken.md", LastUpdated: "24/09/2026", Problems: []string{"lastUpdated must be a date"}},
		DocBrief{Path: "docs/deprecated-b.md", Status: DocStatusDeprecated, LastUpdated: "2030-01-02"},
		DocBrief{Path: "docs/zz-newest.md", Status: "current", LastUpdated: "2031-01-01"},
	)
	envelope, err = BuildContext(task, step, nil, nil, index)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.Docs) != MaxDocBriefs || envelope.Metadata[MetadataDocsTruncated] != "true" {
		t.Fatalf("expected %d docs and the flag, got %d and %v", MaxDocBriefs, len(envelope.Docs), envelope.Metadata)
	}
	kept := paths(envelope.Docs)
	for _, dropped := range []string{"docs/deprecated-a.md", "docs/deprecated-b.md", "docs/broken.md", "docs/0000.md"} {
		if kept[dropped] {
			t.Fatalf("%s should have been dropped", dropped)
		}
	}
	if !kept["docs/0001.md"] || !kept["docs/zz-newest.md"] {
		t.Fatal("dropped more than the limit required")
	}
	if envelope.Docs[0].Path != "docs/0001.md" || envelope.Docs[len(envelope.Docs)-1].Path != "docs/zz-newest.md" {
		t.Fatal("kept docs must stay in index order")
	}

	// Over the byte limit with few entries: the oldest go until it fits.
	index = current(100, strings.Repeat("t", 1000))
	envelope, err = BuildContext(task, step, nil, nil, index)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(envelope.Docs)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxDocBriefBytes || envelope.Metadata[MetadataDocsTruncated] != "true" {
		t.Fatalf("index is %d bytes with metadata %v", len(encoded), envelope.Metadata)
	}
	dropped := len(index) - len(envelope.Docs)
	if dropped == 0 || envelope.Docs[0].Path != index[dropped].Path {
		t.Fatalf("expected the %d oldest docs dropped, kept from %s", dropped, envelope.Docs[0].Path)
	}
	// One more doc back would not have fit.
	if withNext, _ := json.Marshal(index[dropped-1:]); len(withNext) <= MaxDocBriefBytes {
		t.Fatal("dropped more than the byte limit required")
	}
}

func TestValidateRoutedWorkflowRequiresScoreAndModel(t *testing.T) {
	workflow := WorkflowSpec{Version: 1, ID: "workflow", TaskID: "task", DefaultRuntime: RuntimeCopilot, RoutingPolicyVersion: 1, MaxConcurrency: 1, Steps: []WorkflowStep{{
		ID: "agent", Title: "Agent", Kind: StepAgent, Runtime: RuntimeCopilot, Permission: PermissionReadOnly,
		Complexity:     &ComplexityAssessment{Score: 5, Rationale: "moderate change"},
		ModelSelection: &ModelSelection{Model: "claude-sonnet-4.6", Source: "policy", PolicyVersion: 1, Rationale: "middle band"},
	}}}
	if err := ValidateRoutedWorkflow(workflow); err != nil {
		t.Fatal(err)
	}
	workflow.Steps[0].Complexity = nil
	if err := ValidateRoutedWorkflow(workflow); err == nil || !strings.Contains(err.Error(), "complexity") {
		t.Fatalf("expected missing score error, got %v", err)
	}
}

func TestUsageAddsAndTotalsInput(t *testing.T) {
	first := Usage{InputTokens: 1, CacheWriteTokens: 2, LongCacheWriteTokens: 1, CacheReadTokens: 3, OutputTokens: 4, ReportedCents: 5, PremiumRequests: 0.5, Turns: 6}
	sum := first.Add(Usage{InputTokens: 10, CacheWriteTokens: 20, LongCacheWriteTokens: 10, CacheReadTokens: 30, OutputTokens: 40, ReportedCents: 50, PremiumRequests: 2, Turns: 60})
	if sum != (Usage{InputTokens: 11, CacheWriteTokens: 22, LongCacheWriteTokens: 11, CacheReadTokens: 33, OutputTokens: 44, ReportedCents: 55, PremiumRequests: 2.5, Turns: 66}) || sum.TotalInputTokens() != 66 {
		t.Fatalf("sum = %+v, total input %d", sum, sum.TotalInputTokens())
	}
}

func TestCostTotalsKeepSourcesApart(t *testing.T) {
	calls := []UsageRecord{
		{Cost: Cost{Cents: 40, Source: CostReported, EstimatedCents: 38}},
		{Cost: Cost{Cents: 2.5, Source: CostEstimated, EstimatedCents: 2.5}},
		{Cost: Cost{Cents: 0.5, Source: CostEstimated, EstimatedCents: 0.5}},
		{Cost: Cost{Source: CostUnknown}},
		// A call stored before costs had sources is unknown too.
		{},
	}
	total := TotalCost(calls)
	if total != (CostTotal{ReportedCents: 40, EstimatedCents: 3, UnknownCalls: 2}) || total.BestCents() != 43 {
		t.Fatalf("total = %+v, best %v", total, total.BestCents())
	}
}

func TestValidateWorkflowKeepsFeedbackOnWritableAgentsUpstream(t *testing.T) {
	plan := func(change func(steps []WorkflowStep)) WorkflowSpec {
		steps := []WorkflowStep{
			{ID: "write", Title: "Write", Kind: StepAgent, Permission: PermissionWorktree, AllowedPaths: []string{"."}},
			{ID: "notes", Title: "Notes", Kind: StepAgent, Permission: PermissionReadOnly},
			{ID: "verify", Title: "Verify", Kind: StepEvaluation, Permission: PermissionReadOnly, DependsOn: []string{"write", "notes"}, OnFailure: FailureFeedback, FeedbackTarget: "write", MaxRounds: 2},
			{ID: "review", Title: "Review", Kind: StepApproval, Permission: PermissionReadOnly, DependsOn: []string{"verify"}, Review: true, FeedbackTarget: "write"},
		}
		change(steps)
		return WorkflowSpec{Version: 1, ID: "w1", TaskID: "t1", DefaultRuntime: RuntimeCodex, MaxConcurrency: 2, Steps: steps}
	}
	if err := ValidateWorkflow(plan(func([]WorkflowStep) {})); err != nil {
		t.Fatalf("a valid gate and loop: %v", err)
	}
	if err := ValidateWorkflow(plan(func(steps []WorkflowStep) { steps[3].FeedbackTarget = "" })); err != nil {
		t.Fatalf("a gate with no target can still be approved: %v", err)
	}
	if err := ValidateWorkflow(plan(func(steps []WorkflowStep) { steps[2].SystemExitCodes = []int{3, 75} })); err != nil {
		t.Fatalf("a loop that names system exit codes: %v", err)
	}
	for name, change := range map[string]func(steps []WorkflowStep){
		"review on an agent":    func(steps []WorkflowStep) { steps[0].Review = true },
		"read-only target":      func(steps []WorkflowStep) { steps[2].FeedbackTarget = "notes" },
		"missing target":        func(steps []WorkflowStep) { steps[3].FeedbackTarget = "gone" },
		"downstream target":     func(steps []WorkflowStep) { steps[2].FeedbackTarget = "review" },
		"evaluation target":     func(steps []WorkflowStep) { steps[3].FeedbackTarget = "verify" },
		"loop without target":   func(steps []WorkflowStep) { steps[2].FeedbackTarget = "" },
		"loop without rounds":   func(steps []WorkflowStep) { steps[2].MaxRounds = 0 },
		"loop over the cap":     func(steps []WorkflowStep) { steps[2].MaxRounds = MaxFeedbackRounds + 1 },
		"rounds without a loop": func(steps []WorkflowStep) { steps[2].OnFailure = "" },
		"loop on an approval":   func(steps []WorkflowStep) { steps[3].OnFailure, steps[3].MaxRounds = FailureFeedback, 1 },
		"unknown action":        func(steps []WorkflowStep) { steps[2].OnFailure = "retry" },
		"fail on an agent":      func(steps []WorkflowStep) { steps[0].OnFailure = FailureFail },
		"fail on an approval":   func(steps []WorkflowStep) { steps[3].OnFailure = FailureFail },
		"target on an agent":    func(steps []WorkflowStep) { steps[1].FeedbackTarget = "write" },
		"evaluation target, no loop": func(steps []WorkflowStep) {
			steps[2].OnFailure, steps[2].MaxRounds = "", 0
		},
		"system codes, no loop": func(steps []WorkflowStep) {
			steps[2].OnFailure, steps[2].MaxRounds, steps[2].FeedbackTarget, steps[2].SystemExitCodes = "", 0, "", []int{3}
		},
		"system code 0":            func(steps []WorkflowStep) { steps[2].SystemExitCodes = []int{0} },
		"system code over 255":     func(steps []WorkflowStep) { steps[2].SystemExitCodes = []int{256} },
		"system code listed twice": func(steps []WorkflowStep) { steps[2].SystemExitCodes = []int{3, 3} },
	} {
		if err := ValidateWorkflow(plan(change)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
