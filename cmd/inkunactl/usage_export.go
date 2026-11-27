package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	inkuna "github.com/inkuna-ai/inkuna/internal/app"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

const usageExportUsage = "usage: inkunactl usage export [--since DATE] [--format csv|jsonl]"

// runUsageExport writes every stored model call as the usage sinks receive
// it, so anyone can audit their own costs without a sink. It reads only
// what history deletion left.
func runUsageExport(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("usage export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	since := flags.String("since", "", "export only calls that ended on or after DATE (YYYY-MM-DD, local time, or an RFC 3339 time)")
	format := flags.String("format", "csv", "csv, one row per call, or jsonl, one UsageRecord per line")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, usageExportUsage)
	}
	if flags.NArg() > 0 {
		return errors.New(usageExportUsage)
	}
	if *format != "csv" && *format != "jsonl" {
		return fmt.Errorf("--format %q must be csv or jsonl", *format)
	}
	from, err := sinceTime(*since)
	if err != nil {
		return err
	}
	store, _, err := openDatabase()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	installation, err := store.Installation(ctx)
	if err != nil {
		return err
	}
	calls, err := store.UsageSince(ctx, from)
	if err != nil {
		return fmt.Errorf("load usage: %w", err)
	}
	records := make([]extension.UsageRecord, 0, len(calls))
	for _, call := range calls {
		records = append(records, inkuna.UsageRecordOf(installation, call))
	}
	if *format == "jsonl" {
		return writeUsageJSONL(stdout, records)
	}
	return writeUsageCSV(stdout, records)
}

func writeUsageJSONL(stdout io.Writer, records []extension.UsageRecord) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("write usage record %s: %w", record.ID, err)
		}
	}
	return nil
}

var usageExportHeader = []string{
	"id", "installation", "principal", "project", "task", "run", "step", "call", "turn", "kind", "agent", "runtime", "model", "model_used", "auth_mode", "outcome",
	"started_at", "finished_at", "input_tokens", "cache_write_tokens", "long_cache_write_tokens", "cache_read_tokens", "output_tokens", "premium_requests",
	"cost_source", "reported_cents", "estimated_cents", "pricing_table_cents", "pricing_version",
}

// writeUsageCSV writes a row per call. A call's one cost figure goes in
// reported_cents or estimated_cents by its source, so each column sums one
// kind, as `inkunactl cost` keeps them, and a call nothing priced leaves
// both blank rather than zero. pricing_table_cents is the pricing table's
// figure, which a reported cost carries beside it; it and pricing_version
// are blank when the table did not price the call.
func writeUsageCSV(stdout io.Writer, records []extension.UsageRecord) error {
	writer := csv.NewWriter(stdout)
	_ = writer.Write(usageExportHeader)
	cents := func(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
	for _, record := range records {
		text := []string{record.ID, record.Installation, record.Principal, record.Project, record.Task, record.Run, record.Step, record.Call}
		text = append(text, strconv.Itoa(record.Turn), record.Kind, record.Agent, record.Runtime, record.Model, record.ModelUsed, record.AuthMode, record.Outcome)
		for index := range text {
			text[index] = spreadsheetText(text[index])
		}
		tokens := record.Tokens
		row := append(text, timeCell(record.StartedAt), timeCell(record.FinishedAt),
			strconv.Itoa(tokens.Input), strconv.Itoa(tokens.CacheWrite), strconv.Itoa(tokens.LongCacheWrite), strconv.Itoa(tokens.CacheRead), strconv.Itoa(tokens.Output),
			strconv.FormatFloat(record.Premium, 'f', -1, 64), spreadsheetText(record.Cost.Source))
		var reported, estimated, table, version string
		switch record.Cost.Source {
		case domain.CostReported:
			reported = cents(record.Cost.Cents)
		case domain.CostEstimated:
			estimated = cents(record.Cost.Cents)
		}
		if record.Cost.PricingVersion != 0 {
			table, version = cents(record.Cost.EstimatedCents), strconv.Itoa(record.Cost.PricingVersion)
		}
		_ = writer.Write(append(row, reported, estimated, table, version))
	}
	writer.Flush()
	return writer.Error()
}

// timeCell is a time in UTC to the nanosecond, as stored; zero is blank.
func timeCell(moment time.Time) string {
	if moment.IsZero() {
		return ""
	}
	return moment.UTC().Format(time.RFC3339Nano)
}
