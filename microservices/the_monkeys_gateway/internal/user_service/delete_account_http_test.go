package user_service

import (
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMapDeleteAccountErr(t *testing.T) {
	code, msg := mapDeleteAccountErr(status.Error(codes.NotFound, "missing"))
	if code != http.StatusNotFound || msg != "no user/activity found" {
		t.Fatalf("not found: %d %s", code, msg)
	}
	code, msg = mapDeleteAccountErr(status.Error(codes.FailedPrecondition, "paid event: paid-meetup"))
	if code != http.StatusConflict || msg != "paid event: paid-meetup" {
		t.Fatalf("conflict: %d %s", code, msg)
	}
	code, msg = mapDeleteAccountErr(status.Error(codes.Unavailable, "events service is unavailable; cannot verify paid events"))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable: %d", code)
	}
	code, msg = mapDeleteAccountErr(status.Error(codes.Internal, "boom"))
	if code != http.StatusInternalServerError || msg != "couldn't delete the account" {
		t.Fatalf("internal: %d %s", code, msg)
	}
}
