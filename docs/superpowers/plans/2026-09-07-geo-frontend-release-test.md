# Geo clamp + pin — frontend/backend release test list

> **Superseded for production:** use `docs/superpowers/plans/2026-09-07-events-groups-prod-gate.md`. This file is the geo subset only. It does **not** cover authn, authz, full API inventory, staff roles, or performance. Do not ship on this list alone.

**Goal:** Prove the public site and gateway agree on radius 2–100 km, nationwide `radius=0`, optional create/edit pins, and old NULL-coord rows — before shipping.

**Architecture:** Each case is one user action plus one DB or HTTP check. Fixture rows must include: NULL-coord in-person, pinned in-person inside Bengaluru, pinned in-person outside 100 km, virtual, hybrid, public group with pin, public group without pin.

**Tech Stack:** Gateway `http://localhost:8081`, app `http://localhost:3000`, cookie `mat`, Postgres `events.latitude/longitude` and `groups.latitude/longitude`.

## Why this list exists

Wiring UI before a CRUD/release matrix hides failures: NULL coords look like “empty city,” clamp never gets exercised, clone/series/virtual pin rules never get a save. This chat verified chips + a few live rows that all had NULL coords. That is not a release test.

## Global Constraints

- No `/api/v1/admin/*`. Public site only.
- Do not rewrite existing NULL rows except as explicit Create/Update tests.
- Engine does **not** default 25 km. UI default 25 is a near-me send.
- `radius` 0 / omit / negative = nationwide. Never send `radius=0` for near-me.
- Virtual events store NULL coords even if the client sends a pin.
- Event GET still has no lat/lng; groups GET does.
- Signup `role_id = 4` unchanged.
- Errors: `{ "error": "<message>" }` in toasts.

### Fixtures to create first (or reuse if already pinned)

| ID | Kind | Pin | Where | Why |
| --- | --- | --- | --- | --- |
| F1 | Event `in_person` published | yes | ~12.97, 77.59 (Bengaluru) | Must appear at 25 km from Bengaluru |
| F2 | Event `in_person` published | yes | NYC / far | Must **not** appear at 100 km from Bengaluru; must appear nationwide |
| F3 | Event `in_person` published | no (NULL) | address string only | Must **not** appear in any radius list; must appear nationwide and on host profile |
| F4 | Event `virtual` published | n/a | — | Appears in radius lists (skips geo SQL) |
| F5 | Event `hybrid` published | optional | — | Appears in radius lists even with NULL pin |
| F6 | Group public published | yes | Bengaluru | Appears in groups 25 km |
| F7 | Group public published | no | city string Ahmedabad | Radius miss; nationwide / typed city hit |
| F8 | Weekly series `in_person` | yes | Bengaluru | Discovery: one card; agenda: per date |

Today’s DB (2026-09-07 check): F3/F4/F5-like rows exist (NY meetup NULL, Chai virtual, Summit hybrid NULL). **F1, F6, F8 do not.** Radius UI cannot pass until F1/F6 exist.

---

### Task 1: List / discover (Read)

- [ ] **R1** Events no pin, no radius → nationwide. Includes F2, F3, F4.
- [ ] **R2** Events pin + `radius=25` → F1 yes; F2 no; F3 no; F4 yes; F5 yes.
- [ ] **R3** Events pin + `radius=100` → still no F2/F3.
- [ ] **R4** Events pin + `radius=0` → same as R1 (nationwide). Confirm query string omits radius or sends 0 and does **not** shrink the list.
- [ ] **R5** Events pin + `radius=1` → clamp to 2 km; still a geo filter (not nationwide).
- [ ] **R6** Events pin + `radius=250` → clamp to 100 km; same membership as R3.
- [ ] **R7** UI chips 10 / 25 / 50 / 100 / Everywhere match R2–R4. Default chip **25**. Typed city → no pin, string filter, nationwide-style location ILIKE when radius 0.
- [ ] **R8** `sort=nearest` with pin → F1 before far pinned rows. Without pin → soonest (nearest ignored).
- [ ] **R9** Groups pin + 25 km → F6 yes, F7 no.
- [ ] **R10** Groups Everywhere / typed city → F7 visible.
- [ ] **R11** Hosting `GET /events/user/:username` still shows F3 (NULL coords).
- [ ] **R12** Series collapse: F8 is one card on `/events`, not one tile per date.
- [ ] **R13** Desktop + mobile: chips usable, 44px targets, no horizontal overflow.

### Task 2: Create (C)

- [ ] **C1** In-person event, location string, **no pin** → 201 draft. Coords NULL or Nominatim; **no 400** on Nominatim miss.
- [ ] **C2** In-person event, **Use my location** (both non-zero) → row lat/lng match the pin (skip Nominatim). Then appears in R2.
- [ ] **C3** Virtual event, pin control hidden; if lat/lng sent anyway → DB NULL.
- [ ] **C4** Hybrid with pin → coords stored; still listed under radius (hybrid skips hide).
- [ ] **C5** Series create (`POST /events/series`) with pin → first occurrence (and following rows) have the pin.
- [ ] **C6** Group create, city only, no pin → 201; NULL or geocoded; still in R10.
- [ ] **C7** Group create with pin → GET group returns lat/lng; appears in R9.
- [ ] **C8** Omit lat/lng JSON entirely (old client) → same as C1/C6.

### Task 3: Update (U)

- [ ] **U1** Add pin on edit of F3 → row gets coords; then appears in R2. Event GET still has no lat/lng; form may use venue or client state.
- [ ] **U2** Clear pin on a pinned in-person event → do **not** send `0,0` as a pin; omit fields or leave previous coords per product (confirm: omit = Nominatim path, not Null Island).
- [ ] **U3** Switch in-person → virtual → coords NULL after save.
- [ ] **U4** Group edit: prefill from GET lat/lng; change pin; GET reflects new pin.
- [ ] **U5** Ended event: pin control disabled; other ended-field rules unchanged.

### Task 4: Clone / series extras

- [ ] **K1** Clone pinned event → new draft has **same** lat/lng, no re-geocode.
- [ ] **K2** Clone NULL-coord event → clone stays NULL.

### Task 5: Delete / payments (related, same release surface)

- [ ] **D1** Delete unpaid event → 200, row gone, files gone.
- [ ] **D2** Delete event with captured/pending payment → **409**, toast `{ error }`, confirm copy mentions cancel/refund first.
- [ ] **D3** Delete group with paid child event → **409**.
- [ ] **D4** Paid RSVP/tier with Razorpay off → **409** host-facing copy (not a geo bug; same toast path).

### Task 6: Auth / visibility / images

- [ ] **A1** Draft event/group **404** for other users; host can open.
- [ ] **A2** Anonymous discover still does R1–R10 (public reads).
- [ ] **A3** New cover/photo still uploads; strip is server-side; gallery max 4 unchanged.

### Task 7: Evidence required per case

For each checkbox: HTTP status + body **or** SQL `latitude, longitude` **or** screenshot of chip + card titles. “Looks empty” is not a pass.

### Already done (not a substitute)

- Unit: `common/geo` clamp; frontend `geoSearch.test.ts`.
- Live DB (2026-09-07): nationwide 3 events; Bengaluru 25/100 km drops NULL in-person NY meetup; groups with NULL coords vanish under radius.

---

**Count:** 8 fixtures + **13 list** + **8 create** + **5 update** + **2 clone** + **4 delete** + **3 auth/images** = **43** checks (plus 8 fixtures = **51** if you count setup).

Do not start executing this list until the user says to run it.
