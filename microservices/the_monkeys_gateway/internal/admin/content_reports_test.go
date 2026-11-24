package admin

import (
	"context"
	"github.com/gin-gonic/gin"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http/httptest"
	"strings"
	"testing"
)

type failedModeration struct{ pb.ModerationServiceClient }

func (failedModeration) ReportContent(context.Context, *pb.ActionRequest, ...grpc.CallOption) (*pb.ActionResponse, error) {
	return nil, status.Error(codes.Internal, "secret database/DOB detail")
}
func TestLegacyNSFWAdapterSanitizesFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	asc := &AdminServiceClient{Moderation: failedModeration{}, logger: zap.NewNop().Sugar()}
	r.POST("/blogs/:blog_id/nsfw", asc.FlagNsfw("blog"))
	req := httptest.NewRequest("POST", "/blogs/post/nsfw", strings.NewReader(`{"reason":"review"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code < 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
}
