# Group and public discussions — design spec

Date: 2026-09-15  
Status: **spec for review. Storage is decided: Postgres only. Do not implement until this file is accepted.**  
Scope: a **new** feature. Not blogs. Not event comments. No backend code in this pass (another agent owns other work).  
Owner: product + engine.

This spec is for an implementer who has not seen the chat.

---

## 0. Why this is not a blog

Monkeys already has long articles. Those use EditorJS, live in Elasticsearch, and only keep a **pointer** in Postgres. That split earns Elasticsearch: a long document, full-text search, tags, and SEO.

A discussion is the short path: like Reddit or Twitter. Limited characters, optional images, replies. People can post them on the public site and inside public or private groups. The feed is “latest 20,” plus a membership check. Postgres does that on the Postgres NUC we already run. Elasticsearch would only be a second copy of a 500-character row, fetched by id, which is the expensive part of Elasticsearch (heap on the Elasticsearch NUC) without search or sharding.

If someone wants a long piece, they still write a **blog**. Do not open EditorJS for a discussion. Do not write a discussion into Elasticsearch.

---

## 1. How blogs work today (read-only context)

Do not change this path for discussions. It is here so we do not copy it by mistake.

### 1.1 Split brain

| Store | What it holds |
| --- | --- |
| **Elasticsearch** index `the_monkeys_blogs` | The real article: EditorJS `blog.blocks`, tags, slug, `owner_account_id`, `is_draft`, `is_scheduled`, `published_time`, plus search-v2 fields `title` / `summary` / `body` from `searchdoc.Apply` |
| **Postgres** table `blog` | Pointer only: `user_id`, public `blog_id` (string), `status` (`draft` / published / …) |
| **Postgres** `blog_permissions` | Owner (and later co-authors) |

Likes, bookmarks, co-author invites, and `blog_comments` are Postgres rows keyed by the numeric `blog.id`, not by the ES document.

### 1.2 Write path

1. Client opens a WebSocket to the gateway (`/api/v2/blog` draft stream).
2. Gateway streams gRPC `DraftBlogV2`.
3. **First save of a new id:** blog service publishes RabbitMQ `BLOG_CREATE` (`RoutingKeys[1]`) **before** Elasticsearch. If the queue publish fails, it does **not** write ES (avoids an orphan ES doc with no Postgres row).
4. User service consumer `AddBlogWithId` inserts `blog (user_id, blog_id, status)` and owner `blog_permissions`.
5. Blog service `SaveBlog` indexes the JSON into `the_monkeys_blogs` with `DocumentID = blog_id`. Auto-save skips ES if content (minus EditorJS `time` fields) did not change.
6. **Publish:** `POST /api/v1/blog/publish/:blog_id` → `PublishBlogById` sets `is_draft=false`, `is_scheduled=false`, `published_time=now` in ES. RabbitMQ `BLOG_PUBLISH` updates Postgres `blog.status`. Then SEO (Google indexing) and AI analysis queues fire.

Public reads (`GetPublishedBlogById`, home, tags, search v2) all filter **`is_draft: false`**. There is **no** `group_id` and **no** visibility on the blog document today. Every published blog is treated as public.

### 1.3 What this means for group-only *blogs*

That is **not** this spec. Short note so it is not mixed in:

Today you cannot publish an article “only to the group” or “group + landing” without new fields and ACL. See **Appendix A** at the bottom.

---

## 2. Product (locked)

### 2.1 What a discussion is

- Short text. **Max 500 characters** (Unicode runes, trim space). Empty body is allowed only if there is at least one file.
- Optional files: **images only in v1**, **max 4**, reuse storage-v2 style (same idea as event gallery). No EditorJS, no PDF, no video in v1.
- Replies: same 500-char cap. **Two levels:** post → reply → reply-to-reply. No deeper tree.
- Author can delete their own post/reply (soft delete). Group staff with `manage_discussions` can hide or delete any post in that group.
- Likes on the post (same idea as `blog_likes`). No Reddit karma / downvote in v1.

### 2.2 Where it can live

| Surface | `group_id` | Who can read |
| --- | --- | --- |
| Public landing / site feed | `NULL` | Anyone logged in (and logged-out read is allowed, same as public blogs) |
| Public group | set | Anyone can **read**. Only **active members** can **post**. |
| Private or unlisted group | set | Only **active members** can read or post. Never on the public landing. Never in public search. Never sent to Google SEO. |

Unlisted groups follow **private** rules for discussions (members only). Hosts use unlisted so the group page is not listed; discussion content must not leak onto `/` either.

### 2.3 Who can start one

- Site-wide post: any logged-in user.
- Group post: any **active** member (`group_members.status = 'active'`). Pending join requests cannot post.
- Banned / removed / left: cannot post or read private-group threads.

Organizer, co-organizer, and moderator do not get a special “create” right beyond membership. They get **moderation** via existing `manage_discussions` (already on organizers implicitly and on co-organizers / moderators as rows).

### 2.4 What v1 does not include

- Chat / DMs / `message_threads` (tables exist in `000011` but **no Go code uses them**; leave them alone).
- Long-form group blogs (separate spec: `docs/superpowers/specs/2026-09-15-group-scoped-blogs-design.md`).
- **Blog comments.** Table `blog_comments` stays on articles. Do not move Medium-style comments into discussion posts. Event comments stay on events.
- Polls, awards, awards, flair, awards.
- Notifications beyond in-app + SSE for “reply to your post” and “new discussion in a group you belong to” (reuse FRN later; v1 can ship without email).
- Editing after 15 minutes (optional later). v1: no edit, or edit for 15 minutes only — **lock: author may edit for 15 minutes**, then body is frozen. Files cannot be swapped after create in v1 (delete post and rewrite).

---

## 3. Storage (locked)

**Decided: the discussion lives entirely in Postgres.** Postgres on the existing Postgres NUC holds the discussion: body, replies, likes, image keys, and who may see it. MinIO on the existing MinIO NUC holds image bytes. The Elasticsearch NUC stays on blogs. Discussions do not create an index there.

| Store | What it holds |
| --- | --- |
| **Postgres** `discussion_posts` | The post: `body`, author, `group_id` (`NULL` = site-wide), status, counts, `edited_until` |
| **Postgres** `discussion_replies` | Reply body, parent post, optional parent reply, author, status |
| **Postgres** `discussion_likes` | Who liked the post |
| **Postgres** `discussion_files` | Image key, content type, sort order. Max 4. |
| **Postgres** `group_members` + `groups.visibility` | Who may read or post in a group. The author is `author_id` on the post. |
| **MinIO** | Image bytes |

One write, one read, one permission check. The site feed is `WHERE group_id IS NULL AND status = 'visible'`. A private post never has a second copy that search could return.

Rejected for v1:

- Elasticsearch for discussions, including a pointer in Postgres and the body in `the_monkeys_discussions`. The feed would still be a Postgres query, and Elasticsearch would only fetch by id.
- Writing discussions into `the_monkeys_blogs`.
- Reusing `message_threads` / `messages`. Those tables stay for a future chat product.

**Read path:** one Postgres query applies status, `group_id`, and membership, and returns the page of 20 with the body. If the viewer may not see it, return **404**.

**Write path:** insert the post, the file keys, and the counts in one transaction. Reply insert and `reply_count` are one transaction. Like toggle and `like_count` are one transaction.

**Edit:** allowed only while `now < edited_until` (`created_at + 15 minutes`). Files are not swapped after create.

**Soft delete:** set `status = 'deleted'` and clear `body` for other readers. Author and staff still see a tombstone. Do not hard-delete in v1.

**Counts:** maintain `reply_count` / `like_count` on write. Do not `COUNT(*)` on every list.

When the table grows, partition `discussion_posts` by month on this same Postgres NUC. That does not add a machine and does not move the body to Elasticsearch.

---

## 4. Data model

Next unused migration is `000023` (`000022` is already taken). New file only. Do not edit `000001`–`000022`.

```sql
CREATE TABLE discussion_posts (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(32) NOT NULL UNIQUE,  -- short id in URLs, like blog_id
    author_id BIGINT REFERENCES user_account(id) ON DELETE SET NULL,
    group_id BIGINT REFERENCES groups(id) ON DELETE CASCADE,  -- NULL = site-wide
    body TEXT NOT NULL DEFAULT '',
    reply_count INTEGER NOT NULL DEFAULT 0,
    like_count INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'visible',  -- visible | deleted | hidden
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    edited_until TIMESTAMPTZ NOT NULL,  -- created_at + 15 minutes
    CONSTRAINT chk_discussion_body_len CHECK (char_length(body) <= 2000), -- bytes safety; app enforces 500 runes
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
    CONSTRAINT chk_discussion_reply_status CHECK (status IN ('visible', 'deleted', 'hidden'))
);

CREATE TABLE discussion_likes (
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    post_id BIGINT NOT NULL REFERENCES discussion_posts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, post_id)
);

-- file keys only; bytes live in MinIO via storage v2
CREATE TABLE discussion_files (
    id BIGSERIAL PRIMARY KEY,
    post_id BIGINT NOT NULL REFERENCES discussion_posts(id) ON DELETE CASCADE,
    sort_order SMALLINT NOT NULL DEFAULT 0,
    storage_key TEXT NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    UNIQUE (post_id, sort_order),
    CONSTRAINT chk_discussion_files_order CHECK (sort_order BETWEEN 0 AND 3)
);

CREATE INDEX idx_discussion_posts_feed ON discussion_posts (created_at DESC)
    WHERE status = 'visible' AND group_id IS NULL;
CREATE INDEX idx_discussion_posts_group ON discussion_posts (group_id, created_at DESC)
    WHERE status = 'visible';
CREATE INDEX idx_discussion_replies_post ON discussion_replies (post_id, created_at);
```

**Nesting rule:** `parent_reply_id` may point only at a **top-level** reply (`parent_reply_id IS NULL` on the parent). Reject a third level in the service (`FailedPrecondition`).

### 4.1 Author identity

Store `author_id` (the numeric `user_account.id`). Do not store `username` on the post or reply.

**Username change.** Auth already runs `UPDATE user_account SET username`. The discussion row is untouched. The next read joins `user_account` and returns the new username. The post stays theirs.

**Account delete.** `DeleteUserProfile` deletes the `user_account` row. `author_id` becomes `NULL`. The post, its replies from other people, and its images stay. The API shows a deleted account, with no username. Their likes disappear (`discussion_likes.user_id` is `ON DELETE CASCADE`). Their replies on other people’s posts stay, also with a null author. Do not cascade-delete the thread. `DeleteUserProfile` does not need a new discussion query if the foreign keys do this.

---

## 5. API (gateway)

New REST under `/api/v1/discussions` and `/api/v1/groups/:slug/discussions`. **Lock: its own process, `the_monkeys_discussions`.** Groups stay a group service. Discussions read the same Postgres (posts, `group_members`, `user_account`) and do not add code to the groups service. The gateway only adds routes.

| Method | Path | Auth | Behavior |
| --- | --- | --- | --- |
| POST | `/api/v1/discussions` | required | Create site-wide post. Body JSON `{ "body", "file_keys": [] }` |
| GET | `/api/v1/discussions` | optional | Public feed: `group_id IS NULL`, `status=visible`, cursor `before_id` |
| GET | `/api/v1/discussions/:id` | optional / member | 404 if hidden from this viewer (do not 403 on private group — same as unknown slug) |
| DELETE | `/api/v1/discussions/:id` | author or `manage_discussions` | Soft delete |
| POST | `/api/v1/discussions/:id/replies` | required | `{ "body", "parent_reply_id?" }` |
| POST | `/api/v1/discussions/:id/like` | required | Toggle like |
| GET | `/api/v1/groups/:slug/discussions` | see ACL | Group feed |
| POST | `/api/v1/groups/:slug/discussions` | active member | Group post |

Files: `POST /api/v1/discussions/:id/files` after create, max 4, image content types only (`image/jpeg`, `image/png`, `image/webp`, `image/gif`). Or accept `file_keys` already uploaded to a pending prefix. Match event photo upload as closely as possible.

List page size: **20**. No unbounded lists.

Copy in errors: human sentences, no SQL, no gRPC codes (same events rule).

---

## 6. Access control (must be tested)

```
read_site_post     → status=visible (or author reading own deleted)
read_group_post    → if group.visibility = 'public': anyone
                     if private/unlisted: actor is active member, else 404
create_site_post   → logged in
create_group_post  → active member
reply / like       → same as read, plus logged in
moderate           → group organizer (implicit) or permission manage_discussions
```

**Never** put private-group posts in:

- `GET /api/v1/discussions` (site feed)
- Search v2
- SEO / sitemap
- Recommendations

Public-group posts **may** appear on the group page. They **do not** appear on the site-wide feed unless we add an explicit `{ "also_on_landing": true }` later. **v1 lock: group posts stay on the group. Site feed is only `group_id IS NULL`.** That keeps private leaks impossible for the landing page.

---

## 7. Files

Reuse storage v2 + `storage_assets` on the existing MinIO NUC. Key prefix `discussions/{public_id}/{n}`. The key is a Postgres row in `discussion_files`. Public-group and site-wide images can use the public bucket. **Private-group images must not be world-readable.** Serve them only through an auth-checked GET (same idea as verification private bucket, lighter: signed URL or gateway stream if the viewer is a member).

If v1 shipping private-group files is too heavy, **lock fallback:** private-group discussions are **text-only** in v1; files allowed only on site-wide and public groups. Prefer full files if storage auth is copy-paste from events.

---

## 8. Notifications (v1 optional, v1.1 expected)

Reuse RabbitMQ → notification service → FRN, same as events.

| Trigger | Recipients | Channel |
| --- | --- | --- |
| Reply to your post or reply | Author (not self) | in-app + SSE |
| New post in a group | Skip v1 (too noisy) | — |

Do not email the whole group on every post.

---

## 9. Frontend (when that repo is free)

- Composer: textarea 500, remaining count, image attach.
- Site: `/discussions` feed + `/discussions/:id`.
- Group: new **Discussions** tab next to Events / About / Members.
- Private group: tab visible only to members (same as members-only events).
- Do not reuse the blog editor.

Out of scope for this engine spec to implement UI. This is the contract the UI must follow.

---

## 10. Testing (required before merge)

- Create site post, list feed, 500-rune reject, empty body + no files reject.
- Public group: stranger can GET list, cannot POST; member can POST.
- Private group: stranger `GET /groups/:slug/discussions` returns **404** (same as an unknown group). Direct `GET /discussions/:id` returns **404**. Never return post bodies.
- Nesting: two levels ok, third level rejected.
- Soft delete hides body from others.
- Site feed never contains a row with `group_id` set.
- Like toggle is idempotent.
- No new Elasticsearch index. `the_monkeys_blogs` mapping is unchanged.

---

## 11. Implementation notes (when coding starts)

- Do not edit blog ES mapping or `DraftBlogV2`. Do not add a discussions index on the Elasticsearch NUC.
- Discussion body, replies, likes, and file keys are Postgres on the existing Postgres NUC.
- Do not reuse `blog_comments` or `event_comments`.
- Do not start from `message_threads`.
- Additive proto only.
- Users are not engineers: toast = `st.Message()`.

---

## Appendix A — Group-scoped **blogs** (not this feature)

Today a published blog is always public (ES `is_draft: false`, SEO, search, landing).

To attach long articles to groups we would need, as a **later** spec:

| Audience | Postgres + ES fields | Landing / search / SEO | Group page | Private group |
| --- | --- | --- | --- | --- |
| Group only | `group_id`, `audience=group_only` | **Exclude** | Members see it | Same; non-members 404 |
| Group + public | `group_id`, `audience=public` | Include if group is public | Show | **Illegal:** a private group cannot have `audience=public`. Force `group_only`. |
| Public, no group | as today | Include | — | — |

Also: every public ES query (`is_draft: false`) must add `audience=public` (or missing for old docs). `GetPublishedBlogById` must check membership when `audience=group_only`. Skip `HandleSEOForBlog` for group-only. That is a blog-service change. **Do not do it in the discussion PR.**
