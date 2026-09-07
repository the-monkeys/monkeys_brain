# Recurring Events, Past Meetups, and Profile Surface

Status: **plan only — wait for approval before coding.**

This is additive Meetup-style work. Existing `/api/v1/events` and `/api/v1/groups` contracts stay valid. Reuse what already exists; do not add parallel tables or duplicate card components.

---

## Current state (what we already have)

### Recurring events — half-built, not exposed

| Layer | Status |
| --- | --- |
| Schema `event_series` + `events.series_id` / `series_occurrence_at` | Done (`schema/000011_meetup_communities.up.sql`) |
| DB helpers `CreateSeries`, `GenerateSeriesOccurrences`, `CancelSeriesOccurrence`, `UpdateSeriesFutureOccurrences` | Done (`microservices/the_monkeys_events/internal/database/recurring.go`) |
| gRPC / gateway / EventForm | **Missing** — no RPC, no REST, no create UI |
| Detail badge | Display-only (`EventSeriesNote.tsx`) |

`recurrence_rule` is a free-text column intended for RFC 5545 RRULE (e.g. `FREQ=WEEKLY;INTERVAL=1;BYDAY=TU`). Expansion into datetimes is meant to live in the service layer; the DB only stores the rule and materialises rows it is given.

### Past / completed meetups

- Scheduler marks `published`/`live` as `completed` when `end_time < NOW()` (`scheduler.go` + `ArchivePastEvents`).
- **Discovery still shows them.** `ListEvents` uses `publicStatuses = published, live, completed`. The UI "All upcoming" sends **no** `date` query. Backend only applies `start_time >= CURRENT_DATE` when `date=all`. Empty `date` = no time filter. That is why Bengaluru Aug 24 events appear on the light-theme grid.
- RSVP is already blocked for `completed`/`cancelled` in `CreateRSVP` and `RsvpPanel` (`closed` flag). A race remains: if the scheduler has not run yet, `end_time` is past but status is still `published`, so RSVP still works.
- `UpdateEvent` has **no** status/time guard — a host can fully rewrite a completed event (title, time, location, capacity).
- There is **no** "schedule again" / clone RPC.

### Photos (glimpses)

Already live, cap of 4, host-only writes:

- `POST /api/v1/events/:slug/photos` and `DELETE .../photos/:photo` (edit_event)
- `GET /api/v2/storage/events/:slug/photos` (public)
- UI: `EventGallery.tsx` (`MAX_PHOTOS = 4`)

Keep this. On past events, treat it as the glimpse gallery (hosts can still add after the meetup).

### Profile page

`apps/the_monkeys/src/app/[username]/page.tsx` stacks blogs (`Blogs` + `ProfileBlogCard`) then a list of `EventCard`. No groups. No tabs. Not the discovery card grid.

Group page tabs (`Events | About | Members | Join requests | Invites`) stay as they are. This plan does **not** put blogs on the group tab bar.

---

## Product rules

### Recurrence

Organizer can create a series with:

- every N days
- weekly (optional weekdays)
- monthly (same day-of-month)
- yearly
- optional end: after N occurrences **or** until a date

Each occurrence is a normal `events` row linked by `series_id`. RSVP, comments, tickets, and photos stay per occurrence. Editing "this event" vs "this and future" is a series concern.

Horizon: generate the next ~12 occurrences up front; the existing events scheduler fills more as the window moves. Cap at 52 generated rows per create to avoid runaway inserts.

### When a meetup is over

| Action | After `end_time` / `completed` |
| --- | --- |
| RSVP / cancel RSVP / pay | No |
| Public discovery / "Popular events near …" | No |
| Open the detail page | Yes (history) |
| Host edit title, description, cover, tags, photos | Yes (glimpses / writeup) |
| Host change time, type, location, capacity, tickets, visibility | No on the same slug |
| Host "Schedule again" | Yes — creates a **new** event (copy fields, new slug, new times) |
| Host cancel / delete | Cancel stays; delete still blocked if paid attendees exist |
| Gallery (max 4) | Yes — this is the glimpse |

If the event is part of a series, "Schedule again" is unnecessary; the next occurrence already exists. Show a link to the next upcoming sibling instead.

### Profile (`/{username}`)

Tab bar in the same style as the group community tabs:

1. **Posts** — existing blogs
2. **Events** — public events this user hosts, **card grid** (`EventGridCard`)
3. **Groups** — public published groups they organize, **card grid** (`GroupGridCard`)

Events tab: **Upcoming** then a collapsed **Past** row. Other visitors never see drafts. Owner can still see drafts in Hosting (`/events` tab), not on the public profile.

### Discovery date

Default feed = upcoming only (`end_time >= NOW()` and status in `published`/`live`). Past events belong on the group Events tab (Past) and the profile Events tab (Past), not on "Popular events near …".

---

## Backend (do first)

### Step B1 — Discovery default is upcoming

**Where:** `microservices/the_monkeys_events/internal/database/events.go`

- Add `DateFilterUpcoming` (`upcoming`) and `DateFilterPast` (`past`).
- `ListEvents`: if `date` is empty, treat as `upcoming`. Filter `e.end_time >= NOW()` and **drop `completed` from the default public status list** for this RPC only (`published` + `live`).
- `date=past`: `e.end_time < NOW()` (or `status = completed`), still public visibility.
- `date=this-week` / `this-month` stay as they are, but also exclude ended events unless `past`.
- `GetGroupEvents` / `GetUserEvents`: do **not** force upcoming; the UI will pass `date=upcoming` or `date=past`. If `date` is empty on those RPCs, keep current behaviour for the Hosting dashboard (owner sees everything they asked for via `status`).
- `commonFilters`: when `date=all`, keep `start_time >= CURRENT_DATE` (upcoming). Do not use `all` to mean "including years ago".

**Where:** `apis/serviceconn/gateway_event/pb/gw_event.proto` — comment on `date_filter` only; field number unchanged.

**Where:** gateway `listQuery` — no new params.

Backward compatible: clients that already send `date=all` still get upcoming. Clients that send nothing to `GET /events` start getting upcoming instead of the full history. That is the intended fix.

### Step B2 — RSVP and edit guards on ended events

**Where:** `attendees.go` `CreateRSVP` — refuse if `end_time < NOW()` even when status is still `published` (same error as completed).

**Where:** `CancelRSVP` — refuse after end (seat is historical).

**Where:** `events.go` `UpdateEvent` — load status + end_time. If ended/completed/cancelled:

- Allow: title, description, cover_image, tags (glimpse writeup).
- Reject: start_time, end_time, event_type, location, meeting_link, capacity, visibility (FailedPrecondition).

Keep one UPDATE; compare requested time/type/location/capacity to stored values and reject if they changed. Do not split into a second RPC.

### Step B3 — Schedule again (clone)

**Where:** proto `EventService` — additive RPC:

```
rpc CloneEvent(CloneEventReq) returns (EventResp);
```

`CloneEventReq`: `slug`, `account_id`, `start_time`, `end_time`, `client_info`. Field numbers new. Copies title, description, type, location, meeting_link, capacity, cover, tags, group, visibility, ticket-tier *templates* (not attendee counts). New slug. Status `draft`. Host must then publish.

**Where:** `events.go` new `CloneEvent`; service + gateway `POST /api/v1/events/:slug/clone`. Auth + organizer/co-host `edit_event`.

Do not clone cancelled paid-attendee state. Do not clone `series_id` (the clone is a one-off unless the host later attaches a series).

### Step B4 — Recurring series API (reuse `recurring.go`)

No new tables.

**Where:** `gw_event.proto` — additive messages + RPCs (new field numbers only):

- `CreateSeries` — body = event fields + `recurrence` (`freq`, `interval`, `by_day[]`, `count` or `until`).
- `GetSeries` / `ListSeriesOccurrences`
- `UpdateSeries` — `scope = this | this_and_future` (maps to existing `UpdateSeriesFutureOccurrences` + single `UpdateEvent`)
- `CancelSeriesOccurrence` — already in DB
- `PauseSeries` / `ResumeSeries` — `event_series.status`

Service layer:

- New small file `microservices/the_monkeys_events/internal/services/rrule.go`: build RRULE string + expand next N timestamps. One library (e.g. `github.com/teambition/rrule-go`) or a tight local expander for the four freqs above — pick one, wrap it, do not leak RRULE strings through REST if we can send structured `recurrence` instead. Store RRULE in DB as today.
- `CreateSeries` RPC: insert series, generate occurrences via existing `GenerateSeriesOccurrences`, return the first slug.
- Scheduler (`runUpkeep`): after `ArchivePastEvents`, call `GenerateSeriesOccurrences` for active series whose last generated occurrence is inside the horizon. Idempotent (DB already skips existing `series_occurrence_at`).

**Where:** gateway `microservices/the_monkeys_gateway/internal/events/`

- `POST /api/v1/events/series`
- `GET /api/v1/events/series/:id`
- `PATCH /api/v1/events/series/:id`
- `POST /api/v1/events/:slug/cancel-occurrence`

Authorize with the same host guard as create event.

Do **not** add recurrence fields onto `CreateEventReq` (keeps the one-off path unchanged). Series is a separate create.

### Step B5 — User public groups for profile

`GET /api/v1/groups/user/:username` already exists (`GetUserGroups`). For a **public profile** we need only public published groups they organize.

Options (pick the first; smaller):

- Reuse `ListGroups` with no extra RPC if we add `organizer_username` filter — **avoid** unless we already have it.
- Prefer: `GetUserGroups` + query `public_only=1` (additive proto field on `ListGroupsReq`). When set, restrict `visibility = public` and `status = published`, hide drafts. Other callers unchanged.

**Where:** `microservices/the_monkeys_groups/internal/database/search.go` `GetUserGroups`.

No migration.

---

## Frontend (after backend)

Repo: `local/the_monkeys` (not this engine repo).

### Step F1 — Stop showing ended events on discovery

**Where:** `EventsDiscover.tsx`

- Default `date` to `upcoming` (send `date=upcoming`, not empty).
- Relabel the select: Upcoming / This week / This month. Remove the meaning "all including past".
- Empty state stays "No events around {city} yet".

**Where:** `CommunityGroups.tsx` — unchanged (groups are not dated).

### Step F2 — Group Events tab: Upcoming | Past

**Where:** `GroupCommunity.tsx` `GroupEventsPanel`

- Two queries or one query with `date=upcoming` plus a "Past events" toggle (`date=past`).
- Same `EventGridCard` grid.
- Ended cards: no RSVP CTA; optional faded "Ended" badge on the card (`EventGridCard` — one status chip, reuse `eventStatusLabel`).

Do not add Blogs or Groups tabs on this bar.

### Step F3 — Past event detail

**Where:** `RsvpPanel.tsx` — `closed` if cancelled, completed, **or** `end_time` in the past. Copy: "This meetup has ended".

**Where:** `EventDetailClient.tsx` / `EventActions.tsx`

- Host: hide Edit time/capacity; keep Edit for writeup **or** disable those fields in `EventForm` when `isEnded`.
- Host: button **Schedule again** → small dialog (new start/end) → `POST .../clone` → redirect to new draft.
- If `series_id` set, show next occurrence link instead of clone (uses `GetSeries`).
- Gallery heading: if ended, **Glimpses** instead of **Photos**. Same 4-photo cap, same upload for hosts.

**Where:** `EventForm.tsx` — if `event.status === 'completed'` or ended, only title, description, cover, tags are enabled.

### Step F4 — Recurring create UI

**Where:** `EventForm.tsx` (create only, not edit of a single occurrence)

- Collapsed "Repeat" block: Off / Daily / Weekly / Monthly / Yearly, interval number, weekly day chips, end (never / on date / after N).
- If Repeat ≠ Off, submit to `POST /events/series` instead of `POST /events`.
- Edit occurrence: existing form; add radio "Only this event" vs "This and future" when `series_id` is set (PATCH series vs PUT event).

**Where:** `eventsApi.ts` + `eventTypes.ts` — series types and calls next to existing event helpers.

Keep RRULE out of the React tree; send structured `recurrence`.

### Step F5 — Profile tabs + card grids

**Where:** `app/[username]/page.tsx` + new `ProfileActivity.tsx` (or similar)

Extract the tab button already in `GroupCommunity.tsx` into a tiny shared `TextTabs` (active orange underline) so profile and group stay DRY.

Tabs:

| Tab | Data | Card |
| --- | --- | --- |
| Posts | existing `Blogs` | keep `ProfileBlogCard` (list is fine for long posts; optional wrap in `grid` without redesigning the blog card) |
| Events | `listUserEvents(username, { date: 'upcoming' })` + past toggle | `EventGridCard` (same as discovery) |
| Groups | `listUserGroups(username, { public_only: true })` | `GroupGridCard` |

Remove the separate `ProfileEvents` list-of-`EventCard` block once the Events tab exists.

Owner vs visitor: same tabs; drafts stay off the public Events tab.

### Step F6 — Theme / "Ended" on cards

Ended events on Past grids: keep full opacity, add a small **Ended** pill. Do not grey-out in a way that fails light theme contrast. Discovery never receives them after F1/B1.

---

## What we will not do in this pass

- Recurring fees, per-occurrence different venues, or "exceptions" calendar UI beyond skip/cancel one date.
- Attendee-uploaded photos (host gallery only, already capped at 4).
- Blogs inside the group tab bar.
- New migrations if `event_series` already covers the rule (it does).
- Backfill of old events into series.
- Changing protobuf field numbers on existing messages.

---

## File map (quick)

Backend:

- `schema/000011_meetup_communities.up.sql` — already has series (no 000015 unless we discover a gap)
- `apis/serviceconn/gateway_event/pb/gw_event.proto` — CloneEvent + series RPCs
- `microservices/the_monkeys_events/internal/database/events.go` — date defaults, UpdateEvent guards
- `microservices/the_monkeys_events/internal/database/attendees.go` — RSVP after end
- `microservices/the_monkeys_events/internal/database/recurring.go` — reuse
- `microservices/the_monkeys_events/internal/services/rrule.go` — new, small
- `microservices/the_monkeys_events/internal/services/service.go` + `scheduler.go` — wire generate
- `microservices/the_monkeys_gateway/internal/events/handler.go` + `routes.go`
- `microservices/the_monkeys_groups/internal/database/search.go` — `public_only`

Frontend (`local/the_monkeys`):

- `src/lib/geoSearch.ts` — untouched
- `src/components/events/discover/EventsDiscover.tsx` — upcoming default
- `src/components/groups/detail/GroupCommunity.tsx` — upcoming/past
- `src/components/events/RsvpPanel.tsx`, `EventForm.tsx`, `EventActions.tsx`, `EventDetailClient.tsx`, `EventGallery.tsx`
- `src/app/[username]/page.tsx`, `ProfileEvents.tsx` → tabbed profile
- `src/services/events/eventsApi.ts`, `eventTypes.ts`

---

## Suggested build order

1. B1 + F1 (past events vanish from discovery) — smallest user-visible win
2. B2 + F3 (RSVP/edit/glimpses on ended detail)
3. B3 + Schedule again button
4. B5 + F5 (profile tabs)
5. B4 + F4 (recurring) — largest; ships last so one-off paths stay stable

Approve this order or say which slice to do first.
