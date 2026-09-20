-- 000023_allow_multiple_social_accounts.up.sql

-- 1. Drop single-account constraint to allow multiple accounts per platform per user
ALTER TABLE social_accounts DROP CONSTRAINT IF EXISTS uq_social_accounts_owner_platform;

-- 2. Update status constraint to include 'disconnected'
ALTER TABLE social_accounts DROP CONSTRAINT IF EXISTS chk_social_accounts_status;
ALTER TABLE social_accounts ADD CONSTRAINT chk_social_accounts_status
    CHECK (status IN ('active', 'disabled', 'disconnected'));

-- 3. Add is_mock and avatar_url columns
ALTER TABLE social_accounts ADD COLUMN IF NOT EXISTS is_mock BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE social_accounts ADD COLUMN IF NOT EXISTS avatar_url TEXT;

-- 4. Update the provisioning trigger to set is_mock = TRUE and conflict on (owner_user_id, platform, external_account_ref)
CREATE OR REPLACE FUNCTION provision_social_mock_accounts()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO social_accounts (owner_user_id, platform, display_name, handle, external_account_ref, is_mock, status)
    SELECT NEW.id, platform, initcap(platform) || ' Mock', '@' || NEW.username, 'mock:' || platform || ':' || NEW.id, TRUE, 'active'
    FROM unnest(ARRAY['x', 'linkedin', 'instagram', 'facebook', 'youtube', 'tiktok']) AS platform
    ON CONFLICT (owner_user_id, platform, external_account_ref) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
