# Changelog

All notable changes to **agyent** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.52] - 2026-09-08

### Added
- **Replied Message Short Context Capture & Injection (`channels/telegram`, `channels/zalo`, `debouncer`, `engine`):**
  - **Inbound Context Extraction:** Telegram and Zalo channel routers extract the sender name, message ID, and a clean truncated snippet (up to 300 runes) from referenced reply messages (`ReplyToMessage` on Telegram, `ReplyToMsg` on Zalo).
  - **Multimodal Summary Fallback:** If the replied message contains media without text, cleanly generates descriptive placeholders (`[Photo]`, `[Document: <filename>]`, `[Voice Audio]`, `[Audio]`, `[Video]`, `[Sticker]`, `[Attachment]`).
  - **Debouncer Coalescing:** Preserves `ReplyContext` across debounced message batches.
  - **Level 4 Prompt Injection:** Injects `[REPLIED MESSAGE CONTEXT]` with sender and snippet above the user prompt in `ComposeTurnPrompt`, `ComposeResolvedTurnPrompt`, and `ComposeContinuationPrompt`, giving agents immediate quote comprehension while strictly preserving KV-cache prefix invariance for Levels 0–3.

---

## [1.0.51] - 2026-09-07

### Fixed
- **Zalo Native `photo_url` & Direct Media Unmarshaling (`channels/zalo`):**
  - **Native Media Field Mapping:** Added support for official Zalo Bot Platform media fields: `photo_url`, `image_url`, `doc_url`, `document_url`, `file_url`, `audio_url`, `voice_url`, `video_url`, and `message_type: CHAT_PHOTO`.
  - **Direct CDN Media Ingress:** Accurately unmarshals direct Zalo CDN image links (`https://photo-stal-*.zdn.vn/...`) into `domain.InboundAttachmentRef`, enabling full multimodal processing for single and batch/album photo updates.
  - **Live Payload Inspector Script:** Added `scripts/debug_zalo_polling.go` utility for real-time Zalo update stream inspection.
  - **Comprehensive Test Coverage:** Added unit test `TestZaloInboundMessage_UnmarshalJSON_RealZaloPlatformPhotoURL`.

---

## [1.0.50] - 2026-09-07

### Fixed
- **Session-Scoped MCP Turn Leases, Deep-Copied Environment Isolation & Dynamic Versioning (`mcp`, `concurrency`, `harness/agy`):**
  - **Turn Leases:** Implemented session-scoped MCP turn lease acquisition and release to prevent tool unmounting races across concurrent sessions.
  - **Environment Isolation:** Deep-copied environment variables in runner to guarantee complete subprocess isolation.
  - **Dynamic Versioning:** Dynamically derived CLI version from git tags during build and release packaging.

---

## [1.0.48] - 2026-09-06

### Fixed
- **Zalo Nested Media Payload Extraction (`channels/zalo`):**
  - **Nested Payload & Schema Compatibility:** Added `ZaloAttachmentPayload` struct and `GetEffectiveURL()`, `GetEffectiveFileID()`, `GetEffectiveFileName()`, `GetEffectiveFileSize()`, and `GetEffectiveCaption()` methods. Resolved nested `attachments[i].payload.url`, `image_url`, `src`, `link` objects alongside top-level `photo`, `image`, `document`, `voice`, `audio`, and `video` schema variations.
- **Zero-Empty-Prompt Defense-in-Depth Guard (`channels/zalo`, `engine`, `bootstrap`):**
  - **Zero-Empty-Prompt Fallback:** If an inbound message contains attachments without text/caption, automatically synthesize a fallback user prompt (`"[Người dùng gửi ảnh/tệp đính kèm. Em hãy kiểm tra và phân tích tệp này.]"`) across Zalo Router, Engine, and Bootstrap Prompt Composers.
  - **Bubbletea /dev/tty Immunity:** Completely eliminates the risk of headless Bubbletea interactive TTY prompt crashes when receiving media without text.

---

## [1.0.47] - 2026-09-06

### Fixed
- **Inbound Media Caption & Description Unmarshaling (`channels/zalo`):**
  - **Struct Unmarshaling:** Added `Caption` and `Description` JSON fields to `ZaloInboundMessage` and `ZaloAttachment` structs in `client.go`, preventing caption drops during webhook or polling update decoding.
  - **Router Fallback Extraction:** Updated Zalo router to extract `rawText` with multi-level fallback: `msg.Text` -> `msg.Caption` -> `msg.Description` -> `attachment.Caption` -> `attachment.Description`.
  - **AttachmentRef Caption Mapping:** Mapped extracted captions directly to `domain.InboundAttachmentRef.Caption` for rich multimodal prompt context.

---

## [1.0.46] - 2026-09-06

### Fixed
- **AGY CLI Execution Argument Parsing (`harness/agy`):**
  - **Removed Erroneous `--print` Flag in STDIN Mode:** Removed standalone `--print` flag argument from `Execute` which caused `agy` CLI's flag parser to consume `--output-format` as prompt text (`--print took "--output-format" as its prompt`). Headless execution reliably uses `--output-format json` with the prompt streamed over STDIN.

---

## [1.0.45] - 2026-09-06

### Fixed
- **Headless AGY Execution & TTY Block Prevention (`harness/agy`):**
  - **Headless CLI Flags:** Passed `--print`, `--dangerously-skip-permissions` (when requested/configured), and `--disable-slash-commands` to AGY CLI executions to prevent Bubbletea interactive prompt crashes (`could not open TTY: open /dev/tty: no such device or address`) when running headlessly in daemon mode.
  - **Bubbletea / TTY Error Classification:** Added regex classification in `parser.go` to cleanly surface headless TTY blocking errors.
- **Inbound Media Extension & Multimodal Detection (`channels/zalo`):**
  - **Automatic Extension Inference:** Added automatic filename generation with appropriate extensions (`.jpg`, `.ogg`, `.mp4`, `.doc`) in Zalo message router when inbound attachments have empty filenames or lack extensions.
  - **Content-Type Fallback:** Enhanced Zalo media downloader to infer file extensions from HTTP `Content-Type` headers if the remote attachment filename is extensionless, enabling LLM Multimodal Vision to natively recognize and process images without fallback tool invocations.

---

## [1.0.44] - 2026-09-06

### Fixed
- **Three-Tier Security Boundary & Agent Memory Mutation (`security/pathjail`, `security/command_policy`, `security/manager`):**
  - **Differentiated Control Plane from Cognitive Data Plane:** Replaced overbroad blanket substring checks (`/.agyent/`) with targeted control-plane canonical path resolution. Restored agent autonomy to write to `MEMORY.md`, `USER.md`, `SOUL.md`, `IDENTITY.md`, and workspace files under `~/.agyent/workspace/` without triggering false-positive `[Path Jail - Control Plane Protection]` denials.
  - **Inviolable Control Plane Defense-in-Depth:** Enforced strict write protection on true control-plane assets (`~/.agyent/config.yaml`, `~/.agyent/agyent.db*`, `<workspaceDir>/.agents/hooks.json`, `/etc/shadow`, `~/.ssh`, `~/.aws`, `~/.kube`, `~/.gnupg`) across all security presets.
  - **Session Grant Pre-Execution Ordering:** Prioritized deep-scan `hasControlPlanePathReference` checks before session grant lookups in `manager.go`, preventing permission grants from bypassing control-plane and sensitive path protection.
  - **Cross-Platform Path & Command AST Normalization:** Normalized directory separators via `filepath.ToSlash` in Windows daemon config path checks, supported glob patterns in `isManageable`, and handled escaped quotes and quoted parentheses in shell pipeline segment splitting and command substitutions.
  - **Subagent APIS-4D Identity Propagation:** Added `AGYENT_AGENT_WORKSPACE` and `AGYENT_USER_ID` environment variables to `subagent.taskExecutor`.

---

## [1.0.43] - 2026-09-06

### Added
- **Project-Scoped Worker Isolation (ADR 0002) (`security`, `execution`, `harness/agy`, `storage/sqlite`):**
  - **`agy_project_registry` SQLite Table & Migration (`000013`):** Introduced dedicated tenant-to-project isolation mapping ensuring worker/subagent executions run within their strictly isolated project sandboxes.
  - **Execution Admission Chokepoint:** Integrated `ExecutionAdmission` in `execution.Service` enforcing APIS-4D execution identity, requiring `--project <id>` and `--sandbox` parameters for worker runs.
  - **CommandPolicy AST & Obfuscation Scanner:** Implemented deep AST parsing for shell commands, pipeline unnesting, subshell extraction, and Base64-encoded command deobfuscation scanning.
  - **`workspace_only` Security Preset & SuperAdmin Switching:** Added `workspace_only` preset and allowed SuperAdmins to dynamically switch security presets via `/security preset` commands.
  - **AI Fleet Definitions & System Diagrams:** Added comprehensive agent fleet specifications in `.agents/agents/` with refactored Mermaid architecture diagrams.

### Fixed
- **Headless AGY Permission Provisioning (`security`, `harness/agy`):**
  - **Automatic Tool Grants:** Auto-provisioned workspace and host `settings.json` tool permissions and AGY project permission grants to prevent non-interactive headless AGY permission denials.
  - **Control Plane Write Protection:** Hardened `pathjail` to enforce unconditional write protection on control-plane paths (`.agents/hooks.json`, `~/.agyent/`, `config.yaml`) across all path argument variations.
  - **Agent Project ID Binding:** Ensured AGY runner and execution service always select and propagate dedicated agent project IDs and resource boundaries.
- **Compaction Safety & Multi-Bot Zalo Routing (`engine`, `config`, `channels/zalo`):**
  - **Uncancelled Bounded Context for Compaction:** Guarded SQLite compaction transactions with uncancelled bounded context to prevent database corruption or state loss during turn timeouts.
  - **Config-to-SQLite Agent Sync:** Synchronized configured agent profiles from `config.yaml` into SQLite on daemon bootstrap.
  - **Zalo Multi-Bot Routing:** Added fallback bot name resolution from bound agent profiles in Zalo adapter.

---

## [1.0.42] - 2026-09-06

### Fixed
- **Multi-Target MCP Configuration Mirroring (`adapters/mcp`, `doctor`):**
  - **Declarative Config Target Alignment:** Configured `MCPSyncer` default path to `~/.gemini/config/mcp_config.json` (the global Declarative Config path recognized by Antigravity CLI and Language Server) instead of legacy `~/.gemini/antigravity-cli/mcp_config.json`.
  - **Multi-Target Atomic Mirroring:** Atomically synced mounted and unmounted ephemeral MCP server definitions across all runtime configuration targets (`~/.gemini/config/mcp_config.json`, `~/.gemini/antigravity/mcp_config.json`, and `~/.gemini/antigravity-cli/mcp_config.json`), ensuring seamless MCP tool injection (`camoufox_*`, `sqlite_*`, `scheduler`, `subagent_dispatcher`) across all background scheduler turns and interactive sessions.
  - **Fallback State Resolution:** Enhanced `readConfigUnderLock` to automatically fall back to alternative targets if the primary file does not exist yet.

---

## [1.0.41] - 2026-09-06

### Fixed
- **Scheduler Ephemeral MCP Mounting, Permission Bypass & Security Hooks (`scheduler`, `security`, `cmd`):**
  - **Dynamic Ephemeral MCP Plugin Mounting:** Injected `PluginManagerPort` and `MCPRegistryPort` into `TaskExecutor` and `Scheduler`, dynamically mounting active MCP servers (such as `browser-camoufox`, `scheduler`, `database-sqlite`, `subagent-dispatcher`) and acquiring turn leases before scheduled turn execution.
  - **Automatic Security Hook Provisioning:** Ensured workspace security hooks (`.agents/hooks.json`) are automatically provisioned before background scheduled tasks and heartbeats execute.
  - **Creator Privilege & DangerouslySkipPermissions Inheritance:** Propagated `e.cfg.AGY.DangerouslySkipPermissions` to scheduled turns created by SuperAdmins and resolved principal providers from channel session keys, eliminating headless stdin approval blocking on privileged tools (`run_command`, browser execution).
  - **Browser Camoufox Typing Imports:** Fixed missing `Union` and `Optional` typing imports in `builtin/plugins/browser-camoufox` (`handlers/extraction_handler.py`, `kinematics/mouse_dynamics.py`).

---

## [1.0.40] - 2026-09-06

### Added
- **Parent Agent & Creator Permission Inheritance for Scheduled Tasks (`scheduler`, `security`, `engine`):**
  - **Delegated User Principal:** Scheduled and background cron turns inherit the task creator's identity (`domain.PrincipalUser`, `Provider: task.Channel`, `SubjectID: task.CreatedBy`, `AccountID: task.AgentName`), resolving agent persona security presets and workspace boundaries.
  - **Channel-Routable Session Keys:** Constructed channel session keys (`domain.FormatSessionKey`) for cron turns, enabling synchronous and interactive HITL approval cards to route directly to the creator's Telegram chat.
  - **Quality Assertion against False-Positive Completion:** Guarded `TaskExecutor.ExecuteSchedule` against silent failures by asserting non-empty output, checking for soft-deny refusal patterns, and validating generated media artifacts before marking tasks as `COMPLETED`.
- **Multi-Tier HITL Approval & Smart Base Binary Extraction (`security`, `channels/telegram`, `engine`):**
  - **3-Tier Interactive Telegram Card:** Rendered approval hierarchy: `[ ✅ Allow Once ]`, `[ 🛡️ Allow Command (<base>) ]`, `[ 🔓 Allow All (Session) ]`, `[ ❌ Deny ]`, and `[ 🛑 Force Kill Agent ]`.
  - **Smart Base Command Matching:** Implemented `domain.ExtractBaseCommand` to dynamically parse shell command strings, environments, absolute/relative paths, and pipelines, extracting base binaries (e.g. `python3`, `node`, `git`, `curl`) for prefix matching.
  - **Session-Lifecycle-Bound Grants:** Bound session permissions to the active session lifecycle rather than arbitrary TTL timers, automatically invalidating grants upon session resets (`/reset`, `/new`, `/clear`, `/c switch`), agent switches (`/a use`), project switches (`/p use`), and cron turn completions.
  - **Hard Guardrails Invariance:** Core system protections (anti-self-escalation, `agyent.db` protection, `pkill agyent`, forbidden system paths) are strictly evaluated prior to session grant matching, ensuring wildcard `*` never bypasses system security.

---

## [1.0.39] - 2026-09-06

### Fixed
- **Live EventBus Streaming & Typing Heartbeat for Turn Auto-Recovery (`engine`, `channels/telegram`):**
  - **Live Streaming on Recovery:** Enabled real-time EventBus streaming (`isStreaming = true`) during turn auto-recovery, allowing Telegram `DeliveryThrottler` to render live streaming tokens, tool actions, and typing indicators as soon as daemon reboots.
  - **Typing Heartbeat:** Added persistent background typing heartbeat during batch/non-streaming recovery runs to eliminate silent UI freezes on Telegram.
  - **Full Context Level 0–4 Assembly:** Guaranteed full Foundation, Soul, Identity, Memory, Directives, and Skills injection for recovered turns where `ConversationID` was not yet initialized before the daemon crashed.
  - **Outbound Message Routing Metadata:** Populated complete `AgentName`, `SessionKey`, and `ThreadID` in all recovery messages, ensuring flawless multi-bot routing.
  - **Telegram HTTP 400 Safe Fallback:** Automatically cleared `opts.ReplyParameters` on Telegram 400 errors (such as `replied message not found` from old rebooted sessions), guaranteeing message delivery.

---

## [1.0.38] - 2026-09-06

### Fixed
- **Zalo Polling Deadlock, Dual-Format JSON Normalization & Transport Overhaul (`channels/zalo`):**
  - **Dual Response Envelope Normalization:** Supported both Telegram-compatible (`ok`, `result`, `error_code`, `description`) and Zalo-native (`error`, `message`, `data`) API response envelopes with automatic normalization in `APIResponse.Normalize()`, preventing inbound message drop (`GetUpdates`) and response extraction failure on native payloads.
  - **Idle Long-Polling Timeout Resolution:** Enhanced `IsTimeoutError` to recognize HTTP 408, JSON ErrorCode 408, native Zalo Error 408, `context.DeadlineExceeded`, and HTTP client timeouts, returning `[]ZaloUpdate{}` immediately without backoff delays.
  - **Polling Loop & Backoff Hardening:** Eliminated exponential backoff loops on idle long-polling cycles and capped network backoff at 10s.
  - **HTTP Transport Connection Pooling:** Configured `http.Client` with custom `http.Transport` (`MaxIdleConns: 10`, `IdleConnTimeout: 30s`, `DisableKeepAlives: false`) for long-polling stability.

---

## [1.0.37] - 2026-09-06

### Fixed
- **Zalo Public Media Upload Providers & CDN Reliability Overhaul (`channels/zalo`):**
  - Resolved upload failures where 0x0.st returned HTTP 503 (permanently closed uploads due to botnet spam) and Catbox returned HTTP 412 ("Invalid uploader") / connection timeouts.
  - Added **FreeImage.host** as primary high-speed image CDN provider, backed by Cloudflare edge caching, permanent hosting, zero IP blocks, and direct image URLs (`iili.io`).
  - Added **Uguu.se** as secondary high-speed universal file upload provider, supporting all media and document types up to 100MB with direct download URLs (`n.uguu.se`).
  - Enhanced **Catbox.moe** and **Litterbox** uploaders with realistic browser User-Agent headers, accurate multipart MIME headers (`image/jpeg`, `image/png`, etc. instead of generic `application/octet-stream`), and 30-second timeouts.
  - Purged dead **0x0.st** provider from the upload chain.
  - Added transparent warning logging per failed provider for rapid diagnostics in daemon logs.

---

## [1.0.36] - 2026-09-06

### Added
- **In-Flight Turn Auto-Recovery & Daemon Crash Resilience (`engine`, `storage`):**
  - Added SQLite schema migration `000012_in_flight_turns` and `InFlightTurnRepository` to persist active turn lifecycle states (`running`, `completed`, `failed`, `interrupted`) along with channel delivery metadata and recovery context.
  - Implemented asynchronous startup turn recovery worker with bounded concurrency (default 2 workers) and FIFO session lock serialization to resume interrupted user turns cleanly.
  - Implemented stale subagent task reconciliation on startup, transitioning dangling in-progress tasks to `failed` and restoring subagent loop availability.
  - Added circuit-breaker protection (`max_retries = 1`) to eliminate crashing recovery loops, and ephemeral turn discard logic for `/ask` temporary sessions.
  - Added guarded continuation prompt assembly (`ComposeRecoveryPrompt`) to safely resume conversations without re-executing non-idempotent side effects.
  - Added daily janitor retention policy to purge completed/failed turns older than 7 days.
  - Published comprehensive subsystem architectural reference in `docs/turn-recovery-and-resilience-architecture.md`.

---

## [1.0.35] - 2026-09-06

### Added
- **Interactive Model Selection & Gemini 3.8 Flash Support (`engine`, `harness/agy`):**
  - Added support for `gemini-3.8-flash` as the default fast/flash model and added a direct quick-selection button in the `/model` interactive keyboard menu.
  - Dynamically generated `/model` inline keyboard buttons from the runtime model registry (`ModelRegistry`), seamlessly displaying all active, custom, or newly added models.
  - Added shorthand version aliases (`3.8`, `3.7`, `3.6`, `3.1`) to the AGY model parser for streamlined model switching.
- **AGY Project-Scoped Worker Security Isolation Architecture (`docs`, `adr`):**
  - Published comprehensive architectural proposal and [ADR 0002](docs/adr/0002-agy-project-scoped-worker-isolation.md) defining project-scoped worker isolation, per-agent native grant scopes, and agyent-mediated authorization.

### Fixed
- **Standalone Text Message Delivery Alongside Media (`channels/telegram`, `channels/zalo`):**
  - Restored full standalone text chat message bubbles alongside media/photo albums across both Telegram and Zalo channel adapters, eliminating text suppression and caption hijacking.
  - Hardened the Telegram delivery throttler to synchronously upload media during `flushFinalSession` before text chunks, cleanly delete placeholder status messages on image-only turns, and eliminate false-positive warning alerts.
- **Plugin Force Sync on Self-Upgrade (`cmd/update`):**
  - Ensured `agyent update` passes `--force` to `plugin update --all` during automatic self-upgrades so that built-in embedded plugins properly overwrite on-disk versions.

---

## [1.0.34] - 2026-09-06

### Added
- **Zalo Outbound Media & Public CDN Bridging (`channels/zalo`):**
  - Added native `sendPhoto` support for Zalo Bot Platform via public CDN bridging (`Catbox`, `Litterbox`, `0x0.st`) to automatically upload local file artifacts and agent brain images.
  - Implemented `ExtractAndCleanOutboundMedia` to parse markdown image tags `![caption](path)` and document links `[caption](path)`, resolve local filesystem paths, strip raw image tags, and emit typed attachments.
  - Implemented Smart Split Delivery: when outbound text contains an image and text $\le 2000$ runes (Zalo's caption limit), the text is attached directly to the photo caption for a single unified chat bubble. If text $> 2000$ runes, the photo is sent first followed by chunked text messages.
  - Added document delivery formatting that converts non-image documents into clickable download cards (`📄 **Tài liệu:** [filename](url)`).
  - Updated multi-bot routing in `SendFile` and `SendOutboundAttachment` to resolve the originating bot instance dynamically.
- **Zalo Bot Mention Stripping & Multi-Word Tag Support (`channels/zalo`):**
  - Implemented `CleanZaloMention` in Zalo update router to strip leading `@bot` mentions from inbound messages while preserving `RawText` in `domain.CanonicalMessage` and setting `IsMentioned: true`.
  - Robustly handles bot display names with spaces (e.g. `@Trao Mơ FC /new` -> `/new`) and trailing punctuation (`:`, `,`), allowing slash commands (`/new`, `/reset`, `/status`) and prompts to execute reliably in group chats.

### Changed
- **Clean Message Delivery for Zalo Channel (`engine`):**
  - Omitted group/ephemeral `contextTag` headers (`🌐 [agent • Global]`, `📁 [agent • project]`) for Zalo channel messages at the core engine level, delivering 100% clean, distraction-free response text.

---

## [1.0.33] - 2026-09-06

### Fixed
- **Zalo Adapter Streaming Bridge Support (`channels/zalo`):**
  - Resolved issue where Zalo messages failed to deliver when global AGY streaming was enabled (`streaming_enabled: true`) because the engine bypassed `channel.Send` in streaming mode.
  - Added EventBus subscription bridge in Zalo adapter (`EventStreamInit`, `EventStreamDelta`, `EventStreamResult`, `EventStreamError`, `EventStreamInterrupted`).
  - Active turns send an immediate typing indicator and keep an active 4-second heartbeat typing ticker while tokens are generated, signaling progress to Zalo users.
  - Finalized turn response and outbound artifacts are assembled and dispatched to Zalo chat cleanly upon `EventStreamResult`, and mid-stream errors/interruptions are reported immediately.

---

## [1.0.32] - 2026-09-06

### Fixed
- **Zalo Long-Polling 408 Request Timeout Backoff Loop (`channels/zalo`):**
  - Resolved issue where Zalo Bot Platform long-polling idle timeouts (HTTP 408 or JSON error code 408 / "Request timeout") were treated as network failures, triggering exponential backoff sleep up to 16 seconds in `pollBotUpdates`.
  - Introduced `APIError` and `IsTimeoutError` to classify long-polling idle timeouts and return empty updates `[]ZaloUpdate{}` without error, enabling continuous real-time message ingress.
  - Added defense-in-depth in `pollBotUpdates` to prevent backoff sleep on any timeout condition.

---

## [1.0.31] - 2026-09-06

### Fixed
- **Zalo Outbound Multi-Bot Routing & Fallback Resolution (`channels/zalo`):**
  - Resolved outbound message delivery failure where bot client lookup failed when session keys or numeric Bot IDs were used (e.g. `zalo:4293721026991223652:zgr-...`).
  - Implemented `resolveClientEx` with prioritized multi-stage resolution across agent persona name, bot ID string, numeric bot ID, session key, and graceful fallback to primary/default/active bot client pool to ensure messages are never dropped.
  - Automatically registered numeric Bot ID prefixes from tokens (`<botID>:<secret>`) and agent bindings (`bind_agent`) into bot lookup maps during adapter startup and authenticated update polling.
- **Strict Zalo Bot Platform Message Formatting (`channels/zalo`):**
  - Added `FormatToZaloMarkdown` for automatic outbound text normalization conforming strictly to Zalo Bot specifications.
  - Bullet list conversion (`- item`, `* item` to `• item`).
  - Strict italics (`_text_`), strikethrough (`~~text~~`), underline (`{underline}text{/underline}`), big text (`{big}text{/big}`), and named color tags (`{red}`, `{green}`, `{yellow}`, `{orange}`).
  - Automatic Markdown hyperlink normalization (`[Title](url)` to `Title (url)`) ensuring links are directly clickable in Zalo chat bubbles.
  - Markdown horizontal divider conversion (`---` to `────────────────────────`).
  - Full protection for multiline code blocks and inline code snippets preventing accidental style corruption.

---

## [1.0.30] - 2026-09-06

### Fixed
- **Zalo Polling Ingress Dual Payload Support (`channels/zalo`):**
  - Resolved `getUpdates` unmarshaling failure when Zalo Bot Platform returns a single JSON object envelope instead of an array.
  - Dynamically inspects raw payload via `json.RawMessage`: unpacks single update object into `[]ZaloUpdate{update}` and unmarshals array directly into `[]ZaloUpdate`.

---

## [1.0.29] - 2026-09-06

### Added
- **Enterprise Zalo Bot Platform Channel Adapter (`channels/zalo`):**
  - **Hexagonal Architecture Conformance:** Complete channel adapter satisfying `ports.ChannelPort` and `ports.HITLApprovalPort`.
  - **Dual Ingress:** Dual long-polling (`getUpdates`) and webhook listener with constant-time token comparison (`crypto/subtle.ConstantTimeCompare`) and 5MB request body limit (`http.MaxBytesReader`) against denial-of-service.
  - **Multi-Bot Pool & Agent Persona Binding:** Supported multiple Zalo bots with per-agent persona binding (`bind_agent`) and enterprise RBAC authorization.
  - **Exponential Backoff with Full Jitter:** Resilient Zalo API client leveraging `internal/core/retry`.
  - **HITL Security Approval Routing:** Rich interactive approval cards, diff previews, and slash commands (`/approve`, `/deny`, `/kill`).
  - **Rich UTF-16 Formatting & Chunker:** Markdown conversion with UTF-16 offset handling, smart codeblock chunking, and outbound throttler with LRU/TTL eviction to prevent memory leaks.
- **Multi-Channel Composite Multiplexer (`channels/composite`):**
  - Unified multiplexer running Telegram and Zalo channels concurrently.
  - Deterministic HITL approval fallback routing and non-swallowing `errors.Join` lifecycle error reporting.
- **Diagnostics & Setup Wizard Integration:**
  - Added Zalo connectivity and webhook diagnostic checks (`CategoryZalo`) to `agyent doctor`.
  - Integrated Zalo Bot setup into `agyent init` interactive onboarding wizard.

### Fixed
- **Telegram Smart Split Image Delivery:**
  - Attached accompanying text $\le 1024$ runes directly as HTML photo caption, suppressing duplicate separate text messages.
  - Split longer text into photo caption + following markdown message.
  - Added graceful fallback to plain text retry on HTML caption formatting errors.
- **Dynamic Image / Multimedia Task Timeout in Scheduler:**
  - Detected image and media generation requests (`isImageOrMediaTask`) and dynamically elevated execution timeout to at least 300s to prevent premature timeout cancellation.
- **Active Tool Heartbeat Watchdog Ping in Harness:**
  - Emitted periodic silent heartbeat pings to `onMilestone()` during silent tool execution (e.g. `generate_image`, heavy web browsing) to prevent false sliding watchdog timeout.

---

## [1.0.28] - 2026-09-05

### Fixed
- **Camoufox Headless Linux Stabilization & Zombie Elimination:**
  - **Media Freeze & Headless CPU Fix:** Added `image.animation_mode=none`, `media.autoplay.default=5`, and `layers.acceleration.disabled=true` preferences to eliminate 100% CPU lockups on headless Linux VPS environments.
  - **Targeted Process Watchdog:** Profile-scoped process cleanup terminating orphan `camoufox-bin` instances upon profile unlock.
  - **Busy Guard & Page Stop:** Wrapped browser actions in `busy_guard(60s)` with automatic `window.stop()` fallback.
  - **Dedicated Worker Thread Affinity (`AgentWorkerManager`):** Enforced dedicated worker threads in Python daemon to maintain Playwright greenlet thread affinity and support multi-agent parallel browsing without lock contention.
  - **Extended RPC Timeout:** Increased RPC client timeout to 70s to accommodate 60s browser execution deadlines.
- **Scheduler Reliability & Session Starvation Prevention:**
  - **Configurable Task Timeout:** Supported `DefaultTaskTimeoutSeconds` in scheduler config without hardcoded clamping.
  - **ParseNextRun Guard:** Fixed error handling to prevent negative `next_run_at` busy-spin loops on invalid cron parsing.
  - **Active Schedule Scope:** Refined `AcquireDueSchedules` query to only claim `ACTIVE` schedules, avoiding contention with paused or completed jobs.
  - **Contextual Failure Diagnostics:** Emitted enriched diagnostic notifications on `EventScheduleFailed`.
- **AGY Subprocess Timeout Forwarding:**
  - Propagated resolved turn and subagent timeouts to `agy` CLI via `--print-timeout` flag, preventing premature 5-minute process termination on long-running tasks.

---

## [1.0.27] - 2026-09-05

### Added
- **Standardized Architecture & Engineering Workflows:**
  - Introduced formal engineering workflow graphs (`debug.json`, `bugfix.json`, `change.json`, `new-feature.json`) in `.agents/workflows/`.
  - Added automated repository verification gates (`check_workflows.py`, `check_docs.py`, `check_architecture.py`) wired into `make verify`.
  - Standardized repository documentation taxonomy, ADR framework, and engineering contracts.

### Fixed
- **Telegram Zero-Drop Delivery Guarantee & Resilience:**
  - **Extended Request Timeout:** Configured 30s request timeouts across all Telegram bot instances and operations.
  - **Transient Drop Retry:** Implemented 3-attempt exponential backoff retry for transient network drops and upstream 5xx gateway errors.
  - **Safe Chunking & Recursive Bisection:** Lowered markdown chunking threshold to `SafeTelegramMessageLimit = 3200` characters to prevent Telegram 400 "message is too long" errors, with automated recursive chunk bisection fallback.
  - **Multi-Chunk Overflow Delivery:** Guaranteed sequential delivery of all intermediate chunks when streaming response spans $\ge 3$ chunks.
  - **Forum Topic ThreadID Preservation:** Preserved `ThreadID` across deliveries and in-place edit fallbacks.
  - **Edit Fallback:** Added graceful fallback from failed in-place message edits to sending fresh messages upon stream completion.
  - **Detached Error Event Emission:** Emitted `EventStreamError` using detached background context to ensure errors are never dropped upon turn timeout or cancellation.
  - **Instant Typing Indicator:** Dispatched immediate typing action upon message intake, guarded with `sync.Once` on session lock acquisition.
  - **Regression Test Suites:** Added comprehensive test suites `TC-THR-12..17` and engine streaming timeout error tests.

---

## [1.0.26] - 2026-09-05

### Added
- **Security Architecture v3.1 Production Hardening & Defense-in-Depth:**
  - **Centralized Policy Engine (`core/auth`):** Implemented centralized ABAC/RBAC engine with principal resolution (user ID, roles, bot affiliations, workspace permissions) and audit event emission.
  - **Ingress Security & Default Agent Privacy:** Added SQLite migration `000011` making new agents private (`is_public = FALSE`) by default to prevent cross-tenant discovery; added constant-time secret token verification on Telegram webhooks.
  - **Single Execution Chokepoint (`core/execution`):** Created a unified `ExecutionService` acting as the authoritative chokepoint before subprocess dispatch.
  - **OS Peer Credential Verification:** Integrated socket-level caller verification via `SO_PEERCRED` (Linux) and `LOCAL_PEERCRED` (macOS) on local IPC connections.
  - **Anti-IDOR Scoped Repositories:** Enforced parent session/tenant boundaries on all conversation and subagent task repository queries.
  - **Atomic CAS Subagent State Machine:** Implemented Compare-And-Swap lifecycle transitions preventing race conditions during concurrent subagent task execution.
  - **Dynamic MCP Isolation:** Configured per-agent and per-session tool mounting preventing context and tool leakage across workspaces.
  - **Admission Barrier Debouncer & Safe Janitor:** Introduced early authorization checks in message debouncer and non-blocking lock checks in background janitor.
  - **Per-Agent Security Presets & Host CLI (`agyent agent`):** Introduced host CLI management (`agyent agent create/list/edit/delete`) with configurable security presets (`strict`, `balanced`, `developer`, `unrestricted`) and defense-in-depth anti-self-escalation.
- **Scheduler Outbound Media & Artifact Delivery:** Propagated `conversationID`, `workspaceDir`, and media artifacts generated during scheduled turns for seamless Telegram delivery.

---

## [1.0.25] - 2026-09-05

### Added
- **Generic Exponential Backoff with Jitter (`core/retry`):**
  - **Zero-CGO Pure-Go Retry Package:** Implemented production-grade retry mechanics featuring Full, Equal, Decorrelated, and No Jitter algorithms.
  - **Additive Jitter for Rate Limits:** Added additive jitter protection for server-instructed `RetryAfter` durations to prevent synchronized thundering herd spikes against Telegram and LLM APIs.
  - **Generic Runners & Leak Prevention:** Provided generic `DoWithResult[T]` and `Do` functions with permanent error bailout, custom delay extractors, and timer leak prevention.

### Fixed
- **Multi-Bot Scheduled Outbound Delivery:** Routed scheduled task outputs and heartbeat proactive messages by `AgentName` to dedicated Telegram bot instances, with seamless fallback to primary bot token.
- **Model Alias Canonicalization & Reasoning Effort Sanitization:**
  - Standardized model aliases (`sonnet`, `opus`, `flash`, `pro`) in runner argument builders and domain models.
  - Automatically stripped unsupported `--effort` flags on models without reasoning effort support (e.g. Gemini Flash models).
  - Added self-healing retry in scheduled executor upon detecting CLI reasoning effort rejection errors.
- **EventBus High-Concurrency Stress Test:** Sized async queue buffer to prevent dropped event assertions during high-volume parallel stress testing.

---

## [1.0.24] - 2026-09-04

### Added
- **Autonomous Scheduling, Cron & Proactive Heartbeats (Pillar 6):**
  - **Proactive Heartbeat Engine:** Added autonomous background health, status, and proactive wakeups configured via `HEARTBEAT.md` with interval and active hour evaluation.
  - **Schedule & Cron Engine:** Added recurring cron and one-shot scheduled execution with `skip_to_latest` misfire policy and overlap suppression.
  - **Prefix KV-Cache Optimization:** Level 4 temporal prompt injection for due tasks, guaranteeing zero invalidation of Level 0–3 system runtime and directive prefixes.
  - **New Slash Commands & Interactive Callbacks:** Added `/schedule`, `/cron`, `/heartbeat` commands with RBAC authorization and Telegram inline keyboard callbacks (`sched:cancel:`, `hb:on`, `hb:off`, `hb:trigger`).
  - **Scheduler MCP Plugin & IPC API:** Introduced builtin scheduler MCP tools and IPC action handlers enabling autonomous natural language tool calling by the agent.
  - **Pillar 6 Runtime Foundation:** Updated Level 0 System Runtime Foundation and Genesis onboarding bootstrap to include autonomous scheduling and heartbeat directives.
- **3-Tier Plugin Integrity & VirtualEnv Auto-Discovery:**
  - Added automatic resolution for active `VIRTUAL_ENV` and `~/.agyent/camoufox/venv` in `ResolveCommandPath`.
  - Added `--check` and `--health` self-probe flags across all builtin plugins (`browser-camoufox`, `database-sqlite`, `system-diagnostics`).
  - Integrated automated plugin syntax, typing, and environment verification into `agyent doctor` and `make lint-plugins`.

### Changed
- **Strict Hexagonal Architecture Boundary Enforcement:**
  - Purified domain models by removing direct OS filesystem I/O from `Agent` entity.
  - Relocated CLI stdout parsing from `domain/model.go` to `adapters/harness/agy`.
  - Moved IPC wire DTOs from `domain/security.go` to `adapters/security/ipc`.
  - Purified `domain/subagent.go` and relocated subagent execution from Core to `internal/adapters/subagent`, resolving Core->Adapter dependency inversion.

### Fixed
- **Scheduler Routing, Response Delivery & Concurrency:**
  - Runner response forwarding to target chat upon scheduled task completion.
  - Session routing for plugin notifications via IPC.
  - Fast-path read lock optimization (`AcquireDueSchedules` and `AcquireDueHeartbeats`) preventing write lock contention.
  - Thread-safe in-flight task cancellation using atomic `CompareAndDelete`.
  - User workspace timezone detection wired into the scheduler instance.
- **Security Manager Concurrency & Test Race Hardening:** Thread-safe snapshotting of whitelist patterns in `SecurityManager` preventing data race during concurrent evaluations, and race-free task lookup in subagent engine test.

---

## [1.0.23] - 2026-09-04

### Added
- **Camoufox Full Capabilities Engine 2.0 (v1.4.0):**
  - **Stealth Media Sniffer & FFmpeg Downloader:** Real-time interception of video/audio chunks, YouTube decrypted `videoplayback` streams, HLS playlists (`.m3u8`), and DASH manifests (`.mpd`) with active quality escalation, dual-adaptive stream muxing, and accurate trimming via `imageio-ffmpeg` zero-setup fallback.
  - **Native Desktop Headful Mode:** Added visual debugging support on workstation desktops (`headful: true`) with full anti-detection bypass.
  - **Autonomous CAPTCHA Detection & Solving:** Added automated resolution for Cloudflare Turnstile, hCaptcha, and reCAPTCHA.
  - **Advanced Interaction & Gesture Engine:** Added support for drag-and-drop, keyboard combos, complex mouse gestures, canvas inspection, and dynamic DOM interaction.
  - **New MCP Tools:** Added `camoufox_sniff_media`, `camoufox_download_media`, `camoufox_solve_captcha`, and `camoufox_interact`.

---

## [1.0.22] - 2026-09-04

### Changed
- **Decoupled Core & Standardized Markdown Wire Format:** Decoupled channel-specific HTML generation from `core/engine` and `core/domain`. Engine slash commands and system notifications now produce clean GitHub-Flavored Markdown, while channel adapters transparently handle channel-native rendering.
- **Unified Admin Checking:** Consolidated sender admin verification through `config.IsAdmin()`.

### Fixed
- **Outbound Media Delivery & Document Fallback:** Implemented file lock retries (`openFileWithRetry`) for rapid artifact sync on Windows, added automatic `SendDocument` fallback when Telegram rejects photos, and introduced HTTP 429 rate limit backoff.
- **Overhauled System Runtime Foundation:** Restructured Level 0 System Runtime Foundation into 5 imperative pillars with strict outbound media referencing rules (`![alt](path)` and `[title](path)`).

---

## [1.0.21] - 2026-09-04

### Fixed
- **Multi-Bot Media & Artifact Dispatch:** Ensured artifacts, photos, voice notes, and documents generated during agent turns are routed and delivered to the correct originating bot adapter in multi-bot topologies.
- **Human-In-The-Loop (HITL) Multi-Bot Routing:** Scoped HITL authorization callbacks, button interactions, and inline keyboard events to the specific bot instance that originated the turn.
- **Subagent Session Bleed Prevention:** Hardened `subagent-dispatcher` to isolate subagent session context per conversation ID, eliminating cross-agent state contamination.
- **Path Case & Workspace Normalization:** Normalized filesystem path comparisons across Windows and Unix platforms in `SecurityManager` and `ContextResolver`.

---

## [1.0.20] - 2026-09-03

### Fixed
- **Camoufox Browser Manager Typing Imports & v1.2.4 Upgrade:** Added missing `typing` imports (`Dict`, `Any`, `Optional`) to `browser_manager.py` to prevent Python runtime errors during daemon startup, and bumped builtin browser plugin version to 1.2.4 for automatic client sync.

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
