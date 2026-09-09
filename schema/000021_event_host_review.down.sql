DROP INDEX IF EXISTS idx_event_attendees_host_review;

ALTER TABLE event_attendees
    DROP COLUMN IF EXISTS review_note,
    DROP COLUMN IF EXISTS social_proof_url;

ALTER TABLE events
    DROP COLUMN IF EXISTS requires_host_review;
