-- Group invite links.
--
-- Tokened, shareable links that admit their bearer to a group. A link may carry
-- an optional expiry (expires_at) and a maximum number of uses (max_uses = 0
-- means unlimited). The role granted on acceptance defaults to 'member'.
--
-- This migration is additive; no existing table is altered.

CREATE TABLE IF NOT EXISTS group_invites (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    token VARCHAR(64) NOT NULL UNIQUE,
    role VARCHAR(40) NOT NULL DEFAULT 'member',
    max_uses INTEGER NOT NULL DEFAULT 0, -- 0 = unlimited
    uses INTEGER NOT NULL DEFAULT 0,
    expires_at TIMESTAMP NULL, -- NULL = never expires
    created_by BIGINT NOT NULL REFERENCES user_account (id) ON DELETE CASCADE,
    revoked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_group_invites_uses CHECK (
        uses >= 0
        AND max_uses >= 0
    )
);

CREATE INDEX IF NOT EXISTS idx_group_invites_group ON group_invites (group_id);