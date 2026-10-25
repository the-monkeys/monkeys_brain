package authx

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type coHostAuthClient struct {
	pb.EventServiceClient
	role  string
	calls int
}

type blockingHostAuthClient struct {
	pb.EventServiceClient
	started, release chan struct{}
}

func (c *blockingHostAuthClient) Authorize(_ context.Context, r *pb.AuthorizeReq, _ ...grpc.CallOption) (*pb.AuthorizeResp, error) {
	if r.EventSlug == "slow-event" {
		close(c.started)
		<-c.release
	}
	return &pb.AuthorizeResp{EventExists: true, EventStatus: "published", Role: "viewer"}, nil
}

func TestCoHostInvalidationDoesNotWaitForOtherEvents(t *testing.T) {
	client := &blockingHostAuthClient{started: make(chan struct{}), release: make(chan struct{})}
	g := NewGuard(client, zap.NewNop().Sugar())
	finished := make(chan struct{})
	go func() { defer close(finished); _, _ = g.resolve(&gin.Context{}, "guest", "slow-event") }()
	<-client.started
	defer func() { close(client.release); <-finished }()
	invalidated := make(chan struct{})
	go func() { g.InvalidateEvent("different-event"); close(invalidated) }()
	select {
	case <-invalidated:
	case <-time.After(time.Second):
		t.Fatal("a co-host change blocked behind an unrelated authorization RPC")
	}
}

type staleHostAuthClient struct {
	pb.EventServiceClient
	calls            atomic.Int32
	started, release chan struct{}
}

func (c *staleHostAuthClient) Authorize(context.Context, *pb.AuthorizeReq, ...grpc.CallOption) (*pb.AuthorizeResp, error) {
	role := "viewer"
	if c.calls.Add(1) == 1 {
		close(c.started)
		<-c.release
		role = "co_host"
	}
	return &pb.AuthorizeResp{EventExists: true, EventStatus: "published", Role: role}, nil
}

func TestCoHostOldInFlightLookupCannotRefillFreshStanding(t *testing.T) {
	client := &staleHostAuthClient{started: make(chan struct{}), release: make(chan struct{})}
	g := NewGuard(client, zap.NewNop().Sugar())
	finished := make(chan struct{})
	go func() { defer close(finished); _, _ = g.resolve(&gin.Context{}, "guest", "local-event") }()
	<-client.started
	g.InvalidateEvent("local-event")
	fresh, err := g.resolve(&gin.Context{}, "guest", "local-event")
	close(client.release)
	<-finished
	if err != nil || fresh.Role != "viewer" {
		t.Fatalf("fresh=%+v err=%v", fresh, err)
	}
	cached, err := g.resolve(&gin.Context{}, "guest", "local-event")
	if err != nil || cached.Role != "viewer" || client.calls.Load() != 2 {
		t.Fatalf("cached=%+v calls=%d err=%v", cached, client.calls.Load(), err)
	}
}

func (c *coHostAuthClient) Authorize(context.Context, *pb.AuthorizeReq, ...grpc.CallOption) (*pb.AuthorizeResp, error) {
	c.calls++
	return &pb.AuthorizeResp{EventExists: true, EventStatus: "published", Role: c.role}, nil
}
func TestCoHostChangeInvalidatesCachedStanding(t *testing.T) {
	client := &coHostAuthClient{role: "viewer"}
	g := NewGuard(client, zap.NewNop().Sugar())
	ctx := &gin.Context{}
	first, err := g.resolve(ctx, "guest", "local-event")
	if err != nil || first.Role != "viewer" {
		t.Fatalf("grant=%+v err=%v", first, err)
	}
	invalidator, ok := any(g).(interface{ InvalidateEvent(string) })
	if !ok {
		t.Fatal("co-host changes need immediate grant invalidation, not a stale 15 second permission result")
	}
	client.role = "co_host"
	invalidator.InvalidateEvent("local-event")
	next, err := g.resolve(ctx, "guest", "local-event")
	if err != nil || next.Role != "co_host" {
		t.Fatalf("grant=%+v err=%v", next, err)
	}
	client.role = "viewer"
	invalidator.InvalidateEvent("local-event")
	last, err := g.resolve(ctx, "guest", "local-event")
	if err != nil || last.Role != "viewer" || client.calls != 3 {
		t.Fatalf("grant=%+v calls=%d err=%v", last, client.calls, err)
	}
}
