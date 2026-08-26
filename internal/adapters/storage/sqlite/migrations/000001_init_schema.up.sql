-- 1. Users Whitelist & Role Management
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,                          -- Telegram User ID (e.g. "123456789")
    username TEXT,                                 -- @username without @
    full_name TEXT,
    role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'developer', 'guest')),
    created_at INTEGER NOT NULL                   -- Unix Milliseconds
);

-- 2. Allowed Telegram Groups & Supergroups
CREATE TABLE IF NOT EXISTS allowed_groups (
    group_id TEXT PRIMARY KEY,                     -- Telegram Chat ID (e.g. "-100123456789")
    group_title TEXT,
    is_active INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at INTEGER NOT NULL                   -- Unix Milliseconds
);

-- 3. Agent Personas & Workspaces
CREATE TABLE IF NOT EXISTS agents (
    name TEXT PRIMARY KEY,                         -- 'dev_expert', 'personal_assistant'
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'uninitialized' CHECK (status IN ('uninitialized', 'initialized')),
    workspace_path TEXT NOT NULL,                  -- Absolute path to agent universe
    created_at INTEGER NOT NULL,                  -- Unix Milliseconds
    updated_at INTEGER NOT NULL                   -- Unix Milliseconds
);

-- 4. Projects (Attached to Agents)
CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,                           -- Composite ID: 'agent_name:project_name'
    agent_name TEXT NOT NULL,
    project_name TEXT NOT NULL,
    project_path TEXT NOT NULL,                    -- Absolute filesystem directory
    created_at INTEGER NOT NULL,                  -- Unix Milliseconds
    FOREIGN KEY(agent_name) REFERENCES agents(name) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_projects_agent_name ON projects(agent_name);

-- 5. Sessions (Interactive Context per DM / Group / Topic)
CREATE TABLE IF NOT EXISTS sessions (
    session_key TEXT PRIMARY KEY,                  -- Format: "telegram:chat_id[:thread_id]"
    active_agent TEXT NOT NULL,
    active_project TEXT NOT NULL DEFAULT '',       -- Empty string = Global Mode
    global_conversation_id TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,                  -- Unix Milliseconds
    FOREIGN KEY(active_agent) REFERENCES agents(name) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_sessions_active_agent ON sessions(active_agent);

-- 6. Project Conversation Context Isolation (Solves Conversation Loss Bug)
CREATE TABLE IF NOT EXISTS session_project_conversations (
    session_key TEXT NOT NULL,
    project_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,                  -- Unix Milliseconds
    PRIMARY KEY(session_key, project_id),
    FOREIGN KEY(session_key) REFERENCES sessions(session_key) ON DELETE CASCADE,
    FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_spc_project_id ON session_project_conversations(project_id);

-- 7. Audit Logs & Token Metrics (Permanent Compliance Records)
CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_key TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    project_name TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT '',
    prompt_length INTEGER NOT NULL DEFAULT 0,
    response_length INTEGER NOT NULL DEFAULT 0,
    duration_seconds REAL NOT NULL DEFAULT 0.0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    thinking_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK (status IN ('SUCCESS', 'ERROR')),
    error_message TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL                   -- Unix Milliseconds
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_session_key ON audit_logs(session_key);
CREATE INDEX IF NOT EXISTS idx_audit_logs_agent_name ON audit_logs(agent_name);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at);
