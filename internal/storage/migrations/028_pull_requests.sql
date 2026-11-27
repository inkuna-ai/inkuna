-- The pull request each run branch was opened in, as Inkuna last found it
-- on GitHub (#66). The Tasks view shows it, and a repository whose
-- worktrees.cleanup is merged removes a finished run's worktree once it
-- merged. It is keyed by branch rather than run, since a task's runs can
-- share one; a task delete leaves it, and a check forgets the row once no
-- run is on the branch.
CREATE TABLE pull_requests (
  project_id TEXT NOT NULL REFERENCES projects(id),
  branch TEXT NOT NULL,
  number INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,
  merged_at TEXT,
  checked_at TEXT NOT NULL,
  PRIMARY KEY (project_id, branch)
);
