package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFailRPCMapsFailedPreconditionToConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	asc := &AdminServiceClient{logger: zap.NewNop().Sugar()}
	if !asc.failRPC(c, status.Error(codes.FailedPrecondition, "nothing to settle"), "settle") {
		t.Fatal("expected failRPC to write an error")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestQueryLimitOffsetClamps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?limit=500&offset=-3", nil)
	limit, offset := queryLimitOffset(c)
	if limit != 20 || offset != 0 {
		t.Fatalf("limit=%d offset=%d", limit, offset)
	}
}
