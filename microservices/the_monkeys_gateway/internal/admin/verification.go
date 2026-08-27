package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListVerifications handles GET /api/v1/admin/verifications
// Query params: status (pending|under_review|approved|rejected), limit, offset.
// Mounted behind LocalNetworkMiddleware + AdminKeyMiddleware at group level,
// so no JWT role check is needed here.
func (asc *AdminServiceClient) ListVerifications(ctx *gin.Context) {
	limit, err := strconv.Atoi(ctx.DefaultQuery("limit", "20"))
	if err != nil || limit < 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "limit must be a non-negative integer"})
		return
	}
	offset, err := strconv.Atoi(ctx.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "offset must be a non-negative integer"})
		return
	}

	res, gErr := asc.Client.ListVerificationRequests(context.Background(), &pb.ListVerificationReq{
		Status: strings.TrimSpace(ctx.Query("status")),
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if gErr != nil {
		if status.Code(gErr) == codes.InvalidArgument {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": status.Convert(gErr).Message()})
			return
		}
		asc.logger.Errorf("list verification requests failed: %v", gErr)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not list verification requests"})
		return
	}
	ctx.JSON(http.StatusOK, res)
}

type reviewVerificationBody struct {
	Approve         bool   `json:"approve"`
	RejectionReason string `json:"rejection_reason"`
}

// ReviewVerification handles POST /api/v1/admin/verifications/:id/review
// Body: {"approve": true} or {"approve": false, "rejection_reason": "…"}.
// Approval flips the account's verified badge atomically server-side.
func (asc *AdminServiceClient) ReviewVerification(ctx *gin.Context) {
	requestID := strings.TrimSpace(ctx.Param("id"))
	if requestID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "request id is required"})
		return
	}

	var body reviewVerificationBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}
	if !body.Approve && strings.TrimSpace(body.RejectionReason) == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "rejection_reason is required when rejecting"})
		return
	}

	res, err := asc.Client.ReviewVerificationRequest(context.Background(), &pb.ReviewVerificationReq{
		RequestId:        requestID,
		ReviewerUsername: "admin",
		Approve:          body.Approve,
		RejectionReason:  body.RejectionReason,
	})
	if err != nil {
		switch status.Code(err) {
		case codes.NotFound:
			ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "request not found or not in a reviewable state"})
		case codes.InvalidArgument:
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": status.Convert(err).Message()})
		default:
			asc.logger.Errorf("review verification %s failed: %v", requestID, err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not record the review decision"})
		}
		return
	}
	ctx.JSON(http.StatusOK, res)
}
