package runtime

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/pricing"
)

func decode(t *testing.T, raw string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestExtractOutputClaude(t *testing.T) {
	payload := decode(t, `{"type":"result","subtype":"success","result":"done","structured_output":{"version":1,"status":"completed","summary":"ok"},"usage":{"input_tokens":100,"cache_creation_input_tokens":15,"cache_read_input_tokens":5,"output_tokens":30},"total_cost_usd":0.0625,"num_turns":4}`)
	output, ok := ExtractOutput(domain.RuntimeClaude, payload)
	if !ok || string(output) != `{"status":"completed","summary":"ok","version":1}` {
		t.Fatalf("unexpected output %s %v", output, ok)
	}
	usage, ok := ExtractUsage(domain.RuntimeClaude, payload)
	want := domain.Usage{InputTokens: 100, CacheWriteTokens: 15, CacheReadTokens: 5, OutputTokens: 30, ReportedCents: 6.25, Turns: 4}
	if !ok || usage != want || usage.TotalInputTokens() != 120 {
		t.Fatalf("unexpected usage %+v", usage)
	}
	if _, ok := ExtractOutput(domain.RuntimeClaude, decode(t, `{"type":"assistant","message":{"content":[]}}`)); ok {
		t.Fatal("assistant events must not be treated as output")
	}
	fenced := decode(t, "{\"type\":\"result\",\"result\":\"Here you go:\\n```json\\n{\\\"status\\\":\\\"completed\\\",\\\"summary\\\":\\\"fenced\\\"}\\n```\"}")
	output, ok = ExtractOutput(domain.RuntimeClaude, fenced)
	if !ok || string(output) != `{"status":"completed","summary":"fenced"}` {
		t.Fatalf("fenced result was not parsed: %s %v", output, ok)
	}
}

func TestExtractOutputCodexAndCopilot(t *testing.T) {
	codex := decode(t, `{"type":"item.completed","item":{"id":"1","type":"agent_message","text":"{\"status\":\"completed\",\"summary\":\"codex\"}"}}`)
	output, ok := ExtractOutput(domain.RuntimeCodex, codex)
	if !ok || string(output) != `{"status":"completed","summary":"codex"}` {
		t.Fatalf("unexpected codex output %s %v", output, ok)
	}
	if _, ok := ExtractOutput(domain.RuntimeCodex, decode(t, `{"type":"item.completed","item":{"type":"reasoning","text":"{}"}}`)); ok {
		t.Fatal("reasoning items must be ignored")
	}
	// Codex counts cached tokens inside input_tokens.
	usage, ok := ExtractUsage(domain.RuntimeCodex, decode(t, `{"type":"turn.completed","usage":{"input_tokens":24763,"cached_input_tokens":24448,"output_tokens":122}}`))
	if want := (domain.Usage{InputTokens: 315, CacheReadTokens: 24448, OutputTokens: 122}); !ok || usage != want || usage.TotalInputTokens() != 24763 {
		t.Fatalf("unexpected codex usage %+v", usage)
	}
	if usage, ok := ExtractUsage(domain.RuntimeCodex, decode(t, `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}`)); !ok || usage != (domain.Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Fatalf("codex usage without a cache = %+v", usage)
	}
	copilot := decode(t, `{"type":"assistant.message","data":{"content":"{\"status\":\"completed\",\"summary\":\"copilot\"}","outputTokens":22}}`)
	output, ok = ExtractOutput(domain.RuntimeCopilot, copilot)
	if !ok || string(output) != `{"status":"completed","summary":"copilot"}` {
		t.Fatalf("unexpected copilot output %s %v", output, ok)
	}
	if _, ok := ExtractUsage(domain.RuntimeCopilot, copilot); ok {
		t.Fatal("a Copilot message is not the session's total")
	}
	if _, usage, ok := ExtractMessageUsage(domain.RuntimeCopilot, copilot); !ok || usage != (domain.Usage{OutputTokens: 22, Turns: 1}) {
		t.Fatalf("unexpected copilot message usage %+v %v", usage, ok)
	}
	if _, ok := ExtractOutput(domain.RuntimeCopilot, decode(t, `{"type":"assistant.message_delta","data":{"deltaContent":"{\"status\":"}}`)); ok {
		t.Fatal("partial deltas must not be treated as output")
	}
	if _, ok := ExtractOutput(domain.RuntimeCopilot, decode(t, `{"type":"assistant.message","data":{"content":"no json here"}}`)); ok {
		t.Fatal("prose must not parse as output")
	}
}

func TestExtractFailure(t *testing.T) {
	cases := []struct {
		kind    domain.Runtime
		payload string
		want    string
	}{
		{domain.RuntimeCodex, `{"type":"turn.failed","error":{"message":"usage limit"}}`, "usage limit"},
		{domain.RuntimeCodex, `{"type":"error","message":"boom"}`, "boom"},
		{domain.RuntimeClaude, `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"too many turns"}`, "too many turns"},
		{domain.RuntimeClaude, `{"type":"result","subtype":"error_during_execution"}`, "claude finished with error_during_execution"},
		{domain.RuntimeCopilot, `{"type":"error","data":{"message":"quota"}}`, "quota"},
	}
	for _, test := range cases {
		message, ok := ExtractFailure(test.kind, decode(t, test.payload))
		if !ok || message != test.want {
			t.Fatalf("%s %s: got %q %v", test.kind, test.payload, message, ok)
		}
	}
	for _, payload := range []string{`{"type":"result","subtype":"success"}`, `{"type":"item.completed","item":{"type":"agent_message","text":"{}"}}`, `{"type":"assistant.message","data":{"content":"x"}}`} {
		for _, kind := range []domain.Runtime{domain.RuntimeClaude, domain.RuntimeCodex, domain.RuntimeCopilot} {
			if _, ok := ExtractFailure(kind, decode(t, payload)); ok {
				t.Fatalf("%s must not treat %s as failure", kind, payload)
			}
		}
	}
}

func TestCopilotResultReportsPremiumRequestsNotTokens(t *testing.T) {
	// Copilot 1.0.79's last event on stdout: its usage is premium requests
	// and timings, with no tokens to replace the messages' output.
	result := decode(t, `{"exitCode":0,"sessionId":"8fd58baa","type":"result","usage":{"codeChanges":{"filesModified":[],"linesAdded":0,"linesRemoved":0},"premiumRequests":0.33,"sessionDurationMs":5932,"totalApiDurationMs":4909}}`)
	if _, ok := ExtractUsage(domain.RuntimeCopilot, result); ok {
		t.Fatal("Copilot's result has no token totals")
	}
	if premium, ok := ExtractPremiumRequests(domain.RuntimeCopilot, result); !ok || premium != 0.33 {
		t.Fatalf("premium requests = %v %v", premium, ok)
	}
	for _, payload := range []string{`{"type":"result","data":null}`, `{"type":"assistant.message","data":{"outputTokens":3}}`} {
		if _, ok := ExtractPremiumRequests(domain.RuntimeCopilot, decode(t, payload)); ok {
			t.Errorf("%s reports no premium requests", payload)
		}
	}
	if _, ok := ExtractPremiumRequests(domain.RuntimeClaude, result); ok {
		t.Error("only Copilot reports premium requests")
	}
}

// fixtureUsage replays a fixture event stream through the scanner Start
// uses, so the usage is what a run would report, accumulation included.
func fixtureUsage(t *testing.T, kind domain.Runtime, stream io.Reader) domain.Usage {
	t.Helper()
	return fixtureState(t, kind, stream).usage
}

// fixtureState is everything the scanner read from a fixture stream.
func fixtureState(t *testing.T, kind domain.Runtime, stream io.Reader) *runState {
	t.Helper()
	runtime := newPreset(kind, string(kind)).(*commandRuntime)
	events := make(chan domain.RunEvent)
	drained := make(chan struct{})
	go func() {
		for range events {
		}
		close(drained)
	}()
	done := make(chan error, 1)
	state := &runState{}
	runtime.scan(RunRequest{RunID: "fixture", Budget: domain.Budget{MaxOutputBytes: 1 << 20}}, stream, events, done, state)
	close(events)
	<-drained
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return state
}

// sameUsage compares usages, allowing for the rounding of costs in cents.
func sameUsage(got, want domain.Usage) bool {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	if !near(got.ReportedCents, want.ReportedCents) || !near(got.PremiumRequests, want.PremiumRequests) {
		return false
	}
	got.ReportedCents, got.PremiumRequests = want.ReportedCents, want.PremiumRequests
	return got == want
}

func TestUsageFromFixtureStreamsSplitsInput(t *testing.T) {
	for kind, want := range map[domain.Runtime]domain.Usage{
		// The result event's totals, not the per-message usage before it,
		// with the writes to the one-hour cache and the reported cost.
		domain.RuntimeClaude: {InputTokens: 8, CacheWriteTokens: 28325, LongCacheWriteTokens: 28325, CacheReadTokens: 27913, OutputTokens: 192, ReportedCents: 0.604093, Turns: 3},
		// Cached tokens come out of input_tokens; Codex reports no cost.
		domain.RuntimeCodex: {InputTokens: 315, CacheReadTokens: 24448, OutputTokens: 122},
		// The messages' output, with the premium requests from the result;
		// Copilot reports no input tokens on stdout.
		domain.RuntimeCopilot: {OutputTokens: 381, PremiumRequests: 0.33, Turns: 3},
	} {
		file, err := os.Open(filepath.Join("testdata", "usage", string(kind)+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		got := fixtureUsage(t, kind, file)
		_ = file.Close()
		if !sameUsage(got, want) {
			t.Errorf("%s usage = %+v, want %+v", kind, got, want)
		}
	}
	// Cut off before its result, Copilot's per-message output counts add up.
	messages := `{"type":"assistant.message","data":{"content":"a","outputTokens":40}}` + "\n" + `{"type":"assistant.message","data":{"content":"b","outputTokens":12}}` + "\n"
	if got := fixtureUsage(t, domain.RuntimeCopilot, strings.NewReader(messages)); got != (domain.Usage{OutputTokens: 52, Turns: 2}) {
		t.Fatalf("copilot usage without its result = %+v", got)
	}
}

func TestUsageOfACallCutOffBeforeItsTotalsIsItsMessages(t *testing.T) {
	// The recorded Claude stream ends before its result event, as a call
	// that timed out would: five messages, each with its own usage.
	file, err := os.Open(filepath.Join("testdata", "reads", "claude.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if got, want := fixtureUsage(t, domain.RuntimeClaude, file), (domain.Usage{InputTokens: 12, CacheWriteTokens: 28740, LongCacheWriteTokens: 28740, CacheReadTokens: 113484, OutputTokens: 76, Turns: 5}); got != want {
		t.Fatalf("usage of the cut-off stream = %+v, want %+v", got, want)
	}
	// Claude repeats a message's usage on each of its content blocks; the
	// last report counts once, and the result replaces the messages.
	text := `{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":3,"cache_creation_input_tokens":900,"output_tokens":5}}}`
	tool := `{"type":"assistant","message":{"id":"msg_1","content":[{"type":"tool_use","name":"Read"}],"usage":{"input_tokens":3,"cache_creation_input_tokens":900,"output_tokens":40}}}`
	next := `{"type":"assistant","message":{"id":"msg_2","content":[{"type":"text","text":"b"}],"usage":{"input_tokens":2,"cache_read_input_tokens":900,"output_tokens":7}}}`
	stream := strings.Join([]string{text, tool, next}, "\n") + "\n"
	if got, want := fixtureUsage(t, domain.RuntimeClaude, strings.NewReader(stream)), (domain.Usage{InputTokens: 5, CacheWriteTokens: 900, CacheReadTokens: 900, OutputTokens: 47, Turns: 2}); got != want {
		t.Fatalf("usage of repeated messages = %+v, want %+v", got, want)
	}
	result := `{"type":"result","subtype":"success","num_turns":2,"total_cost_usd":0.01,"usage":{"input_tokens":5,"cache_creation_input_tokens":900,"cache_read_input_tokens":900,"output_tokens":50}}`
	if got, want := fixtureUsage(t, domain.RuntimeClaude, strings.NewReader(stream+result+"\n"+next+"\n")), (domain.Usage{InputTokens: 5, CacheWriteTokens: 900, CacheReadTokens: 900, OutputTokens: 50, ReportedCents: 1, Turns: 2}); !sameUsage(got, want) {
		t.Fatalf("the result must replace the messages and stay: %+v, want %+v", got, want)
	}
}

func TestExtractModelAndAuthMode(t *testing.T) {
	for _, test := range []struct {
		kind    domain.Runtime
		payload string
		model   string
	}{
		{domain.RuntimeClaude, `{"type":"system","subtype":"init","model":"claude-fable-5-1","apiKeySource":"none"}`, "claude-fable-5-1"},
		{domain.RuntimeClaude, `{"type":"assistant","message":{"model":"claude-opus-5-5","content":[]}}`, "claude-opus-5-5"},
		{domain.RuntimeClaude, `{"type":"assistant","message":{"model":"<synthetic>","content":[]}}`, ""},
		{domain.RuntimeClaude, `{"type":"system","subtype":"hook_started"}`, ""},
		{domain.RuntimeCopilot, `{"type":"session.model_change","data":{"newModel":"claude-sonnet-5","previousModel":null}}`, "claude-sonnet-5"},
		{domain.RuntimeCopilot, `{"type":"model.call_start","data":{"model":"claude-haiku-4.5","turnId":"0"}}`, "claude-haiku-4.5"},
		{domain.RuntimeCopilot, `{"type":"assistant.message","data":{"model":"gpt-5.4","outputTokens":3}}`, "gpt-5.4"},
		{domain.RuntimeCopilot, `{"type":"result","usage":{"premiumRequests":1}}`, ""},
		{domain.RuntimeCodex, `{"type":"turn.completed","usage":{"input_tokens":5}}`, ""},
	} {
		model, ok := ExtractModel(test.kind, decode(t, test.payload))
		if model != test.model || ok != (test.model != "") {
			t.Errorf("%s %s: model = %q %v, want %q", test.kind, test.payload, model, ok, test.model)
		}
	}
	for source, want := range map[string]string{"none": domain.AuthSubscription, "ANTHROPIC_API_KEY": domain.AuthAPIKey, "apiKeyHelper": domain.AuthAPIKey, "": ""} {
		mode, ok := ExtractAuthMode(domain.RuntimeClaude, decode(t, `{"type":"system","subtype":"init","apiKeySource":"`+source+`"}`))
		if mode != want || ok != (want != "") {
			t.Errorf("apiKeySource %q: auth mode = %q %v, want %q", source, mode, ok, want)
		}
	}
	if _, ok := ExtractAuthMode(domain.RuntimeCopilot, decode(t, `{"type":"system","subtype":"init","apiKeySource":"none"}`)); ok {
		t.Error("only Claude Code reports how it was signed in")
	}
}

func TestFixtureStreamsReportTheModelUsed(t *testing.T) {
	for kind, want := range map[domain.Runtime][2]string{
		domain.RuntimeClaude:  {"claude-haiku-5-5", domain.AuthSubscription},
		domain.RuntimeCopilot: {"claude-haiku-4.5", ""},
		domain.RuntimeCodex:   {"", ""},
	} {
		file, err := os.Open(filepath.Join("testdata", "usage", string(kind)+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		state := fixtureState(t, kind, file)
		_ = file.Close()
		if state.model != want[0] || state.authMode != want[1] {
			t.Errorf("%s: model %q, auth %q; want %q, %q", kind, state.model, state.authMode, want[0], want[1])
		}
	}
}

func TestCallUsageOfAResumedSessionIsItsOwn(t *testing.T) {
	// Real totals from the end of a session's first call and of the call
	// that resumed it. Claude Code's tokens are the call's own, but its cost
	// is the session's; Codex's tokens are the session's, and so are
	// Copilot's premium requests, while its output is the call's own.
	claudeFirst := decode(t, `{"type":"result","num_turns":30,"total_cost_usd":3.6660,"usage":{"input_tokens":226,"cache_creation_input_tokens":14707,"cache_read_input_tokens":692699,"output_tokens":9705}}`)
	claudeNext := decode(t, `{"type":"result","num_turns":6,"total_cost_usd":3.9619,"usage":{"input_tokens":66,"cache_creation_input_tokens":4237,"cache_read_input_tokens":290475,"output_tokens":2758}}`)
	codexFirst := decode(t, `{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":10}}`)
	codexNext := decode(t, `{"type":"turn.completed","usage":{"input_tokens":3000,"cached_input_tokens":900,"cache_write_input_tokens":0,"output_tokens":30}}`)
	// Copilot's stdout from the same session's two calls: the messages'
	// output, and the session's premium requests on each result.
	copilotFirst := strings.Join([]string{`{"type":"assistant.message","data":{"messageId":"m1","model":"claude-haiku-4.5","content":"ok","outputTokens":67}}`, `{"type":"result","usage":{"premiumRequests":0.33}}`}, "\n")
	copilotNext := strings.Join([]string{`{"type":"assistant.message","data":{"messageId":"m2","model":"claude-haiku-4.5","content":"ok","outputTokens":55}}`, `{"type":"result","usage":{"premiumRequests":0.66}}`}, "\n")
	totals := func(kind domain.Runtime, stream string) domain.Usage {
		return fixtureUsage(t, kind, strings.NewReader(stream+"\n"))
	}
	jsonl := func(payload map[string]any) string {
		data, _ := json.Marshal(payload)
		return string(data)
	}
	for _, test := range []struct {
		kind        domain.Runtime
		first, next string
		want        domain.Usage
	}{
		{domain.RuntimeClaude, jsonl(claudeFirst), jsonl(claudeNext), domain.Usage{InputTokens: 66, CacheWriteTokens: 4237, CacheReadTokens: 290475, OutputTokens: 2758, ReportedCents: 29.59, Turns: 6}},
		{domain.RuntimeCodex, jsonl(codexFirst), jsonl(codexNext), domain.Usage{InputTokens: 1100, CacheReadTokens: 900, OutputTokens: 20}},
		{domain.RuntimeCopilot, copilotFirst, copilotNext, domain.Usage{OutputTokens: 55, PremiumRequests: 0.33, Turns: 1}},
	} {
		before, totals := totals(test.kind, test.first), totals(test.kind, test.next)
		if got := CallUsage(test.kind, totals, before); !sameUsage(got, test.want) {
			t.Errorf("%s resumed call = %+v, want %+v", test.kind, got, test.want)
		}
		// A new session's first call is all its own, and so is a call whose
		// totals fell below the session's: the CLI started counting again.
		if got := CallUsage(test.kind, before, domain.Usage{}); got != before {
			t.Errorf("%s first call = %+v, want %+v", test.kind, got, before)
		}
		if got := CallUsage(test.kind, before, totals); got != before {
			t.Errorf("%s call after a restarted count = %+v, want %+v", test.kind, got, before)
		}
	}
}

func TestFixtureStreamsGiveEachRuntimeItsCostSource(t *testing.T) {
	premium := 0.04
	withPremium := pricing.Default().With(pricing.Overrides{PremiumRequest: &premium})
	for _, test := range []struct {
		kind      domain.Runtime
		requested string
		table     pricing.Table
		source    string
		cents     float64
	}{
		// Claude Code reports a cost; the table's estimate sits beside it.
		{domain.RuntimeClaude, "", pricing.Default(), domain.CostReported, 0.604093},
		// Codex reports none and does not name its model, so the model Inkuna
		// asked for prices its tokens.
		{domain.RuntimeCodex, "gpt-5.6-luna", pricing.Default(), domain.CostEstimated, (315*0.2 + 24448*0.02 + 122*1.2) / 10_000},
		{domain.RuntimeCodex, "", pricing.Default(), domain.CostUnknown, 0},
		// Copilot's premium requests have no price until inkuna.yaml gives one.
		{domain.RuntimeCopilot, "", pricing.Default(), domain.CostUnknown, 0},
		{domain.RuntimeCopilot, "", withPremium, domain.CostEstimated, 0.33 * 4},
	} {
		file, err := os.Open(filepath.Join("testdata", "usage", string(test.kind)+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		state := fixtureState(t, test.kind, file)
		_ = file.Close()
		model := state.model
		if model == "" {
			model = test.requested
		}
		cost := test.table.Cost(test.kind, model, state.callUsage(test.kind))
		if cost.Source != test.source || math.Abs(cost.Cents-test.cents) > 1e-9 {
			t.Errorf("%s (%q) cost = %+v, want %s %.6f cents", test.kind, model, cost, test.source, test.cents)
		}
		if test.kind == domain.RuntimeClaude && math.Abs(cost.EstimatedCents-cost.Cents) > 0.0001 {
			t.Errorf("claude estimate %.6f cents strays from the reported %.6f", cost.EstimatedCents, cost.Cents)
		}
	}
}
