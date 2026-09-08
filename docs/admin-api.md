# Admin API

Base URL: `http://localhost:8081/api/v1/admin`  
(`THE_MONKEYS_GATEWAY_HTTP_PORT`, default 8081.)

Daily staff APIs use the same login as the public site: cookie `mat` or `Authorization: Bearer <access-jwt>`. Platform role comes from `user_role.role_desc` on Validate. Do **not** send `X-Admin-Key` on these routes. Signup stays Viewer (`role_id = 4`).

Money fields are `{ "paise": number, "inr": "941.00" }`. Lists take `limit` (default 20, max 100) and `offset`. Search uses `q` where noted.

## Role matrix

| Route group | Admin | Support | Community | Viewer |
| --- | --- | --- | --- | --- |
| `/payments/*` | yes | no | no | no |
| `/stats` | yes (with `payments_inr`) | yes (no payments) | no | no |
| `/users`, `/blogs`, `/events`, `/groups` lists and Admin actions | yes | no | no | no |
| `/verifications*` | yes | yes | no | no |
| NSFW + hide event comment/question | yes | no | yes | no |

## Staff JWT routes

### Stats (Admin or Support)

`GET /stats`

User/blog/event/group counts. Admin also gets `payments_inr`. Support omits that block. `blogs.orphan_es` is `-1` when Elasticsearch has more than 100 docs (page the orphans list instead).

### Users (Admin)

`GET /users?q=&limit=&offset=` — username, email, status, role, verified, flags.

`POST /users/:id/role` body `{ "role": "Support"|"Community"|"Admin"|"Viewer" }`. Cannot demote the last Admin (409).

`POST /users/:id/flag` body `{ "type": "bot"|"fake"|"spam", "reason": "…" }` — writes `user_admin_flags`.

`POST /users/:id/unflag` optional `{ "type": "bot" }`.

`POST /users/:id/suspend` — sets `user_status` to `suspended`.

`DELETE /users/:id` — same delete pipeline as self-service account delete; actor is the logged-in staff user. Returns **409** if the account organizes a captured/pending paid event, created a group that still has such an event, or still has a payment-linked RSVP (`event_payments` is never mutated). Cancel or settle first.

### Blogs (Admin)

`GET /blogs?status=&q=&limit=&offset=` — Postgres `blog` rows.

`GET /blogs/orphans` — Elasticsearch ids on this page that are not in `blog.blog_id`.

`POST /blogs/:blog_id/unpublish` — moves the ES document to Draft and updates Postgres via the existing queue.

`DELETE /blogs/:blog_id` — hard delete: Elasticsearch document, Postgres blog refs (`blog`, permissions, co-authors, bookmarks), and files (MinIO `posts/{blogId}/` plus CAS refs). Uses the same `BLOG_DELETE` Rabbit fan-out as author delete. Unpublish stays the soft action.

### Events (Admin)

`GET /events?status=&q=&group_slug=&limit=&offset=` — includes drafts.

`POST /events/:slug/cancel` — cancels without being the host.

`POST /events/:slug/unpublish` — sets draft if there are no confirmed paid attendees; otherwise 409.

`DELETE /events/:slug` — hard delete: event row (RSVPs/co-hosts/comments unlink via FKs), MinIO `events/{slug}/`. **409** if captured or pending payments exist. Does not touch `event_payments`. Cancel/unpublish stay the soft actions.

### Groups (Admin)

`GET /groups?status=&q=&limit=&offset=`

`POST /groups/:slug/suspend` — `groups.status = suspended`.

`DELETE /groups/:slug` — hard delete: group row (members unlink via FKs), MinIO `groups/{slug}/`. **409** if any child event has captured or pending payments. Child events are detached (`group_id` SET NULL), not deleted.

### Payments (Admin)

`GET /payments/events?status=&q=&limit=&offset=`

Per event with captured payments: slug, title, host, start time, gross / fee / GST / host payable / refunded / settled / open payable.

`GET /payments/events/:slug`

Event header, `event_payments` lines, settlements.

`POST /payments/events/:slug/settlements`

Body: `{ "note": "upi ref 123" }`. Creates a pending settlement for current open payable (captured host payable minus pending+paid settlements). Open ≤ 0 → HTTP 409 `"nothing to settle"`.

`POST /payments/settlements/:id/mark-paid`

Body: `{ "note": "paid 12 Sep" }`. `pending` → `paid`. Does not call Razorpay. Not pending → 409.

GST Option A reminder: attendee pays listed INR; platform fee = 5% of captured gross; GST = 18% of that fee; host payable = gross − fee − GST. ₹1000 → fee ₹50 + GST ₹9 → host **₹941**.

```bash
curl -s -b mat=<access-jwt> http://localhost:8081/api/v1/admin/payments/events
curl -s -b mat=<access-jwt> http://localhost:8081/api/v1/admin/payments/events/<slug>
curl -s -b mat=<access-jwt> -H 'Content-Type: application/json' \
  -d '{"note":"upi ref 123"}' \
  http://localhost:8081/api/v1/admin/payments/events/<slug>/settlements
```

Viewer / Support / Community cookies get 403 on `/payments/*`.

### Verifications (Admin or Support)

`GET /verifications?status=&limit=&offset=`

Each item includes checksums for uploaded docs (`selfie_checksum`, `id_front_checksum`, `id_back_checksum`), not image bytes. Checksums appear after `POST /api/v1/user/verification`; `*_object_key` is `verifications/sha256/{checksum}`. Staff then fetch a short-lived URL:

`GET /api/v2/storage/verifications/:request_id/:kind/url` (`kind` = `selfie` \| `id_front` \| `id_back`). Admin and Support only (plus the requesting user). Documents stay in the private verification bucket.

`POST /verifications/:id/review`

Body: `{ "approve": true }` or `{ "approve": false, "rejection_reason": "…" }`. Reviewer is the logged-in username, not the string `"admin"`.

```bash
curl -s -b mat=<access-jwt> http://localhost:8081/api/v1/admin/verifications
```

### Moderation (Admin or Community)

Body for NSFW: `{ "reason": "…" }` (required).

- `POST /events/:slug/nsfw`
- `POST /blogs/:blog_id/nsfw`
- `POST /groups/:slug/nsfw`
- `POST /users/:id/nsfw` (`:id` is username or `account_id`)
- `POST /events/:slug/comments/:id/hide`
- `POST /events/:slug/questions/:id/hide`

Hide deletes the comment/question row. Mutating calls write `admin_audit_log`. Needs migration `000019`.

## Legacy key-gated routes

These still require a **local-network IP** and `X-Admin-Key`. They are not the staff dashboard. Backup stays on this gate until it moves to Admin JWT. Do not put payments here. Change `KEYS_ADMIN_SECRET_KEY` in production; do not drop the env var.

```
X-Admin-Key: <KEYS_ADMIN_SECRET_KEY>
Content-Type: application/json
```

Local ranges: `127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `::1/128`, `fc00::/7`.

| Method | Path | Notes |
| --- | --- | --- |
| DELETE | `/users/bulk` | Body `{ "user_ids": [...], "reason": "..." }`, max 100 |
| GET | `/users/suspicious` | Placeholder |
| GET | `/users/flagged` | Placeholder |
| GET | `/health` | Admin process health |
| GET | `/system/stats` | Placeholder |
| POST | `/backup/execute` | SSH backup across servers |

```bash
curl -X DELETE \
  "http://localhost:8081/api/v1/admin/users/bulk" \
  -H "X-Admin-Key: $KEYS_ADMIN_SECRET_KEY" \
  -H "Content-Type: application/json" \
  -d '{"user_ids":["bot1"],"reason":"bulk"}'
```

## Errors

| HTTP | Meaning |
| --- | --- |
| 401 | Missing/invalid JWT (staff) or missing/invalid `X-Admin-Key` (legacy) |
| 403 | Authenticated but wrong role, or non-local IP on legacy routes |
| 404 | Event, settlement target, or user not found |
| 409 | Nothing to settle, settlement not pending, last Admin demotion, unpublish with paid attendees, or delete blocked by captured/pending payments |

## Related

Handoff for the two apps (existing site + **Dashboard**): `docs/frontend-admin-dashboard.md`  
Design: `docs/superpowers/specs/2026-09-05-payments-admin-backend-design.md`  
Plan: `docs/superpowers/plans/2026-09-05-payments-admin-backend.md`  
Storage strip / draft GET / CAS GC: `docs/superpowers/specs/2026-09-06-storage-v2-strip-gc-visibility-design.md`
