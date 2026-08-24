-- Roll back Meetup-like communities and advanced event schema.
-- Tables are dropped in dependency order. Existing event columns added by the
-- up migration are removed after dependent tables are gone.

DROP TABLE IF EXISTS group_analytics_daily;
DROP TABLE IF EXISTS event_analytics_daily;

DROP TABLE IF EXISTS saved_groups;
DROP TABLE IF EXISTS saved_events;
DROP TABLE IF EXISTS user_interests;

DROP TABLE IF EXISTS group_due_payments;
DROP TABLE IF EXISTS group_dues;
DROP TABLE IF EXISTS organizer_subscriptions;
DROP TABLE IF EXISTS plans;

DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS message_thread_members;
DROP TABLE IF EXISTS message_threads;

DROP TABLE IF EXISTS event_question_answers;
DROP TABLE IF EXISTS event_questions;

DROP INDEX IF EXISTS idx_event_attendees_checked_in_by;
DROP INDEX IF EXISTS idx_event_attendees_attendance_status;
ALTER TABLE event_attendees DROP CONSTRAINT IF EXISTS chk_event_attendees_attendance_status;
ALTER TABLE event_attendees DROP CONSTRAINT IF EXISTS chk_event_attendees_guest_count_nonnegative;
ALTER TABLE event_attendees DROP COLUMN IF EXISTS checked_in_by;
ALTER TABLE event_attendees DROP COLUMN IF EXISTS checked_in_at;
ALTER TABLE event_attendees DROP COLUMN IF EXISTS attendance_status;
ALTER TABLE event_attendees DROP COLUMN IF EXISTS guest_count;

DROP INDEX IF EXISTS idx_events_series;
ALTER TABLE events DROP COLUMN IF EXISTS series_occurrence_at;
ALTER TABLE events DROP COLUMN IF EXISTS series_id;
DROP TABLE IF EXISTS event_series;

DROP INDEX IF EXISTS idx_events_rsvp_window;
DROP INDEX IF EXISTS idx_events_visibility;
DROP INDEX IF EXISTS idx_events_venue;
DROP INDEX IF EXISTS idx_events_group;
ALTER TABLE events DROP CONSTRAINT IF EXISTS chk_events_max_guests_nonnegative;
ALTER TABLE events DROP CONSTRAINT IF EXISTS chk_events_visibility;
ALTER TABLE events DROP COLUMN IF EXISTS how_to_find_us;
ALTER TABLE events DROP COLUMN IF EXISTS venue_id;
ALTER TABLE events DROP COLUMN IF EXISTS max_guests_per_rsvp;
ALTER TABLE events DROP COLUMN IF EXISTS allow_guests;
ALTER TABLE events DROP COLUMN IF EXISTS rsvp_closes_at;
ALTER TABLE events DROP COLUMN IF EXISTS rsvp_opens_at;
ALTER TABLE events DROP COLUMN IF EXISTS visibility;
ALTER TABLE events DROP COLUMN IF EXISTS group_id;

DROP TABLE IF EXISTS venues;

DROP TABLE IF EXISTS group_rules;
DROP TABLE IF EXISTS group_bans;
DROP TABLE IF EXISTS group_join_requests;
DROP TABLE IF EXISTS group_topics;
DROP TABLE IF EXISTS group_permissions;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;
