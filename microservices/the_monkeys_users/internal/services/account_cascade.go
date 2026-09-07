package services

import (
	"context"
	"strings"

	eventpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type eventRemovalClient interface {
	CheckUserEventRemoval(ctx context.Context, in *eventpb.AccountIdReq, opts ...grpc.CallOption) (*eventpb.UserRemovalCheckResp, error)
	RemoveUserFromEvents(ctx context.Context, in *eventpb.AccountIdReq, opts ...grpc.CallOption) (*eventpb.BasicResp, error)
}

type groupRemovalClient interface {
	CheckUserGroupRemoval(ctx context.Context, in *grouppb.AccountIdReq, opts ...grpc.CallOption) (*grouppb.UserRemovalCheckResp, error)
	RemoveUserFromGroups(ctx context.Context, in *grouppb.AccountIdReq, opts ...grpc.CallOption) (*grouppb.BasicResp, error)
}

type userRemovalCheck interface {
	GetAllowed() bool
	GetReason() string
	GetBlockingSlugs() []string
}

func errIfRemovalBlocked(kind string, resp userRemovalCheck) error {
	if resp == nil {
		return status.Errorf(codes.Internal, "%s removal check returned an empty response", kind)
	}
	if resp.GetAllowed() {
		return nil
	}
	reason := strings.TrimSpace(resp.GetReason())
	if reason == "" {
		reason = "account still has captured or pending " + kind
	}
	if slugs := resp.GetBlockingSlugs(); len(slugs) > 0 {
		reason = reason + ": " + strings.Join(slugs, ", ")
	}
	return status.Error(codes.FailedPrecondition, reason)
}

func (us *UserSvc) removeUserFromEventsAndGroups(ctx context.Context, accountID string) error {
	if us.events == nil {
		return status.Error(codes.Unavailable, "events service is unavailable; cannot verify paid events")
	}
	if us.groups == nil {
		return status.Error(codes.Unavailable, "groups service is unavailable; cannot verify paid events")
	}

	eventCheck, err := us.events.CheckUserEventRemoval(ctx, &eventpb.AccountIdReq{AccountId: accountID})
	if err != nil {
		return err
	}
	if err := errIfRemovalBlocked("event payments", eventCheck); err != nil {
		return err
	}

	groupCheck, err := us.groups.CheckUserGroupRemoval(ctx, &grouppb.AccountIdReq{AccountId: accountID})
	if err != nil {
		return err
	}
	if err := errIfRemovalBlocked("group paid events", groupCheck); err != nil {
		return err
	}

	if _, err := us.events.RemoveUserFromEvents(ctx, &eventpb.AccountIdReq{AccountId: accountID}); err != nil {
		return err
	}
	if _, err := us.groups.RemoveUserFromGroups(ctx, &grouppb.AccountIdReq{AccountId: accountID}); err != nil {
		return err
	}
	return nil
}
