# Event co-host reliability implementation plan

> **For agentic workers:** Use superpowers:executing-plans and test-driven-development. Preserve the user's existing checkout and branch.

**Goal:** Notify users added as event co-hosts and correctly distinguish removing another co-host from leaving yourself.

**Architecture:** Keep the current immediate-grant behavior and existing REST/protobuf routes. Perform co-host changes, permissions and audit writes transactionally; publish notifications only after successful changes. Extend the existing RabbitMQ/FRN pipeline and mobile-first management UI, not a new invitation acceptance workflow.

**Tech stack:** Go, PostgreSQL, Gin/gRPC, RabbitMQ, FreeRangeNotify, React/Next.js, Vitest.

**Spec:** User requests in this chat, 25 October 2026. No production access, same branch, two local disposable test accounts, existing logged-in organizer, backward compatibility.

## Global constraints

- Work locally on codex/event-timezone-release in both repositories. User subsequently requested final mobile/tablet/desktop UI testing, then stage, commit and push relevant changes in both repositories. Do not merge or switch branches.
- Do not touch monkeys-admin, production endpoints, real email delivery, or existing event data.
- Keep existing co-host permissions and routes. Protobuf additions must be optional and wire-compatible. Organizer cannot leave or be removed.
- Additions at creation and later management both notify the affected co-host. Removal notifies the removed co-host; voluntary leave notifies the organizer and is audited as left.
- Keep existing non-co-host notification templates and behavior unchanged. New notifications are in-app/SSE only.
- Synthetic accounts and disposable events may be created locally for testing. Never print tokens, passwords, API keys or private event links.

## Review focus

1. Duplicate and concurrent additions must not grant/audit/notify twice.
2. A removed co-host must lose access; an actor cannot impersonate someone else or remove the organizer.
3. A self-leave must not become an error after a successful mutation because the page refresh now lacks host permission.
4. Failed transactions must not produce success notifications; notification transport errors must not undo valid event changes.
5. Notification previews must not mislabel a co-host removal as a co-author removal or disclose private access links.

## Task 1: Transactional co-host changes and delivery

Files: events/internal/database/cohosts.go and tests; events/internal/services/service.go, messaging.go and cohost tests; constants/actions.go and frn_templates.go; notification/internal/consumer/consumer.go and event_templates_test.go; documents/frn-event-group-templates.seed.json and template documentation.

Interfaces: Existing AddCoHost/RemoveCoHost RPCs and REST paths remain unchanged. Internal database mutation results may carry canonical actor, recipient and event identity for post-commit fanout. New action/template names are event_cohost_added, event_cohost_removed and event_cohost_left, with inapp/sse suffixes.

- [x] Add failing tests for successful create/add notification, no notification on failure, different remove/leave recipient and wording, duplicate add and transaction rollback.
- [x] Implement only after RED. Grant/revoke/audit atomically, normalize handles, protect the organizer, preserve existing permissions, publish post-commit through the existing pipeline.
- [x] Test notification consumer payloads and template seeds, including no email and canonical event/profile links.
- [x] Run changed Go packages offline and record results or toolchain blockers.

## Task 2: Management UI and notification presentation

Files: frontend EventManage.tsx, a focused EventCohosts component if useful, notificationPresentation.ts, focused component/presentation tests.

Interfaces: addCoHost(slug, username), removeCoHost(slug, username) unchanged. UI receives current viewer/session to identify self and navigates away after leave.

- [x] Write failing tests for Remove on another user, Leave co-host on self, confirmation/cancellation, busy protection, errors and retry, successful form reset, leave navigation and co-host notification copy.
- [x] Implement mobile wrapping, 44px controls, safe form reset captured before awaiting, clear confirmations and success/error states.
- [x] Run focused tests, lint and the full frontend suite. Report known unrelated baseline failures by name.

## Task 3: Local integration and end-to-end proof

- [x] Inspect the supplied local FreeRangeNotify checkout. Start only necessary local services with outbound delivery disabled and isolated data; never use production FRN credentials.
- [x] Rebuild only changed local Monkeys services using logs/local-restore.compose.yml.
- [x] Create two synthetic local accounts and a disposable event. Test creation with co-host, later addition, in-app notification links, duplicate add, remove, self-leave and revoked access.
- [x] Check mobile, tablet and desktop, save screenshot proof, restore the original signed-in organizer view.
- [x] Obtain fresh scoped review and address important findings with regression tests. User subsequently authorized relevant commits and pushes in both repositories.

## Progress and rulings

- Existing implementation grants access immediately, has no co-host notification producer/consumer, audits every deletion as removed and labels all delete buttons Remove.
- Ruling: preserve immediate grants; acceptance/decline invitations would change existing behavior and are not requested in this fix.
- The local Monkeys stack currently blocks FRN at 127.0.0.1:9. User identified C:/Users/Dave/the_monkeys/FreeRangeNotify as the local source; inspect it without using production credentials.
- Prior attendee/calendar fixes are committed separately (backend f8f4a56, frontend ac55d440). No co-host changes existed in those commits.
- Task 1: database audit/duplicate regressions observed RED then GREEN. Service missing-notification cases observed RED then GREEN. Consumer unrecognized-action cases observed RED then GREEN. All five changed Go packages passed locally; no real dependencies used by unit tests.
- Task 2: frontend worker completed 34 focused tests and scoped lint; new co-host controls use session identity, explicit confirmation, pending guards, retryable errors and safe form reset. Full suite before review fixes: 562 passed, 2 known unrelated failures (BlogPageClient members-only sharing and LandingEventsSection missing QueryClientProvider), 121 files.
- Final review: Feynman identified stale browser host caches after self-leave and global authorization-lock contention. Both accepted as P2; frontend worker is fixing cache clearing without privileged refetch. Backend contention regression observed RED, fixed using fixed-size atomic generation buckets so old in-flight responses cannot become fresh permission cache hits. Authx package passed after fix.
- Ruling: generation buckets are bounded at 1024. A rare slug hash collision causes an extra cache miss, not wrong permissions or a blocked unrelated RPC. PostgreSQL remains authoritative for writes across gateway instances.
- Local resources: only 0.5GB free of 15.3GB RAM during parallel compilation. Canceled this task's parallel build and switched to sequential service rebuilds. Stopped the verified leftover network-disabled Go test container; no unrelated user containers touched.
- Two disposable local users created through local APIs: cohost1_mvn9dypi and cohost2_mvn9dypi, with example.test emails. No production or existing-event writes. Original dave browser session remains on 127.0.0.1; disposable event form is prepared but not submitted until local FRN is ready.
- Root seed.templates.py has private-network/prod-config defaults; leave the user's untracked script untouched and reuse JSON seed files with explicit isolated FRN configuration.
- User approved recurring creation support. Added optional CreateSeriesReq.co_host_usernames field 21; user ran generation and replied done. Grant selected co-hosts on every newly created occurrence and notify each once for the series. Do not silently turn occurrence management into series-wide management.
- Isolated local FRN is healthy on the internal restore network with fresh app credentials and separate prefixed indexes. All 77 templates seeded without errors; repeat seed skipped 77. Root seed.templates.py and production configuration untouched.
- Browser created draft local-only-co-host-flow-verification-6bbe0cd8 with cohost1_mvn9dypi, added cohost2_mvn9dypi later, and showed duplicate-host error without a duplicate roster row. Never published.
- Real local RabbitMQ/FRN/gateway evidence: each successful addition produced exactly one in-app and one SSE record; duplicate produced none. UI removal produced exactly one removed in-app/SSE pair. Email and all external-channel counts were zero.
- API integration on that exact draft: active co-host can manage attendees (200), removed or voluntarily departed co-host immediately receives 404 (draft privacy policy), organizer removal rejected (400), duplicate add rejected (409), re-add immediately restores access (200). Both synthetic co-hosts then voluntarily left.
- Original organizer browser displayed two Co-host left notifications with actor profile links and the exact test-event links. Saved mobile proof. Mobile removal dialog fits 390px viewport without horizontal overflow. User explicitly requires existing Monkeys design tokens and button styles, no new colors; frontend worker updating confirmation CTA accordingly.
- Final co-host frontend tests: 36 tests passed across management, real one-time/recurring creation picker and notification presentation. Five changed Go packages passed offline after protobuf generation. Scoped lint and diff checks passed. Fresh review found no remaining P1/P2 issues.
- Recurring runtime: created exactly two unlisted disposable occurrences, selected duplicate/at-prefixed handles and the organizer. Each detail has only the intended co-host, with attendee-management access on both dates. FRN/gateway show exactly one in-app plus one SSE series invitation and zero external channels.
- Browser role tests used an additional disposable cohostui_mvn9dypi account on localhost, separate from the original organizer on 127.0.0.1. Host-added notification is visible with an event link; co-host rows correctly show Remove for others and Leave co-host for self; organizer-only Delete is absent. Cancel preserves access. Confirming self-leave navigates to Events without false mutation errors; reopening Manage shows You cannot manage this event.
- Responsive browser proof captured at 390, 768 and 1440px. All co-host controls remain 44px high. Tablet check exposed a pre-existing header search overflow; minimal existing-breakpoint adjustment uses compact search below 1024px, preserving appearance. Rechecked without horizontal overflow at all three widths. Confirmation CTA now uses the existing brand variant; closing dialog preserves its identity instead of flashing null.
- User requested unrelated full-suite failures be fixed before commit. Full run reproduced the pre-existing members-only post-sharing test; frontend worker owns its diagnosis/fix. A second worker owns the baseline landing event test missing its QueryClientProvider. No production access is authorized.
- Those two runtime failures were fixed: BlogReactions now honors its existing showShare prop without changing public defaults or styles; the landing event test uses the real production provider setup. Fresh full frontend run passed 573 tests in 123 files. Repository-wide lint passed with existing warnings.
- Type-checking exposed stale generated types for two deleted discussion routes and a stale local Zod 3 installation, despite the committed manifest/lock requiring Zod 4. Removed only those two generated type files (regenerable, no source changes). Restored the frozen lock using the repository's pnpm 10.10.0 and existing store, scripts disabled; Zod now matches locked 4.4.3, with no manifest or lockfile changes. Production schema types then passed; remaining test fixture/selector typings are being corrected before final verification.
- Final frozen-tree verification: all 573 frontend tests passed in 123 files with external network access blocked; full TypeScript checking passed after test-only fixture and selector type corrections. No auth schema, package manifest or lockfile changes were required.
- The running local Next process retained paths to replaced dependencies and showed an ENOENT compile overlay. Restarted the verified local development process and preserved only its regenerable webpack cache under ignored logs. Reloading the event page now shows the signed-in organizer and HSR Layout address without the compile overlay. No source or event data was removed.
- Delivery scope excludes unrelated discussion generated files, local logs, credentials, existing landing-page drafts and automatic tsconfig edits. Before production deployment, seed the six new FRN templates using the documented deployment process. Existing asynchronous transport behavior is retained; durable outbox delivery is not introduced or claimed.
