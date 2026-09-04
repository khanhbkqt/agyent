-- 000010_agent_schedules_and_heartbeats.down.sql
DROP INDEX IF EXISTS idx_agent_heartbeats_poll;
DROP TABLE IF EXISTS agent_heartbeats;
DROP INDEX IF EXISTS idx_agent_schedules_agent;
DROP INDEX IF EXISTS idx_agent_schedules_poll;
DROP TABLE IF EXISTS agent_schedules;
