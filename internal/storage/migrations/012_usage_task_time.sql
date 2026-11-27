-- Costs are read by task and by period: every call made for a task, and a
-- project's calls that ended between two times, task by task. One index on
-- task and end time serves both, and replaces the one on task and id, which
-- it leads with the same column. A task's runs are read by task too.
CREATE INDEX usage_task_time ON usage(task_id, finished_at);
DROP INDEX usage_task;
CREATE INDEX executions_task_idx ON executions(task_id, started_at);
