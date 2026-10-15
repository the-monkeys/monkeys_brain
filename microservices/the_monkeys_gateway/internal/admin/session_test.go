package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	eventpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_event/pb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type sessionEvents struct {
	eventpb.EventServiceClient
	calls int
}

func (s *sessionEvents) AdminListEventPayments(context.Context, *eventpb.AdminListEventPaymentsReq, ...grpc.CallOption) (*eventpb.AdminListEventPaymentsResp, error) {
	s.calls++
	return &eventpb.AdminListEventPaymentsResp{}, nil
}

func staffTestRouter(role, username string, events *sessionEvents) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	staff := r.Group("/api/v1/admin", func(c *gin.Context) {
		c.Set("user_role", role)
		c.Set("userName", username)
		c.Set("accountId", "account-1")
	})
	asc := &AdminServiceClient{Events: events, logger: zap.NewNop().Sugar()}
	registerStaffRoutes(staff, asc, asc.logger)
	return r
}

func TestStaffSessionUsesExplicitIdentityAndPermissions(t *testing.T) {
	for _, role := range []string{"Admin", "Support", "Community", "Viewer", "unknown", ""} {
		t.Run(role, func(t *testing.T) {
			r := staffTestRouter(role, "staff", &sessionEvents{})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil))
			if role == "Viewer" || role == "unknown" || role == "" {
				if w.Code != http.StatusForbidden {
					t.Fatalf("got %d, want 403", w.Code)
				}
				return
			}
			if w.Code != http.StatusOK {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			var got struct {
				Username    string   `json:"username"`
				Role        string   `json:"role"`
				AccountID   string   `json:"account_id"`
				Permissions []string `json:"permissions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Username != "staff" || got.Role != role || got.AccountID != "account-1" || len(got.Permissions) == 0 {
				t.Fatalf("invalid session: %+v", got)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("session can be cached")
			}
			for _, p := range got.Permissions {
				if role != "Admin" && (p == "payments.read" || p == "payments.manage") {
					t.Fatal("nonfinancial role received payment access")
				}
			}
		})
	}
}

func TestStaffSessionRejectsMissingIdentity(t *testing.T) {
	w := httptest.NewRecorder()
	staffTestRouter("Admin", "", &sessionEvents{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", w.Code)
	}
}

func TestRegisteredPaymentRoutesDenyCommunity(t *testing.T) {
	events := &sessionEvents{}
	for _, tc := range []struct {
		role, method, path string
		want               int
	}{
		{"Community", "GET", "/api/v1/admin/payments/events", 403},
		{"Community", "GET", "/api/v1/admin/payments/events/example", 403},
		{"Community", "POST", "/api/v1/admin/payments/events/example/settlements", 403},
		{"Community", "POST", "/api/v1/admin/payments/settlements/1/mark-paid", 403},
		{"Admin", "GET", "/api/v1/admin/payments/events", 200},
	} {
		w := httptest.NewRecorder()
		staffTestRouter(tc.role, "staff", events).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.want {
			t.Errorf("%s %s %s: got %d, want %d", tc.role, tc.method, tc.path, w.Code, tc.want)
		}
	}
	if events.calls != 1 {
		t.Fatalf("payment service called %d times; want only the authorised call", events.calls)
	}
}
