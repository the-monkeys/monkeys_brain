package storage_v2

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

// maxEventPhotos caps an event's gallery at four images to match the
// Instagram-style carousel the client renders. It is enforced server-side so a
// crafted request cannot exceed it regardless of client behaviour.
const maxEventPhotos = 4

// eventPhotosPrefix returns the listing prefix for an event's gallery, e.g.
// events/{slug}/photos/. Objects underneath carry a UUID name plus a real
// extension derived from their Content-Type.
func eventPhotosPrefix(slug string) string {
	return "events/" + slug + "/photos/"
}

// eventCoverBase returns the extensionless key for an event's cover image, e.g.
// events/{slug}/cover. The stored object carries a real extension derived from
// its Content-Type, matching the group-image convention.
func eventCoverBase(slug string) string {
	return "events/" + slug + "/cover"
}

// safePhotoName rejects any :photo path param that could escape the event's
// prefix. Gin binds a single segment, but decoding and traversal tokens are
// still guarded here as defence in depth.
func safePhotoName(photo string) bool {
	return photo != "" && !strings.ContainsAny(photo, "/\\") && !strings.Contains(photo, "..")
}

// UploadEventPhoto appends one image to an event's gallery (max 4).
//
// Method: POST /api/v1/events/:slug/photos (auth + edit_event guard)
// Input: multipart form field `image`
//
// Authorization is enforced upstream by the events router's host permission
// guard; this handler therefore trusts the :slug it is handed. It never reads
// the caller identity, so it must not be registered on an unguarded route.
func (s *Service) UploadEventPhoto(ctx *gin.Context) {
	slug := ctx.Param("slug")

	// Enforce the gallery cap before accepting the upload so a full gallery
	// never spends bandwidth on a body we would only discard.
	if s.countEventPhotos(ctx.Request.Context(), slug) >= maxEventPhotos {
		ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"message": "gallery is full (max 4 photos)"})
		return
	}

	file, fileHeader, err := ctx.Request.FormFile("image")
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "missing image"})
		return
	}
	defer file.Close()

	contentType := fileHeader.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "file must be an image"})
		return
	}
	ext := extFromContentType(contentType)
	if ext == "" {
		ext = ".jpg"
	}
	// A UUID name lets a gallery hold several photos without slot bookkeeping,
	// and keeps keys unguessable so listing is the only enumeration path.
	objectName := eventPhotosPrefix(slug) + uuid.NewString() + ext

	var finalReader io.Reader = file
	objectSize := fileHeader.Size

	opts := minio.PutObjectOptions{
		ContentType:  contentType,
		CacheControl: "public, max-age=3600, must-revalidate",
	}

	if fileHeader.Size <= imageMetadataLimit {
		data, err := io.ReadAll(file)
		if err == nil {
			if hash, w, h, ok := s.computeImageMetadata(contentType, data); ok {
				opts.UserMetadata = map[string]string{
					"x-blurhash": hash,
					"x-width":    strconv.Itoa(w),
					"x-height":   strconv.Itoa(h),
				}
			}
			finalReader = bytes.NewReader(data)
			objectSize = int64(len(data))
		} else {
			_, _ = file.Seek(0, io.SeekStart)
		}
	}

	info, err := s.mc.PutObject(ctx.Request.Context(), s.bucket, objectName, finalReader, objectSize, opts)
	if err != nil {
		s.log.Errorf("minio PutObject (event photo) error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "upload failed"})
		return
	}

	resp := gin.H{
		"id":          path.Base(objectName),
		"bucket":      s.bucket,
		"object":      objectName,
		"etag":        info.ETag,
		"size":        info.Size,
		"contentType": contentType,
	}
	// enrichResponse sets resp["url"] to the domain-free, route-resolvable path
	// that GetEventPhoto serves.
	s.enrichResponse(ctx.Request.Context(), resp, objectName, opts)
	ctx.JSON(http.StatusCreated, resp)
}

// ListEventPhotos returns an event's gallery (public).
//
// Method: GET /api/v2/storage/events/:slug/photos
func (s *Service) ListEventPhotos(ctx *gin.Context) {
	slug := ctx.Param("slug")
	prefix := eventPhotosPrefix(slug)

	ch := s.mc.ListObjects(ctx.Request.Context(), s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	photos := make([]gin.H, 0, maxEventPhotos)
	for obj := range ch {
		if obj.Err != nil {
			s.log.Errorf("list event photos error: %v", obj.Err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "list failed"})
			return
		}
		if obj.Key == prefix {
			continue
		}
		urlStr, _ := s.presignedOrCDNURL(ctx.Request.Context(), obj.Key, 10*time.Minute)
		photos = append(photos, gin.H{
			"id":   path.Base(obj.Key),
			"url":  urlStr,
			"size": obj.Size,
			"etag": obj.ETag,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{"photos": photos})
}

// GetEventPhoto streams one gallery image (public).
//
// Method: GET /api/v2/storage/events/:slug/photos/:photo
func (s *Service) GetEventPhoto(ctx *gin.Context) {
	slug := ctx.Param("slug")
	photo := ctx.Param("photo")
	if !safePhotoName(photo) {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid photo"})
		return
	}
	s.streamObject(ctx, eventPhotosPrefix(slug)+photo, "photo not found")
}

// DeleteEventPhoto removes one gallery image.
//
// Method: DELETE /api/v1/events/:slug/photos/:photo (auth + edit_event guard)
func (s *Service) DeleteEventPhoto(ctx *gin.Context) {
	slug := ctx.Param("slug")
	photo := ctx.Param("photo")
	if !safePhotoName(photo) {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid photo"})
		return
	}
	objectName := eventPhotosPrefix(slug) + photo

	if _, err := s.mc.StatObject(ctx.Request.Context(), s.bucket, objectName, minio.StatObjectOptions{}); err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "photo not found"})
		return
	}
	if err := s.mc.RemoveObject(ctx.Request.Context(), s.bucket, objectName, minio.RemoveObjectOptions{}); err != nil {
		s.log.Errorf("minio RemoveObject (event photo) error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "delete failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "deleted", "object": objectName})
}

// countEventPhotos returns how many gallery objects an event currently holds.
// It walks the prefix rather than trusting a client-supplied count, keeping the
// max-4 cap authoritative on the server.
func (s *Service) countEventPhotos(ctx context.Context, slug string) int {
	prefix := eventPhotosPrefix(slug)
	count := 0
	ch := s.mc.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	for obj := range ch {
		if obj.Err != nil {
			s.log.Warnf("count event photos list error: %v", obj.Err)
			continue
		}
		if obj.Key == prefix {
			continue
		}
		count++
	}
	return count
}

// UploadEventCoverImage stores an event's cover image.
//
// Method: POST /api/v1/events/:slug/images/cover (auth + edit_event guard)
// Input: multipart form field `image`
// Behavior: stores to events/{slug}/cover.{ext}, computes BlurHash/dimensions,
// and prunes stale extension variants so a re-upload as a new type never leaks.
//
// Authorization is enforced upstream by the events router's host permission
// guard; this handler therefore trusts the :slug it is handed. It never reads
// the caller identity, so it must not be registered on an unguarded route.
func (s *Service) UploadEventCoverImage(ctx *gin.Context) {
	slug := ctx.Param("slug")

	file, fileHeader, err := ctx.Request.FormFile("image")
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "missing image"})
		return
	}
	defer file.Close()

	contentType := fileHeader.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "file must be an image"})
		return
	}
	ext := extFromContentType(contentType)
	if ext == "" {
		ext = ".jpg"
	}
	objectName := eventCoverBase(slug) + ext

	var finalReader io.Reader = file
	objectSize := fileHeader.Size

	opts := minio.PutObjectOptions{
		ContentType:  contentType,
		CacheControl: "public, max-age=3600, must-revalidate",
	}

	if fileHeader.Size <= imageMetadataLimit {
		data, err := io.ReadAll(file)
		if err == nil {
			if hash, w, h, ok := s.computeImageMetadata(contentType, data); ok {
				opts.UserMetadata = map[string]string{
					"x-blurhash": hash,
					"x-width":    strconv.Itoa(w),
					"x-height":   strconv.Itoa(h),
				}
			}
			finalReader = bytes.NewReader(data)
			objectSize = int64(len(data))
		} else {
			_, _ = file.Seek(0, io.SeekStart)
		}
	}

	info, err := s.mc.PutObject(ctx.Request.Context(), s.bucket, objectName, finalReader, objectSize, opts)
	if err != nil {
		s.log.Errorf("minio PutObject (event cover) error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "upload failed"})
		return
	}

	s.cleanupOtherEventCoverKeys(ctx.Request.Context(), slug, objectName)

	resp := gin.H{
		"bucket":      s.bucket,
		"object":      objectName,
		"etag":        info.ETag,
		"size":        info.Size,
		"contentType": contentType,
	}
	s.enrichResponse(ctx.Request.Context(), resp, objectName, opts)
	// Point clients at the extensionless, route-resolvable path.
	if urlStr, err := s.presignedOrCDNURL(ctx.Request.Context(), eventCoverBase(slug), 10*time.Minute); err == nil {
		resp["url"] = urlStr
	}
	ctx.JSON(http.StatusCreated, resp)
}

// GetEventCoverImage streams an event's cover image (public).
//
// Method: GET /api/v2/storage/events/:slug/cover
func (s *Service) GetEventCoverImage(ctx *gin.Context) {
	slug := ctx.Param("slug")
	objectName, found := s.resolveEventCoverKey(ctx.Request.Context(), slug)
	if !found {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "image not found"})
		return
	}
	s.streamObject(ctx, objectName, "image not found")
}

// DeleteEventCoverImage removes an event's cover image.
//
// Method: DELETE /api/v1/events/:slug/images/cover (auth + edit_event guard)
func (s *Service) DeleteEventCoverImage(ctx *gin.Context) {
	slug := ctx.Param("slug")
	objectName, found := s.resolveEventCoverKey(ctx.Request.Context(), slug)
	if !found {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "image not found"})
		return
	}
	if err := s.mc.RemoveObject(ctx.Request.Context(), s.bucket, objectName, minio.RemoveObjectOptions{}); err != nil {
		s.log.Errorf("minio RemoveObject (event cover) error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "delete failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "deleted", "object": objectName})
}

// resolveEventCoverKey finds the stored object key for an event's cover image,
// trying the canonical extension-bearing keys in turn.
func (s *Service) resolveEventCoverKey(ctx context.Context, slug string) (string, bool) {
	base := eventCoverBase(slug)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".avif", ".gif"} {
		key := base + ext
		if _, err := s.mc.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err == nil {
			return key, true
		}
	}
	if _, err := s.mc.StatObject(ctx, s.bucket, base, minio.StatObjectOptions{}); err == nil {
		return base, true
	}
	return "", false
}

// cleanupOtherEventCoverKeys removes cover keys that differ from currentKey,
// covering old extension variants after a re-upload.
func (s *Service) cleanupOtherEventCoverKeys(ctx context.Context, slug, currentKey string) {
	base := eventCoverBase(slug)
	if base != currentKey {
		if _, err := s.mc.StatObject(ctx, s.bucket, base, minio.StatObjectOptions{}); err == nil {
			if err := s.mc.RemoveObject(ctx, s.bucket, base, minio.RemoveObjectOptions{}); err != nil {
				s.log.Warnf("failed to clean up event cover key %s: %v", base, err)
			}
		}
	}
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".avif", ".gif"} {
		key := base + ext
		if key == currentKey {
			continue
		}
		if _, err := s.mc.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err == nil {
			if err := s.mc.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
				s.log.Warnf("failed to clean up event cover key %s: %v", key, err)
			}
		}
	}
}
