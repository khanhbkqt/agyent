-- 000012_in_flight_turns.up.sql
-- Table for tracking in-flight turn execution across daemon lifecycle
CREATE TABLE IF NOT EXISTS in_flight_turns (
    turn_id TEXT PRIMARY KEY,
    session_key TEXT NOT NULL,
    conversation_id TEXT NOT NULL DEFAULT '',
    agent_name TEXT NOT NULL,
    project_name TEXT NOT NULL DEFAULT '',
    channel TEXT NOT NULL,
    chat_id TEXT NOT NULL,
    thread_id TEXT NOT NULL DEFAULT '',
    inbound_message_id INTEGER NOT NULL DEFAULT 0,
    bot_id INTEGER NOT NULL DEFAULT 0,
    user_id TEXT NOT NULL,
    user_name TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL,
    is_ephemeral BOOLEAN NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'PENDING',
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 1,
    recovery_mode TEXT NOT NULL DEFAULT 'auto',
    error_message TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_in_flight_turns_status ON in_flight_turns(status);
CREATE INDEX IF NOT EXISTS idx_in_flight_turns_session ON in_flight_turns(session_key);

-- Extend subagent_tasks with retry protection against infinite crash loops
ALTER TABLE subagent_tasks ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subagent_tasks ADD COLUMN max_retries INTEGER NOT NULL DEFAULT 1;
