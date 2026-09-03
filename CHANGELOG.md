# Changelog

All notable changes to **agyent** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.0.19] - 2026-09-03

### Added
- **Autonomous Self-Healing Doctor (`agyent doctor --fix`):** Added comprehensive diagnostic triage and automatic remediation for orphaned zombie processes (`agy`, `python`, `camoufox`) and stale file locks (`parent.lock`, session lockfiles) across Windows and Unix platforms.
- **Background Daemon Janitor:** Implemented an autonomous background janitor service within `Engine` that periodically reaps dangling process trees, purges orphaned temporary sockets, and cleans stale workspace artifacts during continuous multi-day gateway operations.

---

## [1.0.18] - 2026-09-03

### Fixed
- **Firefox `parent.lock` Deadlock & Camoufox Stale Lock Prevention:** Handled Firefox parent.lock deadlocks in `browser-camoufox` using non-blocking flock detection and automated ephemeral profile fallback. Reduced daemon RPC timeout from 120s to 35s.
- **TurnID & Generation Guard in DeliveryThrottler:** Added `TurnID` to `ExecutionRequest` and streaming events, implementing Generation matching with atomic `CompareAndDelete` to prevent late turn termination from killing active workers.
- **Bounded Subprocess Exit & Append Mode Turn Handover:** Added bounded subprocess exit wait and synchronized STDIN writing in AGY harness runner, and added turn handover wait in append mode within engine orchestrator.
- **Camoufox Plugin v1.2.3 Update:** Disabled `browser-camoufox` by default in `plugin.json` and bumped plugin version to 1.2.3.

---

## [1.0.17] - 2026-09-03

### Fixed
- **AGY Stream Pipe Deadlock & Turn Timeout Elimination:** Resolved an issue where streaming turns could block indefinitely until a 5-minute timeout occurred if child AGY processes kept stdin pipes open. `StreamParser` now returns immediately upon receiving the final `result` JSON-RPC event.
- **Active Child Process Termination:** Actively closes `stdinPipe` upon streaming completion, ensuring child AGY process trees terminate cleanly and release operating system resources.
- **Thread-Safe Real-Time Steering Control:** Protected `stdinWriter` invocations with a mutex to eliminate race conditions during concurrent real-time steering interrupts.
- **Interrupted Audit Status Mapping:** Mapped `INTERRUPTED` stream status to `ExecutionResult` error for accurate SQLite audit logging.

---

## [1.0.16] - 2026-09-02

### Fixed
- **MCP Server Crash & Handshake Blocking Prevention:** Resolved critical regression where `browser-camoufox/server.py` crashed on startup when `camoufox` Python dependencies were uninstalled on host VPS environments. Added safe optional imports and eliminated blocking daemon loops during `initialize` handshake to guarantee sub-millisecond JSON-RPC startup and zero Antigravity CLI timeout stalls.
- **Pre-Flight MCP Script Validation in AssembleActivePlugins:** Validated on-disk existence of script arguments (`.py`) prior to dynamic mounting into `mcp_config.json`, preventing broken subprocess pipes.
- **Camoufox Plugin Auto-Upgrade v1.2.2:** Bumped builtin browser plugin version to 1.2.2 ensuring instant extraction of optimized MCP server scripts on daemon boot.

---

## [1.0.15] - 2026-09-02

### Fixed
- **Multi-Agent Scoped Plugin Discovery & Resolution:** Supported multi-agent plugin discovery across global (`~/.agyent/plugins`) and per-agent scoped directories (`~/.agyent/agents/<name>/.agents/plugins/`).
- **Auto-Extraction on Plugin Toggle:** Ensured embedded capability plugins are automatically extracted to disk before enabling if not already present.
- **Context Resolver Dual-Scope Aggregation:** Updated `ContextResolver` to assemble plugin rules and skills across dual scopes without omissions or collision.
- **Agent Scoped Plugin Slash Command:** Enhanced `/plugin` slash command to support enabling and disabling plugins scoped per active agent.

---

## [1.0.14] - 2026-09-02

### Added
- **Smart Abort & Real-Time Steering (`queue_mode: "append"`):** Implemented seamless real-time turn steering allowing inbound user messages to gracefully interrupt active streaming turns via `runner.InterruptStream(sessionKey)` (streaming `{"event": "interrupt"}\n` over STDIN). In-flight atomic tool calls complete safely (`state: "DONE"`) before exit without corrupting workspace files.
- **Graceful Fallback & Process Tree Watchdog:** Added configurable `grace_timeout_seconds` (default 3.0s) watchdog to ensure clean process termination before escalating to hard process tree kills.
- **Session Queue Mode Management (`/mode`):** Added `/mode` slash command allowing users to inspect and toggle active session queue mode between sequential FIFO (`/mode fifo`) and real-time append steering (`/mode append`).
- **Interrupted Turn UI Finalization & Audit Metrics:** Emitted `EventStreamInterrupted` across the event bus, appending clean interruption indicators `[Turn Interrupted by User]` to Telegram messages and recording `StatusInterrupted` in SQLite audit logs.

---

## [1.0.13] - 2026-09-02

### Fixed
- **Security Gateway Evaluation & Path Jail Enforcement:** Added explicit path jail enforcement for read/search tools (`list_dir`, `grep_search`, `find_by_name`) and fixed `web_search` parameter evaluation when query-based lookups lack target URLs.
- **Deterministic Level 3 Prefix KV-Cache Sorting:** Sorted progressive skills metadata deterministically by name in ContextResolver to preserve Gemini Prefix KV-Cache invariant and maintain >85% cache hit efficiency.
- **Process Tree Cleanup with ProcessJobGuard:** Attached harness runner subprocesses to `ProcessJobGuard` (Windows Kernel Job Objects / Unix process groups) ensuring complete termination of runaway child processes.
- **Subagent Double-Dispatch Elimination:** Pre-registered background subagent tasks directly in the registry upon dispatch to prevent duplicate polling execution in `pollerLoop`.
- **Slash Commands Directory Traversal Validation:** Added strict regex validation for `/agent` and `/project` commands to block path traversal sequences (`..`, `/`, `\`).
- **Telegram Stream Execution Error Visibility:** Propagated stream execution errors to `DeliveryThrottler` to surface unhandled error notices immediately to users.

---

## [1.0.12] - 2026-09-01

### Fixed
- **Outbound Media & Carousel Album Delivery:** Added `ExtractAndCleanOutboundMedia` in Telegram adapter to parse markdown images, resolve relative and absolute brain directory paths (`~/.gemini/antigravity/brain/<conv_id>/...`), and clean carousel HTML comments (`<!-- slide -->`).
- **Native Telegram Photo Album Groups:** Added `SendMediaGroup` to package multiple outbound images generated within a turn response into native Telegram photo albums.
- **Brain Artifact Detection & HTML Formatting:** Extended `SnapshotWatcher` and AGY harness runner to detect newly created media artifacts in the AGY brain directory during turn execution and sanitized image tags in Telegram HTML formatting.

---

## [1.0.11] - 2026-09-01

### Added
- **APIS-4D Multi-Tenant Environment Context Injection:** Subprocesses executed via the AGY process harness and dynamically mounted MCP servers now receive standard execution environment variables (`AGYENT_AGENT_NAME`, `AGYENT_AGENT_WORKSPACE`, `AGYENT_SESSION_KEY`, `AGYENT_USER_ID`) to ensure strict context isolation and multi-tenant security across concurrent sessions.
- **Plugin Developer & Isolation Standard:** Published comprehensive documentation `docs/plugin-developer-and-isolation-standard.md` detailing multi-tab profile isolation, headless daemon management, and MCP environment contracts.
- **Browser Camoufox & SQLite Session Hardening:** Upgraded `browser-camoufox` and `database-sqlite` plugins with session-aware tab isolation, continuous profile vault integrity checks, and persistent connection stability.

### Fixed
- **Multi-Bot Dynamic Agent Resolution in Commands:** Slash commands and turn initializations now dynamically detect and honor `BindAgent` metadata passed from multi-bot channel adapters, automatically bootstrapping agent workspaces upon first contact.

---

## [1.0.10] - 2026-09-01

### Added
- **Composite Channel Multiplexer (`CompositeChannelMux`):** Introduced `CompositeChannelMux` in `internal/adapters/channels/composite/mux.go` providing a unified channel routing layer for multi-channel extensibility across Telegram, Discord, and custom webhooks.
- **Delivery Throttler Panic Protection:** Added per-session panic recovery routines in `DeliveryThrottler` to safeguard streaming message delivery and guarantee graceful worker lifecycle termination.
- **TargetContext Domain Alignment:** Standardized `ChannelPort` interface across domain and adapter layers using strongly-typed `TargetContext`.

### Fixed
- **Multi-Bot Concurrent Pool Initialization:** Fixed Telegram adapter initialization loop break bug that prevented secondary bot tokens from launching concurrently.
- **Dedicated Bot-to-Agent Binding:** Deprecated and removed runtime `/use` slash command in favor of deterministic 1:1 dedicated bot-to-agent binding and strict RBAC ownership.

---

## [1.0.9] - 2026-08-29

### Added
- **CLI Security Preset Management (`agyent security`):** Introduced full `agyent security` CLI command suite (`status`, `preset` / `set-preset`, `list-presets`) allowing host administrators to inspect security postures, list preset matrices, and switch or downgrade presets freely across `config.yaml` and SQLite database without monotonic chat restrictions.
- **Onboarding Setup Wizard Polish (`agyent init`):** Upgraded `agyent init` into a multi-step interactive onboarding experience with Security Preset selection (`unrestricted`, `developer`, `balanced`, `strict`, `read_only`), HITL Approval Timeout setup, and automated extraction/enablement of builtin Capability Plugins (`browser-camoufox`, `database-sqlite`, `subagent-dispatcher`, `system-diagnostics`).
- **Non-Interactive Onboarding Flags:** Added `--security-preset`, `--approval-timeout`, `--plugins`, `--enable-all-plugins`, and `--skip-plugins` flags to `agyent init` for automated server provisioning.

### Fixed
- **HITL Clean Reason & Diff Formatting:** Separated security policy violation reasons from file diff previews in Telegram Human-In-The-Loop (HITL) approval cards for clean readability.
- **Session Concurrency & Ghost Lock Prevention:** Fixed ghost lock resurrection in `lock_manager.go`, enforced full process-tree termination on HITL force-kill or session cancel, and wrapped background evolution routines with `SafeGo` panic recovery.
- **Dynamic Python Interpreter Resolution for MCP:** Dynamically resolves system and virtualenv Python interpreters (`python3`, `python`, etc.) for embedded MCP plugins and enforces native tool calling in agent directives.
- **Actionable Self-Updater Error Guidance:** Added actionable permission-denied hints and escalation suggestions when running `agyent update` in restricted environments.

---

## [1.0.8] - 2026-08-29

### Added
- **Embedded Builtin Plugins & Auto-Sync Subsystem:** Builtin plugins (`browser-camoufox`, `database-sqlite`, `subagent-dispatcher`, `system-diagnostics`) are now compiled directly into the `agyent` static binary using `//go:embed all:plugins`. Implemented `PluginManager` (`internal/adapters/plugin/manager.go`) featuring atomic file replacement, SHA-256 provenance verification, downgrade protection, and cross-platform lock safety.
- **Plugin Management CLI (`agyent plugin`):** Added new CLI suite `agyent plugin` with subcommands: `list`, `update`, `install`, `enable`, and `disable`. Automatic plugin sync is also integrated into `agyent run` daemon bootstrap and `agyent update` self-updater.
- **Browser Camoufox v1.2.0 Continuous Session Daemon:** Upgraded `browser-camoufox` with a background persistent HTTP daemon (`daemon.py`), persistent profile vault, multi-tab switching, OAuth popup capture, and automatic profile rehydration.
- **Conversation Wildcard Performance Index:** Added SQLite migration `000009_conversations_wildcard_index` for high-throughput wildcard lookups during multi-conversation search and list operations.

### Fixed
- **Telegram Long-Polling VPS Stability:** Increased `getUpdates` polling timeout and HTTP client timeout to eliminate intermittent `context deadline exceeded` errors on high-latency VPS networks.

---

## [1.0.7] - 2026-08-29

### Added
- **Workspace Inbound Media Relocation & Git Isolation:** Introduced `WorkspacePort` (`internal/core/ports/workspace.go`) and `WorkspaceManager` adapter (`internal/adapters/workspace/manager.go`). Inbound message attachments (photos, documents, audio, videos) are now automatically relocated into the active workspace directory at `<workspaceDir>/uploads/<safe_name>` during turn execution.
- **Git Tracking Isolation & Device Name Sanitization:** Automated generation of `uploads/.gitignore` to prevent inbound media from polluting repository git status. Enforced sanitization of Windows reserved device names (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`) and directory traversal sequences.
- **Frictionless In-Workspace PathJail & Diff Alignment:** Aligned PathJail security evaluation and SnapshotWatcher baseline diffing with `<workspaceDir>/uploads/` for seamless agent tool access without triggering out-of-boundary security violations.

---

## [1.0.6] - 2026-08-28

### Fixed
- **Token Cache Hit Ratio & Gross Input Calculation:** Corrected gross input calculation (`InputTokens + CacheReadTokens`) for multi-step tool call turns in both `/tokens` in-chat report and `agyent stats` CLI command. Accurately calculates cache hit efficiency percentage (`CacheReadTokens / GrossInputTokens * 100%`) across complex multi-step reasoning trajectories.

---

## [1.0.5] - 2026-08-28

### Added
- **Per-Agent Declarative Security Isolation (Milestone 14 Polish):** Support for declaring per-agent `security_preset` directly inside `config.yaml` (`agents.<name>.security_preset`). Refactored `SecurityManager` with pre-built isolated evaluator bundles per posture and introduced per-turn execution context (`TurnSecurityContext`) to prevent concurrent singleton state mutations.
- **Model Context Capabilities & Autonomous Context Compaction (Milestone 15):** Integrated context window capabilities metadata for Gemini, Claude, and GPT-OSS models. Added sliding-window autonomous compaction engine (`internal/core/engine/compactor.go`) and new in-chat `/compact` slash command. Preserves Level 0–3 system prefix KV-cache by anchoring compacted continuity digests strictly within Level 4 prompt boundaries.
- **Multi-Agent Token Analytics & Granular Filtering:** Added `GetTokenStatsReport` in SQLite storage and enhanced `/tokens` command with agent-level filtering (`/tokens <agent_name>`) and multi-agent breakdown cards displaying input, output, cache-read savings, and total tokens.
- **Global Token Analytics CLI (`agyent stats`):** Added new CLI command `agyent stats` for terminal-based token consumption analytics, supporting global summaries, per-agent breakdowns, session-specific filters, and live cache efficiency metrics.

### Fixed
- **RBAC Security Access Control on `/compact`:** Enforced agent owner/admin verification on `/compact` slash command while allowing unclaimed system agents to be managed smoothly.

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
