package groups

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
)

// Rate tiers for the groups module. Separate limiter instances keep a burst of
// group traffic from draining another module's budget, and give each tier its
// own bucket.
const (
	// rateRead covers browsing: group listings, detail pages, member rosters.
	rateRead = "120-M"
	// rateWrite covers mutations: creating groups, joining, moderating members.
	rateWrite = "30-M"
)

// rateLimit builds a limiter for one tier. Each call gets its own in-memory
// store, so tiers never share a bucket.
//
// The key is the account id when the caller is authenticated, falling back to
// client IP. Keying authenticated traffic per account keeps a shared office
// NAT from throttling itself collectively, while anonymous traffic still gets
// a per-source cap.
func rateLimit(formatted string) gin.HandlerFunc {
	rate, err := limiter.NewRateFromFormatted(formatted)
	if err != nil {
		panic(err) // programmer error: the tier constants are compile-time literals
	}
	instance := limiter.New(memory.NewStore(), rate)

	return func(ctx *gin.Context) {
		key := ctx.GetString("accountId")
		if key == "" {
			key = ctx.ClientIP()
		}

		res, err := instance.Get(ctx, key)
		if err != nil {
			// A limiter fault must not take the API down with it.
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
