CREATE TABLE discussion_posts (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(32) NOT NULL UNIQUE,
    author_id BIGINT REFERENCES user_account(id) ON DELETE SET NULL,
    group_id BIGINT REFERENCES groups(id) ON DELETE CASCADE,
    body TEXT NOT NULL DEFAULT '',
    reply_count INTEGER NOT NULL DEFAULT 0,
    like_count INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'visible',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    edited_until TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_discussion_body_len CHECK (char_length(body) <= 2000),
    CONSTRAINT chk_discussion_status CHECK (status IN ('visible', 'deleted', 'hidden'))
);

CREATE TABLE discussion_replies (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(32) NOT NULL UNIQUE,
    post_id BIGINT NOT NULL REFERENCES discussion_posts(id) ON DELETE CASCADE,
    parent_reply_id BIGINT REFERENCES discussion_replies(id) ON DELETE CASCADE,
    author_id BIGINT REFERENCES user_account(id) ON DELETE SET NULL,
    body TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'visible',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_discussion_reply_body CHECK (char_length(trim(body)) > 0),
    CONSTRAINT chk_discussion_reply_body_len CHECK (char_length(body) <= 2000),
    CONSTRAINT chk_discussion_reply_status CHECK (status IN ('visible', 'deleted', 'hidden'))
);

CREATE TABLE discussion_likes (
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    post_id BIGINT NOT NULL REFERENCES discussion_posts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, post_id)
);

CREATE TABLE discussion_files (
    id BIGSERIAL PRIMARY KEY,
    post_id BIGINT NOT NULL REFERENCES discussion_posts(id) ON DELETE CASCADE,
    sort_order SMALLINT NOT NULL DEFAULT 0,
    storage_key TEXT NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    UNIQUE (post_id, sort_order),
    CONSTRAINT chk_discussion_files_order CHECK (sort_order BETWEEN 0 AND 3)
);

CREATE INDEX idx_discussion_posts_feed ON discussion_posts (created_at DESC, id DESC)
    WHERE status = 'visible' AND group_id IS NULL;
CREATE INDEX idx_discussion_posts_group ON discussion_posts (group_id, created_at DESC, id DESC)
    WHERE status = 'visible';
CREATE INDEX idx_discussion_replies_post ON discussion_replies (post_id, created_at);
