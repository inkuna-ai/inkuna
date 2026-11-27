package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/testutil/fakecli"
)

// fakeClaude writes a Claude Code stand-in that asks a question on its first
// turn and finishes on a turn that starts over. A resumed turn reports its
// session and fails, after a tool call when acts is set: as a CLI whose
// session is gone fails just after starting up, or one that dies at work.
func fakeClaude(t *testing.T, acts bool) string {
	t.Helper()
	tool := ""
	if acts {
		tool = `printf '%s\n' '{"type":"assistant","message":{"model":"haiku","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"notes.md"}}]}}'` + "\n  "
	}
	script := `#!/bin/sh
prompt=$(cat)
resume=""
while [ $# -gt 0 ]; do [ "$1" = --resume ] && resume=$2; shift; done
if [ -n "$resume" ]; then
  printf '%s\n' '{"type":"system","subtype":"init","session_id":"'"$resume"'"}'
  ` + tool + `echo "No conversation found with session ID: $resume" >&2
  exit 1
fi
case "$prompt" in
  *"# Previous turn"*) printf '%s\n' '{"type":"system","subtype":"init","session_id":"second"}' '{"type":"result","subtype":"success","session_id":"second","structured_output":{"version":1,"status":"completed","summary":"Used sqlite"}}' ;;
  *) printf '%s\n' '{"type":"system","subtype":"init","session_id":"first"}' '{"type":"result","subtype":"success","session_id":"first","structured_output":{"version":1,"status":"input-required","summary":"Read the handlers","humanInput":{"question":"Which database?"}}}' ;;
esac
`
	binary := filepath.Join(t.TempDir(), "claude")
	binary = fakecli.Write(t, binary, script)
	return binary
}

func TestAResumeThatFailsAtStartupStartsOverThroughTheRealParser(t *testing.T) {
	// Claude Code's init event is a message on the timeline but not the
	// model at work, so the resume falls back to a fresh turn.
	service := workflowTestService(t, t.TempDir(), agentruntime.NewClaude(fakeClaude(t, false)))
	run := startSpec(t, service, workflowTestSpec(t, service))
	completed := answerTheQuestion(t, service, run.ID)
	warned := false
	for _, event := range completed.Events {
		warned = warned || strings.Contains(event.Summary, "session could not be resumed")
	}
	if !warned || completed.Steps[0].Summary != "Used sqlite" {
		t.Fatalf("a resume that failed at startup must start the turn over: %#v", completed)
	}
	usage, err := service.RunUsage(run.ID)
	if err != nil || len(usage.Calls) != 3 || usage.Calls[1].Outcome != domain.OutcomeFailed || usage.Calls[2].Outcome != domain.OutcomeCompleted {
		t.Fatalf("expected the first turn, the failed resume, and the fresh turn: %+v, %v", usage.Calls, err)
	}
}

func TestAResumeThatFailsAtWorkIsNotStartedOverThroughTheRealParser(t *testing.T) {
	service := workflowTestService(t, t.TempDir(), agentruntime.NewClaude(fakeClaude(t, true)))
	run := startSpec(t, service, workflowTestSpec(t, service))
	question := waitForInbox(t, service, 1)[0]
	if _, err := service.AnswerInput(question.ID, "sqlite"); err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "failed")
	usage, err := service.RunUsage(run.ID)
	if err != nil || len(usage.Calls) != 2 {
		t.Fatalf("a resume that failed after the model acted must not run again: %+v, %v", usage.Calls, err)
	}
}
