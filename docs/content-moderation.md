# Content moderation and NSFW access

This feature adds ratings and reports without changing publication status, article
IDs, slugs, or existing audience permissions. It uses the primary PostgreSQL
connection of the user service and an independent ModerationService RPC, hosted
on the existing user gRPC server. No extra service process, container, port or
database pool is required. The gateway sends all content reporting, staff review,
age/reveal and media-policy checks to this one user-service endpoint.

## Access policy
A restricted item returns a safe placeholder until an authenticated viewer has
a valid self-declared profile birthday showing age 18 or older and explicitly
reveals that item. Missing/future birthdays never grant access. Age is evaluated
again on every policy/media read; a stored reveal is not proof of eligibility.
February 29 birthdays turn eighteen on March 1 in a non-leap year, using UTC
calendar dates. This is not independent age verification.

Existing author, private group, draft, hidden and deleted-content restrictions
continue to apply. A group rating does not classify its members' unrelated
public content. Shared asset references are checked together.

## Public HTTP API
All writes require the ordinary authenticated session and are rate limited.
The server uses its authenticated account ID; JSON actor/role fields are ignored.

- GET /api/v1/moderation/media?path=... ? policy-only, no-store admission for image proxies/optimizers; returns only allowed, never private reference state
- GET /api/v1/moderation/:kind/:id ? fresh eligibility for the current session
- POST /api/v1/moderation/:kind/:id/report ? {reason, details}
- POST /api/v1/moderation/:kind/:id/reveal
- PUT /api/v1/moderation/:kind/:id/label ? {is_nsfw}

Kinds are blog, event, group and discussion. IDs are the existing public blog ID,
event/group slug or discussion public ID. Reports by ordinary users wait for
review. NSFW reports by authorised Admin, Community and Support accounts apply
a restriction immediately. Non-NSFW reports never automatically apply NSFW.

The existing event-report and staff flag routes use this same policy while
retaining their URLs. Support receives moderation.nsfw only, not general
moderation, user deletion or payment permissions.

## Staff queue
The existing staff authentication and private-network boundary also protect:

- GET /api/v1/admin/moderation/reports?status=pending&limit=30&offset=0
- POST /api/v1/admin/moderation/reports/:id/review ? {action, note}

Review actions are confirm_nsfw, dismiss and reverse. Database constraints and
transactions protect retries and concurrent submissions. Rating changes and
review actions are audited. Author labels cannot clear staff/scanner restrictions.

## Response contract
Content views add optional is_nsfw and content_rating policy metadata, including a revision so a stale safe view cannot overwrite a newer restriction. The latter contains target,
can_reveal, revealed, reason, revision and scan_status. Consumers must treat
these as optional for compatibility with responses from older releases.
Restricted responses contain safe replacement text and no raw body, media,
highlights or arbitrary embedded fields. Inaccessible/stale projections return
content_unavailable instead of their former raw content. List positions and
pagination metadata are retained.

Share and calendar projections remain anonymous-safe. Direct gateway media
paths, including /api/storage and /api/v2/storage aliases, enforce the same
policy before streaming; responses use private, no-store caching.

## Classification
The existing storage NSFW flags and moderation records are inputs to the policy.
No unvalidated automated model is added. Unknown/unscanned historical content is
distinct from confirmed-safe content. An automated detector needs a separately
evaluated model, thresholds, failure policy and false-positive handling.

## Deployment and backward compatibility
Apply migration 000025 before upgrading the user service, then upgrade the
gateway and the public/admin interfaces. When upgrading from the event-hosted
NSFW branch, deploy users first, switch all gateway instances next, and upgrade
events last. Older gateways still call the event-hosted moderation RPC until
they are replaced; new gateways call the existing configured user-service host
and port. Do not remove the old event registration while an older gateway still
uses it. No HTTP/protobuf contract, table, record, revision or stored reveal
changes are needed for this move. Old code can still read ordinary data:
the schema additions do not rename existing columns or rewrite content IDs.
Older binaries do not enforce this new policy and must not serve restricted
content alongside the new gateway.

Previously downloaded originals cannot be retracted by this feature. Purge
previous public page/image caches when enabling enforcement and ensure the
object-store/CDN origin cannot be read publicly around the gateway. External
images hosted by third parties remain controlled by those hosts.
No production deployment is part of this change.

## Local verification
Run the ordinary users/gateway/events suites and the moderation integration suite.
MODERATION_TEST_DSN is accepted only for a deliberately named loopback
moderation_store_test database. Tests create disposable schemas, run real
prerequisite migrations and remove only those schemas.

    go test ./microservices/the_monkeys_users/... ./microservices/the_monkeys_gateway/... ./microservices/the_monkeys_events/... ./common/staffaccess
    go test ./common/moderation -count=1 -timeout 180s -v

The second command must have MODERATION_TEST_DSN set to the disposable database
to execute database tests; without it those tests explicitly skip.
