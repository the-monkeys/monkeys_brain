package scheduler

import (
	"context"
	"errors"
	"testing"

	grouppb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_group/pb"
	"google.golang.org/grpc"
)

type fakeGroups struct {
	exists bool
	active bool
	err    error
}

func (f *fakeGroups) Authorize(_ context.Context, _ *grouppb.AuthorizeGroupReq, _ ...grpc.CallOption) (*grouppb.AuthorizeGroupResp, error) {
	if f.err != nil {
		return nil, f.err
	}
	status := "left"
	if f.active {
		status = "active"
	}
	return &grouppb.AuthorizeGroupResp{
		GroupExists:  f.exists,
		IsMember:     f.active,
		MemberStatus: status,
	}, nil
}

func TestMustClearScheduledGroup(t *testing.T) {
	s := &Scheduler{groups: &fakeGroups{exists: true, active: true}}
	got, err := s.mustClearScheduledGroup(context.Background(), "acc", "tea-club")
	if err != nil || got {
		t.Fatalf("active member clear=%v err=%v", got, err)
	}

	s.groups = &fakeGroups{exists: true, active: false}
	got, err = s.mustClearScheduledGroup(context.Background(), "acc", "tea-club")
	if err != nil || !got {
		t.Fatalf("kicked clear=%v err=%v", got, err)
	}

	s.groups = &fakeGroups{exists: false, active: false}
	got, err = s.mustClearScheduledGroup(context.Background(), "acc", "tea-club")
	if err != nil || !got {
		t.Fatalf("missing group clear=%v err=%v", got, err)
	}

	got, err = s.mustClearScheduledGroup(context.Background(), "acc", "")
	if err != nil || got {
		t.Fatalf("no slug clear=%v err=%v", got, err)
	}

	s.groups = nil
	_, err = s.mustClearScheduledGroup(context.Background(), "acc", "tea-club")
	if err == nil {
		t.Fatal("nil groups client must error")
	}

	s.groups = &fakeGroups{err: errors.New("down")}
	_, err = s.mustClearScheduledGroup(context.Background(), "acc", "tea-club")
	if err == nil {
		t.Fatal("authorize failure must error")
	}
}
