package app

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// A run whose save fails twice says so on its timeline, since a restart
// would show it as it was last saved.
func TestARunThatCouldNotBeSavedSaysSoOnItsTimeline(t *testing.T) {
	repository := branchingRepository(t, manualWorktrees)
	service := workflowTestService(t, repository, writingRuntime())
	_, run, err := planAndStart(t, service, WorkflowRequestView{Objective: "Fix the login crash #42", Kind: domain.TaskBugfix})
	if err != nil {
		t.Fatal(err)
	}
	waitForWorkflowRun(t, service, run.ID, "completed")
	// From here the database refuses to save a run, as when it stays busy
	// past its timeout.
	if _, err := service.store.DB().Exec(`CREATE TRIGGER refuse_runs BEFORE INSERT ON executions BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	service.persistRun(run.ID)
	if text := logged.String(); !strings.Contains(text, "save run "+run.ID+": ") || !strings.Contains(text, "database is locked (1811); trying again") || !strings.Contains(text, "save run "+run.ID+" again: ") {
		t.Fatalf("both tries are logged:\n%s", text)
	}
	view, err := service.WorkflowRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range view.Events {
		if event.Kind == domain.EventWarning && strings.Contains(event.Summary, "The run's state (completed) was not saved: ") && strings.Contains(event.Summary, "database is locked") && strings.Contains(event.Summary, "can be interrupted although it ended") {
			return
		}
	}
	t.Fatalf("the timeline says the run was not saved: %+v", view.Events)
}
