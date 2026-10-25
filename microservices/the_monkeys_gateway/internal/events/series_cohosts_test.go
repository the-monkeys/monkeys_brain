package events

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type seriesCoHostClient struct {
	pb.EventServiceClient
	request *pb.CreateSeriesReq
}

func (c *seriesCoHostClient) CreateSeries(_ context.Context, request *pb.CreateSeriesReq, _ ...grpc.CallOption) (*pb.EventResp, error) {
	// Exercise the protobuf boundary too: the additive field must survive wire encoding.
	wire, err := proto.Marshal(request)
	if err != nil {
		return nil, err
	}
	c.request = &pb.CreateSeriesReq{}
	if err := proto.Unmarshal(wire, c.request); err != nil {
		return nil, err
	}
	return &pb.EventResp{Event: &pb.Event{Slug: "first-occurrence"}}, nil
}

func TestCreateSeriesForwardsSelectedCoHosts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &seriesCoHostClient{}
	handler := &EventServiceClient{Client: client, log: zap.NewNop().Sugar()}
	router := gin.New()
	router.POST("/events/series", func(ctx *gin.Context) {
		ctx.Set("accountId", "owner-account")
		handler.CreateSeries(ctx)
	})
	request := httptest.NewRequest(http.MethodPost, "/events/series", strings.NewReader(`{
		"title":"Weekly meetup","event_type":"virtual","timezone":"Asia/Kolkata",
		"start_time":"2099-11-12T19:00:12+05:30","end_time":"2099-11-12T20:00:12+05:30",
		"co_host_usernames":["guest"," @other "],"recurrence":{"freq":"weekly","count":3},
		"requires_host_review":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || client.request == nil {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	got := client.request
	if !reflect.DeepEqual(got.CoHostUsernames, []string{"guest", " @other "}) {
		t.Fatalf("forwarded co-hosts = %v", got.CoHostUsernames)
	}
	if got.AccountId != "owner-account" || got.Timezone != "Asia/Kolkata" ||
		got.StartTime.AsTime().Format("2006-01-02T15:04:05Z07:00") != "2099-11-12T13:30:12Z" ||
		got.Recurrence.Count != 3 || !got.RequiresHostReview {
		t.Fatalf("series fields changed: %v", got)
	}
}
