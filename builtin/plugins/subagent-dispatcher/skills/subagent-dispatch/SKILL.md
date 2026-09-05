---
name: subagent-dispatch
description: Delegate a concrete independent multi-step or long-running task to an agyent background subagent, then inspect or cancel its task ticket. Use when the user requests delegation or background parallel work; avoid for small sequential tasks.
---

# Background subagent dispatch

Dispatch only a bounded task that can make useful progress independently. Include
the objective, relevant paths/context, expected output, constraints, and a clear
completion condition in `prompt`; the subagent does not inherit every unstated
assumption from the main conversation.

Use `dispatch_subagent` with:

- A concise `title` and complete `prompt`.
- The least-privileged appropriate `agent_name`, model, and effort.
- `workspace_mode: "share"` only when the task must inspect or edit the current
  authorized workspace; otherwise use `scratch`.
- `callback_mode: "notify_user"` for user-facing background work,
  `callback_main` when the main agent must integrate the result, or `silent` only
  when the user does not need a completion notification.

Do not split tightly coupled edits across concurrent shared-workspace tasks. Do
not use delegation to bypass authorization, security presets, or user approval.

Return the task ticket and continue any independent main-track work. Use
`check_subagent_progress` for a requested status check, `list_subagents` for task
discovery, and `cancel_subagent_task` only when the user asks to stop the selected
task or the parent workflow explicitly requires cancellation.

A dispatched ticket is not completion. When the result is needed for the user's
request, wait/check until the task reaches a terminal state and validate its
summary/artifacts before presenting them.
