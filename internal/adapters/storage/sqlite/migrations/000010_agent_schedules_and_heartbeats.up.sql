-- 000010_agent_schedules_and_heartbeats.up.sql
-- Unified tables for agent schedules, recurring cron tasks, and heartbeats.

CREATE TABLE IF NOT EXISTS agent_schedules (
    id TEXT PRIMARY KEY,                                      -- Task UUID / slug (e.g. "sched-8a4b2c1d" or "cron-5b1297cd")
    agent_name TEXT NOT NULL,                                 -- Agent persona identifier
    title TEXT NOT NULL,                                      -- Human-readable task summary
    schedule_type TEXT NOT NULL,                              -- 'once' | 'cron'
    schedule_expr TEXT NOT NULL,                              -- cron syntax ("0 8 * * *") or delay expression ("in 45m", RFC3339)
    prompt TEXT NOT NULL,                                     -- Detailed instruction prompt
    target_session_key TEXT NOT NULL,                         -- Reusable session key (e.g. "sched:agent:task_id")
    chat_id TEXT NOT NULL,                                    -- Destination chat identifier
    thread_id TEXT NOT NULL DEFAULT '',                       -- Destination message thread/topic ID
    channel TEXT NOT NULL DEFAULT 'telegram',                 -- Channel provider (telegram, discord)
    status TEXT NOT NULL DEFAULT 'ACTIVE',                    -- ACTIVE | RUNNING | PAUSED | COMPLETED | FAILED | CANCELLED
    overlap_policy TEXT NOT NULL DEFAULT 'skip',              -- 'skip' | 'cancel_previous' | 'queue'
    misfire_policy TEXT NOT NULL DEFAULT 'skip_to_latest',    -- 'skip_to_latest' | 'run_once'
    next_run_at INTEGER NOT NULL,                             -- Unix Milliseconds (FlexTime)
    last_run_at INTEGER NOT NULL DEFAULT 0,                   -- Unix Milliseconds (FlexTime)
    run_count INTEGER NOT NULL DEFAULT 0,                     -- Execution counter
    max_runs INTEGER NOT NULL DEFAULT 0,                      -- 0 = unlimited (for recurring), 1 for once
    last_error TEXT NOT NULL DEFAULT '',                      -- Error message from most recent failed run
    created_by TEXT NOT NULL,                                 -- User ID of creator (for RBAC)
    created_at INTEGER NOT NULL,                              -- Unix Milliseconds
    updated_at INTEGER NOT NULL                               -- Unix Milliseconds
);

CREATE INDEX IF NOT EXISTS idx_agent_schedules_poll
ON agent_schedules(status, next_run_at);

CREATE INDEX IF NOT EXISTS idx_agent_schedules_agent
ON agent_schedules(agent_name, status);

CREATE TABLE IF NOT EXISTS agent_heartbeats (
    agent_name TEXT PRIMARY KEY,                              -- Agent persona identifier
    enabled INTEGER NOT NULL DEFAULT 0,                       -- 1 = enabled, 0 = disabled
    interval_seconds INTEGER NOT NULL DEFAULT 3600,           -- Wakeup interval in seconds
    target_session_key TEXT NOT NULL DEFAULT '',              -- Target session key
    chat_id TEXT NOT NULL DEFAULT '',                         -- Target chat ID
    thread_id TEXT NOT NULL DEFAULT '',                       -- Target thread ID
    channel TEXT NOT NULL DEFAULT 'telegram',                 -- Channel provider
    status TEXT NOT NULL DEFAULT 'IDLE',                      -- IDLE | RUNNING
    last_run_at INTEGER NOT NULL DEFAULT 0,                   -- Unix Milliseconds (FlexTime)
    next_run_at INTEGER NOT NULL DEFAULT 0,                   -- Unix Milliseconds (FlexTime)
    last_error TEXT NOT NULL DEFAULT '',                      -- Error details if last run failed
    updated_at INTEGER NOT NULL                               -- Unix Milliseconds
);

CREATE INDEX IF NOT EXISTS idx_agent_heartbeats_poll
ON agent_heartbeats(enabled, status, next_run_at);
