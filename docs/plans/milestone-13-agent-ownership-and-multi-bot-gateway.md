# Milestone 13: Multi-Bot Lifecycle Management & Agent Ownership (Granular RBAC)

Comprehensive technical specification and implementation plan for **Milestone 13: Multi-Bot Lifecycle Pools, Dedicated Agent Binding, Per-Agent Ownership, Granular RBAC, and Namespaced Session Keys** of the **`agyent`** system.

---

## 1. Executive Summary & Core Objectives

- **Multi-Bot Gateway:** Run and manage multiple Telegram bots concurrently within a single `agyent` daemon process with fault isolation (one crashing bot does not affect others).
- **Dedicated Agent Binding (1 Bot $\leftrightarrow$ 1 Agent):** Bind specific bot tokens to dedicated agent personas (e.g., Bot 1 $\to$ `@dev_architect`, Bot 2 $\to$ `@personal_assistant`), with auto-initialization and access locking.
- **Granular Agent Ownership (RBAC):** Every agent has a verified `OwnerID` (Telegram User ID of creator) and access control table (`agent_permissions`), isolating workspaces (`SOUL.md`, `IDENTITY.md`, `USER.md`, `MEMORY.md`).
- **Session Key Disambiguation:** Upgrade session keys to `channel:bot_id:chat_id[:thread_id]` to prevent cross-bot session clobbering and race conditions, with seamless fallback for legacy 2/3-part keys.
- **Hexagonal Architecture Compliance:** Centralize all ACL and whitelist verification in Core Engine (`internal/core/engine/`) rather than hardcoding inside channel adapters.
- **Zero-Downtime Migration:** Schema migration `000007_agent_ownership_and_acl.up.sql` with automatic data backfill (`is_public = 1` for existing agents and `agyent`).

---

## 2. End-to-End Multi-Bot & Ownership Architecture Diagram

```mermaid
flowchart TB
    subgraph Channels ["1. MESSAGING SURFACE (Multi-Bot)"]
        BotA["🤖 Bot A: @dev_bot (dev_architect)"]
        BotB["🤖 Bot B: @assistant_bot (personal_assistant)"]
        BotC["🤖 Bot C: @shared_hub (Shared Router)"]
    end

    subgraph ChannelPool ["2. FAULT-ISOLATED BOT POOL (internal/adapters/channels/telegram/)"]
        PoolMgr["Bot Pool Manager (map[int64]*gotgbot.Bot)"]
        Normalizer["SessionKey Normalizer\n(channel:bot_id:chat_id:thread_id)"]
        Throttler["Delivery Throttler (1.5s) & Outbound Multiplexer"]
    end

    subgraph CoreEngine ["3. CORE ENGINE ORCHESTRATOR & RBAC GATE (internal/core/engine/)"]
        InboundQueue["Inbound Canonical Queue"]
        Debouncer["2.0s Debouncer"]
        LockMgr["SessionLockManager (FIFO Mutex)"]
        RBACGate{"[Inbound RBAC Checkpoint]\nCheckAccess(agent, sender_id)"}
        CommandRouter["Slash Commands Router\n(/a new, /use, /a share, /a revoke, /a info)"]
        TurnExecutor["Turn Execution & Prompt Assembler"]
    end

    subgraph SubprocessHarness ["4. SUBPROCESS HARNESS & WORKSPACE JAIL"]
        Harness["AGY Subprocess Controller (stream-json)"]
        JobGuard["Windows Job Object / POSIX Process Group"]
        WS_A["📂 Workspace: workspace-dev_architect/\n(Owner: User 1001 • Identity & Memory)"]
        WS_B["📂 Workspace: workspace-personal_assistant/\n(Owner: User 2002 • Identity & Memory)"]
    end

    subgraph StorageTier ["5. STORAGE & PERSISTENCE TIER"]
        SQLiteDB[("Pure-Go SQLite: agyent.db\n(agents, agent_permissions, sessions, audit_logs)")]
    end

    Channels --> ChannelPool
    ChannelPool --> Normalizer --> InboundQueue --> Debouncer --> LockMgr --> RBACGate
    
    RBACGate -->|Authorized| CommandRouter
    RBACGate -->|Authorized| TurnExecutor
    RBACGate -->|Denied (403)| Throttler
    
    CommandRouter <--> StorageTier
    TurnExecutor <--> StorageTier
    TurnExecutor --> Harness
    Harness --- JobGuard
    Harness <--> WS_A
    Harness <--> WS_B
    Harness --> Throttler --> ChannelPool --> Channels
```

---

## 3. Implementation Phases & Step-by-Step Breakdown

### Phase 1: Core Domain Models & Namespaced SessionKey
- [ ] **1.1.** Extend `domain.Agent` in `internal/core/domain/agent.go` with `OwnerID string` and `IsPublic bool`.
- [ ] **1.2.** Define `domain.AgentPermission` struct with `AgentName`, `UserID`, `Role` (`admin`, `operator`, `viewer`), `GrantedBy`, `GrantedAt`.
- [ ] **1.3.** Update `FormatSessionKey` in `internal/core/domain/session.go` to support optional `botID` parameter (`channel:botID:chatID[:threadID]`).
- [ ] **1.4.** Add `ExtractChatIDFromSessionKey` with fallback logic for legacy 2/3-part and new 4-part session keys.
- [ ] **1.5.** Add comprehensive unit tests in `internal/core/domain/domain_test.go`.

### Phase 2: Database Schema Migration & Storage Repositories
- [ ] **2.1.** Create migration `000007_agent_ownership_and_acl.up.sql`:
  - `ALTER TABLE agents ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';`
  - `ALTER TABLE agents ADD COLUMN is_public INTEGER NOT NULL DEFAULT 0;`
  - `UPDATE agents SET is_public = 1 WHERE is_public = 0;` (Data backfill).
  - `CREATE TABLE agent_permissions (...);`
- [ ] **2.2.** Create rollback migration `000007_agent_ownership_and_acl.down.sql`.
- [ ] **2.3.** Extend `ports.AgentRepository` in `internal/core/ports/storage.go` with RBAC methods (`ShareAgent`, `RevokeAgentAccess`, `ListAgentPermissions`, `CheckAgentAccess`, `ListAgentsForUser`).
- [ ] **2.4.** Implement RBAC methods in `internal/adapters/storage/sqlite/agent_repo.go`.
- [ ] **2.5.** Add unit and integration tests in `internal/adapters/storage/sqlite/agent_repo_test.go`.

### Phase 3: Configuration & Multi-Bot Channel Support
- [ ] **3.1.** Extend `config.TelegramConfig` in `internal/config/config.go` with `Bots []BotConfig` and `GetNormalizedBots()` helper.
- [ ] **3.2.** Update `internal/adapters/channels/telegram/router.go`:
  - Pass `bot.Id` and `bot.Username` in `domain.CanonicalMessage`.
  - Use `FormatSessionKey("telegram", chatID, threadID, bot.Id)` for session key generation.
  - Remove hardcoded `IsUserAdmin` from router (delegating to Core Engine).
- [ ] **3.3.** Update `internal/adapters/channels/telegram/adapter.go`:
  - Manage bot pool `map[int64]*gotgbot.Bot`.
  - Start independent polling goroutines with panic recovery per bot.
  - Support `BindAgent` logic.
  - Route outbound messages via originating `msg.BotID`.
- [ ] **3.4.** Add tests in `internal/config/config_test.go` and `internal/adapters/channels/telegram/adapter_test.go`.

### Phase 4: Core Engine RBAC Middleware, Genesis Bootstrap & Slash Commands
- [ ] **4.1.** Implement `CheckAccess(ctx, agent, senderID)` in `internal/core/engine/engine.go`.
- [ ] **4.2.** Enforce RBAC in `executeTurn`: reject unauthorized interactions with friendly 403 response.
- [ ] **4.3.** Update Genesis Bootstrap prompt in `internal/core/engine/bootstrap.go` to bind `USER.md` specifically to the agent's verified `OwnerID`.
- [ ] **4.4.** Update slash commands in `internal/core/engine/commands.go`:
  - `/a new <name> [desc]`: Set `OwnerID = msg.Sender.ID` and `IsPublic = false`.
  - `/use <name>`: Enforce `CheckAccess`.
  - `/agents`: Filter list using `ListAgentsForUser`.
  - Add `/a share <agent> <user_id> [role]`.
  - Add `/a revoke <agent> <user_id>`.
  - Add `/a info <agent>`.
  - `/bootstrap`: Enforce Owner / SuperAdmin requirement.
- [ ] **4.5.** Add tests in `internal/core/engine/engine_test.go` and `internal/core/engine/commands_test.go`.

---

## 4. Testing Matrix & Quality Gates

| Test Category | Target File | Verification Scope |
| :--- | :--- | :--- |
| **Domain & SessionKey** | `domain_test.go` | Session key namespacing, legacy fallback extraction, struct serialization. |
| **Database Migrations** | `sqlite_test.go` | Schema up/down idempotency, foreign key cascades, data backfill verification. |
| **Storage RBAC Repos** | `agent_repo_test.go` | `ShareAgent`, `RevokeAgentAccess`, `CheckAgentAccess`, `ListAgentsForUser`. |
| **Multi-Bot Config** | `config_test.go` | Legacy single-bot vs Multi-bot YAML parsing and normalization. |
| **Engine RBAC Gate** | `engine_test.go` | Public vs Private agents, Owner vs Shared vs Unauthorized user turns. |
| **Slash Commands** | `commands_test.go` | `/a new`, `/use`, `/a share`, `/a revoke`, `/a info`, `/agents` filter. |
| **Concurrency & Race** | Full repo | `go test -v -race ./...` with zero race conditions. |

---

## 5. Definition of Done (DoD)

- [ ] All 4 implementation phases completed.
- [ ] Database migration 000007 runs cleanly and passes automated rollback tests.
- [ ] 100% of unit, integration, and concurrency tests pass (`go test -race ./...`).
- [ ] Multi-bot setup verified with independent bot tokens.
- [ ] Agent ownership isolation verified between different Telegram users.
- [ ] Master roadmap updated with Milestone 13 completion status.
