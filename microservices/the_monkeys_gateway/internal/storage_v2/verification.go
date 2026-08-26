package storage_v2

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	fpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"
	userpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// DefaultVerificationBucket is used when MINIO_VERIFICATION_BUCKET is unset.
const DefaultVerificationBucket = "the-monkeys-verification"

// verificationHandler bundles the shared Service with the verification-only
// collaborators (users gRPC client + private bucket name) so Service itself
// stays untouched and non-verification paths are unaffected.
type verificationHandler struct {
	*Service
	userCli   userpb.UserServiceClient
	bucket    string
	ensure    sync.Once
	ensureErr error
}

// newVerificationHandler dials the users service (same recipe as
// user_service.NewUserServiceClient) and resolves the private bucket name.
func newVerificationHandler(svc *Service, cfg *config.Config, log interface{ Warnf(string, ...interface{}) }) *verificationHandler {
	addr := fmt.Sprintf("%s:%d", cfg.Microservices.TheMonkeysUser, cfg.Microservices.UserPort)
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Warnf("dial user gRPC failed for verification assets: %v", err)
	}

	bucket := strings.TrimSpace(cfg.Minio.VerificationBucket)
	if bucket == "" {
		bucket = DefaultVerificationBucket
	}
	return &verificationHandler{
		Service: svc,
		userCli: userpb.NewUserServiceClient(cc),
		bucket:  bucket,
	}
}

// ensureBucket lazily creates the PRIVATE verification bucket. MinIO buckets
// are private by default — no anonymous policy is ever applied here, and we
// deliberately never attach a public/CDN read policy like the main bucket.
func (vh *verificationHandler) ensureBucket(ctx context.Context) error {
	vh.ensure.Do(func() {
		exists, err := vh.mc.BucketExists(ctx, vh.bucket)
		if err != nil {
			vh.ensureErr = err
			return
		}
		if !exists {
			if err := vh.mc.MakeBucket(ctx, vh.bucket, minio.MakeBucketOptions{}); err != nil {
				vh.ensureErr = err
				return
			}
			vh.log.Infof("created private verification bucket: %s", vh.bucket)
		}

		// Startup assert: the bucket must carry NO policy whatsoever. Any
		// policy statement (including accidental anon-read grants from ops
		// tooling) disqualifies the bucket from holding identity documents.
		pol, pErr := vh.mc.GetBucketPolicy(ctx, vh.bucket)
		switch {
		case pErr != nil:
			vh.ensureErr = fmt.Errorf("verification bucket %s: read policy failed: %w", vh.bucket, pErr)
		case strings.TrimSpace(pol) != "":
			vh.ensureErr = fmt.Errorf(
				"verification bucket %s has a bucket policy attached (%d bytes); refusing to store identity documents - clear the policy or set MINIO_VERIFICATION_BUCKET to a clean bucket",
				vh.bucket, len(pol))
		default:
			vh.log.Debugf("verification bucket %s verified private (no policy)", vh.bucket)
		}
	})
	return vh.ensureErr
}

// verificationObjectKey derives the CAS object key deterministically from the
// checksum alone — extensionless on purpose, so presigned URLs for reviewers
// never need a DB lookup to reconstruct the key. Content type rides as object
// metadata and is served back automatically on download.
func verificationObjectKey(checksum string) string {
	return "verifications/sha256/" + checksum
}

// allowedDocContentType gates uploads to formats Go can actually decode for
// blurhash/dimension extraction. HEIC/HEIF (default iPhone camera output) is
// rejected up front with an actionable message instead of failing silently
// downstream.
func docContentType(raw string) (string, bool) {
	ct := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	switch ct {
	case "image/jpeg", "image/jpg":
		return "image/jpeg", true
	case "image/png":
		return "image/png", true
	case "image/webp":
		return "image/webp", true
	default:
		return ct, false
	}
}

// sniffImageType identifies an image by MAGIC BYTES, not by client-supplied
// headers (which are trivially forged). Returns "" for anything that is not
// a JPEG, PNG or WebP - including SVG (script injection vector) and HEIC.
func sniffImageType(b []byte) string {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

// UploadVerificationAsset handles POST /api/v2/storage/verifications?kind=…
// Multipart field `file`, kind ∈ {selfie,id_front,id_back}. Owner-scoped:
// you can only ever upload as yourself. Returns the checksum the client must
// echo back when submitting the verification request.
func (vh *verificationHandler) UploadVerificationAsset(ctx *gin.Context) {
	loggedInUser := ctx.GetString("userName")
	if loggedInUser == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}
	if err := vh.ensureBucket(ctx.Request.Context()); err != nil {
		vh.log.Errorf("verification bucket unavailable: %v", err)
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"message": "document storage unavailable"})
		return
	}

	kind := strings.TrimSpace(ctx.Query("kind"))
	if kind != constants.VerificationKindSelfie &&
		kind != constants.VerificationKindIDFront &&
		kind != constants.VerificationKindIDBack {
		ctx.AbortWithStatusJSON(http.StatusBadRequest,
			gin.H{"message": "kind must be selfie, id_front or id_back"})
		return
	}

	file, header, err := ctx.Request.FormFile("file")
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "missing multipart file field 'file'"})
		return
	}
	defer file.Close()

	if header.Size > constants.VerificationAssetMaxBytes {
		ctx.AbortWithStatusJSON(http.StatusRequestEntityTooLarge,
			gin.H{"message": fmt.Sprintf("file exceeds %d bytes", constants.VerificationAssetMaxBytes)})
		return
	}

	rawCT := header.Header.Get("Content-Type")
	contentType, ok := docContentType(rawCT)
	if !ok {
		ctx.AbortWithStatusJSON(http.StatusUnsupportedMediaType,
			gin.H{"message": fmt.Sprintf(
				"unsupported document format %q; upload JPEG, PNG or WebP (convert iPhone HEIC before uploading)", rawCT)})
		return
	}

	// Defense in depth: verify the DECLARED type against the actual magic
	// bytes so a renamed payload cannot masquerade as a document image.
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	if n > 0 {
		_, _ = file.Seek(0, io.SeekStart)
	}
	if got := sniffImageType(head[:n]); got != contentType {
		ctx.AbortWithStatusJSON(http.StatusUnsupportedMediaType,
			gin.H{"message": "file contents do not match its declared image type"})
		return
	}

	prepared, err := vh.prepareAssetUpload(file, header, contentType)
	if prepared != nil && prepared.cleanup != nil {
		defer prepared.cleanup()
	}
	if err != nil {
		vh.log.Errorf("prepare verification upload for %s: %v", loggedInUser, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not process the upload"})
		return
	}

	objectKey := verificationObjectKey(prepared.checksum)
	info, err := vh.mc.PutObject(ctx.Request.Context(), vh.bucket, objectKey, prepared.reader, prepared.size,
		minio.PutObjectOptions{
			ContentType:  contentType,
			CacheControl: "private, no-store",
			UserMetadata: vh.metadataFromPrepared(prepared),
		})
	if err != nil {
		vh.log.Errorf("put verification object for %s: %v", loggedInUser, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "upload failed"})
		return
	}

	// Best-effort bookkeeping: the global CAS row makes the checksum visible
	// to the users service existence probe; the ref records who uploaded what.
	if _, rErr := vh.storageCli.RegisterAsset(ctx.Request.Context(), &fpb.RegisterAssetReq{
		Checksum:    prepared.checksum,
		ObjectKey:   objectKey,
		ContentType: contentType,
		Size:        prepared.size,
		Width:       prepared.width,
		Height:      prepared.height,
		Blurhash:    prepared.blurhash,
	}); rErr != nil {
		vh.log.Warnw("register verification asset failed (upload kept)",
			"checksum", prepared.checksum, "err", rErr)
	}
	if _, rErr := vh.storageCli.CreateAssetRef(ctx.Request.Context(), &fpb.CreateAssetRefReq{
		Checksum:  prepared.checksum,
		OwnerType: "verification",
		OwnerId:   loggedInUser,
		Purpose:   kind,
		FileName:  header.Filename,
	}); rErr != nil {
		vh.log.Warnw("create verification asset ref failed", "checksum", prepared.checksum, "err", rErr)
	}

	resp := gin.H{
		"kind":        kind,
		"checksum":    prepared.checksum,
		"bucket":      vh.bucket,
		"object":      objectKey,
		"etag":        info.ETag,
		"size":        info.Size,
		"contentType": contentType,
	}
	if prepared.blurhash != "" {
		resp["blurhash"] = prepared.blurhash
	}
	if prepared.width > 0 {
		resp["width"] = prepared.width
		resp["height"] = prepared.height
	}
	ctx.JSON(http.StatusCreated, resp)
}

// GetVerificationAssetURL handles GET /api/v2/storage/verifications/:request_id/:kind/url
// Returns a short-TTL presigned URL into the PRIVATE bucket. Access is
// restricted to the request owner or an admin (JWT role), keeping documents
// unreachable to everyone else — including other logged-in users.
func (vh *verificationHandler) GetVerificationAssetURL(ctx *gin.Context) {
	caller := ctx.GetString("userName")
	isAdmin := ctx.GetString("user_role") == constants.RoleAdmin
	if caller == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}

	requestID := strings.TrimSpace(ctx.Param("request_id"))
	kind := strings.TrimSpace(ctx.Param("kind"))
	if requestID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "request_id is required"})
		return
	}

	req, err := vh.userCli.GetMyVerification(ctx.Request.Context(), &userpb.GetVerificationReq{
		RequestId: requestID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "verification request not found"})
			return
		}
		vh.log.Errorf("fetch verification request %s: %v", requestID, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not resolve the request"})
		return
	}

	// Scope check: owner or platform admin only.
	if !isAdmin && req.Username != caller {
		ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "not allowed"})
		return
	}

	var checksum string
	switch kind {
	case constants.VerificationKindSelfie:
		checksum = req.SelfieChecksum
	case constants.VerificationKindIDFront:
		checksum = req.IdFrontChecksum
	case constants.VerificationKindIDBack:
		checksum = req.IdBackChecksum
	default:
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "kind must be selfie, id_front or id_back"})
		return
	}
	if strings.TrimSpace(checksum) == "" {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no such document on this request"})
		return
	}

	if err := vh.ensureBucket(ctx.Request.Context()); err != nil {
		vh.log.Errorf("verification bucket unavailable: %v", err)
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"message": "document storage unavailable"})
		return
	}

	signer := vh.mc
	if vh.publicSigner != nil {
		signer = vh.publicSigner
	}
	presigned, err := signer.PresignedGetObject(ctx.Request.Context(), vh.bucket,
		verificationObjectKey(checksum), constants.VerificationPresignTTL, nil)
	if err != nil {
		vh.log.Errorf("presign verification asset %s: %v", checksum, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not sign the document URL"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"url":                presigned.String(),
		"expires_in_seconds": int(constants.VerificationPresignTTL.Seconds()),
	})
}
