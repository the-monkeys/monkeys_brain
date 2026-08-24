-- Meetup-like communities and advanced event schema.
--
-- This migration is intentionally additive. Existing standalone events keep
-- working because group_id, venue_id, series_id and the new RSVP settings are
-- nullable or have backward-compatible defaults.

-- ================================
-- Groups / Communities
-- ================================

CREATE TABLE IF NOT EXISTS groups (
    id BIGSERIAL PRIMARY KEY,
    slug VARCHAR(255) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    visibility VARCHAR(30) NOT NULL DEFAULT 'public',
    status VARCHAR(30) NOT NULL DEFAULT 'draft',
    city VARCHAR(120),
    region VARCHAR(120),
    country VARCHAR(120),
    timezone VARCHAR(64) NOT NULL DEFAULT 'UTC',
    latitude DECIMAL(9,6),
    longitude DECIMAL(9,6),
    cover_image TEXT,
    logo_image TEXT,
    organizer_id BIGINT NOT NULL,
    member_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (organizer_id) REFERENCES user_account(id) ON DELETE CASCADE,
    CONSTRAINT chk_groups_name_nonempty CHECK (char_length(trim(name)) > 0),
    CONSTRAINT chk_groups_visibility CHECK (visibility IN ('public', 'private', 'unlisted')),
    CONSTRAINT chk_groups_status CHECK (status IN ('draft', 'published', 'archived', 'suspended')),
    CONSTRAINT chk_groups_member_count_nonnegative CHECK (member_count >= 0)
);

CREATE TABLE IF NOT EXISTS group_members (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    role VARCHAR(40) NOT NULL DEFAULT 'member',
    status VARCHAR(40) NOT NULL DEFAULT 'active',
    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (group_id, user_id),
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    CONSTRAINT chk_group_members_role CHECK (role IN ('organizer', 'co_organizer', 'moderator', 'member')),
    CONSTRAINT chk_group_members_status CHECK (status IN ('active', 'pending', 'left', 'removed', 'banned'))
);

CREATE TABLE IF NOT EXISTS group_permissions (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    permission_type VARCHAR(80) NOT NULL,
    UNIQUE (group_id, user_id, permission_type),
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    CONSTRAINT chk_group_permissions_type_nonempty CHECK (char_length(trim(permission_type)) > 0)
);

CREATE TABLE IF NOT EXISTS group_topics (
    group_id BIGINT NOT NULL,
    topic_name VARCHAR(100) NOT NULL,
    PRIMARY KEY (group_id, topic_name),
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    CONSTRAINT chk_group_topics_name_nonempty CHECK (char_length(trim(topic_name)) > 0)
);

CREATE TABLE IF NOT EXISTS group_join_requests (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    answers JSONB,
    status VARCHAR(40) NOT NULL DEFAULT 'pending',
    decided_by BIGINT,
    decided_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (group_id, user_id),
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    FOREIGN KEY (decided_by) REFERENCES user_account(id) ON DELETE SET NULL,
    CONSTRAINT chk_group_join_requests_status CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled'))
);

CREATE TABLE IF NOT EXISTS group_bans (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    reason TEXT,
    banned_by BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (group_id, user_id),
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    FOREIGN KEY (banned_by) REFERENCES user_account(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS group_rules (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    title VARCHAR(120) NOT NULL,
    body TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    CONSTRAINT chk_group_rules_title_nonempty CHECK (char_length(trim(title)) > 0),
    CONSTRAINT chk_group_rules_body_nonempty CHECK (char_length(trim(body)) > 0)
);

CREATE INDEX IF NOT EXISTS idx_groups_status ON groups(status);
CREATE INDEX IF NOT EXISTS idx_groups_location ON groups(country, region, city);
CREATE INDEX IF NOT EXISTS idx_groups_geo ON groups(latitude, longitude);
CREATE INDEX IF NOT EXISTS idx_groups_name_trgm ON groups USING GIN (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_group_members_user ON group_members(user_id);
CREATE INDEX IF NOT EXISTS idx_group_members_group_status ON group_members(group_id, status);
CREATE INDEX IF NOT EXISTS idx_group_topics_topic ON group_topics(topic_name);
CREATE INDEX IF NOT EXISTS idx_group_join_requests_group_status ON group_join_requests(group_id, status);
CREATE INDEX IF NOT EXISTS idx_group_bans_user ON group_bans(user_id);
CREATE INDEX IF NOT EXISTS idx_group_permissions_user ON group_permissions(user_id);

-- ================================
-- Venues / Locations
-- ================================

CREATE TABLE IF NOT EXISTS venues (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    address_line1 VARCHAR(255),
    address_line2 VARCHAR(255),
    city VARCHAR(120),
    region VARCHAR(120),
    country VARCHAR(120),
    postal_code VARCHAR(40),
    latitude DECIMAL(9,6),
    longitude DECIMAL(9,6),
    source VARCHAR(40) NOT NULL DEFAULT 'manual',
    created_by BIGINT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (created_by) REFERENCES user_account(id) ON DELETE SET NULL,
    CONSTRAINT chk_venues_name_nonempty CHECK (char_length(trim(name)) > 0),
    CONSTRAINT chk_venues_source CHECK (source IN ('manual', 'imported', 'provider'))
);

CREATE INDEX IF NOT EXISTS idx_venues_location ON venues(country, region, city);
CREATE INDEX IF NOT EXISTS idx_venues_geo ON venues(latitude, longitude);
CREATE INDEX IF NOT EXISTS idx_venues_name_trgm ON venues USING GIN (name gin_trgm_ops);

-- ================================
-- Event table additive columns
-- ================================

ALTER TABLE events ADD COLUMN IF NOT EXISTS group_id BIGINT NULL REFERENCES groups(id) ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN IF NOT EXISTS visibility VARCHAR(30) NOT NULL DEFAULT 'public';
ALTER TABLE events ADD COLUMN IF NOT EXISTS rsvp_opens_at TIMESTAMP NULL;
ALTER TABLE events ADD COLUMN IF NOT EXISTS rsvp_closes_at TIMESTAMP NULL;
ALTER TABLE events ADD COLUMN IF NOT EXISTS allow_guests BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE events ADD COLUMN IF NOT EXISTS max_guests_per_rsvp INTEGER NOT NULL DEFAULT 0;
ALTER TABLE events ADD COLUMN IF NOT EXISTS venue_id BIGINT NULL REFERENCES venues(id) ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN IF NOT EXISTS how_to_find_us TEXT;

ALTER TABLE events
    ADD CONSTRAINT chk_events_visibility CHECK (visibility IN ('public', 'group_members', 'private', 'unlisted'));

ALTER TABLE events
    ADD CONSTRAINT chk_events_max_guests_nonnegative CHECK (max_guests_per_rsvp >= 0);

CREATE INDEX IF NOT EXISTS idx_events_group ON events(group_id);
CREATE INDEX IF NOT EXISTS idx_events_venue ON events(venue_id);
CREATE INDEX IF NOT EXISTS idx_events_visibility ON events(visibility);
CREATE INDEX IF NOT EXISTS idx_events_rsvp_window ON events(rsvp_opens_at, rsvp_closes_at);

-- ================================
-- Recurring Events
-- ================================

CREATE TABLE IF NOT EXISTS event_series (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT,
    organizer_id BIGINT NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    timezone VARCHAR(64) NOT NULL DEFAULT 'UTC',
    recurrence_rule TEXT NOT NULL,
    recurrence_starts_at TIMESTAMP NOT NULL,
    recurrence_ends_at TIMESTAMP NULL,
    status VARCHAR(40) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE SET NULL,
    FOREIGN KEY (organizer_id) REFERENCES user_account(id) ON DELETE CASCADE,
    CONSTRAINT chk_event_series_title_nonempty CHECK (char_length(trim(title)) > 0),
    CONSTRAINT chk_event_series_rule_nonempty CHECK (char_length(trim(recurrence_rule)) > 0),
    CONSTRAINT chk_event_series_status CHECK (status IN ('active', 'paused', 'completed', 'cancelled'))
);

ALTER TABLE events ADD COLUMN IF NOT EXISTS series_id BIGINT NULL REFERENCES event_series(id) ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN IF NOT EXISTS series_occurrence_at TIMESTAMP NULL;

CREATE INDEX IF NOT EXISTS idx_event_series_group ON event_series(group_id);
CREATE INDEX IF NOT EXISTS idx_event_series_organizer ON event_series(organizer_id);
CREATE INDEX IF NOT EXISTS idx_events_series ON events(series_id, series_occurrence_at);

-- ================================
-- Registration Questions / Attendance
-- ================================

CREATE TABLE IF NOT EXISTS event_questions (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    question_text TEXT NOT NULL,
    question_type VARCHAR(40) NOT NULL DEFAULT 'text',
    required BOOLEAN NOT NULL DEFAULT FALSE,
    options JSONB,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    CONSTRAINT chk_event_questions_text_nonempty CHECK (char_length(trim(question_text)) > 0),
    CONSTRAINT chk_event_questions_type CHECK (question_type IN ('text', 'textarea', 'single_choice', 'multi_choice', 'checkbox'))
);

ALTER TABLE event_attendees ADD COLUMN IF NOT EXISTS guest_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_attendees ADD COLUMN IF NOT EXISTS attendance_status VARCHAR(40) NOT NULL DEFAULT 'registered';
ALTER TABLE event_attendees ADD COLUMN IF NOT EXISTS checked_in_at TIMESTAMP NULL;
ALTER TABLE event_attendees ADD COLUMN IF NOT EXISTS checked_in_by BIGINT NULL REFERENCES user_account(id) ON DELETE SET NULL;

ALTER TABLE event_attendees
    ADD CONSTRAINT chk_event_attendees_guest_count_nonnegative CHECK (guest_count >= 0);

ALTER TABLE event_attendees
    ADD CONSTRAINT chk_event_attendees_attendance_status CHECK (attendance_status IN ('registered', 'checked_in', 'no_show', 'not_coming'));

CREATE TABLE IF NOT EXISTS event_question_answers (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    attendee_id BIGINT NOT NULL,
    question_id BIGINT NOT NULL,
    answer JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (attendee_id, question_id),
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    FOREIGN KEY (attendee_id) REFERENCES event_attendees(id) ON DELETE CASCADE,
    FOREIGN KEY (question_id) REFERENCES event_questions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_event_questions_event ON event_questions(event_id);
CREATE INDEX IF NOT EXISTS idx_event_question_answers_event ON event_question_answers(event_id);
CREATE INDEX IF NOT EXISTS idx_event_attendees_attendance_status ON event_attendees(event_id, attendance_status);
CREATE INDEX IF NOT EXISTS idx_event_attendees_checked_in_by ON event_attendees(checked_in_by);

-- ================================
-- Messaging / Discussions
-- ================================

CREATE TABLE IF NOT EXISTS message_threads (
    id BIGSERIAL PRIMARY KEY,
    thread_type VARCHAR(40) NOT NULL,
    group_id BIGINT,
    event_id BIGINT,
    created_by BIGINT NOT NULL,
    title VARCHAR(255),
    status VARCHAR(40) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    FOREIGN KEY (created_by) REFERENCES user_account(id) ON DELETE CASCADE,
    CONSTRAINT chk_message_threads_type CHECK (thread_type IN ('direct', 'group_discussion', 'event_discussion', 'event_announcement')),
    CONSTRAINT chk_message_threads_status CHECK (status IN ('active', 'closed', 'hidden')),
    CONSTRAINT chk_message_threads_scope CHECK (
        (thread_type = 'direct' AND group_id IS NULL AND event_id IS NULL)
        OR (thread_type = 'group_discussion' AND group_id IS NOT NULL)
        OR (thread_type IN ('event_discussion', 'event_announcement') AND event_id IS NOT NULL)
    )
);

CREATE TABLE IF NOT EXISTS message_thread_members (
    thread_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    last_read_at TIMESTAMP,
    muted BOOLEAN NOT NULL DEFAULT FALSE,
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (thread_id, user_id),
    FOREIGN KEY (thread_id) REFERENCES message_threads(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS messages (
    id BIGSERIAL PRIMARY KEY,
    thread_id BIGINT NOT NULL,
    sender_id BIGINT NOT NULL,
    body TEXT NOT NULL,
    status VARCHAR(40) NOT NULL DEFAULT 'visible',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (thread_id) REFERENCES message_threads(id) ON DELETE CASCADE,
    FOREIGN KEY (sender_id) REFERENCES user_account(id) ON DELETE CASCADE,
    CONSTRAINT chk_messages_body_nonempty CHECK (char_length(trim(body)) > 0),
    CONSTRAINT chk_messages_status CHECK (status IN ('visible', 'deleted', 'hidden', 'reported'))
);

CREATE INDEX IF NOT EXISTS idx_message_threads_group ON message_threads(group_id);
CREATE INDEX IF NOT EXISTS idx_message_threads_event ON message_threads(event_id);
CREATE INDEX IF NOT EXISTS idx_message_threads_created_by ON message_threads(created_by);
CREATE INDEX IF NOT EXISTS idx_message_thread_members_user ON message_thread_members(user_id, archived, muted);
CREATE INDEX IF NOT EXISTS idx_messages_thread_created ON messages(thread_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender_id);

-- ================================
-- Subscriptions / Dues / Entitlements
-- ================================

CREATE TABLE IF NOT EXISTS plans (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(80) NOT NULL UNIQUE,
    name VARCHAR(120) NOT NULL,
    plan_type VARCHAR(40) NOT NULL,
    price_minor BIGINT NOT NULL DEFAULT 0,
    currency VARCHAR(10) NOT NULL DEFAULT 'INR',
    billing_interval VARCHAR(40) NOT NULL DEFAULT 'month',
    active BOOLEAN NOT NULL DEFAULT TRUE,
    entitlements JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_plans_code_nonempty CHECK (char_length(trim(code)) > 0),
    CONSTRAINT chk_plans_name_nonempty CHECK (char_length(trim(name)) > 0),
    CONSTRAINT chk_plans_price_nonnegative CHECK (price_minor >= 0),
    CONSTRAINT chk_plans_type CHECK (plan_type IN ('organizer', 'member', 'pro')),
    CONSTRAINT chk_plans_billing_interval CHECK (billing_interval IN ('none', 'month', 'year'))
);

CREATE TABLE IF NOT EXISTS organizer_subscriptions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    plan_id BIGINT NOT NULL,
    status VARCHAR(40) NOT NULL DEFAULT 'active',
    provider VARCHAR(40) NOT NULL DEFAULT 'razorpay',
    provider_subscription_id VARCHAR(255),
    current_period_start TIMESTAMP,
    current_period_end TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    FOREIGN KEY (plan_id) REFERENCES plans(id) ON DELETE RESTRICT,
    CONSTRAINT chk_organizer_subscriptions_status CHECK (status IN ('active', 'past_due', 'cancelled', 'expired', 'trialing')),
    CONSTRAINT chk_organizer_subscriptions_provider CHECK (provider IN ('razorpay', 'manual'))
);

CREATE TABLE IF NOT EXISTS group_dues (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'INR',
    billing_interval VARCHAR(40) NOT NULL DEFAULT 'month',
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    CONSTRAINT chk_group_dues_amount_positive CHECK (amount_minor > 0),
    CONSTRAINT chk_group_dues_billing_interval CHECK (billing_interval IN ('month', 'year'))
);

CREATE TABLE IF NOT EXISTS group_due_payments (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'INR',
    provider VARCHAR(40) NOT NULL DEFAULT 'razorpay',
    provider_payment_id VARCHAR(255),
    status VARCHAR(40) NOT NULL DEFAULT 'pending',
    period_start TIMESTAMP,
    period_end TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    CONSTRAINT chk_group_due_payments_amount_nonnegative CHECK (amount_minor >= 0),
    CONSTRAINT chk_group_due_payments_provider CHECK (provider IN ('razorpay', 'manual')),
    CONSTRAINT chk_group_due_payments_status CHECK (status IN ('pending', 'paid', 'failed', 'refunded', 'cancelled'))
);

CREATE INDEX IF NOT EXISTS idx_organizer_subscriptions_user ON organizer_subscriptions(user_id, status);
CREATE INDEX IF NOT EXISTS idx_group_dues_group ON group_dues(group_id, active);
CREATE INDEX IF NOT EXISTS idx_group_due_payments_group_user ON group_due_payments(group_id, user_id);
CREATE INDEX IF NOT EXISTS idx_group_due_payments_provider_payment ON group_due_payments(provider_payment_id);

-- ================================
-- Saves / Interests / Discovery
-- ================================

CREATE TABLE IF NOT EXISTS user_interests (
    user_id BIGINT NOT NULL,
    topic_name VARCHAR(100) NOT NULL,
    PRIMARY KEY (user_id, topic_name),
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    CONSTRAINT chk_user_interests_topic_nonempty CHECK (char_length(trim(topic_name)) > 0)
);

CREATE TABLE IF NOT EXISTS saved_events (
    user_id BIGINT NOT NULL,
    event_id BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, event_id),
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS saved_groups (
    user_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, group_id),
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_interests_topic ON user_interests(topic_name);
CREATE INDEX IF NOT EXISTS idx_saved_events_event ON saved_events(event_id);
CREATE INDEX IF NOT EXISTS idx_saved_groups_group ON saved_groups(group_id);

-- ================================
-- Analytics
-- ================================

CREATE TABLE IF NOT EXISTS event_analytics_daily (
    event_id BIGINT NOT NULL,
    day DATE NOT NULL,
    views INTEGER NOT NULL DEFAULT 0,
    shares INTEGER NOT NULL DEFAULT 0,
    saves INTEGER NOT NULL DEFAULT 0,
    rsvps INTEGER NOT NULL DEFAULT 0,
    cancellations INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (event_id, day),
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    CONSTRAINT chk_event_analytics_nonnegative CHECK (
        views >= 0 AND shares >= 0 AND saves >= 0 AND rsvps >= 0 AND cancellations >= 0
    )
);

CREATE TABLE IF NOT EXISTS group_analytics_daily (
    group_id BIGINT NOT NULL,
    day DATE NOT NULL,
    views INTEGER NOT NULL DEFAULT 0,
    joins INTEGER NOT NULL DEFAULT 0,
    leaves INTEGER NOT NULL DEFAULT 0,
    event_views INTEGER NOT NULL DEFAULT 0,
    event_rsvps INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, day),
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    CONSTRAINT chk_group_analytics_nonnegative CHECK (
        views >= 0 AND joins >= 0 AND leaves >= 0 AND event_views >= 0 AND event_rsvps >= 0
    )
);
