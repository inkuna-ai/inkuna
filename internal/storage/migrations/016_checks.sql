-- Each command an evaluation step runs, as a check: its argv, how it ended,
-- and the log it wrote, kept as a `check` artifact. An evaluation step
-- records all of its commands as queued when it starts, so a check that
-- never ran, because an earlier one failed or the run stopped, is listed
-- as skipped.
CREATE TABLE checks (
  id TEXT PRIMARY KEY,
  execution_id TEXT NOT NULL REFERENCES executions(id),
  step_id TEXT NOT NULL,
  step_run_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  argv TEXT NOT NULL,
  status TEXT NOT NULL,
  exit_code INTEGER,
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  log_digest TEXT NOT NULL DEFAULT '',
  log_bytes INTEGER NOT NULL DEFAULT 0,
  log_cut INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX checks_execution_idx ON checks(execution_id);
