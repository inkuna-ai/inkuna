// Package pricing prices model calls: a versioned table of providers'
// published per-token prices, the prices inkuna.yaml adds or changes, and
// the rule that decides each call's cost and where that figure comes from.
package pricing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

//go:embed prices.v1.json
var builtInJSON []byte

// SourceConfig is the Source of a price that inkuna.yaml gives.
const SourceConfig = "inkuna.yaml"

// Price is what a model costs, in US dollars per million tokens. CacheWrite
// is the five-minute cache and LongCacheWrite the one-hour cache. Source is
// where the price was published, or SourceConfig, and AsOf the day it was
// read there.
type Price struct {
	Provider       string  `json:"provider,omitempty"`
	Input          float64 `json:"input"`
	CacheWrite     float64 `json:"cacheWrite"`
	LongCacheWrite float64 `json:"longCacheWrite"`
	CacheRead      float64 `json:"cacheRead"`
	Output         float64 `json:"output"`
	Source         string  `json:"source"`
	AsOf           string  `json:"asOf,omitempty"`
	Note           string  `json:"note,omitempty"`
}

// Cents prices what a call used at these rates.
func (price Price) Cents(usage domain.Usage) float64 {
	long := min(max(usage.LongCacheWriteTokens, 0), usage.CacheWriteTokens)
	dollarsPerMillion := float64(usage.InputTokens)*price.Input +
		float64(usage.CacheWriteTokens-long)*price.CacheWrite + float64(long)*price.LongCacheWrite +
		float64(usage.CacheReadTokens)*price.CacheRead + float64(usage.OutputTokens)*price.Output
	return dollarsPerMillion / 10_000
}

// Table is the prices calls are estimated with, by model. Version names the
// built-in table; prices from inkuna.yaml do not change it. PremiumRequest is
// the price of one Copilot premium request in US dollars, which only
// inkuna.yaml sets.
type Table struct {
	Version        int              `json:"version"`
	Models         map[string]Price `json:"models"`
	PremiumRequest *float64         `json:"premiumRequest,omitempty"`
}

// Default is the built-in table, seeded from the providers' published
// prices, each with its source and the day it was read.
func Default() Table {
	var table Table
	if err := json.Unmarshal(builtInJSON, &table); err != nil {
		panic(fmt.Sprintf("decode embedded pricing table: %v", err))
	}
	return table
}

// dated is the date stamp some model ids end in, such as
// claude-haiku-4-5-20251001, which names a snapshot priced like its model.
var dated = regexp.MustCompile(`-\d{8}$`)

// Price finds a model's price. Ids are matched without regard to case, and a
// dated snapshot takes the price of its model unless it has its own.
func (table Table) Price(model string) (Price, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return Price{}, false
	}
	if price, ok := table.Models[model]; ok {
		return price, true
	}
	price, ok := table.Models[dated.ReplaceAllString(model, "")]
	return price, ok
}

// With returns the table with inkuna.yaml's prices applied: each model it
// names replaces the built-in price or adds one, and the premium request
// price is set when it gives one.
func (table Table) With(overrides Overrides) Table {
	merged := Table{Version: table.Version, Models: make(map[string]Price, len(table.Models)+len(overrides.Models)), PremiumRequest: table.PremiumRequest}
	for model, price := range table.Models {
		merged.Models[model] = price
	}
	for model, rates := range overrides.Models {
		merged.Models[strings.ToLower(strings.TrimSpace(model))] = rates.Price()
	}
	if overrides.PremiumRequest != nil {
		value := *overrides.PremiumRequest
		merged.PremiumRequest = &value
	}
	return merged
}

// Cost decides what one call cost and where the figure comes from. kind is
// the built-in CLI whose stream the runtime writes, empty for plain text, and
// model the one the call used. A cost the runtime reported wins, with the
// estimate kept beside it. Copilot bills premium requests, not tokens, so its
// calls are priced by the premium request price alone, and stay unknown
// without one. Any other call is estimated from its tokens when its model
// has a price, and is otherwise unknown. A call that measured nothing, such
// as one cut off before Codex's totals or a plain-text template's, is
// unknown, never free.
func (table Table) Cost(kind domain.Runtime, model string, usage domain.Usage) domain.Cost {
	cost := domain.Cost{Source: domain.CostUnknown}
	if kind == domain.RuntimeCopilot {
		// A call cut off before Copilot's result reports no premium
		// requests, and one on a model that takes none cannot be told from
		// it, so neither is priced.
		if table.PremiumRequest != nil && usage.PremiumRequests > 0 {
			cents := usage.PremiumRequests * *table.PremiumRequest * 100
			cost = domain.Cost{Cents: cents, Source: domain.CostEstimated, EstimatedCents: cents, PricingVersion: table.Version}
		}
		return cost
	}
	if usage.ReportedCents == 0 && usage.TotalInputTokens()+usage.OutputTokens == 0 {
		return cost
	}
	if price, ok := table.Price(model); ok {
		cost.EstimatedCents, cost.PricingVersion = price.Cents(usage), table.Version
		cost.Cents, cost.Source = cost.EstimatedCents, domain.CostEstimated
	}
	if usage.ReportedCents > 0 {
		cost.Cents, cost.Source = usage.ReportedCents, domain.CostReported
	}
	return cost
}

// Overrides is the pricing block of inkuna.yaml: prices for models the
// built-in table lacks or that cost this user something else, by model id,
// and the price of one Copilot premium request in US dollars.
type Overrides struct {
	Models         map[string]Rates `yaml:"models,omitempty" json:"models,omitempty"`
	PremiumRequest *float64         `yaml:"premiumRequest,omitempty" json:"premiumRequest,omitempty"`
}

// Rates are a model's prices in US dollars per million tokens. Input and
// output are required. A cache rate left out costs what it falls back on: a
// cache write what input costs, a one-hour cache write what a five-minute
// one costs, and a cache read what input costs, since without a rate there
// is no discount to assume.
type Rates struct {
	Input          *float64 `yaml:"input,omitempty" json:"input,omitempty"`
	CacheWrite     *float64 `yaml:"cacheWrite,omitempty" json:"cacheWrite,omitempty"`
	LongCacheWrite *float64 `yaml:"longCacheWrite,omitempty" json:"longCacheWrite,omitempty"`
	CacheRead      *float64 `yaml:"cacheRead,omitempty" json:"cacheRead,omitempty"`
	Output         *float64 `yaml:"output,omitempty" json:"output,omitempty"`
}

// Price resolves the rates, filling each one left out from its fallback.
func (rates Rates) Price() Price {
	value := func(rate *float64, fallback float64) float64 {
		if rate == nil {
			return fallback
		}
		return *rate
	}
	input := value(rates.Input, 0)
	cacheWrite := value(rates.CacheWrite, input)
	return Price{
		Input: input, CacheWrite: cacheWrite, LongCacheWrite: value(rates.LongCacheWrite, cacheWrite),
		CacheRead: value(rates.CacheRead, input), Output: value(rates.Output, 0), Source: SourceConfig,
	}
}

// Problem is one thing wrong with the pricing block. Field is the dotted
// path of the setting it is about.
type Problem struct {
	Field   string
	Message string
}

// Problems lists everything wrong with the pricing block, models in name
// order.
func (overrides Overrides) Problems() []Problem {
	problems := make([]Problem, 0)
	names := make([]string, 0, len(overrides.Models))
	for name := range overrides.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field := "pricing.models." + name
		if strings.TrimSpace(name) == "" {
			problems = append(problems, Problem{Field: "pricing.models", Message: "every priced model needs its id"})
			continue
		}
		if name != strings.ToLower(strings.TrimSpace(name)) {
			problems = append(problems, Problem{Field: field, Message: fmt.Sprintf("write the model %q in lower case without surrounding spaces, as runtimes report it", name)})
		}
		rates := overrides.Models[name]
		if rates.Input == nil || rates.Output == nil {
			problems = append(problems, Problem{Field: field, Message: fmt.Sprintf("the price of %s needs input and output rates", name)})
		}
		for _, rate := range []*float64{rates.Input, rates.CacheWrite, rates.LongCacheWrite, rates.CacheRead, rates.Output} {
			if rate != nil && !validAmount(*rate) {
				problems = append(problems, Problem{Field: field, Message: fmt.Sprintf("the rates of %s must be dollars per million tokens, zero or more", name)})
				break
			}
		}
	}
	if overrides.PremiumRequest != nil && !validAmount(*overrides.PremiumRequest) {
		problems = append(problems, Problem{Field: "pricing.premiumRequest", Message: "the premium request price must be dollars, zero or more"})
	}
	return problems
}

func validAmount(value float64) bool {
	return value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}
