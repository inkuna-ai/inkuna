-- Making questions in Messages readable (#193).
--
-- permissions_json lists what a permission request asks the person to
-- allow, one command line or tool each, so the inbox shows each on its own
-- instead of one run-on sentence.
--
-- stage marks the question a human-input stage asks. Its thread lists the
-- documents of the steps the stage follows, as a review gate's does.
ALTER TABLE inputs ADD COLUMN permissions_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE inputs ADD COLUMN stage INTEGER NOT NULL DEFAULT 0;
