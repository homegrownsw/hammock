-- Migration number: 0001 	 2026-11-09
-- When each device was registered or last sent to (to the day), so ones
-- nothing uses are forgotten by the daily cron trigger. Existing rows
-- count from when they registered. (SQLite can't add a column with a
-- datetime('now') default: the worker sets it on every insert.)
ALTER TABLE devices ADD COLUMN last_used TEXT;
UPDATE devices SET last_used = created WHERE last_used IS NULL;
CREATE INDEX IF NOT EXISTS devices_last_used ON devices (last_used);
