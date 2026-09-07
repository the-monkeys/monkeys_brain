# Blog hard-delete completeness (Phase 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single-blog hard delete always fans out ES + Postgres refs + files (reliable Rabbit), and staff get an additive `DELETE /api/v1/admin/blogs/:blog_id` next to unpublish.

**Architecture:** Reuse existing `BlogService.DeleteABlogByBlogId` (no new proto). Treat Elasticsearch 404 as success so orphans still get `BLOG_DELETE`. Publish that message with `PublishReliable` to users (`RoutingKeys[1]`) and storage (`RoutingKeys[2]`). Gateway Admin-only route calls the same RPC. Unpublish stays a soft draft move.

**Tech Stack:** Go, existing gRPC `DeleteABlogByBlogId`, RabbitMQ `PublishReliable`, Gin staff JWT `RequireRole(Admin)`.

**Spec:** `docs/superpowers/specs/2026-09-06-cascade-delete-interservice-message-design.md` §5 Phase 2

## Global Constraints

- Do not change `POST /blogs/:blog_id/unpublish`.
- Do not change `action` string `delete` (`constants.BLOG_DELETE`).
- Do not add `event_delete` / `group_delete`.
- Do not change signup `role_id = 4`, public author `DELETE /:blog_id` path shape, or migrations `000001`–`000017`.
- No proto regen; reuse `DeleteBlogReq`.
- Do not commit unless the user asks.

---

### Task 1: Delete helpers (TDD)

**Files:**
- Create: `microservices/the_monkeys_blog/internal/services/delete_fanout.go`
- Test: `microservices/the_monkeys_blog/internal/services/delete_fanout_test.go`

**Interfaces:**
- Produces: `esDeleteOK(statusCode int) bool` — true for HTTP 200 and 404
- Produces: `blogDeleteFanOutKeys(keys []string) (users, storage string, ok bool)` — `keys[1]` users, `keys[2]` storage, `ok` if `len(keys) >= 3`

- [ ] **Step 1: Write failing tests** for 200/404 OK, 500 not OK, short key slice not ok, example keys from `.env.example`.

- [ ] **Step 2: Run** `go test ./microservices/the_monkeys_blog/internal/services/ -count=1 -run FanOut` — FAIL (undefined)

- [ ] **Step 3: Implement helpers**

- [ ] **Step 4: Run tests — PASS**

---

### Task 2: Wire DeleteABlogByBlogId

**Files:**
- Modify: `microservices/the_monkeys_blog/internal/services/service.go` (`DeleteABlogByBlogId`)
- Modify: `microservices/the_monkeys_blog/internal/database/opensearch.go` (`DeleteABlogById` treat 404 as success, expose status or return nil error)

**Interfaces:**
- Consumes: Task 1 helpers
- After ES delete (missing doc OK), marshal `interservice`/`InterServiceMessage` with `Action: constants.BLOG_DELETE`, `PublishReliable` to both fan-out keys

- [ ] **Step 1: Make ES 404 a non-error**
- [ ] **Step 2: Replace storage `PublishMessage` with `PublishReliable` on `RoutingKeys[2]`**
- [ ] **Step 3: Build** `go build ./microservices/the_monkeys_blog/...`

---

### Task 3: Admin hard-delete route (TDD)

**Files:**
- Modify: `microservices/the_monkeys_gateway/internal/admin/staff_routes_test.go`
- Modify: `microservices/the_monkeys_gateway/internal/admin/routes.go`
- Modify: `microservices/the_monkeys_gateway/internal/admin/blogs.go`
- Modify: `docs/admin-api.md`

**Interfaces:**
- `DELETE /api/v1/admin/blogs/:blog_id` — Admin only; calls `Blogs.DeleteABlogByBlogId` with `BlogId`, `Ip`, `Client: "admin"`; do **not** set `OwnerAccountId` to the staff account. Audit `blog.delete`. Unpublish unchanged.

- [ ] **Step 1: Extend staff role matrix** — Admin 200, Support/Community/Viewer 403
- [ ] **Step 2: Run test — FAIL** (route missing in test tree)
- [ ] **Step 3: Register route + handler + docs**
- [ ] **Step 4: `go test ./microservices/the_monkeys_gateway/internal/admin/ -count=1` PASS**

---

### Task 4: Verify

```
go test ./microservices/the_monkeys_blog/internal/services/ ./microservices/the_monkeys_gateway/internal/admin/ ./common/interservice/ -count=1
go build ./microservices/the_monkeys_blog/... ./microservices/the_monkeys_gateway/...
```
