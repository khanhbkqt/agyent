---
name: schedule-management
description: >-
  Schedule delayed one-off reminders, recurring cron jobs, and configure agent heartbeats for automated autonomous wakeups.
---

# Schedule, Cron & Heartbeat Management

Use this skill when the user asks to:
1. **Schedule a delayed or future prompt / reminder** (e.g. *"Remind me in 30 minutes to check deployment"*, *"Run this test after 2 hours"*).
2. **Setup recurring cron tasks** (e.g. *"Run git pull and summarize changes every day at 9am"*, *"Check server health every hour"*).
3. **Configure agent heartbeat** (e.g. *"Turn on heartbeat every 15 minutes"*, *"Update heartbeat instructions to monitor logs"*).
4. **List or cancel active scheduled tasks**.

---

## Tool Guidance

### 1. One-off Scheduled Tasks (`schedule_task`)
- Use `schedule_task` with a relative delay or timestamp:
  - `time_expression`: `"in 30m"`, `"after 2h"`, `"in 45s"`, or ISO timestamp `"2026-09-05T09:00:00Z"`.
  - `schedule_type`: `"one_off"`.
  - `prompt`: Write a clear, self-contained instruction for the agent to execute when waking up.
  - `title`: Short title (e.g. `Deployment Check`).

### 2. Recurring Cron Tasks (`schedule_task`)
- Use `schedule_task` with standard 5-field cron or presets:
  - `time_expression`: `"0 8 * * *"` (every day at 8:00 AM), `"*/30 * * * *"` (every 30 mins), `"0 0 * * 1"` (every Monday at midnight), `@daily`, `@hourly`.
  - `schedule_type`: `"cron"`.
  - `prompt`: Instruction for the recurring task.
  - `overlap_policy`: `"skip"` (default, avoid thundering herd).

### 3. Agent Heartbeats (`configure_heartbeat`, `get_heartbeat`, `trigger_heartbeat`)
- Heartbeat is a lightweight background pulse driven by the workspace prompt `HEARTBEAT.md`.
- To turn on:
  ```json
  {"enabled": true, "interval": "30m", "prompt": "Check ongoing background tasks and notify user if any failed."}
  ```
- To turn off:
  ```json
  {"enabled": false}
  ```
- To trigger immediately:
  Call `trigger_heartbeat`.

### 4. Listing & Cancelling
- List tasks using `list_schedules`.
- Cancel a task using `cancel_schedule` with the `task_id` ticket.
