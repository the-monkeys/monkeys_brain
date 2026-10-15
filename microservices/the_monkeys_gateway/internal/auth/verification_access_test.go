package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type verificationAccessClient struct {
	pb.AuthServiceClient
	calls int
}

func (s *verificationAccessClient) ReviewUserVerification(context.Context, *pb.ReviewUserVerificationReq, ...grpc.CallOption) (*pb.ReviewUserVerificationRes, error) {
	s.calls++
	return &pb.ReviewUserVerificationRes{StatusCode: http.StatusOK}, nil
}

func TestLegacyVerificationReviewRequiresStaffPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		role string
		want int
	}{
		{"Viewer", 403}, {"", 403}, {"Owner", 403}, {"Admin", 200}, {"Support", 200}, {"Community", 200},
	} {
		t.Run(tc.role, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/v1/auth/verification/review", strings.NewReader(`{"request_id":"request-1","approved":true}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("userName", "reviewer")
			c.Set("user_role", tc.role)
			client := &verificationAccessClient{}
			asc := &ServiceClient{Client: client, Log: zap.NewNop().Sugar()}
			asc.ReviewUserVerification(c)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d", w.Code, tc.want)
			}
			if tc.want == 403 && client.calls != 0 {
				t.Fatal("unauthorised review reached the service")
			}
		})
	}
}
