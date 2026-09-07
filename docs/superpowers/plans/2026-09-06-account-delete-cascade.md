# Account-delete cascade (Phase 4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans or subagent-driven-development.

**Goal:** Self-delete and admin delete share one pipeline. 409 if the account organizes a paid event or a group that still has paid events. Otherwise delete owned unpaid events/groups (Phase 3 file fan-out), unlink co-host/co-author/membership, then existing profile + `USER_ACCOUNT_DELETE`.

**Architecture:** Users service owns `DeleteUserAccount`. It calls EventService `CheckUserEventRemoval` / `RemoveUserFromEvents` and GroupService `CheckUserGroupRemoval` / `RemoveUserFromGroups`, then `DeleteUserProfile`. Never touch `event_payments`.

**Spec:** `docs/superpowers/specs/2026-09-06-cascade-delete-interservice-message-design.md` §5 Phase 4

## Global Constraints

- Same path for self-delete and `AdminDeleteUser`.
- Map gRPC `FailedPrecondition` to HTTP 409 on both staff and public delete.
- If events/groups clients are missing, fail closed (Unavailable), do not skip the paid gate.
- Do not commit unless asked. User runs `protoc` in WSL.
