-- Rollback migration for 000004_cache_read_tokens
-- Note: DROP COLUMN is supported on SQLite >= 3.35.0. No-op select for safe fallback.
SELECT 1;
