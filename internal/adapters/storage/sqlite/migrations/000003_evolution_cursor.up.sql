-- Add last_reflected_step cursor to conversations table for incremental delta evolution tracking
ALTER TABLE conversations ADD COLUMN last_reflected_step INTEGER NOT NULL DEFAULT 0;
