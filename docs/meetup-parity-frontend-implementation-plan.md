# Meetup-Parity Frontend Implementation Plan

## Purpose

This document defines how the Monkeys web client (`local/the_monkeys`, Next.js app-router, pnpm monorepo) evolves into a Meetup-class community and events UI, wired to the backend delivered in `docs/meetup-parity-implementation-plan.md` (backend Phases 0-8 complete; Phase 9 UI is this document).

It is the frontend companion to the backend plan. The backend already exposes stable REST for events (create, update, publish, cancel, detail, list, tiers, coupons, RSVP, waitlist, payments, comments, reactions, reports, co-hosts, attendees, share, calendar, reminders) and for groups (list, detail, create, update, delete, publish, join, leave, members, roles, moderation, rules, group-scoped event creation).

The frontend work must be additive and backward compatible. Existing event pages, the existing API client (`services/events`), hooks (`hooks/events`) and route constants must keep working.

## Current State

Already in place (foundation reused, do not rebuild):

- **Event API client**: `src/services/events/eventsApi.ts`, `src/services/events/eventTypes.ts`.
- **Event hooks**: `src/hooks/events/useEventQueries.ts` (`useEventList`, `useUserEvents`, `useAttendingEvents`, `useEventDetail`, `useEventComments`, `useEventAttendees`, `useEventCoupons`), `useRefreshEvents.ts`.
- **Event pages**: `src/app/events/page.tsx` (discover/list), `src/app/events/new/`, `src/app/events/[slug]/` (detail + `manage/` + `edit/`).
- **Event components**: `EventActions`, `EventCard` (+ `EventHeroCard`, `EventEmpty`), `EventComments`, `EventForm`, `EventManage`, `EventReactions`, `RsvpPanel`, and the new `components/events/detail/*` set.
- **Time/money helpers**: `src/lib/eventTime.ts`.
- **Route constants**: `src/constants/routeConstants.ts` (`EVENTS_ROUTE`; **no** `GROUPS_ROUTE` yet).
- **Atoms/theme**: `@the-monkeys/ui/atoms/*`, Tailwind `brand.orange #FF5542`, dark mode via class.
- **Auth**: `useAuth()` -> `{ data: session }` (`IUser`).

Not yet built (this plan): groups API client, group pages, advanced RSVP form, attendee management UI, recurrence/venue editors, discovery/search, messaging, billing/dues, analytics.

## Engineering Principles

- Keep every change additive and backward compatible; never break existing event routes or the events API client.
- One API domain = `services/<domain>/<domain>Api.ts` + `<domain>Types.ts`; one query domain = `hooks/<domain>/use<Domain>Queries.ts`.
- Reuse before rebuilding. `RsvpPanel` owns ticket/coupon/payment logic; never duplicate it.
- DRY, modular, fast-rendering. Prefer server components; add `'use client'` only where interactivity requires it.
- Follow the Monkeys theme tokens and atoms exactly. No ad-hoc colors.
- Responsive by default: desktop > 1024 (two-column + sticky), tablet 768-1023 (single column), mobile < 767 (single column + floating action bar).
- Fluid typography via `clamp()`; interactive targets >= 44x44px; images `loading="lazy"` with explicit aspect ratios.
- Native platform primitives first (`<details>/<summary>`, `<dialog>` where viable) before JS widgets.
- Graceful degradation: fallback avatar initials, count-only fallbacks when host-only lists are unavailable, polyfilled `ResizeObserver`/`IntersectionObserver`/`fetch` guards.
- No dead code: build a client only when its backend route exists. Messaging/subscriptions/analytics UIs wait for their backends.
- Consistent time (RFC3339 in, localized out) and currency (minor units, `formatPrice`) formatting everywhere.
- The frontend is gitignored from the parent Go repo; searches need `includeIgnoredFiles: true`.

## Execution Order

Per screen/domain, implement in this order:

1. Types (`<domain>Types.ts`).
2. API client (`<domain>Api.ts`) + fetchers.
3. Query hooks (`use<Domain>Queries.ts`).
4. Route constants.
5. Shared UI primitives/atoms.
6. Page composition (server shell + client islands).
7. Lint (`prettier --write` + `eslint`) + type check + responsive pass.

## Phase 0: Foundation Audit And Conventions ☑️

- Map theme tokens, atoms, hooks, API layer, fetchers and route constants.
- Confirm axios instances (`axiosInstance`, `axiosInstanceNoAuth`, V2 variants) and `fetcher`/`authFetcher` usage.
- Confirm `ProfileImage`/`ProfileFrame`/`DefaultProfile` fallback behavior.
- Confirm Prettier + import-sort enforcement via ESLint.

## Phase 1: Event API Types And Helpers ☑️

- Extend `EventItem` with additive optional fields: `group_id`, `group_slug`, `group_name`, `visibility`, `venue_id`, `venue`, `how_to_find_us`, `rsvp_opens_at`, `rsvp_closes_at`, `allow_guests`, `max_guests_per_rsvp`, `series_id`, `series_occurrence_at`, `recurrence_text`, `questions`, `faqs`.
- Add `Venue`, `EventQuestion`, `EventFaq` types.
- Add `eventTime.ts` helpers: `spotsLeft`, `lowestTierPrice`, `eventPriceLabel`, `formatVenueAddress`, `eventLocationLabel`, `mapQuery`, `eventDateParts`.

## Phase 2: Single Event Detail View ☑️

`app/events/[slug]/` composed via `EventDetailClient.tsx`, all under `components/events/detail/`:

- `EventStickyBar` (floating/sticky registration bar, scroll-reveal, spots-left badge, share, Attend CTA).
- `EventHost` (host/co-host badges, group link).
- `EventFaq` (native `<details>` accordion).
- `EventLocationMap` (OpenStreetMap embed, directions, "how to find us").
- `EventAttendees` (avatar grid, host-only list with count-only fallback).
- `EventGallery` (lazy grid + empty state).
- `EventSidebarMeta` (date/venue/community cards).
- `EventRelated` ("you may also like").
- Two-column desktop + sticky sidebar, single-column tablet/mobile + floating bar; RSVP logic reused from `RsvpPanel`.
- Lint/type clean.

## Phase 3: Groups API Client, Types And Routes ☑️

Wire the group backend surface (`/api/v1/groups`). Blocks Phases 4-7.

- `src/constants/routeConstants.ts`: add `GROUPS_ROUTE = '/groups'`.
- `src/services/groups/groupsTypes.ts`:
  - `GroupItem` (id, slug, name, description, visibility `public|private|unlisted`, status, city, region, country, timezone, latitude, longitude, cover_image, logo_image, organizer, member_count, topics, viewer standing).
  - `GroupMember` (username, role `organizer|co_organizer|moderator|member`, status `active|pending|banned`, joined_at).
  - `GroupRule` (id, title/text, sort_order).
  - `GroupBody`, `JoinBody` (`answers`), `MemberRoleBody`, `BanBody`.
  - `GroupListFilters` (topics, city/region/country, q, page/limit).
- `src/services/groups/groupsApi.ts` (mirror routes.go exactly):
  - `listGroups(filters)` -> `GET /groups`
  - `getUserGroups(username)` -> `GET /groups/user/:username`
  - `getGroup(slug)` -> `GET /groups/:slug`
  - `createGroup(body)` -> `POST /groups`
  - `updateGroup(slug, body)` -> `PUT /groups/:slug`
  - `deleteGroup(slug)` -> `DELETE /groups/:slug`
  - `publishGroup(slug)` -> `POST /groups/:slug/publish`
  - `joinGroup(slug, body?)` -> `POST /groups/:slug/join`
  - `leaveGroup(slug)` -> `DELETE /groups/:slug/membership`
  - `listMembers(slug)` -> `GET /groups/:slug/members`
  - `updateMemberRole(slug, username, body)` -> `PUT /groups/:slug/members/:username/role`
  - `removeMember(slug, username)` -> `DELETE /groups/:slug/members/:username`
  - `banMember(slug, username, body)` -> `POST /groups/:slug/members/:username/ban`
  - `approveJoin(slug, username)` -> `POST /groups/:slug/members/:username/approve`
  - `rejectJoin(slug, username)` -> `POST /groups/:slug/members/:username/reject`
  - `addRule(slug, body)` / `updateRule(slug, id, body)` / `deleteRule(slug, id)`
  - `createGroupEvent(slug, body)` -> `POST /groups/:slug/events`
  - `groupError(err)` message extractor (mirror `eventError`).
- `src/hooks/groups/useGroupQueries.ts`: `useGroupList`, `useUserGroups`, `useGroupDetail`, `useGroupMembers`, plus mutations (create/update/delete/publish/join/leave/role/remove/ban/approve/reject/rules) with cache invalidation.

## Phase 4: Community / Group Discovery Page ☑️

Route `/groups` (model on `app/events/page.tsx`).

- Server shell + client island; search with debounce; topic + location filters; visibility-aware.
- Group card component (`components/groups/GroupCard.tsx`): cover/logo, name, member count, topics, city, join CTA / membership badge.
- Empty state, skeletons, "recommended groups" slot (deferred to Phase 9 if no recommendation API).
- Hero/section for featured groups; "your groups" strip when authenticated.

## Phase 5: Group Detail Page ☑️

Route `/groups/[slug]`.

- Header: cover, logo, name, visibility badge, member count, join/leave CTA (join-request state for private/restricted).
- Tabs/sections: About/description, Rules (`GroupRules`), Upcoming/past group events (reuse `EventCard`), Members preview (link to full members page), Organizers.
- Community callout; share controls.
- Respect visibility: private group shows request-to-join gate; unlisted accessible by link.

## Phase 6: Group Management Flows ☑️

- Create/edit group flow (`/groups/new`, `/groups/[slug]/edit`): name, description, visibility, location, timezone, topics, cover/logo upload (reuse existing file fetcher).
- Group settings page (publish, danger zone: delete — organizer only).
- Group rules editor (add/update/delete, sort).
- Members page (`/groups/[slug]/members`): roster, role management, remove/ban, permission-gated controls.
- Join request review page (approve/reject pending members).
- All destructive actions behind confirm dialogs; controls gated by viewer standing (organizer/staff/permission bits).

## Phase 7: Group-Scoped Events, Venue And Recurrence ☑️

- ☑️ Group event creation flow (`GroupEventForm` → `POST /groups/:slug/events`); focused body mapping 1:1 to `GroupEventBody` (no tiers/co-hosts, adds group-event `visibility`). Staff-gated `/groups/:slug/events/new` page with group context header; redirects to the created event's manage page.
- ☑️ Staff entry points: "Create event" on the group detail header and the group manage action row.
- ☑️ Read-only recurrence indicator (`EventSeriesNote`) on event detail, driven by existing `recurrence_text`/`series_id` fields; renders nothing for one-off events.
- ☑️ Standalone event creation remains supported and unchanged (`EventForm` untouched).
- ⛔ DEFERRED — no backend API (would be dead code):
  - **Venue selector**: `Venue` proto + DB layer exist, but no gRPC/gateway venue endpoints and `venue_id` is not mapped in `GroupEventBody`; only free-text `location` is usable.
  - **Recurrence editor**: `series_id`/`series_occurrence_at`/`recurrence_text` are read-only in responses; `Create/UpdateEventReq` expose no recurrence input fields.
  - **Group-events listing**: no group-slug filter on `ListEventsReq`; listing "intentionally absent" per gateway handler.


## Phase 8: Advanced RSVP And Attendee Management ☑️

- ☑️ Attendee check-in controls in `EventManage` (`AttendeesBlock`): per-attendee "Check in" / "Checked in" toggle wired to `PUT /events/:slug/attendees/:id/attendance` (`updateAttendance`), with per-row busy state and a "N checked in" summary. State reads the observable legacy `checked_in` boolean returned by the attendee list.
- ☑️ CSV attendee export already wired (`exportAttendeesCsv` → `GET /events/:slug/attendees/export`); left unchanged.
- ⛔ DEFERRED — backend accepts no input / returns no data (would be dead UI):
  - **Guest count on RSVP**: `guest_count` exists in the RSVP proto but the gateway `RSVPBody` drops it and the DB ignores it; not stored or returned.
  - **RSVP question answers**: `answers` exists in the RSVP proto but is unmapped at the gateway and unstored; event questions are never hydrated into event detail and have no gateway CRUD routes.
  - **RSVP windows / guest settings**: `rsvp_opens_at`/`rsvp_closes_at`/`allow_guests`/`max_guests_per_rsvp` are dead input (gateway `EventBody` omits them, DB never writes/reads them).
  - **No-show / not-coming states & answers view**: `attendance_status` is write-only server-side (accepted by the PUT but not returned by the attendee list), and `event_question_answers` are never returned; only check-in state is observable, so only check-in is surfaced.
  - **Announcement composer**: messaging backend absent (Phase 10).


## Phase 9: Discovery, Search And Recommendations ☑️

- ☑️ Events discovery search completed to the full backend-supported filter set: added debounced `location` and `tags` (comma-separated) inputs alongside the existing `q` search + event-type chips on `/events` (`ListFilters` already carried these fields; backend `GET /events` binds `q`/`type`/`location`/`tags`/`status`).
- ☑️ Groups discovery already ships the backend-supported filters (`q` search + `city` + `topics`) from Phase 4; no rebuild — reused as the groups search surface (`GET /groups` binds `q`/`topics`/`city`/`region`/`country`/`status`).
- ☑️ No duplicate `/search` pages: discovery pages are the search surface (reuse-before-rebuild), keeping one code path per domain.
- ⛔ DEFERRED — no backend route (would be dead calls):
  - **Saved events/groups**: events expose only `POST/DELETE /:slug/save` (write, no state field returned) and there is **no** saved-listing route (`GET /events/saved`); groups have **no** save/unsave or saved-listing routes at all. Without a listing endpoint or a `viewer_saved` flag, neither a saved page nor a reliable save toggle can be built.
  - **Recommended events/groups**: no recommendation routes on either service.
  - **Date filters for events**: `GET /events` accepts no date params (`from`/`to`/`upcoming`/`past`); only `q`/`type`/`location`/`tags`/`status`.


## Phase 10: Messaging (Backend-Gated) ⛔ BLOCKED — backend absent (verified 2026-08-23)

- Thread list / inbox.
- Direct message thread.
- Group discussion thread.
- Event attendee announcement composer.
- Build only after the messaging service and routes exist.
- ⛔ VERIFIED BLOCKED: no messaging backend exists. No gateway routes (`/messages`, `/conversations`, `/threads`, `/inbox`, `/dm`, `/announcements`), no `gw_message.proto` / gRPC service, and no `the_monkeys_messaging` microservice. The only artifacts are three **unused** DB tables in `schema/000011_meetup_communities.up.sql` (`message_threads`, `message_thread_members`, `messages`) that no service queries. WebSocket exists only for notifications/blog, not messaging. The backend plan lists the RPCs (`CreateThread`/`SendMessage`/…) as unimplemented. Nothing to build until the backend lands.


## Phase 11: Subscriptions, Dues, Billing And Organizer Tools (Backend-Gated) ⛔ BLOCKED — backend absent (verified 2026-08-23)

- Organizer plan page, billing status page.
- Group dues settings, member dues payment flow (reuse Razorpay integration from `RsvpPanel`).
- Event analytics page, organizer dashboard.
- Build only after the billing/subscription/analytics services exist.
- ⛔ VERIFIED BLOCKED: none of these have a backend. No gateway routes for plans/subscriptions/billing/invoices, no `/groups/:slug/dues*` routes, no event analytics/organizer-dashboard routes; no billing/subscription/analytics microservice; no billing/dues/subscription proto RPCs. Migration `000011` creates four **orphaned** tables (`plans`, `organizer_subscriptions`, `group_dues`, `group_due_payments`) that no service queries, plus unused permission bits (`manage_dues`, `view_group_analytics`). Razorpay is wired **only** for event RSVP ticket payments (already consumed by `RsvpPanel`); there is no subscription/dues payment order creation. The activity service exposes only mock analytics (blog-internal), not organizer-facing event/group metrics. Nothing to build until the backend lands.


## Shared UI Primitives

Extract and reuse across phases (place in `components/common` or `packages/ui` as appropriate):

- Avatar/initials fallback (already `ProfileImage`/`ProfileFrame`).
- Card shells, badges (visibility/status/role), count pills.
- Filter bar + debounced search input.
- Confirm dialog, empty state, skeleton loaders.
- Sticky/floating action bar pattern (from `EventStickyBar`).
- Map embed, gallery grid, FAQ accordion (from event detail set).

## Backward Compatibility Checklist

Before merging each phase:

- Existing `/events`, `/events/new`, `/events/[slug]`, `/events/[slug]/manage`, `/events/[slug]/edit` render unchanged.
- Existing events API client and hooks still compile and behave the same.
- Events with no `group_id` render exactly as before.
- Free RSVP flow works without payment config; paid flow degrades gracefully.
- No new required props on existing shared components; all additions optional.
- `prettier --write` + `eslint` clean; type check clean; no new `no-img-element` beyond the existing `<img>` convention.

## Verification Strategy

- **Type/lint**: `get_errors` on changed files; `npx prettier --write` then `npx eslint` on changed globs; scoped `next build` per milestone.
- **Responsive**: verify desktop (>1024), tablet (768-1023), mobile (<767) breakpoints for each page.
- **Graceful degradation**: host-only lists fall back to counts; missing images fall back to initials/placeholder.
- **Auth states**: signed-out, member, staff, organizer views for group pages.

## Milestones

1. **Groups foundation** — Phase 3 (API client/types/hooks/route) + Phase 4 (discovery) + Phase 5 (detail).
2. **Group management** — Phase 6 (create/edit/settings/rules/members/join review).
3. **Group events** — Phase 7 (group-scoped creation, venue, recurrence).
4. **Advanced RSVP** — Phase 8 (guests, questions, check-in).
5. **Discovery** — Phase 9 (search, saved, recommendations).
6. **Messaging** — Phase 10 (backend-gated).
7. **Billing and pro tools** — Phase 11 (backend-gated).
