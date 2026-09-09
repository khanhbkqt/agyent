---
name: agyent-architecture
description: Comprehensive architectural guide for agyent gateway daemon, including Channel Ingress RBAC, Two-Layer Security (Preset vs Scope), Ports & Adapters boundaries, and agent execution scope.
---

# Agyent architecture and operating scope

This document defines the architectural model, security boundaries, and operating
scope of the `agyent` personal-assistant gateway daemon. It guides agents and
developers on how agyent components interact, where security boundaries lie, and
the strict limits of agent execution authority.

## 1. System topology and hexagonal boundaries

`agyent` is organized around Ports & Adapters (Hexagonal Architecture):

- **Domain Layer** (`internal/core/domain/agent.go`): Contains core domain models
  such as `Agent`, `Session`, `Turn`, and `CanonicalMessage`. Zero outward
  dependencies on adapters, CLI, or third-party infrastructure.
- **Ports Layer** (`internal/core/ports/channel.go`, `internal/core/ports/policy.go`,
  `internal/core/ports/execution.go`, `internal/core/ports/storage.go`): Core-owned
  interfaces specifying ingress, egress, persistence, and authorization contracts.
- **Application Core** (`internal/core/engine/engine.go`, `internal/core/auth/policy.go`,
  `internal/core/execution/service.go`, `internal/core/concurrency/lock_manager.go`):
  Coordinates message debouncing, session serialization, prompt assembly, and
  fail-closed authorization.
- **Adapters Layer**:
  - Channels: Telegram (`internal/adapters/channels/telegram/filter.go`) and Zalo
    (`internal/adapters/channels/zalo/router.go`).
  - Security & Guardrails: Gateway manager (`internal/adapters/security/manager.go`)
    and virtual path jail (`internal/adapters/security/pathjail/pathjail.go`).
  - Storage: SQLite repository (`internal/adapters/storage/sqlite/agent_repo.go`).
- **Composition Root** (`cmd/agyent/run.go`): CLI wiring, dependency injection, and
  daemon lifecycle management.

### Primary Runtime Flow

```text
Channel Ingress (Telegram / Zalo)
  -> Admission & Channel RBAC Filter
  -> Message Debouncer
  -> Core Engine & FIFO Session Lock
  -> Context & Persona Resolution
  -> Auth Policy Engine (Fail-Closed)
  -> Execution Service (Chokepoint)
  -> AGY Harness Process Tree
  -> PreToolUse Security IPC Hooks
  -> EventBus & Streaming Throttler
  -> Channel Delivery
```

## 2. Channel ingress and RBAC model

Agyent enforces role-based access control (RBAC) at channel ingress before any
message reaches the execution engine:

### Group Chat Whitelisting
- Ingress requires the group ID to be declared in `allowed_group_ids` (e.g.
  `zalo.allowed_group_ids` or `telegram.allowed_group_ids`).
- **Open Group Interaction**: Once a group is whitelisted, all group members may
  interact with the agent for standard conversation and project tasks.
- **Administrative Restriction**: Sensitive slash commands (`/security`, `/config`,
  `/whitelist`, `/a delete`) are restricted exclusively to designated Admins
  (`admin_user_ids`) and the Agent Owner (`owner_id`).

### Direct Messages (1-1 DM)
- If an agent is marked Private (`is_public: false`), direct messages from
  unauthenticated or unrecognized users are dropped at the channel ingress filter.
- Only authenticated Admins and the Agent Owner can initiate 1-1 private sessions
  with private agents.

### Persona Resolution
- When a single custom agent is configured, incoming messages automatically bind
  to that persona as the default agent without requiring manual `/a use` commands.

## 3. Two-layer security architecture

Agyent separates shell command execution permissions from filesystem access
boundaries into two independent, non-overlapping layers:

### Layer 1: Security Presets (Command Guardrails)
- **Controlled Component**: Shell command execution via `run_command` in
  `internal/adapters/security/manager.go`.
- **Presets**: `unrestricted`, `developer`, `workspace_only`, `balanced`,
  `strict`, `read_only`.
- **Policy Enforcement**: Presets determine blacklist matching, dangerous syntax
  filtering, and whether sensitive commands require interactive Human-in-the-Loop
  (HITL) approval.
- **Critical Invariant**: Security presets ONLY control shell command execution
  behavior. Presets NEVER expand or bypass filesystem scope.

### Layer 2: Allowed Paths (Filesystem Scope & Virtual Path Jail)
- **Controlled Component**: Virtual Path Jail in
  `internal/adapters/security/pathjail/pathjail.go`.
- **Enforcement Scope**: Governs all file manipulation tools (`view_file`,
  `write_to_file`, `replace_file_content`, `list_dir`, `grep_search`,
  `find_by_name`) and subprocess working directories (`Cwd`).
- **Strict Boundary**: The agent is strictly confined to:
  1. Primary workspace directory: `workspace_path`.
  2. Explicitly configured external directories: `agents.<name>.allowed_paths`
     in `~/.agyent/config.yaml`.
- **Isolation Guarantee**: Any access outside the allowed paths is unconditionally
  denied with `[Path Jail]`. Switching to the `developer` or `unrestricted` preset
  does NOT permit access outside the allowed paths.

## 4. Agent execution scope and operating boundaries

Agents executing inside the agyent harness must understand their operational
boundaries:

### What the Agent CAN Do Autonomously
- Inspect, read, write, and search files within its workspace directory and any
  explicit `allowed_paths`.
- Execute shell commands permitted by its active security preset.
- Autonomously query runtime diagnostics using read-only SQL on the `audit_logs`
  table in `~/.agyent/agyent.db` when investigating turn errors or performance.
- Schedule delayed reminders, recurring cron jobs, and workspace heartbeats
  (`HEARTBEAT.md`) via scheduler tools.

### What is OUT OF SCOPE for the Agent
- **Cannot Edit Host Configuration**: The agent cannot write to or alter
  `~/.agyent/config.yaml` because host configuration resides outside its
  filesystem scope. Path Jail blocks any modification attempt.
- **Cannot Mutate System DB**: The agent cannot alter SQLite system schemas or
  directly update control-plane tables in `~/.agyent/agyent.db`.
- **Cannot Self-Elevate Scope**: The agent cannot add directories to its own
  allowed paths or unilaterally weaken security presets.
- **Cannot Force Unlock**: The agent cannot circumvent FIFO session locks.

### Remediating Interceptions and Denials
When a tool call is blocked, the agent must diagnose the exact trigger and guide
the human owner rather than hallucinating unauthorized capabilities:

- **When Blocked by `[Path Jail]`**:
  - Explain clearly that the requested file path or `Cwd` is outside the agent's
    permitted filesystem scope.
  - Clarify that changing security presets (e.g. `/security preset developer`)
    will not grant filesystem access.
  - Instruct the owner to add the required directory to `allowed_paths` in
    `~/.agyent/config.yaml`:
    ```yaml
    agents:
      <agent_name>:
        allowed_paths:
          - "/path/to/required/directory"
    ```
- **When Blocked by `[Command Guardrail]`**:
  - State the intercepted command and the triggered security rule.
  - Instruct the owner to approve via the interactive chat button, or use:
    - `/security grant <command>` for a one-time execution approval.
    - `/whitelist add "<command>"` for permanent command whitelisting.
    - `/security preset <preset>` to change command execution posture.

## 5. Storage, concurrency, and lifecycle rules

- **CGO-Free SQLite**: Zero CGO via `modernc.org/sqlite`. Preserves WAL mode,
  `busy_timeout(5000)`, and foreign keys.
- **Dual-Pool Design**: Exactly 1 dedicated writer connection and up to 20 reader
  connections (`internal/adapters/storage/sqlite/agent_repo.go`).
- **Timestamps**: All database timestamps are persisted as Unix milliseconds
  and scanned using `FlexTime`.
- **FIFO Session Serialization**: Active turns are serialized per session key via
  `internal/core/concurrency/lock_manager.go`. Network I/O and process waits
  must not hold the session lock longer than necessary.
- **Process Group Isolation**: Subprocesses launched by the execution service
  run with `--project outside-of-project` and pass `--add-dir` only for the
  resolved workspace directory. POSIX process groups or Windows Job Objects
  ensure clean process termination.
