# Discussion discovery and following feed implementation plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task by task.

**Goal:** Give discussions the landing page's discovery column, keep all public discussions on `/discussions`, and reserve `/feed` for signed-in readers and followed authors.

**Architecture:** Reuse the existing event/discovery cards in a responsive discussion layout. Add an authenticated following-discussions endpoint using an optional protobuf filter and explicit service acknowledgement. Keep the existing public list and blog feed contracts unchanged. The personalized page validates its session and remains noindex.

**Tech stack:** Next.js 14, React Query, existing Monkeys UI, Gin, Go gRPC, PostgreSQL.

**Spec:** User's marked landing-page reference and explicit public-discussions/private-following-feed requirements in this chat.

## Constraints and design

- Work locally in the current visible directories and `codex/discussions-compatibility` branches. No production access, existing event data changes, commit or push.
- Reuse existing colors, typography and discovery components. Keep discussion content first on mobile and the marked side column on wide screens.
- All visible public site/group discussions remain discoverable regardless of follows. Never include private/group-only content in the following list merely because its author is followed.
- Following is checked in SQL before limiting or cursor pagination. Never filter one public page in the browser.
- Anonymous or invalid sessions cannot mount the personalized feed or request its data. Session outages fail closed with a recoverable message. `/feed` remains noindex.
- No schema migration. Existing users and API calls retain current behavior. New protobuf fields default to false; the gateway requires acknowledgement of filtering to avoid unsafe mixed-version fallback.
- The user runs protobuf generation. Do not run or generate a command for it.

## Tasks

### 1. Authenticated followed discussions

- [x] Add optional `following_only` request and response fields. User regenerated them.
- [x] Add failing SQL/service/gateway tests for authentication, followed-author filtering, cursor scope, unchanged public feed and mixed-version rejection.
- [x] Implement the filter using `user_follows`, preserving public visibility checks; wire a protected `/discussions/following` endpoint.
- [x] Run affected Go tests offline and review boundaries.

### 2. Responsive discussion discovery

- [x] Add a shared discussion layout and client discovery column reusing featured event and landing discovery cards.
- [x] Keep public metadata and discussion data independent of optional discovery failures.
- [x] Verify desktop side column and stacked mobile/tablet layout.

### 3. Private following page

- [x] Add failing tests for missing/invalid session, failed session validation, noindex metadata and followed-discussion request/paging.
- [x] Add a server session gate and a client auth gate; isolate personalized caches by verified account ID.
- [x] Retain existing followed-post pagination and add an independently paged Discussions view using only the authenticated following endpoint. Empty states link to public discovery and never substitute global content.
- [x] Run frontend tests, types and lint; test local guest and signed-in flows. Rebuild only gateway/discussions.

## Review focus

- A followed author publishing to a private group must not leak into the site or following feed.
- A cursor from a non-followed author must not be accepted into a following page.
- Signing out or switching accounts must not reuse another viewer's personalized cache.
- Old clients keep the global list; old discussion services cannot silently satisfy a following request.
- Discovery API errors must not hide discussion content or break replies.

## Decisions and verification

- Reused the marked reference's discovery column on the right at desktop widths; discussion content remains first on phones/tablets. Both discussion list and detail inherit the layout. No new palette or invented content.
- Kept followed posts and followed discussions as separate views with independent pagination. The existing blog following API contract remains unchanged.
- Review reproduced three session bugs before fixing them: discarded server identity, stale successful auth data surviving a failed revalidation, and a retry button that did not refetch client auth. The client now reconciles the verified server account with client auth before mounting queries, fails closed on revalidation errors, and retries the correct check. A second read-only review found no remaining actionable issues.
- Also reproduced stale following results after unfollowing. Successful follow/unfollow now resets both personalized query prefixes. Account-ID keys prevent cross-account reuse.
- Because `/feed` is private, removed it from the public sitemap and AI collection links. Public RSS remains available and links to the public homepage.
- Full frontend verification: 156 test files and 775 tests passed. TypeScript passed. Lint exited successfully with existing warnings. Logs: backend ignored `logs/following-frontend-suite.log`, `logs/following-feed-types.log`, `logs/following-feed-lint.log`.
- Backend database/service/gateway discussion packages passed with vet enabled and dependency downloads disabled. Tests cover public visibility, follow filtering before LIMIT, invalid cursors, authentication and rolling-upgrade acknowledgement. Log: `logs/following-backend-tests.log`.
- Rebuilt only the local discussions and gateway services, sequentially. Reloaded the existing local access proxy to resolve the recreated gateway's new container address. Loopback checks: health 200, public discussion list 200, unauthenticated following endpoint 401.
- Browser: the secondary synthetic local account could see the public discussion without following. Following its author added it to `/feed` Discussions; unfollowing removed it while `/discussions` retained it. Restored the original follow relationship, then signed out only that synthetic local session. `/feed` redirected to `/auth/login?callbackURL=%2Ffeed`; public discussion list/detail remained readable.
- Checked 390, 768 and 1366 pixel layouts without horizontal overflow. Restored the viewport and retained the main signed-in account. Screenshots in ignored `logs/discussions-discovery-desktop.png`, `logs/discussions-discovery-mobile.png`, and `logs/following-feed-mobile.png`.
- No production access, schema migration, event-data changes, commit, push or remote merge. Unrelated generated/configuration/user edits remain excluded.
