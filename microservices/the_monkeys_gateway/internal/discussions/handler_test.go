package discussions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_discussion/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGetDiscussionHidesMissingAsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := &Client{
		svc: &fakeSvc{err: status.Error(codes.NotFound, "discussion not found")},
		log: zap.NewNop().Sugar(),
	}
	r := gin.New()
	r.GET("/api/v1/discussions/:id", c.GetDiscussion)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/discussions/secret", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "discussion not found") {
		t.Fatalf("body %s", w.Body.String())
	}
}

func TestCreateDiscussionSendsAccountAndFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeSvc{discussion: &pb.Discussion{PublicId: "abc", Body: "hello"}}
	c := &Client{svc: fake, log: zap.NewNop().Sugar()}
	r := gin.New()
	r.POST("/api/v1/discussions", func(ctx *gin.Context) {
		ctx.Set("accountId", "acc-1")
		ctx.Next()
	}, c.CreateDiscussion)

	body := `{"body":"hello","audience":"group_only","files":[{"storage_key":"discussions/abc/0","content_type":"image/png"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/discussions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if fake.create == nil || fake.create.GetAccountId() != "acc-1" || fake.create.GetBody() != "hello" || fake.create.GetAudience() != "group_only" {
		t.Fatalf("create = %+v", fake.create)
	}
	if len(fake.create.GetFiles()) != 1 || fake.create.GetFiles()[0].GetContentType() != "image/png" {
		t.Fatalf("files = %+v", fake.create.GetFiles())
	}
}

func TestHideDiscussionMarksTheStaffAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeSvc{discussion: &pb.Discussion{PublicId: "abc", Status: "hidden"}}
	c := &Client{svc: fake, log: zap.NewNop().Sugar()}
	r := gin.New()
	r.POST("/api/v1/discussions/:id/hide", func(ctx *gin.Context) {
		ctx.Set("accountId", "staff")
		ctx.Next()
	}, c.HideDiscussion)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/discussions/abc/hide", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if fake.action != "hide" || fake.deleted.GetAccountId() != "staff" || fake.deleted.GetPublicId() != "abc" {
		t.Fatalf("action=%q req=%+v", fake.action, fake.deleted)
	}
}

type fakeSvc struct {
	pb.DiscussionServiceClient
	err        error
	discussion *pb.Discussion
	create     *pb.CreateDiscussionReq
	deleted    *pb.DiscussionActionReq
	action     string
}

func (f *fakeSvc) CreateDiscussion(_ context.Context, in *pb.CreateDiscussionReq, _ ...grpc.CallOption) (*pb.DiscussionResp, error) {
	f.create = in
	if f.err != nil {
		return nil, f.err
	}
	return &pb.DiscussionResp{Discussion: f.discussion}, nil
}

func (f *fakeSvc) DeleteDiscussion(ctx context.Context, in *pb.DiscussionActionReq, _ ...grpc.CallOption) (*pb.DiscussionResp, error) {
	f.deleted = in
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if vals := md.Get(discussionActionHeader); len(vals) > 0 {
			f.action = vals[0]
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &pb.DiscussionResp{Discussion: f.discussion}, nil
}

func (f *fakeSvc) GetDiscussion(context.Context, *pb.DiscussionActionReq, ...grpc.CallOption) (*pb.DiscussionResp, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &pb.DiscussionResp{Discussion: f.discussion}, nil
}
