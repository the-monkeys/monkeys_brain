ALTER TABLE blog
    ADD COLUMN IF NOT EXISTS group_id BIGINT NULL REFERENCES groups(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS audience VARCHAR(20) NOT NULL DEFAULT 'public';

ALTER TABLE blog DROP CONSTRAINT IF EXISTS chk_blog_audience;
ALTER TABLE blog ADD CONSTRAINT chk_blog_audience
    CHECK (audience IN ('public', 'group_only'));

CREATE INDEX IF NOT EXISTS idx_blog_group_id ON blog(group_id) WHERE group_id IS NOT NULL;
