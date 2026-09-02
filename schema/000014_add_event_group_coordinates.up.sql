-- Add geographic coordinates to events for radius / nearest searches.
-- groups already has latitude/longitude from 000011_meetup_communities.
ALTER TABLE events ADD COLUMN IF NOT EXISTS latitude double precision;
ALTER TABLE events ADD COLUMN IF NOT EXISTS longitude double precision;

CREATE INDEX IF NOT EXISTS idx_events_lat_lng ON events (latitude, longitude);
