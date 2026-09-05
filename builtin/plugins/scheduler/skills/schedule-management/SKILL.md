---
name: schedule-management
description: Create, list, cancel, or inspect one-off schedules, recurring cron tasks, and agent heartbeats. Use when the user asks for a reminder, future/recurring execution, or heartbeat configuration.
---

# Schedule and heartbeat management

Translate the user's time request using their known timezone. If an absolute time
is ambiguous and the timezone cannot be inferred from context, ask for the missing
timezone before creating the task.

## One-off and recurring tasks

Use `schedule_task` with a self-contained `prompt` and `time_expression`:

- Relative or absolute one-time execution: `schedule_type: "one_off"`; expressions
  can include `in 30m`, `after 2h`, or an ISO timestamp.
- Recurring execution: `schedule_type: "cron"`; use a standard five-field cron or
  a supported preset such as `@daily`.
- When the expression makes the type obvious, `schedule_type: "auto"` is valid.
- Default `overlap_policy` to `skip` unless the user explicitly needs queued work
  or cancellation of the previous run.

Do not silently turn an immediate request into a scheduled task. The stored prompt
must contain the context needed at wake time without relying on the current chat
turn.

After creation, report the task ID, interpreted next run time with timezone, target
agent, recurrence, and overlap policy from the tool response.

## Listing and cancellation

- Use `list_schedules`; narrow by agent/status when the user supplied them.
- Use `cancel_schedule` only for the exact `task_id` the user selected. If a name
  matches several tasks, list them before cancelling.

## Heartbeats

- Use `configure_heartbeat` to enable/disable, set interval, or update the
  `HEARTBEAT.md` instruction.
- Use `get_heartbeat` to inspect current state.
- Use `trigger_heartbeat` only when the user asks for an immediate pulse; enabling
  a heartbeat does not imply an immediate run.

Report the effective interval, target agent, next run, and whether instructions
were changed.
