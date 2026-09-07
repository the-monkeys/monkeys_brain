# Geo radius clamp and optional create pin — design spec

Date: 2026-09-06  
Branch: `feat/payments-admin-backend`  
Status: **design approved in chat. Do not implement until this spec is accepted as written.**  
Scope: backend only (events + groups list and write). No UI. No PostGIS. No migration.

This spec is the source of truth for an implementer who has not seen the chat.

`docs/meetup-parity-implementation-plan.md` guessed default 40 km / max 250 km. **Those numbers do not apply.** This spec is 2 / 25 / 100 km, with 25 as a **frontend** default only.

---

## 1. Product (locked)

Harden the existing Meetup-style radius search on **events and groups**. Keep the current URLs. Keep bbox + Haversine in Postgres. Do not add PostGIS.

**Units:** kilometres only.

**Existing events, groups, and users must not change.** No backfill, no rewrite of NULL coords, no user/profile/geo-origin tables, no IP or GPS fallback.

---

## 2. Current behavior (why this exists)

| Surface | Today |
| --- | --- |
| `GET /api/v1/events` and `GET /api/v1/groups` | Query `user_lat`, `user_lng`, `radius`. Gateway `DefaultQuery("radius", "0")`. Geo SQL runs only if pin is non-zero **and** `radius > 0`. |
| Geo SQL | Bounding box (`geoBox`) then Haversine (`6371 * acos(...)`). Requires `latitude IS NOT NULL AND longitude IS NOT NULL`. |
| Online / hybrid events | Skip the radius predicate (reachable from anywhere). |
| Default radius 0 | **No geo filter** even if lat/lng are sent. Nationwide + other filters. |
| Event create/update | Nominatim on `location` string. `(0,0)` → SQL NULL via `nullCoord`. Failed Indian addresses often leave NULL. |
| Group create/update | `coordsFromPlace`: client lat/lng if both non-zero, else Nominatim on city/region/country. |
| `CreateEventReq` / `UpdateEventReq` / `CreateSeriesReq` | **No** latitude/longitude fields. Pin cannot be sent for events. |
| Rows with NULL coords | Omitted from any radius search. Still appear on unfiltered lists and `GET /api/v1/events/user/:username`. |

That last row is why a host’s own in-person event can vanish from “near me”: Nominatim missed, coords are NULL, radius SQL drops the row.

---

## 3. Backward compatibility (non-negotiable)

1. **Do not UPDATE or DELETE existing event, group, or user rows** as part of this work. No geocode backfill job.
2. **Do not 400** in-person create/update when Nominatim fails. Save anyway (today’s contract).
3. **Do not change list results** for clients that omit `radius` or send `0`. That remains “no geo filter.”
4. **Do not default server radius to 25 km.** The app may send `radius=25` when the user turns on near-me. The engine must not invent that.
5. Signup, `role_id`, JWT, and user tables are out of scope.
6. Proto additions are **additive field numbers**. Old clients that omit them keep working (`double` defaults to 0 → treat as no pin).

The only list behavior change: when a client **already** sends `radius > 0`, clamp that value to **[2, 100] km**. Values `1` become `2`; values `> 100` become `100`. Negative and `0` still mean no geo filter.

---

## 4. List API

Same routes: `GET /api/v1/events`, `GET /api/v1/groups`. No `/discover`.

| Client sends | Server does |
| --- | --- |
| No pin (`user_lat`/`user_lng` missing, unparsable, or `0,0`) | No geo filter. `radius` ignored. |
| Pin + `radius` omitted or `0` or negative | No geo filter (today). |
| Pin + `radius` > 0 | Clamp to **[2, 100] km**, then existing bbox + Haversine. NULL-coord rows stay out. |
| `sort=nearest` without a pin | Ignore nearest; keep existing sort. |
| `sort=nearest` with a pin | Existing nearest sort (NULL coords sort last, as groups already do). |

Online/hybrid events still skip the radius predicate. Groups have no online type; radius applies to rows with coords only.

Gateway parse stays as today: invalid numbers are ignored, **not 400**.

Legacy `location` ILIKE when `radius == 0` stays.

---

## 5. Create / update

**Events (new proto fields):** add `double latitude` and `double longitude` to `CreateEventReq`, `UpdateEventReq`, and `CreateSeriesReq` (next unused field numbers). Gateway JSON `EventBody` mirrors them as optional `latitude` / `longitude`. User runs `protoc`. Agents do not edit `*.pb.go`.

**Resolve coords (events and groups, same rule):**

1. If both lat and lng are a real point (neither is 0): **use the pin**. Skip Nominatim.
2. Else: Nominatim (events: `location`; groups: city/region/country as today). `(0,0)` → SQL NULL.
3. Never 400 on geocode miss or missing pin.
4. One of lat/lng only, or `0,0`: treat as omitted, fall through to Nominatim.

**Online / virtual events:** do not persist a pin. Coords stay NULL even if the client sends lat/lng. Hybrid and in-person use the pin-or-Nominatim rule above.

**Groups:** already accept lat/lng via `coordsFromPlace`. Keep that write path; list uses the shared clamp.

**Clone:** copy the source row’s `latitude`/`longitude` (including NULL). Do not re-geocode on clone.

No rewrite of existing NULL rows when someone else lists events.

---

## 6. Architecture

### 6.1 Shared clamp

New package `common/geo` (events and groups already duplicate `geoBox`):

```go
const (
    MinSearchRadiusKm int32 = 2
    MaxSearchRadiusKm int32 = 100
)

// ClampSearchRadius returns (0, false) when radius <= 0 (no geo filter).
// Otherwise it clamps to [MinSearchRadiusKm, MaxSearchRadiusKm] and returns (clamped, true).
func ClampSearchRadius(radiusKm int32) (clamped int32, apply bool)
```

Call this in **events and groups list SQL builders** before `geoBox` / Haversine. Do not clamp only in the gateway — gRPC must not skip the cap.

Leave `geoBox` where it is in each service. Do not change the Haversine formula.

### 6.2 Pin helper

```go
func UseClientPin(lat, lng float64) bool // both non-zero
```

Events create/update/series use pin if `UseClientPin`, else `Geocode`. Groups can keep `coordsFromPlace` if it already matches.

### 6.3 No new tables / indexes

`events.latitude` / `longitude` and `groups.latitude` / `longitude` already exist (btree from `000014` / `000011`). No migration.

---

## 7. Errors

| Case | Response |
| --- | --- |
| List missing pin or radius 0 | 200, unfiltered geo (other filters still apply) |
| List radius 1 with pin | 200, treated as 2 km |
| List radius 250 with pin | 200, treated as 100 km |
| Unparsable query coords/radius | Ignore field (today), not 400 |
| Create/update Nominatim fail | 201/200, NULL coords |
| Create/update invalid pin | Nominatim fallback, not 400 |

---

## 8. Tests (no live Nominatim)

- `ClampSearchRadius` table: `0` and negative → `apply=false`; `1` → `2`; `25` → `25`; `100` → `100`; `250` → `100`.
- List SQL / filter construction (unit): pin + radius 0 → no `IS NOT NULL` geo predicate; pin + radius 40 → predicate with 40; no pin → no predicate; radius 1 → 2 in the predicate.
- Create: client pin stored; omitted pin + geocode `(0,0)` → NULL insert args; still succeeds.
- Groups list uses the same clamp.

Do not call Nominatim in CI. Stub `Geocode` or test the pin/null helpers in isolation.

---

## 9. Out of scope

- PostGIS, GiST, `earthdistance`
- IP geolocation, browser GPS as a **server** origin, `user_addresses` / home city
- Backfill of existing NULL coordinates
- Miles
- New list path `/discover`
- Server-side default 25 km
- 400 on in-person create without a point
- Copying `venues.latitude`/`longitude` onto the event (pin on the request is enough)
- Frontend `geoSearch.ts` (document the 25 km default for the app; do not change that repo here)

---

## 10. Frontend contract (this repo does not implement it)

When the user enables near-me, the app should send `user_lat`, `user_lng`, and `radius` in **2–100**, typical default **25**. Omitting `radius` or sending `0` still means nationwide on the engine (backward compatible). Unmapped venues will not appear in that radius query until they have coordinates (pin on a later edit).

---

## 11. Files (expected)

| Area | Files |
| --- | --- |
| Clamp | `common/geo/` (new) |
| Events list + write | `microservices/the_monkeys_events/internal/database/events.go`, `recurring.go`, existing `geo.go` (thin wrapper or delete after move) |
| Groups list + write | `microservices/the_monkeys_groups/internal/database/search.go`, `groups.go`, `geo.go` |
| Proto | `apis/serviceconn/gateway_event/pb/gw_event.proto` only for event pin fields. Groups already have lat/lng. |
| Gateway | `microservices/the_monkeys_gateway/internal/events/handler.go`, `models.go`; groups handler only if list clamp is duplicated there (prefer services) |
| Docs | `docs/context.md` geo bullet: clamp [2,100], radius 0 = no filter, 25 km is UI |

Do not rewrite migrations `000001`–`000019`.
