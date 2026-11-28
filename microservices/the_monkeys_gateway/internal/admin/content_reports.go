package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/common/staffaccess"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/moderation"
	"go.uber.org/zap"
)

func (asc *AdminServiceClient) RegisterModerationRoutes(router *gin.Engine, authClient *auth.ServiceClient, policy *moderation.Service, log *zap.SugaredLogger) {
	mw := auth.InitAuthMiddleware(authClient, log)
	staff := router.Group("/api/v1/admin/moderation", LocalNetworkMiddleware(log), mw.AuthRequired, RequirePermission(log, staffaccess.ModerationNSFW))
	staff.GET("/reports", policy.ListReports)
	staff.POST("/reports/:id/review", policy.Review)
}
