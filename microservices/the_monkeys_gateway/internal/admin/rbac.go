package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

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
