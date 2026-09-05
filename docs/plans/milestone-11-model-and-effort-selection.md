# Milestone 11: Dynamic Model & Reasoning Effort Selection

> **Document status:** Historical
> **Code authority:** model domain, AGY parser, engine commands and current reference document
> **Last verified:** 2026-09-05

**Status:** Done (100%)  
**Owner:** Principal Architect / Backend Agent  
**Target Completion:** 2026-08-26  

---

## 1. Objectives & Overview

Equip `agyent` with flexible, robust AI Model selection (`--model`) and Reasoning Effort control (`--effort`), matching the native capabilities of the Antigravity CLI (`agy`) while enforcing strict capability constraints and preventing runtime command failures.

---

## 2. Key Architecture Deliverables

1. **5-Tier Precedence Resolution Hierarchy:**
   - **Tier 1:** Per-Turn Explicit Overrides (`/ask --model=...`).
   - **Tier 2:** Session Active Overrides (`/model`, `/effort` per conversation).
   - **Tier 3:** Agent Persona Defaults (`agent.DefaultModel`, `agent.DefaultEffort`).
   - **Tier 4:** Global Gateway Configuration (`config.yaml`: `agy.default_model`, `agy.default_effort`).
   - **Tier 5:** System Defaults & Normalization Fallback (`gemini-3.7-flash`, `high`).

2. **Dynamic Model Discovery (`agy models` Parser):**
   - Automatically queries `agy models` at runtime.
   - Intelligently groups suffixed model IDs (`-high`, `-medium`, `-low`) into base models with supported effort tiers.
   - In-memory thread-safe cache (`sync.RWMutex`) with graceful fallback to default catalogue.

3. **Effort Capability Enforcement & Edge Case Handling:**
   - **No-Effort Models (e.g. `claude-sonnet-4-6`, `claude-opus-4-6-thinking`):** Harness strips `--effort` flag completely to prevent CLI rejection.
   - **Subset-Effort Models (e.g. `gemini-3.1-pro` supporting only `[low, high]`):** Clamps unsupported effort (`medium`) to `high` (`DefaultEffort`).
   - **CLI Error Recovery (`isEffortError`):** Automatically detects if a custom model rejects `--effort` and transparently retries turn execution without the flag.
   - **Zero-Lockout BYOK Passthrough:** Custom models pass directly to `agy` without hardcoded validation locks.

4. **Database & Storage Tracking:**
   - Migration `000005_model_selection.up.sql`: Added `active_model`, `active_effort` to `sessions`; `default_model`, `default_effort` to `agents`; and `model`, `effort` to `audit_logs`.
   - SQLite WAL Dual-Pool repositories updated to record and query model metrics.

5. **Telegram UI Integration:**
   - Interactive `/model` and `/effort` menus with Telegram Inline Keyboards.
   - 1-tap callback routing (`m:set:*`, `eff:set:*`, `m:reset`, `eff:reset`).
   - Live status reporting in `/status`.

---

## 3. Verification & Test Suite

- **Unit Tests:** `TestModelCapability_LookupAndNormalize`, `TestParseModelsOutput`, `TestEngine_ResolveExecutionParams_5Tiers`, `TestEngine_ModelAndEffortSlashCommands`.
- **Live Integration Tests:**
  - `TestRealAGY_DynamicModelDiscovery`: Verified dynamic discovery of all 7 real models from `agy models`.
  - `TestRealAGY_ModelAndEffortExecution`: Full live turn execution with `gemini-3.1-pro` + `low` effort, verified in SQLite `audit_logs`.
