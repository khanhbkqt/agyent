# Milestone 2: Pure-Go SQLite Storage Engine & Concurrency Locks

> **Technical Specification & Evidence Dashboard**  
> **Milestone ID:** 2  
> **Milestone Name:** Storage & Sessions  
> **Status:** Completed & Verified (100%)  

---

## 1. Milestone 2 Objectives

1. **Pure-Go SQLite Engine (`modernc.org/sqlite`):**
   - Integrate 100% Pure-Go SQLite driver without CGO/gcc dependencies, enabling static cross-compilation across Windows, Linux (x86_64, ARM64), and macOS.
   - Configure optimal anti-lock PRAGMAs (`busy_timeout(5000)`, `journal_mode(WAL)`, `synchronous(NORMAL)`, `foreign_keys(1)`, `temp_store(MEMORY)`, `cache_size(-8000)`).
   - Establish a concurrency-safe Connection Pool for SQLite WAL (`MaxOpenConns(1)`, `MaxIdleConns(1)`).

2. **Embedded Transactional Database Migrations (`embed.FS`):**
   - Manage schema versions using the `schema_migrations` tracking table.
   - Embed SQL DDL files directly into the Go binary using `embed.FS`.
   - Execute each migration within an isolated database transaction, ensuring idempotency and safe daemon upgrades.

3. **Complete Database Schema & Multi-Project Context Isolation:**
   - Standardize all timestamps to **Unix Milliseconds (`int64`, `time.Now().UnixMilli()`)**.
   - Add `session_project_conversations` table to eliminate `project_conversation_id` overwrites when switching across projects (`/p ecommerce` $\leftrightarrow$ `/p scraper`).
   - Create indexes on critical query paths (`agent_name`, `active_agent`, `session_key`, `created_at`).
   - Enforce data integrity constraints: `ON DELETE CASCADE` for Projects/Conversations and `ON DELETE RESTRICT` for Agents with active sessions.

4. **Comprehensive Repository Implementations (CRUD & Domain Contracts):**
   - `SessionRepository`: Session management, dual-scope conversation context.
   - `AgentRepository`: Agent profiles and bootstrap lifecycle states (`uninitialized`/`initialized`).
   - `ProjectRepository`: Project metadata and filesystem paths.
   - `UserRepository`: User whitelist and role authorization (`IsUserAllowed`).
   - `GroupRepository`: Telegram group authorization (`IsGroupAllowed`).
   - `AuditRepository`: Execution logging, latency tracking, and token usage metrics.
   - `StoragePort`: Composed interface combining all repositories with `Close() error`.

5. **Memory-Leak-Proof Session Mutexes (`SessionLockManager` / `KeyedMutex`):**
   - Implement reference-counted mutexes keyed by `SessionKey`.
   - Automatically clean up entries from memory map when `refCount == 0`, preventing memory leaks over long-running daemons.
   - Support Context Cancellation, Watchdog Timeout (`ErrLockTimeout`), and Double Unlock safety.

6. **Standardized Error Models & Sentinel Errors:**
   - Define canonical domain errors: `ErrNotFound`, `ErrSessionNotFound`, `ErrAgentNotFound`, `ErrProjectNotFound`, `ErrUserNotFound`, `ErrGroupNotFound`, `ErrAlreadyExists`, `ErrInvalidStateTransition`.
   - Standardize `errors.Is(err, ports.ErrNotFound)` checks across all callers.

7. **Comprehensive Test Suite & Coverage Threshold ($\ge 90\%$):**
   - 100% of tests run in isolated SQLite in-memory sandboxes via `t.TempDir()`.
   - Concurrency stress testing with 100 simultaneous goroutines under `-race` (2,000 operations), proving zero `SQLITE_BUSY` errors and zero data races.
   - Security verification against SQL injection, supporting Vietnamese Unicode diacritics and emojis.

---

## 2. Review & Sign-Off Dashboard

| Review Role | Status | Key Technical Requirements |
| :--- | :--- | :--- |
| 🛡️ **Principal Architect / Tech Lead** | **APPROVED WITH ENRICHMENTS** ✅ | - **Fix Multi-Project Bug:** Created `session_project_conversations` table to store distinct `conversation_id` records per `(session_key, project_id)`.<br>- **KeyedMutex Memory Leak:** Implemented reference-counted mutexes instead of unbounded storage in `sync.Map`.<br>- **Pragma DSN:** Enforced complete PRAGMAs in DSN string to ensure all connections enable Foreign Keys and WAL Mode.<br>- **Timestamp Resolution:** Standardized to Unix Milliseconds (`int64`). |
| 🧪 **QA/QC Lead** | **APPROVED WITH FULL TEST MATRIX** ✅ | - **6-Category Test Matrix:** Added `TC-SESS-01..07`, `TC-AGT-01..05`, `TC-PRJ-01..04`, `TC-USR-01..05`, `TC-AUD-01..03`.<br>- **Security & Injection Immunity:** Verified parameterized queries against `' OR '1'='1'`, stacked `DROP TABLE`, and `UNION SELECT` payloads.<br>- **Lock Manager Stress & Leak Assert:** 50 goroutines testing mutual exclusion, asserting `ActiveLockCount() == 0`, timeout watchdog, and double unlock safety.<br>- **High-Concurrency Stress:** 100 workers executing 2,000 simultaneous operations under `-race`. |
| 🧠 **Domain Expert** | **APPROVED** ✅ | - **Dual-Scope Conversation Resolution:** Ensured `GetActiveConversationID()` prioritizes `session_project_conversations` when `active_project != ""` and falls back to `global_conversation_id` in Global Mode.<br>- **Idempotent Migration:** Automated migrations run cleanly during gateway startup or `agyent init` without failing on existing tables. |

---

## 3. Architecture & Code Specification

### 3.1 Standard Directory Structure Layout

```
internal/
├── core/
│   ├── domain/
│   │   ├── session.go            <-- Dual-scope helper updates
│   │   └── ...
│   └── ports/
│       └── storage.go            <-- Sentinel errors & composed interface
└── adapters/
    └── storage/
        └── sqlite/
            ├── sqlite.go         <-- Connection Manager, DSN Builder, Migrate, SQLiteStore
            ├── sqlite_test.go    <-- In-memory CRUD, Cascade & Concurrency Stress Tests
            ├── lock_manager.go   <-- Ref-counted KeyedMutex with Watchdog Timeout
            ├── lock_manager_test.go <-- Stress & Leak tests for Lock Manager
            ├── session_repo.go   <-- SessionRepository & Multi-Project Context
            ├── agent_repo.go     <-- AgentRepository implementation
            ├── project_repo.go   <-- ProjectRepository implementation
            ├── user_repo.go      <-- UserRepository & GroupRepository implementation
            ├── audit_repo.go     <-- AuditRepository implementation
            ├── helpers.go        <-- Scanner helpers for NullString, Millisecond conversions
            └── migrations/       <-- Embedded SQL DDL scripts
                ├── 000001_init_schema.up.sql
                └── 000001_init_schema.down.sql
```

---

### 3.2 Complete Database Schema (`000001_init_schema.up.sql`)

```sql
-- 1. Users & Whitelist Table
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,                          -- Telegram User ID (e.g. "123456789")
    username TEXT,                                 -- @username
    full_name TEXT,
    role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'developer', 'guest')),
    created_at INTEGER NOT NULL                   -- Unix Milliseconds
);

-- 2. Allowed Groups Table
CREATE TABLE IF NOT EXISTS allowed_groups (
    group_id TEXT PRIMARY KEY,                     -- Telegram Chat ID (e.g. "-100123456789")
    group_title TEXT,
    is_active INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at INTEGER NOT NULL                   -- Unix Milliseconds
);

-- 3. Agent Profiles Table
CREATE TABLE IF NOT EXISTS agents (
    name TEXT PRIMARY KEY,                         -- 'dev_expert', 'personal_assistant'
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'uninitialized' CHECK (status IN ('uninitialized', 'initialized')),
    workspace_path TEXT NOT NULL,                  -- Absolute path to agent universe
    created_at INTEGER NOT NULL,                  -- Unix Milliseconds
    updated_at INTEGER NOT NULL                   -- Unix Milliseconds
);

-- 4. Projects Table (Multi-Project Workspaces)
CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,                           -- Composite ID: 'agent_name:project_name'
    agent_name TEXT NOT NULL,
    project_name TEXT NOT NULL,
    project_path TEXT NOT NULL,                    -- Absolute filesystem path
    created_at INTEGER NOT NULL,                  -- Unix Milliseconds
    FOREIGN KEY(agent_name) REFERENCES agents(name) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_projects_agent_name ON projects(agent_name);

-- 5. Sessions Table
CREATE TABLE IF NOT EXISTS sessions (
    session_key TEXT PRIMARY KEY,                  -- Format: "telegram:chat_id[:thread_id]"
    active_agent TEXT NOT NULL,
    active_project TEXT NOT NULL DEFAULT '',       -- Empty string = Global Mode
    global_conversation_id TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,                  -- Unix Milliseconds
    FOREIGN KEY(active_agent) REFERENCES agents(name) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_sessions_active_agent ON sessions(active_agent);

-- 6. Multi-Project Conversation Isolation Table
CREATE TABLE IF NOT EXISTS session_project_conversations (
    session_key TEXT NOT NULL,
    project_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,                  -- Unix Milliseconds
    PRIMARY KEY(session_key, project_id),
    FOREIGN KEY(session_key) REFERENCES sessions(session_key) ON DELETE CASCADE,
    FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- 7. Audit Log & Token Metrics Table
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
```

---

## 4. Verification Plan & QA Testing Matrices

### 4.1 Coverage Allocation Targets

```
+---------------------------------------------------------------------------------------------------------+
| Package / Component                      | Target Coverage | Mandatory Edge Cases Tested                |
+------------------------------------------+-----------------+--------------------------------------------+
| internal/adapters/storage/sqlite         | >= 95%          | DSN builder, WAL Pragmas, Tx rollback      |
| sqlite/session_repo.go                   | >= 95%          | Dual-Scope, GetOrCreate, Multi-project Conv|
| sqlite/agent_repo.go                     | >= 92%          | Status transitions, List, Delete Restrict  |
| sqlite/project_repo.go                   | >= 92%          | Composite IDs, List by Agent, Cascades     |
| sqlite/user_repo.go                      | >= 92%          | Roles, Whitelists (User & Group IsAllowed) |
| sqlite/audit_repo.go                     | >= 90%          | Token usages, Error messages, Pagination   |
| sqlite/lock_manager.go                   | 100%            | Concurrency, Timeouts, Leaks, Double Unlock|
| sqlite/helpers.go                        | 100%            | NullString, NullInt64, Unix Milliseconds   |
+---------------------------------------------------------------------------------------------------------+
| TOTAL MILESTONE 2 STORAGE ENGINE COVERAGE| >= 93%          | Comprehensive QA Sign-Off Baseline         |
+---------------------------------------------------------------------------------------------------------+
```

### 4.2 Detailed Test Matrices (Test Cases)

```
+---------------------------------------------------------------------------------------------------------+
|                                      STORAGE & CONCURRENCY TEST MATRIX                                  |
+--------------------------+----------------------------------------------------+-------------------------+
| Test Case Category       | Test Scenario & Assertion Details                  | Target Verification     |
+--------------------------+----------------------------------------------------+-------------------------+
| 1. CRUD & Sentinel Errs  | - TC-SESS-01..07: Get non-existent -> ErrNotFound; | Return ports.ErrNotFound|
|                          |   GetOrCreateSession; Save & Update; Project Conv  | errors.Is() assertions  |
|                          |   isolation (Set/Get/Clear); Delete Cascade.       |                         |
|                          | - TC-AGT-01..05: Save/Get/List; Status transition; | Agent lifecycle         |
|                          |   CHECK constraint invariant.                      |                         |
|                          | - TC-PRJ-01..04: Save/Get; List by agent filter;   | Project isolation       |
|                          |   Delete with cascade.                             |                         |
|                          | - TC-USR-01..05: User/Group CRUD; IsUserAllowed;   | Whitelist authorization |
|                          |   IsGroupAllowed (active vs inactive).             |                         |
|                          | - TC-AUD-01..03: Log audit; Token metrics; List    | Metrics & Log tracking  |
|                          |   audit with pagination & desc ordering.           |                         |
+--------------------------+----------------------------------------------------+-------------------------+
| 2. FK Enforcement &      | - Insert orphan project -> constraint error.       | SQLite constraint error |
|    Cascade Deletions     | - Delete agent with active session -> RESTRICT.    | RESTRICT violation      |
|                          | - Delete agent with projects -> CASCADE deletion.  | CASCADE cleanup         |
+--------------------------+----------------------------------------------------+-------------------------+
| 3. Security & Unicode    | - SQL Injection payloads in all text fields.       | 0 Syntax/Injection bugs |
|                          | - Vietnamese diacritics, Emojis, Windows paths.    | UTF-8 exact match       |
+--------------------------+----------------------------------------------------+-------------------------+
| 4. High-Concurrency      | - 100 concurrent workers (2,000 mixed CRUD ops)    | 0 SQLITE_BUSY errors    |
|    Stress Test (-race)   |   under go test -race.                             | 0 Data races            |
+--------------------------+----------------------------------------------------+-------------------------+
| 5. SessionLockManager    | - 50 concurrent goroutines locking same key.       | FIFO serialization      |
|    Exhaustive Suite      | - Assert ActiveLockCount() == 0 upon finish.       | 0 Memory leaks          |
|                          | - Watchdog timeout triggering (ErrLockTimeout).    | Timeout watchdog        |
|                          | - Context cancel unblocking & Double unlock test.  | Panic & deadlock free   |
+--------------------------+----------------------------------------------------+-------------------------+
| 6. Migration Runner      | - Run Migrate() on empty DB -> tables created.     | Clean init              |
|    Idempotency & Rollback| - Run Migrate() second time -> no-op, no error.    | Idempotent execution    |
|                          | - Malformed DDL in transaction -> complete rollback| Zero partial state      |
+--------------------------+----------------------------------------------------+-------------------------+
```

---

## 5. Milestone Completion Status
- **Status:** 100% Completed & Verified.
