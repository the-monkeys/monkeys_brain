# Public events/groups UI — geo pin, radius, delete 409

> **For agentic workers:** Execute in this session. Do **not** git commit. Browser-verify after each task. Skip `/api/v1/admin/*`.

**Goal:** Wire the existing public Next.js app to the backend geo clamp (2–100 km, default 25), optional create/edit pins, and paid-delete 409 copy — without breaking nationwide search or omitting-pin writes.

**Architecture:** Keep current list URLs and IP-based near-me. Change only radius steps, optional lat/lng on write bodies, and delete confirm copy. Shared helpers in `geoSearch.ts` plus small `PlacePin` / `RadiusChips` components.

**Tech Stack:** Next.js 14, React 18, Vitest, Tailwind, existing `eventsApi` / `groupsApi`.

## Global Constraints

- Public site only. Do not call `/api/v1/admin/*`.
- Backward compatible: omit pin → same as today (Nominatim / city string). Typed city → nationwide string filter, no pin.
- Near-me: pin **and** `radius` in 2–100. Never send `radius=0` for near-me. Engine does not default 25.
- Default UI radius **25 km**. Auto-expand only through 10/25/50/100. Typed location stays string-only.
- Event GET has no lat/lng; prefills may use `venue` coords. Groups GET has lat/lng.
- Virtual events omit pin. In-person/hybrid send both non-zero coords when the host pinned.
- 409 delete/RSVP: show `{ error }` via existing `eventError` / `groupError`.
- Mobile-first. Do not commit unless the user asks.
- Signup `role_id = 4` unchanged.

### File map

| File | Responsibility |
| --- | --- |
| `apps/the_monkeys/src/lib/geoSearch.ts` | Clamp, steps, near-me query helper |
| `apps/the_monkeys/src/lib/geoSearch.test.ts` | Unit tests |
| `apps/the_monkeys/src/components/geo/PlacePin.tsx` | Optional geolocation pin control |
| `apps/the_monkeys/src/components/geo/RadiusChips.tsx` | 10/25/50/100/Everywhere |
| `EventsDiscover.tsx` / `CommunityGroups.tsx` | Use new steps + chips |
| `eventTypes.ts` / `EventForm.tsx` / `GroupEventForm.tsx` | Optional lat/lng |
| `GroupForm.tsx` | Send existing GroupBody lat/lng |
| `EventManage.tsx` / `GroupSettings.tsx` | Confirm copy for paid 409 |

Frontend lives at `local/the_monkeys/apps/the_monkeys/` (nested git repo; engine gitignores it).

---

### Task 1: `geoSearch` clamp and steps

**Files:**
- Test: `local/the_monkeys/apps/the_monkeys/src/lib/geoSearch.test.ts`
- Modify: `local/the_monkeys/apps/the_monkeys/src/lib/geoSearch.ts`

- [ ] Write failing tests for clamp (0/neg → 0 no-apply; 1→2; 25→25; 250→100), steps `[10,25,50,100]`, default index of 25, `nearMeQuery` omits radius 0.
- [ ] Implement until `pnpm test -- src/lib/geoSearch.test.ts` passes.
- [ ] Skip commit.

### Task 2: Discover radius UI

**Files:** `EventsDiscover.tsx`, `CommunityGroups.tsx`, `RadiusChips.tsx`

- [ ] Default 25 km. Auto-expand 25→50→100 only if user did not pick a chip. Everywhere omits pin+radius.
- [ ] Typed city: no pin (existing `manualOverride`).
- [ ] Add `nearest` sort when a pin is active.
- [ ] Browser: `/events` and groups hub, mobile + desktop.

### Task 3: Event pin on create/update/series

**Files:** `eventTypes.ts`, `EventForm.tsx`, `GroupEventForm.tsx`, `groupsTypes.ts` (`GroupEventBody`), `PlacePin.tsx`

- [ ] Optional `latitude`/`longitude` on bodies. Virtual omits. PlacePin uses browser geolocation.
- [ ] Prefill from `event.venue` when present.
- [ ] Browser: create in-person with and without pin; virtual omits pin.

### Task 4: Group pin

**Files:** `GroupForm.tsx`

- [ ] Prefill from `group.latitude`/`longitude`. Send only when both non-zero.
- [ ] Browser: create/edit group with and without pin.

### Task 5: Delete 409 copy

**Files:** `EventManage.tsx`, `GroupSettings.tsx`

- [ ] Confirm dialog: cancel/refund first if paid. Keep toast of server `{ error }`.
- [ ] Browser: open manage pages; confirm copy; RSVP still toasts `eventError`.
