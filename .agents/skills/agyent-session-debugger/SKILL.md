---
name: agyent-session-debugger
description: >-
  Diagnostic runbook and triage workflow for debugging agyent session issues, turn timeouts,
  stuck agents, subagent failures, high token contexts, and SQLite state. Activate this skill
  whenever a Telegram/Discord session hangs, fails to respond, reports timeouts, or when
  diagnosing database state.
---

# Agyent Session Debugger & Triage Runbook

This skill provides a standardized, step-by-step diagnostic procedure to rapidly identify, triage, and resolve runtime issues in `agyent` gateway sessions without trial-and-error.

---

## 1. Quick Diagnostics (One-Command Triage)

Run the built-in diagnostic tool to immediately inspect all active sessions, token counts, error logs, and subagent task states:

```bash
# Inspect all recent sessions and system health
go run scripts/debug_session.go

# Inspect a specific session key
go run scripts/debug_session.go --session "telegram:8718145628:8544450322"
```

The tool automatically evaluates:
- **Token context bloat** (> 1,000,000 tokens).
- **Subprocess timeout watchdog violations** (duration >= 300s).
- **Session lock bottlenecks** (FIFO lock / active turns).
- **Subagent task lifecycle** (failures, timeouts, path errors).
- **Brain transcript location** (`~/.gemini/antigravity/brain/<conv_id>`).

---

## 2. Standard Triage Flowchart

```
[User Reports Session Issue / Agent Not Responding]
                      │
                      ▼
         1. Run Diagnostics Script
     `go run scripts/debug_session.go`
                      │
        ┌─────────────┴─────────────┐
        ▼                           ▼
[Tokens > 1,000,000]        [Last Status = ERROR]
        │                           │
        ▼                           ▼
Context Bloat / Latency     Check Error Message:
- Suggest user run `/new`   - "context deadline exceeded": Subprocess Timeout
- Or run `/reset` in chat   - "lock": Stale Session Lock (Run `/force_unlock`)
                            - "directory name invalid": Invalid Project Path
```

---

## 3. SQLite Database Inspection Cheatsheet

If inspecting manually via Go or SQLite CLI on `~/.agyent/agyent.db`:

### Inspect Active Sessions & Conversations
```sql
SELECT session_key, active_agent, active_project, global_conversation_id, datetime(updated_at/1000, 'unixepoch', 'localtime') AS updated_at 
FROM sessions 
ORDER BY updated_at DESC;
```

### Inspect Recent Audit Logs & Error Messages
```sql
SELECT id, session_key, agent_name, conversation_id, status, duration_seconds, total_tokens, error_message, datetime(created_at/1000, 'unixepoch', 'localtime') AS created_at 
FROM audit_logs 
ORDER BY id DESC LIMIT 10;
```

### Inspect Background Subagent Tasks
```sql
SELECT id, parent_session_key, agent_name, status, duration_seconds, error_message, datetime(created_at/1000, 'unixepoch', 'localtime') AS created_at 
FROM subagent_tasks 
ORDER BY created_at DESC LIMIT 10;
```

---

## 4. Common Failure Modes & Solutions

### Mode A: Context Length Overload (> 1M tokens) & 5-Minute Timeout
- **Symptom:** Logs show `total_tokens` > 1,000,000, duration > 3000s, followed by `AGY stream execution timed out timeout=5m0s` (`context deadline exceeded`).
- **Root Cause:** As Antigravity conversation context approaches 1.5M–2M tokens, inference latency and tool execution per step increase, exceeding the default 300s watchdog.
- **Fix:**
  1. Tell the user to send `/new` or `/reset` in the Telegram/Discord chat to start a fresh conversation.
  2. The prompt cache will reset to 0 tokens and response time will return to < 5 seconds.

### Mode B: Silent Failure in Streaming Mode (Agent Không Phản Hồi)
- **Symptom:** User sees typing indicator stop and receives no message or error.
- **Root Cause:** When `isStream == true`, if a timeout or error occurs before the stream parser emits events, the error must be emitted as `EventStreamError` on the EventBus so `DeliveryThrottler` can post an update to Telegram.
- **Verification:** Ensure `runner.ExecuteStream` and `DeliveryThrottler.OnStreamError` have error fallback delivery.

### Mode C: Stale Session Lock
- **Symptom:** User sees `⚠️ Could not acquire session lock` or requests hang indefinitely.
- **Root Cause:** A previous turn was cancelled or crashed without releasing the lock.
- **Fix:**
  - Send `/force_unlock` in the chat to cancel active turns and clear the FIFO mutex lock.

### Mode D: Subagent "The directory name is invalid"
- **Symptom:** Subagent tasks fail with `fork/exec ...: The directory name is invalid.`
- **Root Cause:** `task.ProjectName` was set to a project name (e.g. `pod-trends`) rather than an absolute directory path on disk.
- **Fix:** Ensure `taskExecutor` checks `os.Stat(task.ProjectName)` and only sets `cmd.Dir` if it is an existing directory.

---

## 5. Antigravity Brain Transcript Inspection

For in-depth tool sequence or raw prompt investigation:
1. Locate conversation ID from session (`sessions.global_conversation_id`).
2. Open transcript at:
   - Windows: `C:\Users\<User>\.gemini\antigravity\brain\<conv_id>\.system_generated\logs\transcript.jsonl`
   - Linux/macOS: `~/.gemini/antigravity/brain/<conv_id>/.system_generated/logs/transcript.jsonl`
3. Filter tool calls:
   ```powershell
   Select-String -Path "$HOME\.gemini\antigravity\brain\<conv_id>\.system_generated\logs\transcript.jsonl" -Pattern '"tool_calls"'
   ```

---

## 6. Telegram User Recovery Commands Reference

Instruct the user or administrators with these instant commands:
- `/new` — Start a new conversation thread with clean context.
- `/reset` — Clear conversation history for current agent & project.
- `/force_unlock` — Break stale session lock and cancel any hung subprocesses.
- `/status` — View current agent, scope, streaming mode, and health metrics.
- `/tokens` (or `/metrics`) — Check live token usage, KV-cache savings, and turn counts.
- `/tasks` — View active and background subagent tasks.
