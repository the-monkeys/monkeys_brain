package discussions

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_gateway/internal/auth"
	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
	"go.uber.org/zap"
)

const (
	rateRead  = "120-M"
	rateWrite = "30-M"
)

// RegisterDiscussionRouter mounts discussion reads and writes.
// Public reads stay optional so a stranger gets 404, not 401, on a private post.
func RegisterDiscussionRouter(router *gin.Engine, cfg *config.Config, authClient *auth.ServiceClient, lg *zap.SugaredLogger) {
	mware := auth.InitAuthMiddleware(authClient, lg)
	c := &Client{svc: dial(cfg, lg), log: lg}

	read := router.Group("/api/v1/discussions", rateLimit(rateRead))
	read.GET("", mware.AuthOptional, c.ListDiscussions)
	read.GET("/:id", mware.AuthOptional, c.GetDiscussion)

	write := router.Group("/api/v1/discussions", mware.AuthRequired, rateLimit(rateWrite))
	write.POST("", c.CreateDiscussion)
	write.PUT("/:id", c.EditDiscussion)
	write.DELETE("/:id", c.DeleteDiscussion)
	write.POST("/:id/hide", c.HideDiscussion)
	write.POST("/:id/replies", c.ReplyToDiscussion)
	write.POST("/:id/like", c.LikeDiscussion)

	groupRead := router.Group("/api/v1/groups/:slug/discussions", rateLimit(rateRead))
	groupRead.GET("", mware.AuthOptional, c.ListGroupDiscussions)

	groupWrite := router.Group("/api/v1/groups/:slug/discussions", mware.AuthRequired, rateLimit(rateWrite))
	groupWrite.POST("", c.CreateGroupDiscussion)
}

func rateLimit(formatted string) gin.HandlerFunc {
	rate, err := limiter.NewRateFromFormatted(formatted)
	if err != nil {
		panic(err)
	}
	instance := limiter.New(memory.NewStore(), rate)
	return func(ctx *gin.Context) {
		key := ctx.GetString("accountId")
		if key == "" {
			key = ctx.ClientIP()
		}
		res, err := instance.Get(ctx, key)
		if err != nil {
			ctx.Next()
			return
		}
		ctx.Header("X-RateLimit-Limit", strconv.FormatInt(res.Limit, 10))
		ctx.Header("X-RateLimit-Remaining", strconv.FormatInt(res.Remaining, 10))
		ctx.Header("X-RateLimit-Reset", strconv.FormatInt(res.Reset, 10))
		if res.Reached {
			ctx.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"message": "too many requests, please slow down",
			})
			return
		}
		ctx.Next()
	}
}
