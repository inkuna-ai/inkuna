package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/proc"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// EventChecks names a run whose checks changed: one was queued, started, or
// finished.
const EventChecks = "inkuna:checks"

// EventCheckOutput names a running check whose log has grown, at most every
// checkNotifyEvery.
const EventCheckOutput = "inkuna:check"

// colourEnvironment asks the tools a check runs to keep their colours,
// which most switch off when they are not writing to a terminal.
var colourEnvironment = []string{"FORCE_COLOR=1", "CLICOLOR_FORCE=1"}

// failureTail is how much of a failed check's log its error event and the
// step's error carry, enough for a failure's summary; the whole log is the
// check's, a click away.
const failureTail = 1 << 10

// CheckView is one command of an evaluation step, as the Checks view lists
// it.
type CheckView struct {
	storage.CheckRecord
	// StepTitle is the evaluation step's title in the run's workflow.
	StepTitle string `json:"stepTitle,omitempty"`
	// DurationMs is how long the command ran, so far for a running one.
	DurationMs int64 `json:"durationMs"`
}

// CheckLogView is a check's log, or the part of it after a byte offset.
type CheckLogView struct {
	// Text is the log from byte Offset to byte End. Reset says it is the
	// whole log, to replace what the view has: always once the command has
	// ended or the log has left output out, since then it is not only added
	// to. The view asks for more from End, so offsets count bytes, not
	// characters.
	Text   string `json:"text"`
	Offset int    `json:"offset"`
	End    int    `json:"end"`
	Reset  bool   `json:"reset"`
	// Running says the command is still writing.
	Running bool `json:"running"`
	// Cut says output was left out between the log's head and tail; Bytes
	// is how much the command wrote.
	Cut   bool  `json:"cut"`
	Bytes int64 `json:"bytes"`
}

// CheckTotals counts the checks of a run's latest attempt at each
// evaluation step.
type CheckTotals struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Running int `json:"running"`
	Total   int `json:"total"`
}

// credentialName matches the names of variables that usually hold a
// credential, whose values a check's log hides even though the check is not
// given them.
var credentialName = regexp.MustCompile(`(?i)(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|AUTH|COOKIE|SESSION|PRIVATE)`)

// shortestHiddenAmbient is the shortest value of an ambient variable a log
// hides. It is longer than shortestHiddenSecret because these names are
// only guessed at, and a value such as "enabled" is no secret.
const shortestHiddenAmbient = 8

// checkEnvironment is what a check starts with, and the values its log
// hides. A failed check's log goes back to the agent that wrote the code,
// which may also have written the check, so a check gets what a worker
// does: the non-secret baseline and the secrets inkuna.yaml declares,
// never the rest of Inkuna's environment. The log hides those secrets and
// the values of the credential-looking variables the check was not given,
// in case it reads one from elsewhere, such as a shell profile.
func checkEnvironment(deps runDependencies) ([]string, []string) {
	environment := agentruntime.BaselineEnvironment()
	hidden := make([]string, 0, len(deps.secretNames))
	for _, name := range deps.secretNames {
		if value, _, err := deps.secrets.Resolve(name); err == nil && value != "" {
			environment = append(environment, name+"="+value)
			hidden = append(hidden, value)
		}
	}
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if !agentruntime.ReservedVariable(name) && credentialName.MatchString(name) && len(value) >= shortestHiddenAmbient {
			hidden = append(hidden, value)
		}
	}
	return environment, hidden
}

// executeEvaluationStep runs a step's commands in order, each as a check
// with checkEnvironment plus INKUNA_HUMAN_INPUTS naming the inputs file,
// written afresh before it starts, and the variables that keep colours. Every command is recorded as queued first. Each one's output is
// kept, redacted, as a `check` artifact, and the view can read it while
// the command runs. The step stops at the first failure, listing the rest
// as skipped, unless it continues on failure, when it fails once every
// command has run.
func (service *Service) executeEvaluationStep(ctx context.Context, executionID, stepRunID, workingDirectory string, inputs humanInputsFile, step domain.WorkflowStep) (domain.HandoffResult, error) {
	commands := make([][]string, 0, len(step.Commands))
	for _, command := range step.Commands {
		if len(command) > 0 {
			commands = append(commands, command)
		}
	}
	if len(commands) == 0 {
		summary := "No deterministic verification command was detected"
		return domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted, Summary: summary}, service.recordSyntheticStep(executionID, stepRunID, summary)
	}
	deps := service.dependenciesFor(executionID)
	environment, secretValues := checkEnvironment(deps)
	taskID := service.taskOf(executionID)

	checks := make([]storage.CheckRecord, len(commands))
	for index, command := range commands {
		checks[index] = storage.CheckRecord{ID: fmt.Sprintf("%s-%d", stepRunID, index), ExecutionID: executionID, StepID: step.ID, StepRunID: stepRunID, Position: index, Argv: command, Status: storage.CheckQueued}
		service.saveCheck(executionID, stepRunID, checks[index])
	}
	service.emit(EventChecks, executionID)
	// Whatever ends the step early, the commands it never reached are
	// skipped rather than left queued.
	next := 0
	defer func() {
		for index := range checks {
			if checks[index].Status == storage.CheckQueued {
				checks[index].Status = storage.CheckSkipped
				service.saveCheck(executionID, stepRunID, checks[index])
			}
		}
		service.emit(EventChecks, executionID)
	}()

	verification := make([]string, 0, len(commands))
	var failed []string
	var firstFailure error
	for ; next < len(checks); next++ {
		if ctx.Err() != nil {
			return domain.HandoffResult{}, ctx.Err()
		}
		if len(failed) > 0 && !step.ContinueOnFailure {
			break
		}
		check := &checks[next]
		command := check.Argv
		label := strings.Join(command, " ")
		if err := service.appendRunEvent(executionID, syntheticEvent(stepRunID, int64(next*2), domain.EventTool, "Running "+label)); err != nil {
			return domain.HandoffResult{}, err
		}
		if err := inputs.write(); err != nil {
			return domain.HandoffResult{}, fmt.Errorf("write human input answers: %w", err)
		}
		check.Status, check.StartedAt = storage.CheckRunning, time.Now().UTC()
		service.saveCheck(executionID, stepRunID, *check)
		log := newCheckLog(deps.checkLogBytes, secretValues, func() { service.emit(EventCheckOutput, check.ID) })
		service.setLiveCheck(check.ID, log)
		service.emit(EventChecks, executionID)

		process := proc.Command(ctx, command[0], command[1:]...)
		process.Dir = workingDirectory
		// The last value of a repeated variable wins, so an inherited
		// INKUNA_HUMAN_INPUTS cannot point the check at another file.
		process.Env = append(append(append([]string(nil), environment...), colourEnvironment...), humanInputsEnv+"="+inputs.path)
		// Marked as a worker is, so a check a crash left running ends at the
		// next startup.
		if instance := service.instance(); instance != "" {
			process.Env = append(process.Env, proc.Mark(instance))
		}
		process.Stdout = log
		process.Stderr = log
		runErr := proc.Start(process)
		if runErr == nil {
			runErr = process.Wait()
		}
		log.Close()
		check.FinishedAt = time.Now().UTC()
		text, cut, written := log.snapshot()
		check.LogBytes, check.LogCut = written, cut
		var exit *exec.ExitError
		switch {
		case runErr == nil:
			code := 0
			check.ExitCode, check.Status = &code, storage.CheckPassed
		case ctx.Err() != nil:
			check.Status = storage.CheckCancelled
		case errors.As(runErr, &exit) && exit.ExitCode() >= 0:
			code := exit.ExitCode()
			check.ExitCode, check.Status = &code, storage.CheckFailed
		default:
			// It could not start, or a signal stopped it.
			check.Status, check.Error = storage.CheckFailed, runErr.Error()
		}
		// The log is kept even when the run is being cancelled.
		if ref, err := service.storeArtifact(context.WithoutCancel(ctx), taskID, executionID, step.ID, "", "check", checkLogName(step.ID, next), "text/plain; charset=utf-8", strings.NewReader(text)); err == nil {
			check.LogDigest = ref.Digest
		} else {
			_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, "Could not keep the log of "+label+": "+err.Error()))
		}
		service.saveCheck(executionID, stepRunID, *check)
		service.setLiveCheck(check.ID, nil)
		service.emit(EventChecks, executionID)

		switch check.Status {
		case storage.CheckPassed:
			passed := label + " passed"
			verification = append(verification, passed)
			if err := service.appendRunEvent(executionID, syntheticEvent(stepRunID, int64(next*2+1), domain.EventComplete, passed)); err != nil {
				return domain.HandoffResult{}, err
			}
		case storage.CheckFailed:
			// The timeline and the run's error show the log's end as text,
			// without the colours the check kept it in.
			detail := strings.TrimSpace(lastBytes(ansiEscape.ReplaceAllString(text, ""), failureTail))
			failure := "Verification failed: " + label
			if detail != "" {
				failure += "\n" + detail
			}
			if err := service.appendRunEvent(executionID, syntheticEvent(stepRunID, int64(next*2+1), domain.EventError, failure)); err != nil {
				return domain.HandoffResult{}, err
			}
			failed = append(failed, label)
			if firstFailure == nil {
				firstFailure = fmt.Errorf("verification command %q failed: %w", command[0], runErr)
				if detail != "" {
					firstFailure = fmt.Errorf("%w: %s", firstFailure, detail)
				}
			}
		case storage.CheckCancelled:
			return domain.HandoffResult{}, ctx.Err()
		}
	}
	switch {
	case len(failed) == 1:
		return domain.HandoffResult{}, firstFailure
	case len(failed) > 1:
		return domain.HandoffResult{}, fmt.Errorf("%d verification commands failed (%s); the first: %w", len(failed), strings.Join(failed, ", "), firstFailure)
	}
	return domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted, Summary: "Deterministic verification passed", Verification: verification}, nil
}

// saveCheck records a check. A failure to record it is reported on the
// timeline instead of failing the verification, whose outcome does not
// depend on it.
func (service *Service) saveCheck(executionID, stepRunID string, check storage.CheckRecord) {
	if err := service.store.SaveCheck(context.Background(), check); err != nil {
		_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, "Could not record the check "+strings.Join(check.Argv, " ")+": "+err.Error()))
	}
}

func (service *Service) setLiveCheck(id string, log *checkLog) {
	service.checksMu.Lock()
	defer service.checksMu.Unlock()
	if log == nil {
		delete(service.liveChecks, id)
		return
	}
	if service.liveChecks == nil {
		service.liveChecks = make(map[string]*checkLog)
	}
	service.liveChecks[id] = log
}

func (service *Service) liveCheck(id string) *checkLog {
	service.checksMu.Lock()
	defer service.checksMu.Unlock()
	return service.liveChecks[id]
}

// taskOf names a run's task, from the live run or its stored record.
func (service *Service) taskOf(executionID string) string {
	service.runsMu.RLock()
	run := service.runs[executionID]
	service.runsMu.RUnlock()
	if run != nil && run.task.ID != "" {
		return run.task.ID
	}
	if execution, err := service.store.Execution(context.Background(), executionID); err == nil {
		return execution.TaskID
	}
	return ""
}

func checkLogName(stepID string, position int) string {
	return fmt.Sprintf("%s-check-%d.log", stepID, position+1)
}

// lastBytes is the end of text, from the start of a line.
func lastBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[len(text)-limit:]
	if newline := strings.IndexByte(text, '\n'); newline >= 0 {
		text = text[newline+1:]
	}
	return text
}

// RunChecks lists the checks of a run's evaluation steps, in the order
// they were recorded, each with its step's title and how long it ran.
func (service *Service) RunChecks(runID string) ([]CheckView, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	ctx := service.context()
	execution, err := service.execution(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}
	records, err := service.store.Checks(ctx, runID)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	if workflow, err := service.store.Workflow(ctx, execution.WorkflowID); err == nil {
		for _, step := range workflow.Steps {
			titles[step.ID] = step.Title
		}
	}
	now := time.Now().UTC()
	views := make([]CheckView, 0, len(records))
	for _, record := range records {
		view := CheckView{CheckRecord: record, StepTitle: titles[record.StepID]}
		switch {
		case record.StartedAt.IsZero():
		case !record.FinishedAt.IsZero():
			view.DurationMs = record.FinishedAt.Sub(record.StartedAt).Milliseconds()
		case record.Status == storage.CheckRunning:
			view.DurationMs = now.Sub(record.StartedAt).Milliseconds()
		}
		views = append(views, view)
	}
	return views, nil
}

// CheckLog reads a check's log. While the command runs it is the log so
// far, from the byte offset the view already has; once the command has
// ended it is the stored log, whole.
func (service *Service) CheckLog(checkID string, from int) (CheckLogView, error) {
	if err := service.ready(); err != nil {
		return CheckLogView{}, err
	}
	if log := service.liveCheck(checkID); log != nil {
		text, cut, written := log.snapshot()
		view := CheckLogView{Running: true, Cut: cut, Bytes: written, End: len(text)}
		// An offset that is not one this log handed out, or a log that has
		// left output out and so changed, gets the whole log again.
		if cut || from < 0 || from > len(text) || (from < len(text) && !utf8.RuneStart(text[from])) {
			view.Text, view.Reset = text, true
		} else {
			view.Text, view.Offset = text[from:], from
		}
		return view, nil
	}
	check, err := service.store.Check(service.context(), checkID)
	if err != nil {
		return CheckLogView{}, err
	}
	view := CheckLogView{Reset: true, Running: check.Status == storage.CheckRunning, Cut: check.LogCut, Bytes: check.LogBytes}
	if check.LogDigest == "" {
		return view, nil
	}
	text, err := service.readCheckLog(check)
	if err != nil {
		return CheckLogView{}, err
	}
	view.Text, view.End = text, len(text)
	return view, nil
}

func (service *Service) readCheckLog(check storage.CheckRecord) (string, error) {
	file, err := service.artifacts.Open(check.LogDigest)
	if err != nil {
		return "", fmt.Errorf("open the log of %s: %w", strings.Join(check.Argv, " "), err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read the log of %s: %w", strings.Join(check.Argv, " "), err)
	}
	return string(data), nil
}

// SaveCheckLog writes a finished check's stored log to a file the user
// picks; it returns the path, or "" when they cancel.
func (service *Service) SaveCheckLog(checkID string) (string, error) {
	if service.ctx == nil || service.ctx.Value("frontend") == nil {
		return "", errors.New("saving a file is available in the Inkuna desktop app")
	}
	check, err := service.store.Check(service.context(), checkID)
	if err != nil {
		return "", err
	}
	if check.LogDigest == "" {
		return "", errors.New("this check has no stored log yet")
	}
	text, err := service.readCheckLog(check)
	if err != nil {
		return "", err
	}
	path, err := wailsruntime.SaveFileDialog(service.ctx, wailsruntime.SaveDialogOptions{
		Title: "Save check log", DefaultFilename: checkLogName(check.StepID, check.Position), CanCreateDirectories: true,
		Filters: []wailsruntime.FileFilter{{DisplayName: "Logs (*.log, *.txt)", Pattern: "*.log;*.txt"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := writeExport(path, []byte(text)); err != nil {
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	return path, nil
}

// checkTotals counts the checks of each step's latest attempt: a step that
// ran again (a feedback round, say) is counted by its newest checks only.
func checkTotals(records []storage.CheckRecord) *CheckTotals {
	if len(records) == 0 {
		return nil
	}
	latest := make(map[string]string)
	for _, record := range records {
		latest[record.StepID] = record.StepRunID
	}
	totals := &CheckTotals{}
	for _, record := range records {
		if latest[record.StepID] != record.StepRunID {
			continue
		}
		totals.Total++
		switch record.Status {
		case storage.CheckPassed:
			totals.Passed++
		case storage.CheckFailed:
			totals.Failed++
		case storage.CheckRunning:
			totals.Running++
		}
	}
	return totals
}

// attachCheckTotals gives each run view its check totals.
func (service *Service) attachCheckTotals(views []WorkflowRunView) {
	if service.store == nil || len(views) == 0 {
		return
	}
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	byRun, err := service.store.ChecksFor(service.context(), ids)
	if err != nil {
		return
	}
	for index := range views {
		views[index].Checks = checkTotals(byRun[views[index].ID])
	}
}
