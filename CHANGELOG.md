# Changelog

All notable changes to **agyent** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.0.4] - 2026-08-28

### Added
- **Diagnostic Engine & Triage Command (`agyent doctor`):** Added a modular health diagnostic subsystem under `internal/doctor/` and new CLI command `agyent doctor` (with alias `docter`). Covers automated diagnostics across 7 critical domains: system environment, configuration integrity, AGY CLI authentication/health/quota triage, SQLite WAL database integrity & active/hung session locks, Telegram bot token connectivity, security gateway posture, and plugin directory validation. Supports automated remediation with `--fix`.
- **Self-Updater Subsystem & Upgrade Command (`agyent update`):** Added automated self-updater under `internal/updater/` and new CLI command `agyent update` (with alias `upgrade`). Features real-time GitHub release tracking, SemVer version comparison, interactive confirmation prompts, safe binary replacement across platforms (handling locked running processes), and startup update notification banners.

### Fixed
- **One-Line Installer Seamless Execution:** Stripped UTF-8 BOM sequence from `install.ps1` and `install.sh` to eliminate PowerShell token parsing errors during `irm ... | iex` execution, and added graceful running process termination and file overwrite safety.

---

## [1.0.3] - 2026-08-28

### Added
- **Proactive Agent Genesis Greeting on `/new` & `/reset`:** When starting or resetting a conversation via `/new` or `/reset`, the engine now automatically bootstraps the session and triggers a proactive greeting turn (`ComposeBootstrapTurnPrompt`), introducing the active agent persona, presenting quick capabilities, and greeting the user immediately without waiting for an initial prompt.
- **Auto-Migration for Legacy Directives:** Added automatic migration of legacy directive files (`IDENTITY.md`, `SOUL.md`, `USER.md`, `MEMORY.md`) into standardized agent workspace directories upon engine initialization.

### Fixed
- **Agent Workspace & Project Path Resolution:** Fixed workspace path resolution for custom project directories and default workspace fallback paths in agent domain models and configuration loaders.
- **Browser Camoufox Interactive Element Targeting:** Enhanced interactive handler element targeting and XPath robustness in `builtin/plugins/browser-camoufox`.

---

## [1.0.2] - 2026-08-27

### Added
- **Milestone Activity Timeout Watchdog:** Added sliding activity-based execution timeout to the AGY process harness. Receiving meaningful milestone activity (`USER_INPUT`, `PLANNER_RESPONSE`, and tool calls) automatically resets the watchdog timer, allowing long-running agent workflows to continue uninterrupted while terminating unresponsive processes.
- **Synchronous Stream Error & Interruption Notice:** Integrated `EventStreamError` propagation across the AGY harness and delivery throttler to deliver explicit, real-time `⚠️ [Execution interrupted: ...]` notifications to the chat channel upon execution timeouts or cancellations.
- **Monotonic Security Upgrades (Milestone 14):** Added SQLite migration `000008_agent_security_preset` to persist and strictly enforce monotonic security posture levels (`unrestricted`, `developer`, `balanced`, `strict`, `read_only`) per agent profile.
- **Session Debugger Skill & Diagnostic CLI:** Added `agyent-session-debugger` diagnostic skill and standalone `scripts/debug_session.go` triage inspector for automated SQLite session health checks, turn token accumulation alerts, and transcript analysis.

### Fixed
- **Subagent Shared Directory Validation:** Added directory existence check before appending `--add-dir` in subagent `share` workspace mode.
- **Telegram Throttler Fallback Race Condition:** Resolved concurrency race condition when falling back to plaintext on Telegram 400 Bad Request markdown parse errors.

---

## [1.0.1] - 2026-08-27

### Fixed
- **Multi-Bot Session Key Parsing:** Enhanced `ParseSessionKey` and `ExtractChatIDFromSessionKey` to accurately parse 4-part namespaced keys (`telegram:botID:chatID:threadID`) and negative group IDs (`-100...`).
- **Bot ID & Thread ID Routing:** Fixed outbound message routing for slash commands, turns, error notifications, and subagent callbacks to ensure responses are delivered via the originating bot token instance and target forum topic.
- **Throttler Multi-Bot Mapping:** Ensured Telegram delivery throttler maps rate limits and message chunks per bot instance.

---

## [1.0.0] - 2026-08-26

### Added
- **Core Foundation (Milestone 1):** Hexagonal Ports & Adapters microkernel architecture, configuration manager (`config.yaml`), and unified `log/slog` structured logging.
- **Storage & Sessions (Milestone 2):** Pure-Go SQLite (`modernc.org/sqlite`) storage adapter with WAL mode, versioned SQL schema migrations (`000001_init_schema`), FIFO concurrency locks, and `FlexTime` timestamp scanner.
- **AGY CLI Process Harness & 2-Way Media Sync (Milestone 3):** Cross-platform subprocess execution engine with streaming JSON parser, timeout watchdog with Windows Job Objects and Unix process groups, and snapshot file watcher for automatic media synchronization.
- **EventBus & Debouncer (Milestone 4):** Non-blocking in-memory pub/sub event bus with lifecycle event hooks (`OnMessageReceived`, `OnPreTurn`, `OnPostTurn`, `OnArtifactCreated`, `OnError`), 2.0s sliding-window Go channel message debouncer and coalescer.
- **Telegram Bot Channel Adapter (Milestone 5):** Complete Telegram adapter supporting Private chats, Groups, and Forum Topics with `@mention` filtering, live streaming throttler (<5µs ingestion latency), smart markdown chunking, and slash commands UI.
- **Engine Orchestrator & CLI Packaging (Milestone 6):** Central engine orchestrator, interactive CLI setup wizard (`agyent init`) powered by `charmbracelet/huh`, dynamic streaming toggle (`/stream on|off`), and multi-platform compilation targets.
- **Context Management, Skills & Plugins (Milestone 7):** 5-tier context resolver, Progressive Skills Disclosure (~60 tokens/skill header index), global and workspace-scoped MCP configuration syncer with cross-process OS file locks, and built-in plugins (`browser-camoufox`, `database-sqlite`, `system-diagnostics`).
- **Multi-Conversation Management & Lifecycle GC (Milestone 8):** Flat multi-conversation model with 1-touch inline keyboards, `/ask` ephemeral queries, `/pin` / `/unpin` protection, and automated background garbage collector for archived sessions.
- **System Meta-Instruction & Prefix Caching (Milestone 9):** Level 0 static system runtime foundation anchored at Index 0 for 85–95% KV-cache hit rate, two-tier memory architecture (`MEMORY.md` durable memory + `memory/YYYY-MM-DD.md` daily episodic logs), and pre-compaction silent memory flush.
- **Agent Self-Learning & Evolution (Milestone 10):** Background reflection engine with zero-LLM heuristic pre-filtering, token-efficient temporal grounding markers (~6 tokens), 4D in-place memory conflict resolution, and rolling 48-hour cold-start lookback window.
- **Prompt Cache Metrics Observability:** Added `cache_read_tokens` tracking, SQLite migration `000004_cache_read_tokens`, `/tokens` command, and simulation benchmark suite (`scripts/benchmark_tokens.py`).
- **Open-Source Standardization:** Full English localization across all documentation and source code, MIT License, Contributing Guide, and standardized repository directives.
