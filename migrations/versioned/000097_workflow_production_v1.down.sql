DROP TABLE IF EXISTS workflow_run_events;
DROP TABLE IF EXISTS workflow_run_nodes;
DROP TABLE IF EXISTS workflow_run_branches;
DROP TABLE IF EXISTS workflow_runs;
DROP TABLE IF EXISTS workflow_versions;
ALTER TABLE custom_agents DROP COLUMN IF EXISTS published_version;
ALTER TABLE custom_agents DROP COLUMN IF EXISTS draft_revision;
