-- One row per model call: each worker turn, peer answer, and orchestrator
-- call, whether it completed, failed, timed out, or was cancelled. A call
-- made while planning comes before its run and has no execution; it belongs
-- to the task. The prompt is kept only as the byte size of its sections.
CREATE TABLE usage (
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
  input_tokens INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL,
  cache_read_tokens INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL,
  cost_cents INTEGER NOT NULL,
  turns INTEGER NOT NULL,
  prompt_bytes INTEGER NOT NULL,
  prompt_sections_json TEXT NOT NULL DEFAULT '[]',
  outcome TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT NOT NULL
);
CREATE INDEX usage_execution ON usage(execution_id, id);
CREATE INDEX usage_task ON usage(task_id, id);
