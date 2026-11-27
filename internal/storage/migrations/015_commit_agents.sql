-- Commits remember the agent that made them, so a run's commit list names
-- it even if a later rerun or round staffs the step differently. Commits
-- from before keep it empty, and the run's workflow names the step's agent.
ALTER TABLE commits ADD COLUMN agent TEXT NOT NULL DEFAULT '';
