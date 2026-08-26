-- Add cache_read_tokens column to audit_logs table for tracking LLM prompt caching metrics
ALTER TABLE audit_logs ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
