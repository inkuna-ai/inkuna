-- A round a person started by resuming a run that stopped before it
-- finished (#171): it runs what the round before it left unfinished. A
-- resume carries no feedback of its own, so this is what tells it apart from
-- a round of feedback, and what the check loop counts a person's last round
-- from. Discarded records that the person threw away the uncommitted changes
-- the stopped steps had left.
CREATE TABLE resumes (
  execution_id TEXT NOT NULL,
  round INTEGER NOT NULL,
  discarded INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  PRIMARY KEY (execution_id, round)
);
