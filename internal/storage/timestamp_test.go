package storage

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// Timestamps sort as text in time order, including the case RFC3339Nano got
// wrong: a fraction that ends in zeros next to one a nanosecond later, the
// step clock.Now takes when Windows' clock has not moved.
func TestTimestampsSortInTimeOrder(t *testing.T) {
	base := time.Date(2026, 11, 17, 16, 57, 10, 487110000, time.UTC)
	times := []time.Time{
		base.Add(time.Nanosecond),
		base,
		base.Truncate(time.Second),
		base.Add(-time.Nanosecond),
		base.Add(500 * time.Millisecond).In(time.FixedZone("CET", 3600)),
		base.Add(time.Second).Truncate(time.Second),
	}
	stored := make([]string, len(times))
	for index, value := range times {
		stored[index] = timestamp(value)
		if len(stored[index]) != len(timestampLayout)-len("Z07:00")+len("Z") {
			t.Fatalf("timestamp(%v) = %q, not fixed width", value, stored[index])
		}
		parsed, err := time.Parse(time.RFC3339Nano, stored[index])
		if err != nil || !parsed.Equal(value) {
			t.Fatalf("timestamp(%v) = %q reads back as %v, %v", value, stored[index], parsed, err)
		}
	}
	byText := slices.Clone(stored)
	sort.Strings(byText)
	byTime := slices.Clone(times)
	slices.SortFunc(byTime, func(left, right time.Time) int { return left.Compare(right) })
	for index := range byTime {
		if byText[index] != timestamp(byTime[index]) {
			t.Fatalf("as text\n%v\nin time order\n%v", byText, byTime)
		}
	}
}

// Every timestamp storage writes goes through timestamp, so no query ever
// compares two widths again.
func TestStorageFormatsTimestampsOnlyThroughTimestamp(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	format := regexp.MustCompile(`\.Format\(time\.RFC3339(Nano)?\)`)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(data), "\n") {
			if format.MatchString(line) {
				t.Errorf("%s:%d formats a timestamp itself; use timestamp():\n%s", file, number+1, strings.TrimSpace(line))
			}
		}
	}
}

// The migration rewrites timestamps stored before it, so step runs that
// started a nanosecond apart list in the order they started.
func TestFixedWidthMigrationRewritesStoredTimestamps(t *testing.T) {
	store, spec := roundsStore(t)
	ctx := context.Background()
	for _, run := range []struct{ id, step string }{{"run-1-implement", "implement"}, {"run-1-approval", "approval"}} {
		if err := store.StartRun(ctx, run.id, "run-1", spec.ID, run.step, 1, domain.RuntimeClaude, ""); err != nil {
			t.Fatal(err)
		}
	}
	// As RFC3339Nano stored them: implement started first.
	legacy := `
UPDATE runs SET started_at = '2026-11-17T16:57:10.48711Z' WHERE id = 'run-1-implement';
UPDATE runs SET started_at = '2026-11-17T16:57:10.487110001Z' WHERE id = 'run-1-approval';
UPDATE executions SET started_at = '2026-11-17T17:57:10+01:00' WHERE id = 'run-1';
UPDATE tasks SET created_at = '' WHERE id = 'task-1';
DELETE FROM schema_migrations WHERE version = '033_fixed_width_timestamps.sql';`
	if _, err := store.DB().ExecContext(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	order := func() []string {
		t.Helper()
		runs, err := store.StepRuns(ctx, "run-1")
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, run := range runs {
			ids = append(ids, run.ID)
		}
		return ids
	}
	if got := order(); got[0] != "run-1-approval" {
		t.Fatalf("before the migration the runs list %v; the legacy values must sort wrong, or this test checks nothing", got)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := order(), []string{"run-1-implement", "run-1-approval"}; !slices.Equal(got, want) {
		t.Fatalf("after the migration the runs list %v, want %v", got, want)
	}
	for _, check := range []struct{ query, want string }{
		{`SELECT started_at FROM runs WHERE id = 'run-1-implement'`, "2026-11-17T16:57:10.487110000Z"},
		{`SELECT started_at FROM runs WHERE id = 'run-1-approval'`, "2026-11-17T16:57:10.487110001Z"},
		{`SELECT started_at FROM executions WHERE id = 'run-1'`, "2026-11-17T16:57:10.000000000Z"},
		{`SELECT created_at FROM tasks WHERE id = 'task-1'`, ""},
	} {
		var got string
		if err := store.DB().QueryRowContext(ctx, check.query).Scan(&got); err != nil || got != check.want {
			t.Errorf("%s = %q, %v; want %q", check.query, got, err, check.want)
		}
	}
}
