# Storage v2: strip metadata, CAS GC, private drafts/verification — design spec

Date: 2026-09-06  
Branch: `feat/payments-admin-backend`  
Status: **design approved in chat (strip = option A, new uploads only). Do not implement until this spec is accepted as written.**  
Scope: gateway storage v2 + `the_monkeys_storage` Postgres refs. Backend only.

This spec is the source of truth. It covers three related bugs, not three products.

---

## 1. Product (locked)

### 1.1 Strip metadata (option A)

On **new** image uploads only: drop EXIF / GPS / XMP / IPTC / text chunks. **Pixels stay identical** (no JPEG/WebP quality re-encode). Existing MinIO objects are left alone until re-uploaded.

Apply to every storage-v2 image write: blog/post CAS, profile pic, event cover/gallery, group logo/cover, verification ID photos.

### 1.2 Last-reference delete

`storage_assets` is content-addressed (checksum). `storage_asset_refs` tracks who uses a checksum. Today `DeleteAssetRef` only sets `deleted_at`. The MinIO object and `storage_assets` row stay forever.

When the **last live reference** to a checksum is gone, delete the object from MinIO and the `storage_assets` row.

### 1.3 Private verification + draft post images

- Verification documents must never be readable without the owner or Admin/Support JWT. They already use a private bucket; public `GET /api/v2/storage/assets/sha256/...` must not serve them.
- `verification_requests` stores **checksums**, not URL paths. Upload does not insert that row; **submit** does. We will persist checksums on submit (already) and return the derived object key on list/get so staff/UI have a path. We will not store a public URL in that table.
- Draft blog/post images must not be world-readable. Published post images stay public.

---

## 2. Current behavior (why these bugs exist)

| Path | What happens today |
| --- | --- |
| `prepareAssetUpload` | SHA-256 of **raw** bytes; blurhash if ≤ 5 MB. No strip. |
| Event/group image PUT | Raw `PutObject` at `events/{slug}/…` / `groups/{slug}/…`. No CAS. |
| Profile PUT | Raw object under `profiles/{user}/profile.{ext}`. |
| Blog upload | CAS under `assets/sha256/…` + `storage_asset_refs` (`owner_type=blog`). `Cache-Control: public`. |
| Blog delete file | Soft-delete ref only. CAS blob remains. |
| `BLOG_DELETE` / account delete | Soft-delete refs; remove legacy `posts/{blogId}/` prefix. Comment in consumer: **CAS keys are untouched**. |
| `GET /posts/:id/:fileName` and `GET /assets/sha256/:p1/:p2/:fileName` | **No auth.** Draft images leak if you have the URL. |
| Verification upload | Private bucket `the-monkeys-verification`, key `verifications/sha256/{checksum}`. Registers `storage_assets` + ref `owner_type=verification`. Does **not** write `verification_requests`. |
| Verification submit | Inserts `selfie_checksum` / `id_front_checksum` / `id_back_checksum`. That is the “path” (fingerprint). Object key is `verifications/sha256/` + checksum. |

JPEG **Orientation** is in EXIF. If we strip it and do not rotate, iPhone photos can show sideways. Locked rule: if Orientation is 1, strip everything. If 2–8, apply a lossless rotate/flip when the MCU grid allows; otherwise **keep only the Orientation tag** and still drop GPS/XMP/IPTC.

---

## 3. Architecture

### 3.1 Strip (gateway)

New package: `microservices/the_monkeys_gateway/internal/storage_v2/imgstrip`

```go
func Strip(data []byte, contentType string) ([]byte, error)
```

- JPEG: drop APP1 EXIF, APP13 IPTC, XMP, COM; keep SOS/image data (except Orientation handling above).
- PNG: drop `eXIf`, `tEXt`, `iTXt`, `zTXt`, `tIME`; keep `IHDR`/`IDAT`/`PLTE`/`tRNS` and color (`gAMA`/`sRGB`/`iCCP`).
- WebP: drop EXIF/XMP chunks.
- GIF / AVIF / SVG / non-image: return input unchanged.
- Failure: return error. Callers **reject the upload** (do not store GPS because the stripper crashed).

Call `Strip` inside `prepareAssetUpload` **before** SHA-256 (covers posts + verification). Event/group/profile writers that skip `prepareAssetUpload` must run the same helper then hash/put.

No new microservice. No `jpegtran` binary. No decode+re-encode.

### 3.2 Last-ref GC (`the_monkeys_storage`)

Extend `DeleteAssetRef` (and the consumer’s `softDeleteAssetRefs`):

1. Soft-delete matching refs (`deleted_at`).
2. For each distinct checksum touched, if **no** row in `storage_asset_refs` with `deleted_at IS NULL` **and** no `verification_requests` row still pointing at that checksum (`selfie_checksum` / `id_front` / `id_back`), then:
   - `DELETE FROM storage_assets WHERE checksum = $1` (FK RESTRICT makes this fail if we missed a pointer — treat as skip + log).
   - Return `orphaned_object_keys` (and bucket hint: public vs verification) to the caller.

Gateway `DeletePostFile`: after successful `DeleteAssetRef`, `RemoveObject` each orphaned key in the right bucket.

Storage **Rabbit consumer** (`BLOG_DELETE`, `USER_ACCOUNT_DELETE`): after soft-delete refs, run the same orphan query and `RemoveObject` (consumer already has a MinIO client).

Do **not** delete a checksum used by another live blog, profile, or verification request.

Event/group/profile files are **not** CAS today; their delete already `RemoveObject`s the slug path. Out of scope for CAS GC unless we later move them onto checksums.

### 3.3 Visibility

**Verification**

- Keep private bucket, no bucket policy, `Cache-Control: private, no-store`.
- Public `GetAssetFile` only reads the **public** bucket and prefix `assets/sha256/`. If `storage_assets.object_key` starts with `verifications/`, never stream it on that route (404).
- List/get verification APIs: include checksums **and** `object_key` derived as `verifications/sha256/{checksum}` (or join `storage_assets.object_key`). Staff still fetch bytes via `GET /api/v2/storage/verifications/:request_id/:kind/url`.
- Upload still happens **before** submit. That cannot insert `verification_requests` until the user submits (need type/country). Frontend: upload → submit with checksums. If a row is missing checksums, submit did not run or failed — not a strip bug.

**Draft posts**

- `GET /api/v2/storage/posts/:id/:fileName` and `/url` / `/meta`: if the blog is not published, require JWT and the same edit/view access as draft HTML. Anonymous → **404** (do not leak existence with 403).
- `GET /api/v2/storage/assets/sha256/...`: resolve checksum. If **any** live `owner_type=blog` ref belongs to a **published** blog, allow public read. If every live blog ref is draft/unpublished (or only other private owners), require a caller who can access at least one of those drafts; else **404**.
- Published blogs: keep public cache headers. Draft uploads: `Cache-Control: private, no-store`.
- Blog status: Postgres `blog` if present; if no row (ES-only draft), treat as **unpublished**.

Profile / published event / published group images stay public.

---

## 4. Tests (required)

1. JPEG fixture with GPS: after `Strip`, no `GPS`, decode bounds unchanged; Orientation 1 files bit-identical except dropped APP segments.
2. PNG with `tEXt`: text chunk gone; pixels unchanged.
3. `DeleteAssetRef` with two blogs sharing a checksum: deleting one ref does **not** return orphan; deleting the second does.
4. Verification checksum still referenced → not orphaned.
5. `GetAssetFile` for a draft-only checksum without JWT → 404; owner JWT → 200.
6. `GetAssetFile` for a checksum also used by a published blog → 200 without JWT.

---

## 5. Out of scope

- Re-encoding / WebP conversion / multiple sizes (option B/C).
- Backfill of existing objects.
- Moving event/group/profile onto CAS.
- Frontend work except: draft image URLs must send cookies; verification still upload-then-submit.

---

## 6. Deploy

Strip and draft-auth are gateway-only. GC needs storage service + gateway + consumer together so last-ref delete actually removes blobs.

Unknown `action`s on Rabbit stay log-and-ignore (unchanged).
