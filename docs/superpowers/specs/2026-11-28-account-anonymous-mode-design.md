# Account-wide anonymous mode

Date: 28 November 2026
Status: Design for user review; implementation is not approved by this document.

## Intent and agreed product behavior

Let signed-in members write without publicly revealing their account identity. This is a reversible account setting, not a permanent anonymous publishing option on individual posts.

Agreed with the user:

- Enable the existing Go Anonymous control in Account settings using the existing design language.
- While enabled, existing and new articles and discussions remain visible, subject to their current publication, membership, moderation, and NSFW restrictions. Public attribution becomes **Anonymous user**.
- Hide the member's identifying name, username, personal avatar, and public profile links wherever their content is presented. Exclude the profile from search and hide its identity from direct profile visitors.
- Turning the setting off restores real attribution on all their articles and discussions, including those written while anonymous. Communicate this consequence before saving the change.
- Members hosting upcoming or ongoing events cannot enable anonymous mode. Members owning groups cannot enable it while that ownership remains; ownership must first be transferred if a supported transfer route is available.
- Anonymous members cannot create events or groups. Do not reveal their identity automatically to let those actions proceed.
- Preserve account ownership, content, identifiers, permissions, email, phone, birthday, moderation history, and NSFW age checks.
- Reuse existing services. Do not add a production service or change staged mobile work.

## Approaches considered and selected

1. **Selected: one account privacy setting with server-controlled public attribution.** User Service stores the setting. Public content delivery applies its current value without changing the author or the authored text. This supports reversible behavior, preserves ownership, and avoids rewriting every content record on each toggle.
2. Rewrite author fields in every article and discussion on every toggle. Rejected: introduces partial updates across PostgreSQL and Elasticsearch, complicates reversals, and risks corrupting ownership or leaving identifying copies behind.

The user approved basing the design on approach 1. The implementation details below are proposed engineering decisions for review.

## Current implementation findings

- Frontend main includes the merged popup-disable change. Both repositories were refreshed from main before creating `codex/account-anonymous-mode` branches.
- `apps/the_monkeys/src/app/settings/components/account/Visibility.tsx` contains a disabled switch with no persistence handler. `Account.tsx` marks the section Upcoming.
- User Service owns PostgreSQL account records. Gateway exposes public profiles, authenticated profiles, and account-ID detail lookups; public disclosure must be controlled consistently across them.
- Public profile server rendering currently caches profile reads for five minutes. Its missing-profile metadata also includes the requested username. Both require explicit treatment for hidden profiles.
- Articles carry owner IDs and author data; Gateway, SSR, cards, search, images, feeds, and generated metadata can expose identity independently of a visible author label.
- Events have published/live/completed/cancelled/draft statuses, explicit start/end times, event-series ownership, and additional host roles. A targeted search found no ownership-transfer operation in the existing Group Service/protobuf contract; do not promise a transfer control without verifying or separately agreeing that capability. Groups have an explicit organizer and lifecycle status.
- Local Monkeys containers and both UIs remain stopped at the user's request. No local data was deleted.

## Public and private identity contract

The real account and author association remain unchanged internally. Account owners continue to access their private settings, drafts, library, studio, and management functions through existing authentication. Moderation retains server-side attribution under existing authorization; anonymity must not add access to private contact details for moderators or other members.

A shared server policy resolves the public view of an author. For anonymous accounts it emits the anonymous label, a generic non-personal avatar, and no resolvable member profile link or identifying author fields. Do not create a shared fake user account, publish the real owner ID as an anonymous identifier, or let a public client request an unmasked representation.

Audit actual identity-bearing fields rather than masking only known display-name fields: nested author objects, username arrays, account IDs, avatar URLs, social handles, bylines, follow targets, mention previews, structured data, share images, resource owner fields, and creator-specific links. Keep content IDs and canonical content URLs unchanged.

Do not weaken authorization by removing IDs internally. Public ownership controls should use server-derived capabilities such as whether the current viewer may edit, rather than requiring disclosure of the author's real public identity. Existing permission checks remain authoritative.

The author's text, uploaded content, and content URLs are not rewritten. Content may itself identify its author; this feature cannot remove self-disclosure or erase information another person has already saved. The product must not promise retroactive erasure from search engines, browser caches, sent emails, screenshots, or third-party copies.

## Data and existing services

Proposed additive PostgreSQL migration: account-level `is_anonymous`, non-null and default false, plus a monotonically increasing identity visibility version for invalidation and stale-write protection. Existing users retain today's behavior. Preserve existing account IDs and all content ownership references.

Add authenticated self-service reads/writes within the existing Gateway/User Service boundary. Derive the account from the authenticated session; never accept an arbitrary target account from the browser. A save returns the confirmed setting, version, and safe eligibility information for that owner. Retrying the same desired state is idempotent.

Use additive protobuf fields and RPCs without renumbering existing fields. Keep public response shapes compatible where possible, using neutral values and additive anonymity/capability fields. Optional frontend flags alone cannot secure old clients: old public clients must receive already-redacted data.

Resolve author privacy in batches for list responses to avoid a per-card network request. Blog Service remains on its existing storage boundary; use existing trusted inter-service communication rather than giving it a new PostgreSQL connection. Gateway must also sanitize all externally reachable versions of relevant APIs, and content services must not expose alternate unguarded public routes.

If current privacy cannot be established, return a non-identifying fallback or a temporary error. Never fall back to cached real identity. Keep private ownership and NSFW eligibility independent of public attribution.

## Event and group eligibility

Proposed detailed interpretation:

- Block for an organizer or publicly identified co-host of a published/live event that has not ended, including private/group events whose participants need an identifiable host.
- Account for real time boundaries, time zones, ongoing events, and active recurring series. Do not rely exclusively on discovery results or a first page of events.
- Completed/cancelled events do not block. Draft events alone do not block, but anonymous accounts cannot create, publish, schedule, resume, or accept host assignments that would evade the hosting rule.
- Block while a group records the member as its current owner/organizer, regardless of whether that group is currently listed publicly. Do not silently transfer ownership, delete a group, or change existing roles.
- Ordinary attendance and ordinary group membership are not ownership blockers. Public identity shown in their participation surfaces must still follow the account privacy setting.
- Do not make a private audit log public. Historical event attribution and public participation views must not become alternative identity lookups for anonymous accounts.
- Check new host assignments and group ownership transfers too, so an anonymous account cannot acquire a forbidden role through a second route.

Eligibility must be enforced on the server and checked atomically with the setting change. Event/group writes and anonymity writes need compatible locking/transaction ordering against the same account so concurrent requests cannot both succeed. Do not rely on an asynchronously updated search index for this eligibility decision. If a required check fails or its data is unavailable, leave the existing account state unchanged.

Blocked saves identify the owner's blocking resources only through existing authorized views. Do not disclose private events or group details to another user. If no supported group ownership transfer feature exists, explain the restriction accurately; do not introduce an unrelated transfer workflow or link to an unavailable action without further scope agreement.

## Profiles, search, metadata and media

Public profile and account-detail lookups must not return identifying data for an anonymous account, whether the visitor is signed out or signed in as somebody else. Use a generic unavailable-profile response consistent with a missing public profile. Owners retain private Settings access, and authorized moderation uses existing protected routes.

Hidden-profile HTML, page titles, descriptions, headings, hydration payloads, Open Graph/Twitter tags, JSON-LD, and social-image endpoints must not echo the identifying username or name. Do not expose a public Person/ProfilePage object for a hidden profile.

Filter anonymous profiles from all member search, autocomplete, recommendation and discovery results, and profile sitemaps. Content remains eligible for its existing content search and SEO rules, with anonymous attribution. Keep article/discussion IDs, publication data, canonical URLs and NSFW search policy unchanged. Trusted search/AI crawlers do not get an anonymity bypass.

Cover RSS, content exports, author pages, author-specific filters, sitemap-associated media, cached/profile image paths, mentions, follow lists and any public activity streams which could restore the identity. For already indexed pages, serve the new privacy-safe response and support cache purge/revalidation; external recrawl timing is outside our control.

## Immediate updates and caching

Do not persist the new anonymous state optimistically before the server accepts it. Pending/failed saves keep the prior setting and show the existing form's clear success/error treatment.

A successful save must coordinate visibility-version changes, public cache invalidation, and client query refresh. Invalidate affected public profile, account-detail, member-search, author/media, content-list/detail, SSR/metadata, CDN and client caches. In supported updated clients, include existing open tabs' refresh behavior and responses already in flight; identity visibility versions must prevent those clients from applying an older response after observing a successful toggle.

The server safety requirement applies to public requests admitted after an acknowledged enable operation: those requests must not disclose the old identity. A response already generated or transmitted for an earlier request cannot be recalled, even if network delay causes it to arrive after the save confirmation. If asynchronous invalidation alone cannot meet that, use an authoritative privacy check on delivery and remove unsafe response caching on affected routes. Keep batching/request-local deduplication so unrelated pages do not become slower. Keep privacy unknown states neutral instead of briefly flashing the real name.

Already downloaded, transmitted, or displayed information cannot be recalled from other people's browsers. Updated clients will reject stale visibility versions they can identify; older clients and third-party consumers cannot be guaranteed to discard earlier responses or immediately erase an existing display. Any limitation involving browser back/forward or a previously loaded page must be described accurately and tested without promising impossible erasure.

## Frontend behavior and design

Reuse the existing Settings Account section, switch, typography, spacing, theme, and responsive layout. Remove Upcoming only once the backend contract is available. Present the saved state correctly after reload and sign-in. Use a short confirmation explaining that disabling mode restores attribution across all articles and discussions.

Blocked activation explains the relevant hosting/ownership restriction and links only to existing owner management screens. Create-event/group entry points explain that public identity is required, while the server prevents direct API bypass. Preserve access to drafts and resource management under the appropriate permissions without publishing a forbidden public role.

Anonymous bylines use the current author row spacing and generic avatar, omit verification badges and personal profile links, and do not lead to a fabricated anonymous profile. Preserve reading, reporting, reactions, bookmarks, and applicable editing permissions. Email, DOB and phone stay private in both modes.

## Compatibility and deployment gate

Use additive migrations and contracts. Deploy backend changes before enabling the frontend switch. Keep activation off until every public serving path and the deployed frontend can safely handle anonymous data. Existing accounts default to identified; no automatic enrollment or content rewrites.

An old frontend against the new backend must receive neutral, already-redacted data on new public requests after activation. Previously received or in-flight responses remain subject to the explicit disclosure boundary above. A new frontend against an older backend must keep the switch unavailable and preserve all existing settings behavior. Mixed backend versions must not serve unmasked paths after activation; do not claim ordinary protobuf compatibility alone proves this privacy property.

Once any account enables anonymity, a rollback must retain server-side redaction. Disable new activations if necessary, but do not roll back to a server version that ignores stored anonymous settings. Keep migrations/data and a privacy-safe recovery path. Turning off a feature flag must not silently expose existing anonymous users.

Production deployment continues through the current Compose image pull/up workflow. Add no new production service. Document any eventual activation configuration and deployment ordering with the implementation; do not modify production while designing or testing.

## Verification required before release

1. Migration/defaults, idempotent enable/disable, unauthorized writes, missing/failed dependencies, and additive contract/version compatibility.
2. Existing and new articles/discussions switch attribution on/off without changing text, IDs, publication state, or ownership; anonymous users retain only their legitimate private/edit capabilities.
3. Signed-out, other signed-in, owner, staff, and trusted/fake crawler reads across all public/API/SSR/metadata/media/search paths, with planted identifying markers checked in rendered HTML and raw payloads.
4. Upcoming/ongoing/completed/cancelled/draft/private and recurring events, host/co-host roles, group ownership, new creation, role assignment, transfers, pagination, time zones, and concurrent toggle/create/assign attempts.
5. Immediate saves across Settings, cards, details, profile, search, images and updated open tabs; supported clients reject older versions after observing a toggle, and new requests with warm caches cannot resurrect identity. Test pre-toggle delayed responses separately and document the limits for older clients. Failure and retry behavior is explicit.
6. NSFW minor/no-DOB/adult/signed-out matrix, inline reveal, reporting, owner-private email/DOB/phone, and existing ordinary-account journeys remain intact.
7. Desktop, tablet, mobile, both themes, keyboard/screen-reader behavior, loading, error and long-label states. Preserve the established design.
8. Targeted tests first, affected backend service builds/tests, full frontend tests, lint, type checks, production build, and meaningful local browser journeys. Record actual passes/failures/untested areas separately.
9. Keep staged mobile and unrelated patches byte-for-byte unchanged. Use GorillaGigabytes for any new commits/pushes. Do not create or merge PRs.

## Scope boundaries and review

This work changes public attribution and profile discoverability. It does not implement a second account, permanent per-post pseudonyms, separate age verification, deletion/erasure of historical external copies, a new moderation service, or automatic rewriting of authored content.

Engineering decisions requiring confirmation in this design are the treatment of public co-hosts and recurring-series ownership, group ownership regardless of listing state, hiding public follower/mention/activity identities, and the privacy-safe rollout/rollback gates. These complete the agreed account-wide privacy behavior; they do not authorize implementation until the user approves this document.

## Design review outcome

An independent read-only builder review found one material ambiguity: the privacy acknowledgement boundary for previously transmitted or in-flight responses. This revision explicitly scopes the server guarantee to new requests after acknowledged activation and stale-result rejection in supported updated clients. It excludes recalling transmitted bytes or forcing older clients to erase a previous display. No other important planning blockers were reported. No application tests have been run for this design-only change.
