ALTER TABLE discussion_posts
    ADD COLUMN IF NOT EXISTS audience VARCHAR(20) NOT NULL DEFAULT 'public';

ALTER TABLE discussion_posts DROP CONSTRAINT IF EXISTS chk_discussion_audience;
ALTER TABLE discussion_posts ADD CONSTRAINT chk_discussion_audience
    CHECK (audience IN ('public', 'group_only'));

UPDATE discussion_posts SET audience = 'group_only' WHERE group_id IS NOT NULL;

ALTER TABLE discussion_posts DROP CONSTRAINT IF EXISTS discussion_posts_group_id_fkey;
ALTER TABLE discussion_posts
    ADD CONSTRAINT discussion_posts_group_id_fkey
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE SET NULL;

DROP INDEX IF EXISTS idx_discussion_posts_feed;
CREATE INDEX IF NOT EXISTS idx_discussion_posts_site_feed
    ON discussion_posts (created_at DESC, id DESC)
    WHERE status = 'visible' AND (group_id IS NULL OR audience = 'public');
