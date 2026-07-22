-- Private user addresses (address book). PII: NEVER exposed on the public
-- profile or any public endpoint. Owner-scoped access only. Reusable across
-- business cards and, in the future, billing + physical card shipping.
--
-- Design principles:
--   * A user may keep several LABELED addresses (Home, Office, Billing,
--     Shipping). Structured columns (not a freeform blob) so the data can
--     reliably drive shipping/billing later.
--   * These addresses are PRIVATE. They are distinct from user_account data
--     (which is public) and are only ever returned to the owner.
--   * The business-card address remains OPTIONAL: a card may reference one of
--     these addresses (copied into its card_state document) or be left blank.

CREATE TABLE IF NOT EXISTS user_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    account_id VARCHAR(64) NOT NULL, -- owner (user_account.account_id)
    label VARCHAR(60) NOT NULL, -- e.g. "Home", "Office", "Billing"
    line1 VARCHAR(160) NOT NULL,
    line2 VARCHAR(160),
    city VARCHAR(80),
    state VARCHAR(80),
    postal_code VARCHAR(20),
    country VARCHAR(80),
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP,
    FOREIGN KEY (account_id) REFERENCES user_account (account_id) ON DELETE CASCADE,
    CONSTRAINT chk_user_addresses_label_nonempty CHECK (char_length(trim(label)) > 0),
    CONSTRAINT chk_user_addresses_line1_nonempty CHECK (char_length(trim(line1)) > 0)
);

CREATE INDEX IF NOT EXISTS idx_user_addresses_account ON user_addresses (account_id, deleted_at);

-- At most one default address per user (soft-deleted rows excluded).
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_addresses_default ON user_addresses (account_id)
WHERE
    is_default
    AND deleted_at IS NULL;