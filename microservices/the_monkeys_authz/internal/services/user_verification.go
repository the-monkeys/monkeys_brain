package services

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/models"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_authz/internal/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RequestUserVerification allows a user to submit proof for account verification (checkmark).
func (as *AuthzSvc) RequestUserVerification(ctx context.Context, req *pb.RequestUserVerificationReq) (*pb.RequestUserVerificationRes, error) {
	as.logger.Debugf("user verification request from: %s", req.Username)

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		return nil, status.Errorf(codes.InvalidArgument, "username is required")
	}

	// Verify user exists
	user, err := as.dbConn.CheckIfUsernameExist(req.Username)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "user not found")
	}

	// Check if user is already verified
	isVerified, err := as.dbConn.IsUserVerified(req.Username)
	if err != nil {
		as.logger.Errorf("failed to check verification status for %s: %v", req.Username, err)
		return nil, status.Errorf(codes.Internal, "failed to check verification status")
	}
	if isVerified {
		return nil, status.Errorf(codes.AlreadyExists, "user is already verified")
	}

	// Check if there's already a pending request
	existing, err := as.dbConn.GetVerificationRequest(req.Username)
	if err == nil && existing.Status == "pending" {
		return nil, status.Errorf(codes.AlreadyExists, "you already have a pending verification request")
	}

	// Check email is verified (prerequisite for account verification)
	if user.EmailVerificationStatus != "Verified" {
		return nil, status.Errorf(codes.FailedPrecondition, "email must be verified before requesting account verification")
	}

	// Validate verification type
	validTypes := map[string]bool{"social_proof": true, "id_document": true, "professional": true}
	if !validTypes[req.VerificationType] {
		return nil, status.Errorf(codes.InvalidArgument, "invalid verification type, must be: social_proof, id_document, or professional")
	}

	// Validate proof URLs
	if len(req.ProofUrls) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "at least one proof URL is required")
	}

	// Serialize proof URLs to JSON string for DB storage
	proofURLsJSON := strings.Join(req.ProofUrls, ",")

	verReq := &models.VerificationRequest{
		ID:               utils.GenerateGUID(),
		Username:         req.Username,
		VerificationType: req.VerificationType,
		ProofURLs:        proofURLsJSON,
		AdditionalInfo:   req.AdditionalInfo,
		Status:           "pending",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := as.dbConn.CreateVerificationRequest(verReq); err != nil {
		as.logger.Errorf("failed to create verification request for %s: %v", req.Username, err)
		return nil, status.Errorf(codes.Internal, "failed to submit verification request")
	}

	// Track activity
	clientInfo := as.extractClientInfo(req)
	user.IpAddress, user.Client = utils.IpClientConvert(clientInfo.IPAddress, clientInfo.Client)
	as.trackAuthActivity(user, "request_user_verification", req)

	as.logger.Infof("verification request submitted for user %s (type: %s)", req.Username, req.VerificationType)

	return &pb.RequestUserVerificationRes{
		StatusCode: http.StatusOK,
		Message:    "Verification request submitted successfully, it will be reviewed by our team",
		RequestId:  verReq.ID,
	}, nil
}

// GetUserVerificationStatus returns the verification status for a user.
func (as *AuthzSvc) GetUserVerificationStatus(ctx context.Context, req *pb.GetUserVerificationStatusReq) (*pb.GetUserVerificationStatusRes, error) {
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		return nil, status.Errorf(codes.InvalidArgument, "username is required")
	}

	// Check is_verified flag
	isVerified, err := as.dbConn.IsUserVerified(req.Username)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "user not found")
	}

	res := &pb.GetUserVerificationStatusRes{
		StatusCode:         http.StatusOK,
		IsVerified:         isVerified,
		VerificationStatus: "none",
	}

	// Check for latest verification request
	verReq, err := as.dbConn.GetVerificationRequest(req.Username)
	if err == nil {
		res.VerificationStatus = verReq.Status
		if verReq.ReviewedAt.Valid {
			res.VerifiedAt = verReq.ReviewedAt.Time.Format(time.RFC3339)
		}
	}

	return res, nil
}

// ReviewUserVerification allows an admin to approve or reject a verification request.
func (as *AuthzSvc) ReviewUserVerification(ctx context.Context, req *pb.ReviewUserVerificationReq) (*pb.ReviewUserVerificationRes, error) {
	as.logger.Debugf("review verification request %s by %s", req.RequestId, req.ReviewerUsername)

	if req.RequestId == "" || req.ReviewerUsername == "" {
		return nil, status.Errorf(codes.InvalidArgument, "request_id and reviewer_username are required")
	}
	reviewer, err := as.dbConn.CheckIfUsernameExist(req.ReviewerUsername)
	if err != nil || reviewer == nil || utils.EnsureAccountEnabled(reviewer) != nil || !staffaccess.Has(reviewer.Role, staffaccess.VerificationReview) {
		return nil, status.Error(codes.PermissionDenied, "verification review requires staff access")
	}

	// Get the verification request
	verReq, err := as.dbConn.GetVerificationRequestByID(req.RequestId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "verification request not found")
	}

	if verReq.Status != "pending" {
		return nil, status.Errorf(codes.FailedPrecondition, "verification request has already been reviewed")
	}

	now := time.Now()
	verReq.ReviewerUsername = sql.NullString{String: req.ReviewerUsername, Valid: true}
	verReq.ReviewedAt = sql.NullTime{Time: now, Valid: true}
	verReq.UpdatedAt = now

	if req.Approved {
		verReq.Status = "approved"

		// Set is_verified = true on user_account
		if err := as.dbConn.SetUserVerified(verReq.Username, true); err != nil {
			as.logger.Errorf("failed to set user %s as verified: %v", verReq.Username, err)
			return nil, status.Errorf(codes.Internal, "failed to update verification status")
		}
	} else {
		verReq.Status = "rejected"
		verReq.RejectionReason = sql.NullString{String: req.RejectionReason, Valid: req.RejectionReason != ""}
	}

	if err := as.dbConn.UpdateVerificationRequest(verReq); err != nil {
		as.logger.Errorf("failed to update verification request %s: %v", req.RequestId, err)
		return nil, status.Errorf(codes.Internal, "failed to update verification request")
	}

	action := "rejected"
	if req.Approved {
		action = "approved"
	}

	as.logger.Infof("verification request %s %s for user %s by %s", req.RequestId, action, verReq.Username, req.ReviewerUsername)

	return &pb.ReviewUserVerificationRes{
		StatusCode: http.StatusOK,
		Message:    "Verification request " + action + " successfully",
	}, nil
}
