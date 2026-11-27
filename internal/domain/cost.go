package domain

import (
	"sort"
	"time"
)

// CallTotals is what a group of model calls consumed, cost, and took. Cost
// keeps reported and estimated sums apart and counts the calls nothing
// priced; BestCents is their best available total, reported where a call
// has it and otherwise estimated, so with unknown calls it is a floor.
// SubscriptionCents is the part of the reported sum that Claude Code reported
// while signed in to a subscription: what those calls would cost through the
// API, not what the subscription bills. DurationMs adds up how long each call
// ran, so two calls that ran side by side both count.
type CallTotals struct {
	Usage             Usage     `json:"usage"`
	Cost              CostTotal `json:"cost"`
	BestCents         float64   `json:"bestCents"`
	SubscriptionCents float64   `json:"subscriptionCents"`
	Calls             int       `json:"calls"`
	DurationMs        int64     `json:"durationMs"`
}

// Add counts one more call.
func (totals CallTotals) Add(call UsageRecord) CallTotals {
	totals.Usage = totals.Usage.Add(call.Usage)
	totals.Cost = totals.Cost.Add(call.Cost)
	totals.BestCents = totals.Cost.BestCents()
	if call.Cost.Source == CostReported && call.AuthMode == AuthSubscription {
		totals.SubscriptionCents += call.Cost.Cents
	}
	totals.Calls++
	totals.DurationMs += call.Duration().Milliseconds()
	return totals
}

// TaskCost is what a task cost and where: every call made for it, which is
// its planning and each of its runs, failed and cancelled ones included.
// Total covers them all. ByAgent, ByRuntime, and ByModel group the same
// calls, planning included, so each adds up to Total; ByStep groups the
// runs' calls by step id across runs, so it adds up to Total less Planning.
// Groups come most expensive first.
type TaskCost struct {
	TaskID    string       `json:"taskId"`
	Total     CallTotals   `json:"total"`
	Planning  PlanningCost `json:"planning"`
	Runs      []RunCost    `json:"runs"`
	ByAgent   []CostGroup  `json:"byAgent"`
	ByRuntime []CostGroup  `json:"byRuntime"`
	ByModel   []CostGroup  `json:"byModel"`
	ByStep    []CostGroup  `json:"byStep"`
}

// PlanningCost is the orchestrator calls made while planning a task, each
// plan and re-plan, in the order they ended. They come before any run, so
// they belong to the task.
type PlanningCost struct {
	Total CallTotals    `json:"total"`
	Calls []UsageRecord `json:"calls"`
}

// RunCost is one run of a task, as its record says, and what its calls
// cost, step by step.
type RunCost struct {
	ExecutionID string     `json:"executionId"`
	State       string     `json:"state"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  time.Time  `json:"finishedAt"`
	Total       CallTotals `json:"total"`
	Steps       []StepCost `json:"steps"`
}

// StepCost is one step of a run: its turns and its peers' answers, in the
// order they ended. Agent, Runtime, and Model are those of the step's
// latest worker turn, since a peer that answered may be another agent on
// another runtime. StartedAt is when its first call started.
type StepCost struct {
	StepID    string        `json:"stepId"`
	Agent     string        `json:"agent"`
	Runtime   Runtime       `json:"runtime"`
	Model     string        `json:"model"`
	StartedAt time.Time     `json:"startedAt"`
	Total     CallTotals    `json:"total"`
	Calls     []UsageRecord `json:"calls"`
}

// CostGroup is the calls that share one key: an agent, a runtime, a model,
// or a step id. A model key is empty for calls whose runtime chose the
// model and did not say which.
type CostGroup struct {
	Key   string     `json:"key"`
	Total CallTotals `json:"total"`
}

// TaskCostSummary is one task's line in what tasks cost over a period: the
// calls made for it that ended in the period. LastCallAt is when the latest
// of them ended. LatestRunID is the task's latest run, in the period or not,
// empty when it has none, and State is where the task stands: that run's
// state, or, for a task that never ran, approved or planned by whether its
// plan was approved.
type TaskCostSummary struct {
	TaskID      string     `json:"taskId"`
	ProjectID   string     `json:"projectId"`
	Title       string     `json:"title"`
	Objective   string     `json:"objective"`
	Kind        TaskKind   `json:"kind"`
	State       string     `json:"state"`
	LatestRunID string     `json:"latestRunId,omitempty"`
	LastCallAt  time.Time  `json:"lastCallAt"`
	Total       CallTotals `json:"total"`
}

// PeriodCost is what tasks cost over a period: a line for each task with
// calls in it, the most expensive first, and their sum.
type PeriodCost struct {
	Tasks []TaskCostSummary `json:"tasks"`
	Total CallTotals        `json:"total"`
}

// BuildTaskCost arranges a task's calls, in the order they ended, by where
// they were made. Calls without a run are planning. runs are the task's
// runs as recorded, without totals or steps; they keep their order, and a
// run with no calls stays and costs nothing. A run's steps come in the order
// they started.
func BuildTaskCost(taskID string, runs []RunCost, calls []UsageRecord) TaskCost {
	cost := TaskCost{TaskID: taskID, Planning: PlanningCost{Calls: []UsageRecord{}}, Runs: make([]RunCost, 0, len(runs))}
	runIndex := make(map[string]int, len(runs))
	for _, run := range runs {
		run.Total, run.Steps = CallTotals{}, []StepCost{}
		runIndex[run.ExecutionID] = len(cost.Runs)
		cost.Runs = append(cost.Runs, run)
	}
	stepIndex := make(map[string]map[string]int)
	byAgent, byRuntime, byModel, byStep := costGroups{}, costGroups{}, costGroups{}, costGroups{}
	for _, call := range calls {
		cost.Total = cost.Total.Add(call)
		byAgent.add(call.Agent, call)
		byRuntime.add(string(call.Runtime), call)
		byModel.add(call.ModelName(), call)
		if call.ExecutionID == "" {
			cost.Planning.Total = cost.Planning.Total.Add(call)
			cost.Planning.Calls = append(cost.Planning.Calls, call)
			continue
		}
		byStep.add(call.StepID, call)
		// The store never holds a call of a run it does not have, but a call
		// is still counted under its run if one was left out.
		position, ok := runIndex[call.ExecutionID]
		if !ok {
			position = len(cost.Runs)
			runIndex[call.ExecutionID] = position
			cost.Runs = append(cost.Runs, RunCost{ExecutionID: call.ExecutionID, Steps: []StepCost{}})
		}
		run := &cost.Runs[position]
		run.Total = run.Total.Add(call)
		steps := stepIndex[call.ExecutionID]
		if steps == nil {
			steps = make(map[string]int)
			stepIndex[call.ExecutionID] = steps
		}
		index, ok := steps[call.StepID]
		if !ok {
			index = len(run.Steps)
			steps[call.StepID] = index
			run.Steps = append(run.Steps, StepCost{StepID: call.StepID, StartedAt: call.StartedAt, Calls: []UsageRecord{}})
		}
		step := &run.Steps[index]
		step.Total = step.Total.Add(call)
		step.Calls = append(step.Calls, call)
		if call.StartedAt.Before(step.StartedAt) {
			step.StartedAt = call.StartedAt
		}
		if call.Kind != CallPeer {
			step.Agent, step.Runtime, step.Model = call.Agent, call.Runtime, call.ModelName()
		}
	}
	for index := range cost.Runs {
		steps := cost.Runs[index].Steps
		sort.SliceStable(steps, func(a, b int) bool { return steps[a].StartedAt.Before(steps[b].StartedAt) })
	}
	cost.ByAgent, cost.ByRuntime, cost.ByModel, cost.ByStep = byAgent.sorted(), byRuntime.sorted(), byModel.sorted(), byStep.sorted()
	return cost
}

type costGroups map[string]CallTotals

func (groups costGroups) add(key string, call UsageRecord) {
	groups[key] = groups[key].Add(call)
}

// sorted lists the groups most expensive first. Groups that cost the same,
// such as those nothing priced, come by key, so every read has one order.
func (groups costGroups) sorted() []CostGroup {
	list := make([]CostGroup, 0, len(groups))
	for key, total := range groups {
		list = append(list, CostGroup{Key: key, Total: total})
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].Total.BestCents != list[b].Total.BestCents {
			return list[a].Total.BestCents > list[b].Total.BestCents
		}
		return list[a].Key < list[b].Key
	})
	return list
}
