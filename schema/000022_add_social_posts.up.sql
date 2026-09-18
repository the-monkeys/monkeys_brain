CREATE TABLE social_posts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE RESTRICT,
    owner_kind VARCHAR(16) NOT NULL DEFAULT 'user',
    owner_scope_id UUID,
    base_text TEXT NOT NULL DEFAULT '',
    state VARCHAR(32) NOT NULL DEFAULT 'draft',
    version BIGINT NOT NULL DEFAULT 1,
    scheduled_at TIMESTAMPTZ,
    schedule_timezone TEXT,
    schedule_local_at TIMESTAMP,
    queue_position BIGINT,
    queued_at TIMESTAMPTZ,
    last_error_code VARCHAR(128),
    last_error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT chk_social_posts_owner CHECK (
        (owner_kind = 'user' AND owner_scope_id IS NULL) OR
        (owner_kind <> 'user' AND owner_scope_id IS NOT NULL)
    ),
    CONSTRAINT chk_social_posts_state CHECK (
        state IN ('draft', 'scheduled', 'publishing', 'published', 'published_with_errors', 'failed')
    ),
    CONSTRAINT chk_social_posts_version CHECK (version > 0),
    CONSTRAINT chk_social_posts_schedule CHECK (
        (state IN ('scheduled', 'publishing') AND scheduled_at IS NOT NULL AND schedule_timezone IS NOT NULL) OR
        state NOT IN ('scheduled', 'publishing')
    )
);

CREATE TABLE social_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    platform VARCHAR(32) NOT NULL,
    display_name VARCHAR(128) NOT NULL,
    handle VARCHAR(128) NOT NULL,
    external_account_ref VARCHAR(160) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    capabilities JSONB NOT NULL DEFAULT '{}'::JSONB,
    encrypted_access_token BYTEA,
    encrypted_refresh_token BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_social_accounts_platform CHECK (
        platform IN ('x', 'linkedin', 'instagram', 'facebook', 'youtube', 'tiktok')
    ),
    CONSTRAINT chk_social_accounts_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT uq_social_accounts_owner_platform UNIQUE (owner_user_id, platform),
    CONSTRAINT uq_social_accounts_external_ref UNIQUE (owner_user_id, platform, external_account_ref)
);

CREATE TABLE social_media_assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE RESTRICT,
    object_key TEXT NOT NULL UNIQUE,
    source_asset_checksum VARCHAR(64),
    checksum VARCHAR(64) NOT NULL,
    content_type VARCHAR(128) NOT NULL,
    byte_size BIGINT NOT NULL,
    media_kind VARCHAR(16) NOT NULL,
    width INTEGER,
    height INTEGER,
    duration_ms BIGINT,
    processing_status VARCHAR(32) NOT NULL DEFAULT 'ready',
    source_kind VARCHAR(32) NOT NULL DEFAULT 'upload',
    source_asset_ref TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT chk_social_media_assets_kind CHECK (media_kind IN ('image', 'video', 'audio')),
    CONSTRAINT chk_social_media_assets_size CHECK (byte_size >= 0),
    CONSTRAINT chk_social_media_assets_dimensions CHECK (
        (width IS NULL OR width > 0) AND (height IS NULL OR height > 0)
    ),
    CONSTRAINT chk_social_media_assets_duration CHECK (duration_ms IS NULL OR duration_ms >= 0),
    CONSTRAINT chk_social_media_assets_status CHECK (processing_status IN ('pending', 'ready', 'failed')),
    CONSTRAINT chk_social_media_assets_source CHECK (source_kind IN ('upload', 'snapshot', 'card'))
);

CREATE TABLE social_post_renditions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL REFERENCES social_posts(id) ON DELETE RESTRICT,
    social_account_id UUID NOT NULL REFERENCES social_accounts(id) ON DELETE RESTRICT,
    platform VARCHAR(32) NOT NULL,
    text_override TEXT,
    scheduled_at_override TIMESTAMPTZ,
    schedule_timezone_override TEXT,
    schedule_local_at_override TIMESTAMP,
    state VARCHAR(32) NOT NULL DEFAULT 'draft',
    version BIGINT NOT NULL DEFAULT 1,
    provider_post_ref VARCHAR(256),
    provider_payload JSONB,
    provider_result JSONB,
    last_error_code VARCHAR(128),
    last_error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    CONSTRAINT uq_social_post_renditions_account UNIQUE (post_id, social_account_id),
    CONSTRAINT chk_social_renditions_platform CHECK (
        platform IN ('x', 'linkedin', 'instagram', 'facebook', 'youtube', 'tiktok')
    ),
    CONSTRAINT chk_social_renditions_state CHECK (
        state IN ('draft', 'scheduled', 'publishing', 'published', 'failed')
    ),
    CONSTRAINT chk_social_renditions_version CHECK (version > 0)
);

CREATE TABLE social_rendition_media (
    rendition_id UUID NOT NULL REFERENCES social_post_renditions(id) ON DELETE RESTRICT,
    asset_id UUID NOT NULL REFERENCES social_media_assets(id) ON DELETE RESTRICT,
    position SMALLINT NOT NULL,
    alt_text TEXT,
    caption TEXT,
    crop JSONB,
    platform_metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (rendition_id, asset_id),
    CONSTRAINT uq_social_rendition_media_position UNIQUE (rendition_id, position),
    CONSTRAINT chk_social_rendition_media_position CHECK (position >= 0)
);

CREATE TABLE social_publish_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL REFERENCES social_posts(id) ON DELETE RESTRICT,
    rendition_id UUID NOT NULL REFERENCES social_post_renditions(id) ON DELETE RESTRICT,
    rendition_version BIGINT NOT NULL,
    dedupe_key VARCHAR(256) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'ready',
    run_at TIMESTAMPTZ NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    lease_owner VARCHAR(128),
    lease_expires_at TIMESTAMPTZ,
    provider_idempotency_key VARCHAR(256) NOT NULL,
    last_error_code VARCHAR(128),
    last_error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    CONSTRAINT uq_social_publish_jobs_dedupe UNIQUE (dedupe_key),
    CONSTRAINT chk_social_publish_jobs_status CHECK (
        status IN ('ready', 'leased', 'retry_wait', 'succeeded', 'dead', 'cancelled')
    ),
    CONSTRAINT chk_social_publish_jobs_attempts CHECK (attempt_count >= 0 AND max_attempts > 0)
);

CREATE UNIQUE INDEX uq_social_publish_jobs_active_rendition
    ON social_publish_jobs (rendition_id, rendition_version)
    WHERE status IN ('ready', 'leased', 'retry_wait');

CREATE TABLE social_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id UUID NOT NULL REFERENCES social_posts(id) ON DELETE RESTRICT,
    aggregate_version BIGINT NOT NULL,
    event_type VARCHAR(128) NOT NULL,
    payload JSONB NOT NULL,
    idempotency_key VARCHAR(256) NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_owner VARCHAR(128),
    lease_expires_at TIMESTAMPTZ,
    delivery_attempts INTEGER NOT NULL DEFAULT 0,
    delivered_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_social_outbox_idempotency UNIQUE (idempotency_key),
    CONSTRAINT chk_social_outbox_attempts CHECK (delivery_attempts >= 0)
);

CREATE TABLE social_publish_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES social_publish_jobs(id) ON DELETE RESTRICT,
    rendition_id UUID NOT NULL REFERENCES social_post_renditions(id) ON DELETE RESTRICT,
    social_account_id UUID NOT NULL REFERENCES social_accounts(id) ON DELETE RESTRICT,
    attempt_number INTEGER NOT NULL,
    worker_id VARCHAR(128) NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    outcome VARCHAR(32) NOT NULL DEFAULT 'started',
    retryable BOOLEAN,
    error_code VARCHAR(128),
    error_message TEXT,
    provider_request_id VARCHAR(256),
    provider_result JSONB,
    CONSTRAINT uq_social_publish_attempts_number UNIQUE (job_id, attempt_number),
    CONSTRAINT chk_social_publish_attempts_outcome CHECK (
        outcome IN ('started', 'succeeded', 'failed')
    )
);

CREATE TABLE social_post_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL REFERENCES social_posts(id) ON DELETE RESTRICT,
    rendition_id UUID REFERENCES social_post_renditions(id) ON DELETE RESTRICT,
    job_id UUID REFERENCES social_publish_jobs(id) ON DELETE RESTRICT,
    actor_type VARCHAR(32) NOT NULL,
    actor_user_id BIGINT REFERENCES user_account(id) ON DELETE RESTRICT,
    event_type VARCHAR(128) NOT NULL,
    correlation_id VARCHAR(128),
    idempotency_key VARCHAR(256),
    detail JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_social_post_events_actor CHECK (actor_type IN ('user', 'worker', 'system'))
);

CREATE TABLE social_post_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID REFERENCES social_posts(id) ON DELETE RESTRICT,
    rendition_id UUID REFERENCES social_post_renditions(id) ON DELETE RESTRICT,
    actor_user_id BIGINT REFERENCES user_account(id) ON DELETE RESTRICT,
    action VARCHAR(128) NOT NULL,
    correlation_id VARCHAR(128),
    source_ip INET,
    user_agent TEXT,
    before_state JSONB,
    after_state JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE social_command_idempotency (
    owner_user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE RESTRICT,
    action VARCHAR(128) NOT NULL,
    idempotency_key VARCHAR(256) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_user_id, action, idempotency_key)
);

CREATE INDEX idx_social_posts_owner_state_schedule
    ON social_posts (owner_user_id, state, scheduled_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_social_posts_owner_queue
    ON social_posts (owner_user_id, queue_position) WHERE deleted_at IS NULL AND queue_position IS NOT NULL;
CREATE INDEX idx_social_renditions_post_state ON social_post_renditions (post_id, state);
CREATE INDEX idx_social_media_assets_owner ON social_media_assets (owner_user_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_social_publish_jobs_claim ON social_publish_jobs (status, run_at) WHERE status IN ('ready', 'retry_wait');
CREATE INDEX idx_social_publish_jobs_expired_lease ON social_publish_jobs (lease_expires_at) WHERE status = 'leased';
CREATE INDEX idx_social_outbox_claim ON social_outbox (delivered_at, available_at);
CREATE INDEX idx_social_post_events_timeline ON social_post_events (post_id, created_at DESC);
CREATE INDEX idx_social_post_audit_log_timeline ON social_post_audit_log (post_id, created_at DESC);

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

CREATE TRIGGER trg_provision_social_mock_accounts
AFTER INSERT ON user_account
FOR EACH ROW EXECUTE FUNCTION provision_social_mock_accounts();

INSERT INTO social_accounts (owner_user_id, platform, display_name, handle, external_account_ref)
SELECT u.id, p.platform, initcap(p.platform) || ' Mock', '@' || u.username, 'mock:' || p.platform || ':' || u.id
FROM user_account u
CROSS JOIN unnest(ARRAY['x', 'linkedin', 'instagram', 'facebook', 'youtube', 'tiktok']) AS p(platform)
ON CONFLICT (owner_user_id, platform) DO NOTHING;
