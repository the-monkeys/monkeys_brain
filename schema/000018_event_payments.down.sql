ALTER TABLE event_attendees
    DROP COLUMN IF EXISTS amount_captured_paise,
    DROP COLUMN IF EXISTS amount_due_paise;

DROP TABLE IF EXISTS event_host_settlements;
DROP TABLE IF EXISTS event_payments;
