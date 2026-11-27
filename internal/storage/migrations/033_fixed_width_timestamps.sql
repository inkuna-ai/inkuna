-- Rewrite every stored timestamp in the one fixed-width form storage now
-- writes (timestampLayout in internal/storage/timestamp.go): UTC with nine
-- fractional digits, such as 2026-11-17T16:57:10.487110000Z. Queries order
-- and compare timestamps as text, which is time order only when every value
-- has the same width; RFC3339Nano dropped trailing zeros, so "10.48711Z"
-- sorted after "10.487110001Z".
--
-- A value in UTC ("...Z") keeps every digit: its fraction is padded with
-- zeros. A value with an offset ("...+01:00") is converted to UTC with
-- strftime, which keeps milliseconds. Anything else, such as an empty
-- string, is left as it is.

UPDATE a2a_messages SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE a2a_messages SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE a2a_tasks SET updated_at = substr(updated_at, 1, 19) || '.' || substr(CASE WHEN substr(updated_at, 20, 1) = '.' THEN substr(updated_at, 21, length(updated_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(updated_at) <> 30;
UPDATE a2a_tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%f', updated_at) || '000000Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', updated_at) IS NOT NULL;
UPDATE approvals SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE approvals SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE approvals SET resolved_at = substr(resolved_at, 1, 19) || '.' || substr(CASE WHEN substr(resolved_at, 20, 1) = '.' THEN substr(resolved_at, 21, length(resolved_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE resolved_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(resolved_at) <> 30;
UPDATE approvals SET resolved_at = strftime('%Y-%m-%dT%H:%M:%f', resolved_at) || '000000Z'
  WHERE resolved_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', resolved_at) IS NOT NULL;
UPDATE artifacts SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE artifacts SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE checks SET finished_at = substr(finished_at, 1, 19) || '.' || substr(CASE WHEN substr(finished_at, 20, 1) = '.' THEN substr(finished_at, 21, length(finished_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(finished_at) <> 30;
UPDATE checks SET finished_at = strftime('%Y-%m-%dT%H:%M:%f', finished_at) || '000000Z'
  WHERE finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', finished_at) IS NOT NULL;
UPDATE checks SET started_at = substr(started_at, 1, 19) || '.' || substr(CASE WHEN substr(started_at, 20, 1) = '.' THEN substr(started_at, 21, length(started_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(started_at) <> 30;
UPDATE checks SET started_at = strftime('%Y-%m-%dT%H:%M:%f', started_at) || '000000Z'
  WHERE started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', started_at) IS NOT NULL;
UPDATE comment_threads SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE comment_threads SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE comments SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE comments SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE comments SET updated_at = substr(updated_at, 1, 19) || '.' || substr(CASE WHEN substr(updated_at, 20, 1) = '.' THEN substr(updated_at, 21, length(updated_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(updated_at) <> 30;
UPDATE comments SET updated_at = strftime('%Y-%m-%dT%H:%M:%f', updated_at) || '000000Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', updated_at) IS NOT NULL;
UPDATE commits SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE commits SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE doc_reads SET opened_at = substr(opened_at, 1, 19) || '.' || substr(CASE WHEN substr(opened_at, 20, 1) = '.' THEN substr(opened_at, 21, length(opened_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE opened_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(opened_at) <> 30;
UPDATE doc_reads SET opened_at = strftime('%Y-%m-%dT%H:%M:%f', opened_at) || '000000Z'
  WHERE opened_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', opened_at) IS NOT NULL;
UPDATE events SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE events SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE executions SET finished_at = substr(finished_at, 1, 19) || '.' || substr(CASE WHEN substr(finished_at, 20, 1) = '.' THEN substr(finished_at, 21, length(finished_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(finished_at) <> 30;
UPDATE executions SET finished_at = strftime('%Y-%m-%dT%H:%M:%f', finished_at) || '000000Z'
  WHERE finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', finished_at) IS NOT NULL;
UPDATE executions SET started_at = substr(started_at, 1, 19) || '.' || substr(CASE WHEN substr(started_at, 20, 1) = '.' THEN substr(started_at, 21, length(started_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(started_at) <> 30;
UPDATE executions SET started_at = strftime('%Y-%m-%dT%H:%M:%f', started_at) || '000000Z'
  WHERE started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', started_at) IS NOT NULL;
UPDATE executions SET worktree_removed_at = substr(worktree_removed_at, 1, 19) || '.' || substr(CASE WHEN substr(worktree_removed_at, 20, 1) = '.' THEN substr(worktree_removed_at, 21, length(worktree_removed_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE worktree_removed_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(worktree_removed_at) <> 30;
UPDATE executions SET worktree_removed_at = strftime('%Y-%m-%dT%H:%M:%f', worktree_removed_at) || '000000Z'
  WHERE worktree_removed_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', worktree_removed_at) IS NOT NULL;
UPDATE feedback SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE feedback SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE github_links SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE github_links SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE inputs SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE inputs SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE inputs SET resolved_at = substr(resolved_at, 1, 19) || '.' || substr(CASE WHEN substr(resolved_at, 20, 1) = '.' THEN substr(resolved_at, 21, length(resolved_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE resolved_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(resolved_at) <> 30;
UPDATE inputs SET resolved_at = strftime('%Y-%m-%dT%H:%M:%f', resolved_at) || '000000Z'
  WHERE resolved_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', resolved_at) IS NOT NULL;
UPDATE kept_worktrees SET kept_at = substr(kept_at, 1, 19) || '.' || substr(CASE WHEN substr(kept_at, 20, 1) = '.' THEN substr(kept_at, 21, length(kept_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE kept_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(kept_at) <> 30;
UPDATE kept_worktrees SET kept_at = strftime('%Y-%m-%dT%H:%M:%f', kept_at) || '000000Z'
  WHERE kept_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', kept_at) IS NOT NULL;
UPDATE projects SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE projects SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE projects SET removed_at = substr(removed_at, 1, 19) || '.' || substr(CASE WHEN substr(removed_at, 20, 1) = '.' THEN substr(removed_at, 21, length(removed_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE removed_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(removed_at) <> 30;
UPDATE projects SET removed_at = strftime('%Y-%m-%dT%H:%M:%f', removed_at) || '000000Z'
  WHERE removed_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', removed_at) IS NOT NULL;
UPDATE pull_requests SET checked_at = substr(checked_at, 1, 19) || '.' || substr(CASE WHEN substr(checked_at, 20, 1) = '.' THEN substr(checked_at, 21, length(checked_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE checked_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(checked_at) <> 30;
UPDATE pull_requests SET checked_at = strftime('%Y-%m-%dT%H:%M:%f', checked_at) || '000000Z'
  WHERE checked_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', checked_at) IS NOT NULL;
UPDATE pull_requests SET merged_at = substr(merged_at, 1, 19) || '.' || substr(CASE WHEN substr(merged_at, 20, 1) = '.' THEN substr(merged_at, 21, length(merged_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE merged_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(merged_at) <> 30;
UPDATE pull_requests SET merged_at = strftime('%Y-%m-%dT%H:%M:%f', merged_at) || '000000Z'
  WHERE merged_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', merged_at) IS NOT NULL;
UPDATE resumes SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE resumes SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE runs SET cache_until = substr(cache_until, 1, 19) || '.' || substr(CASE WHEN substr(cache_until, 20, 1) = '.' THEN substr(cache_until, 21, length(cache_until) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE cache_until GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(cache_until) <> 30;
UPDATE runs SET cache_until = strftime('%Y-%m-%dT%H:%M:%f', cache_until) || '000000Z'
  WHERE cache_until GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', cache_until) IS NOT NULL;
UPDATE runs SET finished_at = substr(finished_at, 1, 19) || '.' || substr(CASE WHEN substr(finished_at, 20, 1) = '.' THEN substr(finished_at, 21, length(finished_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(finished_at) <> 30;
UPDATE runs SET finished_at = strftime('%Y-%m-%dT%H:%M:%f', finished_at) || '000000Z'
  WHERE finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', finished_at) IS NOT NULL;
UPDATE runs SET started_at = substr(started_at, 1, 19) || '.' || substr(CASE WHEN substr(started_at, 20, 1) = '.' THEN substr(started_at, 21, length(started_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(started_at) <> 30;
UPDATE runs SET started_at = strftime('%Y-%m-%dT%H:%M:%f', started_at) || '000000Z'
  WHERE started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', started_at) IS NOT NULL;
UPDATE skills SET updated_at = substr(updated_at, 1, 19) || '.' || substr(CASE WHEN substr(updated_at, 20, 1) = '.' THEN substr(updated_at, 21, length(updated_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(updated_at) <> 30;
UPDATE skills SET updated_at = strftime('%Y-%m-%dT%H:%M:%f', updated_at) || '000000Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', updated_at) IS NOT NULL;
UPDATE staffing_decisions SET decided_at = substr(decided_at, 1, 19) || '.' || substr(CASE WHEN substr(decided_at, 20, 1) = '.' THEN substr(decided_at, 21, length(decided_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE decided_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(decided_at) <> 30;
UPDATE staffing_decisions SET decided_at = strftime('%Y-%m-%dT%H:%M:%f', decided_at) || '000000Z'
  WHERE decided_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', decided_at) IS NOT NULL;
UPDATE task_artifacts SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE task_artifacts SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE tasks SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE tasks SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE tasks SET updated_at = substr(updated_at, 1, 19) || '.' || substr(CASE WHEN substr(updated_at, 20, 1) = '.' THEN substr(updated_at, 21, length(updated_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(updated_at) <> 30;
UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%f', updated_at) || '000000Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', updated_at) IS NOT NULL;
UPDATE tracker_changes SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE tracker_changes SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
UPDATE tracker_changes SET updated_at = substr(updated_at, 1, 19) || '.' || substr(CASE WHEN substr(updated_at, 20, 1) = '.' THEN substr(updated_at, 21, length(updated_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(updated_at) <> 30;
UPDATE tracker_changes SET updated_at = strftime('%Y-%m-%dT%H:%M:%f', updated_at) || '000000Z'
  WHERE updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', updated_at) IS NOT NULL;
UPDATE usage SET finished_at = substr(finished_at, 1, 19) || '.' || substr(CASE WHEN substr(finished_at, 20, 1) = '.' THEN substr(finished_at, 21, length(finished_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(finished_at) <> 30;
UPDATE usage SET finished_at = strftime('%Y-%m-%dT%H:%M:%f', finished_at) || '000000Z'
  WHERE finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', finished_at) IS NOT NULL;
UPDATE usage SET started_at = substr(started_at, 1, 19) || '.' || substr(CASE WHEN substr(started_at, 20, 1) = '.' THEN substr(started_at, 21, length(started_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(started_at) <> 30;
UPDATE usage SET started_at = strftime('%Y-%m-%dT%H:%M:%f', started_at) || '000000Z'
  WHERE started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', started_at) IS NOT NULL;
UPDATE viewed_files SET viewed_at = substr(viewed_at, 1, 19) || '.' || substr(CASE WHEN substr(viewed_at, 20, 1) = '.' THEN substr(viewed_at, 21, length(viewed_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE viewed_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(viewed_at) <> 30;
UPDATE viewed_files SET viewed_at = strftime('%Y-%m-%dT%H:%M:%f', viewed_at) || '000000Z'
  WHERE viewed_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', viewed_at) IS NOT NULL;
UPDATE workflows SET approved_at = substr(approved_at, 1, 19) || '.' || substr(CASE WHEN substr(approved_at, 20, 1) = '.' THEN substr(approved_at, 21, length(approved_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE approved_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(approved_at) <> 30;
UPDATE workflows SET approved_at = strftime('%Y-%m-%dT%H:%M:%f', approved_at) || '000000Z'
  WHERE approved_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', approved_at) IS NOT NULL;
UPDATE workflows SET created_at = substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND length(created_at) <> 30;
UPDATE workflows SET created_at = strftime('%Y-%m-%dT%H:%M:%f', created_at) || '000000Z'
  WHERE created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*[+-][0-9][0-9]:[0-9][0-9]' AND strftime('%Y-%m-%dT%H:%M:%f', created_at) IS NOT NULL;
