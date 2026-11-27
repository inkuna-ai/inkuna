package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/clock"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// Audit event kinds the service records. Run events and tracker writes are
// recorded by the store, in the transaction that stores them.
const (
	auditA2ATask       = "a2a.task"
	auditApproval      = "approval"
	auditConfigSave    = "config.save"
	auditRulesSave     = "rules.save"
	auditLibrarySave   = "library.save"
	auditAgentSave     = "agent.save"
	auditAgentDelete   = "agent.delete"
	auditHistoryDelete = "history.delete"
)

// auditTask is the bus observer that records an A2A task when it is sent
// and each time its state changes, once each. It records the task's agent, state, and
// how many messages and artifacts it holds, never what they say: those are
// the workers' exchanges.
func (service *Service) auditTask(task a2a.Task) {
	if !service.auditing() {
		return
	}
	state := task.Status.State
	// Held until the event is queued, so events are queued in the order
	// their states were decided. The bus already tells observers a task's
	// snapshots in order; this keeps that true whoever calls.
	service.a2aStatesMu.Lock()
	defer service.a2aStatesMu.Unlock()
	if service.a2aStates == nil {
		service.a2aStates = make(map[string]a2a.TaskState)
	}
	last, seen := service.a2aStates[task.ID]
	// An ended task never changes state again, so a snapshot that says
	// otherwise is an older one, and is not recorded.
	if seen && (last == state || last.Terminal()) {
		return
	}
	// A terminal state is kept too: a task that has ended can still gain an
	// artifact, which must not record its end again. An entry is a few
	// bytes per task the process has seen.
	service.a2aStates[task.ID] = state
	record := storage.AuditRecord{
		Kind: auditA2ATask, Task: task.ContextID, Actor: task.Agent, Summary: fmt.Sprintf("%s's task is %s", task.Agent, state),
		Fields: map[string]string{
			"a2aTask": task.ID, "agent": task.Agent, "state": string(state),
			"messages": strconv.Itoa(len(task.History)), "artifacts": strconv.Itoa(len(task.Artifacts)),
		},
	}
	if step, ok := task.Metadata["stepId"].(string); ok && step != "" {
		record.Fields["step"] = step
	}
	ctx := service.context()
	if run, ok := task.Metadata["executionId"].(string); ok && run != "" {
		record.Run = run
		if execution, err := service.store.Execution(ctx, run); err == nil {
			record.Project, record.Task, record.Principal = execution.ProjectID, execution.TaskID, execution.ApprovedBy
		}
	} else if project, err := service.store.ProjectForTask(ctx, task.ContextID); err == nil {
		record.Project = project.ID
	}
	service.audit(record)
}

// auditDecision records a person's decision on a run, such as approving its
// plan, with the principal who made it. Its summary is Inkuna's wording
// alone: a run's title can be a person's or come from the objective, and the
// event names the run, its task, and its project by ID.
func (service *Service) auditDecision(decision string, principal extension.Principal, execution storage.Execution, summary string, fields map[string]string) {
	if !service.auditing() {
		return
	}
	record := storage.AuditRecord{
		Kind: auditApproval, Project: execution.ProjectID, Task: execution.TaskID, Run: execution.ID, Actor: "user", Principal: principal.ID, Summary: summary,
		Fields: map[string]string{"decision": decision, "round": strconv.Itoa(max(execution.Round, 1))},
	}
	for key, value := range fields {
		if value != "" {
			record.Fields[key] = value
		}
	}
	service.audit(record)
}

// auditChange records a save or a deletion a person made. A saved file is
// named by its path and the digest of content, the bytes the save wrote,
// never the content itself. The digest is of what was written rather than
// of the file read back, which another save could have replaced by then. The principal is asked for only to name who made the change:
// these changes never asked before, so a provider that cannot answer leaves
// it empty rather than undoing them. Some saves hold configMu, which a slow
// provider must not hold up, so the principal is asked for, and the event
// queued, apart; Shutdown waits for it.
func (service *Service) auditChange(kind, project, summary string, fields map[string]string, path string, content []byte) {
	if !service.auditing() {
		return
	}
	record := storage.AuditRecord{Time: clock.Now(), Kind: kind, Project: project, Actor: "user", Summary: summary, Fields: map[string]string{}}
	for key, value := range fields {
		if value != "" {
			record.Fields[key] = value
		}
	}
	if path != "" {
		record.Fields["path"] = path
	}
	if content != nil {
		record.Fields["digest"] = contentDigest(content)
	}
	service.auditWG.Add(1)
	go func() {
		defer service.auditWG.Done()
		if principal, err := service.principal(service.context()); err == nil {
			record.Principal = principal.ID
		}
		service.audit(record)
	}()
}

// auditDeletion records each task, or the one run, a history deletion
// removed. The outbox is never part of a deletion, so what was queued for
// them is still delivered.
func (service *Service) auditDeletion(removals []storage.TaskRemoval, runID string) {
	for _, removal := range removals {
		runs := make([]string, 0, len(removal.Runs))
		for _, run := range removal.Runs {
			runs = append(runs, run.ID)
		}
		summary := "Deleted task " + removal.TaskID + " and its history"
		if runID != "" {
			summary = "Deleted run " + runID + " of task " + removal.TaskID
		}
		service.auditChange(auditHistoryDelete, "", summary, map[string]string{"task": removal.TaskID, "run": runID, "runs": strings.Join(runs, ",")}, "", nil)
	}
}

// inputSteps names the steps of the inputs given, such as the gates a run
// is paused at.
func inputSteps(inputs []storage.InputRequest) string {
	steps := make([]string, 0, len(inputs))
	for _, input := range inputs {
		steps = append(steps, input.StepID)
	}
	return strings.Join(steps, ",")
}

// contentDigest names saved content by its sha256 digest, as audit events
// and policies see it in place of the content.
func contentDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func renamedFrom(renaming bool, originalName string) string {
	if renaming {
		return originalName
	}
	return ""
}
