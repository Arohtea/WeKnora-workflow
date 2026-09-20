-- 工作流生产可用 V1：草稿修订、不可变发布版本和运行记录。
ALTER TABLE custom_agents
    ADD COLUMN IF NOT EXISTS draft_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE custom_agents
    ADD COLUMN IF NOT EXISTS published_version BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS workflow_versions (
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    version BIGINT NOT NULL,
    draft_revision BIGINT NOT NULL,
    definition JSONB NOT NULL,
    config_snapshot JSONB NOT NULL,
    published_by VARCHAR(36) NOT NULL DEFAULT '',
    published_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, agent_id, version),
    CONSTRAINT fk_workflow_versions_agent
        FOREIGN KEY (agent_id, tenant_id) REFERENCES custom_agents(id, tenant_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_workflow_versions_agent
    ON workflow_versions (tenant_id, agent_id, version DESC);

CREATE TABLE IF NOT EXISTS workflow_runs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    workflow_version BIGINT NOT NULL,
    draft_revision BIGINT NOT NULL DEFAULT 0,
    definition_snapshot JSONB NOT NULL,
    config_snapshot JSONB NOT NULL,
    input_payload JSONB,
    run_mode VARCHAR(16) NOT NULL DEFAULT 'production',
    requested_by VARCHAR(36) NOT NULL DEFAULT '',
    idempotency_key VARCHAR(128) NOT NULL DEFAULT '',
    retry_of_run_id VARCHAR(36) NOT NULL DEFAULT '',
    trigger_source VARCHAR(24) NOT NULL,
    status VARCHAR(16) NOT NULL,
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    message_id VARCHAR(36) NOT NULL DEFAULT '',
    request_id VARCHAR(64) NOT NULL DEFAULT '',
    input_summary TEXT NOT NULL DEFAULT '',
    input_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    output_summary TEXT NOT NULL DEFAULT '',
    output_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error_summary TEXT NOT NULL DEFAULT '',
    error_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    usage JSONB,
    cancel_requested_at TIMESTAMP WITH TIME ZONE,
    last_heartbeat_at TIMESTAMP WITH TIME ZONE,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL,
    finished_at TIMESTAMP WITH TIME ZONE,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_runs_agent
        FOREIGN KEY (agent_id, tenant_id) REFERENCES custom_agents(id, tenant_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_tenant_agent_time
    ON workflow_runs (tenant_id, agent_id, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_tenant_status_time
    ON workflow_runs (tenant_id, status, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_session
    ON workflow_runs (tenant_id, session_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_mode_status_time
    ON workflow_runs (tenant_id, run_mode, status, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_retry_of
    ON workflow_runs (tenant_id, retry_of_run_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_runs_idempotency
    ON workflow_runs (tenant_id, agent_id, run_mode, idempotency_key)
    WHERE idempotency_key <> '';

CREATE TABLE IF NOT EXISTS workflow_run_branches (
    id VARCHAR(36) PRIMARY KEY,
    run_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    branch_path TEXT NOT NULL DEFAULT '',
    current_node_id VARCHAR(80) NOT NULL DEFAULT '',
    variables JSONB NOT NULL,
    status VARCHAR(16) NOT NULL,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error_summary TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    last_node_run_id BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_run_branches_run
        FOREIGN KEY (run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_workflow_run_branches_run_status
    ON workflow_run_branches (run_id, status);
CREATE INDEX IF NOT EXISTS idx_workflow_run_branches_agent_status
    ON workflow_run_branches (tenant_id, agent_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS workflow_run_nodes (
    id BIGSERIAL PRIMARY KEY,
    run_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    node_id VARCHAR(80) NOT NULL,
    node_name VARCHAR(100) NOT NULL,
    node_type VARCHAR(32) NOT NULL,
    branch_id VARCHAR(36) NOT NULL DEFAULT '',
    branch_path TEXT NOT NULL DEFAULT '',
    sequence BIGINT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 1,
    retry_of BIGINT NOT NULL DEFAULT 0,
    task_id VARCHAR(128) NOT NULL DEFAULT '',
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(16) NOT NULL,
    input_payload JSONB,
    input_summary TEXT NOT NULL DEFAULT '',
    input_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    output_payload JSONB,
    output_summary TEXT NOT NULL DEFAULT '',
    output_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error_summary TEXT NOT NULL DEFAULT '',
    error_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    usage JSONB,
    started_at TIMESTAMP WITH TIME ZONE,
    finished_at TIMESTAMP WITH TIME ZONE,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_run_nodes_run
        FOREIGN KEY (run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_workflow_run_nodes_run_sequence
    ON workflow_run_nodes (run_id, sequence);
CREATE INDEX IF NOT EXISTS idx_workflow_run_nodes_agent_status_time
    ON workflow_run_nodes (tenant_id, agent_id, status, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_workflow_run_nodes_run_status
    ON workflow_run_nodes (run_id, status);
CREATE INDEX IF NOT EXISTS idx_workflow_run_nodes_task
    ON workflow_run_nodes (task_id);
CREATE INDEX IF NOT EXISTS idx_workflow_run_nodes_retry_of
    ON workflow_run_nodes (retry_of);
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_run_nodes_attempt
    ON workflow_run_nodes (run_id, branch_id, node_id, attempt);

CREATE TABLE IF NOT EXISTS workflow_run_events (
    id BIGSERIAL PRIMARY KEY,
    run_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    event_id VARCHAR(96) NOT NULL,
    sequence BIGINT NOT NULL,
    event_type VARCHAR(48) NOT NULL,
    node_id VARCHAR(80) NOT NULL DEFAULT '',
    branch_path TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL,
    occurred_at TIMESTAMP WITH TIME ZONE NOT NULL,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    summary TEXT NOT NULL DEFAULT '',
    summary_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    extra JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_workflow_run_events_run
        FOREIGN KEY (run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_run_events_sequence
    ON workflow_run_events (run_id, sequence);
CREATE INDEX IF NOT EXISTS idx_workflow_run_events_agent_time
    ON workflow_run_events (tenant_id, agent_id, occurred_at DESC);

-- 旧工作流先获得一个兼容发布版本。详细的 v1 -> v2 字段转换由 Go 迁移器
-- 在读取快照时完成，避免用数据库方言重写嵌套 JSON。
UPDATE custom_agents
SET draft_revision = CASE WHEN draft_revision = 0 THEN 1 ELSE draft_revision END,
    published_version = CASE WHEN published_version = 0 THEN 1 ELSE published_version END
WHERE config->>'agent_type' = 'workflow'
  AND deleted_at IS NULL
  AND config->'workflow' IS NOT NULL;

INSERT INTO workflow_versions
    (tenant_id, agent_id, version, draft_revision, definition, config_snapshot, published_by, published_at)
SELECT tenant_id,
       id,
       published_version,
       draft_revision,
       COALESCE(config->'workflow', '{}'::jsonb),
       config,
       '',
       COALESCE(updated_at, CURRENT_TIMESTAMP)
FROM custom_agents
WHERE config->>'agent_type' = 'workflow'
  AND deleted_at IS NULL
  AND config->'workflow' IS NOT NULL
  AND published_version > 0
ON CONFLICT (tenant_id, agent_id, version) DO NOTHING;
