package user_service

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type birthdayUpdateClient struct {
	pb.UserServiceClient
	called bool
}

func (c *birthdayUpdateClient) UpdateUserProfile(context.Context, *pb.UpdateUserProfileReq, ...grpc.CallOption) (*pb.UpdateUserProfileRes, error) {
	c.called = true
	return nil, status.Error(codes.InvalidArgument, "date_of_birth must be a valid calendar date")
}
func TestProfileBirthdayInvalidReturns400AndRetainsAuthorization(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		for _, owner := range []bool{true, false} {
			gin.SetMode(gin.TestMode)
			client := &birthdayUpdateClient{}
			svc := &UserServiceClient{Client: client, log: zap.NewNop().Sugar()}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if owner {
					c.Set("userName", "fixture")
				} else {
					c.Set("userName", "other")
				}
				c.Next()
			})
			router.Handle(method, "/user/:id", svc.UpdateUserProfile)
			req := httptest.NewRequest(method, "/user/fixture", strings.NewReader(`{"values":{"date_of_birth":"invalid"}}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			want := http.StatusUnauthorized
			if owner {
				want = http.StatusBadRequest
			}
			if w.Code != want || client.called != owner {
				t.Fatalf("method=%s owner=%v status=%d called=%v", method, owner, w.Code, client.called)
			}
		}
	}
}
