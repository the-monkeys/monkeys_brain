# NSFW Search Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement task-by-task.

**Goal:** Let verified indexing crawlers read published public NSFW articles while retaining human age gates and normal SEO.
**Architecture:** Existing Gateway verifies a target-bound server assertion and official provider IP ranges; existing User Service policy remains authoritative. Next renders an escaped crawler article without visitor hydration. No new service or database change.
**Tech Stack:** Go/Gin, existing moderation RPC, Next 14/React, Vitest.
**Spec:** User-approved NSFW SEO proposal and subsequent explicit instructions on original content and backward compatibility.

## Global Constraints
- Use existing branches in actual checkouts; preserve staged mobile and unrelated changes.
- Preserve author text, IDs, canonical URLs, age checks, reporting, profile updates and design.
- No preview approval queue; explicitness alone does not exclude a public article.
- No production changes or new Compose procedure, service or migration.
- Feature fails closed without trusted ingress configuration and official crawler verification.
- Old/new frontend/backend combinations retain existing protections.
- Article scope; other resources remain regression controls.
- Test, commit and push relevant changes only; no PR.

## Review Focus
- Forged IP/header assertions must never grant access.
- Crawler cache must never become an anonymous/minor response.
- Old backend must not lead to indexing redacted placeholders.
- Private/deleted changes require fresh public policy checks.
- RSC/prefetch headers cannot bypass provenance checks.

## Task 1: Backend crawler verification and narrow exception
Files: internal/searchcrawler verifier/tests; moderation/filter.go and crawler tests.
Interfaces: Verifier.Verify(*http.Request) bool, Enabled() bool; existing moderation RPC unchanged.
- [x] RED: signed official IP, no/short key, spoofed headers, target/method/query replay, expired/future assertions, IPv6, stale lists.
- [x] GREEN: HMAC binds GET/path/IP/original agent/time; bounded cached official IP refresh; no DNS on user requests.
- [x] RED/GREEN: anonymous fresh public policy, public article detail only, no list/private/draft bypass, unchanged reveal flags, allowlisted crawler fields, no-store.
- [x] Existing and new moderation/Gateway suites pass.

## Task 2: Frontend authenticated provenance and rendering
Files: server assertion helper/tests, blogData.ts, blogSeo.ts, page.tsx, optional Blog capability fields.
Interfaces: server helper returns backend assertion only for authenticated ingress document requests.
- [x] RED/GREEN: missing config, invalid independent ingress secret, fake agent/IP, cookies, RSC and prefetch rejection.
- [x] RED/GREEN: bounded server crawler loader, original escaped article text, truthful registration schema, preview suppression, no visitor hydration.
- [x] New frontend with old backend retains noindex/gate without capability.
- [x] Normal page SEO and viewer rendering tests remain unchanged.

## Task 3: Sitemap-only discovery
Files: seoCatalog.ts, sitemap.ts and focused tests.
- [x] RED/GREEN: include only canonical locations for public NSFW metadata carrying backend capability.
- [x] Keep shared public-content filtering, RSS, exports and snapshots unchanged.
- [x] Private/draft/future/unavailable fixtures remain excluded; canonical fallback preserved.

## Task 4: Verification, documentation and delivery
- [x] Document private ingress/signing settings and default-off behavior.
- [x] Full frontend suite/build; affected backend suites and mixed-version tests.
- [x] Built-in browser human journeys at desktop/tablet/mobile; local HTTP spoof/cache checks.
- [x] Independent whole-change review and tests for important findings.
- [x] Preserve protected unrelated diffs, local data and services.
- [x] Commit/push verified changes; no PR. Backend implementation c744e9bd and frontend e18716bf were pushed successfully; this final plan update records delivery.

## Rulings
- User approved implementation inline; no repeated confirmation.
- Production ingress is unverified: do not claim live crawling enabled. Opt-in requires authenticated ingress headers and private keys. Local fixtures cannot prove live Google access.
- Existing NSFW flag is not a sexual-content taxonomy: no blanket adult tag. Retain accurate 18+ registration markup and allow verified Google classification.
- Original restricted images remain protected; this implements article-text discovery, not public image search.

## Verification rulings and review fixes
- Ruling: preserve the actual stored canonical slug, not the alias used to open a fixture; changing it would violate issued URL compatibility.
- Fixed: legacy public catalog rows omit audience. Successful current anonymous moderation policy now establishes public visibility before issuing the optional capability. Regression proved missing audience and private denial.
- Fixed: crawler projection now retains Markdown, research fields, quote attribution, checklists and chart text. Punctuation rewriting was regraded as important under the author's-content requirement and removed. Regression failed before and passed after.
- Fixed: middleware classifies observable document versus subrequest headers before Server Component signing. Real Flight/RSC prefetch is protected. A header-only probe normalized by Next as HTML remains an allowed document only for a genuinely verified indexing crawler; classification alone never authorizes access.
- Ruling: Search Console's official Google-InspectionTool uses the same verified common-crawler IP list; unrelated Google fetchers remain excluded.
- Ruling: text snippets are permitted in verified crawler output after the user's clarification; an all-text snippet ban would undermine requested AI-search discovery. Human metadata stays generic and original images remain protected.
- Ruling: production ingress and actual provider crawl/index outcomes remain post-deployment verification. No local test substitutes for those checks.
- Automatic approval review rejected replacing the original running Gateway. All original services were preserved; separate local test copies and a test-only proxy were used instead.

- Fixed: public-catalog source selection and metadata now preserve an optional original slug so sitemap discovery matches the existing detail canonical. Explicit canonical_slug retains priority; ordinary public canonical/RSS rules are unchanged. Query, projection and editorial compatibility regressions passed.

## Final verification
- Frontend: 279 test files / 2,479 tests passed; full types, lint (existing warnings), optimized build passed.
- Backend: Blog, Gateway and common moderation suites passed; both existing service images built locally.
- Live final UI on port 3000: 95 HTTP checks passed. Built-in browser minor/adult smoke passed after serving final build; responsive article/homepage checks passed at 320/820/1440.
- Detailed redacted report and source manifest are committed in frontend docs/benchmarks/2026-11-27-nsfw-search-discovery.md.
- Production rollout remains disabled until the trusted ingress contract is independently verified/configured. No production changes, PR or data deletion.
