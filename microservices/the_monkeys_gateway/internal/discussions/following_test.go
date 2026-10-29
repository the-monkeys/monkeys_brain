package discussions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_discussion/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type followingSvc struct {
	pb.DiscussionServiceClient
	req         *pb.ListDiscussionsReq
	acknowledge bool
}

func (s *followingSvc) ListDiscussions(_ context.Context, in *pb.ListDiscussionsReq, _ ...grpc.CallOption) (*pb.ListDiscussionsResp, error) {
	s.req = in
	return &pb.ListDiscussionsResp{FollowingOnly: s.acknowledge, Discussions: []*pb.Discussion{{PublicId: "p1"}}}, nil
}

func TestFollowingEndpointProtectsItsScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, account string
		ack           bool
		code          int
	}{
		{"guest", "", true, 401}, {"signed in", "viewer", true, 200}, {"old service", "viewer", false, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &followingSvc{acknowledge: tc.ack}
			c := &Client{svc: svc, log: zap.NewNop().Sugar()}
			r := gin.New()
			r.GET("/following", func(ctx *gin.Context) { ctx.Set("accountId", tc.account) }, c.ListFollowingDiscussions)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/following?before_id=cursor", nil))
			if w.Code != tc.code {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.code, w.Body.String())
			}
			if tc.account == "" {
				if svc.req != nil {
					t.Fatal("guest reached service")
				}
				return
			}
			if svc.req == nil || !svc.req.GetFollowingOnly() || svc.req.GetAccountId() != "viewer" || svc.req.GetBeforePublicId() != "cursor" || svc.req.GetGroupSlug() != "" {
				t.Fatalf("request=%v", svc.req)
			}
		})
	}
}
