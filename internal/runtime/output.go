package runtime

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// ExtractOutput finds the worker's final structured output in one raw
// runtime event. Each headless CLI reports it differently:
//
//   - Claude Code (--output-format stream-json --json-schema) emits a final
//     {"type":"result"} event carrying "structured_output", or "result" text.
//   - Codex (exec --json --output-schema) emits {"type":"item.completed"} with
//     an "agent_message" item whose text is the JSON document.
//   - Copilot (--output-format=json) emits assistant message events whose
//     content is asked to be JSON; the last parsable message wins.
//
// The returned bytes are a compact JSON object. Later matches replace earlier
// ones so callers keep the final answer.
func ExtractOutput(kind domain.Runtime, payload map[string]any) (json.RawMessage, bool) {
	rawType, _ := payload["type"].(string)
	switch kind {
	case domain.RuntimeClaude:
		if rawType != "result" {
			return nil, false
		}
		if structured, ok := payload["structured_output"]; ok && structured != nil {
			if data, err := json.Marshal(structured); err == nil && isObject(data) {
				return data, true
			}
		}
		if text, _ := payload["result"].(string); text != "" {
			return parseJSONText(text)
		}
		return nil, false
	case domain.RuntimeCodex:
		if rawType != "item.completed" {
			return nil, false
		}
		item, _ := payload["item"].(map[string]any)
		if itemType, _ := item["type"].(string); itemType != "agent_message" {
			return nil, false
		}
		text, _ := item["text"].(string)
		return parseJSONText(text)
	case domain.RuntimeCopilot:
		if !strings.Contains(strings.ToLower(rawType), "message") && !strings.Contains(strings.ToLower(rawType), "result") {
			return nil, false
		}
		for _, candidate := range copilotTexts(payload) {
			if output, ok := parseJSONText(candidate); ok {
				return output, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

// ExtractDenials reads the tool calls a runtime refused for lack of
// permission. Only Claude Code reports them in a structured form: its final
// {"type":"result"} event lists "permission_denials", each with the tool name
// and the input it was called with. Repeats of one call collapse to one entry.
func ExtractDenials(kind domain.Runtime, payload map[string]any) []domain.PermissionDenial {
	if rawType, _ := payload["type"].(string); kind != domain.RuntimeClaude || rawType != "result" {
		return nil
	}
	entries, _ := payload["permission_denials"].([]any)
	denials := make([]domain.PermissionDenial, 0, len(entries))
	seen := map[domain.PermissionDenial]bool{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		tool, _ := entry["tool_name"].(string)
		if tool = strings.TrimSpace(tool); tool == "" {
			continue
		}
		denial := domain.PermissionDenial{Tool: tool, Detail: redact(truncate(claudeToolDetail(entry["tool_input"]), 300))}
		if tool == "Bash" {
			input, _ := entry["tool_input"].(map[string]any)
			command, _ := input["command"].(string)
			// The command is kept verbatim so an approval can allow exactly it.
			// One that carries a credential is left out: it stays a redacted
			// warning and is never stored or offered for approval.
			if command = strings.TrimSpace(command); redact(command) == command {
				denial.Command = command
			}
		}
		if !seen[denial] {
			seen[denial] = true
			denials = append(denials, denial)
		}
	}
	return denials
}

// GrantFor returns the narrowest grant that would let a denied call through,
// or false when no approval can: file edits follow the step's permission
// alone, and a shell call is only ever allowed as one exact command line,
// held to the check a rule's commands are (AllowedCommandFor). The CLIs read
// an allowed line as a pattern, so approving rm -rf build/* would otherwise
// also allow rm -rf build/ ~/src (#270).
func GrantFor(denial domain.PermissionDenial) (domain.PermissionGrant, bool) {
	if denial.Tool == "Bash" {
		argv, ok := AllowedCommandFor(denial.Command)
		if !ok {
			return domain.PermissionGrant{}, false
		}
		return domain.PermissionGrant{Command: strings.Join(argv, " ")}, true
	}
	if !claudeGrantableTool(denial.Tool) {
		return domain.PermissionGrant{}, false
	}
	return domain.PermissionGrant{Tool: denial.Tool}, true
}

// ExtractUsage reads the totals a runtime reports at the end of a call:
// Claude's result and Codex's turn.completed. Input is split into fresh,
// cache-write, and cache-read tokens however the runtime reports them, so
// the three never overlap. In a resumed session some totals cover the
// session's earlier calls too; CallUsage takes those off. Copilot reports no
// token totals: its messages' output is all it says of tokens, and its
// result only premium requests (ExtractPremiumRequests).
func ExtractUsage(kind domain.Runtime, payload map[string]any) (domain.Usage, bool) {
	rawType, _ := payload["type"].(string)
	var usage domain.Usage
	switch kind {
	case domain.RuntimeClaude:
		if rawType != "result" {
			return usage, false
		}
		if raw, ok := payload["usage"].(map[string]any); ok {
			usage = claudeTokens(raw)
		}
		// Claude Code prices the call at API list prices, even when it is
		// signed in to a subscription that bills nothing per call.
		if cost, ok := payload["total_cost_usd"].(float64); ok && cost > 0 {
			usage.ReportedCents = cost * 100
		}
		usage.Turns = intValue(payload["num_turns"])
		return usage, true
	case domain.RuntimeCodex:
		if rawType != "turn.completed" {
			return usage, false
		}
		// OpenAI counts the tokens read from and written to the cache inside
		// input_tokens, so they come off it here.
		if raw, ok := payload["usage"].(map[string]any); ok {
			usage.CacheReadTokens = intValue(raw["cached_input_tokens"])
			usage.CacheWriteTokens = intValue(raw["cache_write_input_tokens"])
			usage.InputTokens = max(intValue(raw["input_tokens"])-usage.CacheReadTokens-usage.CacheWriteTokens, 0)
			usage.OutputTokens = intValue(raw["output_tokens"])
		}
		return usage, true
	}
	return usage, false
}

// ExtractPremiumRequests reads the Copilot premium requests a session has
// used so far, which Copilot's result reports: its requests weighted by each
// model's multiplier, which is what Copilot bills, not money.
func ExtractPremiumRequests(kind domain.Runtime, payload map[string]any) (float64, bool) {
	if rawType, _ := payload["type"].(string); kind != domain.RuntimeCopilot || rawType != "result" {
		return 0, false
	}
	usage, _ := payload["usage"].(map[string]any)
	premium, ok := usage["premiumRequests"].(float64)
	return premium, ok
}

// claudeTokens reads a Claude usage object. Its input_tokens already exclude
// the cached prompt, which it reports as the tokens it wrote to the cache and
// read from it, and it says how many of the writes went to the one-hour
// cache.
func claudeTokens(raw map[string]any) domain.Usage {
	written, _ := raw["cache_creation"].(map[string]any)
	return domain.Usage{
		InputTokens: intValue(raw["input_tokens"]), CacheWriteTokens: intValue(raw["cache_creation_input_tokens"]),
		LongCacheWriteTokens: intValue(written["ephemeral_1h_input_tokens"]), CacheReadTokens: intValue(raw["cache_read_input_tokens"]),
		OutputTokens: intValue(raw["output_tokens"]),
	}
}

// CallUsage is what one call used, from the totals its runtime reported at
// its end and the totals the session had reported before it, which are zero
// for a new session. A resumed session's totals include its earlier calls:
// Claude Code's cost, Codex's tokens, and Copilot's premium requests. Claude
// Code's tokens and turns, and Copilot's output tokens, are the call's own.
// Totals below what the session had reported mean the CLI started counting
// again, so they are the call's own too.
func CallUsage(kind domain.Runtime, totals, before domain.Usage) domain.Usage {
	switch kind {
	case domain.RuntimeClaude:
		if totals.ReportedCents >= before.ReportedCents {
			totals.ReportedCents -= before.ReportedCents
		}
	case domain.RuntimeCodex:
		if totals.TotalInputTokens()+totals.OutputTokens >= before.TotalInputTokens()+before.OutputTokens {
			totals.InputTokens = max(totals.InputTokens-before.InputTokens, 0)
			totals.CacheWriteTokens = max(totals.CacheWriteTokens-before.CacheWriteTokens, 0)
			totals.CacheReadTokens = max(totals.CacheReadTokens-before.CacheReadTokens, 0)
			totals.OutputTokens = max(totals.OutputTokens-before.OutputTokens, 0)
		}
	case domain.RuntimeCopilot:
		if totals.PremiumRequests >= before.PremiumRequests {
			totals.PremiumRequests -= before.PremiumRequests
		}
	}
	return totals
}

// ExtractModel reads the model a call used: from Claude Code's init event
// and the messages it answers with, and from Copilot's model changes, model
// calls, and messages. A later report replaces an earlier one. Codex does not
// name its model.
func ExtractModel(kind domain.Runtime, payload map[string]any) (string, bool) {
	rawType, _ := payload["type"].(string)
	var model string
	switch {
	case kind == domain.RuntimeClaude && rawType == "system":
		if subtype, _ := payload["subtype"].(string); subtype == "init" {
			model, _ = payload["model"].(string)
		}
	case kind == domain.RuntimeClaude && rawType == "assistant":
		message, _ := payload["message"].(map[string]any)
		// An API error comes back as a made-up message from no model.
		if model, _ = message["model"].(string); model == "<synthetic>" {
			model = ""
		}
	case kind == domain.RuntimeCopilot && rawType == "session.model_change":
		data, _ := payload["data"].(map[string]any)
		model, _ = data["newModel"].(string)
	case kind == domain.RuntimeCopilot && (rawType == "model.call_start" || rawType == "assistant.message"):
		data, _ := payload["data"].(map[string]any)
		model, _ = data["model"].(string)
	}
	model = strings.TrimSpace(model)
	return model, model != ""
}

// ExtractAuthMode reads how the runtime was signed in, which only Claude
// Code's init event says: an apiKeySource of "none" is a subscription login,
// and any other source is an API key.
func ExtractAuthMode(kind domain.Runtime, payload map[string]any) (string, bool) {
	if rawType, _ := payload["type"].(string); kind != domain.RuntimeClaude || rawType != "system" {
		return "", false
	}
	if subtype, _ := payload["subtype"].(string); subtype != "init" {
		return "", false
	}
	switch source, _ := payload["apiKeySource"].(string); strings.TrimSpace(source) {
	case "":
		return "", false
	case "none":
		return domain.AuthSubscription, true
	default:
		return domain.AuthAPIKey, true
	}
}

// ExtractMessageUsage reads what one model message used, from the events a
// runtime streams before its totals, with the message's id when it has one.
// A call cut off before its totals reports the sum of these. Claude repeats a
// message's usage on the event for each of its content blocks, so the id is
// what keeps it from counting twice; Copilot reports only output tokens, and
// never its input.
func ExtractMessageUsage(kind domain.Runtime, payload map[string]any) (string, domain.Usage, bool) {
	rawType, _ := payload["type"].(string)
	var usage domain.Usage
	switch {
	case kind == domain.RuntimeClaude && rawType == "assistant":
		message, _ := payload["message"].(map[string]any)
		raw, ok := message["usage"].(map[string]any)
		if !ok {
			return "", usage, false
		}
		usage = claudeTokens(raw)
		usage.Turns = 1
		id, _ := message["id"].(string)
		return id, usage, true
	case kind == domain.RuntimeCopilot && rawType == "assistant.message":
		data, _ := payload["data"].(map[string]any)
		if usage.OutputTokens = intValue(data["outputTokens"]); usage.OutputTokens <= 0 {
			return "", usage, false
		}
		usage.Turns = 1
		id, _ := data["messageId"].(string)
		return id, usage, true
	}
	return "", usage, false
}

// ExtractSession reads the id of the session a call ran in, which a later
// call can resume: Claude Code's session_id (on its init and result events),
// Codex's thread id (on thread.started), and Copilot's sessionId (on its
// result).
func ExtractSession(kind domain.Runtime, payload map[string]any) (string, bool) {
	rawType, _ := payload["type"].(string)
	key := ""
	switch {
	case kind == domain.RuntimeClaude:
		key = "session_id"
	case kind == domain.RuntimeCodex && rawType == "thread.started":
		key = "thread_id"
	case kind == domain.RuntimeCopilot && rawType == "result":
		key = "sessionId"
	}
	session, _ := payload[key].(string)
	session = strings.TrimSpace(session)
	return session, session != ""
}

// ExtractCacheLifetime reads how long the prompt cache the event's request
// wrote will last. Claude Code reports it with each message's usage, as the
// tokens written to the five-minute or the one-hour cache. The other CLIs
// report nothing.
func ExtractCacheLifetime(kind domain.Runtime, payload map[string]any) (time.Duration, bool) {
	if kind != domain.RuntimeClaude {
		return 0, false
	}
	usage, _ := payload["usage"].(map[string]any)
	if message, ok := payload["message"].(map[string]any); ok {
		usage, _ = message["usage"].(map[string]any)
	}
	written, _ := usage["cache_creation"].(map[string]any)
	switch {
	case intValue(written["ephemeral_5m_input_tokens"]) > 0:
		return 5 * time.Minute, true
	case intValue(written["ephemeral_1h_input_tokens"]) > 0:
		return time.Hour, true
	}
	return 0, false
}

// ExtractActivity reports whether an event shows the model at work: a
// message or reasoning it wrote, or a tool it called. A CLI's own startup and
// bookkeeping does not count, even where it is told apart only by its type:
// Claude Code's system events and the assistant message it makes up for an
// API error, Codex's thread and turn events and its warning items, and
// Copilot's session events and the turn markers it emits around a model call
// that failed.
func ExtractActivity(kind domain.Runtime, payload map[string]any) bool {
	rawType, _ := payload["type"].(string)
	switch kind {
	case domain.RuntimeClaude:
		message, _ := payload["message"].(map[string]any)
		model, _ := message["model"].(string)
		return rawType == "assistant" && model != "<synthetic>"
	case domain.RuntimeCodex:
		item, _ := payload["item"].(map[string]any)
		itemType, _ := item["type"].(string)
		return strings.HasPrefix(rawType, "item.") && itemType != "" && itemType != "error"
	case domain.RuntimeCopilot:
		switch rawType {
		case "assistant.message", "assistant.message_delta", "assistant.reasoning", "assistant.reasoning_delta":
			return true
		}
		return strings.HasPrefix(rawType, "tool.")
	}
	return false
}

// ExtractFailure detects a failure the CLI reported inside its event stream.
// Some runtimes exit zero after such events, so Start must not trust the
// exit status alone.
func ExtractFailure(kind domain.Runtime, payload map[string]any) (string, bool) {
	message, ok := extractFailure(kind, payload)
	if !ok {
		return "", false
	}
	return redact(message), true
}

func extractFailure(kind domain.Runtime, payload map[string]any) (string, bool) {
	rawType, _ := payload["type"].(string)
	switch kind {
	case domain.RuntimeCodex:
		switch rawType {
		case "turn.failed":
			if detail, ok := payload["error"].(map[string]any); ok {
				if message, _ := detail["message"].(string); message != "" {
					return message, true
				}
			}
			return "codex turn failed", true
		case "error":
			if message, _ := payload["message"].(string); message != "" {
				return message, true
			}
			return "codex reported an error", true
		}
	case domain.RuntimeClaude:
		if rawType != "result" {
			return "", false
		}
		if failed, _ := payload["is_error"].(bool); failed {
			if text, _ := payload["result"].(string); text != "" {
				return text, true
			}
			return "claude reported an error", true
		}
		if subtype, _ := payload["subtype"].(string); subtype != "" && subtype != "success" {
			return "claude finished with " + subtype, true
		}
	case domain.RuntimeCopilot:
		if rawType == "error" || rawType == "session.error" || rawType == "assistant.error" {
			if data, ok := payload["data"].(map[string]any); ok {
				if message, _ := data["message"].(string); message != "" {
					return message, true
				}
			}
			if message, _ := payload["message"].(string); message != "" {
				return message, true
			}
			return "copilot reported an error", true
		}
	}
	return "", false
}

func copilotTexts(payload map[string]any) []string {
	texts := make([]string, 0, 3)
	for _, key := range []string{"content", "text", "result", "message"} {
		if text, ok := payload[key].(string); ok {
			texts = append(texts, text)
		}
	}
	if data, ok := payload["data"].(map[string]any); ok {
		for _, key := range []string{"content", "text", "result"} {
			if text, ok := data[key].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	if message, ok := payload["message"].(map[string]any); ok {
		if text, ok := message["content"].(string); ok {
			texts = append(texts, text)
		}
	}
	return texts
}

// parseJSONText accepts a JSON object, optionally wrapped in a Markdown code
// fence or surrounded by prose, and returns it compacted.
func parseJSONText(text string) (json.RawMessage, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
		text = strings.TrimSpace(text)
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, false
	}
	candidate := []byte(text[start : end+1])
	if !json.Valid(candidate) || !isObject(candidate) {
		return nil, false
	}
	var compact strings.Builder
	var value any
	if err := json.Unmarshal(candidate, &value); err != nil {
		return nil, false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	compact.Write(data)
	return json.RawMessage(compact.String()), true
}

func isObject(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	return strings.HasPrefix(trimmed, "{")
}

func intValue(raw any) int {
	switch value := raw.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}
