# FreeRangeNotify SSE notes

These are the SSE contract details that bit local Monkeys integration.
The live-dropdown bug from 2026-09-10 was **not** an FRN outage. The browser
was opening EventSource against a different FRN than the one that issued the
token. Fixes on the Monkeys side are listed first. Remaining FRN quirks that
we adapt to (and should not silently patch in FRN) are listed after.

## What was broken on our side

1. `GET /api/v1/notification/sse-token` talks to **local** FRN
   (`FRN_BASE_URL`, Docker `host.docker.internal:8080`).
2. The browser then opened `EventSource` using `NEXT_PUBLIC_FRN_URL`.
3. That env still pointed at production
   (`https://freerangenotify.monkeys.support/v1`).
4. Local Redis tokens are unknown to production FRN, so the stream died
   immediately. `es.onerror` retried every 5s (token mint storm in local
   FRN logs). In-app rows still appeared after a full page refresh because
   those come from `GET /notification/frn`, not SSE.

**Rule:** the browser must connect to the **same** FRN that minted
`sse_token`. The gateway now adds `sse_public_url` from `FRN_SSE_PUBLIC_URL`
to the token response. The dropdown uses that first, then
`NEXT_PUBLIC_FRN_URL`.

Local values:

```
FRN_BASE_URL=http://host.docker.internal:8080/v1   # Docker → FRN
FRN_SSE_PUBLIC_URL=http://localhost:8080/v1        # browser → FRN
NEXT_PUBLIC_FRN_URL=http://localhost:8080/v1       # fallback only
```

`docker compose restart` does not reload `env_file`. Recreate the gateway
after changing FRN URLs.

## Logout / login

SSE is bound to the logged-in username. On logout the dropdown unmounts and
the EventSource is closed. On a new login (including a different account) a
new token is minted for that username. In-flight connects from the previous
account are ignored via a generation counter so we do not keep reading the
old user.

## FRN contract we have to live with

FRN itself is working for token issue (`POST /v1/sse/tokens` → 201) and
external_id → internal UUID resolution. These FRN behaviors are easy to
misread as Monkeys bugs:

### 1. List payload vs SSE payload

`GET /v1/notifications` returns the nested list shape:

```json
{
  "notification_id": "...",
  "content": { "title": "...", "body": "...", "data": {} },
  "template_id": "blog_liked_inapp"
}
```

SSE `event: notification` is a **flat** `ClientPayload` from
`internal/infrastructure/sse/broadcaster.go`:

```json
{
  "notification_id": "...",
  "title": "...",
  "body": "...",
  "data": {},
  "channel": "sse",
  "status": "delivered",
  "created_at": "..."
}
```

There is no `content` wrapper and usually no `template_id`. The Monkeys
dropdown now normalizes both shapes, then refreshes the in-app list so the
row matches REST.

Ask of FRN if this is revisited: emit the same JSON as the list API
(or include `template_id` + nested `content`) so clients do not need two
parsers.

### 2. Event names

The stream sends named events, not the default `message` event:

- `event: connected`
- `event: notification`

Clients must use `addEventListener('notification', ...)`, not `onmessage`.

### 3. Token TTL

`POST /v1/sse/tokens` stores the token in Redis for **15 minutes**. After
that `GET /v1/sse?sse_token=` returns 401. The dropdown refreshes the token
one minute before expiry.

`user_id` in the create-token body may be the Monkeys username
(external_id). FRN resolves it to an internal UUID. Broadcasts also use
that internal UUID, so the token path is required. Do not connect with
`?user_id=<username>` and a token minted for a different user.

### 4. SSE send path vs in-app persist

`in_app` is stored when `POST /v1/notifications` succeeds. The `sse`
channel is a **second** send, processed by the FRN worker, published to
Redis `sse:notifications`, then fanned out by the API broadcaster.

Docker may mark `freerange-notification-worker` unhealthy because of an
OpenTelemetry exporter timeout (`traces export: produced zero addresses`).
That health flag is misleading: the worker still processes and publishes
SSE. If likes never show without refresh **and** local EventSource is
connected, then look for `Sending SSE notification` / `Broadcasting to user`
in worker logs.

### 5. CORS

`GET /v1/sse` sets `Access-Control-Allow-Origin: *`. Allowed origins in
local FRN compose also include `http://localhost:3000`. EventSource cannot
send `X-API-Key`; that is why the browser uses `sse_token` instead of the
API key.

## How to confirm live SSE

1. Logged-in tab on `http://localhost:3000`.
2. DevTools → Network → EventSource to `http://localhost:8080/v1/sse?sse_token=...` is **pending** (not failed).
3. Local FRN logs show `SSE connection established` / `SSE stream started`.
4. Like a post from another account. The dropdown should add a row without
   a refresh. Local FRN should log `Sending SSE notification` and
   `Broadcasting to user`.
