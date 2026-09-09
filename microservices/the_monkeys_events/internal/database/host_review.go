package database

import (
	"net/url"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const RSVPPendingHostReview = "pending_host_review"

const errNoSeatsToApprove = "no seats left to approve"
const errHostReviewSeries = "apply to each date separately when the host reviews guests"

func decideApplyStatus(requiresReview, full bool, amount float64) (status string, due float64) {
	if requiresReview {
		return RSVPPendingHostReview, 0
	}
	switch {
	case full:
		return RSVPWaitlisted, 0
	case amount > 0:
		return RSVPPendingPayment, amount
	default:
		return RSVPConfirmed, 0
	}
}

func decideApproveStatus(full bool, amount float64) (string, float64, error) {
	if full {
		return "", 0, status.Error(codes.FailedPrecondition, errNoSeatsToApprove)
	}
	if amount > 0 {
		return RSVPPendingPayment, amount, nil
	}
	return RSVPConfirmed, 0, nil
}

func refuseSeriesHostReview(requiresReview bool) error {
	if requiresReview {
		return status.Error(codes.FailedPrecondition, errHostReviewSeries)
	}
	return nil
}

func rsvpAlreadyRecorded(status string) bool {
	switch status {
	case RSVPConfirmed, RSVPWaitlisted, RSVPPendingHostReview:
		return true
	default:
		return false
	}
}

func couponHoldsBudget(status string) bool {
	return status != RSVPWaitlisted && status != RSVPPendingHostReview
}

func rsvpFreesNoSeat(status string) bool {
	return status == RSVPWaitlisted || status == RSVPPendingHostReview
}

func parseReviewDecision(raw string) (approve bool, err error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "approve":
		return true, nil
	case "reject":
		return false, nil
	default:
		return false, status.Error(codes.InvalidArgument, "decision must be approve or reject")
	}
}

func validateSocialProofURL(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return status.Error(codes.InvalidArgument, "a public profile URL is required")
	}
	if len(s) > 2048 {
		return status.Error(codes.InvalidArgument, "profile URL is too long")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return status.Error(codes.InvalidArgument, "profile URL must be a valid http(s) link")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return status.Error(codes.InvalidArgument, "profile URL must be a valid http(s) link")
	}
	return nil
}
