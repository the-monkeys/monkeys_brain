# Discover tags, create pin, and geo UX — design spec

Date: 2026-09-07  
Branch: `feat/payments-admin-backend`  
Status: **approved to implement in the same chat** (user: make a plan and proceed; full FE/BE/DB/UI/perf check).  
Audience: public site `local/the_monkeys/apps/the_monkeys` plus events/groups engine. Not `/api/v1/admin/*`.

This spec is the source of truth for an implementer who has not seen the chat.

---

## 1. Product (locked)

Discover radius chips (10/25/50/100/Everywhere) and category chips (Networking, Tech & AI, Writing & Storytelling, Outdoor, Sports & Hobbies) already call the list APIs. Hosts still type free-text tags (`chai, meetup`) and skip the map pin, so those chips look broken and 10 vs 25 vs 50 look identical.

This work makes **create/edit produce the same tags and pins that discover filters on**, restores a saved pin on edit, fixes discover copy, and measures that list/filter UI does not sit on stale results for more than one second.

---

## 2. Current behavior (why this exists)

| Surface | Today |
| --- | --- |
| `/events` hero chips | Send `tags=networking\|tech\|writing\|outdoor\|sports`. Working. |
| Event create tags | Comma field. No shared slugs. Discover chips miss host events. |
| Group topics | Same: free-text, no chips. |
| PlacePin | “Use my location” only. Lab in-person rows often stay `latitude/longitude NULL`. |
| `GET` event JSON | `scanEvent` reads `e.latitude, e.longitude` then **drops them**. Edit form prefills from `event.venue` only → pin looks gone. |
| Event proto | No top-level `latitude`/`longitude` on `Event`. `Venue` already has them. **Do not run protoc.** Copy DB coords onto `Event.venue` when both are non-zero. |
| Empty discover copy | “No events around Bengaluru yet” even when Everywhere is pressed (`location` input still holds the city). |
| Communities heading | “Popular communities in Bengaluru” while groups request is nationwide. |
| More chip | No `onClick`. |
| Filter refetch | `keepPreviousData` leaves the old card list until the new fetch returns (often >1s). |
| Groups discover | Radius chips exist but **omit `city`** on near-me, so unpinned Bengaluru groups vanish under radius. |

---

## 3. Approaches considered

**Tags**

1. Shared chip list on create/edit (same slugs as discover), plus optional extra comma tags. **Chosen.** One vocabulary, still allows `chai`.
2. Discover-only aliases (`tech` matches `ai`, `javascript`). Rejected: hidden magic, extra backend.
3. Force a single required category. Rejected: existing events have none.

**Pin**

1. Keep geolocation only. Rejected: hosts skip it.
2. Leaflet map click. Better later; extra dependency this pass.
3. “Pin this address” that geocodes the Place/City field via a Next.js server route (Nominatim with User-Agent), plus existing “Use my location”. **Chosen.** No proto. Server Nominatim on save stays as fallback.

**Edit pin restore**

1. Add `Event.latitude` proto fields (user runs protoc). Correct long-term; blocked this pass.
2. Copy scanned event coords onto `pb.Event.Venue` lat/lng. **Chosen.** Form already reads `event.venue`.

---

## 4. Shared category vocabulary

Single frontend module `src/lib/eventCategories.ts`:

| Label | Tag slug |
| --- | --- |
| Networking | `networking` |
| Tech & AI | `tech` |
| Writing & Storytelling | `writing` |
| Outdoor | `outdoor` |
| Sports & Hobbies | `sports` |

Rules:

- Slugs are lowercase `[a-z0-9-]+`.
- Create/edit: multi-select chips. Extra comma tags append, de-duplicated, lowercased.
- Discover events: one chip at a time (existing toggle). **More** reveals a short text field that sets `tags` to that custom slug (same query param). Empty More field + chip off = no tag filter.
- Groups discover/create: same slugs go in `topics` (groups have no `tags` query).

Do not change backend tag matching. List still ANDs/filters on stored strings as today.

---

## 5. Pin UX

`PlacePin`:

- Existing: Use my location, Clear pin, show `lat, lng`.
- New: **Pin this address** — reads the nearest form `input[name=location]` or `input[name=city]` (or an `address` prop). Calls `GET /api/geocode?q=` (Next.js route, server-side Nominatim, 5s timeout, User-Agent `TheMonkeysApp/1.0`). On miss: “Could not pin that address. Try Use my location.”
- Virtual events: pin hidden (existing).
- Ended events: pin disabled (existing).
- Do not send `0,0`. Omit lat/lng when cleared (existing Nominatim-on-save path).

Engine: `scanEvent` attaches non-zero DB coords to `e.Venue` (create Venue stub if nil). GetEvent JSON then has `venue.latitude` / `venue.longitude`. EventForm `pinFromCoords(event?.venue?.latitude, event?.venue?.longitude)` starts working for pinned rows without a real venue row.

No migration. No backfill of NULL rows.

---

## 6. Discover copy, fetch, groups geo

- Empty events copy uses **displayLocation** (`everywhere` vs city), not the raw location input while nationwide.
- Communities heading: nationwide → “Popular communities everywhere”; else city heading as today.
- While `useEventList` / `useGroupList` is fetching a new key (not first load), overlay the grid (opacity + “Updating…”) so stale cards are not mistaken for the new filter for >1s.
- Groups near-me: send `city` (IP/city label) **with** `user_lat`/`user_lng`/`radius`, same as events, so unpinned city groups still match.

---

## 7. Out of scope

- `/api/v1/admin/*`, messaging, dues, recommendations, saved lists.
- Re-enabling paid ticket price on create (stays ₹0 until Razorpay is on).
- Event proto top-level lat/lng (needs user `protoc`).
- Leaflet map picker.
- Git commit unless the user asks.
- Signup `role_id = 4`. Migrations `000001`–`000019`.

---

## 8. Testing (required before calling this done)

Every claim needs evidence: HTTP, SQL, UI, or timing. “Looks fine” is a fail.

| Layer | What |
| --- | --- |
| Frontend unit | Category merge; geocode miss returns null; pinFromCoords still rejects 0,0. |
| Backend unit | `attachEventCoords` copies non-zero pair onto venue; zeros/NULL leave venue nil. |
| API | Create in-person + pin → 201; SQL `latitude/longitude` non-NULL. GET slug → `venue.latitude` matches. List `tags=writing` omits untagged. Groups list with pin+radius+city includes unpinned city group. |
| DB | No rewrite of unrelated NULL rows. |
| UI desktop + ~375px | Create chips, pin buttons, discover chips, groups chips, empty/nationwide copy. |
| UX | Filter change shows Updating… until new titles. No dead More chip. |
| Performance | Time `GET /api/v1/events?...` from the browser. Flag if **>1000ms** to JSON or if the previous card list stays interactive as if it were the new filter for **>1000ms** without an updating state. |

Do not claim production-GO for payments, staff admin, or authz. This spec is discover/create geo+tags UX plus a measured pass on those surfaces.

---

## 9. Files (expected)

Engine: `microservices/the_monkeys_events/internal/database/events.go`, new `scan_coords_test.go`.

Frontend: `src/lib/eventCategories.ts` (+ test), `src/app/api/geocode/route.ts`, `PlacePin.tsx`, `EventForm.tsx`, `GroupForm.tsx`, `GroupEventForm.tsx`, `EventsDiscover.tsx`, `GroupsPageClient.tsx`.
