---
name: agyent-session-debugger
description: Diagnose agyent Telegram sessions, missing replies, stuck or timed-out turns, subagent failures, token growth, and SQLite runtime state. Use for incident triage and root-cause analysis; do not use for ordinary feature development.
---

# Agyent session debugger

Determine the failure from read-only evidence before recommending or applying
recovery. The user's request takes precedence over this runbook.

## Triage

1. Capture the exact session key, symptom, approximate time, and whether the issue
   affects one conversation or the whole daemon.
2. Run the built-in health checks without live network calls unless connectivity
   is the suspected problem:

   ```bash
   agyent doctor --skip-network
   agyent doctor --session '<session-key>' --skip-network
   ```

3. Inspect persisted session, turn, and subagent evidence:

   ```bash
   go run ./scripts/debug_session.go --session '<session-key>'
   ```

   Pass `--db <path>` when runtime configuration uses a non-default database.

4. Correlate the latest failed audit row with structured daemon logs. Distinguish
   an active long-running turn from a stale lock, timeout, stream-delivery error,
   authorization denial, AGY failure, or subagent state failure.
5. Use the conversation ID from the session/audit row to inspect
   `~/.gemini/antigravity/brain/<conversation-id>/.system_generated/logs/transcript.jsonl`
   when tool/stream sequence evidence is needed. Do not expose secrets or full
   transcripts in the report.

## Evidence interpretation

- `authorization denied`, missing active turn, or security decision errors:
  inspect principal/resource identity and security audit events. Do not weaken the
  preset to make the symptom disappear.
- `context deadline exceeded` or AGY timeout: compare duration with the configured
  timeout and inspect the last transcript activity. Token count alone does not
  prove context overload.
- Lock-related errors: establish whether a process/turn is still active before
  calling the lock stale.
- No Telegram reply with a completed/failed turn: inspect terminal stream events,
  EventBus delivery, throttler state, target context, and Telegram API errors.
- Subagent failure: inspect task status, compare-and-swap transitions,
  `error_message`, workspace mode/path, and process cleanup.
- Database errors: inspect schema version, WAL files, busy/constraint errors, and
  whether the query used the correct tenant scope and pool.

## Recovery boundary

Diagnosis does not authorize mutation. `/force_unlock`, conversation reset,
database repair, process termination, security-preset changes, or task
cancellation are recovery actions. Recommend the narrowest action supported by
evidence and apply it only when the user requested remediation.

After recovery, verify the original entry point with a harmless turn and confirm
that a terminal response/event is recorded.

## Report format

Return:

1. Observed symptom and affected scope.
2. Evidence with timestamps/session/conversation/task IDs, redacted as needed.
3. Root cause, or the remaining hypotheses if evidence is inconclusive.
4. Recovery performed or the exact recommended action.
5. Verification result and preventive code/config change, if applicable.
