package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/constants"
	"go.uber.org/zap"
)

func TestStaffRoleMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	log := zap.NewNop().Sugar()
	staff := r.Group("/api/v1/admin")
	staff.Use(func(c *gin.Context) {
		c.Set("user_role", c.GetHeader("X-Test-Role"))
		c.Next()
	})
	pay := staff.Group("/payments", RequireRole(log, constants.RoleAdmin))
	pay.GET("/events", func(c *gin.Context) { c.Status(http.StatusOK) })
	ver := staff.Group("", RequireRole(log, constants.RoleAdmin, constants.RoleSupport))
	ver.GET("/verifications", func(c *gin.Context) { c.Status(http.StatusOK) })
	ver.GET("/stats", func(c *gin.Context) { c.Status(http.StatusOK) })
	cat := staff.Group("", RequireRole(log, constants.RoleAdmin))
	cat.GET("/users", func(c *gin.Context) { c.Status(http.StatusOK) })
	cat.DELETE("/blogs/:blog_id", func(c *gin.Context) { c.Status(http.StatusOK) })
	cat.DELETE("/events/:slug", func(c *gin.Context) { c.Status(http.StatusOK) })
	cat.DELETE("/groups/:slug", func(c *gin.Context) { c.Status(http.StatusOK) })
	mod := staff.Group("", RequireRole(log, constants.RoleAdmin, constants.RoleCommunity))
	mod.POST("/events/:slug/nsfw", func(c *gin.Context) { c.Status(http.StatusOK) })

	cases := []struct {
		role, method, path string
		want               int
	}{
		{constants.RoleAdmin, http.MethodGet, "/api/v1/admin/payments/events", http.StatusOK},
		{constants.RoleAdmin, http.MethodGet, "/api/v1/admin/stats", http.StatusOK},
		{constants.RoleAdmin, http.MethodGet, "/api/v1/admin/users", http.StatusOK},
		{constants.RoleSupport, http.MethodGet, "/api/v1/admin/stats", http.StatusOK},
		{constants.RoleSupport, http.MethodGet, "/api/v1/admin/users", http.StatusForbidden},
		{constants.RoleCommunity, http.MethodGet, "/api/v1/admin/users", http.StatusForbidden},
		{constants.RoleViewer, http.MethodGet, "/api/v1/admin/stats", http.StatusForbidden},
		{constants.RoleAdmin, http.MethodGet, "/api/v1/admin/verifications", http.StatusOK},
		{constants.RoleAdmin, http.MethodPost, "/api/v1/admin/events/x/nsfw", http.StatusOK},
		{constants.RoleSupport, http.MethodGet, "/api/v1/admin/verifications", http.StatusOK},
		{constants.RoleSupport, http.MethodGet, "/api/v1/admin/payments/events", http.StatusForbidden},
		{constants.RoleSupport, http.MethodPost, "/api/v1/admin/events/x/nsfw", http.StatusForbidden},
		{constants.RoleCommunity, http.MethodPost, "/api/v1/admin/events/x/nsfw", http.StatusOK},
		{constants.RoleCommunity, http.MethodGet, "/api/v1/admin/payments/events", http.StatusForbidden},
		{constants.RoleCommunity, http.MethodGet, "/api/v1/admin/verifications", http.StatusForbidden},
		{constants.RoleViewer, http.MethodGet, "/api/v1/admin/payments/events", http.StatusForbidden},
		{constants.RoleViewer, http.MethodGet, "/api/v1/admin/verifications", http.StatusForbidden},
		{constants.RoleViewer, http.MethodPost, "/api/v1/admin/events/x/nsfw", http.StatusForbidden},
		{constants.RoleAdmin, http.MethodDelete, "/api/v1/admin/blogs/x", http.StatusOK},
		{constants.RoleSupport, http.MethodDelete, "/api/v1/admin/blogs/x", http.StatusForbidden},
		{constants.RoleCommunity, http.MethodDelete, "/api/v1/admin/blogs/x", http.StatusForbidden},
		{constants.RoleViewer, http.MethodDelete, "/api/v1/admin/blogs/x", http.StatusForbidden},
		{constants.RoleAdmin, http.MethodDelete, "/api/v1/admin/events/x", http.StatusOK},
		{constants.RoleSupport, http.MethodDelete, "/api/v1/admin/events/x", http.StatusForbidden},
		{constants.RoleCommunity, http.MethodDelete, "/api/v1/admin/events/x", http.StatusForbidden},
		{constants.RoleAdmin, http.MethodDelete, "/api/v1/admin/groups/x", http.StatusOK},
		{constants.RoleSupport, http.MethodDelete, "/api/v1/admin/groups/x", http.StatusForbidden},
		{constants.RoleViewer, http.MethodDelete, "/api/v1/admin/groups/x", http.StatusForbidden},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("X-Test-Role", tc.role)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%s %s %s: got %d want %d", tc.role, tc.method, tc.path, w.Code, tc.want)
		}
	}
}
