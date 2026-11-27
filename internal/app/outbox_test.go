package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// fakeSink is a sink a test steers. It notes every ID it is offered, waits
// while hold is set, ignoring its context as a hung sink would, and then
// refuses while refuse is set; otherwise it accepts.
type fakeSink struct {
	mu      sync.Mutex
	hold    chan struct{}
	refuse  error
	offered []string
	// active counts Record calls running now, and most the largest number
	// ever running at once.
	active, most int
	audits       []extension.AuditEvent
	usages       []extension.UsageRecord
}

func (sink *fakeSink) offer(id string) error {
	sink.mu.Lock()
	sink.offered = append(sink.offered, id)
	sink.active++
	sink.most = max(sink.most, sink.active)
	hold := sink.hold
	sink.mu.Unlock()
	if hold != nil {
		<-hold
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.active--
	return sink.refuse
}

// release lets a held or refusing sink accept from now on.
func (sink *fakeSink) release() {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.hold != nil {
		close(sink.hold)
		sink.hold = nil
	}
	sink.refuse = nil
}

// accepted returns what the sink accepted, once each by ID, in the order it
// first accepted them. A record a held call accepts late may come twice, as
// the outbox promises at-least-once delivery.
func (sink *fakeSink) accepted() ([]extension.AuditEvent, []extension.UsageRecord) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	seen := map[string]bool{}
	audits, usages := []extension.AuditEvent{}, []extension.UsageRecord{}
	for _, event := range sink.audits {
		if !seen[event.ID] {
			seen[event.ID] = true
			audits = append(audits, event)
		}
	}
	for _, record := range sink.usages {
		if !seen[record.ID] {
			seen[record.ID] = true
			usages = append(usages, record)
		}
	}
	return audits, usages
}

func (sink *fakeSink) offeredIDs() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]string(nil), sink.offered...)
}

type auditSinkOf struct{ *fakeSink }

func (sink auditSinkOf) Record(_ context.Context, event extension.AuditEvent) error {
	if err := sink.offer(event.ID); err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.audits = append(sink.audits, event)
	return nil
}

type usageSinkOf struct{ *fakeSink }

func (sink usageSinkOf) Record(_ context.Context, record extension.UsageRecord) error {
	if err := sink.offer(record.ID); err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.usages = append(sink.usages, record)
	return nil
}

// fastDelivery shortens the delivery loops' waits for the test. Called
// before the service is built, it is undone after the service shuts down.
func fastDelivery(t *testing.T) {
	t.Helper()
	timeout, retryMin, retryMax, poll := sinkTimeout, sinkRetryMin, sinkRetryMax, outboxPoll
	sinkTimeout, sinkRetryMin, sinkRetryMax, outboxPoll = 100*time.Millisecond, 5*time.Millisecond, 20*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { sinkTimeout, sinkRetryMin, sinkRetryMax, outboxPoll = timeout, retryMin, retryMax, poll })
}

// withSinks registers ext on a service built without New and starts its
// sinks, as initialize does.
func withSinks(t *testing.T, service *Service, ext *fakeExtension) {
	t.Helper()
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{ext})
	if err != nil {
		t.Fatal(err)
	}
	service.extensions = registry
	if err := service.startSinks(); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func sinkBacklog(t *testing.T, service *Service, name string) int {
	t.Helper()
	sinks, err := service.store.OutboxSinks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, sink := range sinks {
		if sink.Name == name {
			return sink.Backlog
		}
	}
	t.Fatalf("sink %s is not recorded", name)
	return 0
}

func healthOf(service *Service, name string) (HealthCheck, bool) {
	for _, check := range service.Doctor() {
		if check.Name == name {
			return check, true
		}
	}
	return HealthCheck{}, false
}

func gitRepository(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if err := runGit(path, "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(path, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	return path
}

func startTestRun(t *testing.T, service *Service) WorkflowRunView {
	t.Helper()
	encoded, err := json.Marshal(workflowTestSpec(t, service))
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	// Settled, so a test that deletes the run once it ends does not race
	// the goroutine still storing its end.
	return settledRun(t, service, run.ID, "completed")
}

func TestASlowAndAFailingSinkNeverHoldUpARun(t *testing.T) {
	fastDelivery(t)
	service := workflowTestService(t, gitRepository(t), &workflowRuntime{})
	// The usage sink hangs, ignoring its context; the audit sink refuses.
	slow := &fakeSink{hold: make(chan struct{})}
	failing := &fakeSink{refuse: errors.New("connection refused")}
	withSinks(t, service, &fakeExtension{
		name: "records", identities: []extension.IdentityProvider{&fakeIdentity{principal: ada}},
		auditSinks: []extension.AuditSink{auditSinkOf{failing}}, usageSinks: []extension.UsageSink{usageSinkOf{slow}},
	})
	t.Cleanup(slow.release)
	run := startTestRun(t, service)

	eventually(t, "Doctor shows both sinks stuck", func() bool {
		audit, okAudit := healthOf(service, "Sink records/audit")
		usage, okUsage := healthOf(service, "Sink records/usage")
		return okAudit && okUsage && audit.Status == "error" && strings.Contains(audit.Detail, "records waiting") && strings.Contains(audit.Detail, "connection refused") &&
			usage.Status == "error" && strings.Contains(usage.Detail, "did not accept record")
	})
	// Timing out again and again, the hung sink still has one call running:
	// each retry waits for it rather than starting another.
	slow.mu.Lock()
	most, offered := slow.most, len(slow.offered)
	slow.mu.Unlock()
	if most != 1 || offered != 1 {
		t.Fatalf("the hung sink had %d calls running at once and was offered %d times, want 1 and 1", most, offered)
	}
	slow.release()
	failing.release()
	eventually(t, "both sinks catch up", func() bool {
		return sinkBacklog(t, service, "records/audit") == 0 && sinkBacklog(t, service, "records/usage") == 0
	})
	if check, _ := healthOf(service, "Sink records/audit"); check.Status != "ready" || check.Detail != "every audit record delivered" {
		t.Fatalf("a sink that caught up shows %+v", check)
	}

	installation, err := service.store.Installation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	audits, _ := failing.accepted()
	kinds := map[string]int{}
	var last int64
	for _, event := range audits {
		kinds[event.Kind]++
		sequence, named := strings.CutPrefix(event.ID, installation+"/audit/")
		number, err := strconv.ParseInt(sequence, 10, 64)
		if !named || err != nil || event.Installation != installation {
			t.Fatalf("event %s is not named by installation %s", event.ID, installation)
		}
		// In the order they were recorded.
		if number <= last {
			t.Fatalf("event %s came after number %d", event.ID, last)
		}
		last = number
		if event.Fields["event"] == string(domain.EventMessage) {
			t.Fatalf("an agent's message was audited: %+v", event)
		}
	}
	if kinds[auditApproval] != 1 || kinds[storage.AuditRunEvent] == 0 || kinds[auditA2ATask] == 0 {
		t.Fatalf("audited kinds = %v, want one approval, run events, and A2A tasks", kinds)
	}
	for _, event := range audits {
		if event.Kind == auditApproval {
			if event.Fields["decision"] != "plan.approve" || event.Summary != "Approved the plan" || event.Principal != ada.ID || event.Run != run.ID || event.Task != run.TaskID || event.Project != "project" || event.Actor != "user" {
				t.Fatalf("the approval = %+v", event)
			}
		}
		if event.Kind == storage.AuditRunEvent && (event.Run != run.ID || event.Principal != ada.ID) {
			t.Fatalf("a run event names another run or approver: %+v", event)
		}
	}

	_, usages := slow.accepted()
	calls, err := service.store.RunUsage(context.Background(), run.ID)
	if err != nil || len(calls) == 0 || len(usages) != len(calls) {
		t.Fatalf("delivered %d usage records for %d calls (%v)", len(usages), len(calls), err)
	}
	for index, record := range usages {
		call := calls[index]
		want := extension.UsageRecord{
			ID: installation + "/" + jsonNumber(call.ID), Installation: installation, Principal: ada.ID, Project: "project", Task: run.TaskID, Run: run.ID,
			Step: call.StepID, Call: call.CallID, Turn: call.Turn, Kind: call.Kind, Agent: call.Agent, Runtime: string(call.Runtime), Model: call.Model, ModelUsed: call.ModelUsed,
			AuthMode: call.AuthMode, Outcome: call.Outcome, StartedAt: call.StartedAt, FinishedAt: call.FinishedAt,
			Tokens:  extension.Tokens{Input: call.Usage.InputTokens, CacheWrite: call.Usage.CacheWriteTokens, LongCacheWrite: call.Usage.LongCacheWriteTokens, CacheRead: call.Usage.CacheReadTokens, Output: call.Usage.OutputTokens},
			Premium: call.Usage.PremiumRequests, Cost: extension.Cost{Source: call.Cost.Source, Cents: call.Cost.Cents, EstimatedCents: call.Cost.EstimatedCents, PricingVersion: call.Cost.PricingVersion},
		}
		if !record.StartedAt.Equal(want.StartedAt) || !record.FinishedAt.Equal(want.FinishedAt) {
			t.Fatalf("usage record %d times = %v..%v, want %v..%v", index, record.StartedAt, record.FinishedAt, want.StartedAt, want.FinishedAt)
		}
		record.StartedAt, record.FinishedAt = want.StartedAt, want.FinishedAt
		if record != want {
			t.Fatalf("usage record %d = %+v\nwant %+v", index, record, want)
		}
	}
}

func jsonNumber(id int64) string { return strconv.FormatInt(id, 10) }

// A record queued just before a crash, and never delivered, is delivered
// after the next start, under the ID it was first offered with.
func TestARecordQueuedBeforeACrashIsDeliveredAfterIt(t *testing.T) {
	fastDelivery(t)
	path := filepath.Join(t.TempDir(), "inkuna.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := &fakeSink{refuse: errors.New("offline")}
	before := &Service{store: store}
	withSinks(t, before, &fakeExtension{name: "records", auditSinks: []extension.AuditSink{auditSinkOf{first}}, usageSinks: []extension.UsageSink{usageSinkOf{first}}})
	before.audit(storage.AuditRecord{Kind: auditApproval, Summary: "Approved the plan"})
	call := domain.UsageRecord{TaskID: "task", StepID: "staffing", CallID: "plan-1", Turn: 1, Kind: domain.CallOrchestrator, Agent: "orchestrator", Runtime: domain.RuntimeClaude, Outcome: domain.OutcomeCompleted}
	if _, err := store.RecordUsage(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	eventually(t, "both records are offered", func() bool { return len(first.offeredIDs()) >= 2 })
	offered := map[string]bool{}
	for _, id := range first.offeredIDs() {
		offered[id] = true
	}
	// The crash: the loops stop and the store closes with nothing delivered.
	before.stopSinks()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	second := &fakeSink{}
	after := &Service{store: store}
	withSinks(t, after, &fakeExtension{name: "records", auditSinks: []extension.AuditSink{auditSinkOf{second}}, usageSinks: []extension.UsageSink{usageSinkOf{second}}})
	t.Cleanup(after.stopSinks)
	eventually(t, "the records are delivered after the restart", func() bool {
		audits, usages := second.accepted()
		return len(audits) == 1 && len(usages) == 1
	})
	audits, usages := second.accepted()
	if !offered[audits[0].ID] || !offered[usages[0].ID] || audits[0].Summary != "Approved the plan" || usages[0].Call != "plan-1" {
		t.Fatalf("after the restart got %+v and %+v, offered before %v", audits[0], usages[0], offered)
	}
}

// History deletion removes a task's usage rows, never what was queued from
// them: a sink that was down while the task was deleted still gets them.
func TestDeletingATaskBeforeDeliveryStillDeliversItsRecords(t *testing.T) {
	fastDelivery(t)
	service := workflowTestService(t, gitRepository(t), &workflowRuntime{})
	sink := &fakeSink{refuse: errors.New("offline")}
	withSinks(t, service, &fakeExtension{name: "records", auditSinks: []extension.AuditSink{auditSinkOf{sink}}, usageSinks: []extension.UsageSink{usageSinkOf{sink}}})
	run := startTestRun(t, service)
	calls, err := service.store.RunUsage(context.Background(), run.ID)
	if err != nil || len(calls) == 0 {
		t.Fatalf("the run recorded no usage: %v", err)
	}
	if _, err := service.DeleteTasks([]string{run.TaskID}, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	service.auditWG.Wait()
	if left, err := service.store.PlanningUsage(context.Background(), run.TaskID); err != nil || len(left) != 0 {
		t.Fatalf("the task's usage was not deleted: %+v %v", left, err)
	}
	sink.release()
	eventually(t, "the deleted task's records are delivered", func() bool {
		return sinkBacklog(t, service, "records/usage") == 0 && sinkBacklog(t, service, "records/audit") == 0
	})
	audits, usages := sink.accepted()
	if len(usages) != len(calls) {
		t.Fatalf("delivered %d usage records of the deleted task's %d calls", len(usages), len(calls))
	}
	for _, record := range usages {
		if record.Task != run.TaskID || record.Run != run.ID {
			t.Fatalf("a usage record names another task: %+v", record)
		}
	}
	// The run's last A2A snapshot may be recorded after the deletion, so
	// the deletion is looked for rather than taken to be last.
	deletions := 0
	for _, event := range audits {
		if event.Kind != auditHistoryDelete {
			continue
		}
		deletions++
		if event.Fields["task"] != run.TaskID || event.Fields["runs"] != run.ID || event.Actor != "user" {
			t.Fatalf("the deletion = %+v", event)
		}
	}
	if deletions != 1 {
		t.Fatalf("recorded %d deletions, want 1", deletions)
	}
}

// The first usage sink a data directory has is given every call already
// stored, oldest first, and a later start gives them again to no one.
func TestTheFirstUsageSinkGetsTheUsageAlreadyStored(t *testing.T) {
	fastDelivery(t)
	service := workflowTestService(t, gitRepository(t), &workflowRuntime{})
	run := startTestRun(t, service)
	calls, err := service.store.RunUsage(context.Background(), run.ID)
	if err != nil || len(calls) == 0 {
		t.Fatalf("the run recorded no usage: %v", err)
	}
	var queued int
	if err := service.store.DB().QueryRow(`SELECT count(*) FROM outbox`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("a build without sinks queued %d records (%v)", queued, err)
	}
	if _, ok := healthOf(service, "Sink records/usage"); ok {
		t.Fatal("Doctor reports a sink in a build without one")
	}

	sink := &fakeSink{}
	withSinks(t, service, &fakeExtension{name: "records", usageSinks: []extension.UsageSink{usageSinkOf{sink}}})
	eventually(t, "the stored usage is delivered", func() bool {
		_, usages := sink.accepted()
		return len(usages) == len(calls)
	})
	installation, err := service.store.Installation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, usages := sink.accepted()
	for index, call := range calls {
		if usages[index].ID != installation+"/"+jsonNumber(call.ID) {
			t.Fatalf("usage record %d = %s, want call %d, oldest first", index, usages[index].ID, call.ID)
		}
	}
	if err := service.store.DB().QueryRow(`SELECT count(*) FROM outbox`).Scan(&queued); err != nil || queued != len(calls) {
		t.Fatalf("the backfill queued %d records, want %d (%v)", queued, len(calls), err)
	}

	// A restart: the same sink is not given them again, and nothing is
	// queued twice.
	service.stopSinks()
	service.outbox = nil
	again := &fakeSink{}
	withSinks(t, service, &fakeExtension{name: "records", usageSinks: []extension.UsageSink{usageSinkOf{again}}})
	time.Sleep(50 * time.Millisecond)
	if offered := again.offeredIDs(); len(offered) != 0 {
		t.Fatalf("after a restart the sink was offered %v again", offered)
	}
	if err := service.store.DB().QueryRow(`SELECT count(*) FROM outbox`).Scan(&queued); err != nil || queued != len(calls) {
		t.Fatalf("a restart queued usage again: %d records (%v)", queued, err)
	}
}

// Progress that could not be saved after a sink's last batch is saved once
// the store takes it again, though no record comes after it.
func TestProgressASinkMadeIsSavedOnceTheStoreTakesIt(t *testing.T) {
	fastDelivery(t)
	store, err := storage.Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// While full holds a row, saving how far a sink got fails, as on a full
	// disk.
	for _, statement := range []string{
		`CREATE TABLE full (n INTEGER)`,
		`INSERT INTO full VALUES (1)`,
		`CREATE TRIGGER full_disk BEFORE UPDATE OF delivered_through ON outbox_sinks WHEN (SELECT count(*) FROM full) > 0 BEGIN SELECT RAISE(ABORT, 'database or disk is full'); END`,
	} {
		if _, err := store.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	sink := &fakeSink{}
	service := &Service{store: store}
	withSinks(t, service, &fakeExtension{name: "records", auditSinks: []extension.AuditSink{auditSinkOf{sink}}})
	t.Cleanup(service.stopSinks)
	service.audit(storage.AuditRecord{Kind: auditApproval, Summary: "Approved the plan"})
	eventually(t, "the sink accepts the record", func() bool {
		audits, _ := sink.accepted()
		return len(audits) == 1
	})
	time.Sleep(30 * time.Millisecond)
	if backlog := sinkBacklog(t, service, "records/audit"); backlog != 1 {
		t.Fatalf("with the store refusing, the backlog is %d, want the accepted record still counted", backlog)
	}
	if _, err := store.DB().Exec(`DELETE FROM full`); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the progress is saved with no new record", func() bool { return sinkBacklog(t, service, "records/audit") == 0 })
	if offered := sink.offeredIDs(); len(offered) != 1 {
		t.Fatalf("the record was offered %d times, want once", len(offered))
	}
}

// An A2A task's end is recorded once, though the task can still gain an
// artifact after it.
func TestAnA2ATaskIsAuditedOncePerState(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.EnableOutbox(nil, storage.OutboxAudit)
	service := &Service{store: store}
	task := a2a.Task{ID: "a2a-1", ContextID: "task", Agent: "researcher", Status: a2a.TaskStatus{State: a2a.StateWorking}}
	service.auditTask(task)
	service.auditTask(task)
	task.Status.State = a2a.StateCanceled
	service.auditTask(task)
	task.Artifacts = []a2a.Artifact{{Name: "handoff"}}
	service.auditTask(task)
	// An older snapshot arriving late changes nothing, and nor does the end
	// seen again after it.
	task.Status.State = a2a.StateWorking
	service.auditTask(task)
	task.Status.State = a2a.StateCanceled
	service.auditTask(task)
	records, err := store.Outbox(context.Background(), storage.OutboxAudit, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	states := []string{}
	for _, record := range records {
		states = append(states, record.Audit.Fields["state"])
	}
	if strings.Join(states, " ") != "working canceled" {
		t.Fatalf("audited states %v, want working then canceled, once each", states)
	}
}

func TestEachSinkIsNamedByItsExtensionAndPlace(t *testing.T) {
	sink := auditSinkOf{&fakeSink{}}
	registry, err := registerExtensions(extension.Product{}, []extension.Extension{
		&fakeExtension{name: "records", auditSinks: []extension.AuditSink{sink, sink}, usageSinks: []extension.UsageSink{usageSinkOf{&fakeSink{}}}},
		&fakeExtension{name: "costs", usageSinks: []extension.UsageSink{usageSinkOf{&fakeSink{}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, registered := range registry.sinks {
		names = append(names, registered.name)
	}
	if want := "records/audit records/audit-2 records/usage costs/usage"; strings.Join(names, " ") != want {
		t.Fatalf("sinks are named %v, want %s", names, want)
	}
	for _, ext := range []*fakeExtension{
		{name: "records", auditSinks: []extension.AuditSink{nil}},
		{name: "records", usageSinks: []extension.UsageSink{nil}},
	} {
		if _, err := registerExtensions(extension.Product{}, []extension.Extension{ext}); err == nil || !strings.Contains(err.Error(), `register extension "records": its`) || !strings.Contains(err.Error(), "sink is nil") {
			t.Fatalf("a nil sink was registered: %v", err)
		}
	}
}

func TestDoctorReportsASinkThisBuildDoesNotHave(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.EnableOutbox(nil, storage.OutboxAudit)
	if _, err := store.RegisterOutboxSink(context.Background(), "records/audit", storage.OutboxAudit); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAudit(context.Background(), storage.AuditRecord{Kind: auditApproval}); err != nil {
		t.Fatal(err)
	}
	// Refused by the build that had it: still missing here, with why.
	if err := store.MarkOutboxFailed(context.Background(), "records/audit", "connection refused"); err != nil {
		t.Fatal(err)
	}
	service := &Service{store: store}
	check, ok := healthOf(service, "Sink records/audit")
	if !ok || check.Status != "missing" || !strings.HasPrefix(check.Detail, "1 audit record waiting; not registered in this build; refused 1 times in a row") || !strings.HasSuffix(check.Detail, ": connection refused") {
		t.Fatalf("Doctor shows %+v, %v", check, ok)
	}
}

// A save is recorded with the digest of what the file holds now, and who
// saved it, never the file's content.
func TestASaveIsAuditedByDigest(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.EnableOutbox(nil, storage.OutboxAudit)
	service := &Service{store: store}
	withIdentity(t, service, &fakeIdentity{principal: ada})
	path := filepath.Join(t.TempDir(), "rules.yaml")
	content := []byte("rules:\n  - name: secret-flow\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	service.auditChange(auditRulesSave, "project", "Saved the rules of Project", nil, path, content)
	// Replaced at once, as by another save: the digest is still of what
	// this save wrote.
	if err := os.WriteFile(path, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.auditWG.Wait()
	records, err := store.Outbox(context.Background(), storage.OutboxAudit, 0, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("queued %d records (%v)", len(records), err)
	}
	digest := sha256.Sum256(content)
	got := records[0].Audit
	if got.Kind != auditRulesSave || got.Principal != ada.ID || got.Project != "project" || got.Fields["path"] != path || got.Fields["digest"] != "sha256:"+hex.EncodeToString(digest[:]) {
		t.Fatalf("the save = %+v", got)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "secret-flow") {
		t.Fatalf("the audit event carries the file's content: %s", encoded)
	}
}
