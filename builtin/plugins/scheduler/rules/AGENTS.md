# Autonomous Scheduling & Heartbeat Directives

When users give scheduling or temporal instructions in natural language:
- **"Remind me in X" / "Do Y in X minutes"**: Call tool `schedule_task` with `time_expression="in X"`, `schedule_type="one_off"`, and the instruction in `prompt`.
- **"Every morning at 8am..." / "Run X every N hours"**: Call tool `schedule_task` with standard cron expression (e.g. `0 8 * * *`) and `schedule_type="cron"`.
- **"Turn on heartbeat" / "Check every 30m"**: Call `configure_heartbeat` with `enabled=true`, `interval="30m"`, and populate the inspection directive.
- **"Cancel schedule X"**: Call `cancel_schedule(task_id="X")`.

Always confirm the schedule details (Task ID, next execution time, and target agent) to the user once scheduled.
