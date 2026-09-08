# Cascade delete and unified interservice message — design spec

Date: 2026-09-06  
Branch: `feat/payments-admin-backend`  
Status: Phases 1–4 implemented. Apply `000018`/`000019` on monkeys Postgres when ready.  
Scope: backend only. No UI.

This spec is the source of truth for an implementer who has not seen the chat.

---

## 1. Product (locked)

When an account is deleted (self-service or staff `AdminDeleteUser`, same pipeline):

- Delete blogs **owned** by that user (files, Elasticsearch, Postgres blog refs).
- On blogs they only co-authored: drop the co-author link; do not delete the post or its files.
- Delete events **organized** by that user, and groups they **created**, including files under those entities.
- On events they only co-host: drop the co-host row; keep the event and its files.
- Unlink members, RSVPs, waitlists, Q&A, comments. **Do not** delete other user accounts.
- Delete the user’s profile picture / profile prefix in storage.

**Paid-event protection (non-negotiable):**

- If the user organizes an event with **captured or pending** payments, **refuse the whole user delete** (gRPC `FailedPrecondition` / HTTP 409). Settle or cancel those events first via existing cancel/refund flows.
- If a group they created still has such a paid event underneath, refuse group delete and therefore user delete (409).
- **Never** delete or mutate `event_payments`, host settlements, or captured payment rows as part of these deletes.

Soft staff actions stay: blog unpublish, event cancel/unpublish, group suspend. **Hard delete** is a separate additive endpoint (Phase 2–3), not a change to those routes.

Public REST, cookies/JWTs, signup `role_id = 4`, and migrations `000001`–`000017` stay as they are. Schema/proto for later phases is additive only.

---

## 2. Architecture (locked)

**Approach 1:** one shared JSON envelope + owning service deletes its rows synchronously (so 409 can return on the request) + one RabbitMQ fan-out for files / search / notifications.

Do not put protobuf on Rabbit. Do not add queues for Phase 1. Keep existing exchange and routing keys.

**Compatibility is a hard constraint.** Rolling deploys must not break existing users, existing messages, or old consumers.

---

## 3. Phases

| Phase | Ship | Behavior change? |
| --- | --- | --- |
| **1 (this work)** | Shared `common/interservice.Message`; every producer/consumer uses it; dual JSON aliases | **None** (except payloads may include both `ip` and `ip_address` / `status` and `blog_status` — old consumers ignore extra keys) |
| 2 | Blog hard-delete gaps + admin hard-delete blog | Additive admin route; existing `DeleteABlogByBlogId` already ES + Rabbit to users Postgres + storage |
| 3 | `event_delete` / `group_delete` after successful DB delete; storage removes `events/{slug}/` and `groups/{slug}/`; tighten paid gate to captured **or** pending; admin hard-delete routes | New actions (consumers first); existing DeleteEvent/DeleteGroup APIs stay |
| 4 | User-delete 409 gate via events/groups gRPC; cascade owned unpaid events/groups; unlink co-author/co-host; then existing profile + `USER_ACCOUNT_DELETE` | 409 for accounts blocked by paid events/groups; others get fuller cleanup |

Deploy rule for any phase that adds an `action` or field: **consumers before producers**. Unknown actions stay log-and-ignore.

---

## 4. Phase 1 — unified envelope

### 4.1 Package

`github.com/the-monkeys/the_monkeys/common/interservice`

Type name: `Message`.

Each service **stops defining its own copy**. Keep local names as type aliases so call sites do not churn:

```go
type TheMonkeysMessage = interservice.Message   // users, storage, authz, notification, cache
type InterServiceMessage = interservice.Message // blog
type eventNotification = interservice.Message   // events
```

No second struct. Aliases must point at `interservice.Message`, not a fork.

### 4.2 Fields (Go names must match existing literals)

Union of every field used on today’s `TheMonkeysMessage`, `InterServiceMessage`, and `eventNotification`:

| Go field | Canonical JSON | Notes |
| --- | --- | --- |
| `Id` | `id` | |
| `AccountId` | `account_id` | |
| `Username` | `username` | |
| `NewUsername` | `new_username` | |
| `FirstName` | `first_name` | |
| `LastName` | `last_name` | |
| `Email` | `email` | |
| `LoginMethod` | `login_method` | |
| `ClientId` | `client_id` | |
| `Client` | `client` | |
| `IpAddress` | `ip_address` **and** `ip` | dual codec |
| `Action` | `action` | existing string values unchanged |
| `Notification` | `notification` | |
| `UserStatus` | `user_status` | blog |
| `BlogId` | `blog_id` | |
| `BlogIds` | `blog_ids` | omitempty |
| `BlogStatus` | `blog_status` **and** `status` | dual codec |
| `BlogTitle` | `blog_title` | |
| `Tags` | `tags` | omitempty |
| `ScheduleTime` | `schedule_time` | omit if zero |
| `Timezone` | `timezone` | |
| `AnalysisRequestedAt` | `analysis_requested_at` | omit if zero |
| `CorrelationId` | `correlation_id` | |
| `Priority` | `priority` | |
| `EventSlug` | `event_slug` | |
| `EventTitle` | `event_title` | |
| `EventIds` | `event_ids` | unused in Phase 1; omitempty |
| `GroupIds` | `group_ids` | unused in Phase 1; omitempty |
| `GroupSlug` | `group_slug` | unused in Phase 1; omitempty |

### 4.3 Dual JSON codec (the production-safety part)

Today:

- Users, blog, cache emit/read `ip_address` and `blog_status`.
- Storage, authz, notification often use `ip`. Storage uses `status` for blog status; notification uses `blog_status`.

**Unmarshal:** accept either alias. If both are present and differ, prefer the canonical key (`ip_address`, `blog_status`).

**Marshal:** if the Go field is non-empty, emit **both** keys with the same value. Do not stop dual-writing in Phase 1.

Empty strings, nil slices, and zero `time.Time` are omitted. Extra JSON keys on inbound messages are ignored.

`action` strings do not change (`user_profile_directory_delete`, `delete`, `blog_update`, `published`, event notification actions, …).

### 4.4 What Phase 1 must not do

- No new Rabbit actions.
- No new queues or routing keys.
- No change to delete/409 behavior.
- No payment or schema migrations.
- No proto regeneration.
- Do not rewrite public REST.
- Do not change signup `role_id = 4`.

### 4.5 Tests (required)

Table tests in `common/interservice/message_test.go`:

1. Production-shaped **users** payload (`ip_address`, `blog_status`, `blog_ids`) unmarshals.
2. Production-shaped **storage** payload (`ip`, `status`) unmarshals into `IpAddress` / `BlogStatus`.
3. Production-shaped **blog** payload (`schedule_time`, `correlation_id`, `tags`) unmarshals.
4. Production-shaped **events notification** payload (`event_slug`, `event_title`) unmarshals.
5. Marshal of a message with IP and blog status includes **both** alias pairs.
6. A simulated **old storage struct** (`json:"ip"`, `json:"status"`) can unmarshal bytes produced by `Message.MarshalJSON`.
7. A simulated **old users struct** (`json:"ip_address"`, `json:"blog_status"`) can unmarshal the same bytes.
8. Zero-value marshal does not emit `"0001-01-01"` schedule times.
9. Both aliases present and different → canonical wins.

Wire-up is compile-time: after aliases, `go build` of users, blog, storage, authz, notification, cache, events, gateway.

---

## 5. Later phases (do not implement now)

**Phase 2:** Keep `DeleteABlogByBlogId` (ES + Rabbit `BLOG_DELETE` to users Postgres `DeleteBlogAndReferences` and storage files). Use the shared message. Add staff hard-delete blog beside unpublish. Prefer `PublishReliable` to storage if today it uses unconfirmed publish.

**Phase 3:** After `DeleteEvent` / `DeleteGroup` succeed, publish `event_delete` / `group_delete`. Storage deletes `events/{slug}/` and `groups/{slug}/` plus CAS refs. Paid gate = captured or pending; never touch payment tables. Unlink via existing FKs. Additive admin hard-delete. Cancel/unpublish/suspend unchanged.

**Phase 4:** Same pipeline for self-delete and admin, owned by the users service (`DeleteUserAccount`). Before `DeleteUserProfile` it calls EventService `CheckUserEventRemoval` / `RemoveUserFromEvents` and GroupService `CheckUserGroupRemoval` / `RemoveUserFromGroups`. 409 if organizer of a paid event, creator of a group that still has paid events, or holder of a payment-linked RSVP. If clear: delete owned unpaid events/groups through Phase 3 paths, drop co-author/co-host/membership/RSVP/comment links, then existing user Postgres delete + `USER_ACCOUNT_DELETE` (profile + blog files + ES).

---

## 6. Out of scope

- Frontend.
- Auto payouts / mutating captured payments.
- Replacing JWT/cookies or LAN `X-Admin-Key` backup routes.
- Rewriting migrations `000001`–`000017`.
- Protobuf on RabbitMQ.
