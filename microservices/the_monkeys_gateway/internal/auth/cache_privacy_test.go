package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type cachePrivacyAuthClient struct{ pb.AuthServiceClient }

func (*cachePrivacyAuthClient) DecodeSignedJWT(_ context.Context, req *pb.DecodeSignedJWTRequest, _ ...grpc.CallOption) (*pb.DecodeSignedJWTResponse, error) {
	if req.Token != "valid" {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return &pb.DecodeSignedJWTResponse{}, nil
}

func TestRegisteredAuthResponsesNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Header("Vary", "Origin"); c.Next() })
	cfg := &config.Config{}
	cfg.Microservices.TheMonkeysAuthz = "localhost"
	asc := RegisterAuthRouter(router, cfg, zap.NewNop().Sugar())
	asc.Client = &cachePrivacyAuthClient{}
	for _, tc := range []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"anonymous session", "GET", "/api/v1/auth/validate-session", "", "", 401},
		{"invalid session", "GET", "/api/v1/auth/validate-session", "invalid", "", 401},
		{"valid session", "GET", "/api/v1/auth/validate-session", "valid", "", 200},
		{"anonymous auth check", "GET", "/api/v1/auth/is-authenticated", "", "", 401},
		{"anonymous refresh", "POST", "/api/v1/auth/refresh", "", "", 401},
		{"logout", "GET", "/api/v1/auth/logout", "", "", 200},
		{"malformed login", "POST", "/api/v1/auth/login", "", "{", 400},
		{"malformed registration", "POST", "/api/v1/auth/register", "", "{", 400},
		{"anonymous settings", "PUT", "/api/v1/auth/settings/username/alice", "", "{}", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.AddCookie(&http.Cookie{Name: "mat", Value: tc.token})
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.want, w.Body.String())
			}
			header := w.Result().Header
			if got := header.Get("Cache-Control"); got != "private, no-store" {
				t.Errorf("Cache-Control = %q, want private, no-store", got)
			}
			vary := strings.ToLower(strings.Join(header.Values("Vary"), ","))
			for _, field := range []string{"origin", "cookie", "authorization"} {
				found := false
				for _, token := range strings.Split(vary, ",") {
					if strings.TrimSpace(token) == field {
						found = true
					}
				}
				if !found {
					t.Errorf("Vary = %q, missing %s", vary, field)
				}
			}
		})
	}
}
