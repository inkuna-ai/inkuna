-- A run's story in its repository's tracker, and the tracker settings it was
-- approved under (docs/mcp-and-trackers.md, #154), as JSON. A later round, a
-- restart, or Mark done writes within them, so a step that edited
-- tracker.yaml in a checkout never raises its own run's ceiling. Empty for a
-- run without a story, and for runs from before it was kept.
ALTER TABLE executions ADD COLUMN story_json TEXT NOT NULL DEFAULT '';
