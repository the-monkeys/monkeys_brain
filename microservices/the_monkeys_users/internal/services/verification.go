package services

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_users/internal/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamp "google.golang.org/protobuf/types/known/timestamppb"
)

func toPbVerificationRequest(v *models.VerificationRequest) *pb.VerificationRequest {
	out := &pb.VerificationRequest{
		Id:               v.Id,
		Username:         v.Username,
		VerificationType: v.VerificationType,
		Country:          v.Country.String,
		IdDocumentType:   v.IDDocumentType.String,
		Status:           v.Status,
		SelfieChecksum:   v.SelfieChecksum.String,
		IdFrontChecksum:  v.IDFrontChecksum.String,
		IdBackChecksum:   v.IDBackChecksum.String,
		AdditionalInfo:   v.AdditionalInfo.String,
		ReviewerUsername: v.ReviewerUsername.String,
		RejectionReason:  v.RejectionReason.String,
	}
	if v.CreatedAt.Valid {
		out.CreatedAt = timestamp.New(v.CreatedAt.Time)
	}
	if v.UpdatedAt.Valid {
		out.UpdatedAt = timestamp.New(v.UpdatedAt.Time)
	}
	if v.ReviewedAt.Valid {
		out.ReviewedAt = timestamp.New(v.ReviewedAt.Time)
	}
	return out
}

// nullString maps an empty API string to SQL NULL so legacy columns stay
// clean instead of accumulating empty strings.
func nullString(s string) sql.NullString {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// validChecksum enforces the storage_assets.checksum shape: exactly 64
// hex characters (SHA-256 fingerprint).
func validChecksum(cs string) bool {
	if len(cs) != 64 {
		return false
	}
	for _, r := range cs {
		hexDigit := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !hexDigit {
			return false
		}
	}
	return true
}

// reviewableStatuses bounds the admin queue filter to real states so a
// typo can't silently become a full-table scan disguised as a filter.
var reviewableStatuses = map[string]struct{}{
	constants.VerificationStatusPending:     {},
	constants.VerificationStatusUnderReview: {},
	constants.VerificationStatusApproved:    {},
	constants.VerificationStatusRejected:    {},
}

// SubmitVerificationRequest persists a new blue-check submission. The
// gateway injects Username from the JWT; documents must already live in
// the private bucket (their checksums are verified against storage_assets).
func (us *UserSvc) SubmitVerificationRequest(ctx context.Context, req *pb.SubmitVerificationReq) (*pb.VerificationRequest, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}

	vType := strings.TrimSpace(req.VerificationType)
	if !constants.IsValidVerificationType(vType) {
		return nil, status.Errorf(codes.InvalidArgument, "verification_type must be %q or %q",
			constants.VerificationTypeSocialProof, constants.VerificationTypeIDDocument)
	}

	info := strings.TrimSpace(req.AdditionalInfo)
	if len(info) > constants.MaxAdditionalInfoLen {
		return nil, status.Errorf(codes.InvalidArgument,
			"additional_info exceeds %d characters", constants.MaxAdditionalInfoLen)
	}

	vr := &models.VerificationRequest{
		Username:         username,
		VerificationType: vType,
		AdditionalInfo:   nullString(info),
		Status:           constants.VerificationStatusPending,
	}

	// Collect + normalize document checksums. Deduplicated so the
	// existence probe compares apples to apples.
	var checksums []string
	seen := make(map[string]struct{}, 3)
	collect := func(raw string) (sql.NullString, error) {
		cs := strings.ToLower(strings.TrimSpace(raw))
		if cs == "" {
			return sql.NullString{}, nil
		}
		if !validChecksum(cs) {
			return sql.NullString{}, status.Error(codes.InvalidArgument,
				"asset checksums must be 64 hex characters")
		}
		if _, dup := seen[cs]; !dup {
			seen[cs] = struct{}{}
			checksums = append(checksums, cs)
		}
		return sql.NullString{String: cs, Valid: true}, nil
	}

	var err error
	if vr.SelfieChecksum, err = collect(req.SelfieChecksum); err != nil {
		return nil, err
	}
	if vr.IDFrontChecksum, err = collect(req.IdFrontChecksum); err != nil {
		return nil, err
	}
	if vr.IDBackChecksum, err = collect(req.IdBackChecksum); err != nil {
		return nil, err
	}

	// Country-scoped rules apply only to the government-ID flow. Unknown
	// countries degrade to passport/residence-permit (see constants).
	if vType == constants.VerificationTypeIDDocument {
		country := strings.ToUpper(strings.TrimSpace(req.Country))
		if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
			return nil, status.Error(codes.InvalidArgument,
				"a 2-letter ISO country code is required for id_document requests")
		}
		docType := strings.TrimSpace(req.IdDocumentType)
		if !constants.IsAllowedIDDocument(country, docType) {
			return nil, status.Errorf(codes.InvalidArgument,
				"document type %q is not accepted for %s", docType, country)
		}
		vr.Country = sql.NullString{String: country, Valid: true}
		vr.IDDocumentType = sql.NullString{String: docType, Valid: true}
		if !vr.SelfieChecksum.Valid || !vr.IDFrontChecksum.Valid {
			return nil, status.Error(codes.InvalidArgument,
				"selfie and id_front uploads are required for id_document requests")
		}
	}

	// Reject orphan pointers early: every referenced checksum must have a
	// storage_assets row from a completed upload.
	if len(checksums) > 0 {
		exist, err := us.dbConn.VerificationAssetsExist(checksums)
		if err != nil {
			us.log.Errorf("submit verification: asset probe failed for %s: %v", username, err)
			return nil, status.Error(codes.Internal, "could not verify uploaded assets")
		}
		if !exist {
			return nil, status.Error(codes.InvalidArgument,
				"one or more referenced assets were not found; upload documents first")
		}
	}

	created, err := us.dbConn.CreateVerificationRequest(vr)
	if err != nil {
		if errors.Is(err, database.ErrActiveVerificationExists) {
			return nil, status.Error(codes.AlreadyExists,
				"you already have an active verification request")
		}
		us.log.Errorf("submit verification request for %s failed: %v", username, err)
		return nil, status.Error(codes.Internal, "could not submit the verification request")
	}
	return toPbVerificationRequest(created), nil
}

// GetMyVerification serves the owner's view: latest submission by default,
// or a specific one when request_id is supplied.
func (us *UserSvc) GetMyVerification(ctx context.Context, req *pb.GetVerificationReq) (*pb.VerificationRequest, error) {
	id := strings.TrimSpace(req.RequestId)
	username := strings.TrimSpace(req.Username)
	if id == "" && username == "" {
		return nil, status.Error(codes.InvalidArgument, "username or request_id is required")
	}

	var (
		v   *models.VerificationRequest
		err error
	)
	if id != "" {
		// Admin path (e.g. presigning reviewer access to documents).
		v, err = us.dbConn.GetVerificationRequestByID(id)
	} else {
		// Owner path.
		v, err = us.dbConn.GetLatestVerificationRequest(username)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "no verification request found")
		}
		us.log.Errorf("get verification request for %s failed: %v", username, err)
		return nil, status.Error(codes.Internal, "could not fetch the verification request")
	}
	return toPbVerificationRequest(v), nil
}

// CancelVerificationRequest lets the owner withdraw a PENDING request;
// anything already under review or decided stays as history.
func (us *UserSvc) CancelVerificationRequest(ctx context.Context, req *pb.GetVerificationReq) (*pb.VerificationActionRes, error) {
	username := strings.TrimSpace(req.Username)
	id := strings.TrimSpace(req.RequestId)
	if username == "" || id == "" {
		return nil, status.Error(codes.InvalidArgument, "username and request_id are required")
	}

	if err := us.dbConn.CancelVerificationRequest(id, username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "no pending verification request found")
		}
		us.log.Errorf("cancel verification request %s for %s failed: %v", id, username, err)
		return nil, status.Error(codes.Internal, "could not cancel the verification request")
	}
	return &pb.VerificationActionRes{
		Status:  "success",
		Message: "verification request cancelled",
	}, nil
}

// ListVerificationRequests backs the admin review queue.
func (us *UserSvc) ListVerificationRequests(ctx context.Context, req *pb.ListVerificationReq) (*pb.ListVerificationRes, error) {
	statusFilter := strings.TrimSpace(req.Status)
	if statusFilter != "" {
		if _, ok := reviewableStatuses[statusFilter]; !ok {
			return nil, status.Errorf(codes.InvalidArgument, "unknown status filter %q", statusFilter)
		}
	}

	items, total, err := us.dbConn.ListVerificationRequests(statusFilter, int(req.Limit), int(req.Offset))
	if err != nil {
		us.log.Errorf("list verification requests failed: %v", err)
		return nil, status.Error(codes.Internal, "could not list verification requests")
	}

	res := &pb.ListVerificationRes{
		Requests: make([]*pb.VerificationRequest, 0, len(items)),
		Total:    int32(total),
	}
	for i := range items {
		res.Requests = append(res.Requests, toPbVerificationRequest(&items[i]))
	}
	return res, nil
}

// ReviewVerificationRequest approves or rejects a submission. Approval
// flips user_account.is_verified inside the same transaction as the state
// change, so a badge can never dangle off a rejected request.
//
// TODO(decision email): notify the user on approval/rejection once the
// transport is chosen (SMTP util vs FreeRangeNotify template) — see
// implementation.md open question #4. Fire-and-forget; never fail the RPC.
func (us *UserSvc) ReviewVerificationRequest(ctx context.Context, req *pb.ReviewVerificationReq) (*pb.VerificationRequest, error) {
	id := strings.TrimSpace(req.RequestId)
	reviewer := strings.TrimSpace(req.ReviewerUsername)
	if id == "" || reviewer == "" {
		return nil, status.Error(codes.InvalidArgument, "request_id and reviewer_username are required")
	}

	reason := strings.TrimSpace(req.RejectionReason)
	if !req.Approve && reason == "" {
		return nil, status.Error(codes.InvalidArgument,
			"rejection_reason is required when rejecting a request")
	}

	updated, err := us.dbConn.ReviewVerificationRequest(id, reviewer, req.Approve, reason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound,
				"request not found or not in a reviewable state")
		}
		us.log.Errorf("review verification request %s by %s failed: %v", id, reviewer, err)
		return nil, status.Error(codes.Internal, "could not record the review decision")
	}

	us.log.Infow("verification reviewed", "request_id", id,
		"username", updated.Username, "decision", updated.Status, "reviewer", reviewer)
	return toPbVerificationRequest(updated), nil
}
