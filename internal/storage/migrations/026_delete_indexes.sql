-- Deleting a task removes its rows child table first (#64). For each parent
-- row a delete removes, SQLite looks up the rows whose foreign key names it,
-- so each child column without an index would make every delete scan its
-- table. These are the child columns the deletes go through that no index
-- already leads with.
CREATE INDEX workflows_task_idx ON workflows(task_id);
CREATE INDEX runs_step_idx ON runs(workflow_id, step_id);
CREATE INDEX executions_workflow_idx ON executions(workflow_id);
CREATE INDEX inputs_execution_idx ON inputs(execution_id);
CREATE INDEX doc_reads_run_idx ON doc_reads(run_id);
CREATE INDEX approvals_task_idx ON approvals(task_id);
