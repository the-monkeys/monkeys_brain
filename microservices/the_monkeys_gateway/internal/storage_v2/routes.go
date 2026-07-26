package storage_v2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"

	"github.com/bbrks/go-blurhash"
	"go.uber.org/zap"
	"golang.org/x/image/webp"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/utils"
)

// Router groups and handlers for /api/v2/storage
// This is a thin HTTP layer; implementation wiring to MinIO and image processing will be added next.

type Service struct {
	mc           *minio.Client
	storageCli   pb.UploadBlogFileClient
	bucket       string
	cdnURL       string
	publicBase   string // optional public base (scheme+host) to generate presigned URLs for
	publicSigner *minio.Client
	log          *zap.SugaredLogger
}

const (
	imageMetadataLimit = 5 * 1024 * 1024
	memoryUploadLimit  = 32 * 1024 * 1024

	postAssetOwnerType = "blog"
	postAssetPurpose   = "editor_image"
)

type preparedAssetUpload struct {
	checksum string
	reader   io.Reader
	size     int64
	width    int32
	height   int32
	blurhash string
	cleanup  func()
}

func newService(cfg *config.Config, storageCli pb.UploadBlogFileClient, log *zap.SugaredLogger) (*Service, error) {
	cli, err := minio.New(cfg.Minio.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.Minio.AccessKey, cfg.Minio.SecretKey, ""),
		Secure: cfg.Minio.UseSSL,
	})
	if err != nil {
		return nil, err
	}

	svc := &Service{mc: cli, storageCli: storageCli, bucket: cfg.Minio.Bucket, cdnURL: cfg.Minio.CDNURL, log: log}
	// If a public base is provided (e.g., http://localhost:9000), create a signer bound to that host
	if v := strings.TrimSpace(cfg.Minio.PublicBaseURL); v != "" {
		svc.publicBase = strings.TrimRight(v, "/")
		if pu, err := url.Parse(svc.publicBase); err == nil {
			endpoint := pu.Host
			secure := pu.Scheme == "https"
			ps, psErr := minio.New(endpoint, &minio.Options{
				Creds:  credentials.NewStaticV4(cfg.Minio.AccessKey, cfg.Minio.SecretKey, ""),
				Secure: secure,
				Region: "us-east-1",
			})
			if psErr != nil {
				log.Warnf("failed to init public signer for presigned URLs: %v", psErr)
			} else {
				svc.publicSigner = ps
			}
		}
	}

	// Ensure bucket exists
	ctx := context.Background()
	exists, err := svc.mc.BucketExists(ctx, svc.bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := svc.mc.MakeBucket(ctx, svc.bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, err
		}
		log.Infof("Created MinIO bucket: %s", svc.bucket)
	}

	return svc, nil
}

func newServiceWithRetry(cfg *config.Config, storageCli pb.UploadBlogFileClient, log *zap.SugaredLogger) (*Service, error) {
	const maxRetries = 5
	backoff := 2 * time.Second

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		svc, err := newService(cfg, storageCli, log)
		if err == nil {
			if attempt > 1 {
				log.Infof("MinIO connected on attempt %d", attempt)
			}
			return svc, nil
		}
		lastErr = err
		log.Warnf("MinIO init attempt %d/%d failed: %v (retry in %v)", attempt, maxRetries, err, backoff)
		time.Sleep(backoff)
		backoff *= 2 // exponential backoff: 2s, 4s, 8s, 16s, 32s
	}
	return nil, fmt.Errorf("MinIO init failed after %d attempts: %w", maxRetries, lastErr)
}

func RegisterRoutes(router *gin.Engine, cfg *config.Config, storageCli pb.UploadBlogFileClient, authClient *auth.ServiceClient, log *zap.SugaredLogger) *Service {
	mw := auth.InitAuthMiddleware(authClient, log)

	svc, err := newServiceWithRetry(cfg, storageCli, log)
	if err != nil {
		log.Fatalf("failed to initialize MinIO client: %v", err)
	}

	v2 := router.Group("/api/v2/storage")

	// Public reads (CDN-style). We may later gate or sign URLs as needed.
	{
		// Stream the user's profile image (public). Canonical key.
		v2.GET("/profiles/:user_id/profile", svc.GetProfileImage)
		// JSON metadata for profile image.
		v2.GET("/profiles/:user_id/profile/meta", svc.GetProfileMeta)
		// Presigned/CDN URL for profile image.
		v2.GET("/profiles/:user_id/profile/url", svc.GetProfileURL)
	}
	{
		// Stream legacy blog files and checksum-addressed CAS assets.
		v2.GET("/posts/:id/:fileName", svc.GetPostFile)
		v2.GET("/assets/sha256/:p1/:p2/:fileName", svc.GetAssetFile)
		// Fast-load helpers (public): metadata + presigned/CDN URL
		// JSON with etag, size, contentType, lastModified, cacheControl, blurhash, width, height, url.
		v2.GET("/posts/:id/:fileName/meta", svc.GetPostFileMeta)
		// Returns presigned or CDN URL for direct delivery. Optional ?expires=seconds.
		v2.GET("/posts/:id/:fileName/url", svc.GetPostFileURL)
	}

	// Auth-required writes and deletes (and management)
	v2.Use(mw.AuthRequired)

	// Blog content CRUD
	{
		// Upload multipart form field `file`. Stores/reuses a checksum-addressed asset and creates a blog reference.
		v2.POST("/posts/:id", mw.AuthorizationByID, svc.UploadPostFile)
		// List all objects under posts/{id}/ (auth required).
		v2.GET("/posts/:id", svc.ListPostFiles)
		// Return metadata in headers (ETag, Last-Modified, Cache-Control, X-Blurhash, X-Image-Width, X-Image-Height).
		v2.HEAD("/posts/:id/:fileName", svc.HeadPostFile)
		// Replace an existing file with multipart field `file`. Updates metadata for images.
		v2.PUT("/posts/:id/:fileName", mw.AuthorizationByID, svc.UpdatePostFile)
		// Delete the blog file reference. Legacy path-based objects are removed as a fallback.
		v2.DELETE("/posts/:id/:fileName", mw.AuthorizationByID, svc.DeletePostFile)
	}

	// Profile image CRUD (single resource)
	{
		// Upload multipart field `profile_pic`. Canonical key per user.
		v2.POST("/profiles/:user_id/profile", svc.UploadProfileImage)
		// Metadata in headers (ETag, Last-Modified, Cache-Control, X-Blurhash, X-Image-Width, X-Image-Height).
		v2.HEAD("/profiles/:user_id/profile", svc.HeadProfileImage)
		// Replace profile image (multipart `profile_pic`).
		v2.PUT("/profiles/:user_id/profile", svc.UpdateProfileImage)
		// Delete profile image.
		v2.DELETE("/profiles/:user_id/profile", svc.DeleteProfileImage)

	}

	// ---- Version-free aliases (/api/storage/...) ----
	// These are version-agnostic routes that forward to the same v2 handlers.
	// Frontend code should migrate to these so a future v3 backend change
	// only needs a gateway config update, not a client-side rollout.
	vf := router.Group("/api/storage")
	{
		// Public reads (posts + profiles)
		vf.GET("/profiles/:user_id/profile", svc.GetProfileImage)
		vf.GET("/profiles/:user_id/profile/meta", svc.GetProfileMeta)
		vf.GET("/profiles/:user_id/profile/url", svc.GetProfileURL)
		vf.GET("/posts/:id/:fileName", svc.GetPostFile)
		vf.GET("/assets/sha256/:p1/:p2/:fileName", svc.GetAssetFile)
		vf.GET("/posts/:id/:fileName/meta", svc.GetPostFileMeta)
		vf.GET("/posts/:id/:fileName/url", svc.GetPostFileURL)
	}
	vf.Use(mw.AuthRequired)
	{
		// Auth-required writes/deletes (posts)
		vf.POST("/posts/:id", mw.AuthorizationByID, svc.UploadPostFile)
		vf.GET("/posts/:id", svc.ListPostFiles)
		vf.HEAD("/posts/:id/:fileName", svc.HeadPostFile)
		vf.PUT("/posts/:id/:fileName", mw.AuthorizationByID, svc.UpdatePostFile)
		vf.DELETE("/posts/:id/:fileName", mw.AuthorizationByID, svc.DeletePostFile)
		// Auth-required writes/deletes (profiles)
		vf.POST("/profiles/:user_id/profile", svc.UploadProfileImage)
		vf.HEAD("/profiles/:user_id/profile", svc.HeadProfileImage)
		vf.PUT("/profiles/:user_id/profile", svc.UpdateProfileImage)
		vf.DELETE("/profiles/:user_id/profile", svc.DeleteProfileImage)
	}

	return svc
}

// uniqueName generates a UUID-based filename preserving the original extension.
func uniqueName(original string) string {
	ext := filepath.Ext(original)
	if ext == "" {
		return uuid.NewString()
	}
	return uuid.NewString() + ext
}

// extFromContentType returns the canonical file extension (with leading dot)
// for the given Content-Type. Returns "" for unsupported/unknown types.
// This keeps MinIO object keys self-documenting: "profile.png" instead of "profile".
func extFromContentType(ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.Contains(ct, "image/png"):
		return ".png"
	case strings.Contains(ct, "image/jpeg"):
		return ".jpg"
	case strings.Contains(ct, "image/webp"):
		return ".webp"
	case strings.Contains(ct, "image/avif"):
		return ".avif"
	case strings.Contains(ct, "image/gif"):
		return ".gif"
	case strings.Contains(ct, "image/svg+xml"):
		return ".svg"
	default:
		return ""
	}
}

// metaValue fetches a header-like key from a case-insensitive map provided by MinIO/S3 user metadata.
func metaValue(m map[string]string, key string) string {
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// computeImageMetadata decodes the image and computes:
// - BlurHash placeholder (used for LQIP rendering on clients)
// - Intrinsic width and height
// Returns ok=false if data is not a supported image.
func (s *Service) computeImageMetadata(contentType string, data []byte) (hash string, w, h int, ok bool) {
	var img image.Image
	if strings.Contains(strings.ToLower(contentType), "image/webp") {
		m, err := webp.Decode(bytes.NewReader(data))
		if err != nil {
			s.log.Warnf("webp decode failed for blurhash (ct=%s, len=%d): %v", contentType, len(data), err)
			return "", 0, 0, false
		}
		img = m
	} else {
		m, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			s.log.Warnf("image decode failed for blurhash (ct=%s, len=%d): %v", contentType, len(data), err)
			return "", 0, 0, false
		}
		img = m
	}
	hash, err := blurhash.Encode(4, 3, img)
	if err != nil {
		s.log.Warnf("blurhash encode failed (ct=%s, bounds=%v): %v", contentType, img.Bounds(), err)
		return "", 0, 0, false
	}
	b := img.Bounds()
	return hash, b.Dx(), b.Dy(), true
}

func (s *Service) prepareAssetUpload(file multipart.File, fileHeader *multipart.FileHeader, contentType string) (*preparedAssetUpload, error) {
	if fileHeader.Size <= memoryUploadLimit {
		data, err := io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		prepared := &preparedAssetUpload{
			checksum: hex.EncodeToString(sum[:]),
			reader:   bytes.NewReader(data),
			size:     int64(len(data)),
		}
		if strings.HasPrefix(strings.ToLower(contentType), "image/") && int64(len(data)) <= imageMetadataLimit {
			if hash, w, h, ok := s.computeImageMetadata(contentType, data); ok {
				prepared.blurhash = hash
				prepared.width = int32(w)
				prepared.height = int32(h)
			}
		}
		return prepared, nil
	}

	tmp, err := os.CreateTemp("", "the-monkeys-upload-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}

	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hasher), file)
	if err != nil {
		cleanup()
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, err
	}

	return &preparedAssetUpload{
		checksum: hex.EncodeToString(hasher.Sum(nil)),
		reader:   tmp,
		size:     size,
		cleanup:  cleanup,
	}, nil
}

func (s *Service) assetObjectName(checksum, contentType, originalName string) string {
	ext := extFromContentType(contentType)
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(originalName))
	}
	if len(checksum) < 4 {
		return "assets/sha256/" + checksum + ext
	}
	return fmt.Sprintf("assets/sha256/%s/%s/%s%s", checksum[:2], checksum[2:4], checksum, ext)
}

func (s *Service) metadataFromPrepared(prepared *preparedAssetUpload) map[string]string {
	if prepared.blurhash == "" && prepared.width == 0 && prepared.height == 0 {
		return nil
	}
	return map[string]string{
		"x-blurhash": prepared.blurhash,
		"x-width":    strconv.Itoa(int(prepared.width)),
		"x-height":   strconv.Itoa(int(prepared.height)),
	}
}

func (s *Service) ensureAssetStored(ctx context.Context, prepared *preparedAssetUpload, originalName, contentType, cacheControl string) (objectName string, etag string, uploaded bool, err error) {
	checkRes, checkErr := s.storageCli.CheckAsset(ctx, &pb.CheckAssetReq{Checksum: prepared.checksum})
	if checkErr == nil && checkRes.Exists {
		return checkRes.ObjectKey, "", false, nil
	}
	if checkErr != nil {
		s.log.Warnw("asset check failed; proceeding with candidate upload", "checksum", prepared.checksum, "err", checkErr)
	}

	objectName = s.assetObjectName(prepared.checksum, contentType, originalName)
	opts := minio.PutObjectOptions{
		ContentType:  contentType,
		CacheControl: cacheControl,
		UserMetadata: s.metadataFromPrepared(prepared),
	}
	info, err := s.mc.PutObject(ctx, s.bucket, objectName, prepared.reader, prepared.size, opts)
	if err != nil {
		return "", "", false, err
	}

	registerRes, err := s.storageCli.RegisterAsset(ctx, &pb.RegisterAssetReq{
		Checksum:    prepared.checksum,
		ObjectKey:   objectName,
		ContentType: contentType,
		Size:        prepared.size,
		Width:       prepared.width,
		Height:      prepared.height,
		Blurhash:    prepared.blurhash,
	})
	if err != nil {
		return "", "", true, err
	}
	if !registerRes.Success {
		return "", "", true, fmt.Errorf("register asset failed: %s", registerRes.Error)
	}
	if registerRes.ObjectKey != "" {
		objectName = registerRes.ObjectKey
	}

	return objectName, info.ETag, true, nil
}

func (s *Service) enrichPreparedResponse(ctx context.Context, resp gin.H, objectName string, prepared *preparedAssetUpload, cacheControl string) {
	resp["cacheControl"] = cacheControl
	if prepared.blurhash != "" {
		resp["blurhash"] = prepared.blurhash
	}
	if prepared.width > 0 {
		resp["width"] = prepared.width
	}
	if prepared.height > 0 {
		resp["height"] = prepared.height
	}
	if urlStr, err := s.presignedOrCDNURL(ctx, objectName, 10*time.Minute); err == nil {
		resp["url"] = urlStr
	}
}

// enrichResponse adds computed image metadata, cacheControl, and a direct URL
// to an upload/update JSON response so the client gets everything in one round-trip.
func (s *Service) enrichResponse(ctx context.Context, resp gin.H, objectName string, opts minio.PutObjectOptions) {
	resp["cacheControl"] = opts.CacheControl
	if opts.UserMetadata != nil {
		resp["blurhash"] = opts.UserMetadata["x-blurhash"]
		if w, err := strconv.Atoi(opts.UserMetadata["x-width"]); err == nil {
			resp["width"] = w
		}
		if h, err := strconv.Atoi(opts.UserMetadata["x-height"]); err == nil {
			resp["height"] = h
		}
	}
	if urlStr, err := s.presignedOrCDNURL(ctx, objectName, 10*time.Minute); err == nil {
		resp["url"] = urlStr
	}
}

// presignedOrCDNURL returns a domain-free relative path for the object.
// Returning a relative path (e.g. "/api/v2/storage/posts/abc/img.png") makes
// stored URLs environment-agnostic: the browser resolves against its current
// origin, so the same data works on localhost, staging, and production with
// zero migration on domain change.
//
// Fallback: if MINIO_CDN_URL is explicitly set, it is used as the prefix
// (backward compat / CDN edge-cache scenario). If neither CDN nor public
// signer is configured, presigned URLs are used as a last resort.
func (s *Service) presignedOrCDNURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	if s.cdnURL != "" {
		trimmed := strings.TrimRight(s.cdnURL, "/")
		// If cdnURL is already a relative path (starts with /), return as-is.
		// If it's an absolute URL, extract just the path portion to stay domain-free.
		if strings.HasPrefix(trimmed, "/") {
			return trimmed + "/" + objectName, nil
		}
		// Absolute cdnURL configured — but we deliberately return only the path.
		// e.g. "https://monkeys.support/api/v2/storage" → "/api/v2/storage"
		// This keeps stored URLs domain-free.
		if idx := strings.Index(trimmed, "/api/"); idx >= 0 {
			return trimmed[idx:] + "/" + objectName, nil
		}
		// Fallback: return the relative storage path directly
		return "/api/v2/storage/" + objectName, nil
	}
	// No CDN configured — return relative path by default
	return "/api/v2/storage/" + objectName, nil
}

// profileImageURL builds the extensionless profile route. The raw object key
// carries an extension (profile.png) that has no matching GET route, so we must
// point at /profiles/{user}/profile which resolves the stored key internally.
func (s *Service) profileImageURL(ctx context.Context, userID string, expiry time.Duration) (string, error) {
	return s.presignedOrCDNURL(ctx, "profiles/"+userID+"/profile", expiry)
}

// Handlers (Create/Read/Update/Delete)

// UploadPostFile
// Method: POST /api/v2/storage/posts/:id (auth required)
// Input: multipart form field `file`
// Behavior: stores/reuses a checksum-addressed asset, computes BlurHash/dimensions for images, and creates a blog reference.
// Response: 201 JSON { bucket, object, fileName, etag, size, contentType }
func (s *Service) UploadPostFile(ctx *gin.Context) {
	blogID := ctx.Param("id")
	accessLevel, _ := ctx.Get("user_access_level")
	accountId, _ := ctx.Get("accountId")
	s.log.Debugw("UploadPostFile: permission check",
		"blog_id", blogID,
		"account_id", accountId,
		"access_level", accessLevel,
	)

	if !utils.CheckUserAccessInContext(ctx, constants.PermissionEdit) &&
		!utils.CheckUserAccessInContext(ctx, constants.PermissionCreate) {
		s.log.Warnw("UploadPostFile: access denied",
			"blog_id", blogID,
			"account_id", accountId,
			"access_level", accessLevel,
		)
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "You are not allowed to perform this action"})
		return
	}

	file, fileHeader, err := ctx.Request.FormFile("file")
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "missing file"})
		return
	}
	defer file.Close()

	fname := uniqueName(fileHeader.Filename)
	contentType := fileHeader.Header.Get("Content-Type")
	const cacheControl = "public, max-age=31536000"

	prepared, err := s.prepareAssetUpload(file, fileHeader, contentType)
	if err != nil {
		s.log.Errorf("prepare asset upload error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "read failed"})
		return
	}
	if prepared.cleanup != nil {
		defer prepared.cleanup()
	}

	objectName, etag, _, err := s.ensureAssetStored(ctx.Request.Context(), prepared, fileHeader.Filename, contentType, cacheControl)
	if err != nil {
		s.log.Errorf("asset store/register error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "upload failed"})
		return
	}

	refRes, err := s.storageCli.CreateAssetRef(ctx.Request.Context(), &pb.CreateAssetRefReq{
		Checksum:  prepared.checksum,
		OwnerType: postAssetOwnerType,
		OwnerId:   blogID,
		Purpose:   postAssetPurpose,
		FileName:  fname,
	})
	if err != nil || !refRes.Success {
		msg := ""
		if refRes != nil {
			msg = refRes.Error
		}
		s.log.Errorf("create asset ref error: %v %s", err, msg)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "upload reference failed"})
		return
	}

	// Respond with stored file info + computed metadata so the client
	// receives blurhash, dimensions, cacheControl, and URL in one round-trip.
	resp := gin.H{
		"bucket":      s.bucket,
		"object":      objectName,
		"fileName":    fname,
		"refId":       refRes.RefId,
		"checksum":    prepared.checksum,
		"etag":        etag,
		"size":        prepared.size,
		"contentType": contentType,
	}
	s.enrichPreparedResponse(ctx.Request.Context(), resp, objectName, prepared, cacheControl)
	ctx.JSON(http.StatusCreated, resp)
}

// ListPostFiles
// Method: GET /api/v2/storage/posts/:id (auth required)
// Behavior: lists objects under posts/{id}/
// Response: 200 JSON { files: [{ object, fileName, size, etag, lastModified }] }
func (s *Service) ListPostFiles(ctx *gin.Context) {
	blogID := ctx.Param("id")
	prefix := "posts/" + blogID + "/"

	ch := s.mc.ListObjects(ctx.Request.Context(), s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	files := make([]gin.H, 0, 8)
	for obj := range ch {
		if obj.Err != nil {
			s.log.Errorf("list object error: %v", obj.Err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "list failed"})
			return
		}
		// Skip the folder key itself if present
		if obj.Key == prefix {
			continue
		}
		files = append(files, gin.H{
			"object":       obj.Key,
			"fileName":     path.Base(obj.Key),
			"size":         obj.Size,
			"etag":         obj.ETag,
			"lastModified": obj.LastModified,
		})
	}

	ctx.JSON(http.StatusOK, gin.H{"files": files})
}

// HeadPostFile
// Method: HEAD /api/v2/storage/posts/:id/:fileName (auth required)
// Behavior: returns object metadata in headers: Content-Type, ETag, Last-Modified, Cache-Control
//
//	and if image: X-Blurhash, X-Image-Width, X-Image-Height
func (s *Service) HeadPostFile(ctx *gin.Context) {
	blogID := ctx.Param("id")
	fileName := ctx.Param("fileName")
	objectName := "posts/" + blogID + "/" + fileName

	info, err := s.mc.StatObject(ctx.Request.Context(), s.bucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "file not found"})
		return
	}

	if info.ContentType != "" {
		ctx.Header("Content-Type", info.ContentType)
	}
	if info.ETag != "" {
		ctx.Header("ETag", info.ETag)
	}
	if !info.LastModified.IsZero() {
		ctx.Header("Last-Modified", info.LastModified.UTC().Format(http.TimeFormat))
	}
	if cc := info.Metadata.Get("Cache-Control"); cc != "" {
		ctx.Header("Cache-Control", cc)
	}
	if info.UserMetadata != nil {
		if bh := metaValue(info.UserMetadata, "x-blurhash"); bh != "" {
			ctx.Header("X-Blurhash", bh)
		}
		if w := metaValue(info.UserMetadata, "x-width"); w != "" {
			ctx.Header("X-Image-Width", w)
		}
		if h := metaValue(info.UserMetadata, "x-height"); h != "" {
			ctx.Header("X-Image-Height", h)
		}
	}

	ctx.Status(http.StatusOK)
}

// UpdatePostFile
// Method: PUT /api/v2/storage/posts/:id/:fileName (auth required)
// Input: multipart form field `file`
// Behavior: stores/reuses a checksum-addressed asset, then moves the blog reference to it.
// Response: 200 JSON { bucket, object, etag, size, contentType }
func (s *Service) UpdatePostFile(ctx *gin.Context) {
	blogID := ctx.Param("id")
	accessLevel, _ := ctx.Get("user_access_level")
	accountId, _ := ctx.Get("accountId")
	s.log.Debugw("UpdatePostFile: permission check",
		"blog_id", blogID,
		"account_id", accountId,
		"access_level", accessLevel,
	)

	if !utils.CheckUserAccessInContext(ctx, constants.PermissionEdit) {
		s.log.Warnw("UpdatePostFile: access denied",
			"blog_id", blogID,
			"account_id", accountId,
			"access_level", accessLevel,
		)
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "You are not allowed to perform this action"})
		return
	}

	fileName := ctx.Param("fileName")

	file, fileHeader, err := ctx.Request.FormFile("file")
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "missing file"})
		return
	}
	defer file.Close()

	contentType := fileHeader.Header.Get("Content-Type")
	const cacheControl = "public, max-age=31536000"

	prepared, err := s.prepareAssetUpload(file, fileHeader, contentType)
	if err != nil {
		s.log.Errorf("prepare asset update error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "read failed"})
		return
	}
	if prepared.cleanup != nil {
		defer prepared.cleanup()
	}

	objectName, etag, _, err := s.ensureAssetStored(ctx.Request.Context(), prepared, fileHeader.Filename, contentType, cacheControl)
	if err != nil {
		s.log.Errorf("asset store/register update error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "update failed"})
		return
	}

	refRes, err := s.storageCli.ReplaceAssetRef(ctx.Request.Context(), &pb.ReplaceAssetRefReq{
		Checksum:  prepared.checksum,
		OwnerType: postAssetOwnerType,
		OwnerId:   blogID,
		Purpose:   postAssetPurpose,
		FileName:  fileName,
	})
	if err != nil || !refRes.Success {
		msg := ""
		if refRes != nil {
			msg = refRes.Error
		}
		s.log.Errorf("replace asset ref error: %v %s", err, msg)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "update reference failed"})
		return
	}

	resp := gin.H{
		"bucket":      s.bucket,
		"object":      objectName,
		"fileName":    fileName,
		"refId":       refRes.RefId,
		"checksum":    prepared.checksum,
		"etag":        etag,
		"size":        prepared.size,
		"contentType": contentType,
	}
	s.enrichPreparedResponse(ctx.Request.Context(), resp, objectName, prepared, cacheControl)
	ctx.JSON(http.StatusOK, resp)
}

// GetPostFile
// Method: GET /api/v2/storage/posts/:id/:fileName (public)
// Behavior: streams the object, sets Content-Type/ETag/Last-Modified/Cache-Control
//
//	and if image: X-Blurhash, X-Image-Width, X-Image-Height
func (s *Service) GetPostFile(ctx *gin.Context) {
	blogID := ctx.Param("id")
	fileName := ctx.Param("fileName")
	objectName := "posts/" + blogID + "/" + fileName
	s.streamObject(ctx, objectName, "file not found")
}

func (s *Service) GetAssetFile(ctx *gin.Context) {
	objectName := "assets/sha256/" + ctx.Param("p1") + "/" + ctx.Param("p2") + "/" + ctx.Param("fileName")
	s.streamObject(ctx, objectName, "file not found")
}

func (s *Service) streamObject(ctx *gin.Context, objectName, notFoundMessage string) {
	obj, err := s.mc.GetObject(ctx.Request.Context(), s.bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		s.log.Errorf("minio GetObject error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "read failed"})
		return
	}
	defer obj.Close()

	stat, err := obj.Stat()
	if err != nil {
		// Not found or access error
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": notFoundMessage})
		return
	}

	if stat.ContentType != "" {
		ctx.Header("Content-Type", stat.ContentType)
	}
	if stat.ETag != "" {
		ctx.Header("ETag", stat.ETag)
	}
	if !stat.LastModified.IsZero() {
		ctx.Header("Last-Modified", stat.LastModified.UTC().Format(http.TimeFormat))
	}
	if cc := stat.Metadata.Get("Cache-Control"); cc != "" {
		ctx.Header("Cache-Control", cc)
	}
	if stat.UserMetadata != nil {
		if bh := metaValue(stat.UserMetadata, "x-blurhash"); bh != "" {
			ctx.Header("X-Blurhash", bh)
		}
		if w := metaValue(stat.UserMetadata, "x-width"); w != "" {
			ctx.Header("X-Image-Width", w)
		}
		if h := metaValue(stat.UserMetadata, "x-height"); h != "" {
			ctx.Header("X-Image-Height", h)
		}
	}

	// For PDFs, ensure inline display in iframes
	if strings.Contains(strings.ToLower(stat.ContentType), "application/pdf") {
		ctx.Header("Content-Disposition", "inline")
	}

	// Stream body
	if _, err := io.Copy(ctx.Writer, obj); err != nil {
		s.log.Errorf("stream write error: %v", err)
	}
}

// DeletePostFile
// Method: DELETE /api/v2/storage/posts/:id/:fileName (auth required)
// Behavior: deletes the blog reference; legacy path-based objects are removed as a fallback.
// Response: 200 JSON { message: "deleted", object }
func (s *Service) DeletePostFile(ctx *gin.Context) {
	blogID := ctx.Param("id")
	accessLevel, _ := ctx.Get("user_access_level")
	accountId, _ := ctx.Get("accountId")
	s.log.Debugw("DeletePostFile: permission check",
		"blog_id", blogID,
		"account_id", accountId,
		"access_level", accessLevel,
	)

	if !utils.CheckUserAccessInContext(ctx, constants.PermissionEdit) {
		s.log.Warnw("DeletePostFile: access denied",
			"blog_id", blogID,
			"account_id", accountId,
			"access_level", accessLevel,
		)
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "You are not allowed to perform this action"})
		return
	}

	fileName := ctx.Param("fileName")
	objectName := "posts/" + blogID + "/" + fileName

	deleteRes, err := s.storageCli.DeleteAssetRef(ctx.Request.Context(), &pb.DeleteAssetRefReq{
		OwnerType: postAssetOwnerType,
		OwnerId:   blogID,
		Purpose:   postAssetPurpose,
		FileName:  fileName,
	})
	if err == nil && deleteRes.Success && deleteRes.DeletedCount > 0 {
		ctx.JSON(http.StatusOK, gin.H{"message": "deleted", "fileName": fileName, "deletedRefs": deleteRes.DeletedCount})
		return
	}
	if err != nil {
		s.log.Warnw("delete asset ref failed; trying legacy object delete", "blog_id", blogID, "file_name", fileName, "err", err)
	} else if deleteRes != nil && !deleteRes.Success {
		s.log.Warnw("delete asset ref unsuccessful; trying legacy object delete", "blog_id", blogID, "file_name", fileName, "error", deleteRes.Error)
	}

	// Legacy fallback for pre-CAS objects stored under posts/{blogId}/.
	_, statErr := s.mc.StatObject(ctx.Request.Context(), s.bucket, objectName, minio.StatObjectOptions{})
	if statErr != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "file not found"})
		return
	}

	err = s.mc.RemoveObject(ctx.Request.Context(), s.bucket, objectName, minio.RemoveObjectOptions{})
	if err != nil {
		s.log.Errorf("minio RemoveObject error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "delete failed"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "deleted", "object": objectName})
}

// resolveProfileKey finds the actual MinIO object key for a user's profile image.
// Both v1 and v2 store with a real extension (e.g. "profiles/{user}/profile.png").
// A brief transitional period may have stored extensionless "profiles/{user}/profile".
// This helper tries common extensions first (the canonical form), then falls back
// to the extensionless key. Returns the resolved key and true if found.
func (s *Service) resolveProfileKey(ctx context.Context, userID string) (string, bool) {
	base := "profiles/" + userID + "/profile"
	// Try extension-bearing keys first — canonical for both v1 and v2.
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".avif", ".gif"} {
		key := base + ext
		if _, err := s.mc.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err == nil {
			return key, true
		}
	}
	// Fallback: extensionless key from brief v2 transitional period.
	if _, err := s.mc.StatObject(ctx, s.bucket, base, minio.StatObjectOptions{}); err == nil {
		return base, true
	}
	return "", false
}

// cleanupOtherProfileKeys removes any profile keys that differ from currentKey.
// This covers: old extension variants (e.g. user re-uploads as .webp replacing .png)
// and the extensionless key from the brief v2 transitional period.
// Called after a successful upload/update so stale files don't waste space.
func (s *Service) cleanupOtherProfileKeys(ctx context.Context, userID, currentKey string) {
	base := "profiles/" + userID + "/profile"
	// Remove extensionless key if it's not the current one.
	if base != currentKey {
		if _, err := s.mc.StatObject(ctx, s.bucket, base, minio.StatObjectOptions{}); err == nil {
			if err := s.mc.RemoveObject(ctx, s.bucket, base, minio.RemoveObjectOptions{}); err != nil {
				s.log.Warnf("failed to clean up profile key %s: %v", base, err)
			} else {
				s.log.Infof("cleaned up stale profile key: %s", base)
			}
		}
	}
	// Remove extension variants that differ from the current key.
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".avif", ".gif"} {
		key := base + ext
		if key == currentKey {
			continue
		}
		if _, err := s.mc.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err == nil {
			if err := s.mc.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
				s.log.Warnf("failed to clean up profile key %s: %v", key, err)
			} else {
				s.log.Infof("cleaned up stale profile key: %s", key)
			}
		}
	}
}

// UploadProfileImage
// Method: POST /api/v2/storage/profiles/:user_id/profile (auth required)
// Input: multipart form field `profile_pic`
// Behavior: stores to profiles/{user_id}/profile.{ext} (extension from Content-Type),
//
//	computes BlurHash/dimensions for images, cleans up any prior key variants.
//
// Response: 201 JSON { bucket, object, etag, size, contentType }
func (s *Service) UploadProfileImage(ctx *gin.Context) {
	userID := ctx.Param("user_id")
	loggedInUser := ctx.GetString("userName")

	if userID != loggedInUser {
		ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "cannot upload profile for another user"})
		return
	}

	file, fileHeader, err := ctx.Request.FormFile("profile_pic")
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "missing profile_pic"})
		return
	}
	defer file.Close()

	contentType := fileHeader.Header.Get("Content-Type")
	// Derive extension from uploaded Content-Type so MinIO key is self-documenting.
	// e.g. image/png → profiles/{user}/profile.png
	ext := extFromContentType(contentType)
	objectName := "profiles/" + userID + "/profile" + ext

	// Streaming upload
	const metadataLimit = 5 * 1024 * 1024
	var finalReader io.Reader = file
	var objectSize int64 = fileHeader.Size

	opts := minio.PutObjectOptions{
		ContentType:  contentType,
		CacheControl: "public, max-age=3600, must-revalidate",
	}

	if strings.HasPrefix(strings.ToLower(contentType), "image/") && fileHeader.Size <= metadataLimit {
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
	} else {
		_, _ = file.Seek(0, io.SeekStart)
	}

	info, err := s.mc.PutObject(ctx.Request.Context(), s.bucket, objectName, finalReader, objectSize, opts)
	if err != nil {
		s.log.Errorf("minio PutObject error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "upload failed"})
		return
	}

	// Clean up any prior profile keys that differ from the one just written.
	// Handles: old extension variants (.png → .webp) and extensionless transitional keys.
	s.cleanupOtherProfileKeys(ctx.Request.Context(), userID, objectName)

	resp := gin.H{
		"bucket":      s.bucket,
		"object":      objectName,
		"etag":        info.ETag,
		"size":        info.Size,
		"contentType": contentType,
	}
	s.enrichResponse(ctx.Request.Context(), resp, objectName, opts)
	// Override with the routable extensionless profile route.
	if urlStr, err := s.profileImageURL(ctx.Request.Context(), userID, 10*time.Minute); err == nil {
		resp["url"] = urlStr
	}
	ctx.JSON(http.StatusCreated, resp)
}

// HeadProfileImage
// Method: HEAD /api/v2/storage/profiles/:user_id/profile (auth required)
// Behavior: returns metadata headers; includes X-Blurhash and dimensions if image
func (s *Service) HeadProfileImage(ctx *gin.Context) {
	userID := ctx.Param("user_id")

	objectName, found := s.resolveProfileKey(ctx.Request.Context(), userID)
	if !found {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "profile not found"})
		return
	}

	info, err := s.mc.StatObject(ctx.Request.Context(), s.bucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "profile not found"})
		return
	}

	if info.ContentType != "" {
		ctx.Header("Content-Type", info.ContentType)
	}
	if info.ETag != "" {
		ctx.Header("ETag", info.ETag)
	}
	if !info.LastModified.IsZero() {
		ctx.Header("Last-Modified", info.LastModified.UTC().Format(http.TimeFormat))
	}
	if cc := info.Metadata.Get("Cache-Control"); cc != "" {
		ctx.Header("Cache-Control", cc)
	}
	if info.UserMetadata != nil {
		if bh := metaValue(info.UserMetadata, "x-blurhash"); bh != "" {
			ctx.Header("X-Blurhash", bh)
		}
		if w := metaValue(info.UserMetadata, "x-width"); w != "" {
			ctx.Header("X-Image-Width", w)
		}
		if h := metaValue(info.UserMetadata, "x-height"); h != "" {
			ctx.Header("X-Image-Height", h)
		}
	}

	ctx.Status(http.StatusOK)
}

// UpdateProfileImage
// Method: PUT /api/v2/storage/profiles/:user_id/profile (auth required)
// Input: multipart form field `profile_pic`
// Behavior: replaces profile image and recomputes image metadata
// Response: 200 JSON { bucket, object, etag, size, contentType }
func (s *Service) UpdateProfileImage(ctx *gin.Context) {
	userID := ctx.Param("user_id")
	loggedInUser := ctx.GetString("userName")

	if userID != loggedInUser {
		ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "cannot update profile for another user"})
		return
	}

	file, fileHeader, err := ctx.Request.FormFile("profile_pic")
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "missing profile_pic"})
		return
	}
	defer file.Close()

	contentType := fileHeader.Header.Get("Content-Type")
	ext := extFromContentType(contentType)
	objectName := "profiles/" + userID + "/profile" + ext

	data, err := io.ReadAll(file)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "read failed"})
		return
	}
	reader := bytes.NewReader(data)

	opts := minio.PutObjectOptions{
		ContentType:  contentType,
		CacheControl: "public, max-age=3600, must-revalidate",
	}
	if strings.HasPrefix(strings.ToLower(contentType), "image/") {
		if hash, w, h, ok := s.computeImageMetadata(contentType, data); ok {
			opts.UserMetadata = map[string]string{
				"x-blurhash": hash,
				"x-width":    strconv.Itoa(w),
				"x-height":   strconv.Itoa(h),
			}
		}
	}

	info, err := s.mc.PutObject(ctx.Request.Context(), s.bucket, objectName, reader, int64(len(data)), opts)
	if err != nil {
		s.log.Errorf("minio PutObject (update) error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "update failed"})
		return
	}

	// Clean up any prior profile keys that differ from the one just written.
	s.cleanupOtherProfileKeys(ctx.Request.Context(), userID, objectName)

	resp := gin.H{
		"bucket":      s.bucket,
		"object":      objectName,
		"etag":        info.ETag,
		"size":        info.Size,
		"contentType": contentType,
	}
	s.enrichResponse(ctx.Request.Context(), resp, objectName, opts)
	// Override with the routable extensionless profile route.
	if urlStr, err := s.profileImageURL(ctx.Request.Context(), userID, 10*time.Minute); err == nil {
		resp["url"] = urlStr
	}
	ctx.JSON(http.StatusOK, resp)
}

// GetPostFileMeta
// Method: GET /api/v2/storage/posts/:id/:fileName/meta (public)
// Behavior: returns JSON metadata for the object including BlurHash, dimensions, and a direct URL (CDN or presigned)
// Response: 200 JSON FileMetaResponse
func (s *Service) GetPostFileMeta(ctx *gin.Context) {
	blogID := ctx.Param("id")
	fileName := ctx.Param("fileName")
	objectName := "posts/" + blogID + "/" + fileName

	info, err := s.mc.StatObject(ctx.Request.Context(), s.bucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "file not found"})
		return
	}

	bh := ""
	w := 0
	h := 0
	if info.UserMetadata != nil {
		bh = metaValue(info.UserMetadata, "x-blurhash")
		if ws := metaValue(info.UserMetadata, "x-width"); ws != "" {
			if vi, err := strconv.Atoi(ws); err == nil {
				w = vi
			}
		}
		if hs := metaValue(info.UserMetadata, "x-height"); hs != "" {
			if vi, err := strconv.Atoi(hs); err == nil {
				h = vi
			}
		}
	}

	urlStr, _ := s.presignedOrCDNURL(ctx.Request.Context(), objectName, 10*time.Minute)

	ctx.JSON(http.StatusOK, gin.H{
		"object":       objectName,
		"etag":         info.ETag,
		"size":         info.Size,
		"contentType":  info.ContentType,
		"lastModified": info.LastModified,
		"cacheControl": info.Metadata.Get("Cache-Control"),
		"blurhash":     bh,
		"width":        w,
		"height":       h,
		"url":          urlStr,
	})
}

// GetProfileMeta
// Method: GET /api/v2/storage/profiles/:user_id/profile/meta (public)
// Behavior: JSON metadata for profile image including BlurHash, dimensions, and direct URL
func (s *Service) GetProfileMeta(ctx *gin.Context) {
	userID := ctx.Param("user_id")

	objectName, found := s.resolveProfileKey(ctx.Request.Context(), userID)
	if !found {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "profile not found"})
		return
	}

	info, err := s.mc.StatObject(ctx.Request.Context(), s.bucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "profile not found"})
		return
	}

	bh := ""
	w := 0
	h := 0
	if info.UserMetadata != nil {
		bh = metaValue(info.UserMetadata, "x-blurhash")
		if ws := metaValue(info.UserMetadata, "x-width"); ws != "" {
			if vi, err := strconv.Atoi(ws); err == nil {
				w = vi
			}
		}
		if hs := metaValue(info.UserMetadata, "x-height"); hs != "" {
			if vi, err := strconv.Atoi(hs); err == nil {
				h = vi
			}
		}
	}

	urlStr, _ := s.profileImageURL(ctx.Request.Context(), userID, 10*time.Minute)

	ctx.JSON(http.StatusOK, gin.H{
		"object":       objectName,
		"etag":         info.ETag,
		"size":         info.Size,
		"contentType":  info.ContentType,
		"lastModified": info.LastModified,
		"cacheControl": info.Metadata.Get("Cache-Control"),
		"blurhash":     bh,
		"width":        w,
		"height":       h,
		"url":          urlStr,
	})
}

// GetPostFileURL
// Method: GET /api/v2/storage/posts/:id/:fileName/url (public)
// Query: expires (seconds, default 600, max 604800)
// Behavior: returns a direct URL to the object (CDN if configured, otherwise presigned S3 URL)
func (s *Service) GetPostFileURL(ctx *gin.Context) {
	blogID := ctx.Param("id")
	fileName := ctx.Param("fileName")
	objectName := "posts/" + blogID + "/" + fileName

	expires := 600
	if qs := ctx.Query("expires"); qs != "" {
		if vi, err := strconv.Atoi(qs); err == nil {
			if vi > 0 {
				// cap at 7 days
				if vi > int((7*24*time.Hour)/time.Second) {
					vi = int((7 * 24 * time.Hour) / time.Second)
				}
				expires = vi
			}
		}
	}

	// Check existence first
	if _, err := s.mc.StatObject(ctx.Request.Context(), s.bucket, objectName, minio.StatObjectOptions{}); err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "file not found"})
		return
	}

	urlStr, err := s.presignedOrCDNURL(ctx.Request.Context(), objectName, time.Duration(expires)*time.Second)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not generate url"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"url": urlStr, "expiresIn": expires})
}

// GetProfileURL
// Method: GET /api/v2/storage/profiles/:user_id/profile/url (public)
// Query: expires (seconds)
// Behavior: returns CDN/presigned URL for profile image
func (s *Service) GetProfileURL(ctx *gin.Context) {
	userID := ctx.Param("user_id")

	if _, found := s.resolveProfileKey(ctx.Request.Context(), userID); !found {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "profile not found"})
		return
	}

	expires := 600
	if qs := ctx.Query("expires"); qs != "" {
		if vi, err := strconv.Atoi(qs); err == nil {
			if vi > 0 {
				if vi > int((7*24*time.Hour)/time.Second) {
					vi = int((7 * 24 * time.Hour) / time.Second)
				}
				expires = vi
			}
		}
	}

	urlStr, err := s.profileImageURL(ctx.Request.Context(), userID, time.Duration(expires)*time.Second)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not generate url"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"url": urlStr, "expiresIn": expires})
}

func (s *Service) GetProfileImage(ctx *gin.Context) {
	// Streams the user's profile image (public)
	userID := ctx.Param("user_id")

	objectName, found := s.resolveProfileKey(ctx.Request.Context(), userID)
	if !found {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "profile not found"})
		return
	}

	obj, err := s.mc.GetObject(ctx.Request.Context(), s.bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		s.log.Errorf("minio GetObject (profile) error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "read failed"})
		return
	}
	defer obj.Close()

	stat, err := obj.Stat()
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "profile not found"})
		return
	}

	if stat.ContentType != "" {
		ctx.Header("Content-Type", stat.ContentType)
	}
	if stat.ETag != "" {
		ctx.Header("ETag", stat.ETag)
	}
	if !stat.LastModified.IsZero() {
		ctx.Header("Last-Modified", stat.LastModified.UTC().Format(http.TimeFormat))
	}
	if cc := stat.Metadata.Get("Cache-Control"); cc != "" {
		ctx.Header("Cache-Control", cc)
	}
	if stat.UserMetadata != nil {
		if bh := metaValue(stat.UserMetadata, "x-blurhash"); bh != "" {
			ctx.Header("X-Blurhash", bh)
		}
		if w := metaValue(stat.UserMetadata, "x-width"); w != "" {
			ctx.Header("X-Image-Width", w)
		}
		if h := metaValue(stat.UserMetadata, "x-height"); h != "" {
			ctx.Header("X-Image-Height", h)
		}
	}

	if _, err := io.Copy(ctx.Writer, obj); err != nil {
		s.log.Errorf("stream write error (profile): %v", err)
	}
}

func (s *Service) DeleteProfileImage(ctx *gin.Context) {
	// Deletes the user's profile image (auth required)
	userID := ctx.Param("user_id")
	loggedInUser := ctx.GetString("userName")

	if userID != loggedInUser {
		ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "cannot delete profile for another user"})
		return
	}

	// Resolve actual key (extensionless v2 or legacy .png/.jpg from v1)
	objectName, found := s.resolveProfileKey(ctx.Request.Context(), userID)
	if !found {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "profile not found"})
		return
	}

	if err := s.mc.RemoveObject(ctx.Request.Context(), s.bucket, objectName, minio.RemoveObjectOptions{}); err != nil {
		s.log.Errorf("minio RemoveObject (profile) error: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "delete failed"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "deleted", "object": objectName})
}
