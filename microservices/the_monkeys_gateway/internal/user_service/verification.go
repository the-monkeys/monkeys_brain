package user_service

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// submitVerificationBody mirrors SubmitVerificationReq minus identity: the
// gateway injects Username from the JWT so clients cannot verify as someone
// else.
type submitVerificationBody struct {
	VerificationType string `json:"verification_type"`
	Country          string `json:"country"`
	IDDocumentType   string `json:"id_document_type"`
	SelfieChecksum   string `json:"selfie_checksum"`
	IDFrontChecksum  string `json:"id_front_checksum"`
	IDBackChecksum   string `json:"id_back_checksum"`
	AdditionalInfo   string `json:"additional_info"`
}

// SubmitVerification handles POST /api/v1/user/verification (auth required).
// Documents must be uploaded FIRST via POST /api/v2/storage/verifications;
// this call references their checksums and flips the request to pending.
func (usc *UserServiceClient) SubmitVerification(ctx *gin.Context) {
	username := ctx.GetString("userName")
	if username == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}

	var body submitVerificationBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "invalid request: " + err.Error()})
		return
	}

	res, err := usc.Client.SubmitVerificationRequest(context.Background(), &pb.SubmitVerificationReq{
		Username:         username,
		VerificationType: strings.TrimSpace(body.VerificationType),
		Country:          strings.TrimSpace(body.Country),
		IdDocumentType:   strings.TrimSpace(body.IDDocumentType),
		SelfieChecksum:   strings.TrimSpace(body.SelfieChecksum),
		IdFrontChecksum:  strings.TrimSpace(body.IDFrontChecksum),
		IdBackChecksum:   strings.TrimSpace(body.IDBackChecksum),
		AdditionalInfo:   body.AdditionalInfo,
	})
	if err != nil {
		switch status.Code(err) {
		case codes.AlreadyExists:
			ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"message": "you already have an active verification request"})
		case codes.InvalidArgument:
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": grpcMessage(err)})
		default:
			usc.log.Errorf("submit verification for %s failed: %v", username, err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not submit the verification request"})
		}
		return
	}
	ctx.JSON(http.StatusCreated, res)
}

// GetMyVerification handles GET /api/v1/user/verification/me — the caller's
// latest submission and its lifecycle state.
func (usc *UserServiceClient) GetMyVerification(ctx *gin.Context) {
	username := ctx.GetString("userName")
	if username == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}

	res, err := usc.Client.GetMyVerification(context.Background(), &pb.GetVerificationReq{
		Username: username,
		Latest:   true,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.JSON(http.StatusOK, gin.H{"status": "none", "message": "no verification request submitted yet"})
			return
		}
		usc.log.Errorf("get verification for %s failed: %v", username, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not fetch the verification request"})
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// CancelVerification handles DELETE /api/v1/user/verification/:request_id —
// withdraw a still-pending submission (ownership enforced server-side).
func (usc *UserServiceClient) CancelVerification(ctx *gin.Context) {
	username := ctx.GetString("userName")
	requestID := strings.TrimSpace(ctx.Param("request_id"))
	if username == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}
	if requestID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "request_id is required"})
		return
	}

	res, err := usc.Client.CancelVerificationRequest(context.Background(), &pb.GetVerificationReq{
		Username:  username,
		RequestId: requestID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "no pending verification request found"})
			return
		}
		usc.log.Errorf("cancel verification %s for %s failed: %v", requestID, username, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "could not cancel the verification request"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": res.Message})
}

// grpcMessage extracts the server-provided detail from a gRPC error so
// validation messages (e.g. country/doc-type rules) reach the client intact.
func grpcMessage(err error) string {
	if s, ok := status.FromError(err); ok {
		return s.Message()
	}
	return "invalid request"
}
