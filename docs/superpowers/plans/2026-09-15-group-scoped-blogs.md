# Group-scoped blogs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a member publish an EditorJS blog to a group as `public` (group + landing) or `group_only` (members only), without leaking private-group articles into home, search, or SEO.

**Architecture:** Keep the existing split (ES = body, Postgres = pointer). Add `group_id` / `audience` to both. Public ES lists `must_not` `audience=group_only` (missing field = public). `GetPublishedBlogById` fetches then 404s unless the viewer is the author or an active member. Reuse groups `Authorize` (`is_member`, `member_status`). Skip Google SEO for `group_only`.

**Tech Stack:** Go, Elasticsearch `the_monkeys_blogs`, Postgres `blog`, gRPC additive fields, Gin, existing groups `Authorize`.

**Spec:** `docs/superpowers/specs/2026-09-15-group-scoped-blogs-design.md`

**Not this plan:** discussions (`docs/superpowers/specs/2026-09-15-group-discussions-design.md`), new blog-comment APIs, `message_threads`.

## Global Constraints

- Do not rewrite existing blog rows or reindex the whole ES cluster. Old docs without `audience` stay public.
- Private / unlisted group + `audience=public` → coerce to `group_only` at publish.
- Wrong viewer on a group-only post → gRPC/HTTP **NotFound** `"blog not found"`, never 403.
- User runs `protoc` in WSL. Agents must not run protoc or hand-edit `*.pb.go`.
- Do not git commit unless the user explicitly asks. Skip every commit step until then.
- TDD for helpers and ACL. Human copy only in status messages.
- Do not mix this with the other agent's in-progress branch work without coordinating.

### File map

| File | Responsibility |
| --- | --- |
| `schema/000022_blog_group_audience.up.sql` (or next free number) | `blog.group_id`, `blog.audience` |
| `microservices/the_monkeys_blog/internal/audience/audience.go` | Parse, coerce, public-list `must_not`, `CanRead` |
| `apis/serviceconn/gateway_blog/pb/gw_blog.proto` | Additive `group_slug`, `audience` on publish + get |
| `microservices/the_monkeys_blog/internal/database/opensearch.go` | Publish script sets fields; public queries filter |
| `microservices/the_monkeys_blog/internal/database/v2_queries.go` | Home / follow / tags public filter |
| `microservices/the_monkeys_blog/internal/database/blogs_matadata.go` | Metadata public filter |
| `microservices/the_monkeys_gateway/internal/blogsearch/query.go` | Search v2 `must_not audience=group_only` |
| `microservices/the_monkeys_blog/internal/services/service.go` | Publish coerce + skip SEO; GetById ACL |
| `microservices/the_monkeys_users/internal/database/database.go` | Persist group on create/publish |
| `common/interservice/message.go` | Optional `GroupId`, `Audience` on the envelope |
| `microservices/the_monkeys_gateway/internal/blog/routes.go` + handler | Pass group_slug; group blogs list route |
| `microservices/the_monkeys_groups` | Reuse `Authorize` only |

---

### Task 1: Audience helper (no I/O)

**Files:**
- Create: `microservices/the_monkeys_blog/internal/audience/audience.go`
- Test: `microservices/the_monkeys_blog/internal/audience/audience_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `const AudiencePublic = "public"`
  - `const AudienceGroupOnly = "group_only"`
  - `func Normalize(audience string) string` — empty/`public` → `public`; `group_only` stays; anything else → `public`
  - `func Coerce(audience, groupVisibility string) string` — if visibility is `private` or `unlisted`, return `group_only`; else `Normalize(audience)`
  - `func IsGroupOnly(docAudience any) bool` — true only if string/value is `group_only`
  - `func PublicListMustNot() map[string]interface{}` — `{"term": {"audience": "group_only"}}`
  - `func CanRead(audience, ownerAccountID, viewerAccountID string, memberActive bool) bool`

- [ ] **Step 1: Write the failing test**

```go
package audience

import "testing"

func TestNormalize(t *testing.T) {
	if Normalize("") != AudiencePublic {
		t.Fatal("empty is public")
	}
	if Normalize("group_only") != AudienceGroupOnly {
		t.Fatal("keep group_only")
	}
	if Normalize("secret") != AudiencePublic {
		t.Fatal("unknown collapses to public")
	}
}

func TestCoercePrivateGroup(t *testing.T) {
	if Coerce("public", "private") != AudienceGroupOnly {
		t.Fatal("private group cannot be public")
	}
	if Coerce("public", "unlisted") != AudienceGroupOnly {
		t.Fatal("unlisted same as private")
	}
	if Coerce("public", "public") != AudiencePublic {
		t.Fatal("public group may be public")
	}
}

func TestIsGroupOnlyMissingField(t *testing.T) {
	if IsGroupOnly(nil) || IsGroupOnly("") {
		t.Fatal("legacy docs are public")
	}
	if !IsGroupOnly("group_only") {
		t.Fatal("explicit group_only")
	}
}

func TestCanRead(t *testing.T) {
	if !CanRead("public", "owner", "", false) {
		t.Fatal("anonymous reads public")
	}
	if CanRead("group_only", "owner", "stranger", false) {
		t.Fatal("stranger cannot read group_only")
	}
	if !CanRead("group_only", "owner", "owner", false) {
		t.Fatal("author can read")
	}
	if !CanRead("group_only", "owner", "member", true) {
		t.Fatal("active member can read")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./microservices/the_monkeys_blog/internal/audience -count=1`

Expected: FAIL package not found / undefined

- [ ] **Step 3: Write minimal implementation**

```go
package audience

const (
	AudiencePublic    = "public"
	AudienceGroupOnly = "group_only"
)

func Normalize(audience string) string {
	if audience == AudienceGroupOnly {
		return AudienceGroupOnly
	}
	return AudiencePublic
}

func Coerce(audience, groupVisibility string) string {
	switch groupVisibility {
	case "private", "unlisted":
		return AudienceGroupOnly
	default:
		return Normalize(audience)
	}
}

func IsGroupOnly(docAudience any) bool {
	s, _ := docAudience.(string)
	return s == AudienceGroupOnly
}

func PublicListMustNot() map[string]interface{} {
	return map[string]interface{}{
		"term": map[string]interface{}{"audience": AudienceGroupOnly},
	}
}

func CanRead(audience, ownerAccountID, viewerAccountID string, memberActive bool) bool {
	if Normalize(audience) != AudienceGroupOnly {
		return true
	}
	if viewerAccountID != "" && viewerAccountID == ownerAccountID {
		return true
	}
	return memberActive
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./microservices/the_monkeys_blog/internal/audience -count=1`

Expected: PASS

---

### Task 2: Postgres pointer columns

**Files:**
- Create: `schema/000022_blog_group_audience.up.sql` (if `000022` exists, use next)
- Create: `schema/000022_blog_group_audience.down.sql`

**Interfaces:**
- Consumes: `groups(id)`, `blog`
- Produces: `blog.group_id`, `blog.audience`

- [ ] **Step 1: Write up migration**

```sql
ALTER TABLE blog
    ADD COLUMN IF NOT EXISTS group_id BIGINT NULL REFERENCES groups(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS audience VARCHAR(20) NOT NULL DEFAULT 'public';

ALTER TABLE blog DROP CONSTRAINT IF EXISTS chk_blog_audience;
ALTER TABLE blog ADD CONSTRAINT chk_blog_audience
    CHECK (audience IN ('public', 'group_only'));

CREATE INDEX IF NOT EXISTS idx_blog_group_id ON blog(group_id) WHERE group_id IS NOT NULL;
```

- [ ] **Step 2: Write down migration**

```sql
DROP INDEX IF EXISTS idx_blog_group_id;
ALTER TABLE blog DROP CONSTRAINT IF EXISTS chk_blog_audience;
ALTER TABLE blog DROP COLUMN IF EXISTS audience;
ALTER TABLE blog DROP COLUMN IF EXISTS group_id;
```

- [ ] **Step 3: Apply locally via existing `db-migrations` compose service** (do not hand-run against prod).

---

### Task 3: Proto fields (user runs protoc)

**Files:**
- Modify: `apis/serviceconn/gateway_blog/pb/gw_blog.proto`

**Interfaces:**
- Consumes: existing `PublishBlogReq` (fields 1–11), `BlogByIdRes`
- Produces: additive fields; generated Go after **user** runs protoc

- [ ] **Step 1: Add to `PublishBlogReq`**

```protobuf
    string group_slug = 12; // empty = no group
    string audience = 13;   // 'public' | 'group_only'; empty = public
```

- [ ] **Step 2: Add to `BlogByIdRes` and the v2 metadata maps used by the client (JSON from ES is enough for v2 maps; proto Get-by-id needs):**

```protobuf
    string group_slug = 9;
    string audience = 10;
```

Check `BlogByIdRes` current field numbers in the proto before picking 9/10. Use the **next unused** numbers. Never reuse.

- [ ] **Step 3: Ask the user to run protoc.** Do not generate or edit `*.pb.go`.

---

### Task 4: Inter-service message + user-service write

**Files:**
- Modify: `common/interservice/message.go` (or `the_monkeys_users` message struct if that is what the consumer unmarshals — match `TheMonkeysMessage` / `InterServiceMessage` used by `BLOG_CREATE`)
- Modify: `microservices/the_monkeys_users/internal/database/database.go` `AddBlogWithId` and `UpdateBlogStatusToPublish`
- Test: `common/interservice/message_test.go` if envelope tests already exist

**Interfaces:**
- Consumes: JSON fields `group_id` (int64, 0 = none), `audience`
- Produces: Postgres `blog.group_id` / `blog.audience` on create and publish

- [ ] **Step 1: Add optional JSON fields** `GroupId int64 \`json:"group_id,omitempty"\`` and `Audience string \`json:"audience,omitempty"\``.

- [ ] **Step 2: `AddBlogWithId` INSERT becomes**

```sql
INSERT INTO blog (user_id, blog_id, status, group_id, audience)
VALUES ($1, $2, $3, NULLIF($4, 0), COALESCE(NULLIF($5, ''), 'public'))
RETURNING id;
```

Treat `group_id` 0 as SQL NULL.

- [ ] **Step 3: `UpdateBlogStatusToPublish` also sets `group_id` and `audience` when the message carries them** so a blog drafted without a group can gain one at publish.

---

### Task 5: Persist fields on draft + publish in ES

**Files:**
- Modify: `microservices/the_monkeys_blog/internal/database/opensearch.go` `PublishBlogById` script
- Modify: `microservices/the_monkeys_blog/internal/services/service.go` `PublishBlog`
- Modify: `microservices/the_monkeys_blog/internal/services/service_v2.go` `DraftBlogV2` (copy `group_slug` / `audience` through `req` so `SaveBlog` indexes them)
- Modify: `microservices/the_monkeys_blog/internal/scheduler/scheduler.go` if scheduled publish calls SEO

**Interfaces:**
- Consumes: `audience.Coerce`, groups visibility (blog service may get visibility from gateway-passed fields or a groups lookup)
- Produces: ES `_source.audience`, `_source.group_slug`, `_source.group_id`

- [ ] **Step 1: Extend `PublishBlogById` to accept audience + group fields** (new options struct, do not break existing tests). Script:

```
ctx._source.is_draft = false;
ctx._source.is_scheduled = false;
ctx._source.published_time = params.published_time;
ctx._source.audience = params.audience;
if (params.group_slug != '') { ctx._source.group_slug = params.group_slug; }
if (params.group_id != 0) { ctx._source.group_id = params.group_id; }
```

- [ ] **Step 2: In `PublishBlog`, resolve group**

If `req.GroupSlug == ""`: audience = `public`, skip groups.

If set: gateway or blog service must know visibility + membership. **Prefer gateway:** validate member via existing `Authorize`, coerce audience, pass slug + audience + numeric id if proto allows. If proto should not carry numeric id, pass slug only and let user service resolve `groups.id` from slug.

Simplest lock: **gateway** calls `Authorize`; rejects if `!is_member || member_status != "active"` with `"join this group to publish there"`; sets audience with `Coerce`; forwards slug + audience on `PublishBlogReq`. User service looks up `groups.id` from slug when updating `blog.group_id`.

- [ ] **Step 3: Skip SEO when `audience.IsGroupOnly`**

In `PublishBlog` around the `HandleSEOForBlog` goroutine: if group-only, do not call SEO. Same in `scheduler.PublishScheduledBlog`.

- [ ] **Step 4: Draft stream** — if the client sends `group_slug` / `audience` on the map, leave them on `req` so `SaveBlog` stores them. Do not invent defaults that mark drafts group_only by accident.

---

### Task 6: Public ES lists hide `group_only`

**Files:**
- Modify: `opensearch.go` queries that already `term is_draft: false` **except** get-by-id and owner-owned draft/publish dashboards
- Modify: `v2_queries.go` `GetBlogsOfUsersByAccountIds`, `GetAllPublishedBlogsLatestFirst`, tag listings used for public surfaces
- Modify: `blogs_matadata.go` `GetAllPublishedBlogsMetadata` and public metadata
- Modify: `microservices/the_monkeys_gateway/internal/blogsearch/query.go` `mustNot` slice (~line 216)

**Interfaces:**
- Consumes: `audience.PublicListMustNot()`
- Produces: public feeds without group-only docs

Append to each public `bool.must_not` array:

```go
audience.PublicListMustNot(),
```

**Do not** add this to:

- `GetPublishedBlogById` / `GetPublishedBlogByIdAndOwner` (ACL happens after fetch)
- Draft queries (`is_draft: true`)
- Owner dashboard RPCs that list **this** user’s published posts (author must see their group-only posts)

- [ ] **Step 1: Add `must_not` to search v2** (`query.go` `mustNot`).

- [ ] **Step 2: Add `must_not` to home/latest/tags/follow-feed functions listed above.**

- [ ] **Step 3: Unit-test `buildSearchBody` if tests exist under `blogsearch`; otherwise add a small test that the marshaled query contains `"group_only"`.**

Run: `go test ./microservices/the_monkeys_gateway/internal/blogsearch ./microservices/the_monkeys_blog/internal/database -count=1`

---

### Task 7: Get-by-id ACL

**Files:**
- Modify: `microservices/the_monkeys_blog/internal/services/service.go` `GetPublishedBlogById`
- Modify: gateway blog GET handler so it passes **viewer** `account_id` (from JWT, may be empty)
- Reuse groups `Authorize` from **gateway** (blog service should not take a hard groups dependency if the gateway can 404 after fetch)

**Preferred shape (keep blog service dumb):**

1. Gateway fetches the blog as today.
2. Read `audience` / `group_slug` from the JSON/proto.
3. If `audience.CanRead(...)` with `memberActive=false` and viewer is not owner → call `Authorize` when `group_slug != ""`.
4. `memberActive := resp.GetIsMember() && resp.GetMemberStatus() == "active"`.
5. If still not `CanRead` → `404` `{"error":"blog not found"}`.

If Get-by-id is implemented only inside the blog service without gateway wrapping, pass `viewer_account_id` on `BlogByIdReq` (additive proto field) and inject a groups client. **Prefer gateway wrap** to avoid a new blog→groups dial.

- [ ] **Step 1: Test `CanRead` cases in Task 1 are the source of truth.**

- [ ] **Step 2: Gateway GET published blog applies ACL.** Find the existing GET-by-id handler in `microservices/the_monkeys_gateway/internal/blog/routes.go` (search `GetPublishedBlogById`). After a 200 from gRPC, inspect audience.

- [ ] **Step 3: Manual cases**
  - public blog, logged out → 200
  - group_only, stranger → 404
  - group_only, author → 200
  - group_only, active member → 200

---

### Task 8: List blogs on a group

**Files:**
- Modify: `microservices/the_monkeys_gateway/internal/groups/routes.go` (or blog routes) — new `GET /api/v1/groups/:slug/blogs`
- Modify: blog ES — new query `GetPublishedBlogsByGroupSlug(ctx, slug, includeGroupOnly bool)`

**Interfaces:**
- Consumes: `Authorize` for the slug
- Produces: published blogs with `group_slug` = slug; if viewer is not an active member, extra filter `must_not audience=group_only` (so public-group + `audience=public` posts remain visible to strangers; private group strangers get **404** on this route, same as a missing group)

- [ ] **Step 1: Gateway**

```
authz := Authorize(accountID, slug)
if !authz.GroupExists { 404 group not found }
if visibility in (private, unlisted) && !(is_member && active) { 404 group not found }
includeGroupOnly := is_member && active
blogs := blogClient.ListByGroup(slug, includeGroupOnly)
```

- [ ] **Step 2: ES query** `must`: `term group_slug`, `term is_draft false`; if `!includeGroupOnly` add `must_not audience=group_only`.

---

### Task 9: Blog comments — do not redesign

**Files:** none for a new API.

**Locked decision:** comments on articles stay `blog_comments`. Discussions stay `discussion_posts`. Event comments stay `event_comments`.

There is no gateway blog-comment route today. Do not add one in this plan.

If a later PR adds `GET/POST /api/v1/blog/:id/comments`, it **must** call the same `CanRead` as Task 7 first.

- [ ] **Step 1: No code.** Confirm in review that nobody pointed blog comments at the discussion tables.

---

### Task 10: Regression sweep

- [ ] **Step 1:** `go test ./microservices/the_monkeys_blog/... ./microservices/the_monkeys_gateway/internal/blogsearch/... ./microservices/the_monkeys_users/... -count=1`

- [ ] **Step 2:** Publish a normal blog with no group. Home and search still show it. SEO still runs.

- [ ] **Step 3:** As a member of a **public** group, publish `audience=public`. Shows on landing **and** `GET /groups/:slug/blogs`.

- [ ] **Step 4:** Same group, publish `group_only`. Hidden from home/search. Visible on group blogs as member. Stranger GET-by-id → 404.

- [ ] **Step 5:** **Private** group, try `audience=public`. Stored as `group_only`. Stranger group blogs route → 404. No SEO call in logs.

---

## Spec coverage

| Spec rule | Task |
| --- | --- |
| Default public / legacy missing field | 1, 6 |
| Coerce private/unlisted | 1, 5 |
| ES + Postgres fields | 2, 4, 5 |
| Public lists hide group_only | 6 |
| GET 404 | 7 |
| Group page list | 8 |
| Skip SEO | 5 |
| Active member to publish | 5 |
| Blog comments not discussions | 9 |

## Placeholder scan

No TBD. Proto numbers must be checked against the live `gw_blog.proto` before adding fields. Migration number must be the next free `schema/0000xx`.
