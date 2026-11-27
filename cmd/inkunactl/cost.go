package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

const costUsage = "usage: inkunactl cost [--since DATE] [--csv] | inkunactl cost TASK [--by step|agent|runtime|model] [--csv]"

// planningKey names the planning calls where a task's steps are listed, so
// the rows add up to the task: planning belongs to no step.
const planningKey = "(planning)"

// runCost prints what tasks cost: every task, the most expensive first, or
// one task's breakdown by run and by step, agent, runtime, or model. It
// reads Inkuna's database as the app does, so it can run beside the app.
func runCost(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("cost", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	since := flags.String("since", "", "count only calls that ended on or after DATE (YYYY-MM-DD, local time, or an RFC 3339 time)")
	by := flags.String("by", "", "group a task's calls by step, agent, runtime, or model")
	asCSV := flags.Bool("csv", false, "print the rows as CSV")
	// The task may come before the flags or after them.
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, costUsage)
	}
	var taskID string
	if flags.NArg() > 0 {
		taskID = flags.Arg(0)
		if err := flags.Parse(flags.Args()[1:]); err != nil {
			return fmt.Errorf("%w\n%s", err, costUsage)
		}
	}
	if flags.NArg() > 0 {
		return errors.New(costUsage)
	}
	if taskID != "" && *since != "" {
		return errors.New("--since narrows the task list; a task's breakdown covers every call made for it")
	}
	if taskID == "" && *by != "" {
		return errors.New("--by groups one task's calls; name the task")
	}
	if *by == "" {
		*by = "step"
	}
	if !validGrouping(*by) {
		return fmt.Errorf("--by %q must be step, agent, runtime, or model", *by)
	}
	from, err := sinceTime(*since)
	if err != nil {
		return err
	}
	store, path, err := openDatabase()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	// A removed project's tasks still cost what they cost, under its name.
	projects, err := store.AllProjects(ctx)
	if err != nil {
		return fmt.Errorf("load projects: %w", err)
	}
	names := make(map[string]string, len(projects))
	for _, project := range projects {
		names[project.ID] = project.Name
	}
	if taskID == "" {
		cost, err := store.PeriodCost(ctx, "", from, time.Time{})
		if err != nil {
			return fmt.Errorf("load task costs: %w", err)
		}
		if *asCSV {
			return writeTaskListCSV(stdout, cost, names)
		}
		return writeTaskList(stdout, cost, names, *since)
	}
	cost, err := store.TaskCost(ctx, taskID)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("task %q was not found in %s", taskID, path)
	}
	if err != nil {
		return fmt.Errorf("load task cost: %w", err)
	}
	if *asCSV {
		return writeTaskCostCSV(stdout, cost, *by)
	}
	// A plan that failed before its task was saved has calls and no task.
	var objective string
	if task, err := store.Task(ctx, taskID); err == nil {
		objective = task.Objective
	}
	return writeTaskCost(stdout, cost, objective, *by)
}

func validGrouping(by string) bool {
	switch by {
	case "step", "agent", "runtime", "model":
		return true
	}
	return false
}

// sinceTime reads --since: a date is the start of that day in local time.
func sinceTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if day, err := time.ParseInLocation(time.DateOnly, value, time.Local); err == nil {
		return day, nil
	}
	moment, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since %q must be a date such as 2026-10-01 or an RFC 3339 time", value)
	}
	return moment, nil
}

// writeTaskList prints every task with calls, the most expensive first, and
// their total.
func writeTaskList(stdout io.Writer, cost domain.PeriodCost, names map[string]string, since string) error {
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "TASK\tPROJECT\tSTATE\tLAST CALL\tCALLS\tINPUT\tCACHE WRITE\tCACHE READ\tOUTPUT\tTIME\tCOST\tOBJECTIVE\t")
	for _, task := range cost.Tasks {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t\n", task.TaskID, names[task.ProjectID], task.State, clock(task.LastCallAt), task.Total.Calls,
			tokenColumns(task.Total.Usage), callTime(task.Total), totalLabel(task.Total.Cost), shorten(task.Objective, 60))
	}
	_, _ = fmt.Fprintf(table, "TOTAL\t\t\t\t%d\t%s\t%s\t%s\t\t\n", cost.Total.Calls, tokenColumns(cost.Total.Usage), callTime(cost.Total), totalLabel(cost.Total.Cost))
	if err := table.Flush(); err != nil {
		return err
	}
	period := ""
	if since != "" {
		period = " since " + since
	}
	if _, err := fmt.Fprintf(stdout, "\n%d %s%s: %s\n", len(cost.Tasks), plural(len(cost.Tasks), "task", "tasks"), period, costSummary(cost.Total.Cost)); err != nil {
		return err
	}
	return subscriptionNote(stdout, cost.Total)
}

// writeTaskCost prints a task's totals, its planning, each run with its
// steps, and its calls grouped as asked.
func writeTaskCost(stdout io.Writer, cost domain.TaskCost, objective, by string) error {
	heading := "Task " + cost.TaskID
	if objective != "" {
		heading += ": " + objective
	}
	total := cost.Total
	share := "no input"
	if input := total.Usage.TotalInputTokens(); input > 0 {
		share = fmt.Sprintf("%.0f%% read from cache", 100*float64(total.Usage.CacheReadTokens)/float64(input))
	}
	if _, err := fmt.Fprintf(stdout, "%s\n%d %s over %d %s: %d input tokens (%s), %d output tokens, %s\nPlanning: %d %s, %s\n\n",
		heading, total.Calls, plural(total.Calls, "call", "calls"), len(cost.Runs), plural(len(cost.Runs), "run", "runs"), total.Usage.TotalInputTokens(), share, total.Usage.OutputTokens,
		costSummary(total.Cost), cost.Planning.Total.Calls, plural(cost.Planning.Total.Calls, "call", "calls"), totalLabel(cost.Planning.Total.Cost)); err != nil {
		return err
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "RUN\tSTATE\tSTARTED\tCALLS\tINPUT\tCACHE WRITE\tCACHE READ\tOUTPUT\tTIME\tCOST\t")
	for _, run := range cost.Runs {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t\n", run.ExecutionID, run.State, clock(run.StartedAt), run.Total.Calls, tokenColumns(run.Total.Usage), callTime(run.Total), totalLabel(run.Total.Cost))
		for _, step := range run.Steps {
			_, _ = fmt.Fprintf(table, "  %s\t\t\t%d\t%s\t%s\t%s\t\n", step.StepID, step.Total.Calls, tokenColumns(step.Total.Usage), callTime(step.Total), totalLabel(step.Total.Cost))
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout)
	table = tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(table, "%s\tCALLS\tINPUT\tCACHE WRITE\tCACHE READ\tOUTPUT\tTIME\tCOST\t\n", strings.ToUpper(by))
	for _, group := range costGroups(cost, by) {
		_, _ = fmt.Fprintf(table, "%s\t%d\t%s\t%s\t%s\t\n", groupLabel(group.Key, by), group.Total.Calls, tokenColumns(group.Total.Usage), callTime(group.Total), totalLabel(group.Total.Cost))
	}
	_, _ = fmt.Fprintf(table, "TOTAL\t%d\t%s\t%s\t%s\t\n", total.Calls, tokenColumns(total.Usage), callTime(total), totalLabel(total.Cost))
	if err := table.Flush(); err != nil {
		return err
	}
	if total.SubscriptionCents > 0 {
		_, _ = fmt.Fprintln(stdout)
	}
	return subscriptionNote(stdout, total)
}

// costGroups lists a task's calls grouped as asked. Steps leave planning
// out, so it gets a row of its own and the rows add up to the task.
func costGroups(cost domain.TaskCost, by string) []domain.CostGroup {
	switch by {
	case "agent":
		return cost.ByAgent
	case "runtime":
		return cost.ByRuntime
	case "model":
		return cost.ByModel
	}
	groups := append([]domain.CostGroup(nil), cost.ByStep...)
	if cost.Planning.Total.Calls > 0 {
		groups = append(groups, domain.CostGroup{Key: planningKey, Total: cost.Planning.Total})
	}
	return groups
}

// groupLabel shows a model the runtime chose and did not name as "default".
func groupLabel(key, by string) string {
	if key == "" && by == "model" {
		return "default"
	}
	return key
}

var csvHeader = []string{"calls", "input_tokens", "cache_write_tokens", "cache_read_tokens", "output_tokens", "duration_ms", "reported_usd", "estimated_usd", "unknown_calls", "best_usd", "subscription_usd"}

// csvTotals are a total's CSV columns, in csvHeader's order. Costs are in
// dollars with no sign, so a spreadsheet reads them as numbers.
func csvTotals(total domain.CallTotals) []string {
	usd := func(cents float64) string { return strconv.FormatFloat(cents/100, 'f', 6, 64) }
	usage := total.Usage
	return []string{
		strconv.Itoa(total.Calls), strconv.Itoa(usage.InputTokens), strconv.Itoa(usage.CacheWriteTokens), strconv.Itoa(usage.CacheReadTokens), strconv.Itoa(usage.OutputTokens),
		strconv.FormatInt(total.DurationMs, 10), usd(total.Cost.ReportedCents), usd(total.Cost.EstimatedCents), strconv.Itoa(total.Cost.UnknownCalls), usd(total.BestCents), usd(total.SubscriptionCents),
	}
}

// spreadsheetText keeps a text cell from being read as a formula: a
// spreadsheet runs a cell that starts with =, +, -, or @, or with a tab,
// carriage return, or line feed before one, and some locales take the
// full-width ＝ for =. Objectives, names, and step ids come from users and
// repositories. An apostrophe in front makes it text.
func spreadsheetText(value string) string {
	if first, _ := utf8.DecodeRuneInString(value); value != "" && strings.ContainsRune("=+-@\t\r\n＝", first) {
		return "'" + value
	}
	return value
}

func writeTaskListCSV(stdout io.Writer, cost domain.PeriodCost, names map[string]string) error {
	writer := csv.NewWriter(stdout)
	_ = writer.Write(append([]string{"task", "project", "objective", "kind", "state", "last_call"}, csvHeader...))
	for _, task := range cost.Tasks {
		text := []string{task.TaskID, names[task.ProjectID], task.Objective, string(task.Kind), task.State}
		for index := range text {
			text[index] = spreadsheetText(text[index])
		}
		_ = writer.Write(append(append(text, task.LastCallAt.UTC().Format(time.RFC3339)), csvTotals(task.Total)...))
	}
	writer.Flush()
	return writer.Error()
}

func writeTaskCostCSV(stdout io.Writer, cost domain.TaskCost, by string) error {
	writer := csv.NewWriter(stdout)
	_ = writer.Write(append([]string{by}, csvHeader...))
	for _, group := range costGroups(cost, by) {
		_ = writer.Write(append([]string{spreadsheetText(groupLabel(group.Key, by))}, csvTotals(group.Total)...))
	}
	writer.Flush()
	return writer.Error()
}

func subscriptionNote(stdout io.Writer, total domain.CallTotals) error {
	if total.SubscriptionCents == 0 {
		return nil
	}
	_, err := fmt.Fprintf(stdout, "%s of the reported cost came from a subscription login: what those calls would cost through the API, not what the subscription bills.\n", dollars(total.SubscriptionCents))
	return err
}

// clock is a time in the local zone, to the minute; zero is blank.
func clock(moment time.Time) string {
	if moment.IsZero() {
		return ""
	}
	return moment.Local().Format("2006-01-02 15:04")
}

// callTime is how long a total's calls ran, to the second.
func callTime(total domain.CallTotals) string {
	return (time.Duration(total.DurationMs) * time.Millisecond).Round(time.Second).String()
}

// shorten cuts text to at most limit characters, marking the cut.
func shorten(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}
