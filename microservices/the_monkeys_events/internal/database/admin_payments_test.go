package database

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCannotUnpublishWithPaid(t *testing.T) {
	if !cannotUnpublishWithPaid(1) {
		t.Fatal("paid attendees must block unpublish")
	}
	if cannotUnpublishWithPaid(0) {
		t.Fatal("zero paid should allow unpublish")
	}
}

func TestErrNothingToSettle(t *testing.T) {
	if err := errNothingToSettle(0); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("open 0: %v", err)
	}
	if err := errNothingToSettle(-1); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("open negative: %v", err)
	}
	if err := errNothingToSettle(1); err != nil {
		t.Fatal(err)
	}
}
