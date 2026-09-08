# Payments, Speed, and Admin Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make events/groups APIs fast without extra auth hops, store Razorpay captures in INR with 5% platform fee + 18% GST on that fee, and expose JWT-role staff APIs (Admin / Support / Community) — without a UI.

**Architecture:** Keep platform Razorpay in `the_monkeys_events`. Add integer-paise ledger tables. Gateway `/api/v1/admin` stays the only admin HTTP surface and fans out to new Admin RPCs on existing services. No new microservice.

**Tech Stack:** Go, pgx, Postgres 17, gRPC/protobuf, Gin gateway, Razorpay REST, docker compose.

**Spec:** `docs/superpowers/specs/2026-09-05-payments-admin-backend-design.md` — follow it if this plan and the spec disagree on money math, the spec wins.

## Global Constraints

- Backend only. Do not change frontend apps.
- Additive protobuf field numbers only. Never reuse numbers.
- Do not rewrite applied migrations `schema/000001`–`000017`. Next files are `000018`+.
- Paid tickets: INR only. GST Option A: fee = 5% of captured gross; GST = 18% of fee; host = gross − fee − GST. Example ₹1000 → host ₹941.
- Money in the DB is `BIGINT` paise. Razorpay amounts are paise.
- v1 settlements are bookkeeping only (no Razorpay payout / Route).
- Staff HTTP: JWT `user_role.role_desc` (`Admin`, `Support`, `Community`). Signup stays `role_id = 4` (Viewer). Do not use LAN + `X-Admin-Key` for daily staff APIs.
- Production data: additive schema/proto only. No rewrite of `000001`–`000017`. No change to public event/group JSON contracts.
- Never log Razorpay secrets or interpolate `database/sql` errors into gRPC user messages.
- After proto edits, regenerate `*.pb.go` / `*_grpc.pb.go` (user often runs `protoc` manually). `go vet ./...` must stay clean. Never copy protobuf structs by value.
- Do not `docker compose up --build -d` the whole stack for every Go edit (see Task 1).
- Do not use orphan tables `plans`, `organizer_subscriptions`, `group_dues`, `group_due_payments`.

---

## Files that will be created or modified

| Path | Responsibility |
| --- | --- |
| `schema/000018_event_payments.up.sql` / `.down.sql` | Payment ledger, settlements, attendee paise columns |
| `schema/000019_admin_moderation.up.sql` / `.down.sql` | `admin_audit_log`, `user_admin_flags`, `blog_admin_flags`, `event_admin_flags`, `group_admin_flags`, suspend status if missing |
| `microservices/the_monkeys_events/internal/money/money.go` | Fee/GST math |
| `microservices/the_monkeys_events/internal/money/money_test.go` | Golden INR cases |
| `microservices/the_monkeys_events/internal/services/payments.go` | Orders in paise; keep HMAC |
| `microservices/the_monkeys_events/internal/database/attendees.go` | Capture/refund writes ledger |
| `microservices/the_monkeys_events/internal/database/events.go` | Faster list SQL; skip second Authorize |
| `microservices/the_monkeys_events/internal/database/geocoding.go` | Context + no-op if coords present |
| `microservices/the_monkeys_events/internal/database/tickets_coupons.go` | Force INR |
| `microservices/the_monkeys_groups/internal/database/search.go` | Bounding box |
| `apis/serviceconn/gateway_event/pb/gw_event.proto` | Admin payment + admin list RPCs |
| `apis/serviceconn/gateway_user/pb/gw_user.proto` | Admin list/stats/flag RPCs |
| `apis/serviceconn/gateway_blog/pb/gw_blog.proto` | Admin list/orphan/nsfw |
| `apis/serviceconn/gateway_group/pb/gw_group.proto` | Admin list/suspend |
| Matching `internal/services` + `internal/database` in each service | Implement RPCs |
| `apis/serviceconn/gateway_authz/pb/gw_auth.proto` | Additive `ValidateResponse.role` |
| `microservices/the_monkeys_authz/internal/db/db.go` | JOIN `user_role` on existing Validate query |
| `microservices/the_monkeys_gateway/internal/auth/middleware.go` | Set `user_role` from Validate |
| `microservices/the_monkeys_gateway/internal/admin/rbac.go` | `RequireRole` |
| `constants/database.go` | `RoleSupport`, `RoleCommunity` |
| `.env.example` | Local loop + webhook comments |
| `docs/admin-api.md` | JWT roles; correct base path |

---

### Task 1: Fast local API loop (no product behaviour change)

**Files:**
- Modify: `.env.example` (comments only)
- Do not change compose service list unless adding a documented `profiles:` comment in a short `docs/superpowers/plans/local-api-loop.md` snippet inside this task’s commit message/docs is enough — add section to `docs/context.md` only if the user already treats it as handoff; prefer `.env.example` comments.

**Interfaces:** none.

- [ ] **Step 1: Document the loop in `.env.example` above the Razorpay keys**

Add:

```
# Local speed: do not rebuild every Go change.
# 1) docker compose up -d the_monkeys_db db-migrations the_monkeys_cache rabbitmq elasticsearch-node1 minio
# 2) Point MICROSERVICES_* hosts to localhost and run:
#    go run ./microservices/the_monkeys_authz
#    go run ./microservices/the_monkeys_events
#    go run ./microservices/the_monkeys_groups
#    go run ./microservices/the_monkeys_gateway
# 3) Rebuild one image: docker compose up --build -d the_monkeys_events
# Razorpay webhooks cannot hit localhost; use Razorpay CLI forward or a tunnel
# to POST /api/v1/events/payment/webhook
# Razorpay dashboard: enable automatic capture.
```

- [ ] **Step 2: Confirm infra comes up without rebuilding Go images**

Run:

```
docker compose up -d the_monkeys_db db-migrations the_monkeys_cache rabbitmq elasticsearch-node1 minio
```

Expected: containers healthy; `db-migrations` exits 0 (or already applied).

- [ ] **Step 3: Commit**

```
git add .env.example
git commit -m "docs: local loop for events APIs without full compose rebuild"
```

---

### Task 1b: Fast auth — role on Validate, no extra query, no extra Authorize on public GET

**Files:**
- Modify: `apis/serviceconn/gateway_authz/pb/gw_auth.proto` — add `string role = 7;` on `ValidateResponse` (next free number; confirm 5 is error, 6 is account_id)
- Generate pb
- Modify: `microservices/the_monkeys_authz/internal/db/db.go` — add `LEFT JOIN user_role ur ON ur.id = ua.role_id` and scan `ur.role_desc` into the user model (new field). Same one query as today.
- Modify: `microservices/the_monkeys_authz/internal/services/services.go` — `Validate` returns `Role: user.Role`. Use incoming `ctx`, not `context.Background()`.
- Modify: `microservices/the_monkeys_authz/internal/utils/jwt.go` — additive `Role string \`json:"role,omitempty"\`` on new tokens only.
- Modify: `microservices/the_monkeys_gateway/internal/auth/middleware.go` — after Validate, `ctx.Set("user_role", res.Role)` and `ctx.Set("accountId", ...)` as today.
- Create: `microservices/the_monkeys_gateway/internal/admin/rbac.go`
- Modify: `microservices/the_monkeys_gateway/internal/events/routes.go` — **remove** `guard.RequireVisible()` from public GET detail/comments/share. Keep guards on writes.
- Modify: `microservices/the_monkeys_gateway/internal/groups/routes.go` — same for public GET group.
- Modify: `microservices/the_monkeys_events/internal/services/service.go` — `redactForViewer` without `Authorize`.
- Modify: `constants/database.go` — `RoleSupport = "Support"`, `RoleCommunity = "Community"` (Admin already exists).
- Test: authz Validate still 401 on bad token; role `Viewer` for a normal user.

**Interfaces:**
- Consumes: existing JWT cookie
- Produces: `ValidateResponse.role` string exactly `Admin`|`Owner`|`Editor`|`Viewer`|`Support`|`Community`

`RequireRole` implementation:

```go
func RequireRole(log *zap.SugaredLogger, allowed ...string) gin.HandlerFunc {
    allow := map[string]struct{}{}
    for _, r := range allowed {
        allow[r] = struct{}{}
    }
    return func(c *gin.Context) {
        role := c.GetString("user_role")
        if _, ok := allow[role]; !ok {
            c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
            return
        }
        c.Next()
    }
}
```

Do **not** add LAN or `X-Admin-Key` to new groups.

- [ ] **Step 1: Proto + JOIN + middleware tests**
- [ ] **Step 2: Drop public GET Authorize; redact without second Authorize**
- [ ] **Step 3:** `go test ./microservices/the_monkeys_authz/... ./microservices/the_monkeys_gateway/internal/events/... ./microservices/the_monkeys_events/internal/services/...`
- [ ] **Step 4: Commit** `fix: return role from Validate and skip extra event Authorize on public GET`

---

### Task 2: Money package (fee + GST)

**Files:**
- Create: `microservices/the_monkeys_events/internal/money/money.go`
- Create: `microservices/the_monkeys_events/internal/money/money_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:

```go
package money

const (
    CurrencyINR    = "INR"
    PlatformFeeBPS = 500
    GstBPS         = 1800
)

type Split struct {
    GrossPaise        int64
    PlatformFeePaise  int64
    GstPaise          int64
    HostPayablePaise  int64
    FeeBPS            int
    GstBPS            int
}

func ToPaise(rupees float64) int64
func SplitGross(grossPaise int64) Split
```

`ToPaise` uses half-up: `int64(math.Round(rupees * 100))` — do not use the old `amount*100 + 0.5` truncation pattern for negatives (prices are >= 0).

`SplitGross`:

```go
func roundBPS(amount int64, bps int64) int64 {
    if amount == 0 || bps == 0 {
        return 0
    }
    return (amount*bps + 5000) / 10000
}
```

Then `fee := roundBPS(gross, 500)`, `gst := roundBPS(fee, 1800)`, `host := gross - fee - gst`.

- [ ] **Step 1: Write tests first**

```go
func TestSplitGross1000INR(t *testing.T) {
    s := SplitGross(100_000)
    if s.PlatformFeePaise != 5000 || s.GstPaise != 900 || s.HostPayablePaise != 94_100 {
        t.Fatalf("got %+v", s)
    }
}

func TestSplitGrossZero(t *testing.T) {
    s := SplitGross(0)
    if s != (Split{FeeBPS: PlatformFeeBPS, GstBPS: GstBPS}) {
        t.Fatalf("got %+v", s)
    }
}

func TestToPaise(t *testing.T) {
    if ToPaise(499) != 49900 {
        t.Fatal("499 rupees")
    }
    if ToPaise(1.15) != 115 {
        t.Fatal("1.15")
    }
}
```

Add one odd-paise case: `SplitGross(333)` (₹3.33). Assert `fee+gst+host == 333`.

- [ ] **Step 2: Run tests — expect FAIL (package missing)**

```
go test ./microservices/the_monkeys_events/internal/money -v
```

- [ ] **Step 3: Implement `money.go` so tests pass**

- [ ] **Step 4: Re-run tests — expect PASS**

- [ ] **Step 5: Commit**

```
git add microservices/the_monkeys_events/internal/money
git commit -m "feat: INR platform fee and GST split in paise"
```

---

### Task 3: Migration `000018` payment ledger

**Files:**
- Create: `schema/000018_event_payments.up.sql`
- Create: `schema/000018_event_payments.down.sql`

Use the exact table definitions from the spec §6.4 (`event_payments`, `event_host_settlements`, attendee paise columns). Down file drops tables and columns in reverse order.

Do **not** put a unique index on `event_host_settlements(event_id)` (multiple settlements over time).

- [ ] **Step 1: Write up/down SQL as in the spec**

- [ ] **Step 2: Apply**

```
docker compose up db-migrations
```

Expected: migration version 18 applied. If the migrate container already ran `up` to 17, this command applies 18.

- [ ] **Step 3: Commit**

```
git add schema/000018_event_payments.up.sql schema/000018_event_payments.down.sql
git commit -m "feat: event payment ledger and host settlement tables"
```

---

### Task 4: Wire capture/refund to the ledger + INR + paise orders

**Files:**
- Modify: `microservices/the_monkeys_events/internal/services/payments.go` (`createOrder` amount type)
- Modify: `microservices/the_monkeys_events/internal/services/service.go` (`RSVPEvent` / `ProcessPaymentWebhook` / `refundAll`)
- Modify: `microservices/the_monkeys_events/internal/database/attendees.go`
- Modify: `microservices/the_monkeys_events/internal/database/interface.go`
- Modify: `microservices/the_monkeys_events/internal/database/tickets_coupons.go`
- Modify: `microservices/the_monkeys_events/internal/services/payments_test.go`
- Test: `microservices/the_monkeys_events/internal/database/attendees_payment_test.go` (new; use sqlmock **only if already used**; otherwise table-driven tests of a new `func BuildPaymentRow(...)` in `money` or `database` that does not need DB)

**Interfaces:**
- Consumes: `money.SplitGross`, `money.ToPaise`
- Produces: `ConfirmPayment` also inserts `event_payments`; `MarkRefunded` sets payment row `status='refunded'`, `refund_paise=gross_paise`

- [ ] **Step 1: Reject non-INR on paid tiers**

In `insertTier` after currency default:

```go
if in.Price > 0 && !strings.EqualFold(currency, money.CurrencyINR) {
    return nil, status.Error(codes.InvalidArgument, "paid tickets must use INR")
}
currency = money.CurrencyINR
```

Same on update tier.

- [ ] **Step 2: Change `createOrder` to `amountPaise int64`**

```go
body := map[string]any{
    "amount":   amountPaise,
    "currency": "INR",
    "receipt":  receipt,
}
```

Remove float `toPaise` from the HTTP client path (keep a test that 100000 paise is sent).

- [ ] **Step 3: RSVP pending path**

`CreateRSVP` already returns `AmountDue` float for proto. Keep proto `double amount_due` for the checkout widget. Internally:

```go
duePaise := money.ToPaise(result.AmountDue)
orderID, err := s.pay.createOrder(ctx, duePaise, "INR", fmt.Sprintf("evt-rsvp-%d", result.AttendeeID))
```

`AttachPaymentOrder` should set `amount_due_paise` and **not** treat pending as captured. Stop writing captured semantics into `amount_paid` until webhook (or write `amount_paid=0` until capture; on capture set `amount_paid = gross rupees` for old columns).

- [ ] **Step 4: `ConfirmPayment` INSERT**

After locking the attendee row, load `organizer_id`, `amount_due_paise` (fallback `ToPaise(amount_paid)` for old rows).

```go
split := money.SplitGross(grossPaise)
_, err = tx.ExecContext(ctx, `
INSERT INTO event_payments (
  event_id, attendee_id, organizer_user_id, currency,
  gross_paise, fee_bps, gst_bps, platform_fee_paise, gst_paise, host_payable_paise,
  razorpay_order_id, razorpay_payment_id, status
) VALUES ($1,$2,$3,'INR',$4,$5,$6,$7,$8,$9,$10,$11,'captured')
ON CONFLICT (razorpay_payment_id) DO NOTHING`,
    eventID, attendeeID, organizerID, split.GrossPaise, split.FeeBPS, split.GstBPS,
    split.PlatformFeePaise, split.GstPaise, split.HostPayablePaise,
    orderID, paymentID)
```

Update attendee: `status=confirmed`, `payment_id`, `amount_captured_paise=gross`, `amount_paid=float(gross)/100`.

- [ ] **Step 5: Refunds**

`MarkRefunded`: also

```sql
UPDATE event_payments
SET status = 'refunded', refund_paise = gross_paise, razorpay_refund_id = $2, refunded_at = NOW()
WHERE razorpay_payment_id = $1
```

Open payable queries **must** use `status = 'captured'` only.

- [ ] **Step 6: Tests**

- Unit: split identity on confirm helper.
- Existing `payments_test.go` webhook HMAC still passes.
- `go test ./microservices/the_monkeys_events/...`

- [ ] **Step 7: Commit**

```
git commit -m "feat: record GST split on Razorpay capture and refund"
```

---

### Task 5: Speed — event/group list SQL + GetEvent + geocode

**Files:**
- Modify: `microservices/the_monkeys_events/internal/database/events.go` (`eventColumns`, `eventFrom`, `seriesCollapseCond`, `list`, `redact` path in service)
- Modify: `microservices/the_monkeys_events/internal/services/service.go` (`redactForViewer`)
- Modify: `microservices/the_monkeys_events/internal/database/geocoding.go`
- Modify: `microservices/the_monkeys_groups/internal/database/search.go`
- Test: `microservices/the_monkeys_events/internal/database/events_test.go` (extend)
- Test: existing group tests if any

**Interfaces:** public REST unchanged.

- [ ] **Step 1: Remove per-row attendee subquery from `eventColumns`**

Delete:

```
(SELECT COUNT(1) FROM event_attendees a WHERE a.event_id = e.id AND a.status = 'confirmed') AS attendee_count
```

Add join in `eventFrom`:

```
LEFT JOIN (
  SELECT event_id, COUNT(*)::int AS attendee_count
  FROM event_attendees
  WHERE status = 'confirmed'
  GROUP BY event_id
) ac ON ac.event_id = e.id
```

Select `COALESCE(ac.attendee_count, 0)`. Update `scanEvent` only if column order changes — keep order stable: put `attendee_count` in the same scan slot.

- [ ] **Step 2: Bounding box helper** (shared copy in events and groups is OK; do not create a new shared module unless one already exists)

```go
func geoBox(lat, lng float64, radiusKm int32) (minLat, maxLat, minLng, maxLng float64) {
    r := float64(radiusKm)
    dlat := r / 111.0
    cos := math.Cos(lat * math.Pi / 180)
    if math.Abs(cos) < 0.01 {
        cos = 0.01
    }
    dlng := r / (111.0 * cos)
    return lat - dlat, lat + dlat, lng - dlng, lng + dlng
}
```

AND the existing acos predicate. Bind `nearest` ORDER BY with `$n` placeholders, not `%f`.

- [ ] **Step 3: Series collapse**

Replace correlated `seriesCollapseCond` with `DISTINCT ON (COALESCE(e.series_id, e.id))` for collapsed lists only. Keep group agenda **uncollapsed**. Verify `total` counts collapsed rows (subquery `SELECT COUNT(*) FROM (SELECT DISTINCT ON ... ) s`).

- [ ] **Step 4: `redactForViewer` without `Authorize`**

Replace `s.db.Authorize` with:

- draft + empty account → NotFound
- draft + account: `SELECT EXISTS (organizer or co_host)` — if false NotFound
- meeting link: clear unless host or `viewerStatus == "confirmed"`

- [ ] **Step 5: Geocode**

Change signature to `Geocode(ctx context.Context, location string) (float64, float64)`. Use `http.NewRequestWithContext`. If create/update already has non-zero lat/lng, skip Nominatim. Callers that ignore ctx: pass `context.Background()` only from tests.

- [ ] **Step 6: Tests**

```
go test ./microservices/the_monkeys_events/internal/database ./microservices/the_monkeys_events/internal/services ./microservices/the_monkeys_groups/...
```

Expected: PASS. Manually `EXPLAIN ANALYZE` the new list query on a DB with events.

- [ ] **Step 7: Commit**

```
git commit -m "perf: cheaper event and group list queries"
```

---

### Task 6: EventService admin payment RPCs + gateway HTTP

**Files:**
- Modify: `apis/serviceconn/gateway_event/pb/gw_event.proto`
- Generate: `gw_event.pb.go`, `gw_event_grpc.pb.go`
- Create: `microservices/the_monkeys_events/internal/database/admin_payments.go`
- Modify: `microservices/the_monkeys_events/internal/database/interface.go`
- Modify: `microservices/the_monkeys_events/internal/services/service.go`
- Create: `microservices/the_monkeys_gateway/internal/admin/payments.go`
- Modify: `microservices/the_monkeys_gateway/internal/admin/routes.go` — payments subgroup: `mware.AuthRequired`, `RequireRole(constants.RoleAdmin)`. Remove `LocalNetworkMiddleware` and `AdminKeyMiddleware` from this subgroup.
- Modify: `docs/admin-api.md`

**Interfaces — proto (add at end of `gw_event.proto`, new field numbers):**

```
message MoneyINR {
  int64 paise = 1;
  string inr = 2; // "941.00"
}

message AdminEventPaymentSummary {
  int64 event_id = 1;
  string slug = 2;
  string title = 3;
  string organizer_username = 4;
  google.protobuf.Timestamp start_time = 5;
  MoneyINR gross_captured = 6;
  MoneyINR platform_fee = 7;
  MoneyINR gst = 8;
  MoneyINR host_payable_captured = 9;
  MoneyINR refunded_gross = 10;
  MoneyINR settled = 11;
  MoneyINR open_payable = 12;
}

message AdminListEventPaymentsReq {
  int32 limit = 1;
  int32 offset = 2;
  string query = 3;
}

message AdminListEventPaymentsResp {
  repeated AdminEventPaymentSummary events = 1;
  int32 total = 2;
}

message AdminGetEventPaymentsReq { string slug = 1; }

message AdminPaymentLine {
  string attendee_username = 1;
  string razorpay_order_id = 2;
  string razorpay_payment_id = 3;
  string status = 4;
  MoneyINR gross = 5;
  MoneyINR platform_fee = 6;
  MoneyINR gst = 7;
  MoneyINR host_payable = 8;
}

message AdminSettlementLine {
  int64 id = 1;
  MoneyINR payable = 2;
  string status = 3;
  string note = 4;
}

message AdminGetEventPaymentsResp {
  AdminEventPaymentSummary event = 1;
  repeated AdminPaymentLine payments = 2;
  repeated AdminSettlementLine settlements = 3;
}

message AdminCreateSettlementReq { string slug = 1; string note = 2; }
message AdminMarkSettlementPaidReq { int64 settlement_id = 1; string note = 2; }
message AdminSettlementResp { AdminSettlementLine settlement = 1; string error = 2; }
```

Service:

```
rpc AdminListEventPayments(AdminListEventPaymentsReq) returns (AdminListEventPaymentsResp);
rpc AdminGetEventPayments(AdminGetEventPaymentsReq) returns (AdminGetEventPaymentsResp);
rpc AdminCreateSettlement(AdminCreateSettlementReq) returns (AdminSettlementResp);
rpc AdminMarkSettlementPaid(AdminMarkSettlementPaidReq) returns (AdminSettlementResp);
```

Open payable SQL:

```sql
captured.host - COALESCE(settled.paid_or_pending, 0)
```

where captured is `SUM(host_payable_paise) FILTER (WHERE status = 'captured')`.

Create settlement: if open <= 0 → FailedPrecondition `"nothing to settle"`. Insert `payable_paise = open`, `status='pending'`.

Mark paid: only `pending` → `paid`.

- [ ] **Step 1: Add proto, regenerate pb.go**

- [ ] **Step 2: Implement DB + service methods**

- [ ] **Step 3: Gateway routes** (inside existing admin group):

```
GET  /payments/events
GET  /payments/events/:slug
POST /payments/events/:slug/settlements
POST /payments/settlements/:id/mark-paid
```

Format JSON with `paise` + `inr` using a small helper `func inrJSON(paise int64) gin.H { return gin.H{"paise": paise, "inr": fmt.Sprintf("%.2f", float64(paise)/100)} }`.

- [ ] **Step 4: Tests**

Settlement cannot exceed open payable. List empty DB returns total 0.

```
go test ./microservices/the_monkeys_events/... ./microservices/the_monkeys_gateway/internal/admin/...
```

- [ ] **Step 5: Update `docs/admin-api.md` base URL to `/api/v1/admin`**

- [ ] **Step 6: Commit**

```
git commit -m "feat: admin APIs for per-event host payable after GST"
```

---

### Task 7: Migration `000019` moderation + audit

**Files:**
- Create: `schema/000019_admin_moderation.up.sql` / `.down.sql`

```sql
CREATE TABLE IF NOT EXISTS admin_audit_log (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_ip TEXT,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS user_admin_flags (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    flag_type VARCHAR(20) NOT NULL,
    reason TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_user_admin_flags_type CHECK (flag_type IN ('bot', 'fake', 'spam', 'nsfw'))
);

CREATE TABLE IF NOT EXISTS blog_admin_flags (
    id BIGSERIAL PRIMARY KEY,
    blog_id BIGINT NOT NULL REFERENCES blog(id) ON DELETE CASCADE,
    flag_type VARCHAR(20) NOT NULL,
    reason TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS event_admin_flags (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    flag_type VARCHAR(20) NOT NULL,
    reason TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_admin_flags (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    flag_type VARCHAR(20) NOT NULL,
    reason TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

```sql
INSERT INTO user_role (role_desc) VALUES ('Community') ON CONFLICT DO NOTHING;
```

Add before the flag tables. Down migration: do **not** delete `Community` if any `user_account.role_id` points at it; only delete if unused.

Inspect seeded `user_status`. If there is no `suspended`, `INSERT INTO user_status (status) VALUES ('suspended') ON CONFLICT DO NOTHING;` — only if `status` is unique (it is). Map suspend API to that id.

- [ ] **Step 1: Write SQL, apply via `docker compose up db-migrations`**

- [ ] **Step 2: Commit**

```
git commit -m "feat: admin audit and moderation flag tables"
```

---

### Task 8: Admin catalog stats + lists + real actions

**Files:**
- Modify: user/blog/group/event **protos** with `AdminStats`, `AdminListUsers`, `AdminListBlogs`, `AdminListOrphanBlogs`, `AdminListEvents`, `AdminListGroups`, flag/suspend/nsfw RPCs
- Implement in each service’s database + service layer
- Modify: `microservices/the_monkeys_gateway/internal/admin/routes.go` — replace stub handlers
- Create: `microservices/the_monkeys_gateway/internal/admin/audit.go` — `func (asc *AdminServiceClient) audit(c *gin.Context, action, entityType, entityID string, payload any)`
- Create: handlers `users.go`, `blogs.go`, `events_admin.go`, `groups_admin.go`, `stats.go`
- Dial extra gRPC clients in `RegisterAdminRouter` (events, blog, groups) the same way users are dialed today (`grpc.NewClient`, insecure — match existing)

**HTTP (all under `/api/v1/admin`). Mount three gin groups after `AuthRequired`:**

- `RequireRole(Admin)` — stats (full), users, blogs list/orphans, events list/cancel, groups list/suspend, set role, delete user
- `RequireRole(Admin, Support)` — `/verifications*`
- `RequireRole(Admin, Community)` — NSFW routes + event Q&A/comment hide

| Method | Path | Behaviour | Role |
| --- | --- | --- | --- |
| GET | `/stats` | Counts; `payments_inr` only for Admin | Admin (Support: omit payments) |
| GET | `/users` | Search username/email; include flags | Admin |
| POST | `/users/:id/role` | `{ "role": "Support"\|"Community"\|"Admin"\|"Viewer" }` | Admin |
| POST | `/users/:id/flag` | Write `user_admin_flags` | Admin |
| POST | `/users/:id/nsfw` | Flag user NSFW | Admin or Community |
| POST | `/blogs/:blog_id/nsfw` | Insert blog flag | Admin or Community |
| POST | `/events/:slug/nsfw` | Insert event flag | Admin or Community |
| POST | `/groups/:slug/nsfw` | Insert group flag | Admin or Community |

Keep verification routes; switch them from admin key to Support|Admin JWT. Audit `actor` = username + role.

- [ ] **Step 5: Manual curl (logged-in Admin cookie)**

```
curl -s -b mat=<access-jwt> http://localhost:8081/api/v1/admin/stats
curl -s -b mat=<access-jwt> http://localhost:8081/api/v1/admin/payments/events
```

Viewer cookie must get 403. Support cookie must get 403 on payments and 200 on verifications.

Orphan blogs: reuse blog service ES client already used for search. If too coupled, SQL-only list in v1 and return `"orphan_es": -1` in stats with a comment — **prefer a real ES count**. Blog service already depends on Elasticsearch in compose.

- [ ] **Step 1: Protos + generate**

- [ ] **Step 2: Implement RPCs + HTTP**

- [ ] **Step 3: Replace stub JSON in `FlagUserAsBotOrFake` / stats**

- [ ] **Step 4: Tests for flag persist + suspend + stats zeros**

```
go test ./microservices/the_monkeys_users/... ./microservices/the_monkeys_blog/... ./microservices/the_monkeys_groups/... ./microservices/the_monkeys_gateway/internal/admin/...
go vet ./...
```

- [ ] **Step 5: Manual curl (gateway running)** — see table above; do not use `X-Admin-Key`.

- [ ] **Step 6: Commit**

```
git commit -m "feat: admin catalog stats, lists, flags, and suspend actions"
```

---

### Task 9: Spec self-check and docs

**Files:**
- Modify: `docs/admin-api.md` (complete endpoint list, correct base path, warn default key)
- Modify: `docs/context.md` only if you need a one-line pointer to the spec (optional)

- [ ] **Step 1: Grep plan/spec for TBD/TODO — none allowed**

- [ ] **Step 2: Confirm ₹1000 example still 941 host in docs**

- [ ] **Step 3: Commit docs if they changed**

```
git commit -m "docs: admin payment and catalog API surface"
```

---

## Verification (before any “done” claim)

```
go test ./microservices/the_monkeys_events/...
go test ./microservices/the_monkeys_groups/...
go test ./microservices/the_monkeys_gateway/internal/admin/...
go vet ./...
```

Manual:

1. Free RSVP still confirms without Razorpay keys.
2. With keys: paid RSVP returns `order_id`; webhook `payment.captured` inserts `event_payments` with fee 5% and GST 18% of fee.
3. `GET /api/v1/admin/payments/events/:slug` shows host payable.
4. `GET /api/v1/events` local timing improved vs baseline (note times in the PR).
5. Admin `/stats` returns real counts.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-09-05-payments-admin-backend.md`.

Two execution options:

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks
2. **Inline Execution** — this session, executing-plans, batch with checkpoints

Which approach?
)
