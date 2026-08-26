-- 1. Create conversations catalog table
CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,                           -- AGY Conversation UUID (e.g. "f8b6c9bf-...")
    session_key TEXT NOT NULL,                     -- Gắn với User/Chat/Thread
    agent_name TEXT NOT NULL,                      -- Tên Agent phụ trách
    project_name TEXT NOT NULL DEFAULT '',         -- Rỗng = Global Mode, có giá trị = In-Project
    title TEXT NOT NULL,                           -- Tiêu đề gợi nhớ (Auto-generated từ prompt đầu tiên)
    alias_index INTEGER NOT NULL DEFAULT 0,        -- Số thứ tự hiển thị (#1, #2...)
    turn_count INTEGER NOT NULL DEFAULT 0,         -- Số turn đã trao đổi
    is_pinned INTEGER NOT NULL DEFAULT 0 CHECK (is_pinned IN (0, 1)),      -- 1 = Ghim (Bảo vệ vĩnh viễn)
    is_archived INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0, 1)),  -- 1 = Lưu trữ (Ẩn khỏi menu chính)
    created_at INTEGER NOT NULL,                   -- Unix Milliseconds
    updated_at INTEGER NOT NULL,                   -- Unix Milliseconds
    FOREIGN KEY(session_key) REFERENCES sessions(session_key) ON DELETE CASCADE,
    FOREIGN KEY(agent_name) REFERENCES agents(name) ON DELETE RESTRICT
);

-- Optimized composite indexes for fast lookups and sorting
CREATE INDEX IF NOT EXISTS idx_conversations_active_lookup 
ON conversations(session_key, agent_name, project_name, is_archived, is_pinned DESC, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_conversations_alias
ON conversations(session_key, agent_name, project_name, alias_index);

CREATE INDEX IF NOT EXISTS idx_conversations_gc_purge
ON conversations(is_archived, is_pinned, updated_at);

-- Seamless Backfill: Global active conversations
INSERT OR IGNORE INTO conversations (id, session_key, agent_name, project_name, title, alias_index, turn_count, is_pinned, is_archived, created_at, updated_at)
SELECT s.global_conversation_id, s.session_key, s.active_agent, '', 'Cuộc trò chuyện chính', 1, 1, 0, 0, s.updated_at, s.updated_at
FROM sessions s
WHERE s.global_conversation_id != '';

-- Seamless Backfill: Project active conversations
INSERT OR IGNORE INTO conversations (id, session_key, agent_name, project_name, title, alias_index, turn_count, is_pinned, is_archived, created_at, updated_at)
SELECT spc.conversation_id, spc.session_key, s.active_agent, s.active_project, 'Phiên làm việc ' || s.active_project, 1, 1, 0, 0, spc.updated_at, spc.updated_at
FROM session_project_conversations spc
JOIN sessions s ON s.session_key = spc.session_key
WHERE spc.conversation_id != '';
