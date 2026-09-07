package services

import (
	"strings"
	"testing"

	eventpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrIfRemovalBlocked(t *testing.T) {
	if err := errIfRemovalBlocked("event payments", &eventpb.UserRemovalCheckResp{Allowed: true}); err != nil {
		t.Fatalf("allowed should pass: %v", err)
	}
	err := errIfRemovalBlocked("event payments", &eventpb.UserRemovalCheckResp{
		Allowed:       false,
		Reason:        "account still has captured or pending event payments; cancel or settle them first",
		BlockingSlugs: []string{"paid-meetup"},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", status.Code(err))
	}
	if !strings.Contains(err.Error(), "paid-meetup") {
		t.Fatalf("want blocking slug in message, got %v", err)
	}
	if err := errIfRemovalBlocked("event payments", nil); status.Code(err) != codes.Internal {
		t.Fatalf("nil resp should be Internal, got %v", status.Code(err))
	}
}

func TestRemoveUserFromEventsAndGroupsFailsClosedWithoutClients(t *testing.T) {
	us := &UserSvc{}
	if err := us.removeUserFromEventsAndGroups(nil, "acc-1"); status.Code(err) != codes.Unavailable {
		t.Fatalf("want Unavailable when events/groups clients are missing, got %v", status.Code(err))
	}
}
