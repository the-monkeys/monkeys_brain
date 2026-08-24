package storage_v2

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
)

// groupImageKinds enumerates the two fixed image slots a group owns. Any other
// kind is rejected at the boundary so a caller cannot write arbitrary keys.
var groupImageKinds = map[string]struct{}{"logo": {}, "cover": {}}

// groupImageBase returns the extensionless key prefix for a group image kind,
// e.g. groups/{slug}/logo. The stored object carries a real extension derived
// from its Content-Type, matching the profile-image convention.
func groupImageBase(slug, kind string) string {
	return "groups/" + slug + "/" + kind
}

// UploadGroupImage stores a group's logo or cover.
//
// Method: POST /api/v1/groups/:slug/images/:kind (auth + manage/edit guard)
// Input: multipart form field `image`
// Behavior: stores to groups/{slug}/{kind}.{ext}, computes BlurHash/dimensions,
// and prunes stale extension variants.
//
// Authorization is enforced upstream by the groups router's group permission
// guard; this handler therefore trusts the :slug it is handed. It never reads
// the caller identity, so it must not be registered on an unguarded route.
func (s *Service) UploadGroupImage(ctx *gin.Context) {
	slug := ctx.Param("slug")
	kind := ctx.Param("kind")
	if _, ok := groupImageKinds[kind]; !ok {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid image kind"})
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
	objectName := groupImageBase(slug, kind) + ext

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
		s.log.Errorf("minio PutObject (group image) error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "upload failed"})
		return
	}

	// Remove prior extension variants so a re-upload as a new type doesn't leak.
	s.cleanupOtherGroupImageKeys(ctx.Request.Context(), slug, kind, objectName)

	resp := gin.H{
		"bucket":      s.bucket,
		"object":      objectName,
		"etag":        info.ETag,
		"size":        info.Size,
		"contentType": contentType,
	}
	s.enrichResponse(ctx.Request.Context(), resp, objectName, opts)
	// Point clients at the extensionless, route-resolvable path.
	if urlStr, err := s.presignedOrCDNURL(ctx.Request.Context(), groupImageBase(slug, kind), 10*time.Minute); err == nil {
		resp["url"] = urlStr
	}
	ctx.JSON(http.StatusCreated, resp)
}

// GetGroupImage streams a group's logo or cover (public).
//
// Method: GET /api/v2/storage/groups/:slug/:kind
func (s *Service) GetGroupImage(ctx *gin.Context) {
	slug := ctx.Param("slug")
	kind := ctx.Param("kind")
	if _, ok := groupImageKinds[kind]; !ok {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid image kind"})
		return
	}

	objectName, found := s.resolveGroupImageKey(ctx.Request.Context(), slug, kind)
	if !found {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "image not found"})
		return
	}
	s.streamObject(ctx, objectName, "image not found")
}

// DeleteGroupImage removes a group's logo or cover.
//
// Method: DELETE /api/v1/groups/:slug/images/:kind (auth + edit guard)
func (s *Service) DeleteGroupImage(ctx *gin.Context) {
	slug := ctx.Param("slug")
	kind := ctx.Param("kind")
	if _, ok := groupImageKinds[kind]; !ok {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid image kind"})
		return
	}

	objectName, found := s.resolveGroupImageKey(ctx.Request.Context(), slug, kind)
	if !found {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "image not found"})
		return
	}

	if err := s.mc.RemoveObject(ctx.Request.Context(), s.bucket, objectName, minio.RemoveObjectOptions{}); err != nil {
		s.log.Errorf("minio RemoveObject (group image) error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "delete failed"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "deleted", "object": objectName})
}

// resolveGroupImageKey finds the stored object key for a group image kind,
// trying the canonical extension-bearing keys in turn.
func (s *Service) resolveGroupImageKey(ctx context.Context, slug, kind string) (string, bool) {
	base := groupImageBase(slug, kind)
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

// cleanupOtherGroupImageKeys removes image keys for the same kind that differ
// from currentKey, covering old extension variants after a re-upload.
func (s *Service) cleanupOtherGroupImageKeys(ctx context.Context, slug, kind, currentKey string) {
	base := groupImageBase(slug, kind)
	if base != currentKey {
		if _, err := s.mc.StatObject(ctx, s.bucket, base, minio.StatObjectOptions{}); err == nil {
			if err := s.mc.RemoveObject(ctx, s.bucket, base, minio.RemoveObjectOptions{}); err != nil {
				s.log.Warnf("failed to clean up group image key %s: %v", base, err)
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
				s.log.Warnf("failed to clean up group image key %s: %v", key, err)
			}
		}
	}
}
