package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

// codexResult is the line a fake Codex ends with: its final message, which
// is the run's structured result.
const codexResult = `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"status\":\"done\"}"}}`

// runFake runs script as a worker of runtime with budget and records what
// it sends the timeline. A worker that blocked on a full pipe would run into
// the wall time and fail.
func runFake(t *testing.T, newRuntime func(string) AgentRuntime, script string, budget int64) (RunResult, []domain.RunEvent) {
	t.Helper()
	binary := fakecli.Write(t, filepath.Join(t.TempDir(), "fake-worker"), "#!/bin/sh\n"+script)
	events := make([]domain.RunEvent, 0)
	result, err := newRuntime(binary).Start(context.Background(), RunRequest{
		RunID: "run", WorkingDir: t.TempDir(), Prompt: []byte("test"), Schema: []byte(`{"type":"object"}`), Permission: domain.PermissionReadOnly,
		Budget: domain.Budget{WallTimeSeconds: 30, MaxOutputBytes: budget},
	}, func(event domain.RunEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("the worker should finish: %v", err)
	}
	return result, events
}

func countEvents(events []domain.RunEvent, match func(domain.RunEvent) bool) int {
	count := 0
	for _, event := range events {
		if match(event) {
			count++
		}
	}
	return count
}

// TestAWorkerPastItsOutputBudgetStillFinishes writes far more than its
// budget on stdout and on stderr, more than a pipe holds. Inkuna keeps
// reading both, so the worker finishes and its result counts, while the
// timeline keeps only the events inside the budget and one warning.
func TestAWorkerPastItsOutputBudgetStillFinishes(t *testing.T) {
	script := "yes '{\"type\":\"tool_call\",\"name\":\"read\"}' | head -n 20000\n" +
		"yes 'a warning the worker repeats on stderr' | head -n 30000 >&2\n" +
		"printf '%s\\n' '" + codexResult + "'\n"
	result, events := runFake(t, NewCodex, script, 4096)
	if !strings.Contains(string(result.Output), `"status":"done"`) {
		t.Fatalf("the result after the budget is still taken: %s", result.Output)
	}
	if got := countEvents(events, func(event domain.RunEvent) bool { return strings.Contains(event.Summary, "passed its 4 KiB budget") }); got != 1 {
		t.Fatalf("one warning says the budget was passed, not %d", got)
	}
	if tools := countEvents(events, func(event domain.RunEvent) bool { return event.Kind == domain.EventTool }); tools == 0 || tools > 4096/30 {
		t.Fatalf("only the events inside the budget are recorded: %d tool events", tools)
	}
	if warnings := countEvents(events, func(event domain.RunEvent) bool {
		return strings.Contains(event.Summary, "a warning the worker repeats")
	}); warnings == 0 || warnings > stderrBudget/30 {
		t.Fatalf("stderr becomes warnings only up to its budget: %d", warnings)
	}
	if got := countEvents(events, func(event domain.RunEvent) bool {
		return strings.Contains(event.Summary, "stderr passed its 256 KiB budget")
	}); got != 1 {
		t.Fatalf("one warning says stderr passed its budget, not %d", got)
	}
}

// TestALineTooLongToReadIsSkipped writes a stdout line longer than Inkuna
// reads and a stderr line longer than it reads there. Each is skipped, and
// the lines after them are still read.
func TestALineTooLongToReadIsSkipped(t *testing.T) {
	script := "head -c 5000000 /dev/zero | tr '\\0' a\necho\n" +
		"head -c 100000 /dev/zero | tr '\\0' w >&2\necho >&2\necho 'after the long line' >&2\n" +
		"printf '%s\\n' '" + codexResult + "'\n"
	result, events := runFake(t, NewCodex, script, 16<<20)
	if !strings.Contains(string(result.Output), `"status":"done"`) {
		t.Fatalf("the result after the long line is still taken: %s", result.Output)
	}
	if got := countEvents(events, func(event domain.RunEvent) bool {
		return strings.Contains(event.Summary, "Skipped 1 line(s) of the worker's output longer than 4 MiB.")
	}); got != 1 {
		t.Fatalf("one warning says a line was skipped, not %d: %#v", got, events)
	}
	if got := countEvents(events, func(event domain.RunEvent) bool {
		return strings.Contains(event.Summary, "Skipped 1 line(s) of the worker's output longer than 64 KiB.")
	}); got != 1 {
		t.Fatalf("one warning says a stderr line was skipped, not %d", got)
	}
	if countEvents(events, func(event domain.RunEvent) bool { return event.Summary == "after the long line" }) != 1 {
		t.Fatal("stderr after its long line is still read")
	}
}

// TestALongLastLineIsStillReported ends each stream with a line too long to
// read, as a worker whose only diagnostic is one long line does. Nothing
// follows it, and it is still reported.
func TestALongLastLineIsStillReported(t *testing.T) {
	script := "printf '%s\\n' '" + codexResult + "'\n" +
		"head -c 5000000 /dev/zero | tr '\\0' a\n" +
		"head -c 100000 /dev/zero | tr '\\0' w >&2\n"
	result, events := runFake(t, NewCodex, script, 16<<20)
	if !strings.Contains(string(result.Output), `"status":"done"`) {
		t.Fatalf("the result before the long line is kept: %s", result.Output)
	}
	for _, longest := range []string{"4 MiB", "64 KiB"} {
		if got := countEvents(events, func(event domain.RunEvent) bool {
			return strings.Contains(event.Summary, "longer than "+longest+".")
		}); got != 1 {
			t.Fatalf("one warning says a line longer than %s was skipped, not %d", longest, got)
		}
	}
}

// TestATextWorkerPastItsOutputBudgetStillAnswers is the same for a runtime
// declared with plain-text output, whose answer is its last JSON object.
func TestATextWorkerPastItsOutputBudgetStillAnswers(t *testing.T) {
	newText := func(binary string) AgentRuntime {
		agent, err := New(Spec{Name: "fake", Binary: binary, Stdin: StdinPrompt, Output: OutputText, ReadOnlyArgs: []string{"--read-only"}})
		if err != nil {
			t.Fatal(err)
		}
		return agent
	}
	script := "cat > /dev/null\nyes 'thinking out loud about the change' | head -n 20000\nprintf '%s\\n' 'Here is my answer:' '{\"summary\":\"reviewed\"}'\n"
	result, events := runFake(t, newText, script, 4096)
	if string(result.Output) != `{"summary":"reviewed"}` {
		t.Fatalf("output = %s", result.Output)
	}
	if got := countEvents(events, func(event domain.RunEvent) bool { return strings.Contains(event.Summary, "passed its 4 KiB budget") }); got != 1 {
		t.Fatalf("one warning says the budget was passed, not %d", got)
	}
	if messages := countEvents(events, func(event domain.RunEvent) bool { return event.Kind == domain.EventMessage }); messages > 4096/30 {
		t.Fatalf("only the lines inside the budget are recorded: %d", messages)
	}
}

// TestALineStartingAtTheStderrBudgetIsDropped fills the stderr budget
// exactly, in 32-byte lines, and then writes its last word. The budget is
// spent, so that line is dropped, and a warning says so.
func TestALineStartingAtTheStderrBudgetIsDropped(t *testing.T) {
	script := "yes 'a line of exactly 32 bytes.....' | head -n 8192 >&2\necho 'the last word' >&2\n" +
		"printf '%s\\n' '" + codexResult + "'\n"
	_, events := runFake(t, NewCodex, script, 16<<20)
	if got := countEvents(events, func(event domain.RunEvent) bool { return event.Summary == "a line of exactly 32 bytes....." }); got != stderrBudget/32 {
		t.Fatalf("every line inside the budget is recorded: %d", got)
	}
	if countEvents(events, func(event domain.RunEvent) bool { return event.Summary == "the last word" }) != 0 {
		t.Fatal("a line starting at the budget is dropped")
	}
	if got := countEvents(events, func(event domain.RunEvent) bool {
		return strings.Contains(event.Summary, "stderr passed its 256 KiB budget")
	}); got != 1 {
		t.Fatalf("one warning says stderr passed its budget, not %d", got)
	}
}

// TestSkippedLinesCountTowardTheBudget passes each budget in lines too long
// to keep. The events after them are dropped, as they would be after lines
// that were kept.
func TestSkippedLinesCountTowardTheBudget(t *testing.T) {
	script := "head -c 5000000 /dev/zero | tr '\\0' a\necho\n" +
		"yes '{\"type\":\"tool_call\",\"name\":\"read\"}' | head -n 10\n" +
		"for i in 1 2 3; do head -c 100000 /dev/zero | tr '\\0' w >&2; echo >&2; done\necho 'after the long lines' >&2\n" +
		"printf '%s\\n' '" + codexResult + "'\n"
	result, events := runFake(t, NewCodex, script, 1<<20)
	if !strings.Contains(string(result.Output), `"status":"done"`) {
		t.Fatalf("the result after the budget is still taken: %s", result.Output)
	}
	if tools := countEvents(events, func(event domain.RunEvent) bool { return event.Kind == domain.EventTool }); tools != 0 {
		t.Fatalf("no events after a skipped line passed the budget are recorded: %d", tools)
	}
	for _, warning := range []string{"passed its 1 MiB budget", "stderr passed its 256 KiB budget"} {
		if got := countEvents(events, func(event domain.RunEvent) bool { return strings.Contains(event.Summary, warning) }); got != 1 {
			t.Fatalf("one warning says %q, not %d", warning, got)
		}
	}
	if countEvents(events, func(event domain.RunEvent) bool { return event.Summary == "after the long lines" }) != 0 {
		t.Fatal("stderr after skipped lines passed its budget is dropped")
	}
}
