package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"go.uber.org/zap"
)

func RequirePermission(log *zap.SugaredLogger, permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if c.GetString("userName") == "" || c.GetString("accountId") == "" {
			logPermissionDenial(c, log, permission, "missing_identity", http.StatusUnauthorized)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if !staffaccess.Has(c.GetString("user_role"), permission) {
			logPermissionDenial(c, log, permission, "permission_denied", http.StatusForbidden)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

func logPermissionDenial(c *gin.Context, log *zap.SugaredLogger, permission, reason string, statusCode int) {
	if log == nil {
		return
	}
	log.Warnw("staff permission denied", "reason", reason, "status", statusCode,
		"staff_request_id", c.GetString(staffaccess.RequestIDContextKey),
		"account_id", c.GetString("accountId"), "received_role", c.GetString("user_role"),
		"required_permission", permission, "method", c.Request.Method, "route", c.FullPath())
}

// RequireRole admits callers whose platform role (from JWT Validate) is in
// allowed. It does not hit the database; AuthRequired must have already run.
func RequireRole(log *zap.SugaredLogger, allowed ...string) gin.HandlerFunc {
	allow := make(map[string]struct{}, len(allowed))
	for _, r := range allowed {
		allow[r] = struct{}{}
	}
	return func(c *gin.Context) {
		role := c.GetString("user_role")
		if _, ok := allow[role]; !ok {
			if log != nil {
				log.Debugw("staff role refused", "role", role)
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}
