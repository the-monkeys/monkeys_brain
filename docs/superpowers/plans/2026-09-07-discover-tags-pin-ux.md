# Discover tags, create pin, and geo UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (inline this session). Do **not** git commit unless the user asks. REQUIRED: superpowers:test-driven-development on every code task. REQUIRED: superpowers:verification-before-completion before claiming done. Frontend lives in `local/the_monkeys/apps/the_monkeys/` (nested git repo; engine gitignores it).

**Goal:** Hosts pick the same category slugs discover filters on, can pin a place without GPS-only, edit restores the saved pin, discover copy/fetch/groups near-me match that data, and list/filter is timed so stale UI cannot sit unmarked for more than 1s.

**Architecture:** Keep list URLs and tag matching. Share one frontend category list. Copy event table coords onto `Event.venue` in `scanEvent` (no protoc). Geocode “pin this address” through a Next.js server route. Overlay fetching grids.

**Tech Stack:** Go events service, Next.js 14, Vitest, TanStack Query, Nominatim (server-side only).

## Global Constraints

- Public site only. Do not call `/api/v1/admin/*`.
- Do not run `protoc` or edit `*.pb.go`.
- Do not rewrite migrations `000001`–`000019`. Signup `role_id = 4` unchanged.
- Engine does not default radius 25. UI default 25. `radius` 0/omit = nationwide.
- Never send `0,0` as a pin. Virtual events omit pin.
- Errors: `{ "error": "<message>" }` in toasts. No gRPC codes in UI.
- Do not git commit unless the user asks.
- Do not claim production-GO for payments/staff/authz.

---

### Task 1: Shared category vocabulary

**Files:**
- Create: `local/the_monkeys/apps/the_monkeys/src/lib/eventCategories.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/lib/eventCategories.test.ts`

**Interfaces:**
- Produces: `EVENT_CATEGORIES: { label: string; tag: string }[]`, `mergeCategoryTags(selected: string[], extra: string[]): string[]`

- [ ] **Step 1: Write the failing test**

```ts
import { EVENT_CATEGORIES, mergeCategoryTags } from './eventCategories';
import { describe, expect, it } from 'vitest';

describe('EVENT_CATEGORIES', () => {
  it('uses the discover slugs', () => {
    expect(EVENT_CATEGORIES.map((c) => c.tag)).toEqual([
      'networking',
      'tech',
      'writing',
      'outdoor',
      'sports',
    ]);
  });
});

describe('mergeCategoryTags', () => {
  it('dedupes, lowercases, and keeps extras', () => {
    expect(mergeCategoryTags(['Writing', 'tech'], ['chai', 'tech', '  '])).toEqual([
      'writing',
      'tech',
      'chai',
    ]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run from `local/the_monkeys/apps/the_monkeys`: `pnpm exec vitest run src/lib/eventCategories.test.ts`

Expected: FAIL (module missing).

- [ ] **Step 3: Write minimal implementation**

```ts
export const EVENT_CATEGORIES: { label: string; tag: string }[] = [
  { label: 'Networking', tag: 'networking' },
  { label: 'Tech & AI', tag: 'tech' },
  { label: 'Writing & Storytelling', tag: 'writing' },
  { label: 'Outdoor', tag: 'outdoor' },
  { label: 'Sports & Hobbies', tag: 'sports' },
];

export function mergeCategoryTags(selected: string[], extra: string[]): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const raw of [...selected, ...extra]) {
    const tag = raw.trim().toLowerCase();
    if (!tag || seen.has(tag)) continue;
    seen.add(tag);
    out.push(tag);
  }
  return out;
}
```

- [ ] **Step 4: Run test to verify it passes**

Same vitest command. Expected: PASS.

- [ ] **Step 5: Skip commit** (user did not ask).

---

### Task 2: GET event restores pin via venue coords

**Files:**
- Create: `microservices/the_monkeys_events/internal/database/scan_coords_test.go`
- Modify: `microservices/the_monkeys_events/internal/database/events.go` (`scanEvent`)

**Interfaces:**
- Produces: `attachEventCoords(e *pb.Event, lat, lng sql.NullFloat64)` called from `scanEvent` after Scan.

- [ ] **Step 1: Write the failing test**

```go
package database

import (
	"database/sql"
	"testing"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
)

func TestAttachEventCoordsCopiesNonZeroPairOntoVenue(t *testing.T) {
	e := &pb.Event{}
	attachEventCoords(e, sql.NullFloat64{Float64: 12.97, Valid: true}, sql.NullFloat64{Float64: 77.59, Valid: true})
	if e.Venue == nil || e.Venue.Latitude != 12.97 || e.Venue.Longitude != 77.59 {
		t.Fatalf("venue pin = %+v", e.Venue)
	}
}

func TestAttachEventCoordsSkipsNullOrZero(t *testing.T) {
	e := &pb.Event{}
	attachEventCoords(e, sql.NullFloat64{}, sql.NullFloat64{})
	if e.Venue != nil {
		t.Fatalf("nil coords must not create a venue, got %+v", e.Venue)
	}
	attachEventCoords(e, sql.NullFloat64{Float64: 0, Valid: true}, sql.NullFloat64{Float64: 0, Valid: true})
	if e.Venue != nil {
		t.Fatalf("0,0 is not a pin, got %+v", e.Venue)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

`go test ./microservices/the_monkeys_events/internal/database/ -run TestAttachEventCoords -count=1`

Expected: FAIL (`attachEventCoords` undefined).

- [ ] **Step 3: Write minimal implementation**

Add next to `scanEvent`:

```go
func attachEventCoords(e *pb.Event, lat, lng sql.NullFloat64) {
	if e == nil || !lat.Valid || !lng.Valid || lat.Float64 == 0 || lng.Float64 == 0 {
		return
	}
	if e.Venue == nil {
		e.Venue = &pb.Venue{}
	}
	e.Venue.Latitude = lat.Float64
	e.Venue.Longitude = lng.Float64
}
```

Call `attachEventCoords(&e, lat, lng)` at the end of `scanEvent` before `return &e, nil`.

- [ ] **Step 4: Run test to verify it passes**

Same `go test` command. Expected: PASS.

Rebuild events image if docker compose is how local gateway talks to events (`the_monkeys_events`).

- [ ] **Step 5: Skip commit**.

---

### Task 3: Geocode route + Pin this address

**Files:**
- Create: `local/the_monkeys/apps/the_monkeys/src/app/api/geocode/route.ts`
- Modify: `local/the_monkeys/apps/the_monkeys/src/lib/geoSearch.ts` (add `geocodeAddress`)
- Modify: `local/the_monkeys/apps/the_monkeys/src/lib/geoSearch.test.ts`
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/geo/PlacePin.tsx`

**Interfaces:**
- Produces: `geocodeAddress(query: string): Promise<GeoPin | null>`
- PlacePin: new button **Pin this address**; reads `address` prop or `form input[name=location|city]`.

- [ ] **Step 1: Failing test for empty query**

In `geoSearch.test.ts`:

```ts
it('geocodeAddress returns null for blank query', async () => {
  expect(await geocodeAddress('  ')).toBeNull();
});
```

- [ ] **Step 2: Run vitest** — FAIL until export exists.

- [ ] **Step 3: Implement `geocodeAddress` + Next route**

`geoSearch.ts`:

```ts
export async function geocodeAddress(query: string): Promise<GeoPin | null> {
  const q = query.trim();
  if (!q) return null;
  const res = await fetch(`/api/geocode?q=${encodeURIComponent(q)}`);
  if (!res.ok) return null;
  const body = (await res.json()) as { latitude?: number; longitude?: number };
  return pinFromCoords(body.latitude, body.longitude);
}
```

`route.ts` (App Router GET): read `q`, if empty 400 `{ error: "missing q" }`; Nominatim `search?q=&format=json&limit=1` with User-Agent `TheMonkeysApp/1.0 (contact@monkeys.com.co)`, 5s abort; no result → 404 `{ error: "not found" }`; success `{ latitude, longitude }` numbers.

PlacePin: add optional `address?: string`. On **Pin this address**, resolve query = `address` or closest form `input[name="location"]` / `input[name="city"]`. Call `geocodeAddress`. On null, set existing error string “Could not pin that address. Try Use my location.”

- [ ] **Step 4: Vitest pass.** Manual: button visible, 44px target.

- [ ] **Step 5: Skip commit**.

---

### Task 4: Create/edit chips

**Files:**
- Modify: `EventForm.tsx` — replace tags comma-only UI with chips + extra field; `body.tags = mergeCategoryTags(selected, extra)`.
- Modify: `GroupForm.tsx` — same for `topics`.
- Modify: `EventsDiscover.tsx` — import `EVENT_CATEGORIES`, wire More.
- Modify: `GroupsPageClient.tsx` — category chips → `topics`.

EventForm selected tags init: `event?.tags` intersected with known slugs; extras = tags not in `EVENT_CATEGORIES`.

More on discover: `moreOpen` boolean; when true, show `<input aria-label="Custom tag">` that sets `activeTag` to trimmed lowercase on Enter or 300ms debounce. Second click on More closes and does not clear a selected named chip.

- [ ] **Step 1:** No new unit test beyond Task 1; browser-verify in Task 6.

- [ ] **Step 2:** Implement chips. Keep extra `<Input name='tags'>` / topics for custom values.

- [ ] **Step 3:** Skip commit.

---

### Task 5: Discover copy, fetching overlay, groups city

**Files:**
- Modify: `EventsDiscover.tsx` empty heading to use `displayLocation`; communities title nationwide vs city; grid wrapper `aria-busy={popular.isFetching}` and overlay text `Updating…` when `popular.isFetching && !popular.isLoading`.
- Modify: `GroupsPageClient.tsx` near-me filters to include `city: ipLocation.city.trim() || undefined` when sending radius (mirror events). Overlay on `discover.isFetching`.

Empty copy must not say Bengaluru when `nationwide` is true.

- [ ] Implement.
- [ ] Skip commit.

---

### Task 6: Full verification (frontend, backend, DB, UI, UX, responsive, performance)

Do not mark the plan complete until this task has **fresh command/browser output**.

**Actors:** Dave session on `http://localhost:3000`; gateway `http://localhost:8081`; DB `the_monkeys_user_dev`.

- [ ] **V1 Unit:** `go test ./microservices/the_monkeys_events/internal/database/ -run TestAttachEventCoords -count=1` → PASS. `pnpm exec vitest run src/lib/eventCategories.test.ts src/lib/geoSearch.test.ts` from the Next app → PASS.

- [ ] **V2 API+SQL pin:** As a logged-in host, create (or PATCH) an in-person event with both lat/lng. `GET /api/v1/events/:slug` JSON `venue.latitude` / `venue.longitude` match SQL `SELECT latitude, longitude FROM events WHERE slug=...`. Rebuild events container if scanEvent changed.

- [ ] **V3 API tags:** `GET /api/v1/events?tags=writing&date=upcoming` returns only writing-tagged published upcoming. Untagged rooftop chai absent.

- [ ] **V4 Groups geo+city:** `GET /api/v1/groups?city=Bengaluru&user_lat=...&user_lng=...&radius=25` includes Lab Host chai circle (unpinned). SQL city still Bengaluru, coords may be NULL.

- [ ] **V5 Browser desktop `/events`:** Click Writing → only writing cards (wait for Updating… to clear). Click Networking → empty copy uses **everywhere** if Everywhere is on, else the city. More opens custom tag field. 10/25/50 still send `radius=`.

- [ ] **V6 Browser `/events/new`:** Chips + Pin this address + Use my location. Save draft. SQL coords non-NULL if pin used. Redirect to `/events/:slug`. Open edit: pin numbers still shown.

- [ ] **V7 Browser `/groups`:** Radius 25 + Bengaluru shows chai circle. Topic chip `writing` filters. ~375px viewport: chips wrap, no horizontal page overflow, targets ≥44px.

- [ ] **V8 Performance:** From the events page, after a tag click, record (a) XHR duration for `/api/v1/events?...tags=` (flag if **>1000ms**), (b) whether Updating… is visible until titles change, (c) first `/events` load. Log numbers in the completion report. If list JSON >1s, say so; do not silently tune unless a query is obviously stuck (spinner >1s with no overlay).

- [ ] **V9 Rebuild:** If events binary is stale, `docker compose` rebuild `the_monkeys_events` so V2 GET venue coords work.

Write a short evidence block (URLs, status codes, SQL snippets, timings) in the session reply. No production-GO.

---

## Self-review

- Spec tags, pin, venue restore, copy, More, groups city, perf overlay → Tasks 1–6.
- No TBD. No protoc. No commits.
- `mergeCategoryTags` / `attachEventCoords` / `geocodeAddress` names match across tasks.
