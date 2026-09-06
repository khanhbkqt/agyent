CREATE TABLE IF NOT EXISTS agy_project_registry (
    tenant_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    agent_generation INTEGER NOT NULL DEFAULT 1,
    execution_host_id TEXT NOT NULL,
    agy_config_namespace_id TEXT NOT NULL,
    agy_project_id TEXT NOT NULL,
    workspace_dir TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, agent_name, agent_generation, execution_host_id, agy_config_namespace_id)
);

CREATE INDEX IF NOT EXISTS idx_agy_project_lookup ON agy_project_registry(tenant_id, agent_name, status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agy_project_unique_id ON agy_project_registry(execution_host_id, agy_config_namespace_id, agy_project_id);
