# Payments, speed, and admin backend — design spec

Date: 2026-09-05  
Branch: `feat/payments-admin-backend`  
Status: **design only — do not implement until this spec is approved**  
Scope: backend only (Go services, schema, gateway admin APIs). No UI.

This spec is the source of truth for an implementer who has not seen the chat. Read it before the implementation plan.

---

## 1. Product and locked decisions

Monkeys is a writing platform that also has Meetup-style events and groups. Paid event tickets already go through **platform Razorpay** (Monkeys is the merchant). Hosts do not paste their own Razorpay keys.

### Locked money rule (Option A)

Attendee pays the **listed ticket price** (after coupon), in **INR**.

On every captured payment:

- Platform fee = **5% of amount captured**
- GST = **18% of the platform fee** (not 18% of the ticket)
- Host payable = captured − fee − GST

Example: ticket **₹1000.00**

| Line | Paise | Rupees |
| --- | --- | --- |
| Gross (attendee paid) | 100000 | ₹1000.00 |
| Platform fee 5% | 5000 | ₹50.00 |
| GST 18% on fee | 900 | ₹9.00 |
| Host payable | 94100 | ₹941.00 |

Razorpay’s own MDR (card/UPI charges) is **eaten by the platform**, not by the host. Do not subtract Razorpay fees from host payable in v1.

Currency: **INR only** for paid tickets. Reject any other currency on create/update tier and on RSVP.

### Locked payout rule (v1)

v1 does **not** send money to hosts automatically (no Razorpay Route, no X payouts, no linked accounts).

Money sits in the platform Razorpay account. Admin APIs show **per-event payable**. An admin later marks a settlement as paid after they transfer money outside the app (bank/UPI). Automatic payouts are a later project.

### Locked staff auth (v1) — JWT roles, not a shared key

`X-Admin-Key` + “only from LAN” is **not** access control. Anyone on the office Wi‑Fi who has the key can delete users, and nobody on the internet can log in as a real staff member. That design is rejected.

Staff use the **same login** as everyone else (`mat` cookie / Bearer token). The gateway already validates that token via `the_monkeys_authz`. Role comes from `user_account.role_id` → `user_role.role_desc`.

| `user_role.role_desc` | Already in prod? | Staff APIs |
| --- | --- | --- |
| `Viewer` (id **4**) | Yes. **Every new signup is hardcoded to 4** in `insertIntoUserAccount`. Do not change this. | None |
| `Owner` / `Editor` | Yes (blog co-author roles, also used as account roles) | None |
| `Admin` | Yes | Payments, settlements, all lists/stats, role grants, delete/suspend, plus everything Support and Community can do |
| `Support` | Yes (row exists; almost unused for HTTP) | Verification queue: approve/reject checkmark. Cannot see payments or change roles |
| `Community` | **No — add with INSERT**, new serial id (will be 6 if 1–5 exist). Existing rows untouched | NSFW on events/groups/blogs/images; hide/remove event Q&A and comments. Cannot see payments. Cannot verify users |

HTTP prefix stays `/api/v1/admin`. Middleware:

1. `AuthRequired` (existing JWT)
2. `RequireRole(...)` reading `user_role` from the Validate response — **no extra DB round trip** (see §7)
3. Drop `LocalNetworkMiddleware` on these routes
4. Stop requiring `X-Admin-Key` for staff APIs. Keep the env var only for a later emergency break-glass if we still need it for `POST /backup/execute`; do not use it as the daily login

UI is still later. Staff can call the APIs with the same cookies the site already sets after login.

---

## 2. Current system (what already exists)

```
Browser / curl
  → the_monkeys_gateway (Gin, :8081 typically)
       → gRPC EventService  (the_monkeys_events)
       → gRPC GroupService  (the_monkeys_groups)
       → gRPC UserService / BlogService / Storage / …
  → Postgres 17
```

Run stack: `docker-compose.yml`. Migrations: `schema/*.up.sql` via `db-migrations`.

### Events (already built)

Proto: `apis/serviceconn/gateway_event/pb/gw_event.proto`  
Service: `microservices/the_monkeys_events`  
Gateway: `microservices/the_monkeys_gateway/internal/events`  
Schema: `000010_add_events` + `000011_meetup_communities` + geo/cover/rsvp/index migrations through `000017`.

Already works:

- Event CRUD, series, clone, RSVP this/series (free series only)
- Ticket tiers, coupons, waitlist, check-in
- Razorpay **order create**, **webhook HMAC**, **refund**
- Keys: `KEYS_RAZORPAY_KEY_ID`, `KEYS_RAZORPAY_SECRET`, `KEYS_RAZORPAY_WEBHOOK_SECRET` in `.env` / `config.Keys`
- Webhook REST: `POST /api/v1/events/payment/webhook`
- Paid RSVP blocked if keys empty (`pay.enabled()` false)
- Verified-host required for paid tiers

`event_attendees` already has `payment_order_id`, `payment_id`, `refund_id`, `amount_paid`, `currency`. There is **no fee/GST/host-payable ledger**.

### Groups (already built)

Proto: `apis/serviceconn/gateway_group/pb/gw_group.proto`  
Service: `microservices/the_monkeys_groups`  
Gateway: `microservices/the_monkeys_gateway/internal/groups`  
Schema: `000011`, `000012` invites, `000014` coordinates.

CRUD, members, invites, rules, group-scoped event create. No dues payment code (tables `plans`, `organizer_subscriptions`, `group_dues`, `group_due_payments` are **orphaned** — do not use them in this project).

### Admin (thin and mostly fake)

`microservices/the_monkeys_gateway/internal/admin/routes.go`

Real:

- Force delete user / bulk delete (calls UserService)
- Verification list + review
- SSH backup endpoint (dangerous; do not expand)

Stubs (return “not implemented” JSON, no DB):

- Flag / unflag / flagged list / suspicious users
- User stats, system stats

Comments in that file already list the catalog APIs we want (users, blogs, drafts, orphans, NSFW). Build those for real.

---

## 3. Problems in the current backend

### 3.1 Correctness / money

1. **No platform fee or GST.** `amount_paid` is the attendee gross. Admin cannot compute host payable without a stored snapshot (rates must not change history).
2. **Money is `float64` / `DECIMAL` used as float in Go.** `toPaise` does `amount*100 + 0.5`. Rounding drift on GST. Ledger must use **integer paise**.
3. **`amount_paid` is written at order-create**, before capture (`AttachPaymentOrder`). A pending row looks paid. Split `amount_due_paise` vs `amount_captured_paise`.
4. **Currency not forced to INR** on paid tiers (`insertTier` accepts any string).
5. **Webhook only handles `payment.captured` and `payment.failed`.** If Razorpay account is not auto-capture, payments stall. v1 assumes **auto-capture ON**; document it.
6. **Cancel RSVP refunds via `refundAll(slug)`.** That retries every cancelled-unrefunded payment on the event. That is OK as a retry, but it must also reverse host payable / fee rows (today there are none).
7. **No settlement / payout table.** Cannot show “we still owe this host ₹X”.
8. **Orphan tables** (`plans`, `group_dues`, …) look like billing exists. Ignore them in v1.

### 3.2 Why APIs feel extremely slow (auth + queries + how you run)

This is the direct answer. Slow is **not** one bug. A single `GET /api/v1/events/:slug` today is a chain of extra hops. A logged-in `GET /api/v1/events` list is worse. None of this should require Redis in v1.

#### A. Authn on **every** authenticated request (platform-wide)

`AuthRequired` / `AuthOptional` → gRPC `Validate` → **JWT parse + `CheckIfEmailExist`**.

That SQL is a 4-table join (`user_account`, `user_auth_info`, `email_validation_status`, `user_status`) keyed by email. It runs even when the JWT already has `account_id`, `email`, `username`. It does **not** return `role`. So `ctx.GetString("user_role")` is empty on normal routes (only blog `AuthzRequired` sets it, after a **second** gRPC `CheckAccessLevel`).

`Validate` also uses `context.Background()`, so a cancelled browser request still holds the authz DB pool.

This is why “authn/authz is making APIs slow” is true: **identity is re-loaded from Postgres on every call**, then events/groups **authorize again**.

#### B. Event/group extra authz (this product surface)

| Extra work | Where | What the user feels |
| --- | --- | --- |
| gRPC `Authorize` then gRPC `GetEvent` | gateway `guard.RequireVisible` + handler | Two RPCs for one page |
| `Authorize` SQL **again** inside GetEvent | `redactForViewer` | Third permission lookup |
| 15s grant cache keyed by `account_id + slug` | `authx/guard.go` | Anonymous sharing helps; **each signed-in viewer misses** |
| Groups same pattern | `groups/authx` | Same double hop on group detail |

Write paths also `authorize()` inside the DB transaction. That part stays (correct). The duplicate **read** Authorize is waste.

#### C. Heavy SQL on lists (gets worse with production volume)

| Cause | Where | Effect with lots of rows |
| --- | --- | --- |
| Correlated `COUNT(*)` per row for `attendee_count` | `eventColumns` | N subqueries per page; `000017` index helps but COUNT still runs per row |
| Series collapse correlated subquery | `seriesCollapseCond` | Per candidate row, find soonest sibling |
| Full Haversine in WHERE + ORDER BY | events `list`, groups `search.go` | btree on lat/lng cannot use `acos(...)`; seq scan as tables grow |
| COUNT query then SELECT | `list()`, `ListGroups` | Same filter twice |
| Detail `hydrate` booked COUNT per tier | `hydrate` detail=true | Extra correlated counts |
| Nominatim HTTP on create/update | `geocoding.go`, 5s, no ctx, no cache | CreateEvent can stall 5s on OSM |

#### D. Running the stack

`docker compose up --build -d` rebuilds **every** Go image. That is minutes, not API latency, but it is what “the APIs are slow” often is during local work.

#### E. What is already OK (do not rebuild)

- Gateway grant cache + singleflight for **anonymous** bursts
- Batch tag hydrate on lists
- Series collapse + `upcoming_dates` (shipped)
- `idx_events_series_upcoming`, `idx_event_attendees_confirmed`
- One long-lived gRPC client per gateway
- RSVP `FOR UPDATE` on the event row
- Webhook HMAC + idempotent confirm

Groups list already aggregates topics in SQL; it still needs the geo box + one COUNT.

#### F. How to run APIs fast **today** (no code)

1. Do not rebuild the whole compose file for a Go edit.
2. Infra only: `docker compose up -d the_monkeys_db db-migrations the_monkeys_cache rabbitmq elasticsearch-node1 minio`
3. Host binaries: `go run ./microservices/the_monkeys_authz` then events, groups, gateway. Point `.env` hosts at `localhost`.
4. Rebuild one service: `docker compose up --build -d the_monkeys_events`
5. Prove SQL: `EXPLAIN (ANALYZE, BUFFERS)` on the list query inside `the-monkeys-psql`.

Targets after Task 5+auth fix (local, warm cache, ~thousands of events): list p95 **< 100ms** in the events process; full gateway **< 200ms**. Measure before/after. Do **not** add Redis list caching in this project.

### 3.3 Admin holes (and why the key gate failed)

- Flag endpoints lie (success JSON, no table).
- No list of users/blogs/events/groups with counts.
- No payment dashboard.
- `docs/admin-api.md` path and some behaviours are wrong.
- Backup API accepts SSH password and runs `ssh`/`sshpass` from the gateway process. Treat as existing hazard; do not reuse that pattern.
- Shared `X-Admin-Key` + LAN: anyone on the network is “admin”; remote staff cannot log in as themselves; audit says `deleted_by: "admin"` with no user.

---

## 4. Approaches considered

### Payments / settlements

**A. Columns on `event_attendees` only**  
Add fee/gst/payable columns on the RSVP row. Fast. Weak for refunds, partials, and “we paid the host on date X”.

**B. Ledger tables (recommended)**  
Keep attendee row as the RSVP. Add `event_payments` (one captured/refunded payment) and `event_host_settlements` (payout batch per event or per host+event). Admin reads sums from the ledger. History is immutable.

**C. Razorpay Route / linked accounts**  
Split at capture. Needs host KYC on Razorpay. Too heavy for v1.

**Recommendation: B.** Capture still uses the existing Razorpay client. Split math in our DB.

### Admin surface

**A. New `the_monkeys_admin` microservice**  
Clean boundary, extra deploy, extra proto. Overkill for v1.

**B. Gateway talks Postgres directly for all admin reads**  
Fast counts. Breaks “service owns its tables”. Payment invariants would leak.

**C. Gateway admin HTTP + admin RPCs on existing services (recommended)**  
- Money: new RPCs on `EventService`  
- Users: new RPCs on `UserService`  
- Blogs: new RPCs on `BlogService` (+ ES for orphans)  
- Groups/events catalog: new RPCs on GroupService / EventService  

Gateway `internal/admin` stays the only HTTP admin surface. Middleware is **JWT + role**, not LAN+key.

### Staff identity

**A. Keep X-Admin-Key + LAN (rejected)**  
Not a person, not auditable, not remote-safe.

**B. JWT role on the existing `user_role` table (recommended)**  
Signup stays `role_id = 4` (Viewer). Promote a few humans to Admin / Support / Community. Validate already loads the user row — add `role_desc` to that SELECT (no extra round trip). Put `role` on new JWTs so later we can skip the DB on the hot path.

**C. New `staff_users` table**  
Duplicates `user_account`. Rejected.

### Speed

**A. Redis-cache whole list responses**  
Stale discovery, skip until lists are cheap.

**B. SQL/shape + stop duplicate auth (recommended)**  
1. Return role from existing Validate query; do not add a second lookup.  
2. Public event GET: no separate Authorize RPC (GetEvent already has the row).  
3. Bounding box, DISTINCT ON collapse, drop per-row COUNT, geocode skip/cache.

**C. Denormalize `events.confirmed_attendee_count`**  
Good later; maintain in RSVP/webhook. Optional in the same performance PR if COUNT is still hot after EXPLAIN.

**Recommendation: B now, C if EXPLAIN still shows COUNT as the bottleneck.**

---

## 5. Target architecture

```
Staff browser (same login cookies as the site)
  → Gateway /api/v1/admin/*   AuthRequired + RequireRole
       → EventService  payments only if role=Admin
       → UserService   verify if Support|Admin; lists if Admin
       → Blog/Group    NSFW if Community|Admin

Attendee checkout (unchanged REST)
  POST /api/v1/events/:slug/rsvp
       → CreateRSVP → Razorpay Orders API
  POST /api/v1/events/payment/webhook
       → verify HMAC → ConfirmPayment
            → write event_payments (gross, fee, gst, host_payable)
            → confirm attendee
```

No new Docker service. No UI.

---

## 6. Money design

### 6.1 Integer paise

All stored money is `BIGINT` paise (₹1 = 100).  
API JSON still shows rupees as strings with 2 decimals (`"1000.00"`) **and** paise integers, so admin UIs cannot float-round.

Convert:

```
paise = round_half_up(rupees * 100)
rupees_string = paise / 100 formatted with 2 fraction digits
```

Razorpay `amount` field is already paise. Change `createOrder` to take `int64` paise, not float.

### 6.2 Fee function (single source of truth)

Package: `microservices/the_monkeys_events/internal/money` (pure, unit-tested).

Constants:

```
PlatformFeeBPS = 500   // 5.00% of gross
GstBPS         = 1800  // 18.00% of platform fee
CurrencyINR    = "INR"
```

```
fee = (gross_paise * 500 + 5000) / 10000      // half-up to paise
gst = (fee * 1800 + 5000) / 10000
host = gross_paise - fee - gst
```

If `gross_paise == 0`, all zeros.  
If `host < 0` (should never happen at 5.9%), clamp host to 0 and log error — do not ship a path that can do this at 5%+18%on-fee.

**Snapshot the BPS on the payment row** (`fee_bps`, `gst_bps`) so a future rate change does not rewrite old events.

### 6.3 When to compute

| Event | Action |
| --- | --- |
| RSVP paid, order created | Store `amount_due_paise` on attendee. **Do not** insert `event_payments` yet. |
| `payment.captured` webhook | Insert `event_payments` status=`captured` with fee/gst/host. Set attendee `confirmed`, `payment_id`, `amount_captured_paise`. |
| `payment.failed` / order abandoned | Cancel reservation. No payment row (or status=`failed` if you already created one — v1: no row until capture). |
| Attendee cancel after capture | Razorpay refund **gross**. Payment row `status=refunded`. Host payable for that row becomes 0. Settlement totals exclude refunded. |
| Event cancel | Same as today `refundAll`, plus mark those payment rows refunded. |
| Admin settle | Insert/update `event_host_settlements`; does not call Razorpay. |

### 6.4 Tables (migration `000018_event_payments.up.sql`)

```sql
-- Immutable-enough capture ledger. One row per captured Razorpay payment.
CREATE TABLE event_payments (
    id                      BIGSERIAL PRIMARY KEY,
    event_id                BIGINT NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    attendee_id             BIGINT NOT NULL REFERENCES event_attendees(id) ON DELETE RESTRICT,
    organizer_user_id       BIGINT NOT NULL REFERENCES user_account(id) ON DELETE RESTRICT,
    currency                VARCHAR(3) NOT NULL DEFAULT 'INR',
    gross_paise             BIGINT NOT NULL CHECK (gross_paise >= 0),
    fee_bps                 INTEGER NOT NULL,
    gst_bps                 INTEGER NOT NULL,
    platform_fee_paise      BIGINT NOT NULL CHECK (platform_fee_paise >= 0),
    gst_paise               BIGINT NOT NULL CHECK (gst_paise >= 0),
    host_payable_paise      BIGINT NOT NULL CHECK (host_payable_paise >= 0),
    razorpay_order_id       VARCHAR(255) NOT NULL,
    razorpay_payment_id     VARCHAR(255) NOT NULL,
    status                  VARCHAR(32) NOT NULL, -- captured | refunded | refund_pending
    refund_paise            BIGINT NOT NULL DEFAULT 0,
    razorpay_refund_id      VARCHAR(255),
    captured_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    refunded_at             TIMESTAMPTZ,
    UNIQUE (razorpay_payment_id),
    UNIQUE (razorpay_order_id),
    CONSTRAINT chk_event_payments_currency CHECK (currency = 'INR'),
    CONSTRAINT chk_event_payments_status CHECK (status IN ('captured', 'refund_pending', 'refunded')),
    CONSTRAINT chk_event_payments_split CHECK (
        platform_fee_paise + gst_paise + host_payable_paise = gross_paise
        OR status <> 'captured'
    )
);

-- After a full refund, host_payable_paise stays as historical capture split;
-- admin totals use: SUM(host_payable) FILTER (WHERE status = 'captured').

CREATE INDEX idx_event_payments_event ON event_payments(event_id, status);
CREATE INDEX idx_event_payments_organizer ON event_payments(organizer_user_id, status);

CREATE TABLE event_host_settlements (
    id                  BIGSERIAL PRIMARY KEY,
    event_id            BIGINT NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    organizer_user_id   BIGINT NOT NULL REFERENCES user_account(id) ON DELETE RESTRICT,
    currency            VARCHAR(3) NOT NULL DEFAULT 'INR',
    payable_paise       BIGINT NOT NULL, -- snapshot of captured host_payable at settle time
    status              VARCHAR(32) NOT NULL DEFAULT 'pending', -- pending | paid | cancelled
    note                TEXT,
    marked_paid_at      TIMESTAMPTZ,
    marked_paid_by      TEXT, -- admin key id / "admin"
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_settlements_status CHECK (status IN ('pending', 'paid', 'cancelled')),
    CONSTRAINT chk_settlements_currency CHECK (currency = 'INR')
);

CREATE UNIQUE INDEX idx_event_host_settlements_open
    ON event_host_settlements(event_id)
    WHERE status IN ('pending', 'paid');
```

The unique partial index: one open/paid settlement per event in v1 (settle the whole event once). If you must settle twice after more sales, add a new row only when previous is `paid` and new captures arrived — v1 **blocks settle** if unpaid captured remainder is 0; allows a **new** settlement if more captures happened after a paid settlement. Simpler v1 rule:

- **One settlement row per event**, status pending→paid.
- If more tickets sell after `paid`, admin can create another settlement for the **delta** (status pending). Unique index then cannot be on `event_id` alone.

**v1 settlement rule (explicit):** multiple settlement rows allowed. Unique is `(id)` only. Application checks: `SUM(settlements.payable_paise WHERE status IN ('pending','paid'))` must not exceed `SUM(payments.host_payable_paise WHERE status='captured')`.

Also additive on `event_attendees` (nullable so old rows work):

```sql
ALTER TABLE event_attendees
  ADD COLUMN IF NOT EXISTS amount_due_paise BIGINT,
  ADD COLUMN IF NOT EXISTS amount_captured_paise BIGINT NOT NULL DEFAULT 0;
```

Keep `amount_paid` populated as rupees for old readers (`amount_captured_paise / 100.0`) until proto consumers move. Do not remove the column in this project.

### 6.5 INR enforcement

On `insertTier` / update tier: if `price > 0` and currency not empty and not `INR` → InvalidArgument `"paid tickets must use INR"`. If empty, set INR.

Razorpay order: always `"INR"`.

### 6.6 Secrets

`.env` / `.env.example` already have the three keys. Production: fill them. Webhook URL in Razorpay dashboard:

`https://<public-gateway>/api/v1/events/payment/webhook`

Local: Razorpay cannot reach localhost; use their CLI/`razorpay-cli` forward or a tunnel. Document this in the plan’s test section.

Never log `KEYS_RAZORPAY_SECRET` or webhook secret.

---

## 7. Performance design (make APIs fast)

Auth must not add round trips. Queries must not do N+1 work. Production already has many users/blogs/events/groups — every list change is **additive SQL** (new joins/indexes), never a rewrite of old rows.

### 7.0 Authn/authz without extra queries

Today: gateway → authz Validate → 4-table SQL **plus** (for events) Authorize gRPC/SQL **plus** GetEvent.

v1 changes, backward compatible:

1. **Same `CheckIfEmailExist` query**, add `LEFT JOIN user_role ur ON ur.id = ua.role_id` and return `role_desc` on `ValidateResponse.role` (new proto field). Zero extra round trips vs today. `AuthRequired` sets `user_role` on gin context.
2. **Staff `RequireRole`** only compares that string. No second user lookup.
3. **New JWTs** include `role` (additive claim). Old tokens without `role` still validate; role comes from step 1 until they refresh.
4. **Do not** call `CheckAccessLevel` (blog ACL) on staff routes. That RPC is for blog co-authors, not platform role.
5. **Public GET event/group:** drop `guard.RequireVisible()` extra Authorize RPC. `GetEvent`/`GetGroup` already 404 drafts for strangers. Keep Authorize on **writes** only.
6. **`redactForViewer`:** do not call `Authorize`. Use organizer id + RSVP status already loaded.
7. Pass the **request context** into Validate (not `context.Background()`).
8. Later (only if Validate SQL still shows in p95): skip DB when JWT has `role` + `account_id` and Redis says the account is not revoked. Not in the first speed PR if step 5–6 already cut event latency in half.

`permissions_granted` stays for **blogs**. Do not reuse it for platform staff. Support currently has the same blog permission bundle as Admin in that table — **do not change those rows** (backward compatible). Staff HTTP uses `role_desc` only.

### 7.1 SQL (events list)

1. **Replace correlated attendee COUNT** with a LEFT JOIN aggregate:

```sql
LEFT JOIN (
  SELECT event_id, COUNT(*)::int AS attendee_count
  FROM event_attendees
  WHERE status = 'confirmed'
  GROUP BY event_id
) ac ON ac.event_id = e.id
```

Use `COALESCE(ac.attendee_count, 0)`. `idx_event_attendees_confirmed` already exists.

2. **Replace series collapse subquery** with `DISTINCT ON` (discovery/profile upcoming only):

```sql
SELECT DISTINCT ON (COALESCE(e.series_id, e.id)) …
ORDER BY COALESCE(e.series_id, e.id), e.start_time ASC
```

Then apply LIMIT. Count must count collapsed groups (wrap in subquery or use `COUNT(*) OVER()` / distinct count). Measure both; pick the one EXPLAIN likes.

3. **Bounding box before Haversine** (events and groups):

For radius R km, approx degrees: `dlat = R/111.0`, `dlng = R/(111.0*cos(radians(lat)))`.  
`e.latitude BETWEEN lat-dlat AND lat+dlat AND e.longitude BETWEEN lng-dlng AND lng+dlng`  
then keep the existing acos filter. Optional later: `CREATE INDEX … ON events USING gist (ll_to_earth(latitude, longitude))` — not required if the box + btree `idx_events_lat_lng` is enough.

4. **Single query for page + total** where possible (`COUNT(*) OVER() AS total`) to drop the extra COUNT round trip. If DISTINCT ON makes this ugly, keep two queries but with the cheaper WHERE.

5. **Nearest ORDER BY** must use bound parameters, not `fmt.Sprintf` of floats (injection-safe and plan-cache friendly).

### 7.2 GetEvent / Authorize

- Gateway already caches Authorize 15s.
- In `GetEvent` / `redactForViewer`: **do not call `Authorize` again**. Use `event.organizer` + a cheap `is_co_host` / meeting-link rule:
  - If `account_id` empty: redact meeting link; hide drafts (NotFound).
  - If viewer is organizer_id or has `event_co_hosts` row: host.
  - Else if `viewerStatus == confirmed`: keep meeting link.
- Optionally pass a header from gateway grant — skip; keep logic in the service with one SQL, not full Authorize.

### 7.3 Geocode

- Pass `context.Context` (honor cancel).
- Cache Nominatim results (Redis if gateway/events already have Redis config; otherwise in-process LRU + Postgres `venues` reuse).
- Do **not** fail create if geocode fails (already returns 0,0 → NULL).
- Do **not** call Nominatim when lat/lng already provided.

### 7.4 How to run APIs fast **while developing** (this is a large part of perceived slowness)

Do **not** `docker compose up --build -d` for every Go edit.

**Fast loop:**

1. Compose **infra only**: `the_monkeys_db`, `db-migrations`, `the_monkeys_cache`, `rabbitmq`, `elasticsearch-node1`, `minio`.
2. Run binaries on the host:

```
go run ./microservices/the_monkeys_events
go run ./microservices/the_monkeys_groups
go run ./microservices/the_monkeys_gateway
```

Point `.env` microservice hosts at `localhost` and ports from `.env`.  
3. Optional: `air` / `gow` for reload.  
4. For one service in Docker: `docker compose up --build -d the_monkeys_events` — **not** the whole file.

**Prove SQL slowness:**

```
docker exec -it the-monkeys-psql psql -U <user> -d <db>
EXPLAIN (ANALYZE, BUFFERS)
SELECT … copy the list query …
```

Target for `GET /api/v1/events?date=upcoming` with 1k events: **p95 < 100ms** inside the events service (excluding Nominatim, excluding cold Docker). Gateway total p95 **< 200ms** on local.

Do **not** add Redis list caching in this project.

---

## 8. Admin API design

Base: `http://localhost:<THE_MONKEYS_GATEWAY_HTTP_PORT>/api/v1/admin`  
Auth: same `mat` cookie / `Authorization: Bearer` as the public site.  
`RequireRole` after `AuthRequired`. No LAN check. No `X-Admin-Key` on these routes.

All list endpoints: `limit` default 20 max 100, `offset`, `q` search where it makes sense.  
Money fields: `{ "paise": number, "inr": "941.00" }` pairs.

### 8.0 Role matrix (enforce in gateway; services re-check `role_desc` on money mutations only)

| Route group | Admin | Support | Community | Viewer |
| --- | --- | --- | --- | --- |
| `/payments/*` | yes | no | no | no |
| `/stats` (includes payment totals) | yes | no (or stats **without** payment block — Support gets user/blog/event/group counts only) | no | no |
| `/users` list, flag, suspend, delete, **set role** | yes | no | no | no |
| `/verifications` list + review (checkmark) | yes | yes | no | no |
| `/blogs`, `/events`, `/groups` list | yes | no | no | no |
| NSFW on blog/event/group/image | yes | no | yes | no |
| Event Q&A / comment hide (platform) | yes | no | yes | no |
| Event cancel / group suspend | yes | no | no | no |

`POST /api/v1/admin/users/:id/role` body `{ "role": "Support"|"Community"|"Admin"|"Viewer" }` — **Admin only**. Never expose this to Support/Community. Cannot demote the last Admin (application check).

There is **no contest table** in schema today. “Contest images” maps to **event photos + blog images** NSFW flags until a contest feature exists.

Audit `actor` is `username` + `account_id` + `role`, never the string `"admin"`.

### 8.1 Platform overview

`GET /api/v1/admin/stats`

```json
{
  "users": { "total": 0, "new_7d": 0 },
  "blogs": { "draft": 0, "published": 0, "scheduled": 0, "archived": 0, "orphan_es": 0 },
  "events": { "draft": 0, "published": 0, "live": 0, "completed": 0, "cancelled": 0 },
  "groups": { "draft": 0, "published": 0, "archived": 0, "suspended": 0 },
  "payments_inr": {
    "captured_gross_paise": 0,
    "platform_fee_paise": 0,
    "gst_paise": 0,
    "host_payable_open_paise": 0,
    "refunded_gross_paise": 0
  }
}
```

Fan-out count RPCs in parallel (errgroup). Payment block only if caller is Admin; Support `/stats` omits `payments_inr`.

### 8.2 Users

- `GET /api/v1/admin/users?q=&limit=&offset=` — username, email, account_id, created_at, status, verified, blog/event/group counts (cheap subqueries or 0 in v1 if too heavy; include at least username/email/status).
- Replace stub flag: persist `user_admin_flags (user_id, flag_type, reason, created_at, created_by)`.
- Keep DELETE as today.
- `POST /api/v1/admin/users/:id/suspend` — set `user_status` to a suspended status if one exists; else add status row in a new migration. **Do not guess.** Inspect `user_status` seed data and map. If no suspend status, add `'suspended'` in `000018` or `000019`.

### 8.3 Blogs

`blog.status` values: `Draft`, `Published`, `Scheduled`, `Archived` (`constants/database.go`). Deleted is a separate constant.

- `GET /api/v1/admin/blogs?status=&q=&limit=&offset=`
- `GET /api/v1/admin/blogs/orphans` — ES ids not in `blog.blog_id` (existing comment in admin routes).
- Actions: unpublish → Draft; delete via existing blog delete RPC if present; `POST /api/v1/admin/blogs/:blog_id/nsfw` — `blog_admin_flags` table.

### 8.4 Events

- `GET /api/v1/admin/events?status=&q=&group_slug=&limit=&offset=` including draft.
- Actions: `POST …/events/:slug/cancel` (reuse CancelEvent), `POST …/unpublish` (set draft if no confirmed paid attendees; else 409), `POST …/nsfw`.

### 8.5 Groups

- `GET /api/v1/admin/groups?status=&q=&limit=&offset=`
- `POST …/groups/:slug/suspend` — `groups.status = suspended`
- `POST …/groups/:slug/nsfw`

### 8.6 Payments dashboard (the money admin)

- `GET /api/v1/admin/payments/events?status=&q=&limit=&offset=`  
  Per event: slug, title, host username, start_time,  
  `gross_captured_paise`, `fee_paise`, `gst_paise`, `host_payable_captured_paise`, `refunded_paise`, `already_settled_paise`, `open_payable_paise`.

- `GET /api/v1/admin/payments/events/:slug`  
  Event header + list of `event_payments` (attendee username, payment ids, split, status) + list of settlements.

- `POST /api/v1/admin/payments/events/:slug/settlements`  
  Body: `{ "note": "upi ref 123" }`  
  Creates settlement for **current open payable** (captured host_payable minus already pending/paid settlements). If open is 0 → 409.

- `POST /api/v1/admin/payments/settlements/:id/mark-paid`  
  Body: `{ "note": "paid 12 Sep" }`  
  pending → paid. Does not call Razorpay.

No host-facing payout API in v1.

### 8.7 Audit

`admin_audit_log (id, at, actor_ip, action, entity_type, entity_id, payload jsonb)`.  
Write on every mutating admin call. Required for delete/suspend/settle.

---

## 9. Proto / gateway mapping

Additive protobuf only. Never reuse field numbers.

EventService new RPCs (names exact):

- `AdminListEventPayments(AdminListEventPaymentsReq) returns (AdminListEventPaymentsResp)`
- `AdminGetEventPayments(AdminGetEventPaymentsReq) returns (AdminGetEventPaymentsResp)`
- `AdminCreateSettlement(AdminCreateSettlementReq) returns (AdminSettlementResp)`
- `AdminMarkSettlementPaid(AdminMarkSettlementPaidReq) returns (AdminSettlementResp)`
- `AdminListEvents(AdminListEventsReq) returns (ListEventsResp)` — includes drafts; admin-only, not public ListEvents
- `AdminSetEventStatus` or reuse `CancelEvent` + new `AdminUnpublishEvent`

UserService / BlogService / GroupService: matching `AdminList*` / `AdminStats*` / flag RPCs. If a service proto change is large, gateway may call existing list RPCs with an `admin=true` field — **prefer dedicated Admin RPCs** so public list cannot be widened by accident.

Gateway admin currently only dials UserService. Extend `AdminServiceClient` to hold events/blog/group clients (same pattern as `NewAdminServiceClient`).

---

## 10. Testing requirements

Must add tests before claiming done:

- `money` package: ₹1000 → 5000/900/94100; ₹1.00; ₹499; odd paise 333 (document expected half-up).
- Webhook confirm writes payment row; second webhook no duplicate (unique payment id).
- Refund zeros that row’s contribution to open payable.
- ListEvents EXPLAIN or benchmark comment + test that hydrate still works.
- Admin settlement cannot exceed open payable.
- Paid tier non-INR rejected.
- Existing free RSVP tests still pass.

Manual: `docker compose` infra + `go test` packages listed in the plan.

---

## 11. Backward compatibility (production already has data)

Non-negotiable:

- Do **not** rewrite migrations `000001`–`000017`.
- Do **not** change signup `role_id = 4` (Viewer). Existing users stay on their current `role_id`.
- Add `Community` with `INSERT INTO user_role (role_desc) VALUES ('Community') ON CONFLICT DO NOTHING`. New id; no UPDATE of old roles.
- Public REST `/api/v1/events` and `/api/v1/groups` paths, JSON shapes, and proto field numbers stay valid. Additive fields only.
- `event_attendees.amount_paid` (rupees DECIMAL) stays; new paise columns are extra. Old confirmed tickets keep working; fee rows are created **going forward** on capture. Optional one-time backfill SQL for old captured payments using current 5%+GST formula — run only if product wants history; default **no backfill** (open payable starts at 0 for old tickets).
- Old JWTs without `role` still work.
- Do not drop `KEYS_ADMIN_SECRET_KEY`. Stop using it on staff HTTP; backup endpoint can keep it until Task 8 migrates that route to Admin JWT.
- Elasticsearch blog documents and Postgres `blog` rows are not rebuilt. Orphan API is read-only compare.
- Event/group Authorize RPC remains for **writes** so old clients are unaffected.

## 12. Out of scope (do not build)

- Admin UI / dashboard frontend
- Razorpay Route, host KYC, automatic bank payouts
- Paid “RSVP all dates” as one order
- Group dues / organizer subscriptions (orphan tables)
- Redis caching of discovery lists
- Changing public event/group REST paths
- Rewriting applied migrations `000001`–`000017`

---

## 13. Build order

1. Auth: role on Validate (same query) + drop extra Authorize on public GET + local run notes. **This is the speed slice.**
2. List SQL (counts, collapse, geo box) — still no payment tables.
3. `money` + ledger migration + webhook/refund.
4. Staff JWT routes: payments (Admin), verify (Support), NSFW/Q&A (Community).
5. Catalog lists/stats/suspend for Admin.

Do not ship payment admin until ledger tests pass. Do not put payments behind the old shared key.

---

## 14. Assumptions (challenge these before coding)

1. GST Option A as in §1.
2. Hosts are paid **outside** the app in v1; admin only records it.
3. Platform absorbs Razorpay MDR.
4. Auto-capture is ON in the Razorpay account.
5. Staff auth is JWT `user_role.role_desc` (`Admin` / `Support` / `Community`). Signup stays Viewer.
6. INR only for paid events.
7. No backfill of GST on historical tickets unless product asks.
8. “Contest images” = event/blog photos until a contest schema exists.
)
