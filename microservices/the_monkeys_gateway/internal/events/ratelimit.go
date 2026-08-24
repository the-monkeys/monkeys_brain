package events

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
)

// Rate tiers for the events module. These are deliberately separate instances
// from the shared gateway limiter so a burst of event traffic cannot consume
// the blog feed's budget, and so each tier gets its own bucket.
const (
	// rateRead covers anonymous browsing: listings, detail pages, comments.
	rateRead = "120-M"
	// rateWrite covers mutations. Generous enough for a host editing tiers in
	// a tight loop, tight enough that comment spam needs many accounts.
	rateWrite = "30-M"
	// rateCostly covers endpoints that fan out into repeated service calls or
	// large responses: the paginated CSV export and .ics generation.
	rateCostly = "10-M"
	// rateWebhook is per-IP and sized for Razorpay's retry behaviour. It only
	// exists to cap how many HMAC verifications a flood can force.
	rateWebhook = "600-M"
)

// rateLimit builds a limiter for one tier. Each call gets its own in-memory
// store, so tiers never share a bucket.
//
// The key is the account id when the caller is already authenticated, falling
// back to client IP. Keying authenticated traffic per account keeps a shared
// office NAT from throttling itself collectively, while anonymous traffic
// still gets a per-source cap.
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
