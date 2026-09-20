CREATE OR REPLACE FUNCTION provision_social_mock_accounts()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO social_accounts (owner_user_id, platform, display_name, handle, external_account_ref)
    SELECT NEW.id, platform, initcap(platform) || ' Mock', '@' || NEW.username, 'mock:' || platform || ':' || NEW.id
    FROM unnest(ARRAY['x', 'linkedin', 'instagram', 'facebook', 'youtube', 'tiktok']) AS platform
    ON CONFLICT (owner_user_id, platform) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

ALTER TABLE social_accounts DROP COLUMN IF EXISTS avatar_url;
ALTER TABLE social_accounts DROP COLUMN IF EXISTS is_mock;

ALTER TABLE social_accounts DROP CONSTRAINT IF EXISTS chk_social_accounts_status;
ALTER TABLE social_accounts ADD CONSTRAINT chk_social_accounts_status
    CHECK (status IN ('active', 'disabled'));

ALTER TABLE social_accounts ADD CONSTRAINT uq_social_accounts_owner_platform
    UNIQUE (owner_user_id, platform);
