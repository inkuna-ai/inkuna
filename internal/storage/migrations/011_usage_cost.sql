-- Every call gets a cost and the source of that figure: reported by its
-- runtime, estimated from its tokens and the pricing table, or unknown,
-- which is never valued at zero. Costs are fractional cents, since a small
-- call costs well under one. model_used is the model the runtime's stream
-- named, beside the one Inkuna asked for, and auth_mode how it was signed
-- in. long_cache_write_tokens are the cache writes that last an hour, which
-- cost more than five-minute ones.
--
-- The table is rebuilt to make cost_cents REAL. Existing rows keep what they
-- measured: a cost the built-in claude runtime reported stays reported, and
-- every other row is unknown, since SQL cannot price tokens. A runtime added
-- in inkuna.yaml stored its own name, so whether it wrote Claude Code's
-- stream or Copilot's, whose figure was premium requests, cannot be told,
-- and its figure is dropped rather than taken for money.
CREATE TABLE usage_with_cost (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  execution_id TEXT REFERENCES executions(id),
  step_id TEXT NOT NULL,
  call_id TEXT NOT NULL,
  turn INTEGER NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('worker', 'peer', 'orchestrator')),
  agent TEXT NOT NULL,
  runtime TEXT NOT NULL,
  model TEXT NOT NULL,
  model_used TEXT NOT NULL DEFAULT '',
  auth_mode TEXT NOT NULL DEFAULT '',
  input_tokens INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL,
  long_cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL,
  reported_cents REAL NOT NULL DEFAULT 0,
  premium_requests REAL NOT NULL DEFAULT 0,
  cost_cents REAL NOT NULL DEFAULT 0,
  cost_source TEXT NOT NULL DEFAULT 'unknown' CHECK (cost_source IN ('reported', 'estimated', 'unknown')),
  estimated_cents REAL NOT NULL DEFAULT 0,
  pricing_version INTEGER NOT NULL DEFAULT 0,
  turns INTEGER NOT NULL,
  prompt_bytes INTEGER NOT NULL,
  prompt_sections_json TEXT NOT NULL DEFAULT '[]',
  prompt_digest TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT NOT NULL
);

INSERT INTO usage_with_cost(id, task_id, execution_id, step_id, call_id, turn, kind, agent, runtime, model,
  input_tokens, cache_write_tokens, cache_read_tokens, output_tokens, reported_cents, premium_requests, cost_cents, cost_source,
  turns, prompt_bytes, prompt_sections_json, prompt_digest, outcome, started_at, finished_at)
SELECT id, task_id, execution_id, step_id, call_id, turn, kind, agent, runtime, model,
  input_tokens, cache_write_tokens, cache_read_tokens, output_tokens,
  CASE WHEN runtime = 'claude' THEN cost_cents ELSE 0 END,
  0,
  CASE WHEN runtime = 'claude' AND cost_cents > 0 THEN cost_cents ELSE 0 END,
  CASE WHEN runtime = 'claude' AND cost_cents > 0 THEN 'reported' ELSE 'unknown' END,
  turns, prompt_bytes, prompt_sections_json, prompt_digest, outcome, started_at, finished_at
FROM usage;

DROP TABLE usage;
ALTER TABLE usage_with_cost RENAME TO usage;
CREATE INDEX usage_execution ON usage(execution_id, id);
CREATE INDEX usage_task ON usage(task_id, id);
