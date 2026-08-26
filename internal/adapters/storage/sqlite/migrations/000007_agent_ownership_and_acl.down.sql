DROP INDEX IF EXISTS idx_agent_permissions_user_id;
DROP TABLE IF EXISTS agent_permissions;
DROP INDEX IF EXISTS idx_agents_owner_id;
ALTER TABLE agents DROP COLUMN owner_id;
ALTER TABLE agents DROP COLUMN is_public;
