-- The check loop (docs/feedback-loop.md): a failed check a rule sends back
-- is a feedback item that names the check it reports and keeps the end of
-- its log as the target was sent it; the whole log stays in the check's
-- artifact. A person's items have neither.
ALTER TABLE feedback ADD COLUMN check_id TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback ADD COLUMN tail TEXT NOT NULL DEFAULT '';
