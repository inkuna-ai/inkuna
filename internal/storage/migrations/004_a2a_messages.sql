-- One row per A2A message so conversations stay queryable thread by thread
-- without decoding every task snapshot. a2a_tasks.task_json remains the
-- source for artifacts and status.
CREATE TABLE a2a_messages (
  message_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES a2a_tasks(id),
  context_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  role TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  message_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX a2a_messages_task_idx ON a2a_messages(task_id, sequence);
CREATE INDEX a2a_tasks_updated_idx ON a2a_tasks(updated_at);
CREATE INDEX inputs_a2a_task_idx ON inputs(a2a_task_id);
