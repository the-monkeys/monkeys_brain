package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	authpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type diagnosticAuthClient struct {
	authpb.AuthServiceClient
	role, requestID string
	err             error
	calls           int
}

func (a *diagnosticAuthClient) Validate(ctx context.Context, _ *authpb.ValidateRequest, _ ...grpc.CallOption) (*authpb.ValidateResponse, error) {
	a.calls++
	md, _ := metadata.FromOutgoingContext(ctx)
	if ids := md.Get("x-monkeys-staff-request-id"); len(ids) == 1 {
		a.requestID = ids[0]
	}
	if a.err != nil {
		return nil, a.err
	}
	return &authpb.ValidateResponse{UserName: "fixture-staff", AccountId: "fixture-account", Role: a.role}, nil
}

func TestStaffDenialLogsResolvedRoleAndCorrelatesWithAuthz(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zapcore.InfoLevel)
	log := zap.New(core).Sugar()
	rpc := &diagnosticAuthClient{role: "Viewer"}
	middleware := auth.InitAuthMiddleware(&auth.ServiceClient{Client: rpc}, log)
	router := gin.New()
	router.GET("/api/v1/admin/session", middleware.AuthRequired, RequirePermission(log, staffaccess.Access), func(c *gin.Context) { c.Status(http.StatusOK) })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/session?token=query-secret-fixture", nil)
	request.Header.Set("Authorization", "Bearer access-secret-fixture")
	request.Header.Set("Cookie", "mat=cookie-secret-fixture; mrt=refresh-secret-fixture")
	request.Header.Set("X-Admin-Key", "admin-key-secret-fixture")
	request.Header.Set("X-Request-ID", "untrusted-request-secret-fixture")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", response.Code)
	}
	id := response.Header().Get("X-Request-ID")
	if _, err := uuid.Parse(id); err != nil || id != rpc.requestID {
		t.Fatalf("missing shared diagnostic ID: header=%q rpc=%q", id, rpc.requestID)
	}
	entries := logs.FilterLevelExact(zapcore.WarnLevel).All()
	if len(entries) != 1 {
		t.Fatalf("expected one production-visible denial, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	for key, want := range map[string]any{"reason": "permission_denied", "received_role": "Viewer", "required_permission": "staff.access", "route": "/api/v1/admin/session", "staff_request_id": id, "status": int64(403)} {
		if fields[key] != want {
			t.Errorf("%s=%v, want %v", key, fields[key], want)
		}
	}
	encoded, _ := json.Marshal(logs.All())
	for _, secret := range []string{"query-secret-fixture", "access-secret-fixture", "cookie-secret-fixture", "refresh-secret-fixture", "admin-key-secret-fixture", "untrusted-request-secret-fixture"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("sensitive request data leaked into logs")
		}
	}
}

func TestStaffAuthenticationFailuresAreVisibleWithoutLoggingRPCMessages(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing_credentials", false: "authentication_failed"}[missing], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			core, logs := observer.New(zapcore.InfoLevel)
			log := zap.New(core).Sugar()
			rpc := &diagnosticAuthClient{err: status.Error(codes.Unauthenticated, "sensitive-upstream-message-fixture")}
			middleware := auth.InitAuthMiddleware(&auth.ServiceClient{Client: rpc}, log)
			router := gin.New()
			router.GET("/api/v1/admin/session", middleware.AuthRequired, func(c *gin.Context) { c.Status(http.StatusOK) })
			request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil)
			if !missing {
				request.Header.Set("Authorization", "Bearer access-secret-fixture")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d, want 401", response.Code)
			}
			entries := logs.FilterLevelExact(zapcore.WarnLevel).All()
			if len(entries) != 1 {
				t.Fatalf("expected one denial warning, got %d", len(entries))
			}
			if entries[0].ContextMap()["reason"] != map[bool]string{true: "missing_credentials", false: "authentication_failed"}[missing] {
				t.Fatal("denial reason missing")
			}
			fields := entries[0].ContextMap()
			if fields["received_role"] != "unavailable" || fields["required_permission"] != "staff.access" || fields["permission_scope"] != "staff_admission" {
				t.Fatal("authentication warning must distinguish unavailable identity from the minimum staff-admission requirement")
			}
			encoded, _ := json.Marshal(logs.All())
			if strings.Contains(string(encoded), "sensitive-upstream-message-fixture") || strings.Contains(string(encoded), "access-secret-fixture") {
				t.Fatal("authentication secret logged")
			}
			if missing && rpc.calls != 0 {
				t.Fatal("missing authentication must not call authz")
			}
		})
	}
}

func TestAdminPermissionIsStillAllowedWithoutAdminKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zapcore.InfoLevel)
	log := zap.New(core).Sugar()
	rpc := &diagnosticAuthClient{role: "Admin"}
	middleware := auth.InitAuthMiddleware(&auth.ServiceClient{Client: rpc}, log)
	router := gin.New()
	router.GET("/api/v1/admin/session", middleware.AuthRequired, RequirePermission(log, staffaccess.Access), func(c *gin.Context) { c.Status(http.StatusOK) })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil)
	request.Header.Set("Authorization", "Bearer synthetic-access-fixture")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || logs.FilterLevelExact(zapcore.WarnLevel).Len() != 0 {
		t.Fatal("diagnostics changed Admin access")
	}
}
