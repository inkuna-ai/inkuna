package domain

import (
	"testing"
	"time"
)

func TestCallTotalsKeepMixedSourcesApart(t *testing.T) {
	start := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	calls := []UsageRecord{
		// A reported cost keeps the table's estimate beside it; the reported
		// figure is the one that counts. Signed in to a subscription, it is
		// what the call would cost through the API.
		{Usage: Usage{InputTokens: 10, OutputTokens: 1}, Cost: Cost{Cents: 40, Source: CostReported, EstimatedCents: 38}, AuthMode: AuthSubscription, StartedAt: start, FinishedAt: start.Add(2 * time.Second)},
		{Usage: Usage{InputTokens: 3}, Cost: Cost{Cents: 6, Source: CostReported, EstimatedCents: 6}, AuthMode: AuthAPIKey, StartedAt: start, FinishedAt: start},
		// Only a reported figure is the subscription's: an estimate is the
		// pricing table's.
		{Usage: Usage{InputTokens: 20}, Cost: Cost{Cents: 2.5, Source: CostEstimated, EstimatedCents: 2.5}, AuthMode: AuthSubscription, StartedAt: start, FinishedAt: start.Add(1500 * time.Millisecond)},
		{Usage: Usage{CacheReadTokens: 30}, Cost: Cost{Source: CostUnknown}, StartedAt: start, FinishedAt: start.Add(time.Second)},
		// A call stored before costs had sources is unknown too, and one
		// whose clock went backwards took no time.
		{Usage: Usage{OutputTokens: 4}, StartedAt: start.Add(time.Second), FinishedAt: start},
	}
	var totals CallTotals
	for _, call := range calls {
		totals = totals.Add(call)
	}
	want := CallTotals{
		Usage: Usage{InputTokens: 33, CacheReadTokens: 30, OutputTokens: 5},
		Cost:  CostTotal{ReportedCents: 46, EstimatedCents: 2.5, UnknownCalls: 2},
		// Reported where a call has it, otherwise estimated; the unknown calls
		// add nothing and are counted instead.
		BestCents: 48.5, SubscriptionCents: 40, Calls: 5, DurationMs: 4500,
	}
	if totals != want {
		t.Fatalf("totals = %+v, want %+v", totals, want)
	}
	if only := (CallTotals{}).Add(calls[3]); only.BestCents != 0 || only.Cost.UnknownCalls != 1 || only.Calls != 1 {
		t.Fatalf("a call nothing priced is counted, not valued: %+v", only)
	}
}

func TestModelNameIsTheOneThatPricedTheCall(t *testing.T) {
	for _, test := range []struct{ asked, used, want string }{
		{"haiku", "claude-haiku-5-5", "claude-haiku-5-5"},
		{"gpt-5.5", "", "gpt-5.5"},
		{"", "", ""},
	} {
		if got := (UsageRecord{Model: test.asked, ModelUsed: test.used}).ModelName(); got != test.want {
			t.Fatalf("model asked %q, used %q = %q, want %q", test.asked, test.used, got, test.want)
		}
	}
}
