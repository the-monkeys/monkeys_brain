package events

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type cloneGatewayClient struct {
	pb.EventServiceClient
	request *pb.CloneEventReq
}

func (c *cloneGatewayClient) CloneEvent(_ context.Context, req *pb.CloneEventReq, _ ...grpc.CallOption) (*pb.EventResp, error) {
	c.request = req
	return &pb.EventResp{Event: &pb.Event{Slug: "new-draft", Timezone: req.Timezone}}, nil
}

func TestCloneForwardsSelectedTimezoneAndOriginalInstants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, zone := range []string{"Asia/Kolkata", ""} {
		t.Run("choice="+zone, func(t *testing.T) {
			client := &cloneGatewayClient{}
			handler := &EventServiceClient{Client: client, log: zap.NewNop().Sugar()}
			router := gin.New()
			router.POST("/events/:slug/clone", func(ctx *gin.Context) {
				ctx.Set("accountId", "host")
				handler.CloneEvent(ctx)
			})
			choice := ""
			if zone != "" {
				choice = fmt.Sprintf(`,"timezone":%q`, zone)
			}
			body := `{"start_time":"2026-11-12T19:00:12+05:30","end_time":"2026-11-12T20:00:12+05:30"` + choice + `}`
			req := httptest.NewRequest(http.MethodPost, "/events/source/clone", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Timezone", "America/New_York")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusCreated || client.request == nil {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			got := client.request
			if got.Timezone != zone || got.Slug != "source" || got.AccountId != "host" {
				t.Errorf("forwarded clone = %v; want selected timezone %q", got, zone)
			}
			if got.StartTime.AsTime().Format(time.RFC3339) != "2026-11-12T13:30:12Z" || got.EndTime.AsTime().Format(time.RFC3339) != "2026-11-12T14:30:12Z" {
				t.Errorf("forwarded instants changed: %v", got)
			}
		})
	}
}
