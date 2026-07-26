-- Business Card Generator: server-side persistence (optional; UI currently
-- uses browser localStorage). Apply this when moving cards to the backend.
--
-- Design principles:
--   * A card is a self-contained DOCUMENT. Its contact values (name, email,
--     job title, company, etc.) are independent user input for that specific
--     card and are NOT mirrored from user_account -- a user may put a work
--     email/phone on the card, own fields that don't exist on the account
--     (job_title/company/department/website), or keep several cards.
--   * Account identity is prefilled at CREATION time from the users service
--     (GetUserProfile / GetMyProfile), not permanently duplicated here. There
--     is therefore no sync obligation with user_account.
--   * The full card document lives in ONE place: the card_state JSONB (single
--     source of truth). Discrete columns are limited to metadata we actually
--     query, sort, or constrain by. To search by a JSON contact field later,
--     add a JSONB expression index instead of a mirror column.

CREATE TABLE IF NOT EXISTS business_cards (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id             VARCHAR(64) NOT NULL,        -- owner (user_account.account_id)
    name                   VARCHAR(120) NOT NULL,       -- card label, e.g. "Work card"
    template_id            VARCHAR(64) NOT NULL,
    theme_id               VARCHAR(64) NOT NULL,

-- Full editor state: { input (contact + socialLinks), templateId, themeId,
-- customization }. Single source of truth for the card document.
card_state JSONB NOT NULL,

-- Optional links to deduplicated MinIO assets (avatar / company logo)

avatar_asset_checksum  VARCHAR(64),
    logo_asset_checksum    VARCHAR(64),

    is_default             BOOLEAN NOT NULL DEFAULT FALSE,
    created_at             TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at             TIMESTAMP,

    FOREIGN KEY (account_id) REFERENCES user_account(account_id) ON DELETE CASCADE,
    FOREIGN KEY (avatar_asset_checksum) REFERENCES storage_assets(checksum) ON DELETE SET NULL,
    FOREIGN KEY (logo_asset_checksum) REFERENCES storage_assets(checksum) ON DELETE SET NULL,
    CONSTRAINT chk_business_cards_name_nonempty CHECK (char_length(trim(name)) > 0),
    CONSTRAINT chk_business_cards_card_state_object CHECK (jsonb_typeof(card_state) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_business_cards_account ON business_cards (account_id, deleted_at);

CREATE INDEX IF NOT EXISTS idx_business_cards_updated ON business_cards (updated_at DESC);

-- At most one default card per user (soft-deleted rows excluded).
CREATE UNIQUE INDEX IF NOT EXISTS uq_business_cards_default ON business_cards (account_id)
WHERE
    is_default
    AND deleted_at IS NULL;