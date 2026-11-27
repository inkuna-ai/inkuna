package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// outboxCall is a model call of fillTask's run alpha-1, or of its planning
// when run is empty.
func outboxCall(run, call string) domain.UsageRecord {
	start := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	kind, step := domain.CallWorker, "implement"
	if run == "" {
		kind, step = domain.CallOrchestrator, "staffing"
	}
	return domain.UsageRecord{
		TaskID: "alpha", ExecutionID: run, StepID: step, CallID: call, Turn: 1, Kind: kind, Agent: "implementer", Runtime: domain.RuntimeClaude, Model: "claude-opus-5-5",
		Usage: domain.Usage{InputTokens: 10, CacheWriteTokens: 200, OutputTokens: 30, Turns: 1}, Cost: domain.Cost{Cents: 4, Source: domain.CostEstimated, EstimatedCents: 4, PricingVersion: 2},
		Sections: []domain.PromptSection{{Section: domain.SectionSkill, Name: "conventions", Bytes: 4000}}, PromptDigest: "abc123",
		Outcome: domain.OutcomeCompleted, StartedAt: start, FinishedAt: start.Add(time.Minute),
	}
}

func outboxRecords(t *testing.T, store *Store, kind OutboxKind) []OutboxRecord {
	t.Helper()
	records, err := store.Outbox(context.Background(), kind, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestWithoutASinkNothingIsQueued(t *testing.T) {
	store := historyStore(t)
	fillTask(t, store, "alpha", []string{"alpha-1"}, "")
	ctx := context.Background()
	if _, err := store.RecordUsage(ctx, outboxCall("alpha-1", "alpha-1-call-2")); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAudit(ctx, AuditRecord{Kind: "approval"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("a store with no sink queued %d records (%v)", count, err)
	}
	if store.Auditing() {
		t.Fatal("a store with no sink says it is auditing")
	}
}

func TestAUsageRowIsQueuedWithItsOriginAndNoPrompt(t *testing.T) {
	store := historyStore(t)
	fillTask(t, store, "alpha", []string{"alpha-1"}, "")
	ctx := context.Background()
	if _, err := store.DB().Exec(`UPDATE executions SET approved_by='grace' WHERE id='alpha-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE tasks SET created_by='ada' WHERE id='alpha'`); err != nil {
		t.Fatal(err)
	}
	var woken atomic.Int32
	store.EnableOutbox(func(kind OutboxKind) {
		if kind == OutboxUsage {
			woken.Add(1)
		}
	}, OutboxUsage)
	worker, err := store.RecordUsage(ctx, outboxCall("alpha-1", "alpha-1-call-2"))
	if err != nil {
		t.Fatal(err)
	}
	// A planning call is stored before its task is saved, so the caller
	// names who it was for; without that, the task's creator is read.
	planning, err := store.RecordUsageFor(ctx, outboxCall("", "plan-1"), UsageOrigin{Project: "project", Principal: "lin"})
	if err != nil {
		t.Fatal(err)
	}
	read, err := store.RecordUsage(ctx, outboxCall("", "plan-2"))
	if err != nil {
		t.Fatal(err)
	}
	records := outboxRecords(t, store, OutboxUsage)
	if len(records) != 3 || woken.Load() != 3 {
		t.Fatalf("queued %d usage records and woke %d times, want 3 of each", len(records), woken.Load())
	}
	want := []struct {
		id                 int64
		project, principal string
	}{{worker, "project", "grace"}, {planning, "project", "lin"}, {read, "project", "ada"}}
	for index, record := range records {
		queued := record.Usage
		if queued == nil || queued.Record.ID != want[index].id || queued.Project != want[index].project || queued.Principal != want[index].principal {
			t.Fatalf("record %d = %+v, want usage %d for %s by %s", index, queued, want[index].id, want[index].project, want[index].principal)
		}
		if queued.Record.Sections != nil || queued.Record.PromptDigest != "" || queued.Record.PromptBytes != 0 {
			t.Fatalf("a queued record keeps what the prompt held: %+v", queued.Record)
		}
		if queued.Record.Usage.CacheWriteTokens != 200 || queued.Record.Cost.PricingVersion != 2 {
			t.Fatalf("a queued record lost its counts or cost: %+v", queued.Record)
		}
	}
	// The usage table keeps the sections.
	calls, err := store.StepUsage(ctx, "alpha-1", "implement")
	if err != nil || len(calls) != 2 || len(calls[1].Sections) != 1 || calls[1].PromptDigest != "abc123" {
		t.Fatalf("the stored row lost its sections: %+v %v", calls, err)
	}
}

func TestDeletingATaskKeepsWhatWasQueuedForIt(t *testing.T) {
	store := historyStore(t)
	fillTask(t, store, "alpha", []string{"alpha-1"}, "")
	ctx := context.Background()
	store.EnableOutbox(nil, OutboxUsage, OutboxAudit)
	if _, err := store.RecordUsage(ctx, outboxCall("alpha-1", "alpha-1-call-2")); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, domain.RunEvent{Version: 1, RunID: "alpha-1-implement", Sequence: 2, Timestamp: time.Now(), Kind: domain.EventLifecycle, Summary: "finished"}); err != nil {
		t.Fatal(err)
	}
	before := len(outboxRecords(t, store, OutboxUsage)) + len(outboxRecords(t, store, OutboxAudit))
	if _, err := store.DeleteTasks(ctx, []string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	if calls, err := store.PlanningUsage(ctx, "alpha"); err != nil || len(calls) != 0 {
		t.Fatalf("the task's usage rows were not deleted: %+v %v", calls, err)
	}
	usage, audit := outboxRecords(t, store, OutboxUsage), outboxRecords(t, store, OutboxAudit)
	if before != 2 || len(usage)+len(audit) != before || usage[0].Usage.Record.TaskID != "alpha" {
		t.Fatalf("deleting the task touched the outbox: %d records before, %d usage and %d audit after", before, len(usage), len(audit))
	}
}

func TestTheBackfillQueuesStoredUsageOnceOldestFirst(t *testing.T) {
	store := historyStore(t)
	fillTask(t, store, "alpha", []string{"alpha-1", "alpha-2"}, "")
	ctx := context.Background()
	stored, err := store.usage(ctx, `ORDER BY id`)
	if err != nil || len(stored) != 3 {
		t.Fatalf("the fixture stores %d usage rows (%v), want 3", len(stored), err)
	}
	store.EnableOutbox(nil, OutboxUsage)
	// Recorded once the outbox is on, so queued as it is stored, and left
	// alone by the backfill.
	live, err := store.RecordUsage(ctx, outboxCall("alpha-1", "alpha-1-call-2"))
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.BackfillUsage(ctx)
	if err != nil || queued != 3 {
		t.Fatalf("BackfillUsage = %d, %v; want the 3 rows stored before", queued, err)
	}
	records := outboxRecords(t, store, OutboxUsage)
	ids := make([]int64, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.Usage.Record.ID)
	}
	if want := []int64{live, stored[0].ID, stored[1].ID, stored[2].ID}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("queued usage rows %v, want %v", ids, want)
	}
	// Once per data directory, even across a restart.
	if queued, err := store.BackfillUsage(ctx); err != nil || queued != 0 {
		t.Fatalf("a second backfill queued %d (%v)", queued, err)
	}
	if len(outboxRecords(t, store, OutboxUsage)) != 4 {
		t.Fatal("a second backfill queued rows again")
	}
}

func TestARunEventIsAuditedInItsTransaction(t *testing.T) {
	store := historyStore(t)
	fillTask(t, store, "alpha", []string{"alpha-1"}, "")
	ctx := context.Background()
	if _, err := store.DB().Exec(`UPDATE executions SET approved_by='grace' WHERE id='alpha-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE steps SET spec_json=json_set(spec_json,'$.agent','implementer') WHERE id='implement'`); err != nil {
		t.Fatal(err)
	}
	store.EnableOutbox(nil, OutboxAudit)
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	events := []domain.RunEvent{
		{Version: 1, RunID: "alpha-1-implement", Sequence: 2, Timestamp: at, Kind: domain.EventMessage, Summary: "Here is what I think of the secret plan"},
		{Version: 1, RunID: "alpha-1-implement", Sequence: 3, Timestamp: at, Kind: domain.EventUsage, Summary: "used tokens"},
		{Version: 1, RunID: "alpha-1-implement", Sequence: 4, Timestamp: at, Kind: domain.EventTool, Summary: "Claude tool result: SECRET_CONTENT of main.go"},
		{Version: 1, RunID: "alpha-1-implement", Sequence: 5, Timestamp: at, Kind: domain.EventLifecycle, Summary: "Wrote to GitHub #64: commented",
			Payload: map[string]any{"trackerWrite": domain.TrackerWrite{Tracker: "github", Action: "comment", Kind: "comment", Issue: "#64", Outcome: domain.TrackerApplied, URL: "https://example.com/c/1"}}},
		{Version: 1, RunID: "alpha-1-implement", Sequence: 6, Timestamp: at, Kind: domain.EventFile, Summary: "Claude tool: Write — SECRET_CONTENT.go"},
		{Version: 1, RunID: "alpha-1-implement", Sequence: 7, Timestamp: at, Kind: domain.EventWarning, Summary: "SECRET_CONTENT on stderr"},
		{Version: 1, RunID: "alpha-1-implement", Sequence: 8, Timestamp: at, Kind: domain.EventError, Summary: "Verification failed: go test\nSECRET_CONTENT"},
		{Version: 1, RunID: "alpha-1-implement", Sequence: 9, Timestamp: at, Kind: domain.EventComplete, Summary: "The handoff says SECRET_CONTENT"},
		{Version: 1, RunID: "alpha-1-implement", Sequence: 10, Timestamp: at, Kind: domain.EventLifecycle, Summary: "Waiting for human input: SECRET_CONTENT of the prompt"},
	}
	for _, event := range events {
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	records := outboxRecords(t, store, OutboxAudit)
	if len(records) != 7 {
		t.Fatalf("queued %d audit events, want every event but the message and the usage: %+v", len(records), records)
	}
	tool, write := *records[0].Audit, *records[1].Audit
	// A run event is recorded by its facts alone: its summary, here the
	// tool's output, stays on the run's timeline.
	wantTool := AuditRecord{Time: at, Kind: AuditRunEvent, Project: "project", Task: "alpha", Run: "alpha-1", Actor: "implementer", Principal: "grace",
		Fields: map[string]string{"event": "tool", "step": "implement", "stepRun": "alpha-1-implement", "sequence": "4"}}
	if !reflect.DeepEqual(tool, wantTool) {
		t.Fatalf("tool event = %+v\nwant %+v", tool, wantTool)
	}
	if write.Kind != AuditTrackerWrite || write.Summary != "" || write.Actor != "implementer" || write.Fields["tracker"] != "github" || write.Fields["outcome"] != domain.TrackerApplied || write.Fields["issue"] != "#64" {
		t.Fatalf("tracker write = %+v", write)
	}
	for _, record := range records {
		if record.Audit.Summary != "" {
			t.Fatalf("a run event's summary reached the outbox: %+v", record.Audit)
		}
		encoded, _ := json.Marshal(record.Audit)
		if strings.Contains(string(encoded), "secret plan") || strings.Contains(string(encoded), "SECRET_CONTENT") {
			t.Fatalf("what a worker, a command, or a person wrote reached the outbox: %s", encoded)
		}
	}
	if last := records[len(records)-1].Audit; last.Fields["event"] != "lifecycle" || last.Fields["sequence"] != "10" {
		t.Fatalf("the lifecycle event lost its facts: %+v", last)
	}
}

func TestSinksRecordWhereTheyStand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	installation, err := store.Installation(ctx)
	if err != nil || len(installation) != 32 {
		t.Fatalf("installation = %q, %v", installation, err)
	}
	store.EnableOutbox(nil, OutboxAudit)
	for _, kind := range []string{"approval", "config.save", "rules.save"} {
		if err := store.QueueAudit(ctx, AuditRecord{Kind: kind}); err != nil {
			t.Fatal(err)
		}
	}
	sink, err := store.RegisterOutboxSink(ctx, "example/audit", OutboxAudit)
	if err != nil || sink.Backlog != 3 || sink.DeliveredThrough != 0 {
		t.Fatalf("a new sink = %+v, %v; want all 3 records waiting", sink, err)
	}
	records := outboxRecords(t, store, OutboxAudit)
	if err := store.MarkOutboxDelivered(ctx, "example/audit", records[0].ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.MarkOutboxFailed(ctx, "example/audit", "connection refused"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.RegisterOutboxSink(ctx, "example/audit", OutboxUsage); err == nil {
		t.Fatal("a sink registered again as another kind was accepted")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// What a sink got survives a restart, and so does the installation.
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if again, err := store.Installation(ctx); err != nil || again != installation {
		t.Fatalf("the installation changed across a restart: %q, %v", again, err)
	}
	sink, err = store.RegisterOutboxSink(ctx, "example/audit", OutboxAudit)
	if err != nil || sink.Backlog != 2 || sink.DeliveredThrough != records[0].ID || sink.LastError != "connection refused" || sink.Failures != 2 || sink.FailedAt.IsZero() {
		t.Fatalf("the sink after a restart = %+v, %v", sink, err)
	}
	if err := store.MarkOutboxDelivered(ctx, "example/audit", records[2].ID); err != nil {
		t.Fatal(err)
	}
	sinks, err := store.OutboxSinks(ctx)
	if err != nil || len(sinks) != 1 || sinks[0].Backlog != 0 || sinks[0].LastError != "" || sinks[0].Failures != 0 {
		t.Fatalf("a sink that caught up = %+v, %v", sinks, err)
	}
	if next := outboxRecords(t, store, OutboxUsage); len(next) != 0 {
		t.Fatalf("audit records listed as usage: %+v", next)
	}
}

func TestUsageSinceReadsWhatTheOutboxWouldQueue(t *testing.T) {
	store := historyStore(t)
	fillTask(t, store, "alpha", []string{"alpha-1"}, "")
	ctx := context.Background()
	if _, err := store.DB().Exec(`UPDATE executions SET approved_by='grace' WHERE id='alpha-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE tasks SET created_by='ada' WHERE id='alpha'`); err != nil {
		t.Fatal(err)
	}
	for _, call := range []domain.UsageRecord{outboxCall("alpha-1", "alpha-1-call-2"), outboxCall("", "plan-2")} {
		if _, err := store.RecordUsage(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	exported, err := store.UsageSince(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	store.EnableOutbox(nil, OutboxUsage)
	if _, err := store.BackfillUsage(ctx); err != nil {
		t.Fatal(err)
	}
	records := outboxRecords(t, store, OutboxUsage)
	if len(exported) < 3 || len(exported) != len(records) {
		t.Fatalf("UsageSince read %d calls and the backfill queued %d", len(exported), len(records))
	}
	for index, record := range records {
		// The round is the run view's, read from the step runs; no record
		// carries it.
		queued := *record.Usage
		queued.Record.Round = 0
		got, _ := json.Marshal(exported[index])
		want, _ := json.Marshal(queued)
		if string(got) != string(want) {
			t.Fatalf("call %d reads\n%s\nwhere the outbox queues\n%s", index, got, want)
		}
	}
	last := exported[len(exported)-1]
	if last.Principal != "ada" || last.Project != "project" || exported[len(exported)-2].Principal != "grace" {
		t.Fatalf("the calls' origins are %+v and %+v", exported[len(exported)-2].UsageOrigin, last.UsageOrigin)
	}
}

func TestUsageSinceStartsAtTheExactTime(t *testing.T) {
	store := historyStore(t)
	ctx := context.Background()
	ended := time.Date(2026, 11, 18, 9, 1, 0, 500_000_000, time.UTC)
	for index, finished := range []time.Time{ended.Add(-time.Hour), ended, ended.Add(time.Hour)} {
		call := outboxCall("", "plan-"+strings.Repeat("x", index+1))
		call.StartedAt, call.FinishedAt = finished.Add(-time.Minute), finished
		if _, err := store.RecordUsage(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		from  time.Time
		calls []string
	}{
		{time.Time{}, []string{"plan-x", "plan-xx", "plan-xxx"}},
		{ended.Add(-200 * time.Millisecond), []string{"plan-xx", "plan-xxx"}},
		{ended, []string{"plan-xx", "plan-xxx"}},
		// Within the same second, after the call ended.
		{ended.Add(200 * time.Millisecond), []string{"plan-xxx"}},
	} {
		exported, err := store.UsageSince(ctx, test.from)
		if err != nil {
			t.Fatal(err)
		}
		calls := make([]string, 0, len(exported))
		for _, call := range exported {
			calls = append(calls, call.Record.CallID)
		}
		if !reflect.DeepEqual(calls, test.calls) {
			t.Fatalf("UsageSince(%s) = %v, want %v", test.from, calls, test.calls)
		}
	}
}
