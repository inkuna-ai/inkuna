-- Files marked viewed in a run's Changes view. A mark is kept against the
-- blob the file had when it was marked, so a file that changes again reads
-- as not viewed; marks on the file's other versions stay, so a commit scope
-- that shows an earlier version still finds its mark.
CREATE TABLE viewed_files (
  execution_id TEXT NOT NULL REFERENCES executions(id),
  path TEXT NOT NULL,
  blob TEXT NOT NULL,
  viewed_at TEXT NOT NULL,
  PRIMARY KEY (execution_id, path, blob)
);
