-- 1. Extend agents table with security_preset
ALTER TABLE agents ADD COLUMN security_preset TEXT NOT NULL DEFAULT 'balanced';

-- 2. Backfill existing agents to balanced
UPDATE agents SET security_preset = 'balanced' WHERE security_preset = '' OR security_preset IS NULL;
