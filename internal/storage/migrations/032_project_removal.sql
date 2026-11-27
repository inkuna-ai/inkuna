-- Connecting and removing projects (#197).
--
-- removed_at marks a project the person removed, or whose inkuna.yaml entry
-- went away. Its tasks and runs stay readable under its name, and
-- connecting its repository again brings it back with them.
--
-- listed records that the project has been in inkuna.yaml's repositories,
-- which is the list of connected projects. A project from before that, added
-- only to this database, is written into inkuna.yaml once at startup rather
-- than taken for one whose entry the person removed.
ALTER TABLE projects ADD COLUMN removed_at TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN listed INTEGER NOT NULL DEFAULT 0;
