package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// humanInputsSeen is what TestHumanInputsHelper reports an evaluation
// command saw: the file INKUNA_HUMAN_INPUTS named, where the command ran,
// and the file's content.
type humanInputsSeen struct {
	Path    string                    `json:"path"`
	Dir     string                    `json:"dir"`
	Answers []domain.HumanInputAnswer `json:"answers"`
}

// TestEvaluationStepsReadTheirUpstreamHumanInputAnswers runs a flow where
// each of two human-input stages gates a different evaluation step, and a
// third evaluation step has none upstream. Each check reads only its own
// chain's answers, from a file outside the worktree it runs in.
func TestEvaluationStepsReadTheirUpstreamHumanInputAnswers(t *testing.T) {
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	service := workflowTestService(t, repositoryPath, &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	reports := t.TempDir()
	evaluation := func(id string, dependsOn ...string) domain.WorkflowStep {
		return domain.WorkflowStep{
			ID: id, Title: "Check " + id, Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, DependsOn: dependsOn,
			Commands: [][]string{{os.Args[0], "-test.run=TestHumanInputsHelper", "--", "--human-inputs-helper", filepath.Join(reports, id+".json")}},
			Budget:   domain.Budget{WallTimeSeconds: 60},
		}
	}
	question := func(id, prompt string) domain.WorkflowStep {
		return domain.WorkflowStep{ID: id, Title: "Ask " + id, Kind: domain.StepHumanInput, Prompt: prompt, Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 60}}
	}
	implement := spec.Steps[0]
	implement.ID, implement.Agent, implement.Permission, implement.AllowedPaths = "implement", "implementer", domain.PermissionWorktree, []string{"."}
	implement.DependsOn = []string{"approver"}
	spec.MaxConcurrency = 2
	spec.Steps = []domain.WorkflowStep{
		question("colour", "Which colour should the banner be?"),
		question("approver", "Who approves the docs change?"),
		implement,
		evaluation("docs", "implement"),
		evaluation("banner", "colour"),
		evaluation("smoke"),
	}
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)

	// Both questions are answered before the docs check runs, so leaving
	// the colour out of its file is the scoping, not the timing.
	questions := map[string]string{}
	for _, input := range waitForInbox(t, service, 2) {
		questions[input.StepID] = input.ID
	}
	if _, err := service.AnswerInput(questions["colour"], "teal"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AnswerInput(questions["approver"], "Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	completed := waitForWorkflowRun(t, service, run.ID, "completed")
	if completed.Worktree == "" {
		t.Fatalf("the writable step did not get a worktree: %#v", completed)
	}

	seen := func(id string) humanInputsSeen {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(reports, id+".json"))
		if err != nil {
			t.Fatalf("%s check did not report: %v", id, err)
		}
		var report humanInputsSeen
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	answer := func(report humanInputsSeen) []string {
		values := []string{}
		for _, entry := range report.Answers {
			if entry.AnsweredAt.IsZero() || time.Since(entry.AnsweredAt) > time.Minute {
				t.Errorf("answer %#v has no answer time", entry)
			}
			values = append(values, entry.StageID+": "+entry.Prompt+" = "+entry.Answer)
		}
		return values
	}

	docs := seen("docs")
	if got, want := answer(docs), []string{"approver: Who approves the docs change? = Ada Lovelace"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("docs check read %v, want %v", got, want)
	}
	if !sameDirectory(t, docs.Dir, completed.Worktree) {
		t.Fatalf("docs check ran in %s, not the worktree %s", docs.Dir, completed.Worktree)
	}
	if within(t, docs.Path, completed.Worktree) || within(t, docs.Path, repositoryPath) {
		t.Fatalf("answers file %s is inside the code an agent can write", docs.Path)
	}
	if !within(t, docs.Path, filepath.Join(service.dataDir, "runs", run.ID)) {
		t.Fatalf("answers file %s is not in the run's data directory", docs.Path)
	}
	if got, want := answer(seen("banner")), []string{"colour: Which colour should the banner be? = teal"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("banner check read %v, want %v", got, want)
	}
	if smoke := seen("smoke"); len(smoke.Answers) != 0 || smoke.Path == docs.Path {
		t.Fatalf("a check with no human-input stage upstream read %#v", smoke)
	}
}

// TestAnswerIsRecordedBeforeTheNextStepRuns delivers an answer the way
// AnswerInput does but stops before AnswerInput would mark it answered, as
// happens when the step resumes first. The step itself records the answer,
// so the check after it still reads it.
func TestAnswerIsRecordedBeforeTheNextStepRuns(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	report := filepath.Join(t.TempDir(), "check.json")
	spec.Steps = []domain.WorkflowStep{
		{ID: "approver", Title: "Ask", Kind: domain.StepHumanInput, Prompt: "Who approves?", Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 60}},
		{ID: "check", Title: "Check", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, DependsOn: []string{"approver"}, Budget: domain.Budget{WallTimeSeconds: 60},
			Commands: [][]string{{os.Args[0], "-test.run=TestHumanInputsHelper", "--", "--human-inputs-helper", report}}},
	}
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)
	input := waitForInbox(t, service, 1)[0]
	ctx := context.Background()
	if claimed, err := service.store.ClaimInput(ctx, input.ID, "Ada Lovelace"); err != nil || !claimed {
		t.Fatalf("claim the answer: %v, %v", claimed, err)
	}
	service.runsMu.RLock()
	live := service.runs[run.ID]
	service.runsMu.RUnlock()
	if _, err := service.bus.SendMessage(live.ctx, agent.Human, a2a.MessageSendParams{Message: a2a.Message{Role: a2a.RoleUser, TaskID: input.A2ATaskID, Parts: []a2a.Part{a2a.TextPart("Ada Lovelace")}}}); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var seen humanInputsSeen
	if err := json.Unmarshal(data, &seen); err != nil || len(seen.Answers) != 1 || seen.Answers[0].Answer != "Ada Lovelace" || seen.Answers[0].AnsweredAt.IsZero() {
		t.Fatalf("check read %#v, %v", seen.Answers, err)
	}
	if stored, err := service.store.Input(ctx, input.ID); err != nil || stored.State != storage.InputAnswered {
		t.Fatalf("stored input = %#v, %v", stored, err)
	}
}

// TestHumanInputsHelper stands in for an evaluation command: it records
// what INKUNA_HUMAN_INPUTS gave it in the file named by its last argument.
func TestHumanInputsHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--human-inputs-helper" {
		return
	}
	report := humanInputsSeen{Path: os.Getenv(humanInputsEnv)}
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	report.Dir = directory
	data, err := os.ReadFile(report.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &report.Answers); err != nil || report.Answers == nil {
		t.Fatalf("answers file %q is not a JSON list: %v", data, err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Args[len(os.Args)-1], encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestHumanInputsTamperHelper stands in for a command that tries to change
// the answers for the commands after it. It records whether a plain write
// was refused, then changes the mode and writes anyway.
func TestHumanInputsTamperHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--human-inputs-tamper" {
		return
	}
	path := os.Getenv(humanInputsEnv)
	outcome := "written"
	if err := os.WriteFile(path, []byte("[]\n"), 0o600); err != nil {
		outcome = "refused"
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`[{"stageId":"approver","answer":"Mallory"}]`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(os.Args[len(os.Args)-1], []byte(outcome), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEachCommandReadsTheAnswersInkunaWrote runs a command that rewrites the
// answers file and then one that reads it. The file is read-only, and Inkuna
// writes it again before each command, so the second still reads the
// recorded answers.
func TestEachCommandReadsTheAnswersInkunaWrote(t *testing.T) {
	ctx := context.Background()
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	step := evaluationRun(t, service)
	inputs := humanInputsFile{
		path: filepath.Join(t.TempDir(), "human-inputs", "01-check.json"),
		data: []byte(`[{"stageId":"approver","prompt":"Who approves?","answer":"Ada Lovelace","answeredAt":"2026-10-17T21:48:13Z"}]` + "\n"),
	}
	reports := t.TempDir()
	tamper := []string{os.Args[0], "-test.run=TestHumanInputsTamperHelper", "--", "--human-inputs-tamper", filepath.Join(reports, "tamper.txt")}
	read := []string{os.Args[0], "-test.run=TestHumanInputsHelper", "--", "--human-inputs-helper", filepath.Join(reports, "read.json")}
	step.Commands = [][]string{tamper, read}
	if _, err := service.executeEvaluationStep(ctx, "execution", "step-run", t.TempDir(), inputs, step); err != nil {
		t.Fatal(err)
	}
	outcome, err := os.ReadFile(filepath.Join(reports, "tamper.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// Root, for one, writes to a read-only file anyway; the rewrite before
	// the next command is what holds then.
	if want := "refused"; !bypassesFileModes(t) && string(outcome) != want {
		t.Fatalf("a plain write to the answers file was %q; want it %s", outcome, want)
	}
	data, err := os.ReadFile(filepath.Join(reports, "read.json"))
	if err != nil {
		t.Fatal(err)
	}
	var seen humanInputsSeen
	if err := json.Unmarshal(data, &seen); err != nil || len(seen.Answers) != 1 || seen.Answers[0].Answer != "Ada Lovelace" {
		t.Fatalf("the next command read %#v, %v", seen.Answers, err)
	}
}

// bypassesFileModes reports whether this process can write to a read-only
// file, as root can.
func bypassesFileModes(t *testing.T) bool {
	t.Helper()
	path := filepath.Join(t.TempDir(), "read-only")
	if err := os.WriteFile(path, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

// TestAnswersAreListedInTheOrderTheyWereGiven asks two questions at once and
// answers the one asked last first. The file lists answers by when they
// were given, not by when they were asked.
func TestAnswersAreListedInTheOrderTheyWereGiven(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	spec := workflowTestSpec(t, service)
	report := filepath.Join(t.TempDir(), "check.json")
	question := func(id string) domain.WorkflowStep {
		return domain.WorkflowStep{ID: id, Title: "Ask " + id, Kind: domain.StepHumanInput, Prompt: "Question " + id + "?", Permission: domain.PermissionReadOnly, Budget: domain.Budget{WallTimeSeconds: 60}}
	}
	spec.MaxConcurrency = 2
	spec.Steps = []domain.WorkflowStep{
		question("first"),
		question("second"),
		{ID: "check", Title: "Check", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, DependsOn: []string{"first", "second"}, Budget: domain.Budget{WallTimeSeconds: 60},
			Commands: [][]string{{os.Args[0], "-test.run=TestHumanInputsHelper", "--", "--human-inputs-helper", report}}},
	}
	spec = savePlanned(t, service, spec)
	run := startSpec(t, service, spec)
	inbox := waitForInbox(t, service, 2)
	sort.Slice(inbox, func(i, j int) bool { return inbox[i].CreatedAt.After(inbox[j].CreatedAt) })
	for _, input := range inbox {
		if _, err := service.AnswerInput(input.ID, "answer to "+input.StepID); err != nil {
			t.Fatal(err)
		}
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var seen humanInputsSeen
	if err := json.Unmarshal(data, &seen); err != nil || len(seen.Answers) != 2 {
		t.Fatalf("check read %#v, %v", seen.Answers, err)
	}
	if seen.Answers[0].StageID != inbox[0].StepID || seen.Answers[1].StageID != inbox[1].StepID || !seen.Answers[0].AnsweredAt.Before(seen.Answers[1].AnsweredAt) {
		t.Fatalf("answers %#v are not in the order they were given (%s, then %s)", seen.Answers, inbox[0].StepID, inbox[1].StepID)
	}
}

// TestHumanInputsFileHasAnAbsolutePath gives Inkuna a relative data
// directory. The commands run elsewhere, so the path they get must still
// reach the file.
func TestHumanInputsFileHasAnAbsolutePath(t *testing.T) {
	t.Chdir(t.TempDir())
	service := &Service{dataDir: "data"}
	spec := domain.WorkflowSpec{Steps: []domain.WorkflowStep{{ID: "check", Kind: domain.StepEvaluation}}}
	inputs, err := service.humanInputs(context.Background(), "run-1", spec, spec.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(inputs.path) {
		t.Fatalf("answers path %q is relative", inputs.path)
	}
	if err := inputs.write(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if data, err := os.ReadFile(inputs.path); err != nil || string(data) != "[]\n" {
		t.Fatalf("answers file from another directory = %q, %v", data, err)
	}
}

func TestHumanInputsNamesAreDistinctAndPortable(t *testing.T) {
	ids := []string{"check:docs", "check/docs", "check docs", "check_docs", "CON", "..", "é", strings.Repeat("x", 300)}
	spec := domain.WorkflowSpec{}
	for _, id := range ids {
		spec.Steps = append(spec.Steps, domain.WorkflowStep{ID: id})
	}
	portable := regexp.MustCompile(`^[0-9]{2,}-[A-Za-z0-9_-]{1,64}\.json$`)
	named := map[string]string{}
	for _, id := range ids {
		name, err := humanInputsName(spec, id)
		if err != nil || !portable.MatchString(name) {
			t.Errorf("step %q is named %q, %v", id, name, err)
		}
		if other, taken := named[name]; taken {
			t.Errorf("steps %q and %q share %s", other, id, name)
		}
		named[name] = id
	}
	if _, err := humanInputsName(spec, "missing"); err == nil {
		t.Error("a step outside the workflow has no answers file")
	}
}

func TestUpstreamHumanInputsFollowsTheWholeChain(t *testing.T) {
	step := func(id string, kind domain.StepKind, dependsOn ...string) domain.WorkflowStep {
		return domain.WorkflowStep{ID: id, Kind: kind, DependsOn: dependsOn}
	}
	spec := domain.WorkflowSpec{Steps: []domain.WorkflowStep{
		step("scope", domain.StepHumanInput),
		step("approver", domain.StepHumanInput),
		step("unrelated", domain.StepHumanInput),
		step("plan", domain.StepAgent, "scope"),
		step("build", domain.StepAgent, "plan", "approver"),
		step("review", domain.StepAgent, "plan"),
		step("check", domain.StepEvaluation, "build", "review"),
	}}
	if got, want := upstreamHumanInputs(spec, "check"), map[string]bool{"scope": true, "approver": true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upstream of check = %v, want %v", got, want)
	}
	if got := upstreamHumanInputs(spec, "scope"); len(got) != 0 {
		t.Fatalf("a stage is not upstream of itself: %v", got)
	}
}

func sameDirectory(t *testing.T, first, second string) bool {
	t.Helper()
	return resolved(t, first) == resolved(t, second)
}

// within reports whether path is inside directory, after symbolic links,
// such as macOS's /var to /private/var, are resolved.
func within(t *testing.T, path, directory string) bool {
	t.Helper()
	relative, err := filepath.Rel(resolved(t, directory), resolved(t, path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
