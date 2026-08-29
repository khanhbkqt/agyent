-- Create covering index for active conversations wildcard queries and idle scanning
CREATE INDEX IF NOT EXISTS idx_conversations_active_wildcard ON conversations (is_archived, is_pinned DESC, updated_at DESC);
