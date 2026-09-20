# Group-scoped blogs — design spec

Date: 2026-09-15  
Status: **spec for review. Plan is in `docs/superpowers/plans/2026-09-15-group-scoped-blogs.md`.**  
Scope: long EditorJS articles attached to a group. **Not** discussions. **Not** blog-comment redesign.

---

## 1. Product (locked)

A writer can publish a **blog** (existing editor) with one audience:

| `audience` | `group_id` | Landing, search v2, SEO, public profile | Group page | Who may `GET` by id |
| --- | --- | --- | --- | --- |
| `public` (default) | null | Yes | — | Anyone |
| `public` | public group | Yes | Yes | Anyone |
| `group_only` | any group | **No** | Yes, **active members only** | Author **or** active member. Everyone else **404** |
| `public` + private/unlisted group | — | **Illegal.** Coerce to `group_only` at publish. | Members | Members / author |

Old blogs (no `audience` field in ES, `group_id` null in Postgres) stay **public**.

Who may set a group on publish: **active member** of that group (`group_members.status = 'active'`). Not pending joiners.

---

## 2. Blog comments (locked — do not fold into discussions)

Keep `blog_comments` for comments **on an article**. Discussions are a different object (short posts, files, group feed).

There is **no** public blog-comment HTTP API in the gateway today (only a Postgres table + account-delete cleanup, and unused `the_monkeys_comments` ES index name). Do **not** build a comment system in this project.

When comments are wired later:

- `CanReadBlog` first. If the article is 404 for this viewer, comments are 404 too.
- Do not store article comments as `discussion_posts`.

Event comments (`event_comments`) stay on events.

---

## 3. Storage

**Postgres `blog`:** add `group_id BIGINT NULL REFERENCES groups(id) ON DELETE SET NULL` and `audience VARCHAR(20) NOT NULL DEFAULT 'public'` with CHECK `public | group_only`.

**Elasticsearch** same document: `group_id` (number), `group_slug` (keyword), `audience` (keyword). Writes go through existing `SaveBlog` / publish script.

**RabbitMQ `BLOG_CREATE` / `BLOG_PUBLISH`:** include `group_id` / `audience` so user service can persist the pointer. Missing fields = public, no group.

---

## 4. Reads

Public lists and search: `must_not term audience=group_only`. Missing `audience` is not group_only, so old docs stay visible. Same `must_not` trick search v2 already uses for `is_draft`.

`GetPublishedBlogById`: do **not** hide `group_only` in ES. Fetch, then ACL. Wrong viewer → `NotFound` `"blog not found"` (no leak).

Author always reads their own published `group_only` blog.

`Authorize` on groups already returns `is_member` and `member_status`. Reuse it. No new groups RPC unless a test proves we need one.

---

## 5. SEO and AI

Skip `HandleSEOForBlog` when `audience=group_only`. AI queue may still run (internal). Do not submit group-only URLs to Google.

---

## 6. Group delete

Deleting a group does **not** delete blogs. Postgres `blog.group_id` is `ON DELETE SET NULL`. Keep `audience` as written.

`GROUP_DELETE` fans out to storage (MinIO `groups/{slug}/`) **and** the blog queue (routing key index 3). The blog consumer runs `update_by_query` on documents with `term group_slug=<slug>` and removes `group_slug` and `group_id` from Elasticsearch. It does **not** change `audience`.

That prevents a later group that reuses the slug from inheriting old articles. A former `group_only` post becomes author-only (there is no group to join). A former public+group post stays public and is no longer listed on a group page.

---

## 8. Scheduled publish after kick / leave

A live publish re-checks `Authorize` (active member). The scheduler must do the same at fire time.

If the scheduled doc has a `group_slug` and the author is **not** an active member (left, kicked, banned) **or** the group no longer exists: **still publish**, strip `group_slug` / `group_id` on that document in the same ES update as publish, **keep `audience`**. `group_only` becomes author-only. `public` stays public and leaves the group page.

Do not hold or fail the job for membership. If groups gRPC is down, fail the attempt so the scheduler retries (do not publish into a group you could not check).

The `BLOG_PUBLISH` Rabbit message must carry the post-detach `group_slug` (empty) so Postgres `group_id` is cleared.

---

## 9. Group visibility public → private / unlisted

`public` + private/unlisted is illegal. Coerce at publish **and** when a group’s visibility becomes `private` or `unlisted`.

After a successful `UpdateGroup`, if the resulting visibility is `private` or `unlisted`, fan out `GROUP_AUDIENCE_COERCE` to the user queue (routing key 1) and the blog queue (routing key 3):

- Elasticsearch: `update_by_query` docs with that `group_slug` that are **not** already `group_only` (missing `audience` counts as public) → set `audience=group_only`. Keep `group_slug`.
- Postgres: `UPDATE blog SET audience = 'group_only' WHERE group_id = <id> AND audience = 'public'`.

Switching the group back to `public` does **not** auto-public those posts.

---

## 10. Read ACL on id-based surfaces

Wrong viewer on a `group_only` post is always HTTP **404** `"blog not found"`, never 403.

Apply `canViewPublishedBlog` (same as GET by id) to:

- GET `/api/v2/blog/:blog_id` (already)
- GET `/api/v2/blog/:blog_id/stats` (`AuthOptional`)
- POST `/api/v2/blog/:blog_id/activity` (`AuthOptional`)
- POST like and bookmark
- GET like/bookmark counts and is-liked / is-bookmarked

`GET /in-my-bookmark` drops posts the viewer can no longer read. Unlike and remove-bookmark stay allowed so the viewer can clean their own list without leaking the article.

Group membership does **not** grant edit, publish, archive, or delete on someone else’s blog. Those stay on `blog_permissions` (owner / co-author).

---

## 11. Out of scope

Discussions, changing EditorJS, migrating `blog_comments` into discussions, rewriting old ES mappings beyond new fields on write, frontend (separate repo).
