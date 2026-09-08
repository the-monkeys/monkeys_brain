# Events and groups — frontend integration

Date: 2026-09-06  
Branch: `feat/payments-admin-backend` (working tree, including uncommitted geo/payments/delete)  
Audience: **existing public site** (`apps/the_monkeys`). Not the staff Dashboard.

Staff event/group admin (`/api/v1/admin/events`, `/api/v1/admin/groups`, NSFW, settlements) is documented in `docs/frontend-admin-dashboard.md`. Do not call those from this app.

Gateway: `http://localhost:8081`. Browser uses cookie `mat` (or `Authorization: Bearer`). Errors: `{ "error": "<message>" }`. Show that string in toasts. Do not show gRPC codes.

Signup role stays Viewer. No new user/auth APIs in this doc.

---

## What you must change (this branch)

These are the **deltas** the existing events/groups UI must handle. URLs below already exist unless marked **new field**.

| Change | API | UI work |
| --- | --- | --- |
| Near-me radius | `GET /api/v1/events`, `GET /api/v1/groups` | Send `user_lat`, `user_lng`, and `radius` **2–100** (default **25**). Omit radius or send `0` = nationwide (do **not** use 0 for near-me). Engine does not default 25. |
| Map pin on create/edit | `POST/PUT /api/v1/events`, `POST /api/v1/events/series` | **New JSON** `latitude`, `longitude` (optional). Both non-zero → stored; skip Nominatim. Virtual → ignored (coords stay null). Omitted → same as today (geocode `location`). |
| Group pin | `POST/PUT /api/v1/groups` | Already had `latitude`/`longitude`. Same rule: both non-zero = pin. |
| Hard delete | `DELETE /api/v1/events/:slug`, `DELETE /api/v1/groups/:slug` | **409** if captured/pending payments. Show server text. Cancel (and refund) first. Unsold paid tiers do **not** block event delete. |
| Paid RSVP / paid tier | `POST /api/v1/events/:slug/rsvp`, `POST /api/v1/events/:slug/tiers` | Still **409** if Razorpay keys missing. Copy is host-facing. |
| Images | event/group cover, photos, logo | Same paths. New uploads strip GPS/EXIF. No request-shape change. Gallery still max **4**. |
| Draft detail | `GET /api/v1/events/:slug`, `GET /api/v1/groups/:slug` | Draft / private still **404** for people who should not see them. |

**Not backfilled:** events/groups with NULL coords stay off radius results. Hosting list `GET /api/v1/events/user/:username` still shows them. After a successful pin save, they appear in near-me.

`GET` event JSON still has **no** `latitude`/`longitude` (only `location` string + optional `venue`). Groups **do** return `latitude`/`longitude`. If the map needs a pin on edit, keep it in client state or re-send the last pin.

---

## 1. Events — HTTP surface

Base: `/api/v1/events`. Auth: public reads; cookie on writes. `AuthOptional` on detail / profile / group agenda / comments / share / calendar so the viewer RSVP and drafts hydrate when logged in.

### 1.1 List and read

| Method | Path | Auth | Notes |
| --- | --- | --- | --- |
| GET | `/api/v1/events` | public | Discovery. Query below. |
| GET | `/api/v1/events/user/:username` | optional | Hosting. `date=upcoming\|past`. Drafts not on public profile. |
| GET | `/api/v1/events/group/:slug` | optional | Group agenda. Per-date (not collapsed). |
| GET | `/api/v1/events/attending` | required | Caller’s RSVPs. |
| GET | `/api/v1/events/:slug` | optional | Detail. Draft **404** for non-hosts. |
| GET | `/api/v1/events/:slug/comments` | optional | |
| GET | `/api/v1/events/:slug/share` | optional | SEO/share meta. |
| GET | `/api/v1/events/:slug/calendar` | optional | `.ics` download. |

**List query (`GET /api/v1/events` and user/group lists that use the same helper):**

| Query | Default | Meaning |
| --- | --- | --- |
| `limit` | 20 | Max 100. |
| `offset` | 0 | |
| `date` | upcoming if empty | `upcoming`, `past`, `this-week`, `this-month`, `all` |
| `type` | | `virtual`, `in_person`, `hybrid` |
| `status` | | Usually leave empty on public discovery. |
| `q` | | Title search. |
| `location` | | String ILIKE **only when `radius` is 0**. |
| `tags` | | Comma-separated. |
| `sort` | soonest | `soonest`, `popular`, `newest`, `nearest` (`nearest` needs a pin; else soonest). |
| `user_lat` / `user_lng` | omitted | Viewer pin. `0,0` = no pin. |
| `radius` | **0** | Kilometres. **0 / omitted / negative = no geo filter.** `> 0` clamped to **[2, 100]**. |

Near-me: pin **and** `radius` in 2–100. Virtual/hybrid events are not hidden by radius. Rows with NULL coords are omitted from that filter only.

Discovery/profile collapse one card per series (`recurrence_text`, `upcoming_dates` max 3). Group agenda and past lists stay one row per date.

### 1.2 Create / update / series / clone

| Method | Path | Auth | Body |
| --- | --- | --- | --- |
| POST | `/api/v1/events` | required | `EventBody` → **201**, draft. |
| POST | `/api/v1/events/series` | required | Same body + `recurrence` required → **201**, first occurrence. **Must stay before `/:slug`.** |
| PUT | `/api/v1/events/:slug` | `edit_event` | `EventBody`. |
| POST | `/api/v1/events/:slug/clone` | `edit_event` | `{ "start_time", "end_time" }` → new **draft**. Copies source coords (no re-geocode). |
| POST | `/api/v1/events/:slug/publish` | `edit_event` | |
| POST | `/api/v1/events/:slug/cancel` | `edit_event` | Refunds paid RSVPs. |
| DELETE | `/api/v1/events/:slug` | organizer | Hard delete. **409** if payments. |

**`EventBody` (create / update / series):**

```json
{
  "title": "",
  "description": "",
  "start_time": "RFC3339",
  "end_time": "RFC3339",
  "timezone": "Asia/Kolkata",
  "event_type": "virtual | in_person | hybrid",
  "location": "",
  "latitude": 12.97,
  "longitude": 77.59,
  "meeting_link": "",
  "capacity": 0,
  "cover_image": "",
  "tags": [],
  "co_host_usernames": [],
  "ticket_tiers": [{ "name": "", "price": 0, "currency": "INR", "capacity": 0 }],
  "group_slug": "",
  "visibility": "public | group_members | private | unlisted",
  "rsvp_closes_at": "RFC3339 or omit",
  "rsvp_close_hours_before": 0,
  "recurrence": {
    "freq": "weekly",
    "interval": 1,
    "by_day": ["SA"],
    "count": 0,
    "until": null,
    "rsvp_close_hours_before": 0
  }
}
```

`latitude` / `longitude` are **new, optional**. Omit both for old clients. Send both for a map pin (in-person/hybrid). Virtual: omit or send; server stores NULL.

Ended events: host may edit title, description, cover, tags only.

After create, send the host to `/events/:slug`, not manage.

### 1.3 Tickets, coupons, RSVP

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/:slug/tiers` | Paid tier **409** if Razorpay not configured. |
| PUT / DELETE | `/:slug/tiers/:id` | |
| POST | `/:slug/coupons` | Unique `(event, code)`. |
| GET | `/:slug/coupons` | Host only. |
| DELETE | `/:slug/coupons/:id` | |
| POST | `/:slug/coupons/validate` | Attendee checkout. |
| POST | `/:slug/rsvp` | Body below. |
| DELETE | `/:slug/rsvp` | |

**RSVP body:**

```json
{
  "ticket_tier_id": 1,
  "coupon_code": "",
  "scope": "this"
}
```

`scope`: `this` (default) or `series`. Series = every open future occurrence. **Paid + `series` is refused.** Ended event / after `rsvp_closes_at` → error.

**RSVP response (paid):** `status` `pending_payment`, plus `payment_order_id`, `amount_due`, `currency`, `razorpay_key_id` for Checkout. Confirm happens on webhook `POST /api/v1/events/payment/webhook` (not the browser).

### 1.4 Host tools and social

| Method | Path |
| --- | --- |
| GET | `/:slug/attendees` |
| GET | `/:slug/attendees/export` |
| PUT | `/:slug/attendees/:id/attendance` |
| POST / DELETE | `/:slug/save` |
| POST / DELETE | `/:slug/comments`, `/:slug/comments/:id` |
| POST / DELETE | `/:slug/react` |
| POST | `/:slug/report` |
| POST / DELETE | `/:slug/cohosts`, `/:slug/cohosts/:username` |

### 1.5 Event images

Writes (auth + `edit_event`):

| Method | Path | Form |
| --- | --- | --- |
| POST | `/api/v1/events/:slug/images/cover` | `image` |
| DELETE | `/api/v1/events/:slug/images/cover` | |
| POST | `/api/v1/events/:slug/photos` | `image`. **409** if gallery full (4). |
| DELETE | `/api/v1/events/:slug/photos/:photo` | |

Public reads (`/api/v2/storage` and `/api/storage` alias):

| Method | Path |
| --- | --- |
| GET | `/api/v2/storage/events/:slug/cover` |
| GET | `/api/v2/storage/events/:slug/photos` |
| GET | `/api/v2/storage/events/:slug/photos/:photo` |

---

## 2. Groups — HTTP surface

Base: `/api/v1/groups`. Invite links use `/api/v1/group-invites` so `:token` does not collide with `:slug`.

### 2.1 List and read

| Method | Path | Auth | Notes |
| --- | --- | --- | --- |
| GET | `/api/v1/groups` | optional | Public published. Geo query same as events (`user_lat`, `user_lng`, `radius`). Also `country`, `region`, `city`, `q`, `topics`, `status`. |
| GET | `/api/v1/groups/user/:username` | optional | `public_only=1` on someone else’s profile. |
| GET | `/api/v1/groups/:slug` | optional | Draft/private **404** if the caller cannot see it. Returns `latitude`, `longitude`, `viewer_role`, `viewer_member_status`, `rules`. |

Near-me rules are the same as events (km, clamp 2–100, radius 0 = nationwide). Groups have no virtual type; NULL coords drop out of radius only.

### 2.2 Create / update / delete

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/api/v1/groups` | Draft. Body: name, visibility, city/region/country, optional `latitude`/`longitude`, topics, images URLs. |
| PUT | `/api/v1/groups/:slug` | `edit_group`. |
| POST | `/api/v1/groups/:slug/publish` | |
| DELETE | `/api/v1/groups/:slug` | Organizer. **409** if a child event has captured/pending payments. |

### 2.3 Members, invites, rules, group event

| Method | Path |
| --- | --- |
| POST | `/:slug/join` |
| DELETE | `/:slug/membership` |
| GET | `/:slug/members` |
| PUT | `/:slug/members/:username/role` |
| DELETE | `/:slug/members/:username` |
| POST | `/:slug/members/:username/ban` |
| POST | `/:slug/members/:username/approve` |
| POST | `/:slug/members/:username/reject` |
| POST | `/:slug/members` |
| POST / GET / DELETE | `/:slug/invites`, `/:slug/invites/:id` |
| GET | `/api/v1/group-invites/:token` (optional auth) |
| POST | `/api/v1/group-invites/:token/accept` |
| POST / PUT / DELETE | `/:slug/rules`, `/:slug/rules/:id` |
| POST | `/:slug/events` | Create event under the group (`manage_events`). Same event create body. |

### 2.4 Group images

| Method | Path |
| --- | --- |
| POST / DELETE | `/api/v1/groups/:slug/images/:kind` | `kind` = `logo` \| `cover`. Form field `image`. |
| GET | `/api/v2/storage/groups/:slug/:kind` | Public read. |

---

## 3. Status codes the UI must handle

| Code | When |
| --- | --- |
| 200 / 201 | Success. |
| 400 | Bad JSON / missing `recurrence` on series / invalid image kind. |
| 401 | Write without cookie. |
| 403 | Missing host permission. |
| 404 | Unknown slug, or draft/private the viewer cannot see. |
| **409** | Event/group delete with payments; gallery full; paid RSVP/tier when payments off; (staff) unpublish with paid attendees. |
| 503 | Account delete cannot reach events/groups (not an event route; settings). |

---

## 4. Frontend file map (existing app)

| Area | Typical files |
| --- | --- |
| Discovery | `EventsDiscover.tsx`, `lib/geoSearch.ts` |
| Event form | `EventForm.tsx`, `events/new`, `events/[slug]/edit` |
| Detail / RSVP | `EventDetailClient.tsx`, `RsvpPanel.tsx` |
| Host | `EventManage.tsx` |
| Groups | `GroupCommunity.tsx`, `groupsApi.ts` |
| Types | `eventTypes.ts`, `groupsTypes.ts` |

**Geo UI:** `geoSearch.ts` should use km steps inside **2–100**, default **25**, and never send `radius=0` while a pin is set if the user asked for near-me.

**API clients:** `eventsApi.ts` / `groupsApi.ts` — add `latitude`/`longitude` on event create/update/series; keep group pin fields; map **409** on delete.

---

## 5. Out of scope for this app

- `/api/v1/admin/*` (Dashboard).
- Blog storage draft 404s, verification object keys.
- Account `DELETE /api/v1/user/:id` (settings, not events UI) except: deleting a host account also **409**s on paid events/groups — handle there.
- Backfill of old NULL coordinates.
- Miles, PostGIS, `/discover`.
