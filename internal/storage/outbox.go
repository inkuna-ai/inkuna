package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/inkuna-ai/inkuna/internal/clock"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// OutboxKind is which sinks an outbox record is for.
type OutboxKind string

const (
	OutboxAudit OutboxKind = "audit"
	OutboxUsage OutboxKind = "usage"
)

// Audit event kinds the store records itself, from the rows it writes.
const (
	AuditRunEvent     = "run.event"
	AuditTrackerWrite = "tracker.write"
)

// AuditRecord is one audit event as the outbox keeps it, built from what
// Inkuna has already stored and in the redacted form it stored it. It
// never holds a secret value, a worker's transcript, or file content.
type AuditRecord struct {
	Time      time.Time         `json:"time"`
	Kind      string            `json:"kind"`
	Project   string            `json:"project,omitempty"`
	Task      string            `json:"task,omitempty"`
	Run       string            `json:"run,omitempty"`
	Actor     string            `json:"actor,omitempty"`
	Principal string            `json:"principal,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// UsageOrigin is who a model call was made for: the project, and the
// principal who started the work. RecordUsageFor reads what it is not given
// from the call's run, or its task when the call came before a run.
type UsageOrigin struct {
	Project   string `json:"project,omitempty"`
	Principal string `json:"principal,omitempty"`
}

// QueuedUsage is a usage row as the outbox keeps it: its counts and figures,
// without the prompt's sections or digest, and who it was made for.
type QueuedUsage struct {
	Record domain.UsageRecord `json:"record"`
	UsageOrigin
}

// OutboxRecord is one queued record. Exactly one of Audit and Usage is set,
// by Kind.
type OutboxRecord struct {
	ID        int64
	Kind      OutboxKind
	CreatedAt time.Time
	Audit     *AuditRecord
	Usage     *QueuedUsage
}

// OutboxSink is where one registered sink stands: the last record it
// accepted, the error it last returned, and how many records wait for it.
type OutboxSink struct {
	Name             string     `json:"name"`
	Kind             OutboxKind `json:"kind"`
	DeliveredThrough int64      `json:"deliveredThrough"`
	DeliveredAt      time.Time  `json:"deliveredAt,omitzero"`
	// LastError is what the sink returned the last time it refused a
	// record, cleared once it accepts one.
	LastError string    `json:"lastError,omitempty"`
	FailedAt  time.Time `json:"failedAt,omitzero"`
	// Failures counts refusals in a row of the record it is stuck on.
	Failures int `json:"failures"`
	Backlog  int `json:"backlog"`
}

// outbox says which kinds the store queues, and whom to tell when it does.
// It is off until EnableOutbox, so a build with no sink writes nothing.
type outbox struct {
	audit, usage atomic.Bool
	queued       atomic.Pointer[func(OutboxKind)]
}

// EnableOutbox makes the store queue records of the given kinds from now
// on, each in the transaction of the row it is made from, and call queued
// after each commit that queued one. It is called once, before anything
// is recorded, by a build with a sink of that kind.
func (store *Store) EnableOutbox(queued func(OutboxKind), kinds ...OutboxKind) {
	if queued != nil {
		store.outbox.queued.Store(&queued)
	}
	for _, kind := range kinds {
		switch kind {
		case OutboxAudit:
			store.outbox.audit.Store(true)
		case OutboxUsage:
			store.outbox.usage.Store(true)
		}
	}
}

// Auditing reports whether the store queues audit events, so a caller can
// skip building one that would be dropped.
func (store *Store) Auditing() bool { return store.outbox.audit.Load() }

func (store *Store) notifyQueued(kind OutboxKind) {
	if queued := store.outbox.queued.Load(); queued != nil {
		(*queued)(kind)
	}
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Installation is this data directory's random ID, made once by its
// migration, which every record sent to a sink carries.
func (store *Store) Installation(ctx context.Context) (string, error) {
	var id string
	if err := store.db.QueryRowContext(ctx, `SELECT id FROM installation LIMIT 1`).Scan(&id); err != nil {
		return "", fmt.Errorf("read the installation id: %w", err)
	}
	return id, nil
}

// QueueAudit queues an audit event for the audit sinks, and does nothing
// when none is registered.
func (store *Store) QueueAudit(ctx context.Context, record AuditRecord) error {
	if !store.outbox.audit.Load() {
		return nil
	}
	if record.Time.IsZero() {
		record.Time = clock.Now()
	}
	if err := insertOutbox(ctx, store.db, OutboxAudit, 0, record); err != nil {
		return fmt.Errorf("queue the %s audit event: %w", record.Kind, err)
	}
	store.notifyQueued(OutboxAudit)
	return nil
}

func insertOutbox(ctx context.Context, q querier, kind OutboxKind, usageID int64, record any) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO outbox(kind,usage_id,record_json,created_at) VALUES(?,?,?,?)`,
		kind, sql.NullInt64{Int64: usageID, Valid: usageID != 0}, data, timestamp(clock.Now()))
	return err
}

// queueUsage queues a stored usage row, filling what origin leaves empty
// from the row's run, or its task when it has no run yet. A task that is
// still being planned has not been saved, so its origin comes from the
// caller.
func queueUsage(ctx context.Context, q querier, record domain.UsageRecord, origin UsageOrigin) error {
	var project, principal string
	var err error
	if record.ExecutionID != "" {
		err = q.QueryRowContext(ctx, `SELECT project_id, approved_by FROM executions WHERE id=?`, record.ExecutionID).Scan(&project, &principal)
	} else {
		err = q.QueryRowContext(ctx, `SELECT project_id, created_by FROM tasks WHERE id=?`, record.TaskID).Scan(&project, &principal)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read who call %s was made for: %w", record.CallID, err)
	}
	if origin.Project == "" {
		origin.Project = project
	}
	if origin.Principal == "" {
		origin.Principal = principal
	}
	// Counts and figures only: what the prompt held stays on the machine.
	record.Sections, record.PromptDigest, record.PromptBytes = nil, "", 0
	return insertOutbox(ctx, q, OutboxUsage, record.ID, QueuedUsage{Record: record, UsageOrigin: origin})
}

// BackfillUsage queues every usage row stored before a usage sink was first
// registered, oldest first, and records that it did, so it happens once per
// data directory. Rows already queued are left alone. It returns how many
// it queued.
func (store *Store) BackfillUsage(ctx context.Context) (int, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("backfill usage: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var done int
	if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM outbox_backfills WHERE kind=?`, OutboxUsage).Scan(&done); err != nil {
		return 0, fmt.Errorf("backfill usage: %w", err)
	}
	if done > 0 {
		return 0, nil
	}
	records, err := queryUsage(ctx, transaction, `WHERE id NOT IN (SELECT usage_id FROM outbox WHERE usage_id IS NOT NULL) ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("backfill usage: %w", err)
	}
	for _, record := range records {
		if err := queueUsage(ctx, transaction, record, UsageOrigin{}); err != nil {
			return 0, fmt.Errorf("backfill usage %d: %w", record.ID, err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO outbox_backfills(kind,queued,backfilled_at) VALUES(?,?,?)`, OutboxUsage, len(records), timestamp(clock.Now())); err != nil {
		return 0, fmt.Errorf("backfill usage: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return 0, fmt.Errorf("backfill usage: %w", err)
	}
	if len(records) > 0 {
		store.notifyQueued(OutboxUsage)
	}
	return len(records), nil
}

// auditedEvent reports whether a run event becomes an audit event. A message
// is what an agent said, part of its transcript, and a usage event is the
// usage sinks' to send, so neither is.
func auditedEvent(kind domain.EventKind) bool {
	return kind != domain.EventMessage && kind != domain.EventUsage
}

// runEventAudit is the audit event for a run event, with the run, task,
// project, and approver its step run belongs to. It carries the event's
// facts and never its summary: summaries are written for the run's own
// timeline, and of every kind some hold what a worker, a command, or a
// person wrote, redacted of secrets but not of content. A tool event names
// a command or starts its output, a warning is a line of stderr, an error a
// check's log, a completion the worker's handoff, a lifecycle event can
// quote a step's prompt, and a tracker write the comment or title it wrote.
// A tracker write keeps its structured fields, not its description.
func runEventAudit(ctx context.Context, q querier, event domain.RunEvent) (AuditRecord, error) {
	var project, task, execution, approver, step, agent string
	err := q.QueryRowContext(ctx, `SELECT COALESCE(e.project_id,''), COALESCE(e.task_id,''), COALESCE(e.id,''), COALESCE(e.approved_by,''), r.step_id, COALESCE(json_extract(s.spec_json,'$.agent'),'')
FROM runs r LEFT JOIN executions e ON e.id = r.execution_id LEFT JOIN steps s ON s.workflow_id = r.workflow_id AND s.id = r.step_id
WHERE r.id=?`, event.RunID).Scan(&project, &task, &execution, &approver, &step, &agent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AuditRecord{}, fmt.Errorf("read the run of step run %s: %w", event.RunID, err)
	}
	record := AuditRecord{
		Time: event.Timestamp, Kind: AuditRunEvent, Project: project, Task: task, Run: execution, Actor: "inkuna", Principal: approver,
		Fields: map[string]string{"event": string(event.Kind), "step": step, "stepRun": event.RunID, "sequence": strconv.FormatInt(event.Sequence, 10)},
	}
	// What an agent did is the agent's; starting, warning, and failing it
	// are Inkuna's.
	switch event.Kind {
	case domain.EventTool, domain.EventFile, domain.EventComplete:
		if agent != "" {
			record.Actor = agent
		}
	}
	if event.Artifact != nil {
		record.Fields["artifact"] = event.Artifact.Digest
	}
	if write, ok := trackerWriteOf(event); ok {
		record.Kind = AuditTrackerWrite
		record.Actor = "inkuna"
		if write.Kind != "" && agent != "" {
			record.Actor = agent
		}
		for key, value := range map[string]string{"tracker": write.Tracker, "action": write.Action, "issue": write.Issue, "outcome": write.Outcome, "url": write.URL, "kind": string(write.Kind), "update": string(write.Update)} {
			if value != "" {
				record.Fields[key] = value
			}
		}
	}
	return record, nil
}

// trackerWriteOf reads the tracker write a run event reports, which the app
// puts in its payload as it was made, or as JSON once read back.
func trackerWriteOf(event domain.RunEvent) (domain.TrackerWrite, bool) {
	value, ok := event.Payload["trackerWrite"]
	if !ok {
		return domain.TrackerWrite{}, false
	}
	if write, ok := value.(domain.TrackerWrite); ok {
		return write, true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return domain.TrackerWrite{}, false
	}
	var write domain.TrackerWrite
	if err := json.Unmarshal(data, &write); err != nil {
		return domain.TrackerWrite{}, false
	}
	return write, true
}

// Outbox lists up to limit records of a kind queued after the one with ID
// after, in the order they were queued.
func (store *Store) Outbox(ctx context.Context, kind OutboxKind, after int64, limit int) ([]OutboxRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,kind,record_json,created_at FROM outbox WHERE kind=? AND id>? ORDER BY id LIMIT ?`, kind, after, limit)
	if err != nil {
		return nil, fmt.Errorf("read the %s outbox: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]OutboxRecord, 0)
	for rows.Next() {
		var record OutboxRecord
		var data []byte
		var created string
		if err := rows.Scan(&record.ID, &record.Kind, &data, &created); err != nil {
			return nil, fmt.Errorf("read the %s outbox: %w", kind, err)
		}
		record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		switch record.Kind {
		case OutboxAudit:
			record.Audit = &AuditRecord{}
			err = json.Unmarshal(data, record.Audit)
		case OutboxUsage:
			record.Usage = &QueuedUsage{}
			err = json.Unmarshal(data, record.Usage)
		}
		if err != nil {
			return nil, fmt.Errorf("decode outbox record %d: %w", record.ID, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the %s outbox: %w", kind, err)
	}
	return records, nil
}

// RegisterOutboxSink records a sink a build registered, the first time with
// nothing delivered, so it is given every record of its kind still queued,
// and returns where it stands.
func (store *Store) RegisterOutboxSink(ctx context.Context, name string, kind OutboxKind) (OutboxSink, error) {
	if _, err := store.db.ExecContext(ctx, `INSERT INTO outbox_sinks(name,kind) VALUES(?,?) ON CONFLICT(name) DO NOTHING`, name, kind); err != nil {
		return OutboxSink{}, fmt.Errorf("register sink %s: %w", name, err)
	}
	sinks, err := store.outboxSinks(ctx, `WHERE name=?`, name)
	if err != nil {
		return OutboxSink{}, err
	}
	if len(sinks) == 0 || sinks[0].Kind != kind {
		return OutboxSink{}, fmt.Errorf("sink %s is recorded as another kind than %s", name, kind)
	}
	return sinks[0], nil
}

// OutboxSinks lists every sink a build has registered in this data
// directory, by name, with its backlog.
func (store *Store) OutboxSinks(ctx context.Context) ([]OutboxSink, error) {
	return store.outboxSinks(ctx, `ORDER BY name`)
}

func (store *Store) outboxSinks(ctx context.Context, where string, args ...any) ([]OutboxSink, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT name,kind,delivered_through,delivered_at,last_error,failed_at,failures,
(SELECT count(*) FROM outbox WHERE outbox.kind = outbox_sinks.kind AND outbox.id > outbox_sinks.delivered_through)
FROM outbox_sinks `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("read the outbox sinks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	sinks := make([]OutboxSink, 0)
	for rows.Next() {
		var sink OutboxSink
		var delivered, failed string
		if err := rows.Scan(&sink.Name, &sink.Kind, &sink.DeliveredThrough, &delivered, &sink.LastError, &failed, &sink.Failures, &sink.Backlog); err != nil {
			return nil, fmt.Errorf("read the outbox sinks: %w", err)
		}
		sink.DeliveredAt, _ = time.Parse(time.RFC3339Nano, delivered)
		sink.FailedAt, _ = time.Parse(time.RFC3339Nano, failed)
		sinks = append(sinks, sink)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the outbox sinks: %w", err)
	}
	return sinks, nil
}

// MarkOutboxDelivered records that a sink accepted every record of its kind
// through the one with ID through, and clears its last error.
func (store *Store) MarkOutboxDelivered(ctx context.Context, name string, through int64) error {
	if _, err := store.db.ExecContext(ctx, `UPDATE outbox_sinks SET delivered_through=max(delivered_through,?), delivered_at=?, last_error='', failed_at='', failures=0 WHERE name=?`,
		through, timestamp(clock.Now()), name); err != nil {
		return fmt.Errorf("record delivery to sink %s: %w", name, err)
	}
	return nil
}

// MarkOutboxFailed records that a sink refused the record it is stuck on.
func (store *Store) MarkOutboxFailed(ctx context.Context, name, message string) error {
	if _, err := store.db.ExecContext(ctx, `UPDATE outbox_sinks SET last_error=?, failed_at=?, failures=failures+1 WHERE name=?`,
		message, timestamp(clock.Now()), name); err != nil {
		return fmt.Errorf("record a failed delivery to sink %s: %w", name, err)
	}
	return nil
}

// UsageSince lists every stored call that ended at or after from, oldest
// first, as the outbox would queue it: without its prompt, and with who it
// was made for, read from its run, or its task when it has none, as
// queueUsage reads it. A zero from takes every call. It reads only what
// history deletion left, where a sink got every call.
func (store *Store) UsageSince(ctx context.Context, from time.Time) ([]QueuedUsage, error) {
	// RFC 3339 times trim their fractions, so as text they do not sort within
	// a second: SQL narrows to whole seconds and the exact bound is applied
	// to the parsed time, as in PeriodCost.
	where, args := "", []any{}
	if !from.IsZero() {
		where, args = `WHERE u.finished_at>=?`, append(args, from.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05"))
	}
	rows, err := store.db.QueryContext(ctx, `SELECT u.id,u.task_id,COALESCE(u.execution_id,''),u.step_id,u.call_id,u.turn,u.kind,u.agent,u.runtime,u.model,u.model_used,u.auth_mode,
u.input_tokens,u.cache_write_tokens,u.long_cache_write_tokens,u.cache_read_tokens,u.output_tokens,u.reported_cents,u.premium_requests,u.cost_cents,u.cost_source,u.estimated_cents,u.pricing_version,u.turns,u.outcome,u.started_at,u.finished_at,
CASE WHEN u.execution_id IS NULL THEN COALESCE(t.project_id,'') ELSE COALESCE(e.project_id,'') END,
CASE WHEN u.execution_id IS NULL THEN COALESCE(t.created_by,'') ELSE COALESCE(e.approved_by,'') END
FROM usage u LEFT JOIN executions e ON e.id=u.execution_id LEFT JOIN tasks t ON t.id=u.task_id `+where+` ORDER BY u.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("query usage: %w", err)
	}
	defer func() { _ = rows.Close() }()
	calls := make([]QueuedUsage, 0)
	for rows.Next() {
		var call QueuedUsage
		var runtime, started, finished string
		record, usage, cost := &call.Record, &call.Record.Usage, &call.Record.Cost
		if err := rows.Scan(&record.ID, &record.TaskID, &record.ExecutionID, &record.StepID, &record.CallID, &record.Turn, &record.Kind, &record.Agent, &runtime, &record.Model, &record.ModelUsed, &record.AuthMode,
			&usage.InputTokens, &usage.CacheWriteTokens, &usage.LongCacheWriteTokens, &usage.CacheReadTokens, &usage.OutputTokens, &usage.ReportedCents, &usage.PremiumRequests,
			&cost.Cents, &cost.Source, &cost.EstimatedCents, &cost.PricingVersion, &usage.Turns, &record.Outcome, &started, &finished, &call.Project, &call.Principal); err != nil {
			return nil, fmt.Errorf("read usage row: %w", err)
		}
		record.Runtime = domain.Runtime(runtime)
		record.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		record.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
		if !from.IsZero() && record.FinishedAt.Before(from) {
			continue
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read usage rows: %w", err)
	}
	return calls, nil
}
