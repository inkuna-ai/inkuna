package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// exportFixture is costFixture with run-1 approved by grace and its
// installation's id.
func exportFixture(t *testing.T) string {
	t.Helper()
	costFixture(t)
	store, err := storage.Open(filepath.Join(os.Getenv("INKUNA_DATA_DIR"), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.DB().Exec(`UPDATE executions SET approved_by='grace' WHERE id='run-1'`); err != nil {
		t.Fatal(err)
	}
	installation, err := store.Installation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return installation
}

func exportCSV(t *testing.T, args ...string) []map[string]string {
	t.Helper()
	var output bytes.Buffer
	if err := run(append([]string{"usage", "export"}, args...), nil, &output); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&output).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows[0], usageExportHeader) {
		t.Fatalf("header = %v", rows[0])
	}
	records := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		record := make(map[string]string, len(row))
		for index, cell := range row {
			record[rows[0][index]] = cell
		}
		records = append(records, record)
	}
	return records
}

func TestUsageExportCSVKeepsEachCostInItsSourcesColumn(t *testing.T) {
	installation := exportFixture(t)
	rows := exportCSV(t)
	if len(rows) != 6 {
		t.Fatalf("exported %d calls, want the fixture's 6", len(rows))
	}
	calls := make(map[string]map[string]string, len(rows))
	for _, row := range rows {
		calls[row["call"]] = row
	}
	pick := func(row map[string]string, columns ...string) []string {
		cells := make([]string, 0, len(columns))
		for _, column := range columns {
			cells = append(cells, row[column])
		}
		return cells
	}
	costs := []string{"cost_source", "reported_cents", "estimated_cents", "pricing_table_cents", "pricing_version"}
	for call, want := range map[string][]string{
		"run-1-research-researcher":   {"reported", "4", "", "4", "1"},
		"run-1-implement-implementer": {"estimated", "", "2.5", "2.5", "1"},
		// Nothing priced it, so no figure stands in for one.
		"run-1-implement-reviewer": {"unknown", "", "", "", ""},
	} {
		if got := pick(calls[call], costs...); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s costs %v, want %v", call, got, want)
		}
	}
	research := calls["run-1-research-researcher"]
	want := []string{"grace", "project", "task-1", "run-1", "research", "1", "worker", "researcher", "claude", "haiku", "subscription", "completed",
		"2026-10-19T09:06:00Z", "2026-10-19T09:07:00Z", "10", "0", "0", "1000", "100", "0"}
	if got := pick(research, "principal", "project", "task", "run", "step", "turn", "kind", "agent", "runtime", "model", "auth_mode", "outcome",
		"started_at", "finished_at", "input_tokens", "cache_write_tokens", "long_cache_write_tokens", "cache_read_tokens", "output_tokens", "premium_requests"); !reflect.DeepEqual(got, want) {
		t.Fatalf("research call = %v, want %v", got, want)
	}
	if research["installation"] != installation || !strings.HasPrefix(research["id"], installation+"/") {
		t.Fatalf("research call is %s from %s, want an id under %s", research["id"], research["installation"], installation)
	}
	// A planning call has no run and is the task creator's, unknown here.
	// Its call id starts with a minus, which a spreadsheet would run.
	if staffing := calls["'-staffing-orchestrator"]; staffing["run"] != "" || staffing["kind"] != "orchestrator" || staffing["principal"] != "" {
		t.Fatalf("planning call = %v", staffing)
	}
}

func TestUsageExportJSONLWritesUsageRecords(t *testing.T) {
	installation := exportFixture(t)
	var output bytes.Buffer
	if err := run([]string{"usage", "export", "--format", "jsonl"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("wrote %d lines, want the fixture's 6:\n%s", len(lines), output.String())
	}
	records := make(map[string]extension.UsageRecord, len(lines))
	for _, line := range lines {
		// Strictly, so the export writes a UsageRecord and nothing else.
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.DisallowUnknownFields()
		var record extension.UsageRecord
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode %s: %v", line, err)
		}
		if record.Installation != installation || !strings.HasPrefix(record.ID, installation+"/") {
			t.Fatalf("record %s is from %s", record.ID, record.Installation)
		}
		records[record.Call] = record
	}
	if cost := records["run-1-implement-reviewer"].Cost; cost != (extension.Cost{Source: "unknown"}) {
		t.Fatalf("the unpriced call costs %+v", cost)
	}
	if cost := records["run-1-research-researcher"].Cost; cost != (extension.Cost{Source: "reported", Cents: 4, EstimatedCents: 4, PricingVersion: 1}) {
		t.Fatalf("the reported call costs %+v", cost)
	}
	research := records["run-1-research-researcher"]
	if research.Principal != "grace" || research.Run != "run-1" || research.Tokens != (extension.Tokens{Input: 10, CacheRead: 1000, Output: 100}) || research.AuthMode != "subscription" {
		t.Fatalf("research call = %+v", research)
	}
}

func TestUsageExportSinceSkipsOlderCalls(t *testing.T) {
	exportFixture(t)
	rows := exportCSV(t, "--since", "2026-10-19T00:00:00Z")
	if len(rows) != 5 {
		t.Fatalf("exported %d calls since 2026-10-19, want 5", len(rows))
	}
	for _, row := range rows {
		if row["task"] != "task-1" {
			t.Fatalf("exported %s's call %s, which ended before 2026-10-19", row["task"], row["call"])
		}
	}
	var output bytes.Buffer
	if err := run([]string{"usage", "export", "--since", "2026-10-20T00:00:00Z", "--format", "jsonl"}, nil, &output); err != nil || output.Len() != 0 {
		t.Fatalf("an export after every call wrote %q (%v)", output.String(), err)
	}
}

func TestUsageExportRefusesWhatItCannotWrite(t *testing.T) {
	exportFixture(t)
	for _, args := range [][]string{{"--format", "xlsx"}, {"run-1"}, {"--since", "yesterday"}} {
		if err := run(append([]string{"usage", "export"}, args...), nil, &bytes.Buffer{}); err == nil {
			t.Fatalf("usage export %v succeeded", args)
		}
	}
}
