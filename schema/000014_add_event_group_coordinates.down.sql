-- Reverse 000014: drop only what the up migration created.
DROP INDEX IF EXISTS idx_events_lat_lng;

ALTER TABLE events DROP COLUMN IF EXISTS latitude;
ALTER TABLE events DROP COLUMN IF EXISTS longitude;
