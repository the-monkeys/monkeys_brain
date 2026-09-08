-- Capture ledger (GST Option A) and host settlement bookkeeping.
-- Additive: existing event_attendees.amount_paid stays for old readers.

CREATE TABLE event_payments (
    id                      BIGSERIAL PRIMARY KEY,
    event_id                BIGINT NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    attendee_id             BIGINT NOT NULL REFERENCES event_attendees(id) ON DELETE RESTRICT,
    organizer_user_id       BIGINT NOT NULL REFERENCES user_account(id) ON DELETE RESTRICT,
    currency                VARCHAR(3) NOT NULL DEFAULT 'INR',
    gross_paise             BIGINT NOT NULL CHECK (gross_paise >= 0),
    fee_bps                 INTEGER NOT NULL,
    gst_bps                 INTEGER NOT NULL,
    platform_fee_paise      BIGINT NOT NULL CHECK (platform_fee_paise >= 0),
    gst_paise               BIGINT NOT NULL CHECK (gst_paise >= 0),
    host_payable_paise      BIGINT NOT NULL CHECK (host_payable_paise >= 0),
    razorpay_order_id       VARCHAR(255) NOT NULL,
    razorpay_payment_id     VARCHAR(255) NOT NULL,
    status                  VARCHAR(32) NOT NULL,
    refund_paise            BIGINT NOT NULL DEFAULT 0,
    razorpay_refund_id      VARCHAR(255),
    captured_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    refunded_at             TIMESTAMPTZ,
    UNIQUE (razorpay_payment_id),
    UNIQUE (razorpay_order_id),
    CONSTRAINT chk_event_payments_currency CHECK (currency = 'INR'),
    CONSTRAINT chk_event_payments_status CHECK (status IN ('captured', 'refund_pending', 'refunded')),
    CONSTRAINT chk_event_payments_split CHECK (
        platform_fee_paise + gst_paise + host_payable_paise = gross_paise
        OR status <> 'captured'
    )
);

CREATE INDEX idx_event_payments_event ON event_payments(event_id, status);
CREATE INDEX idx_event_payments_organizer ON event_payments(organizer_user_id, status);

-- Multiple settlements per event are allowed (delta after more captures).
-- Do not unique-index event_id: application enforces open payable.
CREATE TABLE event_host_settlements (
    id                  BIGSERIAL PRIMARY KEY,
    event_id            BIGINT NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    organizer_user_id   BIGINT NOT NULL REFERENCES user_account(id) ON DELETE RESTRICT,
    currency            VARCHAR(3) NOT NULL DEFAULT 'INR',
    payable_paise       BIGINT NOT NULL,
    status              VARCHAR(32) NOT NULL DEFAULT 'pending',
    note                TEXT,
    marked_paid_at      TIMESTAMPTZ,
    marked_paid_by      TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_settlements_status CHECK (status IN ('pending', 'paid', 'cancelled')),
    CONSTRAINT chk_settlements_currency CHECK (currency = 'INR')
);

ALTER TABLE event_attendees
    ADD COLUMN IF NOT EXISTS amount_due_paise BIGINT,
    ADD COLUMN IF NOT EXISTS amount_captured_paise BIGINT NOT NULL DEFAULT 0;
