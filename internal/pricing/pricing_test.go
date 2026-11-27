package pricing

import (
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

func amount(value float64) *float64 { return &value }

func near(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

func TestBuiltInPricesCiteTheirSourceAndDate(t *testing.T) {
	table := Default()
	if table.Version != 1 || len(table.Models) == 0 {
		t.Fatalf("built-in table = version %d with %d models", table.Version, len(table.Models))
	}
	for model, price := range table.Models {
		if model != strings.ToLower(model) {
			t.Errorf("%s: ids are matched in lower case", model)
		}
		if parsed, err := url.Parse(price.Source); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			t.Errorf("%s: source %q is not the page the price was read from", model, price.Source)
		}
		if _, err := time.Parse(time.DateOnly, price.AsOf); err != nil {
			t.Errorf("%s: asOf %q is not a date", model, price.AsOf)
		}
		if price.Provider == "" || price.Input <= 0 || price.Output <= 0 || price.CacheRead <= 0 || price.CacheWrite < price.Input || price.LongCacheWrite < price.CacheWrite {
			t.Errorf("%s: implausible rates %+v", model, price)
		}
	}
}

func TestBuiltInTableReproducesClaudeCodesReportedCost(t *testing.T) {
	// Real calls: the tokens of each result event and the total_cost_usd
	// Claude Code reported beside them, all written to the one-hour cache.
	for _, call := range []struct {
		model   string
		usage   domain.Usage
		dollars float64
	}{
		{"claude-fable-5-1", domain.Usage{InputTokens: 34, CacheWriteTokens: 15160, LongCacheWriteTokens: 15160, CacheReadTokens: 14470, OutputTokens: 1077}, 0.3610075},
		{"claude-opus-5-5", domain.Usage{InputTokens: 8, CacheWriteTokens: 9436, LongCacheWriteTokens: 9436, CacheReadTokens: 42736, OutputTokens: 3237}, 0.1488072},
		{"claude-sonnet-5-5", domain.Usage{InputTokens: 6, CacheWriteTokens: 15660, LongCacheWriteTokens: 15660, CacheReadTokens: 45968, OutputTokens: 1099}, 0.0782388},
	} {
		price, ok := Default().Price(call.model)
		if !ok {
			t.Fatalf("%s has no price", call.model)
		}
		if got := price.Cents(call.usage); math.Abs(got-call.dollars*100) > 0.001 {
			t.Errorf("%s estimate = %.5f cents, Claude Code reported %.5f", call.model, got, call.dollars*100)
		}
	}
}

func TestPriceSplitsCacheWritesByLifetime(t *testing.T) {
	price := Price{Input: 1, CacheWrite: 2, LongCacheWrite: 4, CacheRead: 0.5, Output: 8}
	usage := domain.Usage{InputTokens: 1_000_000, CacheWriteTokens: 3_000_000, LongCacheWriteTokens: 1_000_000, CacheReadTokens: 2_000_000, OutputTokens: 1_000_000}
	// $1 input + 2M x $2 + 1M x $4 + 2M x $0.5 + $8 output = $18.
	if got := price.Cents(usage); !near(got, 1800) {
		t.Fatalf("cents = %v, want 1800", got)
	}
}

func TestCostOfAKnownModelIsEstimated(t *testing.T) {
	usage := domain.Usage{InputTokens: 9000, CacheReadTokens: 1_000_000, OutputTokens: 10_000}
	// gpt-5.6-terra: 9000 x $2 + 1M x $0.20 + 10k x $12 per million.
	cost := Default().Cost(domain.RuntimeCodex, "gpt-5.6-terra", usage)
	if cost.Source != domain.CostEstimated || !near(cost.Cents, 33.8) || !near(cost.EstimatedCents, 33.8) || cost.PricingVersion != 1 {
		t.Fatalf("codex cost = %+v", cost)
	}
	// A dated snapshot is priced like its model, and ids ignore case.
	if cost := Default().Cost("", "Claude-Haiku-4-5-20251001", domain.Usage{OutputTokens: 1_000_000}); cost.Source != domain.CostEstimated || !near(cost.Cents, 500) {
		t.Fatalf("dated snapshot cost = %+v", cost)
	}
}

func TestCostOfAnUnknownModelIsUnknown(t *testing.T) {
	usage := domain.Usage{InputTokens: 100, OutputTokens: 100}
	for _, model := range []string{"", "house-model"} {
		if cost := Default().Cost("", model, usage); cost != (domain.Cost{Source: domain.CostUnknown}) {
			t.Errorf("model %q cost = %+v, want unknown and unvalued", model, cost)
		}
	}
}

func TestCostOfACallThatMeasuredNothingIsUnknown(t *testing.T) {
	// Codex cut off before its totals, or a plain-text template: no tokens
	// and no cost, which is not the same as free.
	for _, kind := range []domain.Runtime{domain.RuntimeCodex, ""} {
		if cost := Default().Cost(kind, "gpt-5.6-luna", domain.Usage{}); cost != (domain.Cost{Source: domain.CostUnknown}) {
			t.Errorf("%q call that measured nothing = %+v", kind, cost)
		}
	}
}

func TestReportedCostWinsAndKeepsTheEstimate(t *testing.T) {
	usage := domain.Usage{OutputTokens: 1_000_000, ReportedCents: 2600}
	cost := Default().Cost(domain.RuntimeClaude, "claude-opus-5-5", usage)
	if cost.Source != domain.CostReported || cost.Cents != 2600 || !near(cost.EstimatedCents, 2000) || cost.PricingVersion != 1 {
		t.Fatalf("claude cost = %+v", cost)
	}
	// Without a price the reported cost still stands, with no estimate.
	if cost := Default().Cost(domain.RuntimeClaude, "", usage); cost != (domain.Cost{Cents: 2600, Source: domain.CostReported}) {
		t.Fatalf("unpriced reported cost = %+v", cost)
	}
	// A call cut off before Claude Code reported its cost is estimated.
	if cost := Default().Cost(domain.RuntimeClaude, "claude-opus-5-5", domain.Usage{OutputTokens: 1_000_000}); cost.Source != domain.CostEstimated || !near(cost.Cents, 2000) {
		t.Fatalf("cut-off claude cost = %+v", cost)
	}
}

func TestCopilotIsPricedByPremiumRequestsOnly(t *testing.T) {
	usage := domain.Usage{InputTokens: 4487, OutputTokens: 67, PremiumRequests: 0.33, Turns: 1}
	// Without a premium request price the cost is unknown, even for a model
	// whose tokens the table could price.
	if cost := Default().Cost(domain.RuntimeCopilot, "gpt-5-mini", usage); cost != (domain.Cost{Source: domain.CostUnknown}) {
		t.Fatalf("copilot cost without a premium price = %+v", cost)
	}
	table := Default().With(Overrides{PremiumRequest: amount(0.04)})
	if cost := table.Cost(domain.RuntimeCopilot, "claude-haiku-4.5", usage); cost.Source != domain.CostEstimated || !near(cost.Cents, 1.32) || !near(cost.EstimatedCents, 1.32) {
		t.Fatalf("copilot cost = %+v", cost)
	}
	// A call cut off before Copilot's result reports no premium requests,
	// so it is unknown, not free.
	if cost := table.Cost(domain.RuntimeCopilot, "claude-haiku-4.5", domain.Usage{OutputTokens: 52, Turns: 1}); cost.Source != domain.CostUnknown {
		t.Fatalf("cut-off copilot cost = %+v", cost)
	}
}

func TestConfiguredPriceWins(t *testing.T) {
	table := Default().With(Overrides{Models: map[string]Rates{
		"gpt-5.6-terra": {Input: amount(1), Output: amount(10), CacheRead: amount(0.1)},
		"house-model":   {Input: amount(0), Output: amount(0)},
	}})
	if table.Version != 1 {
		t.Fatalf("version = %d: configured prices keep the built-in version", table.Version)
	}
	price, _ := table.Price("gpt-5.6-terra")
	if price != (Price{Input: 1, CacheWrite: 1, LongCacheWrite: 1, CacheRead: 0.1, Output: 10, Source: SourceConfig}) {
		t.Fatalf("configured price = %+v", price)
	}
	if cost := table.Cost(domain.RuntimeCodex, "gpt-5.6-terra", domain.Usage{InputTokens: 1_000_000}); !near(cost.Cents, 100) || cost.Source != domain.CostEstimated {
		t.Fatalf("configured cost = %+v", cost)
	}
	// A configured free model is estimated at zero, not unknown.
	if cost := table.Cost("", "house-model", domain.Usage{InputTokens: 50}); cost.Source != domain.CostEstimated || cost.Cents != 0 {
		t.Fatalf("free model cost = %+v", cost)
	}
	// The built-in table is unchanged.
	if price, _ := Default().Price("gpt-5.6-terra"); price.Source == SourceConfig {
		t.Fatal("applying overrides changed the built-in table")
	}
}

func TestRatesFallBack(t *testing.T) {
	price := Rates{Input: amount(3), CacheWrite: amount(3.75), Output: amount(15)}.Price()
	if price.LongCacheWrite != 3.75 || price.CacheRead != 3 {
		t.Fatalf("fallbacks = %+v", price)
	}
}

func TestProblems(t *testing.T) {
	overrides := Overrides{
		Models: map[string]Rates{
			"good":   {Input: amount(1), Output: amount(2)},
			"Shouty": {Input: amount(1), Output: amount(2)},
			"half":   {Input: amount(1)},
			"minus":  {Input: amount(1), Output: amount(2), CacheRead: amount(-0.1)},
			"inf":    {Input: amount(math.Inf(1)), Output: amount(2)},
			" ":      {Input: amount(1), Output: amount(2)},
		},
		PremiumRequest: amount(-1),
	}
	got := map[string]bool{}
	for _, problem := range overrides.Problems() {
		got[problem.Field] = true
	}
	for _, field := range []string{"pricing.models", "pricing.models.Shouty", "pricing.models.half", "pricing.models.minus", "pricing.models.inf", "pricing.premiumRequest"} {
		if !got[field] {
			t.Errorf("no problem reported for %s; got %v", field, got)
		}
	}
	if got["pricing.models.good"] || len(got) != 6 {
		t.Errorf("problems = %v", got)
	}
	if problems := (Overrides{PremiumRequest: amount(0)}).Problems(); len(problems) != 0 {
		t.Errorf("a zero premium price is valid: %v", problems)
	}
}
