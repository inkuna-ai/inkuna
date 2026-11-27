-- The tracker changes agents ask for in their handoffs (docs/mcp-and-trackers.md,
-- #155). Each is kept when its step ends: refused, with the reason, when Go's
-- check fails, else pending until a person approves the run, which applies
-- it, or requests changes that run its step again, which discards it.
-- change_json is the change as Go checked it, with the issue resolved; url is
-- what applying it made, a comment or a new issue. a2a_task_id is the asking
-- step's conversation, which hears of a change that failed.
CREATE TABLE tracker_changes (
  execution_id TEXT NOT NULL REFERENCES executions(id),
  id TEXT NOT NULL,
  round INTEGER NOT NULL,
  step_id TEXT NOT NULL,
  step_run_id TEXT NOT NULL,
  a2a_task_id TEXT NOT NULL DEFAULT '',
  position INTEGER NOT NULL,
  kind TEXT NOT NULL,
  issue TEXT NOT NULL DEFAULT '',
  change_json TEXT NOT NULL,
  -- refused, pending, discarded, applying, applied, or failed.
  state TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (execution_id, id)
);
CREATE INDEX tracker_changes_run_idx ON tracker_changes(execution_id, state);
