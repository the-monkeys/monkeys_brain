package user_service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	authpb "github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_authz/pb"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_user/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type profilePrivacyAuthClient struct{ authpb.AuthServiceClient }

func (*profilePrivacyAuthClient) Validate(_ context.Context, req *authpb.ValidateRequest, _ ...grpc.CallOption) (*authpb.ValidateResponse, error) {
	switch req.Token {
	case "owner":
		return &authpb.ValidateResponse{UserName: "alice", AccountId: "alice-id", Role: "Viewer", Email: "alice@example.test"}, nil
	case "reader":
		return &authpb.ValidateResponse{UserName: "bob", AccountId: "bob-id", Role: "Viewer"}, nil
	case "admin":
		return &authpb.ValidateResponse{UserName: "staff", AccountId: "staff-id", Role: "Admin"}, nil
	default:
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
}

type profilePrivacyClient struct {
	pb.UserServiceClient
	request *pb.UserProfileReq
	err     error
}

func (f *profilePrivacyClient) GetUserProfile(_ context.Context, req *pb.UserProfileReq, _ ...grpc.CallOption) (*pb.UserProfileRes, error) {
	f.request = req
	if f.err != nil {
		return nil, f.err
	}
	res := &pb.UserProfileRes{Username: req.Username, AccountId: "alice-id", Bio: "Public biography"}
	if req.IsPrivate {
		res.DateOfBirth = "1990-01-02"
	}
	return res, nil
}

func profilePrivacyRouter(client *profilePrivacyClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Header("Vary", "Origin"); c.Next() })
	cfg := &config.Config{}
	cfg.Microservices.TheMonkeysUser = "localhost"
	cfg.Microservices.TheMonkeysActivity = "localhost"
	usc := RegisterUserRouter(router, cfg, &auth.ServiceClient{Client: &profilePrivacyAuthClient{}}, zap.NewNop().Sugar(), nil)
	usc.Client = client
	return router
}

func assertProfilePrivateCache(t *testing.T, header http.Header) {
	t.Helper()
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
}

// Removing the verified-username comparison, trusting request privacy flags,
// or omitting the route cache policy must fail this account matrix.
func TestRegisteredProfilePrivacyIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, path, token string
		private, public   bool
	}{
		{"self", "/api/v1/user/alice", "owner", true, false},
		{"self cannot suppress private selection", "/api/v1/user/alice?is_private=false", "owner", true, false},
		{"other reader", "/api/v1/user/alice", "reader", false, false},
		{"admin ordinary read", "/api/v1/user/alice", "admin", false, false},
		{"reader privacy injection", "/api/v1/user/alice?is_private=true&IsPrivate=true", "reader", false, false},
		{"admin privacy injection", "/api/v1/user/alice?is_private=true", "admin", false, false},
		{"anonymous public", "/api/v1/user/public/alice?is_private=true", "", false, true},
		{"owner public", "/api/v1/user/public/alice?is_private=true", "owner", false, true},
		{"reader public", "/api/v1/user/public/alice?is_private=true", "reader", false, true},
		{"admin public", "/api/v1/user/public/alice?is_private=true", "admin", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &profilePrivacyClient{}
			router := profilePrivacyRouter(client)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.token != "" {
				req.AddCookie(&http.Cookie{Name: "mat", Value: tc.token})
			}
			req.Header.Set("Is-Private", "true")
			req.Header.Set("Is_Private", "true")
			req.Header.Set("X-Is-Private", "true")
			req.Header.Set("Username", "alice")
			req.Header.Set("X-User-Role", "Admin")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			wantStatus := http.StatusOK
			if tc.public {
				wantStatus = http.StatusAccepted
			}
			if w.Code != wantStatus {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			if client.request == nil || client.request.Username != "alice" || client.request.IsPrivate != tc.private || client.request.Email != "" {
				t.Fatalf("unexpected upstream profile scope: %+v", client.request)
			}
			var profile map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
				t.Fatal(err)
			}
			if tc.private {
				if profile["date_of_birth"] != "1990-01-02" {
					t.Error("self profile lost DOB")
				}
			} else if _, exists := profile["date_of_birth"]; exists {
				t.Error("DOB leaked to non-private profile")
			}
			if _, exists := profile["email"]; exists {
				t.Error("account email leaked into profile")
			}
			if tc.public {
				if got := w.Result().Header.Get("Cache-Control"); got != "" {
					t.Errorf("public profile cache policy changed: %q", got)
				}
				if got := w.Result().Header.Get("Vary"); got != "Origin" {
					t.Errorf("public Vary changed: %q", got)
				}
			} else {
				assertProfilePrivateCache(t, w.Result().Header)
			}
		})
	}
}

func TestRegisteredPrivateProfileFailuresNoStore(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		err         error
		want        int
	}{
		{"anonymous", "", nil, 401},
		{"invalid session", "invalid", nil, 401},
		{"missing profile", "owner", status.Error(codes.NotFound, "missing"), 404},
		{"service failure", "owner", status.Error(codes.Unavailable, "unavailable"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &profilePrivacyClient{err: tc.err}
			router := profilePrivacyRouter(client)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/user/alice", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			assertProfilePrivateCache(t, w.Result().Header)
			if tc.want == 401 && client.request != nil {
				t.Fatal("unauthenticated request reached profile RPC")
			}
		})
	}
}

func TestRegisteredPublicSearchKeepsCachePolicy(t *testing.T) {
	router := profilePrivacyRouter(&profilePrivacyClient{})
	// Validation fails before any RPC; this still exercises actual public registration.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/user/search", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Result().Header.Get("Cache-Control"); got != "" {
		t.Errorf("public search cache policy changed: %q", got)
	}
	if got := w.Result().Header.Get("Vary"); got != "Origin" {
		t.Errorf("public search Vary changed: %q", got)
	}
}
