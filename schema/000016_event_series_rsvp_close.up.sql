-- Relative RSVP close for a recurring series (hours before each occurrence
-- start). NULL/0 means no extra close; events.rsvp_closes_at stays the
-- per-date timestamp that CreateRSVP enforces.
ALTER TABLE event_series ADD COLUMN IF NOT EXISTS rsvp_close_hours_before INTEGER NULL;
