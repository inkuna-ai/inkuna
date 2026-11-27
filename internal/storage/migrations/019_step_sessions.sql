-- A step run keeps the runtime session its worker last ran in, so a later
-- round can resume it while its prompt cache lasts (docs/feedback-loop.md).
-- A run that was reopened or rehydrated after a restart has no worker in
-- memory, so the row is the only place left to find it. The session's
-- usage is the totals the runtime reported for it, which a resumed call's
-- totals include, as JSON; cache_until is empty when resuming costs the
-- same at any time.
ALTER TABLE runs ADD COLUMN session TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN session_runtime TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN cache_until TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN session_usage TEXT NOT NULL DEFAULT '';
