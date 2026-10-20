package storage_v2

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	userpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/admin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetVerificationAssetContent serves only authenticated image bytes. It never
// redirects to the public storage host, signs browser URLs, or mutates MinIO.
// Existing /url clients retain their original contract.
func (vh *verificationHandler) GetVerificationAssetContent(ctx *gin.Context) {
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Header("Vary", "Cookie, Authorization")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Cross-Origin-Resource-Policy", "same-origin")
	ctx.Header("Referrer-Policy", "no-referrer")
	ctx.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	ctx.Header("X-Request-ID", uuid.NewString())

	readContext, cancel := context.WithTimeout(ctx.Request.Context(), 20*time.Second)
	defer cancel()
	// The gateway's general write timeout is deliberately long for other APIs.
	// Bound this private stream separately, including a client that stops reading.
	writer := http.NewResponseController(ctx.Writer)
	if err := writer.SetWriteDeadline(time.Now().Add(20 * time.Second)); err != nil {
		vh.contentFailure(ctx, http.StatusServiceUnavailable, "response_deadline_unavailable")
		return
	}
	deadlineDone := make(chan struct{})
	stopDeadline := context.AfterFunc(readContext, func() {
		defer close(deadlineDone)
		_ = writer.SetWriteDeadline(time.Now())
	})
	defer func() {
		// Flush success, JSON failures, and interrupted partial responses while
		// the deadline/cancellation callback can still interrupt a stalled write.
		// Do not commit an empty 200 response during panic recovery.
		if ctx.Writer.Written() {
			if err := writer.Flush(); err != nil {
				vh.log.Warnw("verification document stream interrupted", "reason", "stream_interrupted", "request_id", ctx.Writer.Header().Get("X-Request-ID"))
			}
		}
		// A cancellation callback must finish before Gin can reuse its writer.
		if !stopDeadline() {
			<-deadlineDone
		}
		_ = writer.SetWriteDeadline(time.Time{})
	}()
	checksum, ok := vh.contentChecksum(ctx, readContext)
	if !ok {
		return
	}

	// Unlike upload's ensureBucket, reads must not create a bucket or change
	// policy. Retain the existing requirement that ID documents have no bucket
	// policy, but check it with a read-only operation and fail closed.
	policy, err := vh.mc.GetBucketPolicy(readContext, vh.bucket)
	if err != nil || strings.TrimSpace(policy) != "" {
		vh.contentFailure(ctx, http.StatusServiceUnavailable, "private_bucket_unavailable")
		return
	}
	object, err := vh.mc.GetObject(readContext, vh.bucket, verificationObjectKey(checksum), minio.GetObjectOptions{})
	if err != nil {
		vh.contentStorageFailure(ctx, err)
		return
	}
	defer object.Close()
	info, err := object.Stat()
	if err != nil {
		vh.contentStorageFailure(ctx, err)
		return
	}
	if info.Size > constants.VerificationAssetMaxBytes {
		vh.contentFailure(ctx, http.StatusRequestEntityTooLarge, "document_too_large")
		return
	}
	if info.Size <= 0 {
		vh.contentFailure(ctx, http.StatusUnsupportedMediaType, "unsupported_document")
		return
	}

	// Treat stored Content-Type as untrusted. Only the raster types accepted
	// by the verification uploader may be rendered on the staff origin.
	prefix := make([]byte, min(info.Size, 512))
	if _, err := io.ReadFull(object, prefix); err != nil {
		vh.contentStorageFailure(ctx, err)
		return
	}
	contentType := sniffImageType(prefix)
	if contentType == "" {
		vh.contentFailure(ctx, http.StatusUnsupportedMediaType, "unsupported_document")
		return
	}
	ctx.Header("Content-Type", contentType)
	ctx.Header("Content-Length", strconv.FormatInt(info.Size, 10))
	ctx.Header("Content-Disposition", `inline; filename="verification-image"`)
	ctx.Status(http.StatusOK)
	// Bound transfer size without buffering a complete private document.
	if _, err := io.CopyN(ctx.Writer, io.MultiReader(bytes.NewReader(prefix), object), info.Size); err != nil {
		vh.log.Warnw("verification document stream interrupted", "reason", "stream_interrupted", "request_id", ctx.Writer.Header().Get("X-Request-ID"))
		return
	}
}

// contentChecksum mirrors the existing /url owner/staff and private-network
// policy. The object key is derived only from the authoritative request, never
// from a client-supplied URL, bucket or checksum.
func (vh *verificationHandler) contentChecksum(ctx *gin.Context, readContext context.Context) (string, bool) {
	caller := ctx.GetString("userName")
	if caller == "" {
		vh.contentFailure(ctx, http.StatusUnauthorized, "unauthorized")
		return "", false
	}
	requestID := strings.TrimSpace(ctx.Param("request_id"))
	if _, err := uuid.Parse(requestID); err != nil {
		vh.contentFailure(ctx, http.StatusBadRequest, "invalid_request_id")
		return "", false
	}
	kind := ctx.Param("kind")
	if kind != constants.VerificationKindSelfie && kind != constants.VerificationKindIDFront && kind != constants.VerificationKindIDBack {
		vh.contentFailure(ctx, http.StatusBadRequest, "invalid_document_kind")
		return "", false
	}
	req, err := vh.userCli.GetMyVerification(readContext, &userpb.GetVerificationReq{RequestId: requestID})
	if err != nil || req == nil {
		if status.Code(err) == codes.NotFound {
			vh.contentFailure(ctx, http.StatusNotFound, "request_not_found")
		} else {
			vh.contentFailure(ctx, http.StatusServiceUnavailable, "request_unavailable")
		}
		return "", false
	}
	if req.Username != caller {
		if !staffaccess.Has(ctx.GetString("user_role"), staffaccess.VerificationDocuments) {
			vh.contentFailure(ctx, http.StatusForbidden, "permission_denied")
			return "", false
		}
		if !admin.RequestIsLocal(ctx) {
			vh.contentFailure(ctx, http.StatusForbidden, "private_network_required")
			return "", false
		}
	}
	var checksum string
	switch kind {
	case constants.VerificationKindSelfie:
		checksum = req.SelfieChecksum
	case constants.VerificationKindIDFront:
		checksum = req.IdFrontChecksum
	case constants.VerificationKindIDBack:
		checksum = req.IdBackChecksum
	}
	if checksum == "" {
		vh.contentFailure(ctx, http.StatusNotFound, "document_not_attached")
		return "", false
	}
	if decoded, err := hex.DecodeString(checksum); err != nil || len(decoded) != 32 {
		vh.contentFailure(ctx, http.StatusServiceUnavailable, "invalid_document_reference")
		return "", false
	}
	return checksum, true
}

func (vh *verificationHandler) contentStorageFailure(ctx *gin.Context, err error) {
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		vh.contentFailure(ctx, http.StatusNotFound, "document_not_found")
		return
	}
	// A storage AccessDenied is a backend storage problem, not a staff-session
	// denial. Do not send 403 and cause the admin UI to discard a valid session.
	vh.contentFailure(ctx, http.StatusServiceUnavailable, "document_storage_unavailable")
}

func (vh *verificationHandler) contentFailure(ctx *gin.Context, statusCode int, reason string) {
	id := ctx.Writer.Header().Get("X-Request-ID")
	vh.log.Warnw("verification document unavailable", "reason", reason, "status", statusCode, "request_id", id)
	ctx.AbortWithStatusJSON(statusCode, gin.H{"message": "Verification document could not be loaded.", "reason": reason, "request_id": id})
}
