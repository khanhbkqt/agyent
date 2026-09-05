# Milestone 10B: Semantic AI Handoff & Single-Pass Unified Memory Evolution

> **Document status:** Historical
> **Code authority:** evolution adapter, engine integration and current reference document
> **Last verified:** 2026-09-05

Comprehensive technical specification, empirical benchmark findings, architectural review feedback, and implementation plan for **Semantic AI Handoff, Single-Pass Memory Evolution, Configurable Quota Shields, and `/learn` Slash Commands** in `agyent`.

---

## 1. Executive Summary & Problem Context

In standard token-billed architectures, optimization focuses heavily on per-token pricing. In `agyent`, however, interactions run directly through the **Antigravity CLI (`agy`)** authenticated via **Google Account Tier / Monthly Subscriptions**:
- **Zero Variable Token Cost:** Token volume incurs no incremental API cost.
- **Critical Resource Constraints:** **Request Quota (RPM / RPD)** and **Subprocess Spawn Overhead**.
- **The Problem:** 
  1. Naive keyword/pattern matching (`strings.Contains`) in `filter.go` is brittle, fails to capture natural Vietnamese/English feedback, and yields false alarms on commented code snippets.
  2. A naive 2-pass approach (Call 1: Classify $\rightarrow$ Call 2: Extract) consumes 2 separate quota requests per session.
- **The Solution:** A **Single-Pass Unified Reflection Engine** with a **4-Tier Quota Shield** pipeline, paired with synchronous `/learn` and `/remember` commands.

---

## 2. Empirical Benchmark Validation (Real `agy` CLI)

Empirical testing was executed against the **real `agy` CLI binary** on the host workstation across 6 diverse Vietnamese and English test cases:

| Test Case | Dialogue Slice | Expected Result | `gemini-3.7-flash` (low effort) | `gemini-3.5-flash` |
| :--- | :--- | :--- | :---: | :---: |
| **Case A (Direct Correction)** | *"Chỗ này viết sai rồi em, phải dùng FlexTime scanner chứ"* | `should_reflect: true` (`lesson`) | ✅ **PASS** | ❌ FAIL |
| **Case B (Subtle Preference)** | *"Anh thích code ngắn gọn, trả về diff markdown thôi nha"* | `should_reflect: true` (`preference`) | ✅ **PASS** | ❌ FAIL |
| **Case C (False-Positive Bug Mention)** | *"Hôm qua bên database bị lỗi crash server nhưng anh fix xong rồi, giờ qua task mới nhé"* | `should_reflect: false` (`none`) | ✅ **PASS** | ❌ FAIL |
| **Case D (Banter / Noise)** | *"Chào em, hôm nay trời đẹp nhỉ"* | `should_reflect: false` (`none`) | ✅ **PASS** | ✅ PASS |
| **Case E (ADR / Architecture Decision)** | *"Thống nhất dự án này chuyển qua kiến trúc Ports & Adapters, không dùng monolithic nữa"* | `should_reflect: true` (`adr`) | ✅ **PASS** | ❌ FAIL |
| **Case F (Code Paste with Comment Bug)** | Go snippet containing `// fix crash when scanning nil timestamp` | `should_reflect: false` (`none`) | ✅ **PASS** | ❌ FAIL |

### Empirical Conclusions:
- `gemini-3.7-flash` (low effort) achieved **100% accuracy**, seamlessly navigating Vietnamese idioms (*anh/em*, nuanced phrasing) while rejecting false-positive bug mentions.
- Single-pass evaluation completes in $<1.5\text{s}$, consuming exactly **1 quota turn** per idle session.

---

## 3. Rigorous Architectural Review Feedback

### 3.1. Principal Systems & Concurrency Review
- **Sidecar OS Locking (`MEMORY.md.lock`):** To avoid Windows `ERROR_SHARING_VIOLATION` during atomic `os.Rename(.tmp, MEMORY.md)`, locking must always be applied to a sidecar file (`.lock`), never directly to the target markdown file being replaced.
- **Anti-Poison-Pill Fail-Open Bounds:** If an `agy` subprocess crashes, times out, or produces malformed JSON for $\ge 2$ consecutive attempts, the engine must forcibly advance `last_reflected_step` with a `WARN` log to prevent infinite retry loops.
- **Hot-Path Zero-Allocation Regex:** Pre-compile all code-block stripping and noise-filtering regular expressions during `NewHeuristicFilter` to eliminate GC allocation spikes.

### 3.2. Engineering Tech Lead Review
- **Clean Architecture Ports:** Ensure `HeuristicFilterPort` remains pure in `internal/core/ports/` and `EvolutionOrchestrator` depends strictly on ports.
- **Backward Compatible Config:** Ensure `nil` or empty `evolution.heuristics` configuration in `config.yaml` automatically falls back to comprehensive default multilingual keyword sets.
- **Synchronous UX for `/learn` & `/remember`:**
  - Execute synchronously directly via `MemoryStorePort` (bypassing the asynchronous worker queue).
  - Deterministically generate `conflict_key` via pure Go slugification (`slugify(rule, max 35 chars)`).
  - Provide formatted HTML confirmation responses on Telegram with clear badges.

---

## 4. Implementation Tasks & Deliverables

### Task 1: Configuration Layer Updates
- [ ] Add `EvolutionHeuristicsConfig` inside `EvolutionConfig` in `internal/config/config.go`.
- [ ] Add bilingual (EN/VN) default keyword lists in `DefaultConfig()`.
- [ ] Implement fallback normalization in `LoadConfig()` for empty heuristic settings.

### Task 2: Quota Shield & Heuristic Filter Refactoring
- [ ] Refactor `internal/adapters/evolution/filter.go` to use pre-compiled regexes and accept `EvolutionConfig`.
- [ ] Implement **Shield 1 (Local Zero-Quota Noise Filter)** to reject trivial chatter (< 5 words) and strip code blocks.
- [ ] Implement **Deterministic Bypass** for `AuditStatus == "ERROR"` and explicit session triggers.

### Task 3: Single-Pass Unified Reflection & Poison-Pill Guard
- [ ] Update static prompt in `internal/adapters/evolution/reflection.go` to evaluate and extract in a single turn (`candidates: []` on empty signal).
- [ ] Implement fail-open JSON parsing with robust markdown fence extraction.
- [ ] Add poison-pill cursor advance in `internal/adapters/evolution/orchestrator.go` after 2 consecutive failures.

### Task 4: Synchronous `/learn` & `/remember` Slash Commands
- [ ] Add `/learn` and `/remember` dispatcher in `internal/core/engine/commands.go`.
- [ ] Implement pure-Go `slugify()` helper for deterministic conflict keys.
- [ ] Update `/help` documentation in `commands.go`.

### Task 5: Comprehensive Unit & Chaos Testing
- [ ] `filter_test.go`: Test local noise filter, code block stripping, and audit error bypass.
- [ ] `reflection_test.go`: Test single-pass prompt execution and `candidates: []` handling.
- [ ] `commands_learn_test.go`: Test `/learn` and `/remember` execution and atomic file locking.
- [ ] `evolution_chaos_test.go`: Test anti-poison-pill fail-open cursor advance.

---

## 5. Verification Plan

```bash
# 1. Run evolution subsystem tests
go test -v ./internal/adapters/evolution/...

# 2. Run engine slash command tests
go test -v ./internal/core/engine -run "TestHandleCommand|TestLearnCommand"

# 3. Run chaos & concurrency tests
go test -v ./internal/core/engine -run "TestEvolutionChaos"

# 4. Run all tests
go test ./...
```
