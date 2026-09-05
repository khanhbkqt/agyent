# Scheduler plugin rules

- Interpret schedule times in the user's known timezone and report the effective
  next run with timezone.
- Persist a self-contained prompt; future execution must not depend on hidden
  context from the creation turn.
- Do not create, cancel, trigger, or reconfigure a schedule/heartbeat without a
  corresponding user request.
- Report the returned task ID and effective agent after creation or cancellation.
