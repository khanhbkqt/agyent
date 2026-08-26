-- 1. Extend agents table with ownership and visibility
ALTER TABLE agents ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN is_public INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_agents_owner_id ON agents(owner_id);

-- 2. Data Backfill: Set existing agents to public for backward compatibility
UPDATE agents SET is_public = 1 WHERE is_public = 0;
UPDATE agents SET is_public = 1 WHERE name = 'agyent';

-- 3. Granular Agent Collaborator Permissions Table
CREATE TABLE IF NOT EXISTS agent_permissions (
    agent_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'operator' CHECK (role IN ('admin', 'operator', 'viewer')),
    granted_by TEXT NOT NULL DEFAULT '',
    granted_at INTEGER NOT NULL,
    PRIMARY KEY (agent_name, user_id),
    FOREIGN KEY(agent_name) REFERENCES agents(name) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_agent_permissions_user_id ON agent_permissions(user_id);
