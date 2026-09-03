-- Speeds discovery collapse (soonest upcoming row per series) and the
-- per-row confirmed-attendee COUNT used by list + popular sort.
CREATE INDEX IF NOT EXISTS idx_events_series_upcoming
	ON events (series_id, start_time)
	WHERE series_id IS NOT NULL AND status IN ('published', 'live');

CREATE INDEX IF NOT EXISTS idx_event_attendees_confirmed
	ON event_attendees (event_id)
	WHERE status = 'confirmed';
