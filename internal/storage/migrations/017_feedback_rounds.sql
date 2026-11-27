-- Feedback rounds (docs/feedback-loop.md): a run can be sent back to the
-- stages that wrote it, and runs those stages and what follows them again.
-- A run records its current round, and its step runs and commits the round
-- they belong to.
ALTER TABLE executions ADD COLUMN round INTEGER NOT NULL DEFAULT 1;
-- The plan the run was approved with, so a later round runs it even after
-- the task is planned again. Empty for runs from before it was kept.
ALTER TABLE executions ADD COLUMN plan_json TEXT NOT NULL DEFAULT '';

ALTER TABLE runs ADD COLUMN execution_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN round INTEGER NOT NULL DEFAULT 1;
-- Every step run's id starts with its run's id and a hyphen: a step's own
-- <run>-<step> and its peers' <run>-<step>-peer<n>-<agent>, which store the
-- parent's step_id, so the suffix cannot be stripped. A run id holds one
-- hyphen, after "run", so one run's id and a hyphen never starts another's.
-- A row from before runs were recorded matches none and keeps no run.
UPDATE runs SET execution_id = COALESCE((
  SELECT executions.id FROM executions
  WHERE executions.workflow_id = runs.workflow_id
    AND substr(runs.id, 1, length(executions.id) + 1) = executions.id || '-'
), '');
CREATE INDEX runs_execution_idx ON runs(execution_id, step_id, round);

-- A round's commit for a step is its own row; the key gains the round.
CREATE TABLE commits_by_round (
  execution_id TEXT NOT NULL REFERENCES executions(id),
  step_id TEXT NOT NULL,
  round INTEGER NOT NULL DEFAULT 1,
  agent TEXT NOT NULL DEFAULT '',
  hash TEXT NOT NULL,
  subject TEXT NOT NULL,
  stat TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (execution_id, step_id, round)
);
INSERT INTO commits_by_round(execution_id, step_id, round, agent, hash, subject, stat, created_at)
  SELECT execution_id, step_id, 1, agent, hash, subject, stat, created_at FROM commits;
DROP TABLE commits;
ALTER TABLE commits_by_round RENAME TO commits;

-- What a round was sent back with, one row per item. The target's reply
-- (#50) sets status and note.
CREATE TABLE feedback (
  execution_id TEXT NOT NULL REFERENCES executions(id),
  round INTEGER NOT NULL,
  id TEXT NOT NULL,
  target TEXT NOT NULL,
  author TEXT NOT NULL,
  text TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'open',
  note TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  PRIMARY KEY (execution_id, round, id)
);
