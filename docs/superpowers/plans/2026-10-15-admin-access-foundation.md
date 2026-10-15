# Admin Access Foundation Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans for the implementation and regression checks.

**Goal:** Replace guessed staff access with explicit server permissions, restrict existing financial actions, protect verification decisions, and make staff navigation usable on smaller screens.

**Architecture:** Keep existing public and admin routes compatible. A shared server permission policy drives the new admin session response and staff API guards. The separate Svelte admin app consumes that response and refuses unavailable or invalid access instead of inferring roles from other APIs.

**Tech Stack:** Go, Gin, existing gRPC contracts, Svelte 5, TypeScript, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-14-platform-admin-feature-plan.md`, access foundation and Phase 0.

## Global constraints

- Work in the visible checkout, on `codex/admin-access-foundation`.
- Preserve existing unrelated changes and public API response contracts.
- Do not run or generate a protoc command or hand-edit generated protobuf files. Ask the user to perform protobuf generation when a required contract is ready.
- No production requests, payment transfers, applied migrations, commits, or pushes in this slice.
- Existing role names and assignments remain compatible. Existing settlement access becomes Admin-only while dedicated finance grants are designed.
- Local admin files are ignored by the parent repository; document their modified paths and preserve them in place. Repository ownership is still a business decision.

## Review focus

- Unknown roles, missing identities, bad session payloads, and API failures must never imply access.
- A direct URL must not mount a forbidden admin page or issue its data requests.
- Legacy verification review must enforce the same permission as the staff route, including at its service boundary.
- Staff must retain a working navigation/logout path at phone and tablet widths.
- Existing active/legacy account states stay compatible while suspended users lose authentication.

## Task 1: shared staff policy and guarded gateway session

Files: `common/staffaccess/`, `microservices/the_monkeys_gateway/internal/admin/rbac.go`, `routes.go`, new `session.go`, and route/session tests.

Produces: `staffaccess.Permissions(role string) []string`, `staffaccess.Has(role, permission string) bool`, `RequirePermission`, and `GET /api/v1/admin/session` returning username, account_id, role, permissions.

- [x] Write failing permission/session tests, including unknown role denial and Community payment denial on the actual route registrations.
- [x] Implement an explicit permission policy for existing roles and use it in staff route registration.
- [x] Return a non-cacheable staff session with no sensitive account fields.
- [x] Run the common/staffaccess and gateway admin package tests.

## Task 2: admin app consumes explicit access

Files: `local/monkeys-admin/src/lib/api.ts`, `session.ts`, `session.test.ts`, `api.test.ts`, `src/App.svelte`, `components/SideNav.svelte`.

Consumes the session contract from Task 1. Produces `Session`, validated permission-aware navigation and `canVisit(path, permissions)`.

- [x] Write failing tests for the single session request, denied/invalid/unavailable access, and navigation/direct-route restrictions.
- [x] Remove role inference from statistics/list responses.
- [x] Gate page mounting and clear the session on permission/session failure.
- [x] Add recoverable loading/error/access-denied states.
- [x] Run admin Vitest, Svelte check, and production build.

## Task 3: close legacy verification and suspension gaps

Files: gateway auth review handler/tests; authz verification/service tests; authz token validation and JWT utility/tests.

Consumes shared staff policy. Existing protobuf inputs/outputs remain unchanged.

- [x] Reproduce nonstaff verification review and suspended token/login acceptance with tests.
- [x] Guard the legacy gateway review handler and recheck the reviewer from database state in the authz service.
- [x] Deny suspended accounts during token issuance and current-account validation/refresh. Preserve other existing account states.
- [x] Run related gateway auth and authz package tests.

## Task 4: reachable mobile staff navigation and current API documentation

Files: admin `SideNav.svelte`, `App.svelte`, `app.css`, `README.md`; `docs/admin-api.md`.

- [x] Add a compact, accessible menu with route selection and logout at smaller widths.
- [ ] Keep desktop navigation and page content usable without overflow (browser validation still pending).
- [x] Document the new session response, permissions, Admin-only settlement access, rollout order, and ignored local source files.
- [ ] Run the complete admin checks and the affected backend packages; inspect the UI if local runtime access is available.

## Task 5: finance contract handoff and tracker

- [x] Inventory the existing event protobuf messages against the next finance feature's requirements.
- [x] Prepare additive summary balances, capture/refund evidence and settlement dates/actor fields.
- [x] Ask the user to run their protobuf generation process. User completed generation on 15 October 2026; generated output is preserved, including unrelated discussion output.
- [x] Add SQL-boundary regression tests for reserved versus paid balances, signed recovery balances, host search, and payment history evidence; watch them fail before implementation.
- [x] Populate the additive fields through the database and HTTP gateway without removing legacy fields.
- [x] Display the breakdown and existing manual settlement history in the mobile-first admin app, with missing legacy data shown as unavailable rather than zero.
- [ ] Verify backend packages, the complete admin suite/check/build and obtain one fresh read-only review.

This tracker is not the full immutable accounting ledger, payout approval workflow, or payment-provider reconciliation system described in the feature spec. Those remain separate follow-on work; the existing manual settlement actions do not transfer funds.

## Execution ledger

- Plan prepared after the user approved the feature design and authorised proceeding on 15 October 2026.
- Ruling: retain the current visible checkout, per the user's standing preference. A feature branch protects main; no separate worktree is created.
- Ruling: role-based permissions are an initial compatibility layer, not the complete configurable multi-role finance model. Financial routes become Admin-only immediately; custom finance assignments require their own contract/migration work.
- Ruling: keep the existing nonfinancial Community access for this slice to avoid silently revoking unrelated staff workflows. Later granular staff assignments will replace that broad legacy role deliberately.
- Baseline: original admin tests and affected backend packages passed. Initial sandboxed admin Vitest hit a filesystem EPERM; the approved unsandboxed rerun passed.
- Tasks 1-3: implementation and regression runs passed before the user's generation: shared policy, actual staff route/session boundaries, explicit frontend session/navigation, legacy verification guards, suspended login/access/refresh rejection. Admin suite: 13 passed; Svelte check: zero errors/warnings; production bundle built.
- Task 4: compact mobile navigation implemented and checked by the Svelte compiler; live authenticated browser validation is still pending. API/readme documentation is being updated.
- Task 5: SQL-boundary tests reproduced the absent split-balance aggregates, missing host search and missing transaction evidence. Generated Go getters verified before dependent implementation.
- Ruling: preserve legacy `settled` as pending plus paid; expose new reserved/paid fields and signed host balance additively. This avoids changing old readers; callers must move to the new fields to distinguish a reservation from money paid.
- Compatibility regression: watched `TestOlderEventServiceDoesNotInventPaidBalances` fail because absent additive messages became zeros. Gateway now returns null for missing new summary fields during rolling upgrades.
- Tracker regression: event database suite and gateway admin suite passed after SQL/JSON changes. Admin suite now has 16 passing tests; Svelte check zero errors/warnings; production build passed (JS 96.81 kB, gzip 32.38 kB).
- Browser attempt: opened local admin on port 5174. The gateway connection to 127.0.0.1:8081 was refused; UI showed the intended access-check failure/retry/logout screen. No authenticated finance/mobile claims are made from this attempt.
- Final fresh read-only review dispatched. Full backend suite and affected gateway/authz/events packages are running; results pending.
- Review returned no Critical findings and two Important findings: strict role-type validation and stale verification-evidence selection. Both are accepted for the single regression/fix pass.
- Final: minor (deferred): refocusing the app currently remounts the active page during session revalidation, so selection/draft notes are lost. A later draft-preserving revalidation flow must keep controls blocked until access is rechecked.
- Final: Ruling: the full financial ledger, approvals, payout execution/reconciliation and redesigned settlement writes remain follow-on work, not silently represented as complete by this tracker. Cost: manual settlement records still require external operational review.
- Final: Ruling: preserve unrelated generated discussion output and unrelated untracked artifacts untouched; only review/ship relevant authored files. Cost: their ownership must be checked separately before any future staging.
- Final: Ruling: authenticated mobile/desktop validation remains pending because the local gateway is unavailable, rather than inferring usability from compilation. Cost: screen-level integration defects may remain until the local services are running.
- Final: Ruling: admin repository ownership stays unresolved rather than force-adding ignored local source. Cost: backend commits alone cannot back up or ship the admin UI.
- Final: fixed malformed session role coercion: the array-role fixture resolved successfully before the fix; strict string checking now rejects it. Full admin suite passed after the fix.
- Final: fixed cross-request verification evidence: extracted a selection-scoped loader with controlled out-of-order response, late failure, missing-document and clear/invalidation regressions. Request/profile/documents publish together; tab changes/unmount invalidate pending work; decisions require ready evidence and a matching current request. Row/tab changes are blocked during a decision. Full admin suite: 20/20; Svelte check zero errors/warnings; build passed (JS 97.67 kB, gzip 32.71 kB).
- Verification note: the first full backend run compiled the intentionally failing missing-refund fallback test before its fix and reported that one failure; other packages passed. The subsequent gateway admin run passed. A fresh full run after the fix is pending. The redundant affected-package run was cancelled to reduce local compilation load.
- Runtime authorisation: user authorised local Compose startup, building only changed services. Started existing local database/cache/queue/storage images with preserved volumes, no builds and no migrations. Building only authz/events/gateway with dependency startup disabled; unrelated applications are not rebuilt.
- Runtime database preflight: read-only `to_regclass` check confirmed existing `event_payments`, `event_host_settlements` and `user_account` tables.
- Full backend verification retry: sandbox denied a cached Elasticsearch source file (`nodeinfooscpu.go`), causing build failures outside the changed packages as well. Retrying the full suite with approved elevated access and `-p 2`; do not describe the sandbox run as green.
- Elevated full run: changed policy/admin/auth/authz/event packages and the other reported packages passed, but the unchanged `microservices/the_monkeys_users/internal/services` test process was killed for exceeding the test deadline. The full run is therefore not green. An immediate isolated `go test -v -timeout 30s ./microservices/the_monkeys_users/internal/services` passed all three tests (`TestErrIfRemovalBlocked`, `TestRemoveUserFromEventsAndGroupsFailsClosedWithoutClients`, `TestVerificationObjectKey`). The timeout did not reproduce; no unrelated source was modified to hide it.
- Runtime handoff: selected authz/events/gateway builds completed successfully and all three containers reported healthy. Local `/healthz` returned 200 and an unauthenticated `/api/v1/admin/session` returned 401. Refreshed the admin browser to `http://127.0.0.1:5174/login`; the real login form is visible. Staff sign-in is required before authenticated desktop/mobile validation can continue. Do not grant or impersonate an account to bypass this step.
- No staging, commits, pushes, migrations or money transfers performed. Work remains in the visible checkout on `codex/admin-access-foundation`.

### Ignored admin source changed in place

All paths below are under `local/monkeys-admin/`. This list is not a backup; the files remain ignored by the backend repository:

- `README.md`
- `src/App.svelte`
- `src/app.css`
- `src/components/SideNav.svelte`
- `src/pages/Payments.svelte`
- `src/pages/Verifications.svelte`
- `src/lib/api.ts` and `src/lib/api.test.ts`
- `src/lib/session.ts` and `src/lib/session.test.ts`
- `src/lib/payments.ts` and `src/lib/payments.test.ts` (new)
- `src/lib/verification-selection.ts` and `src/lib/verification-selection.test.ts` (new)

## Subsequent verification and delivery handoff

The earlier execution entries above are historical, not the current UI status.

- All 15 long-running local Compose services reached healthy status after the user authorized starting the full stack except AI. The migration container exited successfully with no change.
- The supplied account signed in as Community. Real read-only checks loaded overview, users, posts, events, groups, verification queues and existing private evidence. Payment navigation was absent, and a direct payment URL remained unavailable. No roles, verification decisions, content or payment records were changed during browser tests.
- The approved light-first operations-console redesign now covers the existing admin screens, with optional dark mode, responsive catalogs, native dialogs, deliberate mutation confirmations and explicit loading/error/empty recovery states. Successful same-identity revalidation preserves drafts while temporarily blocking controls; failures clear protected data.
- Review corrections added verification history pagination, actual attached-image readiness before decisions, and a keyboard-focus fallback after dialogs close. Admin verification passed 90 tests, Svelte checking with zero errors/warnings, and the production build.
- The admin source is still ignored and has no separate repository. It is not included in the backend delivery. Its README and RESUME describe the source-ownership and deployment requirements.
- Admin-only payment acceptance testing remains pending with an authorized Admin account. The current payment controls are still manual bookkeeping, not automated transfers, approvals or a complete immutable ledger.
- The user subsequently authorized staging, committing and pushing only the relevant backend work on `codex/admin-access-foundation`. Unrelated discussion generation, local artifacts and ignored frontend files remain excluded.
- Pre-commit backend verification on 16 October 2026: `go test -p 2 -timeout 120s ./...` completed with exit zero, including the users-service tests that had timed out in the historical run. Authored Go files passed the formatting check, staged changes passed `git diff --cached --check`, and the staged-additions scan found no high-risk secret patterns. No production deployment or database update was performed.
