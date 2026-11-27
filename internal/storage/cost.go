package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// TaskCost is what a task cost and where, from its usage rows and its runs'
// records. A plan that failed before its task was saved still left its
// calls under the task id, so those are found without the task; an id with
// neither calls nor a task is not found.
func (store *Store) TaskCost(ctx context.Context, taskID string) (domain.TaskCost, error) {
	calls, err := store.usage(ctx, `WHERE task_id=? ORDER BY id`, taskID)
	if err != nil {
		return domain.TaskCost{}, err
	}
	executions, err := store.queryExecutions(ctx, `SELECT `+executionColumns+` FROM executions WHERE task_id=?`, taskID)
	if err != nil {
		return domain.TaskCost{}, fmt.Errorf("query runs of task %s: %w", taskID, err)
	}
	if len(calls) == 0 && len(executions) == 0 {
		if _, err := store.Task(ctx, taskID); err != nil {
			return domain.TaskCost{}, err
		}
	}
	// Ordered here rather than in SQL: stored times trim their fractions, so
	// as text they do not sort within a second.
	sort.SliceStable(executions, func(a, b int) bool { return executions[a].StartedAt.Before(executions[b].StartedAt) })
	runs := make([]domain.RunCost, 0, len(executions))
	for _, execution := range executions {
		runs = append(runs, domain.RunCost{ExecutionID: execution.ID, State: execution.State, StartedAt: execution.StartedAt, FinishedAt: execution.FinishedAt})
	}
	return domain.BuildTaskCost(taskID, runs, calls), nil
}

// costColumns are what a period's or a task's totals need of a usage row;
// scanCost reads them into a call.
const costColumns = `u.task_id,u.auth_mode,u.input_tokens,u.cache_write_tokens,u.long_cache_write_tokens,u.cache_read_tokens,u.output_tokens,u.reported_cents,u.premium_requests,u.turns,u.cost_cents,u.cost_source,u.estimated_cents,u.pricing_version,u.started_at,u.finished_at`

func scanCost(rows rowScanner) (domain.UsageRecord, error) {
	var call domain.UsageRecord
	var started, finished string
	usage, price := &call.Usage, &call.Cost
	if err := rows.Scan(&call.TaskID, &call.AuthMode, &usage.InputTokens, &usage.CacheWriteTokens, &usage.LongCacheWriteTokens, &usage.CacheReadTokens, &usage.OutputTokens, &usage.ReportedCents, &usage.PremiumRequests, &usage.Turns,
		&price.Cents, &price.Source, &price.EstimatedCents, &price.PricingVersion, &started, &finished); err != nil {
		return domain.UsageRecord{}, fmt.Errorf("read usage row: %w", err)
	}
	call.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	call.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
	return call, nil
}

// TaskTotals sums every call made for each of the tasks, for lists that show
// what a task cost at a glance. A task with no calls is left out.
func (store *Store) TaskTotals(ctx context.Context, taskIDs []string) (map[string]domain.CallTotals, error) {
	totals := make(map[string]domain.CallTotals, len(taskIDs))
	if len(taskIDs) == 0 {
		return totals, nil
	}
	encoded, err := json.Marshal(taskIDs)
	if err != nil {
		return nil, fmt.Errorf("encode task ids: %w", err)
	}
	// The ids go as one JSON array, as in describeTasks.
	rows, err := store.db.QueryContext(ctx, `SELECT `+costColumns+` FROM json_each(?) j CROSS JOIN usage u ON u.task_id=j.value ORDER BY u.id`, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("query usage of tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		call, err := scanCost(rows)
		if err != nil {
			return nil, err
		}
		totals[call.TaskID] = totals[call.TaskID].Add(call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read usage rows: %w", err)
	}
	return totals, nil
}

// PeriodCost is what tasks cost over a period: the calls that ended at or
// after from and before to, summed per task, the most expensive task first.
// An empty projectID takes every project's tasks, and a zero bound leaves
// that side open. Calls of a plan that failed before its task was saved
// belong to no project, so they are not counted here.
func (store *Store) PeriodCost(ctx context.Context, projectID string, from, to time.Time) (domain.PeriodCost, error) {
	// RFC 3339 times trim their fractions, so as text they do not sort within
	// a second. SQL narrows to whole seconds, which a bare second prefix
	// bounds exactly, and the exact bounds are applied to the parsed times.
	where, args := []string{"1"}, []any{}
	if projectID != "" {
		where, args = append(where, "t.project_id=?"), append(args, projectID)
	}
	if !from.IsZero() {
		where, args = append(where, "u.finished_at>=?"), append(args, from.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05"))
	}
	if !to.IsZero() {
		where, args = append(where, "u.finished_at<?"), append(args, to.UTC().Truncate(time.Second).Add(time.Second).Format("2006-01-02T15:04:05"))
	}
	rows, err := store.db.QueryContext(ctx, `SELECT `+costColumns+` FROM usage u JOIN tasks t ON t.id=u.task_id WHERE `+strings.Join(where, " AND ")+` ORDER BY u.id`, args...)
	if err != nil {
		return domain.PeriodCost{}, fmt.Errorf("query usage of the period: %w", err)
	}
	defer func() { _ = rows.Close() }()
	cost := domain.PeriodCost{Tasks: []domain.TaskCostSummary{}}
	index := make(map[string]int)
	for rows.Next() {
		call, err := scanCost(rows)
		if err != nil {
			return domain.PeriodCost{}, err
		}
		if (!from.IsZero() && call.FinishedAt.Before(from)) || (!to.IsZero() && !call.FinishedAt.Before(to)) {
			continue
		}
		position, ok := index[call.TaskID]
		if !ok {
			position = len(cost.Tasks)
			index[call.TaskID] = position
			cost.Tasks = append(cost.Tasks, domain.TaskCostSummary{TaskID: call.TaskID})
		}
		task := &cost.Tasks[position]
		task.Total = task.Total.Add(call)
		if call.FinishedAt.After(task.LastCallAt) {
			task.LastCallAt = call.FinishedAt
		}
		cost.Total = cost.Total.Add(call)
	}
	if err := rows.Err(); err != nil {
		return domain.PeriodCost{}, fmt.Errorf("read usage rows: %w", err)
	}
	if err := store.describeTasks(ctx, cost.Tasks); err != nil {
		return domain.PeriodCost{}, err
	}
	// Tasks that cost the same, such as those nothing priced, come most
	// recently active first.
	sort.SliceStable(cost.Tasks, func(a, b int) bool {
		left, right := cost.Tasks[a], cost.Tasks[b]
		if left.Total.BestCents != right.Total.BestCents {
			return left.Total.BestCents > right.Total.BestCents
		}
		return left.LastCallAt.After(right.LastCallAt)
	})
	return cost, nil
}

// describeTasks fills in each summary's project, objective, kind, latest
// run, and state from its task. Only the summarized tasks are read, and of
// each spec only its objective, so a short period does not load every task.
// Their ids go as one JSON array, which no count of tasks can push past
// SQLite's limit on bound parameters, and CROSS JOIN keeps SQLite looking
// each one up by key rather than walking the tasks. Runs are inserted as
// they start, so the latest is the last inserted, and the state is the
// task's as TaskStates derives it, never the one it was planned in.
func (store *Store) describeTasks(ctx context.Context, summaries []domain.TaskCostSummary) error {
	if len(summaries) == 0 {
		return nil
	}
	index := make(map[string]int, len(summaries))
	ids := make([]string, 0, len(summaries))
	for position, summary := range summaries {
		index[summary.TaskID] = position
		ids = append(ids, summary.TaskID)
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("encode task ids: %w", err)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT t.id,t.project_id,t.kind,COALESCE(json_extract(t.spec_json,'$.title'),''),COALESCE(json_extract(t.spec_json,'$.objective'),''),COALESCE(e.id,''),`+taskStateSQL+`
FROM json_each(?) j CROSS JOIN tasks t ON t.id=j.value
LEFT JOIN executions e ON e.rowid=(SELECT latest.rowid FROM executions latest WHERE latest.task_id=t.id ORDER BY latest.rowid DESC LIMIT 1)`, string(encoded))
	if err != nil {
		return fmt.Errorf("query tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, project, kind, title, objective, run, state string
		if err := rows.Scan(&id, &project, &kind, &title, &objective, &run, &state); err != nil {
			return fmt.Errorf("read task row: %w", err)
		}
		summary := &summaries[index[id]]
		summary.ProjectID, summary.Objective, summary.Kind, summary.State, summary.LatestRunID = project, objective, domain.TaskKind(kind), state, run
		summary.Title = domain.TaskSpec{Title: title, Objective: objective}.DisplayTitle()
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read task rows: %w", err)
	}
	return nil
}
