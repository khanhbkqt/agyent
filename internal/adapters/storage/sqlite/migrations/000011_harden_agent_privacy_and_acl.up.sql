-- 1. Set all legacy unowned public agents to private to prevent unauthorized external access
UPDATE agents 
SET is_public = 0, updated_at = (CAST((julianday('now') - 2440587.5)*86400000 AS INTEGER))
WHERE is_public = 1 AND (owner_id IS NULL OR owner_id = '');

-- 2. Ensure default agent 'agyent' is strictly private if unowned
UPDATE agents 
SET is_public = 0, updated_at = (CAST((julianday('now') - 2440587.5)*86400000 AS INTEGER))
WHERE name = 'agyent' AND (owner_id IS NULL OR owner_id = '');

-- 3. Security Audit Events Table for Persistent Security Audit Logging
CREATE TABLE IF NOT EXISTS security_audit_events (
    id TEXT PRIMARY KEY,
    timestamp INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    actor_provider TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    decision TEXT NOT NULL,
    details TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_security_audit_actor ON security_audit_events(actor_id);
CREATE INDEX IF NOT EXISTS idx_security_audit_timestamp ON security_audit_events(timestamp);
