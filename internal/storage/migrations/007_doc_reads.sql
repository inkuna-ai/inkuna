-- Which docs agents opened during a task, in the order they opened them.
-- Paths only, relative to the project's docs root, with that root relative
-- to the repository as it was when the step ran; never a doc's content.
CREATE TABLE doc_reads (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  step_id TEXT NOT NULL,
  run_id TEXT NOT NULL REFERENCES executions(id),
  project_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  runtime TEXT NOT NULL,
  docs_dir TEXT NOT NULL,
  doc_path TEXT NOT NULL,
  tool TEXT NOT NULL,
  opened_at TEXT NOT NULL
);
CREATE INDEX doc_reads_task ON doc_reads(task_id, id);
CREATE INDEX doc_reads_doc ON doc_reads(project_id, docs_dir, doc_path);
