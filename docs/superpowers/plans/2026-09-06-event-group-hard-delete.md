# Event/group hard-delete (Phase 3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** After a successful unpaid event/group DB delete, publish `event_delete` / `group_delete` so storage removes `events/{slug}/` and `groups/{slug}/`. Tighten the paid gate to captured or pending (never touch payment rows). Add Admin JWT hard-delete next to cancel/unpublish/suspend.

**Architecture:** Consumers first (storage). Existing `DeleteEvent` / `DeleteGroup` keep their public routes. Staff get additive `AdminDeleteEvent` / `AdminDeleteGroup` RPCs. `event_payments` stays `ON DELETE RESTRICT` — any ledger row blocks delete.

**Spec:** `docs/superpowers/specs/2026-09-06-cascade-delete-interservice-message-design.md` §5 Phase 3

## Global Constraints

- Cancel / unpublish / suspend stay. Public DELETE routes stay (organizer only).
- Never UPDATE/DELETE `event_payments` or settlements.
- Signup `role_id = 4`, migrations `000001`–`000017` untouched.
- Do not commit unless asked.
- Proto is additive; generate with `make proto-gen` (WSL if Windows has no protoc).

---

### Task 1: Storage consumer (first)

Constants `EVENT_DELETE = "event_delete"`, `GROUP_DELETE = "group_delete"`. Safe MinIO prefix helper. Handle those actions: CAS soft-delete owner_type event/group by slug; `DeleteMinioPrefix` for `events/{slug}/` and `groups/{slug}/`. Unknown actions still log-and-ignore.

### Task 2: Paid gate SQL

Block event delete if `event_payments` exists for the event **or** any attendee is `pending_payment` **or** confirmed with `payment_id` / captured amount (pre-ledger rows). Block group delete if any child event matches. Do not block merely because a paid tier exists with no payments.

### Task 3: Publish after owner delete

Events: after `DeleteEvent`, `PublishReliable` to `RoutingKeys[0]` (storage). Groups: add Rabbit `ConnManager`, same after `DeleteGroup`. Payload is `interservice.Message` with `Action` + `EventSlug` / `GroupSlug`.

### Task 4: Admin hard-delete

Additive `AdminDeleteEvent(AdminEventActionReq) returns (BasicResp)` and `AdminDeleteGroup` (slug + actor). Skip organizer check; same paid gate; audit; then same Rabbit publish. Gateway `DELETE /api/v1/admin/events/:slug` and `DELETE /api/v1/admin/groups/:slug` (Admin JWT). RBAC tests. Docs.
