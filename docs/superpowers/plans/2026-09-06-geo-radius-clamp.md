# Geo radius clamp and optional create pin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clamp event/group radius search to 2–100 km when a client already sends `radius > 0`, and let event create/update/series accept an optional map pin, without changing existing rows or nationwide lists.

**Architecture:** Shared `common/geo.ClampSearchRadius` is applied in the events and groups SQL builders (not only the gateway). `radius <= 0` still means no geo filter. Event writes prefer a client pin when both coordinates are non-zero; otherwise Nominatim; virtual events always store NULL coords. Clone copies source lat/lng.

**Tech Stack:** Go 1.26, existing Postgres bbox + Haversine, proto3 additive `double` fields, Gin JSON `EventBody`. No PostGIS, no migration.

**Spec:** `docs/superpowers/specs/2026-09-06-geo-radius-clamp-design.md`

## Global Constraints

- Existing events, groups, and users are not rewritten. No geocode backfill.
- Do not 400 when Nominatim fails. Do not default server radius to 25 km.
- `radius` omitted, `0`, or negative → no geo filter (today). Clamp only when `radius > 0`.
- Do not mutate `req.Radius` when applying the clamp (keep `location` ILIKE on `Radius == 0`).
- Units are kilometres. Meetup-parity doc 40/250 km does **not** apply.
- Signup `role_id = 4` unchanged. Do not rewrite migrations `000001`–`000019`. No new migration.
- User runs `protoc` in WSL. Agents must not run protoc or hand-edit `*.pb.go`.
- Do not git commit unless the user explicitly asks. Skip every commit step until then.
- TDD: failing test before production code for each task. No live Nominatim in CI.

### File map

| File | Responsibility |
| --- | --- |
| `common/geo/radius.go` | `ClampSearchRadius`, `UseClientPin`, min/max constants |
| `microservices/the_monkeys_events/internal/database/events.go` | List clamp in `commonFilters`; write coords; clone copy |
| `microservices/the_monkeys_events/internal/database/recurring.go` | Series pin-or-geocode |
| `microservices/the_monkeys_groups/internal/database/search.go` | List clamp in `groupListFilter` |
| `microservices/the_monkeys_groups/internal/database/geocoding.go` | Keep `coordsFromPlace`; call `UseClientPin` |
| `apis/serviceconn/gateway_event/pb/gw_event.proto` | Additive lat/lng on create/update/series |
| `microservices/the_monkeys_gateway/internal/events/models.go` | Optional JSON `latitude`/`longitude` |
| `microservices/the_monkeys_gateway/internal/events/handler.go` | Pass pin into create/update/series RPCs |
| `docs/context.md` | Geo bullet matches this spec |

---

### Task 1: `common/geo` clamp and pin helpers

**Files:**
- Create: `common/geo/radius.go`
- Test: `common/geo/radius_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `const MinSearchRadiusKm int32 = 2`
  - `const MaxSearchRadiusKm int32 = 100`
  - `func ClampSearchRadius(radiusKm int32) (clamped int32, apply bool)`
  - `func UseClientPin(lat, lng float64) bool`

- [ ] **Step 1: Write the failing tests**

```go
package geo

import "testing"

func TestClampSearchRadius(t *testing.T) {
	tests := []struct {
		in     int32
		want   int32
		apply  bool
	}{
		{0, 0, false},
		{-5, 0, false},
		{1, 2, true},
		{2, 2, true},
		{25, 25, true},
		{100, 100, true},
		{250, 100, true},
	}
	for _, tc := range tests {
		got, apply := ClampSearchRadius(tc.in)
		if apply != tc.apply || got != tc.want {
			t.Fatalf("ClampSearchRadius(%d) = (%d, %v), want (%d, %v)",
				tc.in, got, apply, tc.want, tc.apply)
		}
	}
}

func TestUseClientPin(t *testing.T) {
	if UseClientPin(0, 0) || UseClientPin(12.97, 0) || UseClientPin(0, 77.59) {
		t.Fatal("zero on either axis is not a pin")
	}
	if !UseClientPin(12.97, 77.59) {
		t.Fatal("both non-zero is a pin")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./common/geo/ -count=1
```

Expected: FAIL, package or symbols not found.

- [ ] **Step 3: Write minimal implementation**

```go
package geo

const (
	MinSearchRadiusKm int32 = 2
	MaxSearchRadiusKm int32 = 100
)

// ClampSearchRadius returns (0, false) when radiusKm <= 0 (no geo filter).
// Otherwise it clamps to [MinSearchRadiusKm, MaxSearchRadiusKm].
func ClampSearchRadius(radiusKm int32) (clamped int32, apply bool) {
	if radiusKm <= 0 {
		return 0, false
	}
	if radiusKm < MinSearchRadiusKm {
		return MinSearchRadiusKm, true
	}
	if radiusKm > MaxSearchRadiusKm {
		return MaxSearchRadiusKm, true
	}
	return radiusKm, true
}

// UseClientPin is true when both coordinates are a real point (not 0,0 / partial).
func UseClientPin(lat, lng float64) bool {
	return lat != 0 && lng != 0
}
```

- [ ] **Step 4: Run tests — expect PASS**

```
go test ./common/geo/ -count=1
```

- [ ] **Step 5: Commit** (skip unless the user asks)

```
git add common/geo/radius.go common/geo/radius_test.go
git commit -m "feat(geo): clamp search radius to 2-100 km"
```

---

### Task 2: Events list uses clamp (do not default 25)

**Files:**
- Modify: `microservices/the_monkeys_events/internal/database/events.go` (`commonFilters`, the `req.UserLat != 0 && req.UserLng != 0 && req.Radius > 0` block around lines 691–719)
- Test: `microservices/the_monkeys_events/internal/database/list_sql_test.go`

**Interfaces:**
- Consumes: `geo.ClampSearchRadius(int32) (int32, bool)`
- Produces: geo SQL only when pin is present **and** clamp `apply == true`; Haversine/box use the **clamped** value, not raw `req.Radius`

- [ ] **Step 1: Write the failing tests** in `list_sql_test.go` (keep the existing radius-25 bbox test)

```go
func TestCommonFiltersNoGeoWhenRadiusZero(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat:   12.97,
		UserLng:   77.59,
		Radius:    0,
		EventType: EventTypeInPerson,
	})
	where := f.where()
	if strings.Contains(where, "latitude IS NOT NULL") || strings.Contains(where, "acos") {
		t.Fatalf("radius 0 must not geo-filter, got %s", where)
	}
}

func TestCommonFiltersNoGeoWithoutPin(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{Radius: 40, EventType: EventTypeInPerson})
	if strings.Contains(f.where(), "acos") {
		t.Fatal("radius without pin must not geo-filter")
	}
}

func TestCommonFiltersClampsRadiusOneToTwo(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 1, EventType: EventTypeInPerson,
	})
	if !strings.Contains(f.where(), "acos") {
		t.Fatal("radius 1 with pin must still geo-filter")
	}
	radius := geoRadiusBound(f)
	if radius != 2 {
		t.Fatalf("radius 1 must clamp to 2, bound %v", radius)
	}
}

func TestCommonFiltersClampsRadius250To100(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 250, EventType: EventTypeInPerson,
	})
	if geoRadiusBound(f) != 100 {
		t.Fatalf("radius 250 must clamp to 100, bound %v", geoRadiusBound(f))
	}
	span := geoLatSpan(f)
	want := 2 * (100.0 / 111.0)
	if math.Abs(span-want) > 1e-9 {
		t.Fatalf("geoBox must use clamped 100 km, span %v want %v", span, want)
	}
}

func TestCommonFiltersLocationIlikeWhenRadiusZero(t *testing.T) {
	f := &filter{}
	commonFilters(f, &pb.ListEventsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 0, Location: "Bengaluru",
	})
	if !strings.Contains(f.where(), "e.location ILIKE") {
		t.Fatalf("radius 0 must keep location ILIKE, got %s", f.where())
	}
}

func geoRadiusBound(f *filter) int32 {
	// last 8 args: lat, lng, lat, radius, minLat, maxLat, minLng, maxLng
	if len(f.args) < 8 {
		return 0
	}
	switch v := f.args[len(f.args)-5].(type) {
	case int32:
		return v
	default:
		return 0
	}
}

func geoLatSpan(f *filter) float64 {
	minLat, _ := f.args[len(f.args)-4].(float64)
	maxLat, _ := f.args[len(f.args)-3].(float64)
	return maxLat - minLat
}
```

`geoRadiusBound` indexes: append order is `UserLat, UserLng, UserLat, clampedRadius, minLat, maxLat, minLng, maxLng`. From the end: `-1` maxLng, `-2` minLng, `-3` maxLat, `-4` minLat, `-5` radius. If `int32` assertion fails in the test, print `f.args` and fix the helper — do not change production arg order.

- [ ] **Step 2: Run tests — expect FAIL** on clamp (radius 1 still binds 1; 250 still binds 250)

```
go test ./microservices/the_monkeys_events/internal/database/ -count=1 -run "TestCommonFilters"
```

- [ ] **Step 3: Implement** — replace the geo-entry condition so it does **not** use raw `req.Radius > 0` as the only gate. Import `github.com/the-monkeys/the_monkeys/common/geo`.

Replace the block that starts `if req.UserLat != 0 && req.UserLng != 0 && req.Radius > 0 &&` with:

```go
	radiusKm, applyRadius := geo.ClampSearchRadius(req.Radius)
	if req.UserLat != 0 && req.UserLng != 0 && applyRadius &&
		req.EventType != EventTypeOnline && req.EventType != EventTypeHybrid {
		minLat, maxLat, minLng, maxLng := geoBox(req.UserLat, req.UserLng, radiusKm)
		f.args = append(f.args, req.UserLat, req.UserLng, req.UserLat, radiusKm,
			minLat, maxLat, minLng, maxLng)
		// ... keep the existing placeholder index math and inRange SQL unchanged ...
	}
```

Do **not** assign `req.Radius = radiusKm`. Leave `if req.Location != "" && req.Radius == 0` as-is.

Leave `geo.go` / `geoBox` in this package. Do not change `listOrderBy` (`sort=nearest` without a pin already falls through to `start_time`).

- [ ] **Step 4: Run tests — expect PASS**

```
go test ./microservices/the_monkeys_events/internal/database/ -count=1
```

- [ ] **Step 5: Commit** (skip unless the user asks)

---

### Task 3: Groups list uses the same clamp

**Files:**
- Modify: `microservices/the_monkeys_groups/internal/database/search.go` (`groupListFilter` geo block around lines 110–127)
- Modify: `microservices/the_monkeys_groups/internal/database/geocoding.go` (`coordsFromPlace` — use `geo.UseClientPin`)
- Test: `microservices/the_monkeys_groups/internal/database/search_sql_test.go`

**Interfaces:**
- Consumes: `geo.ClampSearchRadius`, `geo.UseClientPin`
- Produces: same list semantics as events

- [ ] **Step 1: Write the failing tests**

```go
func TestGroupListFilterNoGeoWhenRadiusZero(t *testing.T) {
	_, where := groupListFilter(&pb.ListGroupsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 0,
	})
	if strings.Contains(where, "latitude IS NOT NULL") || strings.Contains(where, "acos") {
		t.Fatalf("radius 0 must not geo-filter, got %s", where)
	}
}

func TestGroupListFilterClampsRadius250To100(t *testing.T) {
	args, where := groupListFilter(&pb.ListGroupsReq{
		UserLat: 12.97, UserLng: 77.59, Radius: 250,
	})
	if !strings.Contains(where, "acos") {
		t.Fatal("pin + radius 250 must geo-filter")
	}
	if len(args) < 8 {
		t.Fatalf("expected geo args, got %d", len(args))
	}
	radius, ok := args[len(args)-5].(int32)
	if !ok || radius != 100 {
		t.Fatalf("clamped radius want 100, got %#v", args[len(args)-5])
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

```
go test ./microservices/the_monkeys_groups/internal/database/ -count=1 -run "TestGroupList"
```

- [ ] **Step 3: Implement** in `groupListFilter`:

```go
	radiusKm, applyRadius := geo.ClampSearchRadius(req.Radius)
	if req.UserLat != 0 && req.UserLng != 0 && applyRadius {
		minLat, maxLat, minLng, maxLng := geoBox(req.UserLat, req.UserLng, radiusKm)
		args = append(args, req.UserLat, req.UserLng, req.UserLat, radiusKm,
			minLat, maxLat, minLng, maxLng)
		// existing SQL unchanged
	}
```

In `coordsFromPlace`, replace `if lat != 0 && lng != 0` with `if geo.UseClientPin(lat, lng)`. Behavior is identical.

- [ ] **Step 4: Run tests — expect PASS**

```
go test ./microservices/the_monkeys_groups/internal/database/ -count=1
```

- [ ] **Step 5: Commit** (skip unless the user asks)

---

### Task 4: Additive proto fields (stop for `protoc`)

**Files:**
- Modify: `apis/serviceconn/gateway_event/pb/gw_event.proto` only
- Do **not** edit `*.pb.go`

**Interfaces:**
- Consumes: nothing
- Produces: `CreateEventReq.latitude = 25`, `longitude = 26`; `UpdateEventReq.latitude = 24`, `longitude = 25`; `CreateSeriesReq.latitude = 18`, `longitude = 19`

- [ ] **Step 1: Add fields** (comments only, next unused numbers)

On `CreateEventReq` after `repeated EventQuestion questions = 24;`:

```protobuf
    double latitude = 25;
    double longitude = 26;
```

On `UpdateEventReq` after `google.protobuf.Int32Value rsvp_close_hours_before = 23;`:

```protobuf
    double latitude = 24;
    double longitude = 25;
```

On `CreateSeriesReq` after `ClientInfo client_info = 17;`:

```protobuf
    double latitude = 18;
    double longitude = 19;
```

- [ ] **Step 2: STOP.** Ask the user to run `protoc` in WSL. Do not compile gateway/events code that reads these fields until `gw_event.pb.go` contains `Latitude` / `Longitude` on those three messages.

- [ ] **Step 3: Commit proto** (skip unless the user asks; include generated `*.pb.go` only after the user generated them)

---

### Task 5: Event write path — pin, virtual NULL, clone copy

Start this task only after Task 4 generated code exists.

**Files:**
- Modify: `microservices/the_monkeys_events/internal/database/events.go` (`CreateEvent` ~178, `UpdateEvent` ~296, `CloneEvent` ~450–481, add `resolveEventCoords` next to `nullCoord`)
- Modify: `microservices/the_monkeys_events/internal/database/recurring.go` (`CreateSeries` ~386)
- Test: `microservices/the_monkeys_events/internal/database/coords_test.go` (new)

**Interfaces:**
- Consumes: `geo.UseClientPin`, `Geocode`, `nullCoord`
- Produces: `func resolveEventCoords(ctx context.Context, eventType string, pinLat, pinLng float64, location string) (float64, float64)`

- [ ] **Step 1: Write the failing tests** (no Nominatim)

```go
package database

import (
	"context"
	"testing"
)

func TestResolveEventCoordsOnlineDropsPin(t *testing.T) {
	lat, lng := resolveEventCoords(context.Background(), EventTypeOnline, 12.97, 77.59, "ignored")
	if lat != 0 || lng != 0 {
		t.Fatalf("virtual must persist NULL coords, got %v,%v", lat, lng)
	}
}

func TestResolveEventCoordsUsesPin(t *testing.T) {
	lat, lng := resolveEventCoords(context.Background(), EventTypeInPerson, 12.97, 77.59, "should-not-geocode")
	if lat != 12.97 || lng != 77.59 {
		t.Fatalf("in-person pin must win, got %v,%v", lat, lng)
	}
}

func TestResolveEventCoordsPartialPinFallsThrough(t *testing.T) {
	// Empty location: Geocode returns 0,0 without a network call.
	lat, lng := resolveEventCoords(context.Background(), EventTypeHybrid, 12.97, 0, "  ")
	if lat != 0 || lng != 0 {
		t.Fatalf("partial pin must geocode (empty → 0,0), got %v,%v", lat, lng)
	}
}

func TestNullCoordZeroIsNil(t *testing.T) {
	if nullCoord(0) != nil {
		t.Fatal("0 must become SQL NULL")
	}
	if nullCoord(12.97) == nil {
		t.Fatal("real lat must be stored")
	}
}
```

`TestGeocodeEmptyLocation` already proves empty location is `(0,0)` without hitting Nominatim.

- [ ] **Step 2: Run tests — expect FAIL** (`resolveEventCoords` undefined)

```
go test ./microservices/the_monkeys_events/internal/database/ -count=1 -run "TestResolveEventCoords|TestNullCoord"
```

- [ ] **Step 3: Implement `resolveEventCoords`** next to `nullCoord` in `events.go`:

```go
func resolveEventCoords(ctx context.Context, eventType string, pinLat, pinLng float64, location string) (float64, float64) {
	if eventType == EventTypeOnline {
		return 0, 0
	}
	if geo.UseClientPin(pinLat, pinLng) {
		return pinLat, pinLng
	}
	return Geocode(ctx, location)
}
```

CreateEvent: replace `lat, lng := Geocode(ctx, req.Location)` with:

```go
		lat, lng := resolveEventCoords(ctx, req.EventType, req.Latitude, req.Longitude, req.Location)
```

UpdateEvent: same with `req.EventType`, `req.Latitude`, `req.Longitude`, `req.Location`.

CreateSeries in `recurring.go`: replace `lat, lng := Geocode(ctx, req.Location)` with `resolveEventCoords(ctx, req.EventType, req.Latitude, req.Longitude, req.Location)`.

CloneEvent: extend the `SELECT` to include `latitude, longitude`. Scan into `sql.NullFloat64`. **Delete** `lat, lng := Geocode(ctx, loc)`. Pass:

```go
		var plat, plng float64
		if srcLat.Valid {
			plat = srcLat.Float64
		}
		if srcLng.Valid {
			plng = srcLng.Float64
		}
		// insert still uses nullCoord(plat), nullCoord(plng)
```

Do not geocode on clone. Do not UPDATE any other rows.

- [ ] **Step 4: Run tests — expect PASS**

```
go test ./microservices/the_monkeys_events/internal/database/ -count=1
```

- [ ] **Step 5: Commit** (skip unless the user asks)

---

### Task 6: Gateway JSON pin fields

**Files:**
- Modify: `microservices/the_monkeys_gateway/internal/events/models.go` (`EventBody`)
- Modify: `microservices/the_monkeys_gateway/internal/events/handler.go` (`CreateEvent`, `CreateSeries`, `UpdateEvent` proto literals)

**Interfaces:**
- Consumes: generated `pb.CreateEventReq.Latitude` / `Longitude` (and update/series)
- Produces: HTTP JSON `latitude` / `longitude` optional; omitted → 0,0 → service treats as no pin

- [ ] **Step 1: Write the failing test** in new file `microservices/the_monkeys_gateway/internal/events/models_test.go`:

```go
package events

import (
	"encoding/json"
	"testing"
)

func TestEventBodyPinOptional(t *testing.T) {
	var body EventBody
	if err := json.Unmarshal([]byte(`{"title":"t","start_time":"2026-09-06T10:00:00Z","end_time":"2026-09-06T11:00:00Z","event_type":"in_person"}`), &body); err != nil {
		t.Fatal(err)
	}
	if body.Latitude != 0 || body.Longitude != 0 {
		t.Fatal("omitted pin must stay 0")
	}
	if err := json.Unmarshal([]byte(`{"title":"t","start_time":"2026-09-06T10:00:00Z","end_time":"2026-09-06T11:00:00Z","event_type":"in_person","latitude":12.97,"longitude":77.59}`), &body); err != nil {
		t.Fatal(err)
	}
	if body.Latitude != 12.97 || body.Longitude != 77.59 {
		t.Fatalf("pin must bind, got %v,%v", body.Latitude, body.Longitude)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (no `Latitude` field)

```
go test ./microservices/the_monkeys_gateway/internal/events/ -count=1 -run TestEventBodyPinOptional
```

- [ ] **Step 3: Add to `EventBody`:**

```go
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
```

No `binding:"required"`. Pass `Latitude: body.Latitude, Longitude: body.Longitude` into `CreateEventReq`, `CreateSeriesReq`, and `UpdateEventReq`.

Do not change `listQuery` radius default (`DefaultQuery("radius", "0")`).

- [ ] **Step 4: Run tests — expect PASS**

```
go test ./microservices/the_monkeys_gateway/internal/events/ -count=1
```

- [ ] **Step 5: Commit** (skip unless the user asks)

---

### Task 7: Context doc

**Files:**
- Modify: `docs/context.md` (Geo discovery bullets ~109–113)

**Interfaces:** none

- [ ] **Step 1: Replace the geo discovery bullets** with:

```markdown
### Geo discovery

- Events and groups have lat/lng. Failed Nominatim → NULL coords (`nullCoord`). Optional client pin on event create/update/series skips Nominatim when both values are non-zero. Virtual events always store NULL coords. Clone copies source coordinates (no re-geocode).
- `ListEvents` / `ListGroups` Haversine radius. Filter runs only with a pin **and** `radius > 0`, then clamped to **[2, 100] km**. `radius` 0/omitted = no geo filter (nationwide). Engine does **not** default 25 km; that is a frontend default. Virtual/hybrid events skip the radius predicate. NULL-coord rows are omitted from radius results only.
- Do not backfill existing NULL coordinates. No PostGIS.
```

- [ ] **Step 2: Commit** (skip unless the user asks)

---

## Verification (before claiming done)

```
go test ./common/geo/ ./microservices/the_monkeys_events/internal/database/ ./microservices/the_monkeys_groups/internal/database/ ./microservices/the_monkeys_gateway/internal/events/ -count=1
```

Expected: PASS. No Docker/migration required. No existing SQL rows updated.

Manual (optional, after `docker compose up`): `GET /api/v1/events?user_lat=12.97&user_lng=77.59` with no radius still returns events that lack coordinates; adding `radius=250` must behave like 100 km and still hide NULL-coord rows.
