package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// checkLoopRun runs implement, then verify, which sends its failures back to
// implement up to maxRounds times, then any extra steps, and waits until the
// run ends as want.
func checkLoopRun(t *testing.T, verify [][]string, maxRounds int, want string, extra ...domain.WorkflowStep) (*Service, *workflowRuntime, WorkflowRunView) {
	t.Helper()
	return checkLoopRunWith(t, nil, verify, maxRounds, want, extra...)
}

// checkLoopRunWith is checkLoopRun with change made to the verify step
// first.
func checkLoopRunWith(t *testing.T, change func(*domain.WorkflowStep), verify [][]string, maxRounds int, want string, extra ...domain.WorkflowStep) (*Service, *workflowRuntime, WorkflowRunView) {
	t.Helper()
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := runGit(repositoryPath, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repositoryPath, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	runtime := writingRuntime()
	service := workflowTestService(t, repositoryPath, runtime)
	spec := workflowTestSpec(t, service)
	implement := spec.Steps[0]
	implement.ID, implement.Agent, implement.Permission, implement.AllowedPaths = "implement", "implementer", domain.PermissionWorktree, []string{"."}
	spec.MaxConcurrency = 2
	spec.Steps = append([]domain.WorkflowStep{
		implement,
		{
			ID: "verify", Title: "Verify", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, DependsOn: []string{"implement"},
			Commands: verify, OnFailure: domain.FailureFeedback, FeedbackTarget: "implement", MaxRounds: maxRounds, Budget: domain.Budget{WallTimeSeconds: 60},
		},
	}, extra...)
	if change != nil {
		change(&spec.Steps[1])
	}
	run := startSpec(t, service, savePlanned(t, service, spec))
	return service, runtime, waitForWorkflowRun(t, service, run.ID, want)
}

// checkHelper is a check command: the test binary, run as
// TestCheckLoopHelper, failing the first time (once) or every time (always),
// with exit code 3 unless the mode names another after a colon ("once:1");
// exiting with each run's code in turn, the last repeated and 0 a pass
// ("seq:1,3,1"); or stopped by a signal (signal).
func checkHelper(t *testing.T, mode string) []string {
	return []string{os.Args[0], "-test.run=TestCheckLoopHelper", "--", "--check-loop-helper", mode, filepath.Join(t.TempDir(), "runs")}
}

// TestCheckLoopHelper stands in for a test command. It writes more than a
// feedback item keeps, in colour, and exits with code 3, or the code its
// mode names, until it passes; or a signal stops it.
func TestCheckLoopHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-3] != "--check-loop-helper" {
		return
	}
	mode, counter := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	mode, named, _ := strings.Cut(mode, ":")
	code := 3
	if named != "" && mode != "seq" {
		code, _ = strconv.Atoi(named)
	}
	runs, _ := os.ReadFile(counter)
	if err := os.WriteFile(counter, append(runs, '.'), 0o644); err != nil {
		os.Exit(2)
	}
	if mode == "seq" {
		codes := strings.Split(named, ",")
		code, _ = strconv.Atoi(codes[min(len(runs), len(codes)-1)])
	}
	if (mode == "once" && len(runs) > 0) || (mode == "seq" && code == 0) {
		fmt.Println("ok  	example/greeter")
		os.Exit(0)
	}
	for line := range 400 {
		fmt.Printf("noise line %d of the test output\n", line)
	}
	fmt.Println("\x1b[31m--- FAIL: TestGreeting\x1b[0m")
	fmt.Println(`    greet_test.go:12: got "Hello, !", want "Hello, World!"`)
	if mode == "signal" {
		// As the OOM killer or a tool's own timeout would: no exit code.
		if self, err := os.FindProcess(os.Getpid()); err == nil {
			_ = self.Kill()
		}
		select {}
	}
	os.Exit(code)
}

func TestAFailedCheckSendsItsOutputBackAndTheNextRoundPasses(t *testing.T) {
	service, runtime, run := checkLoopRun(t, [][]string{checkHelper(t, "once")}, 2, "completed")
	if run.Round != 2 {
		t.Fatalf("the run passed in round %d, want 2", run.Round)
	}
	var prompt string
	for _, request := range startedRuns(runtime) {
		if request.RunID == run.ID+".r2.implement" {
			prompt = string(request.Prompt)
		}
	}
	for _, want := range []string{"# Feedback (round 2)", "[k1] From a check:", "exited with code 3.", "--- FAIL: TestGreeting", `want "Hello, World!"`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("round 2's implement prompt lacks %q:\n%s", want, prompt)
		}
	}
	// The item keeps the end of the log, from a line start, without colours.
	if strings.Contains(prompt, "\x1b") || strings.Contains(prompt, "noise line 0 ") {
		t.Fatalf("the prompt carries colours or more than the log's end:\n%s", prompt)
	}
	ctx := context.Background()
	items, err := service.store.Feedback(ctx, run.ID, 2)
	if err != nil || len(items) != 1 {
		t.Fatalf("round 2 has one item: %+v %v", items, err)
	}
	item := items[0]
	if item.ID != "k1" || item.From != storage.FeedbackFromCheck || item.Target != "implement" || item.CheckID != run.ID+"-verify-0" ||
		!strings.HasSuffix(item.Text, " exited with code 3") || len(item.Tail) > checkTail || !strings.Contains(item.Tail, "--- FAIL: TestGreeting") {
		t.Fatalf("the item names the check, how it ended, and its log's end: %+v", item)
	}
	rounds, err := service.RunRounds(run.ID)
	if err != nil || len(rounds) != 2 {
		t.Fatalf("two rounds: %d %v", len(rounds), err)
	}
	said, failure := false, ""
	for _, event := range rounds[0].Events {
		said = said || event.Summary == "Round 2 sends a failed check back to implement (automatic round 1 of 2)"
		if strings.HasPrefix(event.Summary, "Verification failed: ") {
			failure = event.Summary
		}
	}
	if !said {
		t.Fatal("round 1's timeline says which round its failed check started")
	}
	if !strings.Contains(failure, "--- FAIL: TestGreeting") || strings.Contains(failure, "\x1b") {
		t.Fatalf("the failure on the timeline shows the log's end without colours: %q", failure)
	}
}

func TestAlwaysFailingChecksStopTheRunAtTheCap(t *testing.T) {
	service, runtime, run := checkLoopRun(t, [][]string{checkHelper(t, "always")}, 2, "failed")
	// Round 1 and two automatic rounds ran; the third failure stops it.
	if run.Round != 3 || !strings.Contains(run.Error, "verify has sent its failures back 2 times, its cap (feedback.maxRounds)") || !strings.Contains(run.Error, "step verify:") {
		t.Fatalf("the run stops in round 3 naming the cap: round %d, %q", run.Round, run.Error)
	}
	implemented := 0
	for _, request := range startedRuns(runtime) {
		if strings.HasSuffix(request.RunID, "implement") {
			implemented++
		}
	}
	if implemented != 3 {
		t.Fatalf("implement ran %d times, want 3", implemented)
	}
	if items, err := service.store.Feedback(context.Background(), run.ID, 4); err != nil || len(items) != 0 {
		t.Fatalf("no round past the cap was recorded: %+v %v", items, err)
	}
}

func TestACheckThatCannotStartIsSentBackToo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-check")
	service, runtime, run := checkLoopRun(t, [][]string{{missing}}, 1, "failed")
	if run.Round != 2 || !strings.Contains(run.Error, "its cap (feedback.maxRounds)") {
		t.Fatalf("a check that never starts loops once and stops at the cap: round %d, %q", run.Round, run.Error)
	}
	items, err := service.store.Feedback(context.Background(), run.ID, 2)
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Text, missing+" could not run: ") {
		t.Fatalf("the item says the command could not run: %+v %v", items, err)
	}
	for _, request := range startedRuns(runtime) {
		if request.RunID == run.ID+".r2.implement" && !strings.Contains(string(request.Prompt), "could not run: ") {
			t.Fatalf("the target is told the command could not run:\n%s", request.Prompt)
		}
	}
}

func TestAFailureThatDidNotOptInFailsTheRunBesideOneThatDid(t *testing.T) {
	lint := domain.WorkflowStep{
		ID: "lint", Title: "Lint", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, DependsOn: []string{"implement"},
		Commands: [][]string{checkHelper(t, "always")}, Budget: domain.Budget{WallTimeSeconds: 60},
	}
	_, _, run := checkLoopRun(t, [][]string{checkHelper(t, "always")}, 2, "failed", lint)
	if run.Round != 1 || !strings.Contains(run.Error, "step lint:") || strings.Contains(run.Error, "feedback.maxRounds") {
		t.Fatalf("lint's failure fails the run in round 1: round %d, %q", run.Round, run.Error)
	}
}

func TestTwoStagesThatOptInSendTheirFailuresBackInOneRound(t *testing.T) {
	lint := domain.WorkflowStep{
		ID: "lint", Title: "Lint", Kind: domain.StepEvaluation, Permission: domain.PermissionReadOnly, DependsOn: []string{"implement"},
		Commands: [][]string{checkHelper(t, "once")}, OnFailure: domain.FailureFeedback, FeedbackTarget: "implement", MaxRounds: 1, Budget: domain.Budget{WallTimeSeconds: 60},
	}
	service, _, run := checkLoopRun(t, [][]string{checkHelper(t, "once")}, 2, "completed", lint)
	items, err := service.store.Feedback(context.Background(), run.ID, 2)
	if err != nil || run.Round != 2 || len(items) != 2 {
		t.Fatalf("one round carries both stages' failures: round %d, %+v %v", run.Round, items, err)
	}
	stages := []string{}
	for _, item := range items {
		check, err := service.store.Check(context.Background(), item.CheckID)
		if err != nil {
			t.Fatal(err)
		}
		stages = append(stages, item.ID+"="+check.StepID)
	}
	if strings.Join(stages, ",") != "k1=verify,k2=lint" {
		t.Fatalf("items follow the plan's order: %v", stages)
	}
}

// systemCodes makes the verify step name exit code 3 as a system failure.
func systemCodes(step *domain.WorkflowStep) { step.SystemExitCodes = []int{3} }

// checkPause waits for the run's one question about its checks.
func checkPause(t *testing.T, service *Service) InputView {
	t.Helper()
	inbox := waitForInbox(t, service, 1)
	if pause := inbox[0]; !pause.CheckPause || strings.Join(pause.Options, "|") != pauseRerun+"|"+pauseSend+"|"+pauseFail {
		t.Fatalf("the run asks what to do about its checks: %+v", pause)
	}
	return inbox[0]
}

// implementRuns counts the turns implement was started for.
func implementRuns(runtime *workflowRuntime) int {
	count := 0
	for _, request := range startedRuns(runtime) {
		if strings.HasSuffix(request.RunID, "implement") {
			count++
		}
	}
	return count
}

// TestACodeFailureStillStartsARoundWhenSystemCodesAreNamed checks that an
// exit code the rule does not list goes back to the writer as before.
func TestACodeFailureStillStartsARoundWhenSystemCodesAreNamed(t *testing.T) {
	service, runtime, run := checkLoopRunWith(t, systemCodes, [][]string{checkHelper(t, "once:1")}, 2, "completed")
	items, err := service.store.Feedback(context.Background(), run.ID, 2)
	if err != nil || run.Round != 2 || len(items) != 1 || !strings.HasSuffix(items[0].Text, " exited with code 1") || items[0].Target != "implement" {
		t.Fatalf("exit 1 goes back to implement in round 2: round %d, %+v %v", run.Round, items, err)
	}
	if implementRuns(runtime) != 2 {
		t.Fatalf("implement ran %d times, want 2", implementRuns(runtime))
	}
	if inputs, err := service.store.Inputs(context.Background(), "", 10); err != nil || len(inputs) != 0 {
		t.Fatalf("nothing paused for the person: %+v %v", inputs, err)
	}
}

// TestASystemFailurePausesAndReRunningTheChecksPassesTheRun checks that a
// listed exit code pauses the run without a writer round, and that Re-run
// checks runs only the checks again.
func TestASystemFailurePausesAndReRunningTheChecksPassesTheRun(t *testing.T) {
	service, runtime, run := checkLoopRunWith(t, systemCodes, [][]string{checkHelper(t, "once")}, 1, "waiting")
	pause := checkPause(t, service)
	if pause.StepID != "verify" || !strings.Contains(pause.Question, "Round 1: a check of verify failed in a way implement cannot fix") {
		t.Fatalf("the pause names the stage and the writer: %+v", pause)
	}
	if !strings.Contains(pause.Context, "exited with code 3") || !strings.Contains(pause.Context, "--- FAIL: TestGreeting") || strings.Contains(pause.Context, "\x1b") {
		t.Fatalf("the pause shows how the check ended and its log's end without colours:\n%s", pause.Context)
	}
	if implementRuns(runtime) != 1 {
		t.Fatal("no writer round starts while the run is paused")
	}
	if _, err := service.AnswerInput(pause.ID, "just fix it"); err == nil || !strings.Contains(err.Error(), "answer with one of: "+pauseRerun) {
		t.Fatalf("a pause takes only one of its choices: %v", err)
	}
	if _, err := service.AnswerInput(pause.ID, pauseRerun); err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	if run.Round != 2 || implementRuns(runtime) != 1 {
		t.Fatalf("round 2 re-ran the checks alone: round %d, implement ran %d times", run.Round, implementRuns(runtime))
	}
	if items, err := service.store.Feedback(context.Background(), run.ID, 2); err != nil || len(items) != 0 {
		t.Fatalf("re-running the checks sends nothing back: %+v %v", items, err)
	}
	// The answer only says what the run does next, so no worker gets it.
	if answered, err := service.store.AnsweredInputs(context.Background(), run.ID); err != nil || len(answered) != 0 {
		t.Fatalf("the pause is not an answer for the workers: %+v %v", answered, err)
	}
	rounds, err := service.RunRounds(run.ID)
	if err != nil || len(rounds) != 2 {
		t.Fatalf("two rounds: %d %v", len(rounds), err)
	}
	said := false
	for _, event := range rounds[0].Events {
		said = said || strings.HasPrefix(event.Summary, "Round 2 runs the checks of verify again")
	}
	if !said {
		t.Fatalf("round 1's timeline says the checks run again: %+v", rounds[0].Events)
	}
	for _, step := range rounds[1].Steps {
		if step.ID == "implement" && step.Round != 1 {
			t.Fatalf("round 2 keeps implement from round 1: %+v", step)
		}
	}
}

// TestACheckThatCannotStartPausesWhenSystemCodesAreNamed checks that a
// missing command is a system failure on a stage that names codes, and that
// Fail the run fails it.
func TestACheckThatCannotStartPausesWhenSystemCodesAreNamed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-check")
	service, runtime, run := checkLoopRunWith(t, systemCodes, [][]string{{missing}}, 2, "waiting")
	pause := checkPause(t, service)
	if !strings.Contains(pause.Context, missing+" could not run: ") {
		t.Fatalf("the pause says the command could not run:\n%s", pause.Context)
	}
	if _, err := service.AnswerInput(pause.ID, pauseFail); err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "failed")
	if run.Round != 1 || !strings.Contains(run.Error, "you chose to fail the run") || implementRuns(runtime) != 1 {
		t.Fatalf("the run fails in round 1 as chosen: round %d, %q, implement ran %d times", run.Round, run.Error, implementRuns(runtime))
	}
}

// TestSendingASystemFailureToTheWriterStartsAnAutomaticRound checks that
// the person can still send a system failure back as a code failure.
func TestSendingASystemFailureToTheWriterStartsAnAutomaticRound(t *testing.T) {
	service, runtime, run := checkLoopRunWith(t, systemCodes, [][]string{checkHelper(t, "once")}, 1, "waiting")
	if _, err := service.AnswerInput(checkPause(t, service).ID, pauseSend); err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "completed")
	items, err := service.store.Feedback(context.Background(), run.ID, 2)
	if err != nil || run.Round != 2 || len(items) != 1 || !strings.HasSuffix(items[0].Text, " exited with code 3") || implementRuns(runtime) != 2 {
		t.Fatalf("the failure goes to implement as a code failure: round %d, %+v %v", run.Round, items, err)
	}
}

// TestAMixedRoundPausesThenSendsItsCodeFailuresAfterTheReRun checks that
// code failures wait for the pause, go back only after the re-run, and that
// neither the pause nor the re-run counted toward the cap.
func TestAMixedRoundPausesThenSendsItsCodeFailuresAfterTheReRun(t *testing.T) {
	mixed := func(step *domain.WorkflowStep) {
		systemCodes(step)
		step.ContinueOnFailure = true
	}
	service, runtime, run := checkLoopRunWith(t, mixed, [][]string{checkHelper(t, "once"), checkHelper(t, "always:1")}, 1, "waiting")
	pause := checkPause(t, service)
	if !strings.Contains(pause.Context, "Waiting for your answer") || !strings.Contains(pause.Context, "exited with code 1") || implementRuns(runtime) != 1 {
		t.Fatalf("the code failure waits with the pause:\n%s", pause.Context)
	}
	if _, err := service.AnswerInput(pause.ID, pauseRerun); err != nil {
		t.Fatal(err)
	}
	// Round 2 re-runs the checks; the code failure alone goes back in round
	// 3, which is the stage's first automatic round, so its cap of 1 stops
	// the run when it fails again.
	run = waitForWorkflowRun(t, service, run.ID, "failed")
	if run.Round != 3 || !strings.Contains(run.Error, "its cap (feedback.maxRounds)") || implementRuns(runtime) != 2 {
		t.Fatalf("the code failure goes back once after the re-run: round %d, %q, implement ran %d times", run.Round, run.Error, implementRuns(runtime))
	}
	ctx := context.Background()
	if items, err := service.store.Feedback(ctx, run.ID, 2); err != nil || len(items) != 0 {
		t.Fatalf("the re-run sends nothing back: %+v %v", items, err)
	}
	items, err := service.store.Feedback(ctx, run.ID, 3)
	if err != nil || len(items) != 1 || !strings.HasSuffix(items[0].Text, " exited with code 1") || items[0].CheckID != run.ID+".r2.verify-1" {
		t.Fatalf("round 3 sends round 2's code failure alone: %+v %v", items, err)
	}
	rounds, err := service.RunRounds(run.ID)
	if err != nil || len(rounds) != 3 {
		t.Fatalf("three rounds: %d %v", len(rounds), err)
	}
	said := false
	for _, event := range rounds[1].Events {
		said = said || event.Summary == "Round 3 sends a failed check back to implement (automatic round 1 of 1)"
	}
	if !said {
		t.Fatal("the pause was not counted as an automatic round")
	}
}

// TestCancellingAPausedRunWithdrawsItsQuestion checks that a cancelled
// pause leaves no question to answer in Messages.
func TestCancellingAPausedRunWithdrawsItsQuestion(t *testing.T) {
	service, _, run := checkLoopRunWith(t, systemCodes, [][]string{checkHelper(t, "always")}, 1, "waiting")
	pause := checkPause(t, service)
	if _, err := service.CancelWorkflowRun(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "cancelled")
	if input, err := service.store.Input(context.Background(), pause.ID); err != nil || input.State != storage.InputCanceled {
		t.Fatalf("the question is withdrawn: %+v %v", input, err)
	}
}

// TestAReRunDoesNotResetTheAutomaticRoundCount checks that rounds a stage
// already sent back still count after the person re-runs its checks, so a
// pause cannot buy the writer more rounds than feedback.maxRounds.
func TestAReRunDoesNotResetTheAutomaticRoundCount(t *testing.T) {
	// Round 1 fails on the code and uses the stage's one automatic round;
	// round 2 fails on the environment and pauses; the re-run in round 3
	// fails on the code again.
	service, runtime, run := checkLoopRunWith(t, systemCodes, [][]string{checkHelper(t, "seq:1,3,1")}, 1, "waiting")
	if run.Round != 2 || implementRuns(runtime) != 2 {
		t.Fatalf("the pause comes after one automatic round: round %d, implement ran %d times", run.Round, implementRuns(runtime))
	}
	if _, err := service.AnswerInput(checkPause(t, service).ID, pauseRerun); err != nil {
		t.Fatal(err)
	}
	run = waitForWorkflowRun(t, service, run.ID, "failed")
	if run.Round != 3 || !strings.Contains(run.Error, "verify has sent its failures back 1 times, its cap (feedback.maxRounds)") || implementRuns(runtime) != 2 {
		t.Fatalf("the code failure after the re-run hits the cap: round %d, %q, implement ran %d times", run.Round, run.Error, implementRuns(runtime))
	}
	if items, err := service.store.Feedback(context.Background(), run.ID, 4); err != nil || len(items) != 0 {
		t.Fatalf("no writer round past the cap was recorded: %+v %v", items, err)
	}
}

// TestACheckStoppedBySignalPausesWithNoExitCode runs a check that a signal
// stops, so a regression that stored its -1 as an exit code, and so as a
// code failure, would start a writer round instead of pausing.
func TestACheckStoppedBySignalPausesWithNoExitCode(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Windows has no signals: a process ended from outside still exits with a code")
	}
	service, runtime, run := checkLoopRunWith(t, systemCodes, [][]string{checkHelper(t, "signal")}, 2, "waiting")
	checks, err := service.store.Checks(context.Background(), run.ID)
	if err != nil || len(checks) != 1 || checks[0].Status != storage.CheckFailed || checks[0].ExitCode != nil || !strings.Contains(checks[0].Error, "signal") {
		t.Fatalf("the check failed with no exit code, by a signal: %+v %v", checks, err)
	}
	pause := checkPause(t, service)
	if !strings.Contains(pause.Context, "could not run: signal") || implementRuns(runtime) != 1 {
		t.Fatalf("the run pauses with no writer round:\n%s\nimplement ran %d times", pause.Context, implementRuns(runtime))
	}
	if _, err := service.AnswerInput(pause.ID, pauseFail); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "failed")
}
