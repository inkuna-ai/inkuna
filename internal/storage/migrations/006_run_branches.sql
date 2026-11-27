-- Runs remember the branch they worked on, the cleanup policy their plan
-- chose, and when Inkuna removed their worktree, so cleanup can act on runs
-- from earlier sessions and the run view can still show their commits.
ALTER TABLE executions ADD COLUMN branch TEXT NOT NULL DEFAULT '';
ALTER TABLE executions ADD COLUMN cleanup TEXT NOT NULL DEFAULT '';
ALTER TABLE executions ADD COLUMN worktree_removed_at TEXT;
