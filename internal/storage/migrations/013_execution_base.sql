-- Runs remember the commit their workspace started from, so the whole of a
-- run's change can be shown from it to the branch, even after the worktree
-- is gone. Runs from before keep it empty and fall back to the parent of
-- their first commit.
ALTER TABLE executions ADD COLUMN base_commit TEXT NOT NULL DEFAULT '';
