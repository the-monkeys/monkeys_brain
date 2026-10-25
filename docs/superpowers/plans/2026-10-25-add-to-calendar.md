# Add to calendar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Google, Outlook and Apple calendar choices without breaking old calendar consumers.

**Architecture:** Keep single-occurrence export as the default. Add opt-in series export from real persisted dates; the frontend uses provider compose links only for individual occurrences and ICS import for series. No subscription or OAuth.

**Tech Stack:** Next.js/React, existing Radix dialog, TypeScript, Go/Gin/gRPC, PostgreSQL read queries, Vitest and Go tests.

**Spec:** docs/superpowers/specs/2026-10-25-add-to-calendar-design.md

## Global Constraints

- Everything local on `codex/event-timezone-release`; preserve dirty files; no stage/commit/push or event data writes.
- User generates protobuf code; never run or generate a protoc command.
- Existing calendar route, default scope and UIDs stay compatible.
- No private meeting links in provider URLs; authorize every exported occurrence independently.
- UTC stored instants are authoritative, including legacy invalid timezone labels.
- No em dashes in controlled copy; mobile-first with keyboard-accessible controls.

## Review Focus

- Edited/cancelled/private siblings must not become imaginary or exposed series dates.
- New frontend against old backend must not label a single download as a series.
- Legacy malformed dates/zones must not crash the page or shift an instant.
- Multi-byte titles and embedded newlines must not corrupt an ICS record.
- Export scope and viewer identity must cross the gateway safely.

### Task 1: Additive export contract and backend

**Files:** gateway_event/pb/gw_event.proto; events/internal/database/calendar.go and interface.go; events/internal/services/calendar.go and service.go; gateway/internal/events/handler.go; corresponding calendar tests.

**Interfaces:** CalendarReq.include_series bool defaults false. EventDB.CalendarOccurrenceSlugs(ctx, event) ([]string,error) selects dated siblings without hydration/writes. GET calendar accepts scope=this|series, includes viewer ID, returns X-Monkeys-Calendar-Scope only after a supporting response. Existing buildICS(event) []byte remains single-event compatible; a validated multi-event builder renders series.

- [x] Add tests proving default identity forwarding, invalid-scope rejection, single-event UID/time compatibility, UTF-8 folding, date validation, series filtering and per-occurrence privacy. Run tests and observe the missing behavior.
- [x] Add the optional protobuf field and ask the user to generate types; continue independent frontend tasks while waiting.
- [x] Implement a read-only series slug query from the selected original occurrence position. Hydrate without geocoding, redact each candidate, skip inaccessible/non-public-status dates; abort unexpected database failures. Bound export size and refuse truncation.
- [x] Render individual UTC event components; preserve old UIDs and default download response. Add scope acknowledgment derived from an additive CalendarResp field to detect an older event service too.
- [x] Run Go tests for changed packages offline; expected PASS, no real services.

### Task 2: Mobile provider chooser

**Files:** frontend src/lib/eventCalendar.ts; src/components/events/detail/EventCalendarDialog.tsx; EventActions.tsx; eventsApi.ts; focused tests beside these modules.

**Interfaces:** buildCalendarProviderUrl(event, provider, origin) returns validated URL or null, excluding sensitive details. downloadCalendar(slug, scope='this') remains compatible, adds scope only when opting in and refuses unsupported series responses. EventCalendarDialog receives EventItem and owns export state.

- [x] Add failing tests for correct provider dates, zones and no private link; invalid/missing times; new dialog and scope interaction; series import instructions; errors and retry; old-backend rejection and legacy URL behavior.
- [x] Implement UTC provider builders and a dialog with three minimum-44px provider actions, labelled series radio choices, loading/error feedback and import/privacy guidance. Keep Share, report and clone behavior unchanged.
- [x] Run focused Vitest tests with offline guards, then changed-file ESLint; expected PASS.

### Task 3: Integrated local verification and review

- [x] Run full frontend suite and Go changed-package tests; report pre-existing failures by name.
- [x] Rebuild only local gateway/events using existing isolated local override after user generation. Do not touch other services or production.
- [x] Inspect the chooser at 320/390/768/1440 widths, keyboard close/focus and local ICS response content; do not open/save external calendar entries. Restore browser viewport.
- [x] Request a fresh read-only review of scoped calendar changes; fix actionable defects with red/green tests.
- [x] Leave changes uncommitted on the same branch; report import limitations and all verification evidence.

## Progress

User approved the short design and asked to proceed. Native implementation preserves the existing workspace. Task 1 and Task 2 share the explicit scope and acknowledgment contract. No commits are authorized in this task. Protobuf generation is the only anticipated user dependency.

- User generated the additive calendar protobuf types locally. Do not undo unrelated generated files or upgrade the WSL toolchain in this change; user requested that as a later separate task.
- Task 1 implementation: calendar database/service/gateway regressions observed RED, then all three changed Go packages passed with dependency downloads disabled. Organizer parameter escaping had its own RED then GREEN package run.
- Task 2 implementation: 24 focused frontend tests passed; scoped ESLint passed. Browser chooser checked at 320/390/768/1440 with no horizontal overflow, scrollable short viewports, 64px provider rows and 44px close control.
- First full frontend suite: 539 passed, 2 existing failures (`BlogPageClient` members-only sharing; `LandingEventsSection` missing QueryClientProvider). Typecheck still reports existing generated-discussion route, Zod-version and old test-fixture errors; no calendar-file diagnostics.
- Final review: Carver found cancelled dates could compose active provider appointments (P2). Fixed after RED with cancellation-aware ICS choices, keeping valid file downloads enabled. Generic 501 blob errors were also fixed after RED with explicit single-date fallback guidance, promoted from minor because mixed-version fallback is part of the approved compatibility contract. Final focused frontend result: 28/28. No deferred review findings.
- Ruling: use separate saved occurrence components in series ICS rather than a generated RRULE. This is intentional: saved exceptions, finite publication horizon and individual privacy cannot safely be inferred from recurrence_text. The UI calls this Available series dates and discloses that copies do not synchronize.
- Ruling: keep the user's existing checkout/branch and leave all work uncommitted. No worktree, merge, push or event-data writes are authorized.
- Final full frontend result after review fixes: 543 passed, 2 known pre-existing failures (same names above), 120 test files. Changed-file ESLint and whitespace checks passed. Typecheck remains blocked by existing errors outside calendar files; no claim of a clean production frontend build.
- Final Go package verification: all tests pass in events/internal/database, events/internal/services and gateway/internal/events. Only gateway/events rebuilt locally; both healthy on the internal restore-local network.
- Runtime proof: anonymous local gateway download returned 200, text/calendar, private/no-store, one date by default and three dates with scope=series. Neither anonymous file included Join links. Actual signed-in series download through the Next proxy succeeded. No external provider account was accessed or calendar entry created.
- Browser proof: 320/390/768/1440 layouts stay inside viewport with no horizontal dialog overflow; provider targets 64px, close 44px. Escape closes the dialog and returns focus to Add to calendar. Original viewport restored. Screenshot: event-calendar-mobile.png in this chat's visualization output directory.
- Declined review checks were covered by the parent through test/lint execution, local database-backed GET exports, rebuilt isolated services and browser measurement. Real Google/Outlook/Apple account imports were deliberately not performed because testing is local-only. Unrelated attendee/landing/generated changes remain untouched.
- User UI follow-up: replaced the ended-event Edit writeup label with Edit without changing its route or permissions. Matched all primary action-row buttons to the calendar trigger's 44px minimum height, preserving mobile tap targets and wrapping. Two label/navigation regressions observed RED then GREEN (EventActions: 10/10); scoped ESLint passed. Local browser measured Share, Add to calendar, Edit, Manage and Report at 44px on desktop and mobile, with no mobile horizontal overflow. Saved event-actions-mobile.png and restored the viewport. No API or event-data changes in this follow-up.
- Follow-up full suite: 545 passed, 2 existing failures (BlogPageClient members-only sharing and LandingEventsSection missing QueryClientProvider), 547 tests across 120 files. No new failures.
