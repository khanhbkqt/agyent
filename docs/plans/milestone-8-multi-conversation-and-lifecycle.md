# Milestone 8: Multi-Conversation Management & Lifecycle GC

> **Document status:** Historical
> **Code authority:** conversation repository/commands/GC and current conversation reference
> **Last verified:** 2026-09-05

**Objective:** Build Flat Multi-Conversation Management, Isolated Ephemeral Turns (`/ask`), 1-Touch Adaptive Inline Keyboards, Zero-Memory Pinning, and Tiered Automated Lifecycle Garbage Collection (Lifecycle GC).

---

## 📋 Work Breakdown Structure (WBS)

### Phase 1: Database Schema & Idempotent Migration
- [x] **Task 8.1.1:** Create migration file `internal/adapters/storage/sqlite/migrations/000002_multi_conversations.up.sql`:
  - Create `conversations` table and optimized indexes (`idx_conversations_active_lookup`, `idx_conversations_alias`, `idx_conversations_gc_purge`).
  - Include automated backfill script migrating existing `global_conversation_id` and `project_conversation_id` records into `conversations`.
- [x] **Task 8.1.2:** Update automated migration runner in `internal/adapters/storage/sqlite/sqlite.go`.

### Phase 2: Domain Entities, UI Structs & Storage Repositories
- [x] **Task 8.2.1:** Create `internal/core/domain/conversation.go` defining `Conversation` and `ConversationSummary` structs.
- [x] **Task 8.2.2:** Update `internal/core/domain/message.go` adding `InlineButton`, `InlineKeyboardRow`, and `InlineKeyboard` to `OutboundMessage`.
- [x] **Task 8.2.3:** Declare `ConversationRepository` interface in `internal/core/ports/storage.go`.
- [x] **Task 8.2.4:** Implement `ConversationRepository` in `internal/adapters/storage/sqlite/sqlite.go`:
  - `GetConversation(ctx, id)`
  - `GetConversationByAlias(ctx, sessionKey, agentName, projectName, aliasIndex)`
  - `ListRecentConversations(ctx, sessionKey, agentName, projectName, limit, offset)`
  - `SaveConversation(ctx, conv)`
  - `TouchConversation(ctx, sessionKey, agentName, projectName, convID, promptSnippet)`
  - `SetConversationPinned(ctx, id, isPinned)`
  - `SetConversationArchived(ctx, id, isArchived)`
  - `SetConversationTitle(ctx, id, title)`
  - `GetExpiredArchivedConversations(ctx, olderThanDays)` (IDs only)
  - `PurgeConversations(ctx, ids []string)` (Independent DB deletion)

### Phase 3: Core Engine, Bug Fixes & Lifecycle GC Worker
- [x] **Task 8.3.1 (Critical Bug Fix):** Update `internal/adapters/harness/agy/stream_parser.go` to wrap `ports.ErrConversationNotFound` on stream loss, ensuring unified self-healing across streaming mode.
- [x] **Task 8.3.2:** Update `internal/core/engine/engine.go`:
  - Handle `/ask`: Execute turn with `ConversationID = ""` and ephemeral flag (no session overwrite, no `MEMORY.md` update).
  - Standard turns: Call `TouchConversation` upon completion to synthesize title from first user prompt for new sessions.
- [x] **Task 8.3.3:** Build `internal/core/engine/gc.go` (Conversation GC Worker):
  - Goroutine running every 24 hours and at startup.
  - Automatically archive inactive sessions > 14 days or exceeding 10 active sessions.
  - Automatically purge sessions archived > 30 days.
  - Implement `safePurgeBrainDir` with 36-character UUID regex and path traversal verification before calling `os.RemoveAll`.
  - Decouple SQLite transaction from disk deletion I/O.
- [x] **Task 8.3.4:** Update `internal/core/engine/commands.go`:
  - Mid-turn collision prevention: Block or warn when user attempts switching conversation during active LLM turn.
  - Fix agent switch context bleed: Automatically load new agent's latest conversation on `/use <name>`.
  - Implement commands suite: `/ask`, `/c`, `/new`, `/c switch`, `/pin`, `/unpin`, `/c rename`, `/c archive`, `/c clean`.

### Phase 4: Telegram Channel Adapter & Adaptive UI
- [x] **Task 8.4.1:** Update `internal/adapters/channels/telegram/adapter.go`:
  - Convert `domain.InlineKeyboard` $\rightarrow$ `gotgbot.InlineKeyboardMarkup` when delivering outbound messages.
- [x] **Task 8.4.2:** Update `internal/adapters/channels/telegram/router.go`:
  - Handle `u.CallbackQuery` events (standard callback data: `c:sw:<uuid>`, `c:pin:<uuid>`, `c:unpin:<uuid>`, `c:new`, `c:arc:<uuid>`).
  - Call `b.AnswerCallbackQuery` to dismiss Telegram spinner and forward as command message to Engine.

### Phase 5: Automated Testing & Verification
- [x] **Task 8.5.1:** Write unit tests `internal/adapters/storage/sqlite/sqlite_test.go` covering CRUD, alias indexing, pin/unpin, archive, and backfill migrations.
- [x] **Task 8.5.2:** Write unit tests `internal/core/engine/commands_test.go` & `gc_test.go` verifying `/ask`, `/c`, `/pin`, `/c switch`, mid-turn guard, `safePurgeBrainDir`, and GC worker.
- [x] **Task 8.5.3:** Write integration tests `internal/adapters/channels/telegram/router_test.go` verifying CallbackQuery routing and Inline Keyboard rendering.
- [x] **Task 8.5.4:** Write stream parser test `stream_parser_test.go` asserting `ports.ErrConversationNotFound` in streaming mode.

---

## 🎯 Definition of Done

1. Automated SQLite migrations run with safe backfill; zero legacy data loss.
2. The `/ask` command executes independently without modifying `active_conversation_id` in `/status`.
3. The `/c` menu renders an intuitive list with 1-touch interactive inline buttons on Telegram.
4. `/pin` and `/unpin` accurately pin/unpin the active session without requiring parameters.
5. GC Worker safely purges `brain/<id>/` directories of expired archived sessions (> 30 days) and permanently protects pinned sessions (`is_pinned = 1`).
6. The `safePurgeBrainDir` function prevents 100% of path traversal risks and root folder deletions.
7. Self-healing on `ports.ErrConversationNotFound` operates seamlessly across both Stream and Batch modes.
8. Entire test suite `go test ./...` achieves 100% pass rate.
