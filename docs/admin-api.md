# Admin API

Base URL: `http://localhost:8081/api/v1/admin`  
(`THE_MONKEYS_GATEWAY_HTTP_PORT`, default 8081.)

Daily staff APIs use the same login as the public site: cookie `mat` or `Authorization: Bearer <access-jwt>`. Platform role comes from `user_role.role_desc` on Validate. Do **not** send `X-Admin-Key` on these routes. Signup stays Viewer (`role_id = 4`).

Money fields are `{ "paise": number, "inr": "941.00" }`. Lists take `limit` (default 20, max 100) and `offset`. Search uses `q` where noted. Staff routes also retain the existing private-network gate; authentication alone does not make the dashboard public.

## Role matrix

- Admin: all current staff permissions, including payments and role/account administration.
- Support: overview without financial totals, verification queue/review/document access.
- Community: overview without financial totals, user list/delete, post/event/group catalog and moderation actions, and verification queue/review/document access. Existing broad nonfinancial access is retained for compatibility. No payment, role-change, flag/unflag or account-suspend permission.
- Viewer, unknown roles, and missing identities: no staff access.

These permissions are an initial server-owned mapping of existing roles, not the future configurable multi-role finance model. The authoritative policy is `common/staffaccess/permissions.go`.

### Staff session

`GET /session`

Returns only the current username, account ID, role and explicit permissions. It uses `Cache-Control: no-store` and varies by Cookie/Authorization. A nonstaff account receives 403. Invalid or missing authentication is rejected by the existing authentication middleware. Example:

```json
{"username":"staff","account_id":"account-id","role":"Support","permissions":["staff.access","overview.read","verification.read","verification.review","verification.documents.read"]}
```

The admin app must use this endpoint, not infer staff roles from successful catalog or statistics requests. A failed or malformed session response is not permission. Direct URLs to unauthorized sections must not mount or fetch their page data. On access failures the app clears the staff view and offers retry or logout.

### Staff access diagnostics

JWT-protected `/api/v1/admin/*` requests receive a server-generated `X-Request-ID` response header. Gateway authentication/permission-denial warnings and the corresponding authz `Validate` records share its `staff_request_id`. Client-provided request IDs are not reused. This identifier is diagnostic only and never grants access.

- Gateway warnings distinguish missing credentials, authentication failure, missing identity, and permission denial. Permission warnings include the received role, required permission, HTTP status, method and route template, not query strings.
- Authz logs `staff identity resolved` at info level for gateway-marked staff validation. `db_role` is the current role read from the database; `token_role` is the claim issued earlier. Authorization still uses the current database role. Ordinary public-page validation does not emit these staff records.
- At startup, authz logs `authz database identity` at info level: actual database name/user, server address/port, current schema/search path, and resolved `user_account`/`user_role` table schemas. This is one read-only catalog query with a two-second timeout. A diagnostic failure warns and does not prevent service startup or change authorization.

Production's default info level includes these records, including denial warnings, without globally enabling debug logging. Explicit higher log levels still apply. These diagnostic records exclude passwords, signing secrets, tokens, cookies, admin keys, email addresses, request bodies and raw database/RPC errors. Do not collect or share full environment files or authentication headers while investigating. No schema migration or protobuf generation is required.

Current permission names include `staff.access`, `overview.read`, `users.read`, `users.manage`, `users.delete`, `posts.read`, `posts.manage`, `posts.orphans.read`, `events.read`, `events.manage`, `groups.read`, `groups.manage`, `verification.read`, `verification.review`, `verification.documents.read`, `moderation.manage`, `payments.read`, and `payments.manage`.

Deploy authz, event service and gateway changes before the new admin bundle. The admin deliberately has no role-guessing fallback if `/session` is unavailable. Public login routes and response shapes remain unchanged. Suspended accounts are rejected during login, token issuance, validation, identity decoding and refresh; other existing account states retain their earlier behavior. The legacy auth verification-review handler and authz service both enforce staff review permission, with a current database check in authz.

## Staff JWT routes

### Stats (Admin, Support or Community)

`GET /stats`

User/blog/event/group counts. Admin also gets `payments_inr`. Support and Community omit that block. `blogs.orphan_es` is `-1` when Elasticsearch has more than 100 docs (page the orphans list instead).

### Users (Admin or Community for list/delete; Admin for other changes)

`GET /users?q=&limit=&offset=` — username, email, status, role, verified, flags.

`POST /users/:id/role` body `{ "role": "Support"|"Community"|"Admin"|"Viewer" }`. Cannot demote the last Admin (409).

`POST /users/:id/flag` body `{ "type": "bot"|"fake"|"spam", "reason": "…" }` — writes `user_admin_flags`.

`POST /users/:id/unflag` optional `{ "type": "bot" }`.

`POST /users/:id/suspend` — sets `user_status` to `suspended`.

`DELETE /users/:id` — same delete pipeline as self-service account delete; actor is the logged-in staff user. Returns **409** if the account organizes a captured/pending paid event, created a group that still has such an event, or still has a payment-linked RSVP (`event_payments` is never mutated). Cancel or settle first.

### Posts (Admin or Community; orphan listing is Admin-only)

`GET /blogs?status=&q=&limit=&offset=` — Postgres `blog` rows.

`GET /blogs/orphans` — Elasticsearch ids on this page that are not in `blog.blog_id`.

`POST /blogs/:blog_id/unpublish` — moves the ES document to Draft and updates Postgres via the existing queue.

`DELETE /blogs/:blog_id` — hard delete: Elasticsearch document, Postgres blog refs (`blog`, permissions, co-authors, bookmarks), and files (MinIO `posts/{blogId}/` plus CAS refs). Uses the same `BLOG_DELETE` Rabbit fan-out as author delete. Unpublish stays the soft action.

### Events (Admin or Community)

`GET /events?status=&q=&group_slug=&limit=&offset=` — includes drafts.

`POST /events/:slug/cancel` — cancels without being the host.

`POST /events/:slug/unpublish` — sets draft if there are no confirmed paid attendees; otherwise 409.

`DELETE /events/:slug` — hard delete: event row (RSVPs/co-hosts/comments unlink via FKs), MinIO `events/{slug}/`. **409** if captured or pending payments exist. Does not touch `event_payments`. Cancel/unpublish stay the soft actions.

### Groups (Admin or Community)

`GET /groups?status=&q=&limit=&offset=`

`POST /groups/:slug/suspend` — `groups.status = suspended`.

`DELETE /groups/:slug` — hard delete: group row (members unlink via FKs), MinIO `groups/{slug}/`. **409** if any child event has captured or pending payments. Child events are detached (`group_id` SET NULL), not deleted.

### Payments (Admin)

`GET /payments/events?status=&q=&limit=&offset=`

Per event with payment history, including fully refunded events: slug, title, host, start time, gross / fee / GST / host payable / refunded / settled / open payable. Search matches event title, slug and host username.

Additive finance fields (old fields remain):

- `pending_settlements`: pending reservations, not completed payments.
- `paid_settlements`: settlements staff have recorded paid.
- `host_balance`: current captured-status host share minus paid settlements, before pending reservations. This is signed: a negative value flags a recovery/reconciliation obligation after a refund.
- Legacy `settled` continues to mean pending plus paid; do not label it money already transferred.
- `open_payable` remains the nonnegative amount available to reserve: host share minus pending and paid settlements, floored at zero.

Captured aggregates exclude both refunded and refund-pending payment rows. These records are not provider/bank reconciliation proof. Neither a reservation nor a staff-paid record calls a payment provider. New fields absent from an older event service must be treated as unavailable, not zero.

`GET /payments/events/:slug`

Event header, `event_payments` lines, settlements. Payment lines add `id`, `captured_at`, `refund_gross`, `razorpay_refund_id`, `refunded_at`, and `currency`. Settlement lines add `created_at`, `marked_paid_at`, `marked_paid_by`, and `currency`. New timestamps are RFC3339 UTC strings or null when absent; the existing header `start_time` representation is unchanged. Detail history is currently unpaginated.

This is the existing capture and manual settlement history, not the full append-only accounting ledger, two-person approval flow, payout holds, or automated reconciliation from the feature plan. Those are follow-on work. The UI requires a completed-transfer reference before recording paid, but the legacy write API is unchanged and does not verify bank evidence.

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

### Verifications (Admin, Support or Community)

#### Private document viewing

`GET /api/v2/storage/verifications/:request_id/:kind/content`, where `kind` is `selfie`, `id_front`, or `id_back`, returns authenticated JPEG, PNG, or WebP bytes. Owners may read their own documents; staff reading another applicant's evidence require `verification.documents.read` and the existing private-network restriction. Login/session checks run on every request.

The gateway reads through its existing internal MinIO client. This endpoint does not redirect to a public storage hostname, generate browser storage signatures, create buckets, change bucket policies, or write objects. It fails closed if the private bucket policy cannot be checked or a policy is attached. Responses use `Cache-Control: private, no-store`, `nosniff`, and safe image content types. Transfers are bounded by the existing verification-upload size limit, a 20-second read context, and a response write deadline. Cancellation interrupts blocked writes; the deadline is cleared before the handler returns.

The staff UI fetches bytes through its existing same-origin `/api` proxy with credentials and displays short-lived browser Blob URLs. These URLs are released on request changes, retry, failure, and page disposal. Review actions remain unavailable until all attached images have displayed successfully. ID-document requests must include both required selfie and ID-front references; document-free readiness is reserved for explicitly classified social-proof requests.

The existing `/api/v2/storage/verifications/:request_id/:kind/url` endpoint and its JSON contract remain unchanged for existing clients. Deploy the gateway change before updating the staff UI; there is deliberately no fallback to public signed links. No database migration, protobuf generation, MinIO upgrade/configuration change, or admin `GATEWAY_URL` change is required.

Safe failure responses include a `request_id` matching the `verification document unavailable` warning. `404` means the request/document is absent; backend storage access failures return `503`, not a staff-session `403`. Do not log or share document bytes, signed URLs, storage credentials, or authentication headers.

`GET /verifications?status=&limit=&offset=`

Each item includes checksums for uploaded docs (`selfie_checksum`, `id_front_checksum`, `id_back_checksum`), not image bytes. Checksums appear after `POST /api/v1/user/verification`; `*_object_key` is `verifications/sha256/{checksum}`. The staff UI uses the authenticated `/content` endpoint above. Legacy clients can still fetch a short-lived URL:

`GET /api/v2/storage/verifications/:request_id/:kind/url` (`kind` = `selfie` \| `id_front` \| `id_back`). Admin, Support and Community (plus the requesting user). Documents stay in the private verification bucket.

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
