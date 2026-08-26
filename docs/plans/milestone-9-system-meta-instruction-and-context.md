# Milestone 9: System Meta-Instruction & Context Architecture

Technical specification and execution plan for Milestone 9, reviewed and refined by the **Principal AI Systems Architect**, focusing on anchoring the System Meta-Instruction and implementing a production-grade two-tier memory read pipeline.

---

## 1. Objectives & Core Scope

- **Static Level 0 Anchor (Prefix Caching):** Ensure `agyent` maintains an immutable persona anchor, eliminating persona drift while optimizing static prompt prefixes for ~100% KV-cache hit rates and reducing TTFT by 50%–80%.
- **Two-Tier Memory Read Pipeline:** Ingest durable memory from `MEMORY.md` and dynamically resolve daily episodic logs from `memory/YYYY-MM-DD.md` with accurate local timezone handling.
- **Context Hierarchy Assembly:** Codify strict 5-level prompt hierarchy ordering across all turns.

---

## 2. Detailed Task Breakdown

### Phase 1: Core Domain & Static Instructions
- Update `internal/core/engine/bootstrap.go`.
- Define `systemRuntimeFoundationTemplate` constant encoding `[SYSTEM RUNTIME FOUNDATION]` across 6 core pillars (Identity, Capabilities, Engineering Principles, Memory Protocol, Self-Diagnostics, Persona Adherence).

### Phase 2: Context Adapter & Timezone-Aware Memory Read Pipeline
- Update `internal/adapters/context/resolver.go`.
- **Load Durable Memory:** Scan and ingest `MEMORY.md` into the `<LONG_TERM_MEMORY>` block.
- **Timezone-Aware Daily Memory:** Use user timezone (defaulting to system timezone or `USER.md` timezone setting) to resolve the exact calendar day `memory/YYYY-MM-DD.md`, preventing date jump errors across UTC day boundaries.
- **Safe Fallback:** If daily log files do not exist, gracefully skip without producing empty XML blocks or runtime crashes.

### Phase 3: Engine Wiring & Prefix Caching Optimization
- Refactor `ComposeResolvedTurnPrompt` in `internal/core/engine/bootstrap.go`.
- Anchor Level 0 `[SYSTEM RUNTIME FOUNDATION]` permanently at Index 0.
- Assemble in strict sequential order: Level 0 (System) $\rightarrow$ Level 1 (Global) $\rightarrow$ Level 2 (Workspace) $\rightarrow$ Level 3 (Skills & Plugins) $\rightarrow$ Level 4 (Attachments & User Message).

### Phase 4: Verification & Test Suite
- **Unit Tests (`bootstrap_test.go`):** Verify `ComposeResolvedTurnPrompt` consistently begins with the static Level 0 frame. Test XML tag ordering and structure for `<LONG_TERM_MEMORY>` and `<TODAY_MEMORY>`.
- **Timezone Boundary Tests:** Simulate boundary timestamps (e.g. 23:59 UTC+7) to verify correct resolution of `memory/YYYY-MM-DD.md`.
- **Real Subprocess Verification:** Execute live turns via `Harness.ExecuteStream` with AGY CLI, verifying log outputs to ensure prefixes remain identical across successive turns.

---

## 3. Definition of Done

- [x] `systemRuntimeFoundationTemplate` configured completely and strictly static.
- [x] `ComposeResolvedTurnPrompt` correctly assembles all 5 levels (Level 0 through Level 4).
- [x] Resolver successfully loads `MEMORY.md` and timezone-aware `memory/YYYY-MM-DD.md`.
- [x] 100% pass rate across Unit Tests and Real Subprocess Execution Tests.

---

## 4. Dependency & Risk Matrix
- **Dependencies:** Requires completed context structures from Milestone 7 (`ContextResolver`).
- **Risks:** Oversized `MEMORY.md` bloating the prompt $\rightarrow$ Mitigated by memory warnings and the Compaction Worker in Milestone 10.
