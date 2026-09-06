# Zalo Bot Platform Channel Adapter Architecture

> **Document status:** Reference
> **Code authority:** `internal/adapters/channels/zalo`, `internal/adapters/channels/composite`, `cmd/agyent/run.go`
> **Last verified:** 2026-09-06

This document specifies the technical design, architectural patterns, and integration specifications for the **Zalo Bot Platform Channel Adapter** in `agyent`.

---

## 1. Architectural Overview

The Zalo Channel Adapter implements `ports.ChannelPort` and `ports.HITLApprovalPort` within `agyent`'s Hexagonal Architecture (Ports and Adapters). It connects local and VPS instances of `agyent` to the official **Zalo Bot Platform REST API** (`https://bot-api.zaloplatforms.com`).

```
+-------------------------------------------------------------------------------+
|                             cmd/agyent/run.go                                 |
+---------------------------------------+---------------------------------------+
                                        |
                 +----------------------v----------------------+
                 |      internal/adapters/channels/composite/   |
                 |              Mux (ChannelMux)               |
                 |  - Implements ports.ChannelPort             |
                 |  - Implements ports.HITLApprovalPort        |
                 +----------+-----------------------+----------+
                            |                       |
            +---------------+                       +---------------+
            |                                                       |
+-----------v-----------------------+       +-----------------------v-----------+
| internal/adapters/channels/       |       | internal/adapters/channels/       |
|            telegram/              |       |              zalo/                |
|  - Long-polling & Webhook         |       |  - Long-polling & Webhook         |
|  - Multi-Bot Pool & RBAC          |       |  - Multi-Bot Pool & RBAC          |
|  - Inline Button HITL             |       |  - Text Command & Callback HITL   |
|  - Media Sync                     |       |  - Media Sync & Staging           |
|  - HTML & Markdown Formatting     |       |  - UTF-16 Rich Styling & Markdown |
|                                   |       |  - Backoff Jitter via core/retry  |
+-----------------------------------+       +-----------------------------------+
```

---

## 2. Core Capabilities

### 2.1 Multi-Channel Multiplexer (`composite.Mux`)
- Implements `ports.ChannelPort`, `ports.HITLApprovalPort`, and the lazy attachment-fetcher contract.
- Registers multiple channel adapters simultaneously (e.g. `telegram`, `zalo`).
- Dispatches outbound messages, typing indicators, and attachments to the matching channel adapter based on `msg.Channel` or `domain.ParseSessionKey(msg.SessionKey).Channel`.
- Routes Human-in-the-Loop (HITL) approval cards only when the originating session resolves to a registered channel; missing or invalid routing is denied rather than falling back to another provider.

### 2.2 Multi-Bot Pool & Dedicated Agent Virtualization
- Supports both single-bot and multi-bot configurations under `zalo.bots`:
  ```yaml
  zalo:
    mode: "polling" # or "webhook"
    api_url: "https://bot-api.zaloplatforms.com"
    group_id: "2498572093845"
    admin_user_ids: ["admin_user_1", "admin_user_2"]
    allowed_group_ids: ["2498572093845"]
    bots:
      - name: "support"
        bot_token: "ZALO_TOKEN_PRIMARY"
        bind_agent: "general_assistant"
      - name: "coder"
        bot_token: "ZALO_TOKEN_SECONDARY"
        bind_agent: "deep_coder"
  ```
- Outbound responses and actions retain the originating channel, chat, thread, and normalized bot identity. Agent-bound bot selection remains available for scheduled delivery.

### 2.3 Resilient Networking & Exponential Backoff with Jitter
- Integrated with `internal/core/retry`:
  - Automatically retries transient network interruptions, HTTP 429 (Rate Limit), and 5xx Server Errors.
  - Honors full jitter to eliminate thundering herd collisions against Zalo API endpoints.
  - Aborts immediately on permanent 400 Bad Request or 401 Unauthorized via `retry.Permanent`.

### 2.4 Human-In-The-Loop (HITL) Security Integration
- Formats structured interactive security cards with:
  - Agent Persona (`AgentName`)
  - Target Tool (`ToolName`)
  - Bash Command (`CommandLine` in codeblock)
  - Target File Path (`TargetFile`)
  - Diff Preview (`DiffPreview` in diff block)
  - Security Risk Level (`RiskLevel`) and Reason (`Reason`)
- Admin Approval Commands:
  - `/approve <req_id>` - Approve for single execution (`allow_once`)
  - `/approve <req_id> session` - Whitelist tool/action for current session (`allow_session`)
  - `/deny <req_id>` - Deny execution (`deny`)
  - `/kill <req_id>` - Force terminate subprocess tree (`force_kill`)
- Strict RBAC validation against both `zalo.admin_user_ids` and `security.admin_user_ids`.

### 2.5 Dual Polling & Webhook Modes
- **Polling Mode:** Long-polling (`getUpdates`) with adaptive backoff. Idle server-side timeouts (HTTP 408 Request Timeout or JSON error code 408) are treated as expected empty poll cycles returning `[]ZaloUpdate{}` without error, maintaining continuous real-time responsiveness without sleep backoff delays.
- **Webhook Mode:** Local HTTP webhook server listening on configured host and port, secret verification via `X-Secret-Token` / `X-Bot-Token`, and automatic webhook registration via `setWebhook`.

### 2.6 Message Formatting & UTF-16 Code Unit Preservation
- Zalo Bot Platform rich styling requires UTF-16 code unit offsets (surrogate pairs count as 2). `ZaloMessageFormatter` precisely computes `start` and `len` across Unicode / Vietnamese UTF-8 multi-byte glyphs.
- Automatic conversion of HTML formatting tags (`<b>`, `<code>`, `<pre>`, `<i>`, `<a>`) into Zalo Markdown.
- Smart Chunker (`ChunkZaloMessage`) splits responses exceeding 1950 runes while preserving fenced code block boundaries.

---

## 3. Diagnostic & Doctor Integration

Run diagnostic health checks via the built-in doctor CLI:

```bash
agyent doctor
```

The `CategoryZalo` diagnostics automatically evaluate:
1. **Zalo Bot Token Credentials:** Verifies presence and non-empty token formatting across all configured bots.
2. **Live API Authentication (`getMe`):** Probes Zalo Bot Platform to verify bot validity, display name, and bot ID.
3. **Target Group Whitelist:** Confirms default `group_id` or `allowed_group_ids` are configured.
4. **Webhook URL Verification:** Validates URL schema and reachability when running in `webhook` mode.
