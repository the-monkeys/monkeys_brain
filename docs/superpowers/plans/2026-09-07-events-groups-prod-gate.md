# Events/groups geo + public/staff APIs — production gate plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (or subagent-driven-development) **only after** the user says to run this plan. Skip every git commit until the user asks. REQUIRED: superpowers:verification-before-completion on every case.
>
> **Supersedes:** `docs/superpowers/plans/2026-09-07-geo-frontend-release-test.md` (geo-only, no authn/authz/perf/staff). Keep that file as a geo subset; this file is the prod gate.

**Goal:** Prove the public site and gateway are safe to ship for events/groups geo+CRUD **and** that this branch cannot be called production-ready without authn, authz, database row checks after every write, a full public API pass, a staff role pass, and a performance pass.

**Architecture:** One evidence protocol for every case: (1) named actor + cookie, (2) HTTP request/response, (3) SQL assertion on the row(s) that must have changed or must **not** have changed, (4) optional UI check. A case with only a screenshot or only JSON is a **fail**. Authz cases must also prove the **wrong** actor gets 401/403/404. Performance is a separate phase after functional green; do not tune before correctness.

**Tech Stack:** Gateway `http://localhost:8081`, app `http://localhost:3000`, cookie `mat` or `Authorization: Bearer`, Postgres 17 (`the-monkeys-psql`), docker compose. Contract: `docs/frontend-events-groups.md`. Staff: `docs/admin-api.md`. Geo spec: `docs/superpowers/specs/2026-09-06-geo-radius-clamp-design.md`.

## Global Constraints

- Signup stays Viewer (`role_id = 4`). Do not rewrite migrations `000001`–`000019`.
- Engine does **not** default radius 25 km. UI default 25 is a client send. `radius` 0 / omit / negative = nationwide.
- Virtual events store NULL coords even if the client sends a pin. `(0,0)` is not a pin.
- Event GET JSON has **no** `latitude`/`longitude`. Groups GET **does**.
- Existing NULL-coord rows are not backfilled. Radius SQL requires `latitude IS NOT NULL AND longitude IS NOT NULL`.
- Public app must not call `/api/v1/admin/*`. Staff Dashboard is a different app; those routes still block **engine** prod if untested.
- Errors to clients: `{ "error": "<message>" }` or documented `{ "message" }`. Never gRPC codes in UI.
- Do not git commit unless the user asks.
- **Prod rule:** if any of Authn, Authz, DB-after-write, full public API inventory, or performance is skipped, the verdict is **NO-GO**.

### Evidence protocol (every case)

Log one row in `docs/superpowers/sdd/prod-gate-log.md` (create the file when executing):

| Field | Required |
| --- | --- |
| `id` | Case id from this plan |
| `actor` | `anon` / `host` / `other` / `cohost` / `member` / `admin` / `support` / `community` |
| `http` | Method, path, status, truncated body (`error` / `slug` / `total`) |
| `sql` | Query + result (lat/lng, status, counts, or “no row”) |
| `pass` | yes / no |

SQL templates (run in `the_monkeys_user_dev`):

```sql
-- Event coords and lifecycle
SELECT id, slug, status, event_type, visibility, latitude, longitude, group_id, series_id
FROM events WHERE slug = :slug;

-- Group coords and lifecycle
SELECT id, slug, status, visibility, city, latitude, longitude FROM groups WHERE slug = :slug;

-- Radius membership (Bengaluru pin, clamp already applied in app)
-- In-person with NULL lat/lng must be absent from this set.
SELECT slug, event_type, latitude, longitude FROM events
WHERE status = 'published' AND end_time >= NOW();

SELECT slug, status FROM event_payments WHERE event_id = :id;
SELECT COUNT(*) FROM event_attendees WHERE event_id = :id;
SELECT COUNT(*) FROM group_members WHERE group_id = :id AND status = 'active';
```

HTTP helper:

```bash
GW=http://localhost:8081
# Host cookie from login; never commit cookies or passwords.
curl -sS -D - -o /tmp/body.json -b "mat=$HOST_JWT" -H "Content-Type: application/json" \
  -X POST "$GW/api/v1/events" -d @body.json
```

### Actors (create before Task 1)

| Actor | Role | Purpose |
| --- | --- | --- |
| `anon` | none | Public reads; all writes 401 |
| `host` | Viewer | Owns events/groups under test |
| `other` | Viewer | Stranger: 403/404 on drafts and host tools |
| `cohost` | Viewer + co-host | `edit_event` where granted |
| `member` | Viewer + group member | `group_members` visibility |
| `admin` | Admin | Staff JWT routes |
| `support` | Support | Verifications yes; payments 403 |
| `community` | Community | NSFW yes; payments 403 |

Confirm in DB: `user_account` + `user_role.role_desc`. Signup of a new user must still be Viewer.

---

### Task 0: Lab, log, fixtures

**Files:**
- Create when executing: `docs/superpowers/sdd/prod-gate-log.md`
- Read: `docs/frontend-events-groups.md`, `docs/admin-api.md`

**Interfaces:**
- Produces: actor JWTs (env only), fixture slugs `F1`–`F8` as in the geo subset plan

- [ ] **Step 1:** Confirm compose: gateway 8081, Postgres 5432, events 50059. Migrations `000018` and `000019` applied (`schema_migrations`).
- [ ] **Step 2:** Login each actor; store JWTs in the shell only. SQL-check `role_desc`.
- [ ] **Step 3:** Create geo fixtures (must exist before list tests):

| ID | Create via | Pin | Expect SQL |
| --- | --- | --- | --- |
| F1 | `POST /events` in_person + lat/lng 12.97,77.59 then publish | yes | both columns non-zero |
| F2 | in_person + NYC pin then publish | yes | NYC coords |
| F3 | in_person, location string, **omit** lat/lng then publish | no | NULL,NULL (Nominatim miss is OK) |
| F4 | virtual then publish | n/a | NULL,NULL even if pin sent |
| F5 | hybrid then publish | optional | listed under radius regardless |
| F6 | `POST /groups` + pin 12.97,77.59 then publish | yes | GET returns lat/lng |
| F7 | group city Ahmedabad, no pin then publish | no | NULL or geocoded; not Bengaluru 25 km unless geocoded there |
| F8 | `POST /events/series` weekly in_person + pin | yes | all occurrence rows share pin |

- [ ] **Step 4:** Do **not** claim geo UI works until F1 and F6 SQL pins are non-NULL.

---

### Task 1: Authentication (authn)

**Files:** Gateway `internal/auth/middleware.go` (AuthRequired / AuthOptional). No code changes in this task.

**Interfaces:**
- Consumes: actor JWTs
- Produces: pass/fail for cookie, bearer, missing, garbage, wrong cookie name

- [ ] **A1** No cookie, `POST /api/v1/events` → **401**. SQL: no new `events` row.
- [ ] **A2** `Authorization: Bearer <host jwt>` create event → **201**. SQL: row exists, `organizer_account_id` = host.
- [ ] **A3** Cookie `mat=<host jwt>` same as A2.
- [ ] **A4** Garbage token on write → **401**. SQL: no row.
- [ ] **A5** Empty `mat=` → **401**.
- [ ] **A6** Public `GET /api/v1/events` with no cookie → **200**.
- [ ] **A7** AuthOptional `GET /api/v1/events/:slug` draft: `anon` **404**; `host` **200** (hydrates viewer fields). SQL: row still `draft`.
- [ ] **A8** `GET /api/v1/events/attending` without cookie → **401**.
- [ ] **A9** Staff `GET /api/v1/admin/stats` without cookie → **401** (not 200 empty).
- [ ] **A10** Viewer cookie on `GET /api/v1/admin/stats` → **403**. SQL: no `admin_audit_log` insert.

---

### Task 2: Authorization (authz)

Wrong actor must not mutate. SQL must show **unchanged** row.

| ID | Actor | Call | HTTP | SQL |
| --- | --- | --- | --- | --- |
| Z1 | `other` | `PUT /events/:hostSlug` | 403 | lat/lng/title unchanged |
| Z2 | `other` | `DELETE /events/:hostSlug` | 403 | row remains |
| Z3 | `other` | `POST /events/:slug/publish` | 403 | status stays draft |
| Z4 | `other` | `GET` host **draft** event | 404 | row remains draft |
| Z5 | `other` | `GET` host **private** group | 404 | row remains |
| Z6 | `anon` | `GET` published public event | 200 | — |
| Z7 | `member` | event `visibility=group_members` | 200 | — |
| Z8 | `other` | same event | 404 | — |
| Z9 | `cohost` | `PUT` event they co-host | 200 | title changes |
| Z10 | `other` | `POST /groups/:slug/join` public | 200 | `group_members` active |
| Z11 | `other` | `DELETE /groups/:slug` (not owner) | 403 | group row remains |
| Z12 | `host` | `PUT` member role to organizer | 400/403 | owner unchanged |
| Z13 | `support` | `GET /admin/payments/events` | 403 | `event_payments` unchanged |
| Z14 | `community` | `POST /admin/events/:slug/nsfw` | 200 | flag row; `admin_audit_log` |
| Z15 | `community` | `DELETE /admin/events/:slug` | 403 | event row remains |
| Z16 | `admin` | `DELETE /admin/events/:unpaidSlug` | 200 | event row gone |
| Z17 | `viewer` | `POST /admin/users/:id/role` | 403 | `user_role` unchanged |

- [ ] Run Z1–Z17. Log HTTP + SQL for each.

---

### Task 3: Events CRUD + database

Every write: HTTP then SQL on `events` (and children).

- [ ] **E-C1** `POST /events` in_person + pin → 201 draft. SQL: `status=draft`, lat/lng = body, not Nominatim.
- [ ] **E-C2** `POST /events` in_person omit pin → 201. SQL: lat/lng NULL or geocoded; **not** 400.
- [ ] **E-C3** `POST /events` virtual + pin in body → 201. SQL: lat/lng **NULL**.
- [ ] **E-C4** `POST /events/series` missing `recurrence` → 400. SQL: no new series_id.
- [ ] **E-C5** `POST /events/series` weekly + pin → 201. SQL: occurrence rows share lat/lng; `series_id` set.
- [ ] **E-U1** `PUT` add pin to F3 → 200. SQL: F3 lat/lng now non-NULL. Then R2 list includes F3.
- [ ] **E-U2** `PUT` type to virtual → 200. SQL: lat/lng NULL.
- [ ] **E-U3** `POST /:slug/clone` of F1 → 201 draft. SQL: clone lat/lng **equal** F1; new slug.
- [ ] **E-U4** `POST /:slug/publish` → 200. SQL: `status=published`.
- [ ] **E-U5** `POST /:slug/cancel` unpaid → 200. SQL: `status=cancelled` (or documented).
- [ ] **E-D1** `DELETE` unpaid published event → 200. SQL: no `events` row; MinIO prefix gone (or storage log).
- [ ] **E-D2** `DELETE` with `event_payments` captured/pending → **409**. SQL: event row **still there**; payment rows **untouched**.
- [ ] **E-GET** `GET /events/:slug` published: no lat/lng keys. SQL still has coords if pinned.

UI after each host write: corresponding page shows the same title/status; toast is `{ error }` on failure.

---

### Task 4: Groups CRUD + database

- [ ] **G-C1** `POST /groups` + pin → 201 draft. SQL: lat/lng match. `GET` JSON includes lat/lng.
- [ ] **G-C2** `POST /groups` city only → 201. SQL: pin omitted path.
- [ ] **G-U1** `PUT` pin change → 200. SQL + GET match new pin.
- [ ] **G-U2** `POST /:slug/publish` → 200. SQL: `status=published`.
- [ ] **G-D1** `DELETE` group with no paid child → 200. SQL: group gone; members unlinked.
- [ ] **G-D2** `DELETE` while child has captured payment → **409**. SQL: group remains; `event_payments` unchanged.
- [ ] **G-E1** `POST /:slug/events` as organizer + pin → 201. SQL: `events.group_id` set; coords stored.

---

### Task 5: Geo list (must follow Task 3–4 fixtures)

Bengaluru pin `user_lat=12.9716&user_lng=77.5946`.

| ID | Query | HTTP `events` slugs | SQL check |
| --- | --- | --- | --- |
| R1 | no pin, no radius | includes F2, F3, F4 | all published upcoming |
| R2 | pin + `radius=25` | F1 yes; F2 no; F3 no; F4 yes; F5 yes | F3 NULL so excluded from in-person geo |
| R3 | pin + `radius=100` | F2 still no | distance(F2) > 100 |
| R4 | pin + `radius=0` | same as R1 | geo SQL not applied |
| R5 | pin + `radius=1` | geo still on (clamped 2) | not nationwide |
| R6 | pin + `radius=250` | same membership as 100 | clamp max |
| R7 | `sort=nearest` + pin | F1 before far pinned | — |
| R8 | `sort=nearest` no pin | soonest order | — |
| R9 | groups pin+25 | F6 yes F7 no (unless F7 geocoded in range) | groups NULL out |
| R10 | groups no radius | public published | — |
| R11 | `GET /events/user/:host` | includes F3 | hosting ignores radius |
| R12 | UI chips 10/25/50/100/Everywhere | Network tab matches R2–R4 | — |

- [ ] Run R1–R12. Compare JSON `total`/`slug` to SQL, not to the old empty-city screenshot.

---

### Task 6: Remaining public APIs (inventory — each line is a case)

For each: correct actor **200/201**, wrong actor **401/403/404**, and SQL side effect (or “read-only, no write”).

**Events read:** `GET /events`, `/events/user/:u`, `/events/group/:slug`, `/events/attending`, `/events/:slug`, `/comments`, `/share`, `/calendar`.

**Events write already in Task 3** plus:

| Call | Authz | SQL |
| --- | --- | --- |
| `POST/PUT/DELETE /:slug/tiers` | `edit_event`; paid 409 if Razorpay off | `event_ticket_tiers` |
| `POST/GET/DELETE /:slug/coupons` | host | `event_coupons` |
| `POST /:slug/coupons/validate` | attendee | no extra row |
| `POST /:slug/rsvp` scope this/series | cookie; paid+series refused | `event_attendees`; paid → `event_payments` pending |
| `DELETE /:slug/rsvp` | cookie | attendee cancelled |
| `GET /:slug/attendees` + export | host | read |
| `PUT /:slug/attendees/:id/attendance` | host | `checked_in` |
| `POST/DELETE /:slug/save` | cookie | `saved_events` |
| `POST/DELETE comments` | cookie / author | `event_comments` |
| `POST/DELETE /:slug/react` | cookie | `event_reactions` |
| `POST /:slug/report` | cookie | reports table |
| `POST/DELETE /:slug/cohosts` | organizer | `event_co_hosts` |
| Cover/photos POST/DELETE | `edit_event`; photos **409** at 4 | storage objects; EXIF stripped on new upload |
| `GET /api/v2/storage/events/:slug/cover` | public | bytes |

**Groups remaining:** join, leave, members list/role/remove/ban/approve/reject, add member, invites CRUD, `GET/POST /group-invites/:token`, rules CRUD, logo/cover.

- [ ] Tick every route in `docs/frontend-events-groups.md` §1 and §2. Missing tick = NO-GO.

---

### Task 7: Staff APIs (engine prod, not public UI)

Do **not** call these from `apps/the_monkeys`. Still required for **this git branch** to ship.

Role matrix from `docs/admin-api.md`: payments Admin-only; stats Admin+Support (Support omits `payments_inr`); catalog Admin; verifications Admin+Support; NSFW Admin+Community.

- [ ] Each staff route: Admin expected 200; Viewer 403; Support/Community per table.
- [ ] `DELETE` paid event as Admin → 409; SQL payments unchanged.
- [ ] `POST /payments/events/:slug/settlements` ₹1000 captured → host payable **94100 paise**; SQL `event_host_settlements`.
- [ ] `POST /users/:id/role` cannot demote last Admin (409). SQL role unchanged.
- [ ] Legacy `X-Admin-Key` + LAN routes still 401 without key; **not** used by Dashboard.

---

### Task 8: Performance (only after Tasks 1–7 functional green)

**Files:** Gateway + events/groups list SQL. Measure with `curl -w '%{time_total}'` and Postgres `EXPLAIN (ANALYZE, BUFFERS)` on list queries.

**SLOs (local compose, warm cache, 20-row page):**

| Surface | p95 |
| --- | --- |
| `GET /events` nationwide | < 300 ms |
| `GET /events` pin+radius=25 | < 300 ms |
| `GET /groups` pin+25 | < 300 ms |
| `GET /events/:slug` | < 200 ms |
| `POST /events` (no image) | < 500 ms |

- [ ] **P1** Nationwide vs radius: EXPLAIN must use bbox/Haversine only when `radius>0`; nationwide must **not** apply acos.
- [ ] **P2** No N+1: attendee counts / tags batched (no per-row extra query in logs).
- [ ] **P3** Series collapse: one card per series in discovery JSON (`total` not 12 identical titles).
- [ ] **P4** 50 sequential list requests: no 5xx, p95 within SLO.
- [ ] **P5** Radius clamp 250 → 100 does not scan “country-sized” bbox (box matches 100 km).
- [ ] **P6** UI: discover first contentful cards < 2 s on localhost; radius chip change does not loop-fetch past 100 km.

Failing SLO with correct data = **NO-GO** or documented waiver. Do not “fix” by dropping DB checks.

---

### Task 9: Browser (public app only)

After API+SQL pass:

- [ ] `/events` chips; Network shows `user_lat`, `user_lng`, `radius=25` (not 0).
- [ ] Everywhere omits geo or `radius=0` and list matches R1.
- [ ] Create in-person: pin control; save; SQL pin; card in 25 km.
- [ ] Create virtual: no pin sent; SQL NULL.
- [ ] Group create pin; GET lat/lng.
- [ ] Delete confirm copy; 409 toast is server `error`.
- [ ] Mobile 375px and desktop 1280px.
- [ ] No `/api/v1/admin` in Network.

---

### Task 10: Production go / no-go

**NO-GO if any of:**

1. Authn A1–A10 incomplete.
2. Authz Z1–Z17 incomplete (wrong actor can write).
3. Any write without a matching SQL assertion.
4. Any route in `docs/frontend-events-groups.md` §1–§2 unticked.
5. Staff role matrix unticked on this branch.
6. Performance P1–P6 skipped or p95 failed without waiver.
7. F1/F6 still NULL in DB (geo UI unproven).
8. Signup `role_id` changed from 4.

**GO** only with a filled `prod-gate-log.md` and this checklist complete.

---

### Count (minimum cases)

| Phase | Cases |
| --- | --- |
| Authn | 10 |
| Authz | 17 |
| Events CRUD+DB | 13 |
| Groups CRUD+DB | 7 |
| Geo list | 12 |
| Remaining public inventory | ~40 routes × (happy + denied) |
| Staff | all `docs/admin-api.md` JWT routes × role |
| Performance | 6 |
| Browser | 8 |

That is the professional bar. UI-only or JSON-only is not enough to deploy.
