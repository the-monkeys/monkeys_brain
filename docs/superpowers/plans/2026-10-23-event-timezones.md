# Event timezones implementation plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Make event timezone selection control scheduling, reject invalid zones on the backend, and show each viewer their own local date/time.

**Architecture:** Keep the existing RFC3339/protobuf instant plus IANA timezone contract. Convert wall times only in the frontend; validate identifiers and expand recurrences in the event timezone in the service. Use browser Intl and Go's bundled timezone data, without new dependencies.

**Tech stack:** React/Next.js, TypeScript/Intl, Vitest, Go time/tzdata and existing gRPC tests.

**Spec:** The user's approved frontend/backend fix in this chat, including timezone selection, correct conversion, tests, local-only execution, and no change to production or the MariaDB event.

## Global constraints

- Local code and offline tests only; no production access or data correction.
- No dependency installation, environment-file access, branch switches, staging, commits, or pushes.
- Preserve unrelated dirty files in the engine and nested public frontend.
- Existing UTC instants are never reinterpreted by backend validation or display.
- Omitted timezone remains UTC for API compatibility; nonempty invalid zones are rejected.

## Review focus

- Edits from a browser in another zone must preserve the saved instant without changes.
- Missing/repeated wall times at daylight-saving transitions must not silently shift an event.
- Viewer calendar dates must follow their timezone across midnight and hydration.
- Weekly interval and weekday rules must use civil days across DST.
- Optional RSVP deadlines, recurrence end dates, and scheduling-again must use the selected/event zone.

## Task 1: Frontend conversion and scheduling

**Files:** nested frontend `src/lib/eventTime.ts` and tests; `src/components/events/TimezoneSelect.tsx`; `EventForm.tsx`, `EventActions.tsx`, group `GroupEventForm.tsx`, and focused form tests.

**Interfaces:** `fromLocalInput(value, timezone?) -> string` rejects invalid or nonunique wall times; `toLocalInput(value, timezone?) -> string` formats in the chosen zone; `isValidTimezone(value) -> boolean` supports validation; timezone selector uses controlled value/onChange.

- [x] Write and observe RED for 19:00 Asia/Kolkata => 13:30Z; European summer/winter offsets; cross-zone editing; invalid/nonexistent/ambiguous dates; form payload and selector behavior.
- [x] Implement strict Intl conversion and a visible validated selector for standalone and group forms. Convert start/end/deadlines/recurrence boundaries consistently. Preserve ended-event instants.
- [x] Run focused helper and form tests to GREEN.

## Task 2: Backend validation and recurring dates

**Files:** events service `timezone.go`, `timezone_test.go`, `rrule.go`, `rrule_test.go`, and `service.go`.

**Interfaces:** schedule validation returns an IANA location or InvalidArgument, without shifting input instants. Recurrence expansion receives a start in that location.

- [x] Write and observe RED for invalid zones on create/update/series, valid offset-bearing instants, and recurring 19:00 Europe/Berlin across DST.
- [x] Validate before database work, embed tzdata, and expand using local calendar days. Keep existing wire format and empty-zone compatibility.
- [x] Run offline Go tests for the affected service and gateway mappings.

## Task 3: Viewer display and final verification

**Files:** frontend `src/hooks/events/useViewerTimezone.ts`, event/detail/card/landing components consuming event-time formatters, and focused display tests.

**Interfaces:** hydration-safe viewer timezone hook starts consistently in UTC, then resolves the browser zone. Date/time formatters show explicit zone labels and compare dates in that zone.

- [x] Write and observe RED for an India viewer seeing 19:00 despite a European event timezone, and dates spanning midnight only in the viewing zone.
- [x] Apply the viewer zone consistently, with the valid original zone shown separately on detail.
- [x] Run the complete frontend suite, type checks, affected backend suites, formatting/diff checks, and a final review. Record any unavailable checks or unrelated failures.

## Execution record

- Native execution: no subagent tools are available. Work remains uncommitted in the existing checkouts as requested; no worktree/branch changes.
- Go navigation uses gopls CLI because no gopls MCP is connected. No vulnerability database/network checks will be performed during this offline-only task.

### RED / GREEN evidence

- Initial frontend helper tests observed seven failures for selected-zone conversion, invalid/gap/fold inputs, and viewing-zone dates. Form/display tests also failed before implementation. The final full suite passes all 24 timezone-focused cases: 18 helper, four event-form, and two group/display cases.
- An additional edit regression failed when an unchanged repeated DST hour was re-resolved. Unchanged edits now preserve the exact saved instant, including seconds and which occurrence of the repeated hour it represents.
- Initial backend tests failed for invalid zones on create/update/series, missing UTC defaults, and UTC-based recurrence across DST. Further RED tests caught spring-gap drift, non-UTC values at the existing timestamp-column boundary, and moving an explicit second-fold DTSTART backwards. All now pass.
- Real conversion helper assertions also pass under three simulated browser zones: Europe/Berlin, Asia/Kolkata, and America/New_York.

### Final checks (24 October 2026)

- Offline Go command, with `GOTOOLCHAIN=local`, `GOPROXY=off`, and `GOSUMDB=off`: `go test ./microservices/the_monkeys_events/internal/services ./microservices/the_monkeys_events/internal/database ./microservices/the_monkeys_gateway/internal/events ./microservices/the_monkeys_gateway/internal/groups -count=1` — all four packages pass, exit 0.
- Frontend `npm test -- --maxWorkers=2 --minWorkers=1` — 459 tests pass, two fail, across 114 files. A process-local guard blocked HTTP/fetch and environment-file reads; existing network attempts in other tests could not leave the process.
- Remaining frontend failures: `BlogPageClient.test.tsx` expects the members-only share button to be absent; `LandingEventsSection.test.tsx` renders existing EventHostName/useUser without QueryClientProvider. Those test fixtures were not modified. The existing EventGridCard already used EventHostName before these timezone changes.
- Targeted `npm run lint -- --file ...` for all 18 changed/new frontend files — exit 0, with existing Next image-element warnings.
- TypeScript `tsc --noEmit --incremental false` — fails in untouched areas: stale generated discussion route types, Zod version/schema incompatibilities, and existing fixture/type issues in landing, userInfo, blog headers, editor blocks, and topicCatalog. No diagnostics remain in timezone-changed files.
- Both repositories pass `git diff --check`; Go files were gofmt'd and frontend files prettier-formatted.
- gopls diagnostics had an initial workspace-load timeout, so they are not counted as a reliable semantic check. The actual affected Go compiler/test runs passed.
- No browser/manual integration or production build was run during implementation. Full frontend type checking remains blocked as above. No independent review agent was available; the final self-review checked UTC database boundaries, cross-zone edits, DST gaps/folds, recurrence intervals, and viewer dates.

### Deliberate behavior and limits

- A new or changed ambiguous/nonexistent local time is rejected with an explicit error; there is no silent offset selection or fixed 4.5/5.5-hour correction.
- Recurring missing local times are skipped; future repeated local times use the first occurrence, while the explicitly supplied first instant is preserved. Stored generated occurrences are UTC.
- Legacy invalid zone strings require a valid selection when editing; no existing records are automatically repaired. Omitted API zones remain UTC for compatibility.
- All work is local and uncommitted. Production configuration, the existing MariaDB event, unrelated dirty files, and generated protobuf files were not changed by this implementation.

## Agent handoff — 24 October 2026 (Asia/Calcutta)

### Approved review follow-up, 24 October 2026

User authorized fixing all three review findings with tests, locally only. No
existing event rows, production services, environment files, staging, commits,
or pushes may be changed. Work remains visible in the original checkouts, now
on local `codex/event-timezone-fixes` branches in both repositories.

- [x] DST edit validation: add real form-submit regressions for an unchanged
  Berlin event crossing the repeated hour and a future second-fold start whose
  wall time precedes the current wall time. Use UTC-instant validation instead
  of browser wall-clock minimums where those minimums are not sound. Keep new
  ambiguous times rejected and retain past/inverted-range checks.
- [x] Schedule again: add a visible timezone selector, require an explicit valid
  choice for legacy invalid labels, and send that choice through an additive
  clone API field. Store the normalized zone on the new draft only; omitted
  choices inherit a valid source zone. User performs protobuf generation.
  Tests cover invalid selections, selected-zone UTC payloads, gateway forwarding,
  service defaults/validation, and new-draft persistence without source updates.
- [x] Recurrence: pin literal biweekly Monday/Wednesday dates from Wednesday
  21 October 2026; anchor interval arithmetic to Monday calendar weeks while
  retaining event-zone civil dates, DST behavior, explicit DTSTART and UTC
  persistence. Add Sunday-start and interval-one regressions.
- [x] Observe each regression fail before its fix, rerun affected suites with
  network/environment access disabled, request independent final review, and
  record remaining unrelated suite/typecheck failures accurately.

Implementation decisions: keep the existing UTC-plus-IANA contract and native
selector. Do not silently map `India` to a zone or correct existing timestamps.
No dependency installation, DB migration, or storage changes are needed. The
three fixes share that contract but otherwise have separate frontend/backend
write scopes. This follow-up supersedes the earlier no-branch-switch constraint
only for the two local fix branches; all data and production constraints stand.

Follow-up verification observed in this session:

- Two native form-validity regressions and two missing clone-selector regressions
  failed before the frontend fixes, then passed. Five additional guard cases cover
  rejected gap/fold/past/inverted schedules and ambiguous scheduling-again input.
- Final focused frontend run: 33 tests pass across four files. Lint on the six
  frontend files changed in this follow-up passes without warnings or errors.
- Full frontend suite: 468 pass, two fail, across 115 files / 470 tests. Remaining
  failures are the existing `LandingEventsSection.test.tsx` missing query provider
  and `BlogPageClient.test.tsx` members-only share-button assertion. They were not
  changed as part of these fixes.
- Full TypeScript checking still fails on existing generated discussion route
  types, installed Zod/schema incompatibilities, and unrelated fixtures. No errors
  point to the timezone follow-up files. No production build is claimed.
- Parent independently reran the event protobuf package, event service, event
  database, gateway events, and gateway groups tests with module downloads disabled,
  `-buildvcs=false -timeout 120s -p 2 -count=1`: all five packages pass, exit 0.
- User regenerated protobufs after additive `CloneEventReq.timezone = 6` was added.
  The agent did not generate or run a protobuf-generation command. Other generated
  file changes from that user action are preserved and are not hand-edited.
- Go formatting and scoped diff checks pass. Initial sandbox test commands stalled;
  final passing commands ran with approved local execution outside that sandbox.
  Frontend HTTP and environment-file access remained blocked by the process guard.
- Fresh independent review found two important remaining cases: native RSVP
  minimum/maximum bounds rejected unchanged fold deadlines, and clone inheritance
  called a detail reader that can backfill source coordinates. Both were reproduced
  RED before the final fix pass. No Critical or Minor findings were reported.
- RSVP deadline bounds now use the same saved-instant preservation rule as the
  start/end fields. Two native click-submit cases cover a future second-fold
  deadline below the current wall clock and a valid deadline whose wall time is
  after the start's wall time. Focused frontend verification is now 35/35 passing.
- Clone inheritance now calls `EventDB.GetEventTimezone`, implemented in
  `internal/database/event_timezone.go` as a timezone-only SELECT with no hydration,
  geocoding or source writes. Three service cases and four strict SQL cases failed
  before the fix; all five affected backend packages pass after the fix.
- Review rulings: existing recurrence count/horizon and month/year behavior remain
  unchanged; hypothetical group-form editing is not an established current caller;
  live/browser/deployment/build checks remain unperformed; unrelated failures and
  dirty/generated files are preserved; historical timestamp repair is explicitly
  excluded. These are scope limits, not claims that untested paths are validated.
- gopls navigation/diagnostics again stalled and were stopped; they are not counted
  as successful checks. Actual affected Go compiler/tests and formatting passed.
- Final full frontend rerun after review fixes: 470 pass, two fail, across 115
  files / 472 tests, exit 1. The only failures remain the same pre-existing
  `BlogPageClient.test.tsx` and `LandingEventsSection.test.tsx` cases described
  above. Final lint passes on all six follow-up frontend files. No merge/release
  readiness is claimed while the unrelated typecheck/suite failures remain.
- All three requested fixes and both final-review findings are implemented and
  covered by passing focused tests. No new browser/server integration run was
  performed; the next step is the user's isolated local data-restore test.
- No database/container/server operations, data corrections, dependency changes,
  environment edits, staging, commits, pushes or deployments ran. Existing bad
  timestamps are deliberately unchanged. Rebuild/use the updated local gateway,
  events service and frontend for integration testing, not production endpoints.

Read this section together with the execution record above before making changes. Implementation is present in the working files, not in commits. This handoff update changed only this document; tests were not rerun for a documentation-only update.

### User request and scope

The active request is the public event timezone bug, not the older admin verification-document task from earlier chat history. User approved: "Fix both frontend and backend properly, with timezone selection, correct conversion, and tests. Keep it local for now—don't change production or the existing MariaDB event yet."

The user suspects the MariaDB organizer meant 12 November at 19:00 IST but a European timezone was involved. Do not assume that exact historical conversion path is proven, and do not apply a fixed-hour correction. Do not modify the live event, run a data backfill, deploy, access credentials, read/write environment files, install dependencies, start servers, or use production-connected services. Do not switch branches, stage, commit, push, or discard unrelated changes without new authorization.

### Repositories and branch state

- Engine/backend Git root: `C:/Users/Dave/the_monkeys/the_monkeys_engine`.
- Public frontend is a separate nested Git repository: `C:/Users/Dave/the_monkeys/the_monkeys_engine/local/the_monkeys`.
- Frontend application working directory: `C:/Users/Dave/the_monkeys/the_monkeys_engine/local/the_monkeys/apps/the_monkeys`.
- Read-only `git branch --show-current` checks during this handoff report **main in both repositories**.
- This agent did not create or switch branches, create worktrees, stage, commit, or push. Earlier engine checks reported `codex/private-verification-stream`; an earlier frontend check reported `feature/discussions` before later checks showed main. The cause of these branch-state changes is not established. Do not infer that either branch name still contains the work: inspect the current working files and Git status. Do not switch branches to investigate.
- The older admin task mentioned `local/monkeys-admin` and `codex/verification-document-loading`; those are not the public frontend location or the branch used for this event fix.

### Diagnostic evidence already gathered

- Earlier read-only investigation of `https://monkeys.com.co/events/mariadb-india-meetup-1-online-9348dd5c` found start `2026-11-12T19:00:00.000Z`, end `2026-11-12T20:30:00.000Z` in public JSON-LD.
- The page displayed Friday 13 November at 00:30–02:00 in India, not 12:30 PM. Its agenda text said 19:00–20:30. The saved timezone label was `India`, which is not a valid Intl/IANA scheduling identifier.
- 19:00Z really is 00:30 IST the next day. If the intended schedule was 19:00 IST, the correct UTC instant would be 13:30Z. The actual organizer input and browser timezone are not proven from this evidence.
- Code root causes: free-text timezone hidden in advanced options; input conversion used the browser zone instead of the selected event zone; display preferred event zone rather than viewer zone; backend accepted arbitrary timezone strings and expanded recurrence in UTC.
- Existing database timestamp columns store UTC wall values. Local recurrence expansion must convert generated values back to UTC before MaterializeSeries. No schema migration, protobuf regeneration, or existing-event correction was needed or performed.
- No further production inspection is needed to continue reviewing this implementation.

### Exact implementation files owned by this task

Backend paths relative to engine root:

- `microservices/the_monkeys_events/internal/services/timezone.go` — new validation helper; trim zone, empty defaults UTC, require UTC or qualified IANA name, load timezone, validate timestamps/end-after-start, return gRPC InvalidArgument; embedded time/tzdata.
- `microservices/the_monkeys_events/internal/services/timezone_test.go` — new fake-DB tests for create/update/series validation, preservation of offset-bearing instants, UTC default, Berlin weekly/biweekly DST behavior, and UTC DB-boundary values.
- `microservices/the_monkeys_events/internal/services/rrule.go` — walk civil dates independently of DST, reconstruct local wall time, skip missing times, choose earliest repeated future time, preserve the explicitly supplied first instant, and count weekly intervals in civil days.
- `microservices/the_monkeys_events/internal/services/rrule_test.go` — new spring-gap and explicit second-fold DTSTART regressions.
- `microservices/the_monkeys_events/internal/services/service.go` — validate create/update/series before DB work; expand series in loaded event location and convert occurrences to UTC at the DB boundary.
- `docs/superpowers/plans/2026-10-23-event-timezones.md` — this plan, execution record, and handoff.

Frontend paths relative to the application working directory (18 files):

- `src/lib/eventTime.ts` — strict timezone validation; explicit-zone input formatting/conversion; roundtrip candidate detection for DST gaps/folds; zone labels; viewing-zone date comparison.
- `src/lib/eventTime.test.ts` — 18 helper tests, including existing cases.
- `src/components/events/TimezoneSelect.tsx` — new visible controlled native selector, Intl zone inventory plus fallback, UTC/India/browser/saved zone, explicit legacy invalid-zone prompt.
- `src/components/events/EventForm.tsx` — selected-zone start/end/RSVP/recurrence-until conversion, selected-zone minimums, inline conversion errors, exact preservation of unchanged saved timestamps including seconds and repeated-hour identity. Ended-event times remain preserved.
- `src/components/groups/GroupEventForm.tsx` — visible selector and explicit-zone conversion/errors for the group-create flow.
- `src/components/events/EventActions.tsx` — schedule-again fields use inherited event zone, label it, catch conversion errors, reject inverted/equal ranges.
- `src/hooks/events/useViewerTimezone.ts` — new SSR/hydration-safe UTC initial state, browser timezone after mounting.
- `src/app/events/[slug]/EventDetailClient.tsx` — viewer-zone primary schedule; valid differing original event zone shown separately.
- `src/components/events/EventCard.tsx` — viewer-zone card and hero-card dates/times.
- `src/components/events/EventGridCard.tsx` — viewer-zone calendar-card date/time.
- `src/components/events/detail/EventRelated.tsx` — viewer-zone related-event cards.
- `src/components/events/detail/EventSidebarMeta.tsx` — viewer-zone sidebar schedule.
- `src/components/events/detail/EventStickyBar.tsx` — viewer-zone sticky schedule.
- `src/components/landing/LandingEditorialFeed.tsx` — viewer-zone event entries.
- `src/components/landing/LandingEventsSection.tsx` — viewer-zone upcoming dates.
- `src/components/landing/LandingFeaturedEvent.tsx` — viewer-zone featured schedule.
- `__tests__/src/components/events/EventForm.test.tsx` — four tests total; selector/payload, saved-zone editing, unchanged fold/seconds, and existing test. Added explicit cleanup and offline mocks.
- `__tests__/src/components/events/EventTimezone.test.tsx` — new two-test group-submit/viewer-display coverage, with offline mocks.

No dependency/package/lockfile, API wire contract, gateway handler, generated protobuf, DB schema, or ICS changes were made for this task. Existing gateway InvalidArgument-to-HTTP-400 behavior is reused. Group creation delegates to the validated event service.

### Important implementation contracts for review

- Keep UTC instants and event-zone metadata separate; the backend must never reinterpret an explicit input instant as local wall time.
- Selected-zone conversion uses Intl offsets on both sides of transitions, then verifies candidate instants by local-field roundtrip. New/changed nonexistent and repeated times produce explicit errors instead of silently selecting an offset. There is no UI choosing the first/second fold occurrence in this implementation.
- When timezone and displayed input are unchanged during an edit, preserve the original ISO instant instead of re-resolving minute precision. This protects seconds and a previously saved second-fold occurrence. Changing the zone on an upcoming event deliberately reinterprets the entered wall time in the newly selected zone.
- Empty legacy zones are interpreted as UTC; nonempty invalid zones such as `India` require a valid selection. No automatic data repair.
- The group form is currently used for creation; general editing uses EventForm. GroupEventForm's optional event prop does not have EventForm's full unchanged-fold preservation logic.
- Recurrence uses civil-calendar arithmetic. Missing local times are skipped; future repeated times use the first occurrence, except DTSTART remains its explicitly supplied instant. Existing general recurrence count/horizon/month-AddDate behavior was not redesigned.
- Viewer timezone starts UTC for consistent server/client hydration, then uses browser settings. Explicit zone labels make that initial state unambiguous. No IP-based timezone determination.
- Browser Intl and Go embedded tzdata are the source of date-specific timezone rules; no hardcoded Europe/India offsets.

### Unrelated working changes to preserve

Engine tracked dirty files not owned by this implementation:

- `apis/serviceconn/gateway_discussion/pb/gw_discussion.pb.go`
- `apis/serviceconn/gateway_discussion/pb/gw_discussion_grpc.pb.go`
- `apis/serviceconn/gateway_event/pb/gw_event.pb.go`

Engine untracked items also not owned by this implementation: `.pnpm-store/`, `INVESTIGATION-2026-09-08-insufficient-credits.md`, `implementation.md`, `microservices/buddhicintaka.md`, and `seed.templates.py`. This list is a status snapshot, not authorization to inspect or remove those items.

Nested frontend unrelated changes to preserve:

- Modified `docs/superpowers/specs/2026-09-11-premium-editorial-landing-page-design.md`.
- Untracked `a`.
- Untracked `apps/the_monkeys/__tests__/src/components/landing/LandingGroupsCallout.test.tsx`.
- Untracked `apps/the_monkeys/src/components/landing/LandingGroupsCallout.tsx`, `LandingPostCollection.tsx`, and `LandingSupportingPosts.tsx`.
- Untracked `docs/superpowers/plans/2026-09-11-premium-editorial-landing-page.md` and `2026-09-12-reference-led-landing-page.md`.

Do not include these unrelated changes in a future timezone commit. The landing files listed in the implementation inventory above ARE part of the timezone fix; the separate unrelated landing files listed here are NOT.

### Reproducing verification safely

Backend: from engine root, temporarily set `GOTOOLCHAIN=local`, `GOPROXY=off`, and `GOSUMDB=off`, then run the four-package Go command recorded above. Installed dependencies were reused. No DB/service connection is required for the synthetic/fake-DB tests.

Frontend: run from the application directory. The successful/failed checks above used a process-local Node preload to suppress environment-file access and block fetch/http/https requests. The preload was passed through a data URL in temporary NODE_OPTIONS, not saved into application sources. Restore previous environment-variable values after running. Do not run an unguarded full suite against existing configuration: some unrelated tests attempt network calls.

For reconstruction, the preload source used was:

```javascript
import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import http from 'node:http';
import https from 'node:https';
const env = p => /(?:^|[\\/])\.env(?:\.[^\\/]*)?$/.test(String(p));
const err = () => Object.assign(
  new Error('Environment files disabled for offline checks'), { code: 'ENOENT' }
);
for (const n of ['readFileSync', 'openSync', 'statSync', 'lstatSync']) {
  const original = fs[n];
  fs[n] = function (p, ...args) {
    if (env(p)) {
      if ((n === 'statSync' || n === 'lstatSync') && args[0]?.throwIfNoEntry === false)
        return undefined;
      throw err();
    }
    return original.call(this, p, ...args);
  };
}
const exists = fs.existsSync;
fs.existsSync = p => !env(p) && exists(p);
for (const n of ['readFile', 'open', 'stat', 'lstat']) {
  const original = fs.promises[n];
  fs.promises[n] = async function (p, ...args) {
    if (env(p)) throw err();
    return original.call(this, p, ...args);
  };
  const callbackOriginal = fs[n];
  fs[n] = function (p, ...args) {
    if (env(p)) {
      queueMicrotask(() => args.at(-1)(err()));
      return;
    }
    return callbackOriginal.call(this, p, ...args);
  };
}
const block = () => { throw new Error('Network disabled for offline checks'); };
globalThis.fetch = block;
http.request = block;
http.get = block;
https.request = block;
https.get = block;
syncBuiltinESMExports();
```

Encode that source with `encodeURIComponent` and pass `--import=data:text/javascript,<encoded source>` as temporary NODE_OPTIONS. This guard covers the APIs used by these checks, not every possible network transport; it is not a replacement for sandbox restrictions. Avoid additional browser/server/service checks. The full test run took approximately 668 seconds on this host; environment startup dominated the runtime.

Commands previously run with the guard:

```text
npm test -- --maxWorkers=2 --minWorkers=1
npm run lint -- --file <each of the 18 frontend paths above>
node ../../node_modules/typescript/bin/tsc --noEmit --incremental false
```

For focused frontend verification, select `src/lib/eventTime.test.ts`, `__tests__/src/components/events/EventForm.test.tsx`, and `__tests__/src/components/events/EventTimezone.test.tsx` with the same offline guard. All 24 cases passed in the final full suite. No raw stdout transcript files were saved; the observed results and failures are recorded above. Do not represent the full suite/typecheck as green.

### Safe next steps for another agent

1. Read repository instructions, confirm both Git roots/current statuses, and review only the task-owned file inventory. Preserve unrelated changes. Do not switch branches to recover work; it is in the current dirty files.
2. Independently review and run focused offline tests, particularly DST conversion, exact edit preservation, DB UTC boundaries, recurrence weekdays/intervals, and viewer date rollover.
3. Investigate the two full-suite failures and unrelated typecheck blockers only if the user authorizes expanding scope; do not silently fold those fixes into this work.
4. Browser/manual integration, a production build, independent code review, and rollout are not yet verified. Local UI checks would need isolated configuration and explicit authority; do not start a production-connected dev server.
5. Ask for separate authorization before staging/committing, creating/switching a branch, deploying, or correcting the existing MariaDB event. Before any event correction, confirm the intended time/zone with the organizer; this implementation does not repair its saved UTC instant.
