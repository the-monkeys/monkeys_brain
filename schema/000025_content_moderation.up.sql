-- Ratings are independent of publication and audience. Public identifiers keep
-- deleted targets/report history addressable without exposing their content.
CREATE TABLE content_moderation (
    target_kind TEXT NOT NULL CHECK (target_kind IN ('blog','event','group','discussion')),
    target_id TEXT NOT NULL CHECK (length(btrim(target_id)) > 0),
    author_nsfw BOOLEAN NOT NULL DEFAULT FALSE,
    staff_nsfw BOOLEAN NOT NULL DEFAULT FALSE,
    scanner_nsfw BOOLEAN NOT NULL DEFAULT FALSE,
    scan_status TEXT NOT NULL DEFAULT 'unknown',
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    PRIMARY KEY (target_kind,target_id)
);
CREATE FUNCTION moderation_rating_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.author_nsfw,NEW.staff_nsfw,NEW.scanner_nsfw,NEW.scan_status)
       IS DISTINCT FROM (OLD.author_nsfw,OLD.staff_nsfw,OLD.scanner_nsfw,OLD.scan_status) THEN
        NEW.revision := OLD.revision + 1;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER moderation_rating_revision BEFORE UPDATE ON content_moderation
    FOR EACH ROW EXECUTE FUNCTION moderation_rating_revision();

CREATE TABLE content_reports (
    id BIGSERIAL PRIMARY KEY,
    target_kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    reporter_account_id TEXT NOT NULL,
    reporter_username TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 100),
    details TEXT NOT NULL DEFAULT '' CHECK (length(details) <= 10000),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed_nsfw','dismissed','reversed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_by TEXT NOT NULL DEFAULT '',
    reviewed_at TIMESTAMPTZ,
    review_note TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (target_kind,target_id) REFERENCES content_moderation(target_kind,target_id),
    UNIQUE (reporter_account_id,target_kind,target_id,reason)
);
CREATE INDEX content_reports_queue ON content_reports(status,created_at DESC,id DESC);
CREATE INDEX content_reports_target ON content_reports(target_kind,target_id,status);
-- A durable action receipt prevents an old confirmation retry from undoing a
-- later reversal, and keeps duplicate attempts from generating duplicate audits.
CREATE TABLE content_report_actions (
    report_id BIGINT NOT NULL REFERENCES content_reports(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('confirm_nsfw','dismiss','reverse')),
    actor_account_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (report_id,action)
);
CREATE TABLE content_reveals (
    target_kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    account_id VARCHAR(64) NOT NULL REFERENCES user_account(account_id) ON DELETE CASCADE,
    revision BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (target_kind,target_id,account_id),
    FOREIGN KEY (target_kind,target_id) REFERENCES content_moderation(target_kind,target_id) ON DELETE CASCADE
);

-- External legacy admin paths must also invalidate prior reveals. They retain
-- their own flags; reversing a report explicitly clears NSFW flags only.
CREATE FUNCTION moderation_touch(kind TEXT, public_id TEXT) RETURNS void LANGUAGE sql AS $$
    INSERT INTO content_moderation(target_kind,target_id) VALUES(kind,public_id)
    ON CONFLICT (target_kind,target_id) DO UPDATE SET revision=content_moderation.revision+1;
$$;
CREATE FUNCTION moderation_legacy_flag_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_key TEXT; new_key TEXT; target_table TEXT; public_column TEXT; row_data JSONB;
BEGIN
    target_table := TG_ARGV[1]; public_column := TG_ARGV[2];
    IF TG_OP <> 'INSERT' AND OLD.flag_type='nsfw' THEN
        row_data := to_jsonb(OLD);
        EXECUTE format('SELECT %I FROM %I WHERE id=$1',public_column,target_table)
            INTO old_key USING (row_data->>TG_ARGV[3])::BIGINT;
        IF old_key IS NOT NULL THEN PERFORM moderation_touch(TG_ARGV[0],old_key); END IF;
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.flag_type='nsfw' THEN
        row_data := to_jsonb(NEW);
        EXECUTE format('SELECT %I FROM %I WHERE id=$1',public_column,target_table)
            INTO new_key USING (row_data->>TG_ARGV[3])::BIGINT;
        IF new_key IS NOT NULL AND (old_key IS NULL OR new_key<>old_key OR TG_OP='UPDATE') THEN
            PERFORM moderation_touch(TG_ARGV[0],new_key);
        END IF;
    END IF;
    RETURN NULL;
END $$;
CREATE TRIGGER moderation_blog_legacy AFTER INSERT OR UPDATE OR DELETE ON blog_admin_flags
    FOR EACH ROW EXECUTE FUNCTION moderation_legacy_flag_revision('blog','blog','blog_id','blog_id');
CREATE TRIGGER moderation_event_legacy AFTER INSERT OR UPDATE OR DELETE ON event_admin_flags
    FOR EACH ROW EXECUTE FUNCTION moderation_legacy_flag_revision('event','events','slug','event_id');
CREATE TRIGGER moderation_group_legacy AFTER INSERT OR UPDATE OR DELETE ON group_admin_flags
    FOR EACH ROW EXECUTE FUNCTION moderation_legacy_flag_revision('group','groups','slug','group_id');
INSERT INTO content_moderation(target_kind,target_id)
 SELECT 'blog',b.blog_id FROM blog b JOIN blog_admin_flags f ON f.blog_id=b.id WHERE f.flag_type='nsfw'
 UNION SELECT 'event',e.slug FROM events e JOIN event_admin_flags f ON f.event_id=e.id WHERE f.flag_type='nsfw'
 UNION SELECT 'group',g.slug FROM groups g JOIN group_admin_flags f ON f.group_id=g.id WHERE f.flag_type='nsfw';

-- The same normalization is used for stored discussion URLs, CAS keys and
-- legacy storage routes. It strips only transport prefixes, never path segments.
CREATE FUNCTION moderation_storage_path(value TEXT) RETURNS TEXT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
 SELECT regexp_replace(regexp_replace(
   ltrim(regexp_replace(regexp_replace(split_part(split_part(value,'?',1),'#',1),
     '^https?://[^/]+','','i'), '^/api/(v2/storage|storage|v1/files|v1[.]1/files)/',''),'/'),
   '^post/','posts/'), '^profile/','profiles/');
$$;
CREATE INDEX content_discussion_file_key ON discussion_files(moderation_storage_path(storage_key));
CREATE INDEX content_storage_asset_key ON storage_assets(moderation_storage_path(object_key));

CREATE VIEW content_media_owners AS
 SELECT 'blog'::text AS kind,r.owner_id AS target_id,r.checksum
 FROM storage_asset_refs r WHERE r.owner_type='blog' AND r.deleted_at IS NULL
 UNION SELECT r.owner_type,r.owner_id,r.checksum FROM storage_asset_refs r
 WHERE r.owner_type IN ('event','group','discussion') AND r.deleted_at IS NULL
 UNION SELECT 'discussion',d.public_id,a.checksum
 FROM discussion_posts d JOIN discussion_files f ON f.post_id=d.id
 JOIN storage_assets a ON moderation_storage_path(a.object_key)=moderation_storage_path(f.storage_key)
 UNION SELECT 'discussion',d.public_id,r.checksum
 FROM discussion_posts d JOIN discussion_files f ON f.post_id=d.id
 JOIN storage_asset_refs r ON r.owner_type='blog' AND r.deleted_at IS NULL
 AND moderation_storage_path(f.storage_key)='posts/'||r.owner_id||'/'||r.file_name
 UNION SELECT 'group',g.slug,a.checksum FROM groups g JOIN storage_assets a
 ON moderation_storage_path(a.object_key) IN (moderation_storage_path(g.logo_image),moderation_storage_path(g.cover_image))
 UNION SELECT 'event',e.slug,a.checksum FROM events e JOIN storage_assets a
 ON moderation_storage_path(a.object_key)=moderation_storage_path(e.cover_image);

CREATE FUNCTION moderation_touch_asset(asset_checksum TEXT) RETURNS void LANGUAGE plpgsql AS $$
DECLARE target RECORD;
BEGIN
 FOR target IN SELECT DISTINCT kind,target_id FROM content_media_owners WHERE checksum=asset_checksum ORDER BY kind,target_id LOOP
  PERFORM moderation_touch(target.kind,target.target_id);
 END LOOP;
END $$;
CREATE FUNCTION moderation_asset_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE affected RECORD;
BEGIN
 IF TG_TABLE_NAME='storage_assets' THEN
  IF NEW.is_nsfw IS DISTINCT FROM OLD.is_nsfw OR NEW.object_key IS DISTINCT FROM OLD.object_key THEN
   PERFORM moderation_touch_asset(NEW.checksum);
  END IF;
 ELSE
  IF TG_OP <> 'INSERT' THEN
   IF OLD.owner_type IN ('blog','event','group','discussion') THEN PERFORM moderation_touch(OLD.owner_type,OLD.owner_id); END IF;
   FOR affected IN SELECT DISTINCT target_id FROM content_media_owners WHERE kind='discussion' AND checksum=OLD.checksum ORDER BY target_id LOOP
    PERFORM moderation_touch('discussion',affected.target_id);
   END LOOP;
  END IF;
  IF TG_OP <> 'DELETE' THEN
   IF NEW.owner_type IN ('blog','event','group','discussion') THEN PERFORM moderation_touch(NEW.owner_type,NEW.owner_id); END IF;
   FOR affected IN SELECT DISTINCT target_id FROM content_media_owners WHERE kind='discussion' AND checksum=NEW.checksum ORDER BY target_id LOOP
    PERFORM moderation_touch('discussion',affected.target_id);
   END LOOP;
  END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER moderation_asset_rating AFTER UPDATE OF is_nsfw,object_key ON storage_assets
 FOR EACH ROW EXECUTE FUNCTION moderation_asset_revision();
CREATE TRIGGER moderation_asset_ref AFTER INSERT OR UPDATE OR DELETE ON storage_asset_refs
 FOR EACH ROW EXECUTE FUNCTION moderation_asset_revision();
CREATE FUNCTION moderation_discussion_file_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE key TEXT;
BEGIN
 IF TG_OP <> 'INSERT' THEN
  SELECT public_id INTO key FROM discussion_posts WHERE id=OLD.post_id;
  IF key IS NOT NULL THEN PERFORM moderation_touch('discussion',key); END IF;
 END IF;
 IF TG_OP <> 'DELETE' THEN
  SELECT public_id INTO key FROM discussion_posts WHERE id=NEW.post_id;
  IF key IS NOT NULL THEN PERFORM moderation_touch('discussion',key); END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER moderation_discussion_file AFTER INSERT OR UPDATE OR DELETE ON discussion_files
 FOR EACH ROW EXECUTE FUNCTION moderation_discussion_file_revision();

-- Consent follows the substance of the target, including text-only edits.
-- Counters and bookkeeping updates on discussions/events/groups do not revoke it.
CREATE FUNCTION moderation_content_edit_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='discussion_posts' THEN
  IF NEW.body IS DISTINCT FROM OLD.body THEN PERFORM moderation_touch('discussion',NEW.public_id); END IF;
 ELSIF TG_TABLE_NAME='events' THEN
  IF (NEW.title,NEW.description,NEW.cover_image,NEW.location,NEW.meeting_link,NEW.how_to_find_us)
     IS DISTINCT FROM (OLD.title,OLD.description,OLD.cover_image,OLD.location,OLD.meeting_link,OLD.how_to_find_us) THEN
   PERFORM moderation_touch('event',NEW.slug);
  END IF;
 ELSIF TG_TABLE_NAME='groups' THEN
  IF (NEW.name,NEW.description,NEW.cover_image,NEW.logo_image)
     IS DISTINCT FROM (OLD.name,OLD.description,OLD.cover_image,OLD.logo_image) THEN
   PERFORM moderation_touch('group',NEW.slug);
  END IF;
 ELSE
  -- Blog bodies live in Elasticsearch. PostgreSQL can invalidate consent when
  -- a draft/publish transition or the updated_at content marker changes.
  IF (NEW.updated_at,NEW.status) IS DISTINCT FROM (OLD.updated_at,OLD.status) THEN PERFORM moderation_touch('blog',NEW.blog_id); END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER moderation_discussion_edit AFTER UPDATE OF body ON discussion_posts
 FOR EACH ROW EXECUTE FUNCTION moderation_content_edit_revision();
CREATE TRIGGER moderation_event_edit AFTER UPDATE OF title,description,cover_image,location,meeting_link,how_to_find_us ON events
 FOR EACH ROW EXECUTE FUNCTION moderation_content_edit_revision();
CREATE TRIGGER moderation_group_edit AFTER UPDATE OF name,description,cover_image,logo_image ON groups
 FOR EACH ROW EXECUTE FUNCTION moderation_content_edit_revision();
CREATE TRIGGER moderation_blog_edit AFTER UPDATE OF updated_at,status ON blog
 FOR EACH ROW EXECUTE FUNCTION moderation_content_edit_revision();
