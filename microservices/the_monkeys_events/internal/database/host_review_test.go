package database

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDecideApplyStatusHostReviewIgnoresCapacityAndPrice(t *testing.T) {
	status, due := decideApplyStatus(true, true, 500)
	if status != RSVPPendingHostReview || due != 0 {
		t.Fatalf("host review apply: status=%s due=%v", status, due)
	}
	status, due = decideApplyStatus(true, false, 0)
	if status != RSVPPendingHostReview || due != 0 {
		t.Fatalf("unpaid host review apply: status=%s due=%v", status, due)
	}
}

func TestDecideApplyStatusOpenEventUnchanged(t *testing.T) {
	status, due := decideApplyStatus(false, true, 0)
	if status != RSVPWaitlisted || due != 0 {
		t.Fatalf("full open event: status=%s due=%v", status, due)
	}
	status, due = decideApplyStatus(false, false, 250)
	if status != RSVPPendingPayment || due != 250 {
		t.Fatalf("paid open event: status=%s due=%v", status, due)
	}
	status, due = decideApplyStatus(false, false, 0)
	if status != RSVPConfirmed || due != 0 {
		t.Fatalf("free open event: status=%s due=%v", status, due)
	}
}

func TestDecideApproveStatusPaidAndUnpaid(t *testing.T) {
	got, due, err := decideApproveStatus(false, 400)
	if err != nil || got != RSVPPendingPayment || due != 400 {
		t.Fatalf("paid approve: status=%s due=%v err=%v", got, due, err)
	}
	got, due, err = decideApproveStatus(false, 0)
	if err != nil || got != RSVPConfirmed || due != 0 {
		t.Fatalf("unpaid approve: status=%s due=%v err=%v", got, due, err)
	}
	_, _, err = decideApproveStatus(true, 0)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("full event approve want FailedPrecondition, got %v", err)
	}
}

func TestPendingPaymentRetrySkipsHostReview(t *testing.T) {
	if effectiveHostReview(true, RSVPPendingPayment) {
		t.Fatal("approved paid guests must retry checkout, not re-enter host review")
	}
	if !effectiveHostReview(true, RSVPCancelled) {
		t.Fatal("a cancelled guest applying again still needs host review")
	}
	if effectiveHostReview(false, "") {
		t.Fatal("open events stay open")
	}
}

func TestRefuseSeriesHostReview(t *testing.T) {
	if err := refuseSeriesHostReview(false); err != nil {
		t.Fatal(err)
	}
	if err := refuseSeriesHostReview(true); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
}

func TestHostReviewDoesNotHoldSeatOrCoupon(t *testing.T) {
	if rsvpAlreadyRecorded(RSVPPendingPayment) {
		t.Fatal("pending_payment is a retryable hold, not a finished apply")
	}
	if !rsvpAlreadyRecorded(RSVPPendingHostReview) {
		t.Fatal("pending_host_review is already applied")
	}
	if couponHoldsBudget(RSVPPendingHostReview) {
		t.Fatal("host-review apply must not consume coupon budget")
	}
	if !couponHoldsBudget(RSVPPendingPayment) {
		t.Fatal("pending_payment must hold coupon budget")
	}
	if !rsvpFreesNoSeat(RSVPPendingHostReview) {
		t.Fatal("cancelling a host-review apply must not promote the waitlist")
	}
}

func TestParseReviewDecision(t *testing.T) {
	ok, err := parseReviewDecision("approve")
	if err != nil || !ok {
		t.Fatalf("approve: ok=%v err=%v", ok, err)
	}
	ok, err = parseReviewDecision("REJECT")
	if err != nil || ok {
		t.Fatalf("reject: ok=%v err=%v", ok, err)
	}
	if _, err := parseReviewDecision("maybe"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid decision: %v", err)
	}
}

func TestValidateSocialProofURL(t *testing.T) {
	if err := validateSocialProofURL("https://linkedin.com/in/ada"); err != nil {
		t.Fatal(err)
	}
	if err := validateSocialProofURL("http://instagram.com/ada"); err != nil {
		t.Fatal(err)
	}
	if err := validateSocialProofURL(""); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty URL: %v", err)
	}
	if err := validateSocialProofURL("javascript:alert(1)"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("javascript URL: %v", err)
	}
	if err := validateSocialProofURL("not a url"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("garbage URL: %v", err)
	}
}
