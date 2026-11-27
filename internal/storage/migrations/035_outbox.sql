-- The outbox holds the records Inkuna hands to the audit and usage sinks an
-- embedding binary registers (docs/extensions.md). Rows are written only while
-- such a sink is registered. No foreign key reaches the outbox, so deleting
-- history, which deletes a task's usage rows, never touches what is queued.

-- installation names this data directory in the records it sends, so records
-- from different machines never share an ID. It is made once, here.
CREATE TABLE installation (
  id TEXT NOT NULL
);
INSERT INTO installation(id) VALUES (lower(hex(randomblob(16))));

-- usage_id is the usage row a usage record was made from, so that row is
-- queued at most once; it is empty for audit events.
CREATE TABLE outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL CHECK (kind IN ('audit', 'usage')),
  usage_id INTEGER UNIQUE,
  record_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX outbox_kind_idx ON outbox(kind, id);

-- One row per sink a build has registered. A sink is given its kind's records
-- in outbox order, so delivered_through, the last one it accepted, is all that
-- says where it stands.
CREATE TABLE outbox_sinks (
  name TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('audit', 'usage')),
  delivered_through INTEGER NOT NULL DEFAULT 0,
  delivered_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  failed_at TEXT NOT NULL DEFAULT '',
  failures INTEGER NOT NULL DEFAULT 0
);

-- A kind listed here has had its existing rows queued once: usage, the first
-- time a usage sink was registered.
CREATE TABLE outbox_backfills (
  kind TEXT PRIMARY KEY,
  queued INTEGER NOT NULL,
  backfilled_at TEXT NOT NULL
);
