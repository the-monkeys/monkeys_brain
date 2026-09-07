# Series discovery, RSVP window, payments copy, and speed

Status: **plan only — wait for approval before coding.**

Fixes the weekly series that paints **12 identical tiles** on Popular events, the missing covers on dates 2–12, raw SQL coupon toasts, the paid-ticket “payments are not configured” line, last-day-to-RSVP, RSVP-this vs RSVP-all, and discovery slowness.

Keep one-off events unchanged. Keep generating ~12 occurrence **rows** (RSVP still needs a real date). Change what **discovery lists** and what **hosts/attendees see**.

---

## What you saw (and why)

| Symptom | Cause |
| --- | --- |
| 12 cards, same title, weekly dates | `CreateSeries` inserts 12 **published** `events` rows. `ListEvents` has no series collapse. `eventColumns` does not even select `series_id` / RRULE, so the UI cannot badge “Repeats weekly”. |
| Banner only on Sep 5 | Cover is uploaded **after** create, to the **first slug only**. The other 11 rows were inserted with empty `cover_image`. Placeholder calendar icon is `EventGridCard` with no `cover_image`. |
| Slow grid | 12 cards + 12 image slots; discovery `useEventList` plus radius step-up refetches; each list row runs `(SELECT COUNT…event_attendees)`; list query is debug-logged. |
| Coupon toast with `SQLSTATE 23505` | `CreateCoupon` wraps the Postgres unique error in the gRPC message. Gateway `fail()` returns `st.Message()` to the client. Code `MONKEYS11` already existed on that event. |
| “payments are not configured on this deployment” | Paid tier (`₹499`) requires platform Razorpay keys. Local `.env` `KEYS_RAZORPAY_*` is empty → `pay.enabled()` is false. |

---

## Product rules

### Discovery (Popular / search)

- **One card per series**, representing the **next upcoming** occurrence (`start_time >= NOW()`, not cancelled).
- Card copy: title, that next date/time, cover, price, host. Badge from the rule, e.g. **Every week** / **Every 2 weeks on Sat**.
- Optional extra on the same card (not extra tiles): up to **two more** upcoming dates (so the card can show 1–3 dates total). Clicking the card opens the **next** occurrence detail.
- One-off events: one card, unchanged.
- Group Events tab **Upcoming**: still a calendar of dates (hosts need to see each meetup). Collapse is for **discovery + profile public Events**, not the group agenda.
- Group / profile **Past**: one row per occurred date (history).

### Covers

- All future occurrences in a series share the same cover URL unless a host later overrides one date (out of scope for this pass — copy everywhere).
- After cover upload or `cover_image` update on any occurrence, copy that URL to **all series siblings** (or at least all with empty cover + all future).
- When generating more occurrences, stamp `event_series` cover (add column) or copy from the latest sibling with a cover. Prefer **`event_series.cover_image`** as source of truth so generate/horizon fill cannot drift.

### Errors (hosts and attendees)

Gateway/UI show **one short sentence**. Log the SQL on the server.

| Situation | Toast |
| --- | --- |
| Duplicate coupon code | “That coupon code is already on this event.” |
| Paid ticket, Razorpay keys missing | “Paid tickets aren’t enabled yet. Add a free ticket, or use ₹0 until payments are turned on.” |
| Event ended | “This meetup has ended.” (already mostly there) |
| Anything else 5xx | “Something went wrong. Try again.” |

Never interpolate `err` from `database/sql` into `status.Errorf`.

### Payments (decision)

**Monkeys is the merchant.** Organizers do **not** paste their own Razorpay keys. Money for paid RSVPs hits the platform Razorpay account (keys in `.env` / prod secrets). Paying organizers out (Route / transfers / invoices) is a later settlements project — do not build per-host gateways now.

- Local/dev: set `KEYS_RAZORPAY_KEY_ID` + `SECRET` (+ webhook secret) to allow paid tickets. Until then, free tickets work; paid create is blocked with the host-facing sentence above.
- Verified-host rule for paid tiers (`requireVerifiedForPaidTiers`) stays.

### Last date to RSVP

- One-off: optional datetime on create/edit. Empty = open until the event ends (existing end_time guard).
- Recurring: do **not** ask for one absolute calendar date for the whole series (that would close October dates in September). Use a **relative** close: Off \| hours/days **before each occurrence starts** (e.g. “1 day before”). Empty/Off = no extra close.
- Store: `events.rsvp_closes_at` per occurrence (column already exists). For series, also store offset on `event_series` (new nullable `rsvp_close_before_interval` or minutes int) and apply when generating/updating occurrences.
- `CreateRSVP`: if `rsvp_closes_at` is set and `NOW() > rsvp_closes_at`, refuse with “RSVP for this meetup has closed.”
- `rsvp_opens_at` can wait unless we need it for the same form.

### RSVP this date vs all upcoming

On a series occurrence detail, attendees see:

- **RSVP this meetup** (default) — current behavior, one `event_attendees` row.
- **RSVP all upcoming** — same ticket tier (and coupon if any) applied to every **future** published/live occurrence in that series that is still open.

Constraints for this pass:

- **Free series:** implement fully (batch insert attendees, skip dates already confirmed / ended / closed / full → waitlist those independently).
- **Paid series:** do **not** invent a 12× checkout in this pass. Hide “all upcoming” or disable with “RSVP each date separately for paid meetups.” One Razorpay order covering N dates needs settlements + refunds we do not have.

Hosts are not attendees (existing organizer block stays).

---

## Backend

### B1 — List projection includes series

`events.go` `eventColumns` / `scanEvent`: add `series_id`, `series_occurrence_at`. Join `event_series` for `recurrence_rule` (or a short `recurrence_text` the service already can format). Hydrate once per list, not N+1.

Without this the UI cannot collapse or badge.

### B2 — Collapse series on discovery (and public profile list)

`ListEvents` (and `GetUserEvents` when used as public profile): return **at most one row per `series_id`**, the soonest upcoming occurrence. One-offs (`series_id IS NULL`) unchanged.

Postgres shape (sketch):

```sql
AND (
  e.series_id IS NULL
  OR e.id = (
    SELECT e2.id FROM events e2
    WHERE e2.series_id = e.series_id
      AND e2.status IN ('published', 'live')
      AND e2.end_time >= NOW()
    ORDER BY e2.start_time ASC
    LIMIT 1
  )
)
```

Keep this **off** for `GetGroupEvents` and `date=past`.

Optional payload on the representative event: `upcoming_dates[]` (max 3 ISO starts) so the card can show “Sep 5 · 12 · 19” without extra round trips. Additive proto field.

`total` must count collapsed series, not raw occurrences, or the pager lies.

### B3 — Cover on the series

- Migration `000015`: `event_series.cover_image TEXT`.
- On `MaterializeSeries` / `GenerateSeriesOccurrences`, stamp `tmpl.CoverImage` from the series row if the request cover is empty.
- On cover upload / `UpdateEvent` cover change: if `series_id` set, write series.cover_image and `UPDATE events SET cover_image = $1 WHERE series_id = $2` (this pass: all siblings; “only this date” later).
- Frontend `new/page.tsx`: after `createSeries` + `uploadEventCover(firstSlug)`, persist cover on that slug **and** rely on backend propagate (do not loop 12 updates from the browser).

### B4 — Human errors

`tickets_coupons.go` `CreateCoupon`: detect unique violation (`23505` / `event_coupons_event_id_code_key`) → `codes.AlreadyExists` **without** `%v`. Message: `that coupon code is already on this event`.

`CreateTicketTier` / paid RSVP: keep FailedPrecondition, change message to the host-facing paid-tickets sentence. Log “razorpay disabled” at warn on the server.

Sweep other `status.Errorf(..., "%v", err)` on user-facing create/update paths (tiers, coupons) the same way.

### B5 — RSVP close

- Gateway `EventBody`: optional `rsvp_closes_at`. Recurrence body: optional `rsvp_close_hours_before` (0 = none).
- `CreateEvent` / `UpdateEvent` / `MaterializeSeries`: persist.
- `CreateRSVP`: enforce.
- Ended-event freeze: `rsvp_closes_at` is historical; do not require it to stay editable after end.

### B6 — RSVP all upcoming (free)

- Additive: `RSVPReq.scope` = `this` (default) \| `series`.
- REST: `POST /events/:slug/rsvp` body `{ ticket_tier_id, coupon_code?, scope? }`.
- Service: if `series` and paid tier → FailedPrecondition with the paid-series sentence.
- If `series` and free: load `series_id` from slug; insert/confirm attendee for each open future occurrence (same as today’s free RSVP loop, one TX). Return the same `RSVPResp` for the **clicked** slug; mention how many dates were saved in `message`.

### B7 — Speed (same PR if small)

- Drop or gate `db.log.Debugw("[DEBUG] list query", …)` so production lists are not stringifying SQL.
- Collapse (B2) is the big win (12 → 1 card, 12 fewer images).
- Attendee count: keep the subquery for now **or** replace with `events.attendee_count` only if that column is already maintained (do not add a cache column in this pass unless it already exists).
- Frontend: radius expand already stops at country; ensure it does not refetch after collapse returns virtual+one in-person. No `radius: 0`.
- `EventGridCard`: `loading="lazy"` already; give collapsed cards a real cover so we are not painting 11 empty heroes.

Do **not** add Redis for this list in this pass.

---

## Frontend

### F1 — `EventGridCard`

If `series_id` / `recurrence_text`: small “Repeats weekly” line (reuse `EventSeriesNote` logic, keep one component). If `upcoming_dates` length > 1, show the extra dates on one line. No second grid of clones.

### F2 — `EventsDiscover` / profile Events

No client-side unique-by-title hack. Trust B2. If an old gateway is deployed, client can de-dupe by `series_id` as a safety net (Map, keep earliest `start_time`).

### F3 — `EventForm`

- One-off: “Last day to RSVP” datetime, optional, `min` = now, `max` = start.
- Repeat ≠ Off: replace that with “Close RSVP” select: Off / 12 hours before / 1 day / 3 days / 1 week. Map to `recurrence.rsvp_close_hours_before`.
- Do not send an absolute `rsvp_closes_at` for the whole series.

### F4 — `RsvpPanel` / sticky bar

If `series_id` and next sibling exists and the selected tier is **free**: checkbox or two buttons — “This meetup” vs “All upcoming in this series”. Paid: only this meetup.

### F5 — `EventManage` coupons / tickets

Toasts already use `eventError`. Once B4 lands, copy is enough. Optionally disable **Add** if the code matches an existing chip (same as unique constraint).

### F6 — Create series + cover

Keep single upload after create; backend B3 copies. No 12× `uploadEventCover`.

---

## Out of scope

- Organizer-connected Razorpay accounts / split payouts.
- Paid “RSVP all dates” as one checkout.
- Per-date cover/venue exceptions UI.
- GetSeries calendar API beyond the 3 dates on the card.
- Changing protobuf field numbers on existing fields.

---

## Suggested build order

1. B4 human errors (coupon + payments copy) — smallest, unblocks hosts today.
2. B1 + B2 + F1 + F2 — series cards and speed.
3. B3 + F6 — covers on every date.
4. B5 + F3 — RSVP close.
5. B6 + F4 — RSVP all (free only).
6. Compose rebuild; confirm Popular shows **one** weekly card with cover; duplicate coupon toast is human; paid tier copy is human.

Approve this order, or say which slice to do first.
