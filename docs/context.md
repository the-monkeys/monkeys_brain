# Monkeys — AI handoff context

Read this first. Then read `docs/series-ux-rsvp-payments-plan.md` for the next slice (series discovery, covers, errors, payments, RSVP window, RSVP-one-vs-all, speed).

Do **not** start coding that slice until the user says proceed. Existing `/api/v1/events` and `/api/v1/groups` contracts stay valid. Additive protobuf fields only. No new parallel tables if `event_series` already covers it.

---

## What this product is

Monkeys is a writing + community + events platform (Meetup-like events/groups, plus blogs). Hosts create public meetups (one-off or recurring). Visitors discover by city/radius, RSVP (free or Razorpay), join groups.

Design bar: Meetup-style, DRY, modular, backward compatible, no bloat, few comments. Users are not engineers — never show SQL, gRPC codes, or stack traces in toasts.

---

## Repos and where you are

| Repo | Path | Role |
| --- | --- | --- |
| **Engine (this git repo)** | `c:\Users\Dave\the_monkeys\the_monkeys_engine` | Go microservices, `schema/`, protos, docker compose |
| **Frontend** | `engine/local/the_monkeys` | pnpm/turbo monorepo. App: `apps/the_monkeys` (Next.js 14 App Router, Tailwind). **Gitignored by the engine repo** — it is its own git repo |
| Design / older plans | `docs/` | Meetup parity, recurring/past/profile, geo, admin |

Frontend is **not** in engine `git status`. Edit it on disk; commit inside `local/the_monkeys`. Netlify builds `apps/the_monkeys` from that frontend repo (`npm run build` → turbo lint + next build).

Typical branches: engine `events_upgrade` (events work) vs `main`. Frontend also used `events_upgrade`. Confirm `git branch` before committing.

---

## How to run

- Engine: from repo root, `docker compose up --build -d` after **any** backend/proto/migration change. User expects this every time.
- Gateway HTTP: `http://localhost:8081` (`.env` `THE_MONKEYS_GATEWAY_HTTP_PORT`).
- Frontend: Next on `http://localhost:3000`, proxies `/api/v1` to the gateway.
- Postgres 17, Redis, RabbitMQ, Elasticsearch, MinIO via compose. Migrations: `db-migrations` on `./schema`.
- Latest event/group geo migration: `schema/000014_add_event_group_coordinates.up.sql`. Do not rewrite applied migrations; add `000015+`.
- Protos: `apis/serviceconn/gateway_event/pb/gw_event.proto`, `gateway_group/pb/gw_group.proto`. User often runs `protoc` **manually**. After proto edits, generated `*.pb.go` / `*_grpc.pb.go` must exist before `go build`.
- `go vet ./...` is in CI. Never copy protobuf structs by value (`filtered := *req` copies a mutex).

---

## Architecture (events path)

```
Browser (Next)
  → GET/POST localhost:3000/api/v1/...
  → the_monkeys_gateway (Gin)
  → gRPC EventService / GroupService
  → Postgres
```

Auth: JWT cookies (`mat` access, `mrt` refresh) from `the_monkeys_authz`. Gateway `AuthRequired` / `AuthOptional`. Event mutations also go through `internal/events/authx` guard (`edit_event`, `manage_tickets`, …). The events service **re-checks** `event_permissions`.

Storage: event cover + gallery (max 4 photos) via gateway storage v2 → MinIO. Reads: `GET /api/v2/storage/events/:slug/photos`. Writes: `POST /api/v1/events/:slug/photos` (host).

Payments today: **platform Razorpay**, keys `KEYS_RAZORPAY_KEY_ID` / `SECRET` / `WEBHOOK_SECRET` in `.env`. `microservices/the_monkeys_events/internal/services/payments.go`. If keys are empty, `pay.enabled()` is false and **paid ticket create / paid RSVP** returns FailedPrecondition `"payments are not configured on this deployment"`.

---

## Microservices that matter for events

| Service | Path | Notes |
| --- | --- | --- |
| Gateway | `microservices/the_monkeys_gateway` | REST. Events: `internal/events/{routes,handler,models}.go` |
| Events | `microservices/the_monkeys_events` | DB `internal/database/{events,attendees,recurring,tickets_coupons}.go`; svc `internal/services/{service,rrule,scheduler,payments}.go` |
| Groups | `microservices/the_monkeys_groups` | `GetUserGroups` + `public_only` |
| Authz, users, blog, storage, notification, activity, AI | other folders | only touch if the task needs them |

Gateway error helper: `EventServiceClient.fail` maps gRPC → HTTP JSON `{ "error": "<st.Message()>" }`. **Whatever you put in `status.Error` is what the toast shows.** Do not wrap `fmt.Errorf("%v", err)` from Postgres.

Frontend toast helper: `eventError()` in `apps/the_monkeys/src/services/events/eventsApi.ts`.

---

## Frontend map (app: `local/the_monkeys/apps/the_monkeys/src`)

| Area | Files |
| --- | --- |
| Discovery | `components/events/discover/EventsDiscover.tsx`, `lib/geoSearch.ts`, `hooks/useIPLocation.ts` |
| Cards | `EventGridCard.tsx`, `groups/GroupGridCard.tsx` |
| Create/edit | `components/events/EventForm.tsx`, `app/events/new/page.tsx`, `app/events/[slug]/edit/page.tsx` |
| Detail | `app/events/[slug]/EventDetailClient.tsx`, `RsvpPanel.tsx`, `EventActions.tsx`, `detail/EventGallery.tsx`, `EventStickyBar.tsx`, `EventSeriesNote.tsx` |
| Host tools | `EventManage.tsx` (tickets, coupons, co-hosts, attendees) |
| Groups | `components/groups/detail/GroupCommunity.tsx` (Events \| About \| Members + staff tabs) |
| Profile | `app/[username]/page.tsx` → `ProfileActivity.tsx` (Posts \| Events \| Groups) |
| API types | `services/events/{eventsApi,eventTypes}.ts`, `services/groups/{groupsApi,groupsTypes}.ts` |
| Time helpers | `lib/eventTime.ts` (`isEventEnded`, formatters) |
| Tabs | `components/TextTabs.tsx` |

Stack: React Query, axios instances (`axiosInstance` auth, `axiosInstanceNoAuth`, v2 for storage). Lint: Next ESLint + Prettier (`prettier/prettier`). Format before push.

---

## Data model (events) — already in Postgres

**`events`** (`schema/000010` + `000011` + `000014`): slug, times, timezone, type (`virtual` \| `in_person` \| `hybrid`), location, lat/lng, meeting_link, capacity, status (`draft` \| `published` \| `live` \| `completed` \| `cancelled`), cover_image, visibility, group_id, **series_id**, **series_occurrence_at**, **rsvp_opens_at / rsvp_closes_at** (columns exist; **create/update/RSVP code does not use them yet**).

**`event_series`**: organizer, optional group, title, `recurrence_rule` (RRULE string), starts/ends, status `active|paused|completed|cancelled`.

**`event_ticket_tiers`**, **`event_coupons`** (unique `(event_id, code)`), **`event_attendees`** (RSVP per **occurrence**), tags, comments, reactions, photos (storage, not a SQL gallery table).

Each recurring date is a **normal event row**. RSVP, tickets, coupons, comments, photos are per occurrence unless we add series-scope APIs.

---

## What already shipped (do not rebuild)

### Geo discovery

- Events and groups have lat/lng. Events geocode `location` on create/update (`Geocode` in events DB). Failed geocode → NULL coords.
- `ListEvents` Haversine radius. In-person: city → country, never worldwide. Virtual/hybrid: global (radius does not hide them).
- UI: `geoSearch.ts` radius steps; `EventsDiscover` no longer expands to radius `0`.

### Past / completed

- `ListEvents`: empty `date` ⇒ `upcoming` (`end_time >= NOW()`, status published+live). `date=past` uses public statuses including completed.
- RSVP refused if ended even when status still `published` (`eventHasEnded`).
- Ended events: host may edit title, description, cover, tags only. Time/place/capacity/visibility frozen.
- Clone: `POST /api/v1/events/:slug/clone` → new **draft**. UI: Schedule again.
- Gallery heading **Glimpses** when ended. Cap 4.

### Recurring (create path lives; discovery still wrong)

- `POST /api/v1/events/series` + gRPC `CreateSeries`. EventForm Repeat → `createSeries()` instead of `createEvent()`.
- `rrule.go`: daily/weekly/monthly/yearly, horizon 12, cap 52. Stores RRULE; expands timestamps in service; `MaterializeSeries` inserts **published** occurrences immediately (not drafts).
- `CloneEvent` / `CancelSeriesOccurrence` exist. GetSeries / UpdateSeries / “this and future” UI / scheduler horizon fill were **not** finished.
- **List projection does not select `series_id` or recurrence text** (`eventColumns` in `events.go`). Discovery lists every occurrence as a separate card. That is the 12-card bug.

### Profile / groups

- Profile tabs Posts \| Events \| Groups. Events: upcoming/past via `date=`. Groups: `GET /groups/user/:username?public_only=1`.
- Group Events tab: Upcoming \| Past.
- `GetUserGroups` prefers username over account_id (so you don’t see the viewer’s groups on someone else’s profile).

### Gateway routes (events write)

- `POST /api/v1/events`, `POST /api/v1/events/series` (must stay **before** `/:slug`)
- `POST /api/v1/events/:slug/clone`

---

## Known bugs / product gaps (next slice)

1. **Discovery floods with series occurrences.** Weekly series of 12 shows 12 identical cards. Want 1 card (next date) plus “Repeats every …”, optionally up to 3 upcoming dates on that card — not 12 tiles.
2. **Cover only on first occurrence.** Create series → 12 rows with empty `cover_image` → host uploads cover against the first slug only. `new/page.tsx` uploads after create to that one slug. Later dates show the calendar placeholder.
3. **Slow discovery.** 12 cards, radius auto-step refetches, per-row attendee `COUNT` subquery, `log.Debugw` of full SQL, missing series collapse. UI feels heavy.
4. **Coupon duplicate.** Unique `(event_id, code)`. `CreateCoupon` does `status.Errorf(AlreadyExists, "failed to create coupon: %v", err)` → toast shows `SQLSTATE 23505`. Need a human line: “That coupon code is already on this event.”
5. **Paid ticket on a host machine without Razorpay keys.** Toast: “payments are not configured on this deployment.” Product question (answered in the plan): **Monkeys owns the Razorpay merchant for now**; organizers do not paste their own keys. Enable keys in `.env` for paid tickets; UI copy should be host-facing, not infra-facing.
6. **Last date to RSVP** — columns exist, not wired in form, gateway body, CreateEvent/UpdateEvent, or CreateRSVP.
7. **RSVP this occurrence vs entire series** — not built. Attendees are per `event_id`.

---

## Conventions the user enforces

- After backend changes: `docker compose up --build -d`. Leftover compose names (`hash_the_monkeys_events`) can block recreate; `docker rm -f` the conflict then `up -d`.
- Do not leak SQL/internal errors to the UI.
- Do not add blogs to the group tab bar.
- Do not grey-out past cards in a way that fails light theme; use an **Ended** pill.
- Recurring: reuse `event_series` / `recurring.go`, no second series table.
- Protobuf: new field numbers only; never reuse.
- Frontend lint: Prettier. Wrap long signatures/imports. `pnpm`/`prettier` live under `apps/the_monkeys/node_modules`.
- User pasted live JWTs in chat once — never put tokens in docs or logs.

---

## Quick RPC / REST cheat sheet

| Action | REST | Notes |
| --- | --- | --- |
| List discovery | `GET /api/v1/events?date=upcoming&user_lat&user_lng&radius` | Default upcoming |
| Create one-off | `POST /api/v1/events` | Draft |
| Create series | `POST /api/v1/events/series` | Body + `recurrence`; returns first occurrence; others published |
| Clone | `POST /api/v1/events/:slug/clone` | `{ start_time, end_time }` |
| Coupon | `POST /api/v1/events/:slug/coupons` | Unique per event+code |
| Paid tier | `POST /api/v1/events/:slug/tiers` | Blocked if Razorpay keys missing |
| User events | `GET /api/v1/events/user/:username?date=upcoming\|past` | Drafts not on public profile |
| User groups | `GET /api/v1/groups/user/:username?public_only=1` | |

---

## Docs index

| File | Use |
| --- | --- |
| `docs/context.md` | This handoff |
| `docs/series-ux-rsvp-payments-plan.md` | **Next work — wait for approval** |
| `docs/recurring-past-events-profile-plan.md` | Previous slice (mostly done; discovery series collapse was not in that plan) |
| `docs/meetup-parity-implementation-plan.md` | Long-term Meetup parity (groups already largely done) |
| `docs/meetup-parity-frontend-implementation-plan.md` | Frontend Meetup parity |
| Root `context.Md` | Stale geo/admin prompt — ignore |

When in doubt: read the code in the tables above, not the stale root `context.Md`.
