# Dynamic Model & Reasoning Effort Selection Architecture

> **Document status:** Reference
> **Code authority:** `internal/core/domain/model.go`, engine resolver/commands, AGY model parser
> **Last verified:** 2026-10-13

This document specifies the standardized architecture for **Model Selection** and **Reasoning Effort** support in the `agyent` ecosystem.

---

## 1. Objectives & Architectural Principles

1. **Multi-Model & Variable Effort Normalization:** Fully harness Antigravity CLI (`agy`) flags (`--model <name>` and `--effort <low|medium|high|none>`), allowing seamless switching between frontier reasoning models (`claude-opus-5-5`), low-latency flagship models (`gemini-3.8-flash`), and open-weight models (`gpt-oss-120b`).
2. **5-Tier Resolution Hierarchy:** Ensure deterministic precedence resolution from per-turn flags $\rightarrow$ session scope $\rightarrow$ agent persona $\rightarrow$ global configuration $\rightarrow$ fallback defaults.
3. **Comprehensive Edge Case Resilience:**
   - **Models without Reasoning Effort Support:** Models that do not accept reasoning tokens automatically omit the `--effort` flag to prevent CLI syntax or parameter errors.
   - **Models with Subset Effort Support:** Models that only accept specific effort tiers (e.g. `gpt-oss-120b` supporting only `["medium"]`) are clamped/fallback to their nearest valid default.
   - **Runtime CLI Flag Rejection Recovery:** Automatic detection of CLI errors containing `flag provided but not defined: -effort` or `effort not supported`, triggering a single auto-retry turn with `--effort` stripped.
   - **Backward Compatibility for Retired Models:** Older models removed from `agy` CLI (such as `gemini-3.7-flash`, `gemini-3.6-flash`, `gemini-3.5-flash`, and `gemini-3.1-pro`) are transparently normalized to the active flagship model (`gemini-3.8-flash` or `claude-opus-5-5`), preventing user session breakage.
4. **Prefix KV-Cache Preservation & Cost Tracking:** Preserve Level 0–4 prompt structure across models while recording `model`, `effort`, and `thinking_tokens` in `audit_logs`.

---

## 2. 5-Tier Resolution Hierarchy

$$\text{Resolved}(Model, Effort) = \text{FirstNonEmpty}(T_1, T_2, T_3, T_4, T_5)$$

```
+-------------------------------------------------------------------+
| Tier 1: Per-Turn Explicit Override (e.g. /ask --model=flash ...)  |
+---------------------------------+---------------------------------+
                                  | (if empty)
+---------------------------------v---------------------------------+
| Tier 2: Session Active Override (e.g. /model pro, /effort high)   |
+---------------------------------+---------------------------------+
                                  | (if empty)
+---------------------------------v---------------------------------+
| Tier 3: Agent Persona Default (agents.default_model/effort)       |
+---------------------------------+---------------------------------+
                                  | (if empty)
+---------------------------------v---------------------------------+
| Tier 4: Global Gateway Config (config.yaml: agy.default_model)    |
+---------------------------------+---------------------------------+
                                  | (if empty)
+---------------------------------v---------------------------------+
| Tier 5: Runtime Normalizer & Fallback (Canonical Aliases)         |
+-------------------------------------------------------------------+
```

---

## 3. Model Capability Taxonomy

Each model is characterized by a `ModelCapability` profile with explicit context window limits and compaction thresholds:

| Model Canonical ID | Aliases | Display Name | Supported Efforts | Default Effort | Max Context | Max Output | Auto-Compact Limit (90%) | Capability Notes |
| :--- | :--- | :--- | :--- | :--- | :---: | :---: | :---: | :--- |
| `gemini-3.8-flash` | `flash`, `fast`, `3.8`, `3.8-flash`, `gemini`, `3.7`, `3.7-flash`, `3.6`, `3.6-flash`, `3.5`, `3.5-flash`, `gemini-pro`, `gemini-3.1-pro` | Gemini 3.8 Flash | `["low", "medium", "high"]` | `high` | **2,097,152** | 65,536 | 1,887,436 | Flagship Google multimodal & reasoning Flash. Back-aliases retired Gemini models. |
| `gemini-4-argon` | `argon`, `gemini-4`, `gemini-4-argon`, `4`, `4-argon` | Gemini 4 Argon | `["low", "medium", "high"]` | `high` | **1,048,576** | 65,536 | 943,718 | Frontier Google DeepMind reasoning model with 1M context. |
| `claude-opus-5-5` | `opus`, `claude-opus`, `opus-5.5`, `claude-opus-4-6-thinking`, `smart`, `pro` | Claude Opus 5.5 | `["low", "medium", "high"]` | `high` | **200,000** | 8,192 | 180,000 | Frontier Claude deep reasoning. Aliased as default `pro`/`smart` reasoning model. |
| `claude-sonnet-5-5` | `claude`, `sonnet`, `claude-sonnet`, `sonnet-5.5`, `claude-sonnet-4-6` | Claude Sonnet 5.5 | `["low", "medium", "high"]` | `high` | **200,000** | 8,192 | 180,000 | Frontier Claude coding & generalist with effort support. |
| `gpt-oss-120b` | `gpt-oss`, `oss-120b`, `gpt`, `gpt-oss-120b-medium` | GPT-OSS 120B | `["medium"]` | `medium` | **128,000** | 16,384 | 115,200 | Open-weight foundation model with medium effort. |

---

## 4. Normalization Algorithm & Edge Cases

```go
func NormalizeModelAndEffort(rawModel, rawEffort string, customAliases map[string]string) (canonicalModel string, normalizedEffort string, isCustom bool) {
    // 1. Resolve alias against custom and canonical maps
    canonicalModel = resolveAlias(rawModel, customAliases)

    // 2. Lookup capability in registry
    capability, exists := LookupCapability(canonicalModel)
    if !exists {
        // Custom / BYOK unlisted model: passthrough
        return canonicalModel, rawEffort, true
    }

    // 3. Edge Case 1: Model has NO effort support (SupportedEfforts is empty)
    if len(capability.SupportedEfforts) == 0 {
        return capability.ID, "", false // Strip --effort flag entirely
    }

    // 4. Edge Case 2: Model supports subset of efforts
    if rawEffort == "" {
        normalizedEffort = capability.DefaultEffort
    } else if contains(capability.SupportedEfforts, rawEffort) {
        normalizedEffort = rawEffort
    } else {
        // Clamp/fallback to model's default effort if requested effort is invalid
        normalizedEffort = capability.DefaultEffort
    }

    return capability.ID, normalizedEffort, false
}
```

---

## 5. User Interaction & Slash Commands

- `/model` or `/m`: View active model, resolution source, and switch model via 1-touch Telegram Inline Keyboard.
- `/model <name>`: Switch active model for the current session (e.g. `/model flash`, `/model pro`, `/model reset`).
- `/effort` or `/eff`: View active effort and select level via Inline Keyboard `[ Low 🟢 ] [ Medium 🟡 ] [ High 🔴 ] [ None ⚪ ]`.
- `/effort <level>`: Set reasoning effort for current session (`low`, `medium`, `high`, `none`, `reset`).
- `/status`: Displays active model and effort along with scope and uptime.
- `/tokens`: Displays model name and effort alongside token usage breakdown.
