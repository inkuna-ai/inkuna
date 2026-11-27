-- A delete that leaves a run's worktree on disk records it here (#65). The
-- run's rows are gone by then, so this is the only record that the directory
-- is Inkuna's, which lets Settings list it, measure it, and remove it later.
-- A row outlives the task it names, so neither id is a foreign key.
CREATE TABLE kept_worktrees (
  path TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  branch TEXT NOT NULL DEFAULT '',
  task_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  kept_at TEXT NOT NULL
);
