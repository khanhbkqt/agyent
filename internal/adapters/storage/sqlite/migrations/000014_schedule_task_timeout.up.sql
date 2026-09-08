-- 000014_schedule_task_timeout.up.sql
-- Add timeout_seconds column to agent_schedules to support custom per-task execution deadlines.

ALTER TABLE agent_schedules ADD COLUMN timeout_seconds INTEGER NOT NULL DEFAULT 0;
