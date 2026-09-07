# Frontend and Dashboard handoff

Date: 2026-09-06  
Branch: `feat/payments-admin-backend`  
Audience: the **existing public site**, and a **new staff app named Dashboard**.

This is the frontend contract for everything shipped on this branch (payments + staff APIs + hard-delete/cascade + storage v2 privacy/GC/strip). Staff HTTP details and curls: `docs/admin-api.md`.

---

## Two apps (locked)

| App | Who | HTTP base | Auth |
| --- | --- | --- | --- |
| **Existing site** | Viewers, authors, organizers, members | `/api/v1/*` except `/admin`, plus `/api/v2/storage/*` (and `/api/storage/*` aliases) | Cookie `mat` or `Authorization: Bearer`. Signup stays Viewer (`role_id = 4`). |
| **Dashboard** (new) | Admin, Support, Community | `/api/v1/admin/*` plus `GET /api/v2/storage/verifications/:request_id/:kind/url` for ID photos | **Same** cookie/JWT. Gateway reads `user_role` from authz `Validate`. **Do not send `X-Admin-Key`.** |

Do not put an admin panel inside the existing site. Do not point Dashboard at LAN-key routes (`X-Admin-Key` + local IP). Those remain backup/bulk-delete only.

Money JSON on staff payments APIs: `{ "paise": number, "inr": "941.00" }`. Lists: `limit` (default 20, max 100), `offset`. Search: `q` where noted.

Apply Postgres **`000018`** (payments ledger) and **`000019`** (admin flags / audit / suspend) before payments, NSFW, or suspend. Until then those tables do not exist.

---

## 1. What the backend built (from the start)

All of this is **backend only**. No Dashboard UI was built in this repo.

### 1.1 Payments + staff APIs

- Event payment ledger (`event_payments`, settlements) with GST Option A: attendee pays listed INR; fee = 5% of captured; GST = 18% of that fee; host payable = gross − fee − GST (₹1000 → host **₹941**).
- **30 new** JWT staff routes under `/api/v1/admin` gated by platform role (Admin / Support / Community), not LAN + `X-Admin-Key`.
- JWT `Validate` now returns `role`. New tokens may include a `role` claim; the gateway uses the **DB role from Validate**, so old tokens still work.
- Staff verification queue (list + approve/reject). Support (and Admin) can fetch private ID-doc URLs.

### 1.2 Hard delete + account cascade

- Blog `DELETE` is hard (ES + Postgres refs + MinIO `posts/{id}/` + CAS refs via Rabbit `BLOG_DELETE`). Unpublish stays soft.
- Event/group `DELETE` is hard (row + MinIO prefix). **409** if captured/pending payments exist. Does not mutate `event_payments`.
- Self-delete and Admin user delete are owned by the **users** service: gate (409 if paid organizer / paid child group / payment-linked RSVP), then cascade, then profile delete + `USER_ACCOUNT_DELETE`.
- Shared `common/interservice.Message` for Rabbit fan-out. Events/groups do **not** own account HTTP APIs.

### 1.3 Storage v2: strip, last-ref GC, private drafts/verification

- **New uploads only:** JPEG/PNG/WebP GPS/EXIF/XMP/IPTC/text stripped before SHA-256 / PutObject. Pixels not quality-re-encoded. Existing MinIO objects are not backfilled.
- **CAS GC:** last live `storage_asset_refs` row gone (and no `verification_requests` checksum pointer) → delete `storage_assets` + MinIO object. Same on file replace (`ReplaceAssetRef`). Event/group/profile stay path-based (their delete already removes the slug key).
- **Draft post images** are not world-readable. Anonymous unpublished GET/HEAD/list/meta/url → **404** (not 403). Published blogs stay public. CAS `GET /assets/sha256/...` is public only if **any** live blog ref is `Published`.
- **Verification docs** stay in private bucket `the-monkeys-verification`. Public CAS GET never streams `verifications/` keys. List/get verification JSON includes derived `*_object_key` = `verifications/sha256/{checksum}` (checksums still land on **submit**, not upload).

**Not in this branch:** Dashboard UI, auto host payouts, reports inbox (event reports exist in DB; no admin list; no blog/group/user report queues), storage backfill, moving event/group/profile onto CAS.

---

## 2. Authn and authz (what changed)

### 2.1 Unchanged

- Login / refresh / cookie `mat` / Bearer header.
- Signup still inserts **Viewer** (`role_id = 4`).
- Blog write still uses `AuthorizationByID` / `AuthzRequired` + `blog_permissions` (Create/Edit/Read/Publish/…).
- Event/group host guards on their write routes are unchanged in spirit.
- Legacy `/api/v1/admin` backup/bulk-delete still: **local IP + `X-Admin-Key`**.

### 2.2 New / changed

| Mechanism | Where | Behavior |
| --- | --- | --- |
| `AuthRequired` + `RequireRole(...)` | All **30** Dashboard JWT routes | 401 if no/invalid JWT. 403 if role is wrong (Support on payments, Viewer on everything staff, Community on users/payments). |
| `user_role` from Validate | Gateway staff middleware | Roles: `Admin`, `Support`, `Community`, `Viewer`. |
| `AuthOptional` | Storage **reads** for posts + CAS | Anonymous allowed. If JWT present, identity is set for draft ACL. Invalid token is treated as anonymous (not 401). |
| `HasBlogAccess` (never aborts) | Unpublished post GET/HEAD/list/meta/url and draft-only CAS | Calls `CheckAccessLevel`. **Read or Edit** (case-insensitive) allow. **Create alone does not** — missing Postgres `blog` row still grants Create for “new post”, which must not leak ES-only drafts. Fail closed → **404**. |
| Verification presign | `GET /api/v2/storage/verifications/:request_id/:kind/url` | Owner, **Admin**, or **Support**. Community 403. |
| Account delete mapping | `DELETE /api/v1/user/:id` | gRPC `FailedPrecondition` → **409**; `Unavailable` → **503**. |

Dashboard login is the **same** auth as the public site. After login, send the cookie/JWT on `/api/v1/admin`. If `user_role` is Viewer, every staff route is 403 — send them away in the Dashboard UI.

---

## 3. API counts (HTTP at the gateway)

Counts are routes frontends call, not gRPC.

### 3.1 New staff JWT routes — **30** (Dashboard only)

The public site must **not** call these.

**Payments (Admin only) — 4**

| Method | Path |
| --- | --- |
| GET | `/api/v1/admin/payments/events` |
| GET | `/api/v1/admin/payments/events/:slug` |
| POST | `/api/v1/admin/payments/events/:slug/settlements` |
| POST | `/api/v1/admin/payments/settlements/:id/mark-paid` |

**Users (Admin only) — 6**

| Method | Path |
| --- | --- |
| GET | `/api/v1/admin/users` |
| POST | `/api/v1/admin/users/:id/role` |
| POST | `/api/v1/admin/users/:id/flag` |
| POST | `/api/v1/admin/users/:id/unflag` |
| POST | `/api/v1/admin/users/:id/suspend` |
| DELETE | `/api/v1/admin/users/:id` |

**Blogs (Admin only) — 4**

| Method | Path |
| --- | --- |
| GET | `/api/v1/admin/blogs` |
| GET | `/api/v1/admin/blogs/orphans` |
| POST | `/api/v1/admin/blogs/:blog_id/unpublish` |
| DELETE | `/api/v1/admin/blogs/:blog_id` |

**Events (Admin only) — 4**

| Method | Path |
| --- | --- |
| GET | `/api/v1/admin/events` |
| POST | `/api/v1/admin/events/:slug/cancel` |
| POST | `/api/v1/admin/events/:slug/unpublish` |
| DELETE | `/api/v1/admin/events/:slug` |

**Groups (Admin only) — 3**

| Method | Path |
| --- | --- |
| GET | `/api/v1/admin/groups` |
| POST | `/api/v1/admin/groups/:slug/suspend` |
| DELETE | `/api/v1/admin/groups/:slug` |

**Stats + verification queue (Admin or Support) — 3**

| Method | Path |
| --- | --- |
| GET | `/api/v1/admin/stats` |
| GET | `/api/v1/admin/verifications` |
| POST | `/api/v1/admin/verifications/:id/review` |

**Moderation (Admin or Community) — 6**

| Method | Path |
| --- | --- |
| POST | `/api/v1/admin/events/:slug/nsfw` |
| POST | `/api/v1/admin/blogs/:blog_id/nsfw` |
| POST | `/api/v1/admin/groups/:slug/nsfw` |
| POST | `/api/v1/admin/users/:id/nsfw` |
| POST | `/api/v1/admin/events/:slug/comments/:id/hide` |
| POST | `/api/v1/admin/events/:slug/questions/:id/hide` |

**Verification photos (path existed; staff may use it)**

| Method | Path | Who |
| --- | --- | --- |
| GET | `/api/v2/storage/verifications/:request_id/:kind/url` | Owner, Admin, or Support. `kind` = `selfie` \| `id_front` \| `id_back`. Short-lived presigned MinIO URL. Community cannot see ID docs. |

No **new** HTTP paths were added for storage strip/GC. Visibility is a **behavior change** on existing GETs (see §3.2).

### 3.2 Modified public / user-centric routes (existing site)

Same URLs. Handle extra status codes and cookies on draft images.

| Method | Path | What changed |
| --- | --- | --- |
| DELETE | `/api/v1/user/:id` (self-delete) | **409** if captured/pending payments (organizer, group creator with paid child events, or payment-linked RSVP). **503** if events/groups are down. Body is the gRPC reason. |
| DELETE | `/api/v1/events/:slug` | **409** if captured or pending payments. Cancel so refunds can run. Unsold paid tiers do **not** block. |
| DELETE | `/api/v1/groups/:slug` | **409** if any child event has captured or pending payments. |
| GET | `/api/v1/events/:slug` | Gateway no longer extra-`Authorize`s public detail. Drafts still **404** for non-hosts. |
| GET | `/api/v1/groups/:slug` | Draft / private groups **404** for people who should not see them. |
| POST | paid ticket create / paid RSVP | Still **409** when Razorpay is not configured; host-facing copy changed. |
| GET/HEAD | `/api/v2/storage/posts/:id/:fileName` (+ `/meta`, `/url`) | **Was fully public.** Unpublished blog: send cookies. Anonymous or stranger → **404**. Published → still public, no JWT. Same for `/api/storage/...` aliases. |
| GET | `/api/v2/storage/posts/:id` (list) | Same unpublished 404 gate. |
| GET | `/api/v2/storage/assets/sha256/:p1/:p2/:fileName` | Public only if some **published** blog still refs the checksum. Draft-only CAS: cookie + blog Read/Edit. Verification keys → **404**. |
| POST/PUT | `/api/v2/storage/posts/:id` (and `PUT .../:fileName`) | Unpublished uploads get `Cache-Control: private, no-store`. Strip runs before hash; GPS is not stored. Invalid strip → 400 (`prepareAssetUpload` may surface as read/process error). |
| POST | event/group/profile image | New uploads are metadata-stripped. Same paths. |
| GET/POST | user verification | Response/list items may include `selfie_object_key`, `id_front_object_key`, `id_back_object_key`. Still **not** public URLs. Bytes still via the presign route. |

Auth `Validate` returns `role`. Existing site can ignore it unless it wants to hide a “open Dashboard” link for staff.

### 3.3 Unchanged user-centric APIs

Login, blog HTML CRUD, profiles, RSVP, Razorpay webhook, user verification **submit** (`POST /api/v1/user/verification`, `POST /api/v2/storage/verifications`), event `POST /:slug/report`, organizer cancel/unpublish. Do not rebuild them for Dashboard.

### 3.4 Legacy LAN + `X-Admin-Key` — not Dashboard

Still there: bulk user delete, backup, placeholder `/users/suspicious`, `/users/flagged`, `/system/stats`. **Do not use in Dashboard.**

### 3.5 Internal gRPC (neither frontend)

Users → events/groups: `CheckUserEventRemoval`, `RemoveUserFromEvents`, `CheckUserGroupRemoval`, `RemoveUserFromGroups`. Storage: `DeleteAssetRef` / `ReplaceAssetRef` orphans, `ResolveAssetRead`. Account delete stays `DeleteUserAccount` on users.

---

## 4. Breaking changes?

**No public URL removals.** Staff fake JSON behind LAN + `X-Admin-Key` is replaced by JWT staff routes. A client that only sends `X-Admin-Key` will **401/403** on the 30 Dashboard routes.

Existing site must handle:

1. Self-delete / event delete / group delete → **409** (paid ledger). Show the server message.
2. Unpublish event with confirmed paid attendees → **409**.
3. Last Admin demotion → **409**.
4. Draft event/group detail stays **404**.
5. **Draft editor images:** if the editor loaded `<img src="/api/v2/storage/posts/...">` without cookies (CDN, new tab, SSR without `mat`), those URLs now **404** until the blog is `Published`. Fix: send cookies / `credentials: 'include'` on draft image requests; do not hotlink draft CAS URLs on public pages.
6. Do not persist or display `*_object_key` as a public `<img src>`. Use the presign URL endpoint.

Signup `role_id = 4` is unchanged.

---

## 5. What the existing frontend should change

User-centric only. No `/api/v1/admin/*`.

**Must**

- On delete account / delete event / delete group: handle **409** (and **503** on account delete) and show the server text (“cancel or settle first”).
- Draft blog editor: all GET/HEAD/list/meta/url for `/api/v2/storage/posts/:blogId/...` and unpublished CAS must include the session cookie. Treat 404 as “no access / not published”, not “file missing” if the user is logged out.
- After publish, CAS URLs can stay public (`Cache-Control: public`). After moving a post back to draft, those URLs become private again.
- Verification: keep upload-then-submit. Checksums appear only after submit. Optional: show `*_object_key` in debug; never treat it as a public CDN path.

**Should**

- Ignore GPS/EXIF; the server strips new image uploads.
- Profile / event / group image POST/PUT: no API shape change; strip is server-side. Large files still upload; strip is not skipped at 5 MiB (that cap is blurhash only).

**Must not**

- Build staff screens in this app.
- Call `/api/v1/admin/*` or send `X-Admin-Key`.
- Fetch `GET /api/v2/storage/assets/sha256/...` for verification fingerprints (always 404). Use `/api/v2/storage/verifications/:request_id/:kind/url` only as the **owner**.

---

## 6. What Dashboard should build

Name: **Dashboard**. Login with Admin / Support / Community. Same `mat` cookie (or Bearer). If role is Viewer, show “no access”.

### 6.1 Verification queue (Support’s main screen) — **yes, docs are visible before approve/reject**

1. `GET /api/v1/admin/verifications?status=pending&limit=&offset=` (`under_review`, `approved`, `rejected` too).
2. Each row: username, type (`social_proof` \| `id_document`), country, ID type, status, checksums, **`selfie_object_key` / `id_front_object_key` / `id_back_object_key`** (derived paths, not public URLs), extra text. No image bytes in JSON.
3. For each non-empty checksum: `GET /api/v2/storage/verifications/:request_id/:kind/url` with the staff JWT. Open returned `url` (short TTL; refresh if expired).
4. `POST /api/v1/admin/verifications/:id/review` `{ "approve": true }` or `{ "approve": false, "rejection_reason": "…" }`.

Admin and Support can list, open docs, and review. Community cannot.

Documents live in **private** MinIO. They never appear on the public homepage.

### 6.2 Users, catalog, payments, moderation

Use the 30 routes in §3.1.

| | Admin | Support | Community |
| --- | --- | --- | --- |
| Payments, roles, suspend, hard-delete, catalog lists | yes | no | no |
| Stats (no `payments_inr` for Support) | yes | yes | no |
| Verification queue + ID photos | yes | yes | no |
| NSFW + hide event comment/question | yes | no | yes |

Hard vs soft:

- Blog: unpublish = soft; `DELETE` = ES + Postgres + files.
- Event: cancel/unpublish = soft; `DELETE` = 409 if paid.
- Group: suspend = soft; `DELETE` = 409 if child paid events.
- User `DELETE` = same cascade as self-delete (409 if paid).

GST: ₹1000 → host **₹941**. Marking a settlement paid does **not** call Razorpay.

### 6.3 Suggested Dashboard screens

1. Login (reuse public auth; store `mat` or Bearer).
2. Stats.
3. Verification queue + document viewer (Support).
4. Users (search, role, flag, suspend, delete).
5. Blogs / events / groups + soft and hard actions.
6. Payments per event + settlements (Admin).
7. Moderation NSFW / hide (Community).

Skip reports inbox until the backend adds list APIs. Skip legacy key routes.

### 6.4 Reported content — **not a staff inbox yet**

| Kind | User can file? | Stored? | Dashboard list API? |
| --- | --- | --- | --- |
| Events | `POST /api/v1/events/:slug/report` | `event_reports` | **No** |
| Blogs | no | no `blog_reports` | **No** |
| Groups | no | no | **No** |
| Users | no user-report route | staff flags via flag/NSFW | `GET /admin/users` (flags on the row), not a queue |

`GET /api/v1/admin/users/flagged` is still a LAN-key **placeholder**.

---

## 7. Errors both apps must show

| HTTP | Meaning |
| --- | --- |
| 401 | Missing/invalid JWT (or missing key on legacy routes) |
| 403 | Wrong role (e.g. Support hitting `/payments`) |
| 404 | Missing entity, **or** unpublished storage (do not leak drafts with 403) |
| 409 | Paid delete blocked, nothing to settle, settlement not pending, last Admin, unpublish with paid attendees |
| 400 | Image metadata strip failed on upload (reject; do not retry as a “public CDN” file) |
| 503 | Account delete cannot reach events/groups |

---

## 8. Storage contract cheat sheet (existing site)

| Situation | Request | Expected |
| --- | --- | --- |
| Published post image | GET post/CAS, no cookie | 200, public cache |
| Draft post image, logged-out | GET post/CAS, no cookie | **404** |
| Draft post image, author | GET with `mat` | 200, `private, no-store` |
| Draft post image, other user | GET with their `mat` | **404** unless they have Read/Edit on that blog |
| Same checksum used by a published blog | GET CAS, no cookie | 200 |
| Verification fingerprint | GET `/assets/sha256/...` | **404** |
| Verification as owner/staff | GET `/verifications/:id/:kind/url` | 200 + presigned `url` |

---

## Related

- Staff HTTP curls: `docs/admin-api.md`
- Payments / roles: `docs/superpowers/specs/2026-09-05-payments-admin-backend-design.md`
- Cascade delete: `docs/superpowers/specs/2026-09-06-cascade-delete-interservice-message-design.md`
- Storage strip/GC/visibility: `docs/superpowers/specs/2026-09-06-storage-v2-strip-gc-visibility-design.md`
