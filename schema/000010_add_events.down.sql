-- Drop event-related tables in reverse order of their creation to avoid foreign key constraint errors

DROP TABLE IF EXISTS event_reminders_sent;
DROP TABLE IF EXISTS event_reports;
DROP TABLE IF EXISTS event_reactions;
DROP TABLE IF EXISTS event_comments;
DROP TABLE IF EXISTS event_attendees;
DROP TABLE IF EXISTS event_coupons;
DROP TABLE IF EXISTS event_ticket_tiers;
DROP TABLE IF EXISTS event_tags;
DROP TABLE IF EXISTS event_permissions;
DROP TABLE IF EXISTS event_host_activity_log;
DROP TABLE IF EXISTS event_co_hosts;
DROP TABLE IF EXISTS events;