-- Review gates (docs/feedback-loop.md): a run paused at a gate asks the
-- person in the inbox, as a human-input stage does. The question records
-- that it is a review, and the step a reply sends changes to, empty for a
-- gate that can only be approved.
ALTER TABLE inputs ADD COLUMN review INTEGER NOT NULL DEFAULT 0;
ALTER TABLE inputs ADD COLUMN review_target TEXT NOT NULL DEFAULT '';
