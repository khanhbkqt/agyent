-- 000014_schedule_task_timeout.down.sql
-- Revert timeout_seconds column from agent_schedules.

ALTER TABLE agent_schedules DROP COLUMN timeout_seconds;
