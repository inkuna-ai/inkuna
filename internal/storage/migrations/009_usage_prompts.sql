-- The prompt a worker or peer call sent, as its runtime received it, by
-- its digest in the artifact store. Calls stored before this, and
-- orchestrator calls, have none.
ALTER TABLE usage ADD COLUMN prompt_digest TEXT NOT NULL DEFAULT '';
