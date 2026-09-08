package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/constants"
	"go.uber.org/zap"
)

func TestRequireRoleAllowsListedRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_role", constants.RoleSupport)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	RequireRole(zap.NewNop().Sugar(), constants.RoleAdmin, constants.RoleSupport)(c)
	if c.IsAborted() {
		t.Fatalf("support should pass Admin|Support, got abort status %d", w.Code)
	}
}

func TestRequireRoleRejectsViewer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_role", constants.RoleViewer)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	RequireRole(zap.NewNop().Sugar(), constants.RoleAdmin)(c)
	if !c.IsAborted() {
		t.Fatal("viewer must be forbidden")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestRequireRoleAllowsCommunityForModeration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_role", constants.RoleCommunity)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	RequireRole(zap.NewNop().Sugar(), constants.RoleAdmin, constants.RoleCommunity)(c)
	if c.IsAborted() {
		t.Fatalf("community should pass Admin|Community, got abort status %d", w.Code)
	}
}

func TestRequireRoleRejectsCommunityOnPayments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_role", constants.RoleCommunity)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	RequireRole(zap.NewNop().Sugar(), constants.RoleAdmin)(c)
	if !c.IsAborted() || w.Code != http.StatusForbidden {
		t.Fatalf("community must be forbidden on Admin-only, status %d aborted=%v", w.Code, c.IsAborted())
	}
}
