-- 000023_allow_multiple_social_accounts.down.sql

ALTER TABLE social_accounts DROP COLUMN IF EXISTS avatar_url;
ALTER TABLE social_accounts DROP COLUMN IF EXISTS is_mock;

ALTER TABLE social_accounts DROP CONSTRAINT IF EXISTS chk_social_accounts_status;
ALTER TABLE social_accounts ADD CONSTRAINT chk_social_accounts_status
    CHECK (status IN ('active', 'disabled'));

ALTER TABLE social_accounts ADD CONSTRAINT uq_social_accounts_owner_platform
    UNIQUE (owner_user_id, platform);
