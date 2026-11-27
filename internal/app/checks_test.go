package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/proc"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// evaluationRun stores a running run, "execution", with an evaluation step
// running as "step-run", for checks to be recorded against.
func evaluationRun(t *testing.T, service *Service) domain.WorkflowStep {
	t.Helper()
	ctx := context.Background()
	spec := workflowTestSpec(t, service)
	spec.Steps[0].Kind, spec.Steps[0].Title = domain.StepEvaluation, "Verify"
	if err := service.store.SaveWorkflow(ctx, "approved", spec); err != nil {
		t.Fatal(err)
	}
	if err := service.store.SaveExecution(ctx, storage.Execution{ID: "execution", WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: "running", Title: "Verify"}); err != nil {
		t.Fatal(err)
	}
	if err := service.store.StartRun(ctx, "step-run", "", spec.ID, spec.Steps[0].ID, 1, domain.RuntimeCodex, ""); err != nil {
		t.Fatal(err)
	}
	service.runs["execution"] = &liveExecution{view: WorkflowRunView{ID: "execution", WorkflowID: spec.ID, TaskID: spec.TaskID, ProjectID: "project", State: "completed"}, task: domain.TaskSpec{ID: spec.TaskID}}
	return spec.Steps[0]
}

// checkCommand runs TestCheckCommandHelper with actions: "print:<text>",
// "lines:<n>", "env:<name>", "set:<name>", "wait:<path>", and "exit:<code>".
func checkCommand(actions ...string) []string {
	return append([]string{os.Args[0], "-test.run=TestCheckCommandHelper", "--", "--check-helper"}, actions...)
}

func TestCheckCommandHelper(t *testing.T) {
	actions := []string(nil)
	for index, argument := range os.Args {
		if argument == "--check-helper" {
			actions = os.Args[index+1:]
		}
	}
	if actions == nil {
		return
	}
	for _, action := range actions {
		name, value, _ := strings.Cut(action, ":")
		switch name {
		case "print":
			fmt.Println(value)
		case "lines":
			count, _ := strconv.Atoi(value)
			for line := 1; line <= count; line++ {
				fmt.Printf("line %05d of the output\n", line)
			}
		case "env":
			fmt.Printf("%s=%s\n", value, os.Getenv(value))
		case "set":
			// Whether the variable is set, without printing a value the
			// log would hide.
			_, ok := os.LookupEnv(value)
			fmt.Printf("%s set: %t\n", value, ok)
		case "wait":
			for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if _, err := os.Stat(value); err == nil {
					break
				}
			}
		case "exit":
			code, _ := strconv.Atoi(value)
			os.Exit(code)
		}
	}
	os.Exit(0)
}

func noInputs(t *testing.T) humanInputsFile {
	return humanInputsFile{path: filepath.Join(t.TempDir(), "inputs.json"), data: []byte("[]\n")}
}

func TestEveryCommandsOutputIsKeptAsACheck(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	step := evaluationRun(t, service)
	step.Commands = [][]string{
		checkCommand("print:all good", "env:FORCE_COLOR", "print:api_key=sk-live-123456"),
		checkCommand("print:something broke", "exit:3"),
		checkCommand("print:never runs"),
	}
	_, err := service.executeEvaluationStep(context.Background(), "execution", "step-run", t.TempDir(), noInputs(t), step)
	if err == nil || !strings.Contains(err.Error(), "something broke") {
		t.Fatalf("the failure should say what the command wrote: %v", err)
	}
	checks, err := service.RunChecks("execution")
	if err != nil || len(checks) != 3 {
		t.Fatalf("checks %+v, %v", checks, err)
	}
	passed, failed, skipped := checks[0], checks[1], checks[2]
	if passed.Status != storage.CheckPassed || passed.ExitCode == nil || *passed.ExitCode != 0 || passed.StepTitle != "Verify" || passed.LogDigest == "" || passed.FinishedAt.Before(passed.StartedAt) {
		t.Fatalf("passed check %+v", passed)
	}
	if failed.Status != storage.CheckFailed || failed.ExitCode == nil || *failed.ExitCode != 3 {
		t.Fatalf("a failing command shows its exit code: %+v", failed)
	}
	if skipped.Status != storage.CheckSkipped || !skipped.StartedAt.IsZero() || skipped.LogDigest != "" {
		t.Fatalf("a command after a failure is skipped: %+v", skipped)
	}

	log, err := service.CheckLog(passed.ID, 0)
	if err != nil || !log.Reset || log.Running || !strings.Contains(log.Text, "all good") {
		t.Fatalf("passing log %+v, %v", log, err)
	}
	if !strings.Contains(log.Text, "FORCE_COLOR=1") {
		t.Fatalf("checks run with colours forced on: %q", log.Text)
	}
	if strings.Contains(log.Text, "sk-live-123456") || !strings.Contains(log.Text, "api_key=[REDACTED]") {
		t.Fatalf("a credential must be redacted before it is kept: %q", log.Text)
	}
	artifacts, err := service.RunArtifacts("execution")
	if err != nil {
		t.Fatal(err)
	}
	kinds := 0
	for _, artifact := range artifacts {
		if artifact.Kind == "check" {
			kinds++
		}
	}
	if kinds != 2 {
		t.Fatalf("the two commands that ran each keep a check artifact: %+v", artifacts)
	}

	run, err := service.WorkflowRun("execution")
	if err != nil || run.Checks == nil || *run.Checks != (CheckTotals{Passed: 1, Failed: 1, Total: 3}) {
		t.Fatalf("run totals %+v, %v", run.Checks, err)
	}
}

// TestAChecksEnvironmentIsAWorkersNotTheUsersShell runs a check with a
// credential and an ordinary variable in Inkuna's environment and a secret
// declared in inkuna.yaml. The check gets the declared secret and the
// baseline, not the other two, and its log hides the credential's value
// even when the check prints it from elsewhere.
func TestAChecksEnvironmentIsAWorkersNotTheUsersShell(t *testing.T) {
	t.Setenv("INKUNA_CHECK_TEST_API_KEY", "ambient-key-1234567")
	t.Setenv("INKUNA_CHECK_TEST_PLAIN", "plain-value-1234567")
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	service.config.File.Secrets = append(service.config.File.Secrets, config.SecretRef{Name: "DOCS_TOKEN"})
	if err := service.secretStore.Set("DOCS_TOKEN", "declared-secret-1234567"); err != nil {
		t.Fatal(err)
	}
	step := evaluationRun(t, service)
	step.Commands = [][]string{checkCommand(
		"set:INKUNA_CHECK_TEST_API_KEY", "set:INKUNA_CHECK_TEST_PLAIN", "set:DOCS_TOKEN", "set:PATH", "set:"+humanInputsEnv,
		"print:read ambient-key-1234567 from a profile", "print:and plain-value-1234567", "exit:1",
	)}
	if _, err := service.executeEvaluationStep(context.Background(), "execution", "step-run", t.TempDir(), noInputs(t), step); err == nil {
		t.Fatal("the check exits 1 so its log goes back as a failure")
	}
	checks, err := service.RunChecks("execution")
	if err != nil || len(checks) != 1 {
		t.Fatalf("checks %+v, %v", checks, err)
	}
	log, err := service.CheckLog(checks[0].ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"INKUNA_CHECK_TEST_API_KEY set: false", "INKUNA_CHECK_TEST_PLAIN set: false", "DOCS_TOKEN set: true", "PATH set: true", humanInputsEnv + " set: true",
		"read [REDACTED] from a profile", "and plain-value-1234567",
	} {
		if !strings.Contains(log.Text, want) {
			t.Fatalf("the check's log lacks %q:\n%s", want, log.Text)
		}
	}
}

// TestACheckCarriesItsInkunasMark runs a check and reads its environment:
// it carries the mark of the Inkuna that started it, as a worker does, so
// the next Inkuna on the data directory ends one a crash left running.
func TestACheckCarriesItsInkunasMark(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	step := evaluationRun(t, service)
	step.Commands = [][]string{checkCommand("env:" + proc.InstanceVariable)}
	if _, err := service.executeEvaluationStep(context.Background(), "execution", "step-run", t.TempDir(), noInputs(t), step); err != nil {
		t.Fatal(err)
	}
	checks, err := service.RunChecks("execution")
	if err != nil || len(checks) != 1 {
		t.Fatalf("checks %+v, %v", checks, err)
	}
	log, err := service.CheckLog(checks[0].ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := proc.Mark(InstanceID(service)); !strings.Contains(log.Text, want) {
		t.Fatalf("the check's environment lacks %q:\n%s", want, log.Text)
	}
}

func TestAnEvaluationThatContinuesOnFailureRunsEveryCommand(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	step := evaluationRun(t, service)
	step.ContinueOnFailure = true
	step.Commands = [][]string{checkCommand("print:lint", "exit:1"), checkCommand("print:test"), checkCommand("print:build", "exit:2")}
	_, err := service.executeEvaluationStep(context.Background(), "execution", "step-run", t.TempDir(), noInputs(t), step)
	if err == nil || !strings.Contains(err.Error(), "2 verification commands failed") {
		t.Fatalf("both failures should be named: %v", err)
	}
	checks, err := service.RunChecks("execution")
	if err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for _, check := range checks {
		statuses = append(statuses, check.Status)
	}
	if strings.Join(statuses, " ") != "failed passed failed" {
		t.Fatalf("statuses %v", statuses)
	}
}

func TestOversizedOutputKeepsItsHeadAndTail(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	service.config.CheckLogBytes = config.MinCheckLogBytes
	step := evaluationRun(t, service)
	step.Commands = [][]string{checkCommand("lines:2000")}
	if _, err := service.executeEvaluationStep(context.Background(), "execution", "step-run", t.TempDir(), noInputs(t), step); err != nil {
		t.Fatal(err)
	}
	checks, err := service.RunChecks("execution")
	if err != nil || len(checks) != 1 || !checks[0].LogCut || checks[0].LogBytes != 2000*int64(len("line 00001 of the output\n")) {
		t.Fatalf("checks %+v, %v", checks, err)
	}
	log, err := service.CheckLog(checks[0].ID, 0)
	if err != nil || !log.Cut {
		t.Fatalf("log %+v, %v", log, err)
	}
	if !strings.HasPrefix(log.Text, "line 00001 of the output\n") || !strings.HasSuffix(log.Text, "line 02000 of the output\n") || strings.Contains(log.Text, "line 01000 ") || !strings.Contains(log.Text, "Inkuna left out") {
		t.Fatalf("the log should keep its head and tail around a marker:\n%s", log.Text)
	}
	if len(log.Text) > config.MinCheckLogBytes+256 {
		t.Fatalf("the kept log is %d bytes, over its cap", len(log.Text))
	}
}

func TestARunningChecksLogCanBeReadAsItGrows(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	step := evaluationRun(t, service)
	release := filepath.Join(t.TempDir(), "release")
	step.Commands = [][]string{checkCommand("print:first liné", "wait:"+release, "print:second line")}
	done := make(chan error, 1)
	go func() {
		_, err := service.executeEvaluationStep(context.Background(), "execution", "step-run", t.TempDir(), noInputs(t), step)
		done <- err
	}()
	id := "step-run-0"
	var live CheckLogView
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var err error
		if live, err = service.CheckLog(id, 0); err == nil && strings.Contains(live.Text, "first liné") {
			break
		}
	}
	// Offsets count bytes: é is two.
	if !live.Running || live.Reset || live.Offset != 0 || live.End != 12 || live.Text != "first liné\n" {
		t.Fatalf("the log so far %+v", live)
	}
	if more, err := service.CheckLog(id, live.End); err != nil || more.Text != "" || more.Offset != live.End || more.End != live.End {
		t.Fatalf("nothing new yet %+v, %v", more, err)
	}
	// An offset inside a character is not one the log handed out.
	if odd, err := service.CheckLog(id, 10); err != nil || !odd.Reset || odd.Text != live.Text {
		t.Fatalf("an offset inside é gets the whole log: %+v, %v", odd, err)
	}
	if checks, err := service.RunChecks("execution"); err != nil || checks[0].Status != storage.CheckRunning {
		t.Fatalf("a running check %+v, %v", checks, err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if final, err := service.CheckLog(id, live.End); err != nil || final.Running || !final.Reset || final.Text != "first liné\nsecond line\n" || final.End != len(final.Text) {
		t.Fatalf("the stored log %+v, %v", final, err)
	}
}

func TestACancelledCheckStopsAndSkipsTheRest(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	step := evaluationRun(t, service)
	step.Commands = [][]string{checkCommand("print:waiting", "wait:"+filepath.Join(t.TempDir(), "never")), checkCommand("print:after")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := service.executeEvaluationStep(ctx, "execution", "step-run", t.TempDir(), noInputs(t), step)
		done <- err
	}()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if log, err := service.CheckLog("step-run-0", 0); err == nil && strings.Contains(log.Text, "waiting") {
			break
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a cancelled step must fail")
	}
	checks, err := service.RunChecks("execution")
	if err != nil || checks[0].Status != storage.CheckCancelled || checks[0].ExitCode != nil || checks[1].Status != storage.CheckSkipped {
		t.Fatalf("checks %+v, %v", checks, err)
	}
	if log, err := service.CheckLog("step-run-0", 0); err != nil || !strings.Contains(log.Text, "waiting") {
		t.Fatalf("a cancelled check keeps what it wrote: %+v, %v", log, err)
	}
}

func TestCheckLogRefusesWhatIsNotACheck(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	for _, id := range []string{"", "../etc/passwd", "step-run"} {
		if _, err := service.CheckLog(id, 0); err == nil {
			t.Errorf("%q must be refused", id)
		}
	}
	if _, err := service.CheckLog("missing-0", 0); err == nil {
		t.Fatal("an unknown check must be refused")
	}
}

func TestAStageIDWithSpacesStillNamesItsChecks(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), &workflowRuntime{})
	evaluationRun(t, service)
	id := "execution-vérifier docs-0"
	if err := service.store.SaveCheck(context.Background(), storage.CheckRecord{ID: id, ExecutionID: "execution", StepID: "vérifier docs", StepRunID: "execution-vérifier docs", Argv: []string{"true"}, Status: storage.CheckPassed}); err != nil {
		t.Fatal(err)
	}
	if log, err := service.CheckLog(id, 0); err != nil || !log.Reset || log.Text != "" {
		t.Fatalf("log %+v, %v", log, err)
	}
}
