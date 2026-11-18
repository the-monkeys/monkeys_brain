# Authenticated creation idempotency

The gateway accepts an optional `Idempotency-Key` on these authenticated POST routes:

- `/api/v1/events`
- `/api/v1/events/series`
- `/api/v1/events/:slug/clone`
- `/api/v1/groups`
- `/api/v1/groups/:slug/events`
- `/api/v1/user/verification`

Middleware runs after the existing authentication and route permission guards. Every replay goes through those guards again. No-header requests preserve existing behavior and do not contact the idempotency store. No protobuf, users database, or service changes are required.

## Client contract

Generate a new random key for each creation intent and keep it across double clicks, simultaneous submissions, connection failures, and uncertain retries. Re-send identical body bytes, Content-Type, and query string. Scope includes the authenticated immutable `accountId`, HTTP method, route template, and concrete path (including the slug). The same key on another account, route, or slug represents a separate intent. A changed body, Content-Type, or query on the same scope returns a conflict. Changing JSON whitespace also changes the fingerprint.

A completed event/group retry returns the original successful status and JSON bytes, with `Idempotency-Replayed: true`. Verification stores only the created request ID and re-fetches that exact account-private resource on replay, checking its username and ID before returning any private fields. Its HTTP status stays `201` and response fields stay intact; its lifecycle state may reflect subsequent review/cancellation. A failed private lookup returns `503` and never creates another submission.

New middleware errors contain stable `code`, `message`, and `error` fields (`error` equals `message`). Existing handler success and error responses are unchanged:

- `400 / idempotency_key_invalid`: `Idempotency-Key must contain 1 to 200 visible ASCII characters without spaces.`
- `400 / idempotency_body_invalid`: `Could not read keyed request body.`
- `413 / idempotency_body_too_large`: `Keyed request body exceeds 1 MiB.`
- `401 / idempotency_identity_required`: `An authenticated account is required.` (defensive check; normal auth errors run first).
- `409 / idempotency_key_reused`: `Idempotency-Key was already used with a different request.`
- `409 / idempotency_in_progress`: `This request is still in progress or its outcome is uncertain. Retry with the same key.`
- `503 / idempotency_unavailable`: `Idempotency protection is unavailable. Retry with the same key.`

The in-progress and unavailable responses include `Retry-After: 2`. Keyed responses use `Cache-Control: no-store`. CORS allows the request header and exposes `Idempotency-Replayed` and `Retry-After`. This checkout has no gateway reverse-proxy header allowlist; any external proxy must forward `Idempotency-Key` unchanged.

Do not turn a timeout, `409 idempotency_in_progress`, or `503` into a new key. Reconcile existing resources before starting another intent. A definitive validation failure may be corrected using a new intent/key. Existing rate limits can still reject requests before the idempotency middleware.

## Storage and failure behavior

The middleware reuses the gateway search cache's configured go-redis connection pool directly, with errors handled explicitly rather than through its fail-open search API. Existing host/port normalization is unchanged: a Redis Host already containing a port takes precedence over Redis Port.

`SET NX` reserves a hashed `idempotency:v1:` key for 24 hours. Only a SHA-256 fingerprint of the bounded body, Content-Type, and query is stored with the reservation; raw request keys, account IDs, JWTs, request bodies, and private verification proof are not stored in Redis keys or reservation records. Completed event/group JSON is bounded to 256 KiB. Verification completion contains only its request ID. Cookies, authorization headers, and all other response headers are excluded from storage/replay. The middleware does not log payloads, request keys, or Redis error contents.

Completion uses a Lua compare-and-set against the original reservation and preserves its remaining TTL. A late worker cannot overwrite a newer owner. Completion uses a separate two-second context so a browser disconnect can still persist a successful creation. Reservations are never deleted on errors: 4xx, 5xx, timeouts, panics, non-JSON/oversized successes, and completion-storage failures all retain the original reservation. The client receives the original first response; an uncertain retry is blocked with `409` until the reservation expires. Redis reservation/read failure returns `503` without calling the creation handler. Missing or malformed store data never permits creation.

Protection is bounded by the 24-hour TTL and depends on Redis preserving acknowledged reservation records. Redis persistence, replication, and eviction policy must suit coordination data; cache eviction/data loss or replaying an intent after expiry cannot provide exactly-once semantics. The gateway cannot recover a committed write's response after a worker dies before recording completion. In that case it deliberately blocks retries for the remaining TTL. No new claim of database-level exactly-once behavior is made.

## Focused verification and gateway-only build

From this isolated backend checkout, in PowerShell:

```powershell
$env:GOMAXPROCS='2'
& 'C:/Program Files/Go/bin/go.exe' test -p 1 ./microservices/the_monkeys_gateway/internal/idempotency ./microservices/the_monkeys_gateway/middleware ./microservices/the_monkeys_gateway/internal/events ./microservices/the_monkeys_gateway/internal/groups ./microservices/the_monkeys_gateway/internal/user_service ./microservices/the_monkeys_gateway -count=1
& 'C:/Program Files/Go/bin/go.exe' build -p 1 -o "$env:TEMP/monkeys-idempotency-gateway.exe" ./microservices/the_monkeys_gateway
```

The Redis command/Lua integration test is opt-in. Set `MONKEYS_IDEMPOTENCY_TEST_REDIS_ADDR` to an explicitly disposable loopback IP:port, then run the idempotency package tests. It refuses non-loopback addresses and never reads application Redis settings. It uses unique keys and removes only its own fixtures, without FLUSHDB. A local miniredis fixture with Lua also works; no test dependency is added to this repository. Default tests use a deterministic test-only store plus an explicitly closed loopback socket to verify real go-redis fail-closed behavior. Production uses Redis atomic operations, never the test store or process-local locks.

The existing Compose service name is `the_monkeys_gateway`. The parent can build only that image with `docker compose build the_monkeys_gateway` from this checkout. This builds an image without starting/restarting services; no users-service image or database/protobuf regeneration is needed for this change.
