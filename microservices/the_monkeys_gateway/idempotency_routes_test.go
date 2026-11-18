package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	authpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/events"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/groups"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/user_service"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type creationAuthClient struct{ authpb.AuthServiceClient }

func (creationAuthClient) Validate(context.Context, *authpb.ValidateRequest, ...grpc.CallOption) (*authpb.ValidateResponse, error) {
	return &authpb.ValidateResponse{AccountId: "immutable-account", UserName: "alice"}, nil
}

// Exercise the real route registrations with an intentionally unwired Redis
// store. Authenticated keyed requests must fail before any creation RPC, while
// anonymous requests preserve auth errors and unkeyed bodies keep validation.
func TestCreationRoutesRequireAuthBeforeIdempotency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := &config.Config{}
	log := zap.NewNop().Sugar()
	authClient := &auth.ServiceClient{Client: creationAuthClient{}}
	events.RegisterEventRouter(r, cfg, authClient, nil, log)
	groups.RegisterGroupRouter(r, cfg, authClient, nil, nil, nil, log)
	user_service.RegisterUserRouter(r, cfg, authClient, log, nil)
	for _, path := range []string{"/api/v1/events", "/api/v1/events/series", "/api/v1/events/source/clone", "/api/v1/groups", "/api/v1/groups/team/events", "/api/v1/user/verification"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "same")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 401 || strings.Contains(w.Body.String(), "idempotency_") {
			t.Fatalf("%s reached idempotency before auth: %d %s", path, w.Code, w.Body)
		}
	}
	for _, path := range []string{"/api/v1/events", "/api/v1/events/series", "/api/v1/groups", "/api/v1/user/verification"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`broken-json`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Idempotency-Key", "same")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "idempotency_unavailable") {
			t.Fatalf("%s keyed creation not protected: %d %s", path, w.Code, w.Body)
		}
		req = httptest.NewRequest("POST", path, strings.NewReader(`broken-json`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test-token")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 || strings.Contains(w.Body.String(), "idempotency_") {
			t.Fatalf("%s legacy validation changed: %d %s", path, w.Code, w.Body)
		}
	}
}
