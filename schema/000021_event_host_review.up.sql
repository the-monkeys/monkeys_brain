-- Optional host screening before a guest occupies a seat or pays.
-- Default off keeps today's open RSVP. pending_host_review rows do not
-- count against capacity (see seatsExhausted).

ALTER TABLE events
    ADD COLUMN IF NOT EXISTS requires_host_review BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE event_attendees
    ADD COLUMN IF NOT EXISTS social_proof_url TEXT,
    ADD COLUMN IF NOT EXISTS review_note TEXT;

CREATE INDEX IF NOT EXISTS idx_event_attendees_host_review
    ON event_attendees (event_id)
    WHERE status = 'pending_host_review';
