package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
)

// writeSkill adds a skill to a repository's .inkuna/skills, with a manifest
// when one is given.
func writeSkill(t *testing.T, repository, id, body, manifest string) {
	t.Helper()
	directory := filepath.Join(repository, ".inkuna", "skills", id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + id + "\ndescription: The " + id + " skill\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(directory, "inkuna.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// filler is about size bytes of numbered lines.
func filler(label string, size int) string {
	var builder strings.Builder
	for index := 0; builder.Len() < size; index++ {
		builder.WriteString(label + " line " + string(rune('a'+index%26)) + " of filler text\n")
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

// budgetTestSpec plans one read-only researcher step over repository, with
// the given objective and skills.
func budgetTestSpec(t *testing.T, service *Service, repository, objective string, skills ...string) domain.WorkflowSpec {
	t.Helper()
	task := domain.TaskSpec{
		Version: domain.ContractVersion, ID: "task", Kind: domain.TaskResearch, Objective: objective,
		Source: domain.TaskSource{Kind: "request"}, Repository: repository, Priority: 1, Budget: domain.Budget{WallTimeSeconds: 60},
	}
	if err := service.store.SaveTask(context.Background(), "project", "planned", task); err != nil {
		t.Fatal(err)
	}
	return savePlanned(t, service, domain.WorkflowSpec{
		Version: domain.ContractVersion, ID: "workflow", TaskID: task.ID, DefaultRuntime: domain.RuntimeCodex,
		RoutingPolicyVersion: service.routingPolicy().Version, MaxConcurrency: 1, CreatedAt: time.Now().UTC(),
		Steps: []domain.WorkflowStep{{
			ID: "worker", Title: "Inspect", Kind: domain.StepAgent, Runtime: domain.RuntimeCodex, Permission: domain.PermissionReadOnly, Agent: "researcher",
			SelectedSkills: skills, Complexity: &domain.ComplexityAssessment{Score: 4, Rationale: "read-only inspection"},
			Budget: domain.Budget{WallTimeSeconds: 60, MaxOutputBytes: 1024},
		}},
	})
}

var artifactNote = regexp.MustCompile(`\[truncated: \d+ bytes omitted; full text in inkuna://artifact/([0-9a-f]{64})\]`)

func TestBudgetsCutTheWorkerPromptAndSayWhereTheFullTextIs(t *testing.T) {
	repository := t.TempDir()
	writeSkill(t, repository, "big", filler("big skill", 20<<10), "")
	writeSkill(t, repository, "lazy", "Lazy skill body that stays in its file.", "version: 1\ndelivery: on-demand\n")
	runtime := &workflowRuntime{}
	service := workflowTestService(t, repository, runtime)
	objective := "Inspect the importer\n" + filler("objective", 40<<10)
	run := startSpec(t, service, budgetTestSpec(t, service, repository, objective, "big", "lazy"))
	waitForWorkflowRun(t, service, run.ID, "completed")
	requests := started(runtime)
	if len(requests) != 1 {
		t.Fatalf("want one call, got %d", len(requests))
	}
	prompt := string(requests[0].Prompt)

	// The objective is cut, and its note names the artifact holding it whole.
	match := artifactNote.FindStringSubmatch(prompt)
	if match == nil || !strings.Contains(prompt, "\n# Objective\nInspect the importer\n") {
		t.Fatalf("the objective must be cut with a note naming an artifact:\n%s", prompt[:min(len(prompt), 3000)])
	}
	full, err := service.ReadArtifact(match[1])
	if err != nil || full.Kind != FullTextArtifact || full.Content != objective {
		t.Fatalf("the artifact must hold the whole objective: kind %q, %d bytes, err %v", full.Kind, len(full.Content), err)
	}
	// A skill the worker can open points to its file instead.
	if !strings.Contains(prompt, "omitted; full text in .inkuna/skills/big/SKILL.md]") {
		t.Fatal("the cut skill must point to its file")
	}
	// An on-demand skill is sent by reference only.
	if strings.Contains(prompt, "Lazy skill body") || !strings.Contains(prompt, "# Skills to open when needed\n") || !strings.Contains(prompt, "- `.inkuna/skills/lazy/SKILL.md`: lazy, The lazy skill\n") {
		t.Fatalf("the on-demand skill must be listed, not copied:\n%s", prompt[strings.Index(prompt, "# Skill"):])
	}
	if !strings.Contains(prompt, "Some text was cut to fit.") {
		t.Fatal("the protocol must say sections were cut")
	}
	// The stored record is what was sent, and says what was cut.
	stepContext, err := service.RunContext(run.ID, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if got := stepContext.Envelope.Metadata[domain.MetadataTruncated]; got != "objective,skill:big" {
		t.Fatalf("truncated = %q", got)
	}
	if len(stepContext.Envelope.SkillIndex) != 1 || stepContext.Envelope.SkillIndex[0].Path != ".inkuna/skills/lazy/SKILL.md" || stepContext.Envelope.Objective == objective {
		t.Fatalf("stored envelope: index %+v, objective %d bytes", stepContext.Envelope.SkillIndex, len(stepContext.Envelope.Objective))
	}
	cut := map[string]bool{}
	for _, section := range stepContext.Usage[0].Sections {
		if section.Truncated {
			cut[strings.TrimSuffix(section.Section+":"+section.Name, ":")] = true
		}
	}
	if !reflect.DeepEqual(cut, map[string]bool{"objective": true, "skill:big": true}) {
		t.Fatalf("cut sections = %v", cut)
	}
	if size := domain.EnvelopeBytes(stepContext.Envelope); size > domain.DefaultBudgets().Envelope {
		t.Fatalf("the envelope is %d bytes", size)
	}
}

func TestLaterTurnsAreHeldToTheBudgetsToo(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if !strings.Contains(string(request.Prompt), "# Human responses") {
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffInputRequired, Summary: "Need the spec", HumanInput: &domain.HumanInputRequest{Question: "Paste the spec?"}})}, nil
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Read the spec"})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	run := startSpec(t, service, workflowTestSpec(t, service))
	inbox := waitForInbox(t, service, 1)
	answer := filler("spec", 30<<10)
	if _, err := service.AnswerInput(inbox[0].ID, answer); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	requests := started(runtime)
	second := string(requests[len(requests)-1].Prompt)
	section := second[strings.Index(second, "\n# Human responses\n"):]
	// Budgets count the text; the list markup around it comes on top.
	if len(section) > 2*domain.DefaultBudgets().HumanResponses || strings.Contains(second, answer) {
		t.Fatalf("the answer reached the prompt whole: %d bytes", len(section))
	}
	match := artifactNote.FindStringSubmatch(section)
	if match == nil {
		t.Fatalf("the answer's note must name an artifact:\n%s", section[:500])
	}
	full, err := service.ReadArtifact(match[1])
	if err != nil || !strings.Contains(full.Content, answer) {
		t.Fatalf("the artifact must hold the whole answer: %v", err)
	}
}

func TestATurnThatCutsFurtherCountsFromTheOriginalText(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if !strings.Contains(string(request.Prompt), "# Human responses") {
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffInputRequired, Summary: "Need detail", HumanInput: &domain.HumanInputRequest{Question: "More detail?"}})}, nil
		}
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Done"})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	// A tight envelope: the objective is cut on the first turn, and the
	// answer the second turn adds makes it cut further.
	service.config.File.Budgets = domain.Budgets{Envelope: 8 << 10}
	objective := "Inspect the importer\n" + filler("objective", 20<<10)
	run := startSpec(t, service, budgetTestSpec(t, service, t.TempDir(), objective))
	inbox := waitForInbox(t, service, 1)
	if _, err := service.AnswerInput(inbox[0].ID, filler("detail", 5<<10)); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	requests := started(runtime)
	if len(requests) != 2 {
		t.Fatalf("want two turns, got %d", len(requests))
	}
	sentObjective := func(prompt string) string {
		start := strings.Index(prompt, "\n# Objective\n") + len("\n# Objective\n")
		if end := strings.Index(prompt[start:], "\n# "); end >= 0 {
			return prompt[start : start+end]
		}
		return prompt[start:]
	}
	first, second := sentObjective(string(requests[0].Prompt)), sentObjective(string(requests[1].Prompt))
	if len(second) >= len(first) {
		t.Fatalf("the second turn should cut the objective further: %d then %d bytes", len(first), len(second))
	}
	for turn, sent := range []string{first, second} {
		match := regexp.MustCompile(`\n\[truncated: (\d+) bytes omitted; full text in inkuna://artifact/([0-9a-f]{64})\]\n?$`).FindStringSubmatch(sent)
		if match == nil {
			t.Fatalf("turn %d: no note at the end of the objective", turn+1)
		}
		kept := sent[:strings.LastIndex(sent, "\n[truncated")]
		if want := fmt.Sprint(len(objective) - len(kept)); match[1] != want {
			t.Fatalf("turn %d: the note says %s bytes were left out; %s were", turn+1, match[1], want)
		}
		if full, err := service.ReadArtifact(match[2]); err != nil || full.Content != objective {
			t.Fatalf("turn %d: the artifact must hold the original objective, has %d bytes (%v)", turn+1, len(full.Content), err)
		}
	}
}

func TestAStepWhoseContextCannotFitFailsAndSaysWhatToChange(t *testing.T) {
	repository := t.TempDir()
	writeSkill(t, repository, "big", filler("big skill", 5<<10), "")
	runtime := &workflowRuntime{}
	service := workflowTestService(t, repository, runtime)
	// The objective and the skill each come down to 1 KB at the least,
	// which together is over the envelope's 1 KB.
	service.config.File.Budgets = domain.Budgets{Envelope: domain.MinBudget}
	spec := budgetTestSpec(t, service, repository, filler("objective", 5<<10), "big")
	run := startSpec(t, service, spec)
	failed := waitForWorkflowRun(t, service, run.ID, "failed")
	if len(started(runtime)) != 0 {
		t.Fatal("a context over its budget must not reach the runtime")
	}
	if !strings.Contains(failed.Error, "budgets.envelope") && !strings.Contains(failed.Steps[0].Summary, "budgets.envelope") {
		t.Fatalf("the failure must say which budget to raise: error %q, summary %q", failed.Error, failed.Steps[0].Summary)
	}
}

func TestAFullTextThatCannotBeStoredIsReported(t *testing.T) {
	runtime := &workflowRuntime{}
	service := workflowTestService(t, t.TempDir(), runtime)
	// No artifact store: nothing can be stored, and the step goes on.
	service.artifacts = nil
	run := startSpec(t, service, budgetTestSpec(t, service, t.TempDir(), "Inspect\n"+filler("objective", 20<<10)))
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	var warned bool
	for _, event := range completed.Events {
		warned = warned || (event.Kind == domain.EventWarning && strings.Contains(event.Summary, "The full text of objective was cut to fit and could not be stored") && strings.Contains(event.Summary, "artifact store"))
	}
	if !warned {
		t.Fatalf("the lost full text must be reported: %+v", completed.Events)
	}
	prompt := string(started(runtime)[0].Prompt)
	if !regexp.MustCompile(`\[truncated: \d+ bytes omitted\]`).MatchString(prompt) || strings.Contains(prompt, "full text in inkuna://") {
		t.Fatal("the note must not name an artifact that was never stored")
	}
}

func TestAStepGetsOnlyItsOwnPeerConversation(t *testing.T) {
	runtime := &workflowRuntime{}
	runtime.handle = func(_ context.Context, request agentruntime.RunRequest) (agentruntime.RunResult, error) {
		if strings.HasPrefix(string(request.Prompt), "# Agent: reviewer") {
			return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Reviewed"})}, nil
		}
		// A finished step that names another agent sent it nothing.
		return agentruntime.RunResult{Output: handoffJSON(t, domain.HandoffResult{Status: domain.HandoffCompleted, Summary: "Inspected", Messages: []domain.PeerMessage{{To: "reviewer", Content: "note left for whoever reviews"}}})}, nil
	}
	service := workflowTestService(t, t.TempDir(), runtime)
	spec := workflowTestSpec(t, service)
	review := spec.Steps[0]
	review.ID, review.Title, review.Agent, review.DependsOn = "review", "Review", "reviewer", []string{"worker"}
	spec.Steps = append(spec.Steps, review)
	run := startSpec(t, service, savePlanned(t, service, spec))
	waitForWorkflowRun(t, service, run.ID, "completed")
	for _, request := range started(runtime) {
		if strings.Contains(string(request.Prompt), "note left for whoever reviews") {
			t.Fatalf("a step received another step's messages:\n%s", request.Prompt)
		}
	}
}

func TestSkillFilesAreOnlyTheOnesTheWorkerCanOpen(t *testing.T) {
	repository := t.TempDir()
	writeSkill(t, repository, "same", "Same body.", "")
	writeSkill(t, repository, "stale", "Older body.", "")
	bodies := map[string]string{"same": "Same body.", "stale": "Newer body.", "missing": "Body."}
	if got := skillFiles(repository, nil, bodies); !reflect.DeepEqual(got, map[string]string{"same": ".inkuna/skills/same/SKILL.md"}) {
		t.Fatalf("files = %v", got)
	}
	if got := skillFiles(repository, []string{"internal", "docs/"}, bodies); len(got) != 0 {
		t.Fatalf("a skill outside the allowed paths was offered: %v", got)
	}
	for _, allowed := range [][]string{{"."}, {".inkuna"}, {"./.inkuna/skills/"}} {
		if got := skillFiles(repository, allowed, bodies); len(got) != 1 {
			t.Fatalf("allowed %v: files = %v", allowed, got)
		}
	}
	if insideAllowed(".inkuna/skillsets/x", []string{".inkuna/skills"}) {
		t.Fatal("a sibling directory with the same prefix is not inside")
	}
}
