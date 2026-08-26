-- Add active_model and active_effort columns to sessions table
ALTER TABLE sessions ADD COLUMN active_model TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN active_effort TEXT NOT NULL DEFAULT '';

-- Add default_model and default_effort columns to agents table
ALTER TABLE agents ADD COLUMN default_model TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN default_effort TEXT NOT NULL DEFAULT '';

-- Add model and effort columns to audit_logs table
ALTER TABLE audit_logs ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_logs ADD COLUMN effort TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_audit_logs_model ON audit_logs(model);
