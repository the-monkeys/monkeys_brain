package events

import (
	"context"
	"github.com/gin-gonic/gin"
	pb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_moderation/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http/httptest"
	"strings"
	"testing"
)

type failedModeration struct{ pb.ModerationServiceClient }

func (failedModeration) ReportContent(context.Context, *pb.ActionRequest, ...grpc.CallOption) (*pb.ActionResponse, error) {
	return nil, status.Error(codes.InvalidArgument, "secret DOB detail")
}
func TestLegacyReportAdapterSanitizesFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	esc := &EventServiceClient{Moderation: failedModeration{}}
	r.POST("/events/:slug/report", esc.ReportEvent)
	req := httptest.NewRequest("POST", "/events/event/report", strings.NewReader(`{"reason":"nsfw"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 400 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
}
