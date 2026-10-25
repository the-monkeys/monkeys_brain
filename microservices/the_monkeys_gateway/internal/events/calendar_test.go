package events

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type calendarGatewayClient struct {
	pb.EventServiceClient
	request  *pb.CalendarReq
	includes bool
}

func (c *calendarGatewayClient) GetCalendarFile(_ context.Context, req *pb.CalendarReq, _ ...grpc.CallOption) (*pb.CalendarResp, error) {
	c.request = req
	return &pb.CalendarResp{IcsData: []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"), IncludesSeries: c.includes}, nil
}

func TestCalendarDefaultKeepsDownloadAndForwardsTrustedViewer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, viewer := range []string{"", "confirmed-viewer"} {
		client := &calendarGatewayClient{}
		handler := &EventServiceClient{Client: client, log: zap.NewNop().Sugar()}
		router := gin.New()
		router.GET("/events/:slug/calendar", func(ctx *gin.Context) { ctx.Set("accountId", viewer); handler.DownloadCalendar(ctx) })
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/events/local/calendar?account_id=forged", nil))
		if response.Code != http.StatusOK || client.request == nil {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
		if client.request.AccountId != viewer {
			t.Errorf("viewer=%q; want trusted viewer %q", client.request.AccountId, viewer)
		}
		if client.request.EventSlug != "local" || response.Header().Get("Content-Disposition") != `attachment; filename="local.ics"` {
			t.Fatal("legacy download changed")
		}
		if response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("viewer-specific export must not be shared in a cache")
		}
	}
}

func TestCalendarRejectsUnknownScopeBeforeCallingService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &calendarGatewayClient{}
	handler := &EventServiceClient{Client: client, log: zap.NewNop().Sugar()}
	router := gin.New()
	router.GET("/events/:slug/calendar", handler.DownloadCalendar)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/events/local/calendar?scope=everything", nil))
	if response.Code != http.StatusBadRequest || client.request != nil {
		t.Fatalf("status=%d, request=%v", response.Code, client.request)
	}
}

func TestCalendarSeriesRequiresAcknowledgmentFromEventService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, supportsSeries := range []bool{false, true} {
		client := &calendarGatewayClient{includes: supportsSeries}
		handler := &EventServiceClient{Client: client, log: zap.NewNop().Sugar()}
		router := gin.New()
		router.GET("/events/:slug/calendar", handler.DownloadCalendar)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/events/local/calendar?scope=series", nil))
		expected := http.StatusNotImplemented
		if supportsSeries {
			expected = http.StatusOK
		}
		if response.Code != expected || client.request == nil || !client.request.IncludeSeries {
			t.Fatalf("status=%d request=%v", response.Code, client.request)
		}
		if supportsSeries && response.Header().Get("X-Monkeys-Calendar-Scope") != "series" {
			t.Fatal("missing series acknowledgment")
		}
	}
}
