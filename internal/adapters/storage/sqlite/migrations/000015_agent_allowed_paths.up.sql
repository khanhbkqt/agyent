-- 1. Extend agents table with allowed_paths (JSON string array)
ALTER TABLE agents ADD COLUMN allowed_paths TEXT NOT NULL DEFAULT '[]';

-- 2. Backfill existing agents to empty JSON array
UPDATE agents SET allowed_paths = '[]' WHERE allowed_paths = '' OR allowed_paths IS NULL;
