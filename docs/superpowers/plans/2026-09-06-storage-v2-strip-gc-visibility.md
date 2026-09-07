# Storage v2 Strip, CAS GC, and Private Reads Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Strip GPS/EXIF from new image uploads without re-encoding; delete CAS blobs when the last `storage_asset_refs` row is gone; keep verification and draft-post images off public GETs.

**Architecture:** Gateway `imgstrip.Strip` runs before SHA-256 on every storage-v2 image write. `the_monkeys_storage.DeleteAssetRef` soft-deletes refs, deletes orphan `storage_assets` rows, and returns object keys; gateway and the storage Rabbit consumer `RemoveObject` those keys. Public `GET /api/v2/storage/posts|assets` uses AuthOptional and 404s unless the blog is published (or a published blog still refs the checksum).

**Tech Stack:** Go, JPEG/PNG/WebP byte walkers (no jpegtran), Postgres `storage_assets` / `storage_asset_refs` / `verification_requests` / `blog`, MinIO, gRPC `UploadBlogFile`, Gin gateway.

**Spec:** `docs/superpowers/specs/2026-09-06-storage-v2-strip-gc-visibility-design.md`

## Global Constraints

- Option A only: strip metadata, **no** quality re-encode, **no** multi-size pipeline.
- New uploads only. Do not backfill existing MinIO objects.
- Event/group/profile stay path-based (not CAS). Still strip their new uploads. Their deletes already `RemoveObject` the slug key.
- Verification stays in private bucket `the-monkeys-verification`. Never store a public URL in `verification_requests`.
- Anonymous draft-image GET returns **404**, not 403.
- Signup `role_id = 4` unchanged. Do not rewrite migrations `000001`–`000017`. Additive proto fields only.
- User runs `protoc` in WSL. Agent on Windows should not assume `protoc` exists.
- Do **not** git commit unless the user asks.
- TDD: failing test before production code for each task.

### File map

| File | Responsibility |
| --- | --- |
| `microservices/the_monkeys_gateway/internal/storage_v2/imgstrip/*.go` | Lossless metadata strip |
| `microservices/the_monkeys_gateway/internal/storage_v2/routes.go` | `prepareAssetUpload`, public GET gates, CAS delete GC |
| `microservices/the_monkeys_gateway/internal/storage_v2/events.go` / `groups.go` | Strip before event/group `PutObject` |
| `microservices/the_monkeys_gateway/internal/storage_v2/verification.go` | Already uses `prepareAssetUpload` after Task 2 |
| `apis/serviceconn/gateway_file_service/pb/gw_file_svc.proto` | `DeleteAssetRefRes.orphans`, `ResolveAssetRead` |
| `microservices/the_monkeys_gateway/internal/auth/middleware.go` | `HasBlogAccess` used by unpublished image GETs |
| `microservices/the_monkeys_storage/internal/database/database.go` | Orphan SQL + visibility SQL |
| `microservices/the_monkeys_storage/internal/consumer/consumer.go` | MinIO delete of orphans after ref soft-delete |
| `apis/serviceconn/gateway_user/pb/gw_user.proto` | `selfie_object_key` / `id_front_object_key` / `id_back_object_key` |
| `microservices/the_monkeys_users/internal/services/verification.go` | Fill object keys on list/get |

---

### Task 1: `imgstrip.Strip` (JPEG / PNG / WebP)

**Files:**
- Create: `microservices/the_monkeys_gateway/internal/storage_v2/imgstrip/strip.go`
- Create: `microservices/the_monkeys_gateway/internal/storage_v2/imgstrip/jpeg.go`
- Create: `microservices/the_monkeys_gateway/internal/storage_v2/imgstrip/png.go`
- Create: `microservices/the_monkeys_gateway/internal/storage_v2/imgstrip/webp.go`
- Test: `microservices/the_monkeys_gateway/internal/storage_v2/imgstrip/strip_test.go`

**Interfaces:**
- Consumes: none
- Produces: `func Strip(data []byte, contentType string) ([]byte, error)`

- [ ] **Step 1: Write the failing tests**

```go
package imgstrip

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestStripJPEGDropsGPSMarkerPayload(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	// SOI (FF D8) then inject APP1 containing GPS so a naive store would leak it.
	app1 := []byte{0xFF, 0xE1, 0x00, 0x08, 'G', 'P', 'S', 0x00}
	injected := append([]byte{0xFF, 0xD8}, append(app1, raw[2:]...)...)
	out, err := Strip(injected, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("GPS")) {
		t.Fatal("GPS payload must be gone")
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped jpeg must still decode: %v", err)
	}
}

func TestStripPNGDropsTextChunk(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	// Valid tests in png.go should drop tEXt; this fixture asserts API shape.
	out, err := Strip(buf.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped png must decode: %v", err)
	}
}

func TestStripNonImageUnchanged(t *testing.T) {
	in := []byte("not-an-image")
	out, err := Strip(in, "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Fatal("non-images must pass through")
	}
}

func TestStripGIFUnchanged(t *testing.T) {
	in := []byte("GIF89a-not-parsed")
	out, err := Strip(in, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Fatal("GIF must pass through")
	}
}
```

Also add `TestStripPNGRemovesTEXt` that builds a PNG with a `tEXt` chunk (see Step 3 `dropPNGChunks`) and asserts `tEXt` is absent after Strip.

JPEG Orientation (spec §2): in `jpeg.go`, if Orientation is 1 or missing, drop APP1 entirely. If 2–8, lossless rotate/flip only when both dimensions are multiples of 8 (MCU-aligned); otherwise keep a 26-byte Orientation-only Exif APP1 and still drop GPS/XMP/IPTC.

- [ ] **Step 2: Run tests — expect FAIL** (`Strip` undefined)

Run: `go test ./microservices/the_monkeys_gateway/internal/storage_v2/imgstrip -count=1`

Expected: FAIL compile or undefined `Strip`

- [ ] **Step 3: Implement**

`strip.go`:

```go
package imgstrip

import (
	"fmt"
	"strings"
)

func Strip(data []byte, contentType string) ([]byte, error) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.Contains(ct, "image/jpeg"), strings.Contains(ct, "image/jpg"):
		return stripJPEG(data)
	case strings.Contains(ct, "image/png"):
		return stripPNG(data)
	case strings.Contains(ct, "image/webp"):
		return stripWebP(data)
	default:
		return data, nil
	}
}

func fail(format string, args ...any) error {
	return fmt.Errorf("imgstrip: "+format, args...)
}
```

`jpeg.go` — walk markers after SOI. Copy SOF/DHT/DQT/DRI/SOS (SOS copies through EOI). Drop COM (`FF FE`), APP13 (`FF ED`), APP1 XMP (`http://ns.adobe.com/xap`). For APP1 Exif: parse Orientation (TIFF IFD, tag 0x0112). If missing or 1, drop the segment. If 2–8, keep that APP1 **only if** a cheap scan shows no `GPS` IFD (IFD1 GPS offset tag 0x8825 nonzero → drop GPS by dropping the whole APP1 **and** keep a 26-byte Orientation-only Exif APP1, **or** if rewriting is too error-prone keep the original APP1 — **do not** do that). Required behavior from spec: GPS must go. Implement Orientation-only APP1:

Minimal Exif APP1 body (big-endian): `Exif\0\0` + `MM\0*` + IFD0 with one entry tag 0x0112 type SHORT count 1 value=orientation.

If JPEG is truncated, return `fail("truncated jpeg")`.

`png.go` — after 8-byte signature, walk chunks. Drop types `eXIf`, `tEXt`, `iTXt`, `zTXt`, `tIME`. Copy all others including CRC.

`webp.go` — RIFF/WEBP. Drop chunks named `EXIF` and `XMP `. If `VP8X` flags bit for EXIF/XMP, clear those bits. If not RIFF/WEBP, return error.

- [ ] **Step 4: Re-run tests — expect PASS**

Run: `go test ./microservices/the_monkeys_gateway/internal/storage_v2/imgstrip -count=1`

Expected: `ok`

---

### Task 2: Strip on every storage-v2 image write

**Files:**
- Modify: `microservices/the_monkeys_gateway/internal/storage_v2/routes.go` (`prepareAssetUpload`, `UploadProfileImage` / `UpdateProfileImage` image branch)
- Modify: `microservices/the_monkeys_gateway/internal/storage_v2/events.go` (cover + gallery read-all branch)
- Modify: `microservices/the_monkeys_gateway/internal/storage_v2/groups.go` (same pattern as events)
- Test: `microservices/the_monkeys_gateway/internal/storage_v2/prepare_strip_test.go`

**Interfaces:**
- Consumes: `imgstrip.Strip`
- Produces: `prepareAssetUpload` hashes **stripped** bytes; event/group/profile `PutObject` body is stripped

- [ ] **Step 1: Failing test for checksum-after-strip**

```go
package storage_v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/storage_v2/imgstrip"
)

func TestChecksumUsesStrippedBytes(t *testing.T) {
	raw := []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x08, 'G', 'P', 'S', 0x00, 0xFF, 0xD9}
	stripped, err := imgstrip.Strip(raw, "image/jpeg")
	if err != nil {
		t.Skip("needs a decodeable jpeg from Task 1 fixtures")
	}
	sumRaw := sha256.Sum256(raw)
	sumStrip := sha256.Sum256(stripped)
	if hex.EncodeToString(sumRaw[:]) == hex.EncodeToString(sumStrip[:]) && bytes.Contains(raw, []byte("GPS")) && !bytes.Contains(stripped, []byte("GPS")) {
		t.Fatal("stripped checksum must differ when GPS was removed")
	}
}
```

Replace the tiny invalid JPEG with the same inject-APP1 fixture as Task 1 once `jpeg.Encode` round-trip is copied into this test.

Add `TestPrepareAssetUploadRejectsStripError` by exporting nothing extra: if `Strip` returns error, `prepareAssetUpload` must return that error (use a 1-byte `image/jpeg` payload).

- [ ] **Step 2: Run test — expect FAIL** (`prepareAssetUpload` still hashes raw)

- [ ] **Step 3: Implement `prepareAssetUpload` strip-before-hash**

In `prepareAssetUpload`, after `io.ReadAll` (memory path) and before `sha256.Sum256`:

```go
if strings.HasPrefix(strings.ToLower(contentType), "image/") {
    stripped, err := imgstrip.Strip(data, contentType)
    if err != nil {
        return nil, err
    }
    data = stripped
}
```

Temp-file path: after `io.Copy` to tmp, `ReadFile` / rewind, strip if image, rewrite tmp, hash stripped bytes. If strip fails, `cleanup()` and return error.

Draft post uploads: if you can detect unpublished (Task 5 will add status), for now keep `cacheControl` as today in this task; Task 5 switches draft to `private, no-store`.

Event gallery/cover and group image: after `io.ReadAll`, call `imgstrip.Strip`; on error JSON 400 `"could not strip image metadata"`; `PutObject` stripped bytes; blurhash on stripped bytes.

Profile upload/update: same in the `ReadAll` branch.

Verification already calls `prepareAssetUpload` — no extra change once Step 3 is done.

- [ ] **Step 4: Tests PASS**

Run: `go test ./microservices/the_monkeys_gateway/internal/storage_v2/imgstrip ./microservices/the_monkeys_gateway/internal/storage_v2 -count=1`

Expected: `ok` (storage_v2 package may still pull gin; that is existing)

---

### Task 3: Orphan detection SQL + proto orphans on `DeleteAssetRef`

**Files:**
- Modify: `apis/serviceconn/gateway_file_service/pb/gw_file_svc.proto` (`DeleteAssetRefRes`)
- Modify: `microservices/the_monkeys_storage/internal/database/database.go`
- Test: `microservices/the_monkeys_storage/internal/database/orphan_sql_test.go`
- User runs protoc (WSL)

**Interfaces:**
- Consumes: existing `DeleteAssetRef`
- Produces: `isOrphanChecksum(liveRefs, verificationPointers int) bool`; `DeleteAssetRefRes.orphans` (`checksum`, `object_key`); SQL `collectOrphanedAssetsSQL`

- [ ] **Step 1: Failing SQL string test**

```go
package database

import (
	"strings"
	"testing"
)

func TestIsOrphanChecksum(t *testing.T) {
	if isOrphanChecksum(1, 0) {
		t.Fatal("two-blog share: deleting one ref must not GC")
	}
	if !isOrphanChecksum(0, 0) {
		t.Fatal("last live ref gone with no verification pointer must GC")
	}
	if isOrphanChecksum(0, 1) {
		t.Fatal("verification checksum still referenced must not GC")
	}
}

func TestCollectOrphanedAssetsSQL(t *testing.T) {
	q := collectOrphanedAssetsSQL
	if !strings.Contains(q, "storage_asset_refs") || !strings.Contains(q, "deleted_at IS NULL") {
		t.Fatal("must require zero live refs")
	}
	if !strings.Contains(q, "verification_requests") || !strings.Contains(q, "selfie_checksum") {
		t.Fatal("must not GC blobs still on a verification request")
	}
	if !strings.Contains(q, "storage_assets") {
		t.Fatal("must return object_key from storage_assets")
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`collectOrphanedAssetsSQL` undefined)

- [ ] **Step 3: Proto + SQL + DeleteAssetRef**

Add to `DeleteAssetRefRes`:

```
message OrphanedAsset {
    string checksum = 1;
    string object_key = 2;
}
```

```
repeated OrphanedAsset orphans = 4;
```

WSL:

```bash
cd /mnt/c/Users/Dave/the_monkeys/the_monkeys_engine
protoc -I. apis/serviceconn/gateway_file_service/pb/gw_file_svc.proto --go_out=. --go-grpc_out=.
```

SQL (checksums passed as `ANY($1::text[])`):

```go
const collectOrphanedAssetsSQL = `
SELECT a.checksum, a.object_key
FROM storage_assets a
WHERE a.checksum = ANY($1::text[])
  AND NOT EXISTS (
      SELECT 1 FROM storage_asset_refs r
      WHERE r.checksum = a.checksum AND r.deleted_at IS NULL
  )
  AND NOT EXISTS (
      SELECT 1 FROM verification_requests v
      WHERE v.selfie_checksum = a.checksum
         OR v.id_front_checksum = a.checksum
         OR v.id_back_checksum = a.checksum
  )`
```

```go
func isOrphanChecksum(liveRefs, verificationPointers int) bool {
	return liveRefs == 0 && verificationPointers == 0
}
```

Change `DeleteAssetRef` to (spec §3.2 — DB first, then return keys for MinIO):

1. Begin a transaction.
2. `UPDATE … RETURNING checksum` for the matching live refs. Collect distinct checksums.
3. Run `collectOrphanedAssetsSQL` with those checksums.
4. For each candidate: `DELETE FROM storage_assets WHERE checksum = $1`. If FK RESTRICT / error, log and skip (do not add to the response).
5. Commit. Return `deleted_count` plus `orphans` for rows that were deleted from `storage_assets`.

RETURNING example:

```sql
UPDATE storage_asset_refs
SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE … AND deleted_at IS NULL
RETURNING checksum
```

Do **not** add a `PurgeAssets` RPC. Spec locks GC inside `DeleteAssetRef`. If MinIO `RemoveObject` later fails, the blob is an orphaned object with no DB row — acceptable; do not re-insert `storage_assets`.

- [ ] **Step 4: `go test ./microservices/the_monkeys_storage/internal/database -count=1`**

Expected: `ok` (SQL test only unless a real DB is used)

---

### Task 4: Gateway + consumer delete MinIO orphans

**Files:**
- Modify: `microservices/the_monkeys_gateway/internal/storage_v2/routes.go` (`DeletePostFile`)
- Modify: `microservices/the_monkeys_storage/internal/consumer/consumer.go` (`softDeleteAssetRefs`)
- Test: `microservices/the_monkeys_storage/internal/consumer/orphan_gc_test.go` (bucket choice helper)
- Test: `microservices/the_monkeys_gateway/internal/storage_v2/orphan_bucket_test.go`

**Interfaces:**
- Consumes: `DeleteAssetRefRes.GetOrphans()`
- Produces: `func minioBucketForObjectKey(publicBucket, verificationBucket, objectKey string) string`

- [ ] **Step 1: Failing test for bucket routing**

```go
package storage_v2

import "testing"

func TestMinioBucketForObjectKey(t *testing.T) {
	if minioBucketForObjectKey("pub", "ver", "assets/sha256/ab/cd/ab.bin") != "pub" {
		t.Fatal("cas assets use public bucket")
	}
	if minioBucketForObjectKey("pub", "ver", "verifications/sha256/deadbeef") != "ver" {
		t.Fatal("verification keys use private bucket")
	}
}
```

Put the same helper in consumer package as `minioBucketForObjectKey` (duplicate 8-line function; do not create a new shared module).

- [ ] **Step 2: Run — FAIL** undefined helper

- [ ] **Step 3: Implement helper + call sites**

```go
func minioBucketForObjectKey(publicBucket, verificationBucket, objectKey string) string {
	if strings.HasPrefix(objectKey, "verifications/") {
		return verificationBucket
	}
	return publicBucket
}
```

Gateway `DeletePostFile`: after `DeleteAssetRef` succeeds, loop `deleteRes.GetOrphans()` and `s.mc.RemoveObject` on `minioBucketForObjectKey(s.bucket, s.verificationBucket, o.GetObjectKey())`. Add `verificationBucket string` on `Service`; set it in `newService` from `cfg.Minio.VerificationBucket`, falling back to `DefaultVerificationBucket` (`the-monkeys-verification`).

If `RemoveObject` fails, log and still return HTTP 200. Spec §3.2 already deleted the `storage_assets` row inside `DeleteAssetRef`. Do **not** add `PurgeAssets`.

Consumer `softDeleteAssetRefs`: after `DeleteAssetRef`, for each orphan call `mc.RemoveObject` using the same helper (`cfg.Minio.Bucket` vs verification bucket). Replace the comment that says CAS keys are never removed with: last-ref GC deletes MinIO objects listed in `orphans`.

- [ ] **Step 4: Tests PASS**

Run: `go test ./microservices/the_monkeys_storage/internal/consumer ./microservices/the_monkeys_gateway/internal/storage_v2 -count=1 -timeout 60s`

Expected: `ok` for new unit tests (ignore pre-existing failures unrelated to this diff; if any appear in files you touched, fix them)

---

### Task 5: Draft post / CAS GET is private

**Files:**
- Modify: `apis/serviceconn/gateway_file_service/pb/gw_file_svc.proto` — `ResolveAssetRead`
- Modify: `microservices/the_monkeys_storage/internal/database/database.go`
- Modify: `microservices/the_monkeys_gateway/internal/storage_v2/routes.go` (`RegisterRoutes` GET posts/assets, `GetPostFile` / `GetAssetFile` / meta / url, `UploadPostFile` Cache-Control)
- Modify: `microservices/the_monkeys_gateway/internal/auth/middleware.go` — `HasBlogAccess`
- Test: `microservices/the_monkeys_storage/internal/database/visibility_sql_test.go`
- Test: `microservices/the_monkeys_gateway/internal/storage_v2/visibility_test.go`

**Interfaces:**
- Consumes: `constants.BlogStatusPublished`; `AuthMiddlewareConfig.HasBlogAccess(ctx, blogID) bool`
- Produces: `blogStatusAllowsPublic(status string) bool`; `decideAssetRead(allowPublic, verificationOnly, hasJWT, anyDraftAccess bool) bool`; `ResolveAssetRead`

- [ ] **Step 1: Failing tests**

```go
package storage_v2

import (
	"testing"

	"github.com/the-monkeys/the_monkeys/constants"
)

func TestBlogStatusAllowsPublic(t *testing.T) {
	if !blogStatusAllowsPublic(constants.BlogStatusPublished) {
		t.Fatal("published must be public")
	}
	for _, s := range []string{"", constants.BlogStatusDraft, constants.BlogStatusScheduled, constants.BlogStatusArchived} {
		if blogStatusAllowsPublic(s) {
			t.Fatalf("%q must not be public", s)
		}
	}
}

func TestChecksumFromAssetObjectName(t *testing.T) {
	// assets/sha256/ab/cd/{64hex}.jpg
	sum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	key := "assets/sha256/aa/aa/" + sum + ".jpg"
	got, ok := checksumFromAssetObjectName(key)
	if !ok || got != sum {
		t.Fatalf("got %q", got)
	}
}

func TestDecideAssetRead(t *testing.T) {
	if !decideAssetRead(true, false, false, false) {
		t.Fatal("published must be readable without JWT")
	}
	if decideAssetRead(false, true, true, true) {
		t.Fatal("verification keys must never stream on public CAS GET")
	}
	if decideAssetRead(false, false, false, false) {
		t.Fatal("draft-only without JWT must deny")
	}
	if !decideAssetRead(false, false, true, true) {
		t.Fatal("draft-only with JWT and blog access must allow")
	}
	if decideAssetRead(false, false, true, false) {
		t.Fatal("logged-in stranger must not read another user's draft")
	}
}
```

`visibility_sql_test.go`: assert `resolveAssetReadSQL` joins `blog` on `blog.blog_id = r.owner_id`, filters `r.owner_type = 'blog'`, `r.deleted_at IS NULL`, and mentions `verifications/` or `object_key`.

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement**

```go
func blogStatusAllowsPublic(status string) bool {
	return status == constants.BlogStatusPublished
}
```

Proto:

```
message ResolveAssetReadReq {
    string checksum = 1;
    string blog_id = 2; // optional; used for /posts/:id
}
message ResolveAssetReadResp {
    bool allow_public = 1;
    bool not_found = 2;
    bool verification_only = 3;
    repeated string unpublished_blog_ids = 4;
}
rpc ResolveAssetRead(ResolveAssetReadReq) returns (ResolveAssetReadResp);
```

SQL for checksum:

```sql
-- verification_only
SELECT object_key FROM storage_assets WHERE checksum = $1
```

If `object_key` has prefix `verifications/` → `verification_only=true` (public GET 404).

```sql
SELECT COALESCE(b.status, '')
FROM storage_asset_refs r
LEFT JOIN blog b ON b.blog_id = r.owner_id
WHERE r.checksum = $1
  AND r.owner_type = 'blog'
  AND r.deleted_at IS NULL
```

`allow_public` if **any** status is `Published`. Empty join (no blog row) = unpublished.

For `/posts/:id/:fileName`: `ResolveAssetRead` with `blog_id` set:

```sql
SELECT COALESCE(status, '') FROM blog WHERE blog_id = $1
```

No row → unpublished.

Add `ResolveAssetRead` to `StorageDB` and implement it in `database.go`. Wire the RPC in `microservices/the_monkeys_storage/internal/server/server.go` the same way `DeleteAssetRef` is forwarded.

Writes already use `mw.AuthorizationByID` on `POST/PUT/DELETE /posts/:id` (`:id` is the blog id). Reads today have no auth. Do **not** reuse `AuthorizationByID` on GETs: it returns 401/500, and spec requires **404**.

Add on `AuthMiddlewareConfig` (never abort):

```go
func (c *AuthMiddlewareConfig) HasBlogAccess(ctx *gin.Context, blogID string) bool {
	res, err := c.validateToken(ctx)
	if err != nil {
		return false
	}
	accessResp, err := c.svc.Client.CheckAccessLevel(context.Background(), &pb.AccessCheckReq{
		Email:     res.Email,
		AccountId: res.AccountId,
		UserName:  res.UserName,
		BlogId:    blogID,
	})
	if err != nil || accessResp.StatusCode != http.StatusOK {
		return false
	}
	for _, p := range accessResp.Access {
		if p == constants.PermissionRead || p == constants.PermissionEdit || p == constants.PermissionCreate {
			return true
		}
	}
	return false
}
```

Store `mw` on `Service` in `RegisterRoutes` (`svc.authz = mw`).

```go
func blogStatusAllowsPublic(status string) bool {
	return status == constants.BlogStatusPublished
}

func decideAssetRead(allowPublic, verificationOnly, hasJWT, anyDraftAccess bool) bool {
	if verificationOnly {
		return false
	}
	if allowPublic {
		return true
	}
	return hasJWT && anyDraftAccess
}

func (s *Service) abortNotFound(ctx *gin.Context) {
	ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "file not found"})
}
```

In `RegisterRoutes` (and the `/api/storage` aliases), attach `mw.AuthOptional` to:

- `GET /posts/:id/:fileName`
- `GET /posts/:id/:fileName/meta`
- `GET /posts/:id/:fileName/url`
- `GET /assets/sha256/:p1/:p2/:fileName`

Leave profile/event/group GETs public.

`GetPostFile` / meta / url: `ResolveAssetRead` with `blog_id = :id`. If `verification_only` or `!decideAssetRead(...)`, `abortNotFound`. `hasJWT` is `ctx.GetString("accountId") != ""`. `anyDraftAccess` is `s.authz.HasBlogAccess(ctx, blogID)` when not public.

`GetAssetFile`: parse checksum from `:p1/:p2/:fileName`. `ResolveAssetRead` with checksum. If `verification_only` → 404. If `allow_public` → stream. Else loop `unpublished_blog_ids` and allow only if `HasBlogAccess` is true for **any** of them; otherwise 404. Do not allow merely because a JWT exists.

When filling `unpublished_blog_ids`, include every live `owner_type=blog` ref whose joined `blog.status` is not `Published` (missing row counts as unpublished). If **any** live blog ref is `Published`, set `allow_public=true` and skip ACL.

Draft upload Cache-Control: in `UploadPostFile`, if `ResolveAssetRead` for that `blog_id` is not `allow_public`, use `private, no-store` instead of `public, max-age=31536000`.

- [ ] **Step 4: Tests + `go test` the new packages**

Run: `go test ./microservices/the_monkeys_storage/internal/database ./microservices/the_monkeys_gateway/internal/storage_v2 -count=1`

---

### Task 6: Verification object keys on API + never public-stream verification CAS

**Files:**
- Modify: `apis/serviceconn/gateway_user/pb/gw_user.proto` — fields 16–18 on `VerificationRequest`
- Modify: `microservices/the_monkeys_users/internal/services/verification.go` `toPbVerificationRequest`
- Modify: `microservices/the_monkeys_gateway/internal/storage_v2/routes.go` `GetAssetFile`
- Test: `microservices/the_monkeys_users/internal/services/verification_object_key_test.go`

**Interfaces:**
- Consumes: checksums already on the row
- Produces: `selfie_object_key = "verifications/sha256/" + checksum` when checksum nonempty (same helper as gateway `verificationObjectKey`)

- [ ] **Step 1: Failing test**

```go
package services

import "testing"

func TestVerificationObjectKey(t *testing.T) {
    if verificationObjectKey("abc") != "verifications/sha256/abc" {
        t.Fatal("path must match private bucket layout")
    }
    if verificationObjectKey("") != "" {
        t.Fatal("empty checksum → empty key")
    }
}
```

Duplicate `verificationObjectKey` in users service (do not import gateway). Gateway already has it in `verification.go`; keep both identical.

- [ ] **Step 2: FAIL**

- [ ] **Step 3: Proto + mapper + GetAssetFile guard**

```
string selfie_object_key   = 16;
string id_front_object_key = 17;
string id_back_object_key  = 18;
```

WSL protoc for `gw_user.proto`.

`toPbVerificationRequest`: set the three keys via `verificationObjectKey`.

`GetAssetFile`: parse checksum; `ResolveAssetRead`; if `verification_only` → 404. (Task 5 may already do this; this task is the proto fields + double-check.)

Do **not** insert `verification_requests` on upload. Document in `docs/admin-api.md` one line: checksums appear after `POST /api/v1/user/verification`; `*_object_key` is `verifications/sha256/{checksum}`.

- [ ] **Step 4: Tests PASS**

Run: `go test ./microservices/the_monkeys_users/internal/services ./microservices/the_monkeys_gateway/internal/storage_v2 -count=1`

---

## Verification commands (end)

```bash
go test ./microservices/the_monkeys_gateway/internal/storage_v2/imgstrip ./microservices/the_monkeys_gateway/internal/storage_v2 ./microservices/the_monkeys_storage/internal/database ./microservices/the_monkeys_storage/internal/consumer ./microservices/the_monkeys_users/internal/services -count=1
go build ./microservices/the_monkeys_gateway ./microservices/the_monkeys_storage ./microservices/the_monkeys_users
```

Expected: all `ok`, build exit 0.

## Spec coverage

| Spec | Task |
| --- | --- |
| 1.1 Strip A, all image writes | 1–2 |
| Orientation / GPS | 1 (`jpeg.go`) |
| 1.2 Last-ref MinIO + `storage_assets` | 3–4 |
| Verification FK blocks GC | 3 SQL |
| Consumer CAS untouched today | 4 |
| 1.3 Draft 404 / published public | 5 |
| Verification not on public asset GET | 5–6 |
| Object key on verification JSON | 6 |
| Tests 1–6 in spec §4 | 1 (JPEG/PNG), 3 (share + verification FK), 5 (`decideAssetRead`) |

## Placeholder scan

No TBD. Proto numbers are assigned. Protoc is the user’s WSL step, not a stub.
