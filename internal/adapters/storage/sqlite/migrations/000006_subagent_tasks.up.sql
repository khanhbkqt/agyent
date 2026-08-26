CREATE TABLE IF NOT EXISTS subagent_tasks (
    id TEXT PRIMARY KEY,                                      -- Task UUID (e.g. "task-f8b6c9bf-...")
    parent_session_key TEXT NOT NULL,                         -- Attached to User/Chat/Thread (e.g. "telegram:chat_id[:thread_id]")
    parent_conversation_id TEXT NOT NULL,                     -- AGY Conversation UUID of Main Track
    sub_conversation_id TEXT NOT NULL DEFAULT '',             -- AGY Conversation UUID of Sub-Agent
    agent_name TEXT NOT NULL,                                 -- Target Agent persona (e.g. "researcher", "coder", "agyent")
    project_name TEXT NOT NULL DEFAULT '',                    -- Project Scope (Empty = Global)
    title TEXT NOT NULL,                                      -- Concise human-readable task title
    prompt TEXT NOT NULL,                                     -- Detailed instruction prompt
    model TEXT NOT NULL DEFAULT 'flash',                      -- Execution model (flash, flash_lite, pro)
    effort TEXT NOT NULL DEFAULT 'low',                       -- Reasoning effort (low, medium, high)
    workspace_mode TEXT NOT NULL DEFAULT 'share',             -- "share" (Project CWD) or "scratch" (Isolated Sandbox)
    callback_mode TEXT NOT NULL DEFAULT 'notify_user',        -- "notify_user" | "callback_main" | "silent"
    status TEXT NOT NULL DEFAULT 'PENDING',                   -- PENDING | RUNNING | WAITING_FOR_INPUT | COMPLETED | FAILED | CANCELLED
    current_step INTEGER NOT NULL DEFAULT 0,                  -- Active Step Index
    current_tool TEXT NOT NULL DEFAULT '',                    -- Name of tool currently in ACTIVE state
    progress_message TEXT NOT NULL DEFAULT '',                -- Real-time human-readable progress note
    pending_question TEXT NOT NULL DEFAULT '',                -- Question text if status == WAITING_FOR_INPUT
    result_summary TEXT NOT NULL DEFAULT '',                  -- Distilled summary Markdown
    artifacts_json TEXT NOT NULL DEFAULT '[]',                -- JSON array of generated artifacts
    error_message TEXT NOT NULL DEFAULT '',                   -- Failure details if status == FAILED
    total_tokens INTEGER NOT NULL DEFAULT 0,
    duration_seconds REAL NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,                              -- Unix Milliseconds (FlexTime)
    updated_at INTEGER NOT NULL,                              -- Unix Milliseconds (FlexTime)
    FOREIGN KEY(parent_session_key) REFERENCES sessions(session_key) ON DELETE CASCADE
);

-- Optimized Lookup Indexes
CREATE INDEX IF NOT EXISTS idx_subagent_tasks_lookup
ON subagent_tasks(parent_session_key, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_subagent_tasks_active
ON subagent_tasks(status, created_at ASC);
