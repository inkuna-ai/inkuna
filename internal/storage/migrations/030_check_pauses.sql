-- Pausing a check loop for the person when a check fails in a way the
-- writer cannot fix (#160).
--
-- check_pause marks the question a run asks when an evaluation stage's
-- checks failed for a reason only a person can fix: an exit code the rule
-- names as a system failure, a command that could not start, or one a
-- signal stopped. It is answered only with one of its options (re-run the
-- checks, send them to the writer, or fail the run), and, like a review,
-- it is not an answer the run's workers are given.
ALTER TABLE inputs ADD COLUMN check_pause INTEGER NOT NULL DEFAULT 0;
