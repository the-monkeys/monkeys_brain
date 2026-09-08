# Unified interservice message (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One RabbitMQ JSON type (`common/interservice.Message`) used by every producer and consumer, with dual `ip`/`ip_address` and `status`/`blog_status` aliases, without changing delete or notification behavior.

**Architecture:** Shared Go struct with custom `MarshalJSON`/`UnmarshalJSON`. Service-local `TheMonkeysMessage`, `InterServiceMessage`, and `eventNotification` become type aliases of that struct. Existing action strings and routing keys stay.

**Tech Stack:** Go 1.26, `encoding/json`, existing RabbitMQ helpers. No new queues, protos, or migrations.

**Spec:** `docs/superpowers/specs/2026-09-06-cascade-delete-interservice-message-design.md`

## Global Constraints

- Backward compatible: old payloads must unmarshal; marshal must emit both JSON aliases so old consumers keep working.
- Do not change `action` string values.
- Do not add `event_delete` / `group_delete` handling in consumers in this phase.
- Do not change signup `role_id = 4`, public REST, JWT staff auth, or migrations `000001`–`000017`.
- Do not touch payment rows.
- Do not commit unless the user explicitly asks (user rule overrides plan commit steps).
- Windows agent cannot run `protoc`; this phase does not need it.

---

### Task 1: Shared Message type (TDD)

**Files:**
- Create: `common/interservice/message.go`
- Test: `common/interservice/message_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `interservice.Message` with fields listed in spec §4.2; `MarshalJSON` / `UnmarshalJSON` as in spec §4.3

- [ ] **Step 1: Write the failing tests** in `common/interservice/message_test.go` covering spec §4.5 (users/storage/blog/events payloads, dual marshal, old structs can read new bytes, zero time omitted, canonical wins on conflict).

- [ ] **Step 2: Run tests — expect FAIL** (package does not exist)

```
go test ./common/interservice/ -count=1
```

- [ ] **Step 3: Implement `Message` + codec** in `common/interservice/message.go`

- [ ] **Step 4: Run tests — expect PASS**

```
go test ./common/interservice/ -count=1
```

---

### Task 2: Replace per-service structs with aliases

**Files:**
- Modify: `microservices/the_monkeys_users/internal/models/models.go` — `TheMonkeysMessage`
- Modify: `microservices/the_monkeys_blog/internal/models/models.go` — `InterServiceMessage`
- Modify: `microservices/the_monkeys_storage/internal/models/models.go` — `TheMonkeysMessage`
- Modify: `microservices/the_monkeys_authz/internal/models/auth.go` — `TheMonkeysMessage`
- Modify: `microservices/the_monkeys_notification/internal/models/models.go` — `TheMonkeysMessage`
- Modify: `microservices/the_monkeys_cache/internal/models/models.go` — `TheMonkeysMessage`
- Modify: `microservices/the_monkeys_events/internal/services/messaging.go` — `eventNotification`

**Interfaces:**
- Consumes: `interservice.Message`
- Produces: `type TheMonkeysMessage = interservice.Message` (and the other two aliases). Composite literals and `json.Marshal`/`Unmarshal` at existing call sites keep compiling.

- [ ] **Step 1: Replace each struct body with a type alias** importing `github.com/the-monkeys/the_monkeys/common/interservice`. Do not edit call-site literals unless a field name is missing from `Message`.

- [ ] **Step 2: Build affected packages**

```
go build ./common/interservice/ ./microservices/the_monkeys_users/... ./microservices/the_monkeys_blog/... ./microservices/the_monkeys_storage/... ./microservices/the_monkeys_authz/... ./microservices/the_monkeys_notification/... ./microservices/the_monkeys_cache/... ./microservices/the_monkeys_events/... ./microservices/the_monkeys_gateway/...
```

Expected: exit 0

- [ ] **Step 3: Run package tests that already exist**

```
go test ./common/interservice/ ./microservices/the_monkeys_users/internal/database/ ./microservices/the_monkeys_events/internal/services/ ./microservices/the_monkeys_gateway/internal/admin/ -count=1
```

Expected: PASS (or only pre-existing failures unrelated to the alias)

---

### Task 3: Confirm no behavior-change extras

- [ ] **Step 1:** Grep that no consumer gained `event_delete` / `group_delete` cases.
- [ ] **Step 2:** Grep that `USER_ACCOUNT_DELETE` and `BLOG_DELETE` string constants are unchanged in `constants/actions.go`.

---

## Self-review

- Spec §4.1–4.5 mapped to Task 1–2.
- Spec §4.4 (must not) mapped to Task 3 and Global Constraints.
- Phases 2–4 have no tasks here.
