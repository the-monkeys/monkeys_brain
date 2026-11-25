# NSFW reporting and birthday access implementation plan

> For agentic workers: execute the approved plan using test-driven development; backend first in the actual checkout.

**Goal:** Consistent reporting and adult-only explicit reveal for articles, events, groups and discussions.
**Architecture:** Add an independent moderation RPC on the existing user service (ownership moved from events on 2026-11-25 at the user's request), with PostgreSQL rating/report/consent records. The gateway applies authoritative per-item policy after existing audience checks and before media streaming. The public and admin applications reuse existing designs.
**Tech stack:** Go/Gin/gRPC/PostgreSQL, Next.js, Svelte admin.
**Spec:** User-approved plan in this chat, 25 November 2026.

## Constraints
- Actual backend checkout: C:/Users/Dave/the_monkeys/the_monkeys_engine.
- Fresh main branches, preserve existing uncommitted and staged mobile work; no production or PR changes.
- Existing URLs, IDs, publication and audience checks stay intact.
- DOB is self-declared; missing DOB prompts profile completion; under-18 cannot reveal.
- Support receives only narrowly scoped NSFW moderation permission.
- No raw restricted content under a CSS blur or in anonymous JSON/SEO/media responses.
- Classification errors and unknown legacy scans are not proof of safety; no unvalidated automatic model.

## Tasks
- [x] Fetch main, inventory local changes, create actual backend branch.
- [x] RED/GREEN birthday boundaries and narrow staff permissions.
- [x] Add additive migrations, transactional durable reports, rating sources, audited review/reversal and revision-scoped reveal. Test duplicates, concurrency, privacy and roles.
- [x] Register independent typed moderation RPC without regenerating dirty existing bindings.
- [x] Add explicit bounded content-response filtering and pre-stream media gates, preserving pagination and original ACLs. Test all aliases, fail-closed errors and warm caches.
- [x] Integrate legacy event report and admin flag actions with the same policy.
- [ ] Test backend locally, review, commit only feature files and push.
- [ ] Fresh public/admin branches, reuse menu/report/gate designs, DOB return flow and admin review queue.
- [ ] Test responsive UI, anonymous SSR/SEO/Studio/snap, account changes and old API fallback.
- [ ] Commit and push relevant frontend/admin changes, no PR.

## Review focus
- Mutable DOB and a cached reveal must never grant access after eligibility changes.
- Private/deleted/group-only content remains inaccessible even after adult reveal.
- Shared assets and old media aliases cannot bypass a restriction.
- Retried actions do not produce multiple reports/audits; reversal invalidates old reveals.
- Non-NSFW reports never automatically classify content as NSFW.

## Decisions
- Existing detector hooks are metadata only; automatic model selection requires measured local evaluation.
- Use explicit known envelope fields, never recursively interpret arbitrary article blocks as content identities.
- Already cached/downloaded originals cannot be retracted by application code; deployment must purge prior public caches and prevent direct object-store bypass.

## Backend verification
- Real PostgreSQL policy/store/RPC integration suite passed on disposable localhost database (81.736s final combined run); additional private/shared asset regression passed.
- Gateway, events and storage suites passed; targeted fresh-eligibility and sanitized legacy-adapter regressions passed afterward.
- go vet and feature diff checks passed. Existing backend patch verified unchanged.
- Public frontend work uses a normal project-local checkout at local/the_monkeys-nsfw; the staged mobile checkout is untouched.
