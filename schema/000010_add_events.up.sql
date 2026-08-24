-- ================================
-- Event-related Tables
-- ================================

-- Events Table
CREATE TABLE IF NOT EXISTS events (
    id BIGSERIAL PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    slug VARCHAR(255) NOT NULL UNIQUE,
    start_time TIMESTAMP NOT NULL,
    end_time TIMESTAMP NOT NULL,
    timezone VARCHAR(50) DEFAULT 'UTC',
    event_type VARCHAR(50) NOT NULL, -- 'virtual', 'in_person', 'hybrid'
    location VARCHAR(255),           -- physical address
    meeting_link VARCHAR(255),       -- virtual meeting url
    capacity INTEGER DEFAULT 0,      -- 0 means unlimited
    status VARCHAR(50) DEFAULT 'draft', -- 'draft', 'published', 'live', 'completed', 'cancelled'
    cover_image TEXT,
    organizer_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (organizer_id) REFERENCES user_account(id) ON DELETE CASCADE
);

-- Event Host Permissions Model.
-- The creator is the single owner (events.organizer_id). Additional hosts are
-- granted rights by the owner, or by a host holding manage_hosts. Rights are
-- stored explicitly in event_permissions so authorization is one uniform
-- lookup for owner and co-hosts alike, and every change is audited.

-- Active co-hosts.
CREATE TABLE IF NOT EXISTS event_co_hosts (
    event_id BIGINT NOT NULL,
    co_host_id BIGINT NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'co_host', -- 'co_host', 'attendee_manager'
    added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    added_by BIGINT NOT NULL,            -- who added this host (audit)
    PRIMARY KEY (event_id, co_host_id),
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    FOREIGN KEY (co_host_id) REFERENCES user_account(id) ON DELETE CASCADE
);

-- Fine-grained rights per event operator (creator and active co-hosts).
-- Mirrors blog_permissions so access checks are consistent across the platform.
CREATE TABLE IF NOT EXISTS event_permissions (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    permission_type VARCHAR(50) NOT NULL, -- 'edit_event', 'manage_attendees', 'manage_tickets', 'manage_coupons', 'manage_hosts'
    UNIQUE (event_id, user_id, permission_type),
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE
);

-- Audit trail for host management actions.
CREATE TABLE IF NOT EXISTS event_host_activity_log (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    host_id BIGINT,                        -- the host acted upon (nullable if deleted)
    action VARCHAR(50) NOT NULL,           -- 'invited', 'accepted', 'declined', 'revoked', 'removed', 'role_changed'
    performed_by BIGINT NOT NULL,          -- who performed the action
    action_timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    FOREIGN KEY (host_id) REFERENCES user_account(id) ON DELETE CASCADE,
    FOREIGN KEY (performed_by) REFERENCES user_account(id) ON DELETE CASCADE
);

-- Event Tags Table
CREATE TABLE IF NOT EXISTS event_tags (
    event_id BIGINT NOT NULL,
    tag_name VARCHAR(100) NOT NULL,
    PRIMARY KEY (event_id, tag_name),
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);

-- Event Ticket Tiers Table
CREATE TABLE IF NOT EXISTS event_ticket_tiers (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    name VARCHAR(100) NOT NULL, -- e.g., "Early Bird", "VIP", "General"
    description TEXT,
    price DECIMAL(10,2) DEFAULT 0.00,
    currency VARCHAR(10) DEFAULT 'INR',
    capacity INTEGER DEFAULT 0, -- 0 means unlimited
    sort_order INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);

-- Event Coupons Table
CREATE TABLE IF NOT EXISTS event_coupons (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    code VARCHAR(50) NOT NULL,
    discount_percent DECIMAL(5,2) NOT NULL, -- e.g. 20.00 for 20%
    max_uses INTEGER DEFAULT 0, -- 0 means unlimited
    current_uses INTEGER DEFAULT 0,
    valid_from TIMESTAMP,
    valid_to TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (event_id, code),
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);

-- Event Attendees (RSVPs) Table
CREATE TABLE IF NOT EXISTS event_attendees (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    ticket_tier_id BIGINT NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'confirmed', -- 'pending_payment', 'confirmed', 'waitlisted', 'cancelled'
    payment_order_id VARCHAR(255),          -- Razorpay Order ID, the webhook lookup key
    payment_id VARCHAR(255),                -- Razorpay Payment ID, set once captured
    refund_id VARCHAR(255),                 -- Razorpay Refund ID, set on cancellation
    amount_paid DECIMAL(10,2) DEFAULT 0.00, -- price after any coupon discount
    currency VARCHAR(10) DEFAULT 'INR',
    coupon_used VARCHAR(50),
    checked_in BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (event_id, user_id),
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE,
    FOREIGN KEY (ticket_tier_id) REFERENCES event_ticket_tiers(id) ON DELETE CASCADE
);

-- One reservation per payment order; also the webhook's index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_event_attendees_order
    ON event_attendees(payment_order_id) WHERE payment_order_id IS NOT NULL;

-- Event Comments Table
CREATE TABLE IF NOT EXISTS event_comments (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    comment_text TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE
);

-- Event Reactions Table
CREATE TABLE IF NOT EXISTS event_reactions (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    reaction_type VARCHAR(20) NOT NULL, -- e.g., 'like', 'love', 'celebrate'
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (event_id, user_id, reaction_type),
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE
);

-- Event Reports Table
CREATE TABLE IF NOT EXISTS event_reports (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    reason TEXT NOT NULL,
    status VARCHAR(50) DEFAULT 'pending', -- 'pending', 'reviewed', 'action_taken'
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE CASCADE
);

-- Reminder ledger. The primary key is the claim: a replica that manages to
-- insert the row owns sending that reminder, so attendees are nudged once.
CREATE TABLE IF NOT EXISTS event_reminders_sent (
    event_id BIGINT NOT NULL,
    reminder VARCHAR(20) NOT NULL, -- '24h', '1h'
    sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (event_id, reminder),
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);

-- Indexes for common event queries.
CREATE INDEX IF NOT EXISTS idx_events_organizer ON events(organizer_id);
CREATE INDEX IF NOT EXISTS idx_events_status ON events(status);
CREATE INDEX IF NOT EXISTS idx_events_start_time ON events(start_time);
CREATE INDEX IF NOT EXISTS idx_event_attendees_event ON event_attendees(event_id);
CREATE INDEX IF NOT EXISTS idx_event_attendees_user ON event_attendees(user_id);
CREATE INDEX IF NOT EXISTS idx_event_tiers_event ON event_ticket_tiers(event_id);
CREATE INDEX IF NOT EXISTS idx_event_tags_event ON event_tags(event_id);
CREATE INDEX IF NOT EXISTS idx_event_coupons_event ON event_coupons(event_id);
CREATE INDEX IF NOT EXISTS idx_event_cohosts_host ON event_co_hosts(co_host_id);