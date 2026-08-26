# AGENTS.md - Repository Guide & Agent Engineering Directives

Welcome to **agyent** (`agy-agent`). This document serves as the primary entry point and architectural blueprint for AI agents and human contributors developing or extending this repository.

---

## 1. Project Overview & Mission

**`agyent`** is a high-performance, single-binary Personal AI Assistant Gateway and Multi-Agent Harness written in **Go (Golang)**. It connects real-time communication channels (Telegram Bot, Topics, Groups, Discord) directly to the **Antigravity CLI (`agy`)** running locally on a user's workstation or VPS.

### Core Value Propositions:
- **Zero-CGO & Single Static Binary:** Extreme resource efficiency (<10MB RAM at idle), instant cold starts, and zero native library dependencies.
- **Autonomous Tool-Calling Brain:** Harnesses the full power of Antigravity CLI (`agy`) for terminal commands, file modifications, web browsing, subagents, and Model Context Protocol (MCP) servers.
- **Prefix KV-Cache Optimization:** Strict prompt ordering (Levels 0–4) enabling 85–95% cache hit rates on Gemini models (0.25x Cache Read pricing).
- **Self-Learning & Continuous Evolution:** Background reflection engine that autonomously records lessons, user preferences, and Architectural Decision Records (ADRs) into persistent memory.

---

## 2. System Architecture (Hexagonal / Ports & Adapters)

The codebase strictly adheres to Hexagonal Architecture (Clean Ports and Adapters) to ensure complete testability and modular extensibility:

```
                  +-----------------------------------+
                  |        CLI Entrypoint             |
                  |         (cmd/agyent/)             |
                  +-----------------+-----------------+
                                    |
+-----------------------------------v-----------------------------------+
|                     internal/core/engine/                             |
|          Central Orchestration, Slash Commands, Lifecycle GC          |
|                                                                       |
|  +--------------------+  +--------------------+  +-----------------+  |
|  |  Debouncer/Queue   |  |   Lock Manager     |  |   EventBus      |  |
|  | (core/debouncer/)  |  | (core/concurrency/)|  | (core/eventbus/)|  |
|  +--------------------+  +--------------------+  +-----------------+  |
+-----------------------------------+-----------------------------------+
                                    |
          +-------------------------+-------------------------+
          | Implements Core Interfaces (internal/core/ports/) |
          v                                                   v
+-------------------+ +--------------------+ +--------------------+ +--------------------+
| internal/adapters | | internal/adapters  | | internal/adapters  | | internal/adapters  |
| /channels/telegram| |   /harness/agy/    | |  /storage/sqlite/  | | /evolution/        |
|  Telegram Bot API | | AGY Subprocess JSON| | Pure-Go SQLite WAL | | Self-Reflection &  |
|  Router & Throttle| | Stream & Watchdog  | | Dual-Pool Engine   | | 4D Conflict Merge  |
+-------------------+ +--------------------+ +--------------------+ +--------------------+
```

---

## 3. Directory & Module Map

```
agyent/
├── cmd/agyent/               # Main CLI executable entrypoints
│   ├── main.go               # Root entrypoint
│   ├── root.go               # Cobra root command setup & global flags
│   ├── init.go               # 'agyent init' interactive setup wizard
│   ├── run.go                # 'agyent run' gateway daemon runner
│   ├── register_commands.go  # 'agyent register-commands' Telegram command sync
│   └── version.go            # 'agyent version' build metadata
│
├── internal/
│   ├── config/               # Configuration structs, validation & YAML persistence
│   ├── logger/               # Structured logging setup via Go 'log/slog'
│   ├── wizard/               # Interactive terminal wizard (charmbracelet/huh)
│   │
│   ├── core/                 # Core Domain & Pure Business Logic (No External Drivers)
│   │   ├── domain/           # Canonical models (Agent, User, Session, Conversation, Audit)
│   │   ├── ports/            # Port interfaces for Storage, Channel, Runner, EventBus, Evolution
│   │   ├── engine/           # Central orchestrator, slash commands, prompt assembly, and GC
│   │   ├── debouncer/        # Sliding window message debouncing & batch coalescing
│   │   ├── concurrency/      # FIFO session mutexes and cross-platform OS FileLocks
│   │   └── eventbus/         # In-memory async event publisher/subscriber
│   │
│   └── adapters/             # Secondary Adapters implementing Ports
│       ├── channels/telegram # Telegram bot router, live throttler, markdown chunker
│       ├── harness/agy/      # Subprocess JSON streaming, watcher, and OS Job Objects
│       ├── storage/sqlite/   # Pure-Go SQLite (modernc.org/sqlite) with WAL & FlexTime
│       │   └── migrations/   # Versioned up/down SQL schema migrations (000001..000004)
│       ├── context/          # 5-Tier context resolver, progressive skills index, temporal tags
│       ├── evolution/        # Reflection engine, heuristic filter, 4D memory conflict resolver
│       ├── mcp/              # Global MCP JSON config syncer and zombie cleanup
│       └── plugin/           # Plugin manifest validation & installation
│
├── builtin/plugins/          # Pre-packaged plug-and-play capability plugins
│   ├── browser-camoufox/     # Camoufox anti-detect web browsing & extraction
│   ├── database-sqlite/      # SQLite database schema inspection & query execution
│   └── system-diagnostics/   # Host health metrics, process inspection & disk I/O
│
├── docs/                     # Comprehensive Architecture & Specification Documentation
│   ├── architecture.md       # 4-Tier system architecture overview
│   ├── extensible-architecture.md # Microkernel, Ports/Adapters & Event Hooks
│   ├── lifecycle-and-bootstrap.md # Genesis Onboarding & Agent Lifecycle
│   ├── message-pipeline.md   # Message routing, debouncing & media sync
│   ├── multi-conversation-architecture.md # Flat conversation model & lifecycle GC
│   ├── multi-project.md      # Dual-scope context & codebase isolation
│   ├── storage-and-config.md # SQLite schema, WAL mode & configuration reference
│   ├── agy-cli-harness.md    # Subprocess execution, snapshot diff & watchdog
│   ├── agy-streaming-protocol.md # Real-time delta streaming specification
│   ├── context-management-architecture.md # 5-Tier context hierarchy & skills
│   ├── model-and-effort-selection-architecture.md # Dynamic Model & Reasoning Effort Selection
│   ├── agent-self-learning-and-evolution-architecture.md # Autonomous reflection & 4D memory
│   ├── multi-account-architecture.md # Multi-Account Profile Virtualization & Auto-Cooldown Failover
│   └── plans/                # Master roadmap & historical milestone execution logs
│
├── scripts/                  # Helper scripts & systemd service units
│   ├── deploy/               # systemd unit files (`agyent.service`)
│   └── benchmark_tokens.py   # Benchmark simulation suite for token & KV-cache metrics
│
├── go.mod / go.sum           # Go module dependencies
├── Makefile                  # Build, test, lint, and packaging shortcuts
└── LICENSE                   # MIT License
```

---

## 4. Key Engineering Invariants & Coding Rules

When contributing or modifying code, agents **must adhere** to the following non-negotiable standards:

### 4.1. Zero-CGO & Pure-Go SQLite
- **Never introduce CGO dependencies.** Always import `modernc.org/sqlite`.
- Always configure SQLite in WAL mode: `_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`.
- Use the `FlexTime` custom scanner in `internal/adapters/storage/sqlite/helpers.go` when scanning timestamps to safely handle both ISO-8601 strings and Unix epoch integers.
- Use the **Dual-Pool SQLite Engine**: `writeDB` with `MaxOpenConns=1` (eliminating `SQLITE_BUSY` concurrency deadlocks) and `readDB` with `MaxOpenConns=20` for high parallel read throughput.

### 4.2. Prefix KV-Cache Preservation
- To ensure optimal performance and ~75% cost savings on Gemini cached models, the prompt hierarchy **must maintain strict prefix ordering**:
  - **Level 0:** Static System Runtime Foundation (`[SYSTEM RUNTIME FOUNDATION]`) at Index 0.
  - **Level 1:** Global Core Directives (`IDENTITY.md`, `SOUL.md`, `USER.md`, `MEMORY.md`, `AGENTS.md`).
  - **Level 2:** Workspace Project Directives (`.agents/AGENTS.md`).
  - **Level 3:** Progressive Skills Index (~60 tokens/skill metadata header).
  - **Level 4:** Temporal Context Marker & Current User Turn.
- Separate initial turns (`ComposeResolvedTurnPrompt`) from continuation turns (`ComposeContinuationPrompt`) to eliminate redundant directive serialization on subsequent turns.

### 4.3. Workspace Isolation & Safe Process Execution
- Subprocess executions must never pollute the product repository. Pass `--project outside-of-project --add-dir <workspaceDir>` when executing via the AGY harness.
- On Windows, attach processes to Windows Kernel Job Objects (`CreateJobObject` / `SetInformationJobObject`) to ensure 100% termination of subprocess trees on timeout or cancellation. On Unix, assign a dedicated process group (`Setpgid: true`) and kill via negative PID.

### 4.4. Structured Logging
- Use standard `log/slog` for all daemon and adapter logging.
- Structure log messages with key-value pairs (e.g. `slog.Info("executing turn", "session_key", key, "agent", agentName)`).
- Never log sensitive API keys, bot tokens, or credentials in plain text.

---

## 5. Development & Verification Commands

```bash
# Run all tests
go test ./...

# Run tests with verbose logs and race detector
go test -v -race ./...

# Format and lint code
go fmt ./...
go vet ./...

# Build daemon binary
go build -o bin/agyent ./cmd/agyent

# Cross-compile for Linux/macOS/Windows
make build-all
```

---

## 6. Self-Diagnostics & Troubleshooting for Agents

When debugging errors reported in the runtime environment:
1. **Query Audit Logs:** Check SQLite `audit_logs` in `~/.agyent/agyent.db` for execution turn metrics, token usage, and exact error messages.
2. **Inspect Subprocess Transcripts:** Check `.system_generated/logs/transcript.jsonl` in the conversation workspace for full tool invocation sequences and raw output logs.
3. **Trace System Daemon Logs:** Run `agyent run --verbose` to observe debug-level structured logs from internal adapters.
