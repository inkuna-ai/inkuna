CREATE TABLE executions (
  id TEXT PRIMARY KEY,
  workflow_id TEXT NOT NULL REFERENCES workflows(id),
  task_id TEXT NOT NULL REFERENCES tasks(id),
  project_id TEXT NOT NULL REFERENCES projects(id),
  state TEXT NOT NULL,
  title TEXT NOT NULL,
  worktree TEXT,
  error TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT
);

CREATE TABLE inputs (
  id TEXT PRIMARY KEY,
  execution_id TEXT NOT NULL REFERENCES executions(id),
  step_id TEXT NOT NULL,
  a2a_task_id TEXT,
  agent TEXT NOT NULL,
  asked_by TEXT NOT NULL DEFAULT '',
  question TEXT NOT NULL,
  options_json TEXT NOT NULL DEFAULT '[]',
  context TEXT,
  state TEXT NOT NULL,
  answer TEXT,
  created_at TEXT NOT NULL,
  resolved_at TEXT
);

CREATE TABLE a2a_tasks (
  id TEXT PRIMARY KEY,
  context_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  state TEXT NOT NULL,
  task_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE task_artifacts (
  digest TEXT NOT NULL,
  task_id TEXT NOT NULL,
  execution_id TEXT,
  step_id TEXT,
  agent TEXT,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  media_type TEXT NOT NULL,
  size INTEGER NOT NULL,
  description TEXT,
  created_at TEXT NOT NULL,
  PRIMARY KEY (digest, task_id, step_id, kind)
);

CREATE TABLE commits (
  execution_id TEXT NOT NULL REFERENCES executions(id),
  step_id TEXT NOT NULL,
  hash TEXT NOT NULL,
  subject TEXT NOT NULL,
  stat TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (execution_id, step_id)
);

CREATE INDEX executions_state_idx ON executions(state, started_at);
CREATE INDEX inputs_state_idx ON inputs(state, created_at);
CREATE INDEX a2a_tasks_context_idx ON a2a_tasks(context_id, updated_at);
CREATE INDEX task_artifacts_task_idx ON task_artifacts(task_id, created_at);
