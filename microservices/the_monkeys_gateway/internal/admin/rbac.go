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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if !staffaccess.Has(c.GetString("user_role"), permission) {
			if log != nil {
				log.Debugw("staff permission refused", "permission", permission)
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
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
