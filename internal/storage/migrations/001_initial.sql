CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  repository TEXT NOT NULL UNIQUE,
  default_runtime TEXT NOT NULL CHECK (default_runtime IN ('codex', 'claude', 'copilot')),
  settings_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  state TEXT NOT NULL,
  kind TEXT NOT NULL,
  spec_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE workflows (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  state TEXT NOT NULL,
  spec_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  approved_at TEXT
);

CREATE TABLE steps (
  workflow_id TEXT NOT NULL REFERENCES workflows(id),
  id TEXT NOT NULL,
  state TEXT NOT NULL,
  spec_json TEXT NOT NULL,
  PRIMARY KEY (workflow_id, id)
);

CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  workflow_id TEXT NOT NULL,
  step_id TEXT NOT NULL,
  runtime TEXT NOT NULL,
  state TEXT NOT NULL,
  worktree TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  handoff_json TEXT,
  FOREIGN KEY (workflow_id, step_id) REFERENCES steps(workflow_id, id)
);

CREATE TABLE events (
  run_id TEXT NOT NULL REFERENCES runs(id),
  sequence INTEGER NOT NULL,
  event_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (run_id, sequence)
);

CREATE TABLE artifacts (
  digest TEXT PRIMARY KEY,
  media_type TEXT NOT NULL,
  size INTEGER NOT NULL,
  name TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE skills (
  id TEXT PRIMARY KEY,
  metadata_json TEXT NOT NULL,
  source_path TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE approvals (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  kind TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  state TEXT NOT NULL,
  created_at TEXT NOT NULL,
  resolved_at TEXT
);

CREATE TABLE github_links (
  task_id TEXT PRIMARY KEY REFERENCES tasks(id),
  repository TEXT NOT NULL,
  issue_number INTEGER NOT NULL,
  issue_url TEXT NOT NULL,
  marker TEXT NOT NULL UNIQUE,
  last_status TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX events_created_idx ON events(run_id, created_at);
CREATE INDEX tasks_project_state_idx ON tasks(project_id, state);
