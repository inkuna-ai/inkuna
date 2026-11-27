-- Validated staffing decisions by the key of the orchestrator call that made
-- them (orchestrator.Key), so a plan that would ask the same question again
-- reuses the answer instead of calling the model. The key covers everything
-- the model was given, so a kept decision does not go stale; re-staffing
-- replaces it.
CREATE TABLE staffing_decisions (
  key TEXT PRIMARY KEY,
  decision TEXT NOT NULL,
  decided_at TEXT NOT NULL
);
