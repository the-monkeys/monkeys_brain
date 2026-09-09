package admin

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestIsLocalNetwork(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.8", true},
		{"172.16.4.2", true},
		{"192.168.1.20", true},
		{"::1", true},
		{"fd12:3456::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"100.64.1.1", false},
		{"", false},
		{"not-an-ip", false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if got := isLocalNetwork(ip); got != tc.want {
			t.Errorf("isLocalNetwork(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestLocalNetworkMiddlewareRejectsPublicIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(LocalNetworkMiddleware(zap.NewNop().Sugar()))
	r.GET("/api/v1/admin/stats", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/stats", nil)
	req.RemoteAddr = "8.8.8.8:443"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("public IP got %d, want 403", w.Code)
	}
}

func TestLocalNetworkMiddlewareAllowsPrivateIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(LocalNetworkMiddleware(zap.NewNop().Sugar()))
	r.GET("/api/v1/admin/stats", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/stats", nil)
	req.RemoteAddr = "192.168.1.20:54321"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("private IP got %d, want 200", w.Code)
	}
}
