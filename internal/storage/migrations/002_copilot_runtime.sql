CREATE TABLE projects_with_copilot (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  repository TEXT NOT NULL UNIQUE,
  default_runtime TEXT NOT NULL CHECK (default_runtime IN ('codex', 'claude', 'copilot')),
  settings_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

INSERT INTO projects_with_copilot(id, name, repository, default_runtime, settings_json, created_at)
SELECT id, name, repository, default_runtime, settings_json, created_at FROM projects;

DROP TABLE projects;
ALTER TABLE projects_with_copilot RENAME TO projects;
