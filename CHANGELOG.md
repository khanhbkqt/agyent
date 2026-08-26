# Changelog

All notable changes to **agyent** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
