DROP INDEX IF EXISTS idx_discussion_posts_site_feed;
CREATE INDEX IF NOT EXISTS idx_discussion_posts_feed
    ON discussion_posts (created_at DESC, id DESC)
    WHERE status = 'visible' AND group_id IS NULL;

ALTER TABLE discussion_posts DROP CONSTRAINT IF EXISTS discussion_posts_group_id_fkey;
ALTER TABLE discussion_posts
    ADD CONSTRAINT discussion_posts_group_id_fkey
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE;

ALTER TABLE discussion_posts DROP CONSTRAINT IF EXISTS chk_discussion_audience;
ALTER TABLE discussion_posts DROP COLUMN IF EXISTS audience;
