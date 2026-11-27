package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

const usageUsage = "usage: inkunactl usage [--sections] RUN_ID | inkunactl usage export [--since DATE] [--format csv|jsonl]"

// runUsage prints what a run's model calls consumed, one row per call as the
// run view lists them, so a change to prompts can be measured before and
// after. It opens Inkuna's database as the app does but starts nothing, so
// it can run beside the app.
func runUsage(args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "export" {
		return runUsageExport(args[1:], stdout)
	}
	flags := flag.NewFlagSet("usage", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sections := flags.Bool("sections", false, "list each call's prompt sections in bytes")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, usageUsage)
	}
	if flags.NArg() != 1 {
		return errors.New(usageUsage)
	}
	runID := flags.Arg(0)
	store, path, err := openDatabase()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	calls, err := store.RunUsage(context.Background(), runID)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("run %q was not found in %s", runID, path)
	}
	if err != nil {
		return fmt.Errorf("load run usage: %w", err)
	}
	return writeUsage(stdout, calls, *sections)
}

// openDatabase opens Inkuna's database as the app does, from the configured
// data directory, and returns its path for messages.
func openDatabase() (*storage.Store, string, error) {
	configuration, err := config.Load()
	if err != nil {
		return nil, "", fmt.Errorf("load configuration: %w", err)
	}
	path := filepath.Join(configuration.DataDir, "inkuna.db")
	// Opening creates a missing database, which would hide a wrong data
	// directory behind an empty answer.
	if _, err := os.Stat(path); err != nil {
		return nil, "", fmt.Errorf("no Inkuna database at %s: %w", path, err)
	}
	store, err := storage.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", path, err)
	}
	return store, path, nil
}

// writeUsage prints the calls as a table with the run's total last. Token
// counts are exact integers so two runs can be compared line by line;
// prompt sizes are bytes, roughly four to a token. A cost says where it came
// from: an estimate is marked with ≈ and a call nothing priced with ?.
func writeUsage(stdout io.Writer, calls []domain.UsageRecord, sections bool) error {
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "STEP\tTURN\tKIND\tAGENT\tRUNTIME\tMODEL\tOUTCOME\tINPUT\tCACHE WRITE\tCACHE READ\tOUTPUT\tCOST\tPROMPT BYTES\t")
	promptBytes, subscription := 0, false
	for _, call := range calls {
		promptBytes += call.PromptBytes
		subscription = subscription || (call.Cost.Source == domain.CostReported && call.AuthMode == domain.AuthSubscription)
		_, _ = fmt.Fprintf(table, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t\n", call.StepID, call.Turn, call.Kind, call.Agent, call.Runtime, modelName(call), call.Outcome,
			tokenColumns(call.Usage), costLabel(call.Cost), strconv.Itoa(call.PromptBytes))
	}
	total, cost := domain.TotalUsage(calls), domain.TotalCost(calls)
	_, _ = fmt.Fprintf(table, "TOTAL\t\t\t\t\t\t%d calls\t%s\t%s\t%d\t\n", len(calls), tokenColumns(total), totalLabel(cost), promptBytes)
	if err := table.Flush(); err != nil {
		return err
	}
	share := "no input"
	if input := total.TotalInputTokens(); input > 0 {
		share = fmt.Sprintf("%.0f%% read from cache", 100*float64(total.CacheReadTokens)/float64(input))
	}
	if _, err := fmt.Fprintf(stdout, "\n%d input tokens (%s), %d output tokens, %s\n", total.TotalInputTokens(), share, total.OutputTokens, costSummary(cost)); err != nil {
		return err
	}
	if subscription {
		if _, err := fmt.Fprintln(stdout, "Reported costs from a subscription login are what the calls would cost through the API, not what the subscription bills."); err != nil {
			return err
		}
	}
	if !sections {
		return nil
	}
	_, _ = fmt.Fprintln(stdout, "\nPrompt sections, in bytes:")
	for _, call := range calls {
		parts := make([]string, 0, len(call.Sections))
		for _, section := range call.Sections {
			parts = append(parts, fmt.Sprintf("%s %d", sectionLabel(section), section.Bytes))
		}
		if _, err := fmt.Fprintf(stdout, "%s turn %d %s %s: %s\n", call.StepID, call.Turn, call.Kind, call.Agent, strings.Join(parts, ", ")); err != nil {
			return err
		}
	}
	return nil
}

func tokenColumns(usage domain.Usage) string {
	return strings.Join([]string{strconv.Itoa(usage.InputTokens), strconv.Itoa(usage.CacheWriteTokens), strconv.Itoa(usage.CacheReadTokens), strconv.Itoa(usage.OutputTokens)}, "\t")
}

// dollars shows a cost to the hundredth of a cent, since one call often
// costs less than a cent; less than that still shows it was not nothing.
func dollars(cents float64) string {
	if cents > 0 && cents < 0.01 {
		return "<$0.0001"
	}
	return fmt.Sprintf("$%.4f", cents/100)
}

func costLabel(cost domain.Cost) string {
	switch cost.Source {
	case domain.CostReported:
		return dollars(cost.Cents)
	case domain.CostEstimated:
		return "≈" + dollars(cost.Cents)
	default:
		return "?"
	}
}

// totalLabel is each call's best figure summed, marked as an estimate when
// any part is one, with the calls nothing priced counted beside it. With
// nothing priced it is unknown, not zero.
func totalLabel(total domain.CostTotal) string {
	if total.ReportedCents == 0 && total.EstimatedCents == 0 && total.UnknownCalls > 0 {
		return "?"
	}
	label := dollars(total.BestCents())
	if total.EstimatedCents > 0 {
		label = "≈" + label
	}
	if total.UnknownCalls > 0 {
		label += fmt.Sprintf(" +%d?", total.UnknownCalls)
	}
	return label
}

// costSummary spells out a total by source, never summing one kind into
// another.
func costSummary(total domain.CostTotal) string {
	parts := make([]string, 0, 3)
	if total.ReportedCents > 0 {
		parts = append(parts, dollars(total.ReportedCents)+" reported")
	}
	if total.EstimatedCents > 0 {
		parts = append(parts, "≈"+dollars(total.EstimatedCents)+" estimated")
	}
	if total.UnknownCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d %s without a price", total.UnknownCalls, plural(total.UnknownCalls, "call", "calls")))
	}
	if len(parts) == 0 {
		return "no cost"
	}
	return strings.Join(parts, ", ")
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func sectionLabel(section domain.PromptSection) string {
	if section.Name == "" {
		return section.Section
	}
	return section.Section + ":" + section.Name
}

// modelName shows the model a call's runtime said it used, else the one
// Inkuna asked for, and a call that left the choice to its runtime, which
// did not say, as "default".
func modelName(call domain.UsageRecord) string {
	if model := call.ModelName(); model != "" {
		return model
	}
	return "default"
}
