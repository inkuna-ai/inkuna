-- Runtimes are declared in inkuna.yaml, so the set of valid names is no longer
-- fixed. Go validates a project's default runtime against the declarations;
-- the database only requires that one is recorded.
CREATE TABLE projects_with_declared_runtimes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  repository TEXT NOT NULL UNIQUE,
  default_runtime TEXT NOT NULL CHECK (default_runtime <> ''),
  settings_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

INSERT INTO projects_with_declared_runtimes(id, name, repository, default_runtime, settings_json, created_at)
SELECT id, name, repository, default_runtime, settings_json, created_at FROM projects;

DROP TABLE projects;
ALTER TABLE projects_with_declared_runtimes RENAME TO projects;
