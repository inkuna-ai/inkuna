-- Line comments (docs/feedback-loop.md, #62): a person comments on lines of
-- a run's branch changes, and each comment goes back with the next request
-- for changes as a feedback item. A thread is where its comments are and how
-- it stands; its comments are what the person wrote in it, a draft until it
-- is sent.
CREATE TABLE comment_threads (
  execution_id TEXT NOT NULL REFERENCES executions(id),
  id TEXT NOT NULL,
  path TEXT NOT NULL,
  -- new: lines of the branch's version, numbered at commit_hash; old: lines
  -- the branch removed, numbered at base.
  side TEXT NOT NULL,
  line INTEGER NOT NULL,
  end_line INTEGER NOT NULL,
  base TEXT NOT NULL,
  commit_hash TEXT NOT NULL,
  -- The round the run was in when the thread was started.
  round INTEGER NOT NULL,
  -- open or resolved; resolved_by says who resolved it, human or agent.
  state TEXT NOT NULL DEFAULT 'open',
  resolved_by TEXT NOT NULL DEFAULT '',
  -- The round after which the thread's lines had changed, 0 while they
  -- have not.
  outdated_round INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  PRIMARY KEY (execution_id, id)
);

CREATE TABLE comments (
  execution_id TEXT NOT NULL REFERENCES executions(id),
  id TEXT NOT NULL,
  thread_id TEXT NOT NULL,
  body TEXT NOT NULL,
  target TEXT NOT NULL,
  -- draft until it is sent; then round and item name the feedback item it
  -- went as.
  state TEXT NOT NULL DEFAULT 'draft',
  round INTEGER NOT NULL DEFAULT 0,
  item TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (execution_id, id),
  FOREIGN KEY (execution_id, thread_id) REFERENCES comment_threads(execution_id, id)
);
CREATE INDEX comments_thread_idx ON comments(execution_id, thread_id);

-- A comment's item keeps where it was and what the target was shown as it
-- was sent, whatever happens to its thread after.
ALTER TABLE feedback ADD COLUMN comment_id TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback ADD COLUMN path TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback ADD COLUMN side TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback ADD COLUMN line INTEGER NOT NULL DEFAULT 0;
ALTER TABLE feedback ADD COLUMN end_line INTEGER NOT NULL DEFAULT 0;
ALTER TABLE feedback ADD COLUMN commit_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback ADD COLUMN excerpt TEXT NOT NULL DEFAULT '';
