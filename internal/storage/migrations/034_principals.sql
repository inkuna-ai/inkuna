-- Who acted, by the identity provider's principal ID (the OS user name when
-- there is none): the person who created each task, and the person who
-- approved each run. Rows from before are empty, since no one was recorded.
-- Both are set when the row is first written and never change: a resume or
-- a later save of the run keeps its approver.
ALTER TABLE tasks ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE executions ADD COLUMN approved_by TEXT NOT NULL DEFAULT '';
