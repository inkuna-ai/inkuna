package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/inkuna-ai/inkuna/internal/clock"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

// RecordUsage stores one model call and returns its id. Prompt bytes are
// always the sum of the sections, so the two cannot disagree, and a call
// nothing priced is stored as unknown. With a usage sink registered, the
// call is queued for it in the same transaction, so a call is never stored
// without being queued.
func (store *Store) RecordUsage(ctx context.Context, record domain.UsageRecord) (int64, error) {
	return store.RecordUsageFor(ctx, record, UsageOrigin{})
}

// RecordUsageFor is RecordUsage for a call whose task may not be saved yet,
// as a planning call's is not: origin names who it was made for, where
// RecordUsage reads that from the call's run or task.
func (store *Store) RecordUsageFor(ctx context.Context, record domain.UsageRecord, origin UsageOrigin) (int64, error) {
	if record.TaskID == "" || record.StepID == "" || record.CallID == "" || record.Kind == "" || record.Outcome == "" {
		return 0, errors.New("usage task, step, call, kind, and outcome are required")
	}
	if record.Outcome == domain.OutcomeRunning {
		return 0, fmt.Errorf("call %s is still running: only a call that ended is stored", record.CallID)
	}
	if record.Kind != domain.CallOrchestrator && record.ExecutionID == "" {
		return 0, fmt.Errorf("a %s call needs its run: only orchestrator calls come before one", record.Kind)
	}
	if record.Sections == nil {
		record.Sections = []domain.PromptSection{}
	}
	record.PromptBytes = 0
	for _, section := range record.Sections {
		record.PromptBytes += section.Bytes
	}
	sections, err := json.Marshal(record.Sections)
	if err != nil {
		return 0, fmt.Errorf("encode prompt sections of call %s: %w", record.CallID, err)
	}
	if record.FinishedAt.IsZero() {
		record.FinishedAt = clock.Now()
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = record.FinishedAt
	}
	if record.Cost.Source == "" {
		record.Cost = domain.Cost{Source: domain.CostUnknown}
	}
	usage, cost := record.Usage, record.Cost
	queue := store.outbox.usage.Load()
	var q querier = store.db
	var transaction *sql.Tx
	if queue {
		if transaction, err = store.db.BeginTx(ctx, nil); err != nil {
			return 0, fmt.Errorf("record usage of call %s: %w", record.CallID, err)
		}
		defer func() { _ = transaction.Rollback() }()
		q = transaction
	}
	result, err := q.ExecContext(ctx, `INSERT INTO usage(task_id,execution_id,step_id,call_id,turn,kind,agent,runtime,model,model_used,auth_mode,input_tokens,cache_write_tokens,long_cache_write_tokens,cache_read_tokens,output_tokens,reported_cents,premium_requests,cost_cents,cost_source,estimated_cents,pricing_version,turns,prompt_bytes,prompt_sections_json,prompt_digest,outcome,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.TaskID, sql.NullString{String: record.ExecutionID, Valid: record.ExecutionID != ""}, record.StepID, record.CallID, record.Turn, record.Kind, record.Agent, string(record.Runtime), record.Model, record.ModelUsed, record.AuthMode,
		usage.InputTokens, usage.CacheWriteTokens, usage.LongCacheWriteTokens, usage.CacheReadTokens, usage.OutputTokens, usage.ReportedCents, usage.PremiumRequests,
		cost.Cents, cost.Source, cost.EstimatedCents, cost.PricingVersion, usage.Turns, record.PromptBytes, sections, record.PromptDigest, record.Outcome,
		timestamp(record.StartedAt), timestamp(record.FinishedAt))
	if err != nil {
		return 0, fmt.Errorf("record usage of call %s: %w", record.CallID, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read the id of call %s's usage: %w", record.CallID, err)
	}
	if !queue {
		return id, nil
	}
	record.ID = id
	if err := queueUsage(ctx, transaction, record, origin); err != nil {
		return 0, fmt.Errorf("queue usage of call %s: %w", record.CallID, err)
	}
	if err := transaction.Commit(); err != nil {
		return 0, fmt.Errorf("record usage of call %s: %w", record.CallID, err)
	}
	store.notifyQueued(OutboxUsage)
	return id, nil
}

// RunUsage lists every call a run paid for, in the order they finished:
// the orchestrator calls that planned its task, then its own calls.
func (store *Store) RunUsage(ctx context.Context, executionID string) ([]domain.UsageRecord, error) {
	execution, err := store.Execution(ctx, executionID)
	if err != nil {
		return nil, err
	}
	return store.usage(ctx, `WHERE (task_id=? AND execution_id IS NULL) OR execution_id=? ORDER BY execution_id IS NOT NULL, id`, execution.TaskID, executionID)
}

// PlanningUsage lists the orchestrator calls made while planning a task.
func (store *Store) PlanningUsage(ctx context.Context, taskID string) ([]domain.UsageRecord, error) {
	return store.usage(ctx, `WHERE task_id=? AND execution_id IS NULL ORDER BY id`, taskID)
}

// StepUsage lists one step's calls in a run: its turns and its peers' answers.
func (store *Store) StepUsage(ctx context.Context, executionID, stepID string) ([]domain.UsageRecord, error) {
	return store.usage(ctx, `WHERE execution_id=? AND step_id=? ORDER BY id`, executionID, stepID)
}

// callOfRun matches a call to the step run that made it, whose round it
// was made in: a step run's first call is named after it, and each later
// turn's after it with -turn<N> added.
const callOfRun = `(usage.call_id = runs.id OR substr(usage.call_id, 1, length(runs.id) + 5) = runs.id || '-turn')`

func (store *Store) usage(ctx context.Context, where string, args ...any) ([]domain.UsageRecord, error) {
	return queryUsage(ctx, store.db, where, args...)
}

func queryUsage(ctx context.Context, q querier, where string, args ...any) ([]domain.UsageRecord, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,task_id,COALESCE(execution_id,''),step_id,call_id,turn,COALESCE((SELECT MAX(runs.round) FROM runs WHERE runs.execution_id = usage.execution_id AND `+callOfRun+`), 0),kind,agent,runtime,model,model_used,auth_mode,input_tokens,cache_write_tokens,long_cache_write_tokens,cache_read_tokens,output_tokens,reported_cents,premium_requests,cost_cents,cost_source,estimated_cents,pricing_version,turns,prompt_bytes,prompt_sections_json,prompt_digest,outcome,started_at,finished_at FROM usage `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query usage: %w", err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]domain.UsageRecord, 0)
	for rows.Next() {
		var record domain.UsageRecord
		var runtime, sections, started, finished string
		usage, cost := &record.Usage, &record.Cost
		if err := rows.Scan(&record.ID, &record.TaskID, &record.ExecutionID, &record.StepID, &record.CallID, &record.Turn, &record.Round, &record.Kind, &record.Agent, &runtime, &record.Model, &record.ModelUsed, &record.AuthMode,
			&usage.InputTokens, &usage.CacheWriteTokens, &usage.LongCacheWriteTokens, &usage.CacheReadTokens, &usage.OutputTokens, &usage.ReportedCents, &usage.PremiumRequests,
			&cost.Cents, &cost.Source, &cost.EstimatedCents, &cost.PricingVersion, &usage.Turns, &record.PromptBytes, &sections, &record.PromptDigest, &record.Outcome, &started, &finished); err != nil {
			return nil, fmt.Errorf("read usage row: %w", err)
		}
		record.Runtime = domain.Runtime(runtime)
		if err := json.Unmarshal([]byte(sections), &record.Sections); err != nil {
			return nil, fmt.Errorf("decode prompt sections of usage %d: %w", record.ID, err)
		}
		record.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		record.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read usage rows: %w", err)
	}
	return records, nil
}
