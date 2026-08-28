# Storage Engine & Configuration Architecture (Storage & Setup Wizard)

This document details the **Pure-Go SQLite** database schema, high-concurrency WAL configuration to eliminate locking issues, and the interactive command-line setup wizard **`agyent init`**.

---

## 1. Pure-Go SQLite Database (`agyent.db`) & WAL Configuration

The project utilizes the `modernc.org/sqlite` driver (100% pure Go, zero CGO/gcc required). To guarantee **dozens of concurrent users and group chats without encountering `database is locked` errors**, the Storage Engine enforces performance pragmas upon opening connections:

```go
func OpenDB(dbPath string) (*sql.DB, error) {
    db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)")
    if err != nil {
        return nil, err
    }

    // Connection pool optimization for SQLite WAL
    db.SetMaxOpenConns(1) // Single writer to eliminate disk write race conditions
    db.SetMaxIdleConns(1)
    db.SetConnMaxLifetime(0)

    return db, nil
}
```

### Database Schema:

```sql
-- 1. Users & Whitelist Table
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,               -- Telegram User ID (e.g., "123456789")
    username TEXT,                      -- @username
    full_name TEXT,
    role TEXT DEFAULT 'admin',          -- 'admin' | 'developer' | 'guest'
    created_at INTEGER NOT NULL
);

-- 2. Allowed Telegram Groups Table
CREATE TABLE IF NOT EXISTS allowed_groups (
    group_id TEXT PRIMARY KEY,          -- Telegram Chat/Group ID (e.g., "-100123456789")
    group_title TEXT,
    is_active INTEGER DEFAULT 1,
    created_at INTEGER NOT NULL
);

-- 3. Agent Profiles Table
CREATE TABLE IF NOT EXISTS agents (
    name TEXT PRIMARY KEY,              -- 'dev_expert', 'personal_assistant'
    description TEXT,
    status TEXT DEFAULT 'uninitialized',-- 'uninitialized' | 'initialized'
    workspace_path TEXT NOT NULL,
    default_model TEXT DEFAULT '',      -- Preferred model override
    default_effort TEXT DEFAULT '',     -- Preferred effort override
    security_preset TEXT DEFAULT 'balanced', -- Baseline / active security preset ('balanced', 'strict', etc.)
    owner_id TEXT DEFAULT '',           -- Telegram User ID of owner
    is_public INTEGER DEFAULT 0,        -- 1: Public; 0: Owner + Collaborators
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- 4. Projects Table (Multi-Project Workspaces)
CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,                -- 'agent_name:project_name'
    agent_name TEXT NOT NULL,
    project_name TEXT NOT NULL,
    project_path TEXT NOT NULL,         -- Actual codebase directory
    created_at INTEGER NOT NULL,
    FOREIGN KEY(agent_name) REFERENCES agents(name) ON DELETE CASCADE
);

-- 5. Sessions & Conversation Mapping (Supports Group Topics)
CREATE TABLE IF NOT EXISTS sessions (
    session_key TEXT PRIMARY KEY,       -- Format: "telegram:chat_id[:thread_id]"
    active_agent TEXT NOT NULL,
    active_project TEXT,                -- NULL if in Global Mode
    global_conversation_id TEXT,        -- Conv ID for Global Chat
    project_conversation_id TEXT,       -- Conv ID for Active Project
    updated_at INTEGER NOT NULL,
    FOREIGN KEY(active_agent) REFERENCES agents(name)
);

-- 6. Audit Log & Token Metrics Table
CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_key TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    project_name TEXT,
    conversation_id TEXT,
    prompt_length INTEGER,
    response_length INTEGER,
    duration_seconds REAL,
    input_tokens INTEGER,
    output_tokens INTEGER,
    thinking_tokens INTEGER,
    total_tokens INTEGER,
    status TEXT,                        -- 'SUCCESS' | 'ERROR'
    error_message TEXT,
    created_at INTEGER NOT NULL
);
```

---

## 2. Interactive CLI Setup Wizard (`agyent init`)

When deploying `agyent` for the first time on a VPS or local workstation, run:

```bash
agyent init
```

### Interactive CLI Wizard Flow:

```text
? Welcome to the agyent Setup Wizard! 🚀
? Enter your Telegram Bot Token (from @BotFather): ******************************************
? Enter your Telegram Admin User ID (use @userinfobot to find it): 123456789
? Path to Agent Workspaces directory [default: ~/.agyent/agents]: [Enter]
? Path to agy CLI binary [default: agy]: [Enter]
? Message debounce window in seconds [default: 2.0]: [Enter]
? Enable Real-Time Streaming mode? (Y/n) [default: Y]: [Enter]
? Would you like to create a default agent (dev_expert)? (Y/n): Y

✅ Telegram Bot Token verified: VALID (@my_personal_agy_bot)
✅ AGY CLI binary verified: FOUND (Antigravity CLI v2.0)
✅ SQLite database initialized (WAL Mode) at: ~/.agyent/agyent.db
✅ Configuration file created at: ~/.agyent/config.yaml

🎉 Initialization complete! You can start the gateway with:
   agyent run
```

---

## 3. Configuration File Reference (`~/.agyent/config.yaml`)

```yaml
server:
  host: "127.0.0.1"
  port: 8080

telegram:
  bot_token: "YOUR_TELEGRAM_BOT_TOKEN"
  mode: "polling"              # "polling" or "webhook"
  webhook_url: ""              # Used when mode = "webhook"
  admin_user_ids:
    - 123456789
  allowed_group_ids: []

agy:
  binary_path: "agy"           # Or absolute path "C:\\...\\agy.exe"
  default_timeout_seconds: 300
  default_effort: "high"       # "low" | "medium" | "high"
  default_mode: "accept-edits" # "accept-edits" | "plan"
  dangerously_skip_permissions: true
  streaming_enabled: true      # Toggle Real-Time Streaming mode (NDJSON)
  streaming_throttle_interval_seconds: 1.5 # Telegram message edit throttle interval

storage:
  db_path: "~/.agyent/agyent.db"
  agents_dir: "~/.agyent/agents" # Root directory containing agent workspaces
  debounce_seconds: 2.0
  heartbeat_interval_seconds: 4.0

# Declarative Per-Agent Profiles & Security Presets
agents:
  dev_admin:
    security_preset: "unrestricted" # Full access for trusted dev admin
    default_model: "gemini-2.5-pro"
  auditor:
    security_preset: "strict"       # Whitelist-only for security auditor
    default_model: "gemini-2.5-flash"
  researcher:
    security_preset: "read_only"    # Read-only code exploration
```

---

## 4. Workspace Directory Structure Conventions

To guarantee complete isolation between agents:
- **Default Agent (`agyent`):** Workspace located at `~/.agyent/agents/agyent`.
- **Custom Agents (`<name>`):** Workspaces located at `~/.agyent/agents/<name>`.
- **Subprocess Isolation:** Every execution with the `agy` CLI automatically passes `--project outside-of-project --add-dir <workspace>` to ensure 100% isolation from the host system's `default-cli-project`.
